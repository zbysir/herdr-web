package transcript

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func store(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s := &Store{
		ClaudeRoot: filepath.Join(dir, "claude", "projects"),
		CodexRoot:  filepath.Join(dir, "codex", "sessions"),
		cache:      map[string]cacheEnt{},
	}
	os.MkdirAll(s.ClaudeRoot, 0o700)
	os.MkdirAll(s.CodexRoot, 0o700)
	return s
}

func touch(t *testing.T, path string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o700)
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

const uuid = "b2490162-4d9b-4f41-a0a0-39789154c279"

// 按 id 找：变形拼中那条快路。
func TestFindByIDSlug(t *testing.T) {
	s := store(t)
	want := filepath.Join(s.ClaudeRoot, "-Users-bysir-dev-bysir-herdr-web", uuid+".jsonl")
	touch(t, want)
	got, err := s.Find(Ref{Agent: "claude", Kind: "id", Value: uuid, CWD: "/Users/bysir/dev/bysir/herdr-web"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != want {
		t.Fatalf("找错了：%s", got.Path)
	}
}

// **拼不中也得找到。** claude 的目录名变形规则是别人的（实测点号之类也折成 `-`），
// 照抄那条规则就是给自己埋一个静默坑；session id 全机唯一，所以 glob 一层是正路。
// 这个用例刻意让 cwd 和目录名对不上 —— 变形错了的话这儿就会失败，而生产上的表现是
// 「chat 永远打不开」，报错只会说文件不存在。
func TestFindByIDGlobWhenSlugMisses(t *testing.T) {
	s := store(t)
	want := filepath.Join(s.ClaudeRoot, "-Users-bysir-creght-cn-site", uuid+".jsonl")
	touch(t, want)
	got, err := s.Find(Ref{Agent: "claude", Kind: "id", Value: uuid, CWD: "/Users/bysir/creght.cn/site"})
	if err != nil {
		t.Fatalf("变形拼不中就该 glob：%v", err)
	}
	if got.Path != want {
		t.Fatalf("找错了：%s", got.Path)
	}
}

// codex 的 session id 就在文件名里，按后缀 glob 三层日期目录。
func TestFindByIDCodex(t *testing.T) {
	s := store(t)
	want := filepath.Join(s.CodexRoot, "2026", "09", "21", "rollout-2026-09-21T11-18-41-"+uuid+".jsonl")
	touch(t, want)
	got, err := s.Find(Ref{Agent: "codex", Kind: "id", Value: uuid, CWD: "/w"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != want {
		t.Fatalf("找错了：%s", got.Path)
	}
}

// **id 要过正则。** 它会拼进路径和 glob，`../` 一放进去就是任意文件读取 ——
// 而这个服务是从公网点得到的。
func TestFindRejectsBadID(t *testing.T) {
	s := store(t)
	outside := filepath.Join(filepath.Dir(s.ClaudeRoot), "..", "secret.jsonl")
	touch(t, outside)
	for _, bad := range []string{
		"../../secret",
		"../secret",
		"a/b",
		"..",
		".credentials",
		"",
		strings.Repeat("x", 200),
	} {
		if _, err := s.Find(Ref{Agent: "claude", Kind: "id", Value: bad, CWD: "/w"}); err == nil {
			t.Fatalf("这个 id 该被挡掉：%q", bad)
		}
	}
}

// **`kind:"path"` 必须落在根底下，而且必须是 .jsonl。**
//
// 那个路径是 hook 报上来的、herdr 只转发不校验，也就是说「相信 herdr」等于「相信任何能连上
// 那个 socket 的东西」。少了 .jsonl 那条，一个被改过的 hook 报 `~/.claude/.credentials.json`
// 就能把凭据渲染到页面上 —— 那个文件真在 `~/.claude` 底下。
func TestFindPathMustBeUnderRoot(t *testing.T) {
	s := store(t)
	inside := filepath.Join(s.ClaudeRoot, "-w", "ok.jsonl")
	touch(t, inside)
	if _, err := s.Find(Ref{Agent: "claude", Kind: "path", Value: inside}); err != nil {
		t.Fatalf("根底下的该放过：%v", err)
	}

	home := filepath.Dir(filepath.Dir(s.ClaudeRoot)) // <tmp>/claude
	creds := filepath.Join(home, ".credentials.json")
	touch(t, creds)
	escape := filepath.Join(s.ClaudeRoot, "..", "..", "etc", "passwd.jsonl")
	touch(t, escape)

	// **这几个都得先真建出来。** 不建的话它们是被「文件不存在」挡掉的，不是被边界
	// 挡掉的 —— 那样的测试在边界检查被删掉之后照样会过，等于没测。
	outside := filepath.Join(home, "other.jsonl")
	upOne := filepath.Join(filepath.Dir(s.ClaudeRoot), "up.jsonl")
	touch(t, outside)
	touch(t, upOne)

	for _, bad := range []string{
		creds,   // 在根底下，但不是 .jsonl（`~/.claude/.credentials.json` 就是这种）
		escape,  // 用 .. 穿出去
		outside, // 根外面
		upOne,   // 就在根上面一层
		"",
	} {
		if _, err := s.Find(Ref{Agent: "claude", Kind: "path", Value: bad}); err == nil {
			t.Fatalf("这个路径该被挡掉：%q", bad)
		}
	}
}

// 没报过会话 + 同一个 cwd 下好几个 agent pane：**一律不猜**。
// 猜错的表现是「chat 里显示的是隔壁那个 pane 的对话」，而两边都在同一个项目里干活，
// 屏幕上看着完全正常 —— 实测这台机器上 herdr-web 这个目录就正开着两个 claude pane。
func TestFindAmbiguousRefusesToGuess(t *testing.T) {
	s := store(t)
	touch(t, filepath.Join(s.ClaudeRoot, "-w", "one.jsonl"))
	_, err := s.Find(Ref{Agent: "claude", CWD: "/w", Siblings: 2})
	if !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("该报 ErrAmbiguous，实际：%v", err)
	}
	// 只有一个的时候才猜。
	if _, err := s.Find(Ref{Agent: "claude", CWD: "/w", Siblings: 1}); err != nil {
		t.Fatalf("只有一个 pane 时该猜出来：%v", err)
	}
}

// 猜 claude：同一个目录下取**最近改过**的那一份。
func TestGuessClaudeNewest(t *testing.T) {
	s := store(t)
	dir := filepath.Join(s.ClaudeRoot, "-w")
	old := filepath.Join(dir, "old.jsonl")
	fresh := filepath.Join(dir, "fresh.jsonl")
	touch(t, old)
	touch(t, fresh)
	// 把 old 的时间往前拨，别指望创建顺序 —— 同一秒内建出来的两份 mtime 可能一样。
	past := mustTime(t, old)
	os.Chtimes(old, past, past)

	got, err := s.Find(Ref{Agent: "claude", CWD: "/w", Siblings: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != fresh {
		t.Fatalf("该取最近那份，实际取了 %s", filepath.Base(got.Path))
	}
	// 猜出来的 sig 用的是路径而不是 session id —— 这条路上「换了会话」的表现正是换了一份文件。
	if !strings.HasPrefix(got.Sig, "path:") {
		t.Fatalf("猜出来的 sig 该带 path: 前缀，实际 %q", got.Sig)
	}
}

// 猜 codex：rollout 不按项目分目录，只能读每份的头一行比 cwd。
func TestGuessCodexByCWD(t *testing.T) {
	s := store(t)
	meta := func(cwd string) string {
		return `{"timestamp":"2026-09-21T03:00:00.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"x","cwd":"` + cwd + `"}}` + "\n"
	}
	a := filepath.Join(s.CodexRoot, "2026", "09", "20", "rollout-2026-09-20T10-00-00-aaa.jsonl")
	b := filepath.Join(s.CodexRoot, "2026", "09", "21", "rollout-2026-09-21T10-00-00-bbb.jsonl")
	os.MkdirAll(filepath.Dir(a), 0o700)
	os.MkdirAll(filepath.Dir(b), 0o700)
	os.WriteFile(a, []byte(meta("/w")), 0o600)
	os.WriteFile(b, []byte(meta("/other")), 0o600)

	got, err := s.Find(Ref{Agent: "codex", CWD: "/w", Siblings: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != a {
		t.Fatalf("该挑 cwd 对得上那份，实际 %s", filepath.Base(got.Path))
	}
	if _, err := s.Find(Ref{Agent: "codex", CWD: "/nobody", Siblings: 1}); !errors.Is(err, ErrNoSession) {
		t.Fatalf("没有对得上的该报 ErrNoSession，实际 %v", err)
	}
}

// 认不出的 agent 一律 ErrUnsupported，别去 stat 一堆不存在的目录。
func TestFindUnsupportedAgent(t *testing.T) {
	s := store(t)
	if _, err := s.Find(Ref{Agent: "gemini", Kind: "id", Value: uuid}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("该报 ErrUnsupported，实际 %v", err)
	}
}

// 缓存里那份文件没了要自己作废 —— 会话被删 / 目录被清之后不能一直报「有」。
func TestFindCacheInvalidatesOnDelete(t *testing.T) {
	s := store(t)
	p := filepath.Join(s.ClaudeRoot, "-w", uuid+".jsonl")
	touch(t, p)
	ref := Ref{Agent: "claude", Kind: "id", Value: uuid, CWD: "/w"}
	if _, err := s.Find(ref); err != nil {
		t.Fatal(err)
	}
	os.Remove(p)
	if _, err := s.Find(ref); err == nil {
		t.Fatal("文件删了之后还从缓存里报「有」")
	}
}

// mustTime 把一个文件的 mtime 往前拨一小时用的基准。
func mustTime(t *testing.T, p string) time.Time {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi.ModTime().Add(-time.Hour)
}

// **零值的 Store 必须能用。** 两个根是导出字段，直接用结构体字面量建是正常用法
// （这个包的测试和 internal/server 的测试都这么建），而那时 cache 是 nil map ——
// 往 nil map 里写是 panic，而它发生在一个 HTTP handler 里。踩过一次，钉在这儿。
func TestZeroValueStoreDoesNotPanic(t *testing.T) {
	dir := t.TempDir()
	s := &Store{ClaudeRoot: filepath.Join(dir, "p")} // 注意：没有 cache
	p := filepath.Join(s.ClaudeRoot, "-w", uuid+".jsonl")
	touch(t, p)
	got, err := s.Find(Ref{Agent: "claude", Kind: "id", Value: uuid, CWD: "/w"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != p {
		t.Fatalf("找错了：%s", got.Path)
	}
	// 再来一次走缓存那条分支。
	if _, err := s.Find(Ref{Agent: "claude", Kind: "id", Value: uuid, CWD: "/w"}); err != nil {
		t.Fatal(err)
	}
}

// nil 接收者要答 false：HERDR_WEB_CHAT=0 时 Store 压根不建，而 /api/state 照旧会问一句。
// 少了这道就是「关掉 chat 之后打开页面直接 500」。
func TestNilStoreEnabledIsFalse(t *testing.T) {
	var s *Store
	if s.Enabled() {
		t.Fatal("nil Store 不该说 Enabled")
	}
}

// 会话身份有了、文件还没有（刚开的 agent，还没说第一句）：必须能认出是 ErrNoTranscript ——
// 前端靠这个把「还没对话」画成空状态，而不是一条「找不到会话 xxx 的转录文件」的红字。
func TestFindByIDNoTranscriptYet(t *testing.T) {
	s := store(t)
	for _, agent := range []string{"claude", "codex"} {
		_, err := s.Find(Ref{Agent: agent, Kind: "id", Value: uuid, CWD: "/Users/bysir/dev/bysir/videomake"})
		if !errors.Is(err, ErrNoTranscript) {
			t.Errorf("%s：该是 ErrNoTranscript，拿到 %v", agent, err)
		}
	}
}
