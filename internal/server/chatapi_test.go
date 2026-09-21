package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/zbysir/herdr-web/internal/config"
	"github.com/zbysir/herdr-web/internal/herdr"
	"github.com/zbysir/herdr-web/internal/outbox"
	"github.com/zbysir/herdr-web/internal/transcript"
)

// 这几条盯的是 HTTP 那一层 + **pane → 会话 → 文件**那一跳，因为那一跳是这条路上唯一
// 「错了不报错」的地方：定位到隔壁那个 pane 的对话，屏幕上看着完全正常。
// 转录怎么解析在 internal/transcript 那边测。
//
// 用假 herdr socket：真机上验不到这一步（得正好有个 agent 在跑，而挨个去戳用户正在用的
// agent 不合适），而这儿要的协议只有 `pane.get` 和 `pane.list` 两句话。

// sentIn 假 herdr 收到的一次 pane.send_input。
//
// **Text 也要记**：`start` 那个口的要点正是「发出去的是哪几个字」（白名单里那个命令名），
// 只比按键的话「发了个 enter」就算过，而真正该盯的是没发出别的东西。
type sentIn struct {
	Pane string
	Text string
	Keys []string
}

// sentKeys 记假 herdr 收到的 pane.send_input（一键作答 / 开 agent 那两条路发的东西）。
type sentKeys struct {
	mu   sync.Mutex
	logs []sentIn
}

func (s *sentKeys) add(in sentIn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logs = append(s.logs, in)
}

func (s *sentKeys) all() []sentIn {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sentIn{}, s.logs...)
}

// fakeHerdr 起一个只会答 pane.get / pane.list / pane.send_input 的假 herdr。
func fakeHerdr(t *testing.T, panes []map[string]any) string {
	return fakeHerdrKeys(t, panes, nil)
}

func fakeHerdrKeys(t *testing.T, panes []map[string]any, keys *sentKeys) string {
	t.Helper()
	// 放 /tmp 而不是 t.TempDir()：unix socket 路径有 104 字节上限，macOS 的 TempDir
	// 前缀又长又深（和 internal/agentwatch 那边同一个理由）。
	dir, err := os.MkdirTemp("/tmp", "chatapi")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "h.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				// **一个连接一个请求**（herdr 就是这个语义，见 docs/dev/HERDR-API.md）。
				line, err := bufio.NewReader(c).ReadBytes('\n')
				if err != nil {
					return
				}
				var req struct {
					ID     string         `json:"id"`
					Method string         `json:"method"`
					Params map[string]any `json:"params"`
				}
				json.Unmarshal(line, &req)
				var result any
				switch req.Method {
				case "pane.get":
					want, _ := req.Params["pane_id"].(string)
					for _, p := range panes {
						if p["pane_id"] == want {
							result = map[string]any{"pane": p}
						}
					}
					if result == nil {
						json.NewEncoder(c).Encode(map[string]any{"id": req.ID, "error": map[string]any{"message": "no such pane"}})
						return
					}
				case "pane.list":
					result = map[string]any{"panes": panes}
				case "pane.send_input":
					pid, _ := req.Params["pane_id"].(string)
					var ks []string
					if arr, ok := req.Params["keys"].([]any); ok {
						for _, x := range arr {
							if str, ok := x.(string); ok {
								ks = append(ks, str)
							}
						}
					}
					txt, _ := req.Params["text"].(string)
					if keys != nil {
						keys.add(sentIn{Pane: pid, Text: txt, Keys: ks})
					}
					result = map[string]any{}
				default:
					json.NewEncoder(c).Encode(map[string]any{"id": req.ID, "error": map[string]any{"message": "unexpected " + req.Method}})
					return
				}
				json.NewEncoder(c).Encode(map[string]any{"id": req.ID, "result": result})
			}(conn)
		}
	}()
	return sock
}

// chatServer 攒一个只够跑 apiChat 的 Server：假 herdr + 指到临时目录的转录根。
func chatServer(t *testing.T, on bool, panes []map[string]any) (*Server, *transcript.Store) {
	s, st, _ := chatServerKeys(t, on, panes)
	return s, st
}

func chatServerKeys(t *testing.T, on bool, panes []map[string]any) (*Server, *transcript.Store, *sentKeys) {
	t.Helper()
	keys := &sentKeys{}
	sock := fakeHerdrKeys(t, panes, keys)
	roots := t.TempDir()
	st := &transcript.Store{
		ClaudeRoot: filepath.Join(roots, "claude", "projects"),
		CodexRoot:  filepath.Join(roots, "codex", "sessions"),
	}
	os.MkdirAll(st.ClaudeRoot, 0o700)
	s := &Server{
		Cfg:  &config.Config{Chat: on},
		sess: map[string]*live{},
	}
	s.def = &live{socket: sock, outbox: &outbox.Outbox{C: herdr.New(sock)}}
	if on {
		s.Chat = st
	}
	return s, st, keys
}

func getChat(t *testing.T, s *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("GET", path, nil)
	w := httptest.NewRecorder()
	seg := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api"), "/"), "/")
	s.apiChat(w, r, seg)
	return w
}

// writeClaude 在转录根里摆一份 claude 的会话。
func writeClaude(t *testing.T, st *transcript.Store, slug, id string, texts ...string) {
	t.Helper()
	dir := filepath.Join(st.ClaudeRoot, slug)
	os.MkdirAll(dir, 0o700)
	var b strings.Builder
	for i, txt := range texts {
		line, _ := json.Marshal(map[string]any{
			"type": "user", "uuid": "u" + itoa(i), "timestamp": "2026-09-21T02:00:00.000Z",
			"message": map[string]any{"role": "user", "content": txt},
		})
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

func decodeLog(t *testing.T, w *httptest.ResponseRecorder) *transcript.Log {
	t.Helper()
	if w.Code != 200 {
		t.Fatalf("状态码 %d：%s", w.Code, w.Body.String())
	}
	var l transcript.Log
	if err := json.Unmarshal(w.Body.Bytes(), &l); err != nil {
		t.Fatalf("解析响应：%v（%s）", err, w.Body.String())
	}
	return &l
}

const sid = "b2490162-4d9b-4f41-a0a0-39789154c279"

// 正常那条：herdr 报了会话 id → 找到文件 → 读成对话流。
func TestChatLogByReportedSession(t *testing.T) {
	panes := []map[string]any{{
		"pane_id": "w9:p1E", "agent": "claude", "cwd": "/w",
		"agent_session": map[string]any{"source": "herdr:claude", "agent": "claude", "kind": "id", "value": sid},
	}}
	s, st := chatServer(t, true, panes)
	writeClaude(t, st, "-w", sid, "第一句", "第二句")

	l := decodeLog(t, getChat(t, s, "/api/chat/log?pane=w9:p1E"))
	if len(l.Msgs) != 2 || l.Msgs[0].Text != "第一句" || l.Msgs[1].Text != "第二句" {
		t.Fatalf("对话流不对：%+v", l.Msgs)
	}
	if l.Sig != sid {
		t.Fatalf("sig 该是会话 id，实际 %q", l.Sig)
	}
	if l.Agent != "claude" {
		t.Fatalf("agent 不对：%q", l.Agent)
	}
	// **File 只能是文件名，不是全路径** —— 全路径没必要送到浏览器上。
	if strings.Contains(l.File, "/") {
		t.Fatalf("File 漏了全路径：%q", l.File)
	}
}

// 增量：带上一拍的 Next 再问，只拿到新增那条。
func TestChatLogIncremental(t *testing.T) {
	panes := []map[string]any{{
		"pane_id": "p1", "agent": "claude", "cwd": "/w",
		"agent_session": map[string]any{"kind": "id", "value": sid, "agent": "claude"},
	}}
	s, st := chatServer(t, true, panes)
	writeClaude(t, st, "-w", sid, "第一句")
	first := decodeLog(t, getChat(t, s, "/api/chat/log?pane=p1"))

	writeClaude(t, st, "-w", sid, "第一句", "第二句")
	inc := decodeLog(t, getChat(t, s, "/api/chat/log?pane=p1&from="+itoa(int(first.Next))))
	if len(inc.Msgs) != 1 || inc.Msgs[0].Text != "第二句" {
		t.Fatalf("增量该只有第二句：%+v", inc.Msgs)
	}
}

// **`from` 是坏的一律当 0（整份重来），不报错。** 前端手上那个值可能来自上一个版本的
// 响应，为这个把面板打不开不值当。
func TestChatLogBadFromFallsBackToFull(t *testing.T) {
	panes := []map[string]any{{
		"pane_id": "p1", "agent": "claude", "cwd": "/w",
		"agent_session": map[string]any{"kind": "id", "value": sid, "agent": "claude"},
	}}
	s, st := chatServer(t, true, panes)
	writeClaude(t, st, "-w", sid, "就一句")
	for _, bad := range []string{"", "abc", "-5", "99999999999999999999"} {
		l := decodeLog(t, getChat(t, s, "/api/chat/log?pane=p1&from="+bad))
		if len(l.Msgs) != 1 {
			t.Fatalf("from=%q 时该整份重来，实际 %d 条", bad, len(l.Msgs))
		}
	}
}

// **同一个 cwd 下两个 claude pane 且都没报会话：不猜，报 ambiguous。**
// 这是这条路上最危险的一种错 —— 猜错就是「显示的是隔壁那个 pane 的对话」，
// 而两边都在同一个项目里干活，屏幕上看着完全正常。实测这台机器上 herdr-web 这个目录
// 就正开着两个 claude pane。
func TestChatLogRefusesToGuessWhenAmbiguous(t *testing.T) {
	panes := []map[string]any{
		{"pane_id": "p1", "agent": "claude", "cwd": "/w"},
		{"pane_id": "p2", "agent": "claude", "cwd": "/w"},
	}
	s, st := chatServer(t, true, panes)
	writeClaude(t, st, "-w", sid, "隔壁那个 pane 的对话")

	w := getChat(t, s, "/api/chat/log?pane=p1")
	if w.Code != 409 {
		t.Fatalf("该是 409，实际 %d：%s", w.Code, w.Body.String())
	}
	var out struct{ Reason string }
	json.Unmarshal(w.Body.Bytes(), &out)
	if out.Reason != "ambiguous" {
		t.Fatalf("reason 该是 ambiguous，实际 %q（界面要靠它说清为什么不猜）", out.Reason)
	}
	if strings.Contains(w.Body.String(), "隔壁") {
		t.Fatal("不该把猜出来的那份内容带出去")
	}
}

// 只有一个 pane 时才退回猜 —— 这是没装 hook 的那些 pane 唯一能用上 chat 的路。
func TestChatLogGuessesWhenAlone(t *testing.T) {
	panes := []map[string]any{{"pane_id": "p1", "agent": "claude", "cwd": "/w"}}
	s, st := chatServer(t, true, panes)
	writeClaude(t, st, "-w", sid, "猜对了")
	l := decodeLog(t, getChat(t, s, "/api/chat/log?pane=p1"))
	if len(l.Msgs) != 1 || l.Msgs[0].Text != "猜对了" {
		t.Fatalf("该猜出来：%+v", l.Msgs)
	}
	// 猜出来的 sig 带 path: 前缀，前端据此知道「这是猜的」。
	if !strings.HasPrefix(l.Sig, "path:") {
		t.Fatalf("猜出来的 sig 该带 path: 前缀，实际 %q", l.Sig)
	}
}

// 报了会话但文件还没落地（agent 刚起来、第一条还没写）—— 要报得出来，别 500。
func TestChatLogMissingFile(t *testing.T) {
	panes := []map[string]any{{
		"pane_id": "p1", "agent": "claude", "cwd": "/w",
		"agent_session": map[string]any{"kind": "id", "value": sid, "agent": "claude"},
	}}
	s, _ := chatServer(t, true, panes)
	if w := getChat(t, s, "/api/chat/log?pane=p1"); w.Code != 400 {
		t.Fatalf("该是 400，实际 %d：%s", w.Code, w.Body.String())
	}
}

// 各种说不通的入参。
func TestChatLogBadRequests(t *testing.T) {
	panes := []map[string]any{
		{"pane_id": "shell", "cwd": "/w"},                  // 普通 shell，没有 agent
		{"pane_id": "gem", "agent": "gemini", "cwd": "/w"}, // 还不支持的 agent
		{"pane_id": "p1", "agent": "claude", "cwd": "/w"},  //
	}
	s, _ := chatServer(t, true, panes)
	cases := []struct {
		path string
		code int
		why  string
	}{
		{"/api/chat/log", 400, "不带 pane"},
		{"/api/chat/log?pane=shell", 400, "这个 pane 里没有 agent"},
		{"/api/chat/log?pane=gem", 404, "还不支持的 agent"},
		{"/api/chat/log?pane=nope", 400, "没有这个 pane"},
		{"/api/chat/nope?pane=p1", 404, "没有这个接口"},
		{"/api/chat?pane=p1", 404, "接口名都没给"},
	}
	for _, c := range cases {
		if w := getChat(t, s, c.path); w.Code != c.code {
			t.Errorf("%s：该是 %d，实际 %d（%s）", c.why, c.code, w.Code, strings.TrimSpace(w.Body.String()))
		}
	}
}

// **关掉之后真的关上了。** 和 diff 那条同一个做法：Store 压根不建，口一律 404。
// 「点开一片报错比没有这个入口更糟」（docs/dev/TUI-VS-GUI.md §3 第 5 问）。
func TestChatDisabled(t *testing.T) {
	panes := []map[string]any{{
		"pane_id": "p1", "agent": "claude", "cwd": "/w",
		"agent_session": map[string]any{"kind": "id", "value": sid, "agent": "claude"},
	}}
	s, _ := chatServer(t, false, panes)
	if w := getChat(t, s, "/api/chat/log?pane=p1"); w.Code != 404 {
		t.Fatalf("关掉之后该 404，实际 %d", w.Code)
	}
}

// 转录根底下没有那两个目录（这台机器上既没装 claude 也没装 codex）→ Enabled 为假 → 404。
func TestChatEnabledNeedsARoot(t *testing.T) {
	dir := t.TempDir()
	st := &transcript.Store{
		ClaudeRoot: filepath.Join(dir, "nope", "claude"),
		CodexRoot:  filepath.Join(dir, "nope", "codex"),
	}
	if st.Enabled() {
		t.Fatal("两个根都不在时不该说 Enabled")
	}
}

// **状态跟对话同一拍给。** 它是 chat 模式里唯一能说出「agent 正在干活」的东西 ——
// 转录按「一次 API 请求」flush，agent 想事情时文件一个字节都不动（实测 15.58 秒），
// 那段时间对话流完全静止，没有这一档看着像卡住了（用户报的）。
//
// 服务端这边是**白拿**的：这个口每拍本来就调了 pane.get，agent_status 就在手上。
// 另开一条轮询是在跑着 agent 的那台机器上多敲一遍 herdr，而且两条轮询节奏不一样，
// 会出现「对话更新了但状态还是上一拍的」。
func TestChatLogCarriesStatus(t *testing.T) {
	for _, st := range []string{"working", "blocked", "idle", "done"} {
		panes := []map[string]any{{
			"pane_id": "p1", "agent": "claude", "cwd": "/w", "agent_status": st,
			"agent_session": map[string]any{"kind": "id", "value": sid, "agent": "claude"},
		}}
		s, store := chatServer(t, true, panes)
		writeClaude(t, store, "-w", sid, "一句")
		var out struct {
			Status string `json:"status"`
			Pane   string `json:"pane"`
		}
		w := getChat(t, s, "/api/chat/log?pane=p1")
		if w.Code != 200 {
			t.Fatalf("%s：%d %s", st, w.Code, w.Body.String())
		}
		json.Unmarshal(w.Body.Bytes(), &out)
		if out.Status != st {
			t.Errorf("status 该是 %q，实际 %q", st, out.Status)
		}
		// pane 要回一遍：前端换 pane 时上一拍的响应可能后到，靠它认出来丢掉 ——
		// 不丢的话新 pane 的对话里会混进旧 pane 的几条，而且看着完全正常。
		if out.Pane != "p1" {
			t.Errorf("pane 该回一遍，实际 %q", out.Pane)
		}
	}
}

// 往上翻：`before` 那条路走得通，而且**不能把 next 当成文件尾之外的东西用**。
func TestChatLogEarlier(t *testing.T) {
	panes := []map[string]any{{
		"pane_id": "p1", "agent": "claude", "cwd": "/w",
		"agent_session": map[string]any{"kind": "id", "value": sid, "agent": "claude"},
	}}
	s, store := chatServer(t, true, panes)
	writeClaude(t, store, "-w", sid, "第一句", "第二句", "第三句")

	// 整份很小，所以首屏就是全部、start 是 0、more 为假 —— 这时候 before=0 给空结果。
	full := decodeLog(t, getChat(t, s, "/api/chat/log?pane=p1"))
	if full.More || full.Start != 0 {
		t.Fatalf("小文件该一次给完：more=%v start=%d", full.More, full.Start)
	}
	// **`before=0` 等于没带这个参数**（query string 里这两种分不开），所以走首屏那条路。
	// 前端也只在 `start > 0` 时才去翻（见 ChatPanel 的 loadEarlier），两边对得上。
	zero := decodeLog(t, getChat(t, s, "/api/chat/log?pane=p1&before=0"))
	if len(zero.Msgs) != len(full.Msgs) {
		t.Fatalf("before=0 该和不带一样（%d 条），实际 %d 条", len(full.Msgs), len(zero.Msgs))
	}

	// before 指到文件中间：拿到的是那之前的那一段。
	mid := decodeLog(t, getChat(t, s, "/api/chat/log?pane=p1&before="+itoa(int(full.Next))))
	if len(mid.Msgs) == 0 {
		t.Fatal("before=文件尾 该把前面那些读出来")
	}
	// before 比文件还大（文件被换掉了）：当到头了，别去读一段不存在的区间。
	over := decodeLog(t, getChat(t, s, "/api/chat/log?pane=p1&before=999999999"))
	if len(over.Msgs) != 0 {
		t.Fatalf("before 超出文件该给空结果，实际 %d 条", len(over.Msgs))
	}
}

// **偏移只在同一条会话里有意义。**
//
// `/clear` 之后 claude 写的是另一个文件，而前端手上那个 `from` 是上一份的字节偏移 ——
// 套在新文件上就是从中间某处开始读：前面那一截永远读不到，而且一个字都不报。
// 所以前端把手上那份的 sig 带上，服务端核不过就把偏移丢掉。
func TestChatLogIgnoresOffsetFromAnotherSession(t *testing.T) {
	panes := []map[string]any{{
		"pane_id": "p1", "agent": "claude", "cwd": "/w",
		"agent_session": map[string]any{"kind": "id", "value": sid, "agent": "claude"},
	}}
	s, store := chatServer(t, true, panes)
	writeClaude(t, store, "-w", sid, "第一句", "第二句", "第三句")

	// 带着**别的**会话的 sig + 一个落在文件中间的偏移来问：该整份重来，不该只给后半截。
	w := getChat(t, s, "/api/chat/log?pane=p1&from=120&sig=another-session")
	l := decodeLog(t, w)
	if len(l.Msgs) != 3 {
		t.Fatalf("sig 对不上时该整份重来（3 条），实际 %d 条", len(l.Msgs))
	}
	if l.Sig != sid {
		t.Fatalf("sig 该是服务端这份的：%q", l.Sig)
	}

	// sig 对得上时照旧信那个偏移（增量）。
	inc := decodeLog(t, getChat(t, s, "/api/chat/log?pane=p1&from="+itoa(int(l.Next))+"&sig="+sid))
	if len(inc.Msgs) != 0 {
		t.Fatalf("sig 对得上 + 偏移在尾部：该没有新东西，实际 %d 条", len(inc.Msgs))
	}

	// 不带 sig 的老前端照旧能用（信 from）。
	old := decodeLog(t, getChat(t, s, "/api/chat/log?pane=p1&from="+itoa(int(l.Next))))
	if len(old.Msgs) != 0 {
		t.Fatalf("不带 sig 该照旧信 from，实际 %d 条", len(old.Msgs))
	}
}

// writeAsk 摆一份「agent 在问你」的转录：一条 AskUserQuestion 的 tool_use，
// answered 为真时再补一条 tool_result（= 已经答过了）。
// writeAskQs 写一次「多题」提问。qs 里每项是 {多选?, 选项标签...}。
func writeAskQs(t *testing.T, st *transcript.Store, slug, id string, qs []askQ) {
	t.Helper()
	dir := filepath.Join(st.ClaudeRoot, slug)
	os.MkdirAll(dir, 0o700)
	questions := make([]map[string]any, 0, len(qs))
	for i, q := range qs {
		options := make([]map[string]any, 0, len(q.opts))
		for _, o := range q.opts {
			options = append(options, map[string]any{"label": o})
		}
		questions = append(questions, map[string]any{
			"header": fmt.Sprintf("题%d", i+1), "question": fmt.Sprintf("第 %d 问？", i+1),
			"multiSelect": q.multi, "options": options,
		})
	}
	ask, _ := json.Marshal(map[string]any{
		"type": "assistant", "uuid": "a1", "requestId": "r1", "timestamp": "2026-09-21T02:00:00.000Z",
		"message": map[string]any{"role": "assistant", "content": []map[string]any{{
			"type": "tool_use", "id": "toolu_ask", "name": "AskUserQuestion",
			"input": map[string]any{"questions": questions},
		}}},
	})
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(string(ask)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

type askQ struct {
	multi bool
	opts  []string
}

func writeAsk(t *testing.T, st *transcript.Store, slug, id string, opts []string, multi, answered bool) {
	t.Helper()
	dir := filepath.Join(st.ClaudeRoot, slug)
	os.MkdirAll(dir, 0o700)
	options := make([]map[string]any, 0, len(opts))
	for _, o := range opts {
		options = append(options, map[string]any{"label": o, "description": o + " 的说明"})
	}
	ask, _ := json.Marshal(map[string]any{
		"type": "assistant", "uuid": "a1", "requestId": "r1", "timestamp": "2026-09-21T02:00:00.000Z",
		"message": map[string]any{"role": "assistant", "content": []map[string]any{{
			"type": "tool_use", "id": "toolu_ask", "name": "AskUserQuestion",
			"input": map[string]any{"questions": []map[string]any{{
				"header": "文档", "question": "那份文档在哪儿？", "multiSelect": multi, "options": options,
			}}},
		}}},
	})
	body := string(ask) + "\n"
	if answered {
		res, _ := json.Marshal(map[string]any{
			"type": "user", "uuid": "u2", "timestamp": "2026-09-21T02:00:01.000Z",
			"message": map[string]any{"role": "user", "content": []map[string]any{{
				"type": "tool_result", "tool_use_id": "toolu_ask", "content": "第二个", "is_error": false,
			}}},
		})
		body += string(res) + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func askPanes() []map[string]any {
	return []map[string]any{{
		"pane_id": "p1", "agent": "claude", "cwd": "/w", "agent_status": "blocked",
		"agent_session": map[string]any{"kind": "id", "value": sid, "agent": "claude"},
	}}
}

// 转录里的提问要**带完整负载**出来（别的工具只有一行摘要）—— 被问住时人缺的恰恰是
// 「有几个选项、第二个是什么」。
func TestChatLogCarriesAsk(t *testing.T) {
	s, store := chatServer(t, true, askPanes())
	writeAsk(t, store, "-w", sid, []string{"第一个", "第二个", "第三个"}, false, false)
	l := decodeLog(t, getChat(t, s, "/api/chat/log?pane=p1"))
	if len(l.Msgs) != 1 || l.Msgs[0].Ask == nil {
		t.Fatalf("该带出 ask：%+v", l.Msgs)
	}
	q := l.Msgs[0].Ask.Questions[0]
	if q.Header != "文档" || q.Question != "那份文档在哪儿？" || len(q.Options) != 3 {
		t.Fatalf("负载不对：%+v", q)
	}
	if q.Options[1].Label != "第二个" || q.Options[1].Description == "" {
		t.Fatalf("选项不对：%+v", q.Options)
	}
	// 这条工具行不该再有那串按键名排序抠出来的摘要（会像 agent 自己说的话）
	if l.Msgs[0].Meta != "" {
		t.Fatalf("ask 那条不该有 meta：%q", l.Msgs[0].Meta)
	}
}

// 一键作答：**只发得出 ↓ ×n + ↵**，而且是按 pane 寻址（不依赖 herdr 的焦点）。
/*
一键作答发出去的按键序列。**这套协议是拿真 claude（2.1.278）在隔离的 tmux 里逐键量出来的**
（和 CLAUDE.md 里量 codex 那个粘贴判据一个办法），四条都钉在这儿：

	单选题                发选项序号（1-based）→ 选中 + 自动进下一题
	多选题                发每个要选的序号 → 切换勾选，不跳题；答完要自己 `tab` 翻页
	提交                  Submit 页上发 `1`
	只有一题且单选        序号本身就提交了，**不能再补 `1`**（会打进输入框）

还有一条更要紧的：**一个键一次调用**。`31` 连成一坨发过去整串会被丢掉（可见字符被当成
「粘进来的字符串」，两题都不会答上），所以这儿断言的是**调用次数 == 按键个数**，
不是「发出去的内容拼起来对不对」。
*/
func TestChatAnswerKeys(t *testing.T) {
	// 每项：发的 body → 期望依次发出去的那几下（数字走 text，tab 走命名键）
	for _, tc := range []struct {
		why  string
		qs   []askQ
		body string
		want []string
	}{
		{
			"单题单选（老前端那条 index 路）：只发序号，不补提交键",
			[]askQ{{false, []string{"a", "b", "c"}}},
			`{"pane":"p1","index":1}`,
			[]string{"2"},
		},
		{
			"单题单选（picks 路）",
			[]askQ{{false, []string{"a", "b", "c"}}},
			`{"pane":"p1","picks":[[2]]}`,
			[]string{"3"},
		},
		{
			"两题单选：两个序号 + 一个提交键",
			[]askQ{{false, []string{"a", "b"}}, {false, []string{"x", "y", "z"}}},
			`{"pane":"p1","picks":[[1],[0]]}`,
			[]string{"2", "1", "1"},
		},
		{
			"单题多选：两个序号 + tab 翻页 + 提交键",
			[]askQ{{true, []string{"a", "b", "c"}}},
			`{"pane":"p1","picks":[[0,2]]}`,
			[]string{"1", "3", "tab", "1"},
		},
		{
			"多选在前、单选在后：多选那题要 tab，单选那题自己跳",
			[]askQ{{true, []string{"a", "b", "c"}}, {false, []string{"x", "y"}}},
			`{"pane":"p1","picks":[[0,2],[1]]}`,
			[]string{"1", "3", "tab", "2", "1"},
		},
	} {
		s, store, keys := chatServerKeys(t, true, askPanes())
		writeAskQs(t, store, "-w", sid, tc.qs)
		w := postChat(t, s, tc.body)
		if w.Code != 200 {
			t.Errorf("%s：%d %s", tc.why, w.Code, strings.TrimSpace(w.Body.String()))
			continue
		}
		got := keys.all()
		// ① 一个键一次调用
		if len(got) != len(tc.want) {
			t.Errorf("%s：该发 %d 次（一个键一次），实际 %d 次：%+v", tc.why, len(tc.want), len(got), got)
			continue
		}
		// ② 顺序和内容。**每一下都必须走 `keys`，`text` 一个字都不能有** ——
		//    `send_input` 的 text 会被按 bracketed paste 编码，而 claude 的选择器不理粘贴
		//    （见 sendOneByOne 的 ①，这是用户报的「提交了却一个都没选上」的真因）。
		for i, w2 := range tc.want {
			if got[i].Text != "" {
				t.Errorf("%s：第 %d 下走了 text（%q）—— 必须走 keys", tc.why, i+1, got[i].Text)
			}
			if len(got[i].Keys) != 1 {
				t.Errorf("%s：第 %d 下发了 %d 个键 —— 一次只能一个", tc.why, i+1, len(got[i].Keys))
				continue
			}
			one := strings.Join(got[i].Keys, "+")
			if one != w2 {
				t.Errorf("%s：第 %d 下该是 %q，实际 %q", tc.why, i+1, w2, one)
			}
			if got[i].Pane != "p1" {
				t.Errorf("%s：第 %d 下发错 pane 了：%q", tc.why, i+1, got[i].Pane)
			}
		}
	}
}

// 选择给得不对时**一个键都不许发**：发了一半停在半填的选择器上，比什么都没发更糟
// （Submit 页要求全答完）。
func TestChatAnswerRejectsBadPicks(t *testing.T) {
	two := []askQ{{false, []string{"a", "b"}}, {true, []string{"x", "y"}}}
	for _, tc := range []struct{ why, body string }{
		{"少给一题", `{"pane":"p1","picks":[[0]]}`},
		{"某题一个都没选", `{"pane":"p1","picks":[[0],[]]}`},
		{"单选那题给了两个", `{"pane":"p1","picks":[[0,1],[0]]}`},
		{"序号越界", `{"pane":"p1","picks":[[0],[9]]}`},
		{"同一个选项给了两次（多选是切换，等于没选）", `{"pane":"p1","picks":[[0],[1,1]]}`},
		{"多题却走老的 index 路", `{"pane":"p1","index":0}`},
	} {
		s, store, keys := chatServerKeys(t, true, askPanes())
		writeAskQs(t, store, "-w", sid, two)
		if w := postChat(t, s, tc.body); w.Code != 400 {
			t.Errorf("%s：该 400，实际 %d %s", tc.why, w.Code, strings.TrimSpace(w.Body.String()))
		}
		if n := len(keys.all()); n != 0 {
			t.Errorf("%s：一个键都不该发，实际 %d 次", tc.why, n)
		}
	}
}

// **此刻没有在等你选的时候，一个键都不能发。**
//
// 这是这条路上最要紧的一条：对着一个没有选择框的 pane 打 ↵，会把输入框里的草稿提交出去。
// 判据用的是转录（最后一条工具调用是没答的提问），不是 `agent_status` —— 那个实测在
// 开着选择器时报 idle。
func TestChatAnswerRefusesWhenNotPending(t *testing.T) {
	cases := []struct {
		why      string
		opts     []string
		multi    bool
		answered bool
	}{
		{"已经答过了", []string{"a", "b"}, false, true},
		// 多选 / 多题**现在是支持的**（按键协议实测出来了，见 askKeys），所以不在这张表里；
		// 它们的序列钉在 TestChatAnswerKeys 上。
	}
	for _, c := range cases {
		s, store, keys := chatServerKeys(t, true, askPanes())
		writeAsk(t, store, "-w", sid, c.opts, c.multi, c.answered)
		w := postChat(t, s, `{"pane":"p1","index":1}`)
		if w.Code != http.StatusConflict {
			t.Errorf("%s：该是 409，实际 %d %s", c.why, w.Code, strings.TrimSpace(w.Body.String()))
		}
		var out struct{ Reason string }
		json.Unmarshal(w.Body.Bytes(), &out)
		if out.Reason != "not_pending" {
			t.Errorf("%s：reason 该是 not_pending，实际 %q", c.why, out.Reason)
		}
		if n := len(keys.all()); n != 0 {
			t.Errorf("%s：**一个键都不该发**，实际发了 %d 次", c.why, n)
		}
	}

	// 最后一条工具调用不是提问（普通 Bash）：同样不发。
	s, store, keys := chatServerKeys(t, true, askPanes())
	writeClaude(t, store, "-w", sid, "就是一句话")
	if w := postChat(t, s, `{"pane":"p1","index":0}`); w.Code != http.StatusConflict {
		t.Errorf("压根没有提问时该是 409，实际 %d", w.Code)
	}
	if n := len(keys.all()); n != 0 {
		t.Errorf("压根没有提问时发了 %d 次键", n)
	}
}

// 序号越界 / 负数：拦在发键之前。
func TestChatAnswerBadIndex(t *testing.T) {
	for _, body := range []string{
		`{"pane":"p1","index":2}`, // 只有两个选项
		`{"pane":"p1","index":-1}`,
		`{"index":0}`, // 没给 pane
	} {
		s, store, keys := chatServerKeys(t, true, askPanes())
		writeAsk(t, store, "-w", sid, []string{"a", "b"}, false, false)
		if w := postChat(t, s, body); w.Code != 400 {
			t.Errorf("%s：该是 400，实际 %d %s", body, w.Code, strings.TrimSpace(w.Body.String()))
		}
		if n := len(keys.all()); n != 0 {
			t.Errorf("%s：不该发键，实际发了 %d 次", body, n)
		}
	}
}

func postChat(t *testing.T, s *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("POST", "/api/chat/answer", strings.NewReader(body))
	r.Header.Set("content-type", "application/json")
	w := httptest.NewRecorder()
	s.apiChat(w, r, []string{"chat", "answer"})
	return w
}

// 「在这个 pane 里开一个 agent」那条路。三条都是这个口存在的全部约束：
//
//   - 发出去的**字**必须是白名单里那个命令名 + 一个回车，别的一律发不出去
//     （这个口往一个登录 shell 里敲字并回车，等于远程执行命令）；
//   - pane 里**已经有 agent** 就一个字都不能发 —— 那不是「白开一个」，而是把 `claude`
//     这五个字母当成一句话投进正在跑的那个 agent 的输入框；
//   - 按 pane 寻址，和 `answer` 同理（不能依赖 herdr 此刻焦点在哪儿）。
func TestChatStart(t *testing.T) {
	// 一个没有 agent 的 shell pane（就是那一屏「这儿没有 agent」的来路）
	shell := []map[string]any{{"pane_id": "p9", "agent": "", "cwd": "/w", "agent_status": "unknown"}}

	for _, agent := range []string{"claude", "codex"} {
		s, _, keys := chatServerKeys(t, true, shell)
		w := postStart(t, s, `{"pane":"p9","agent":"`+agent+`"}`)
		if w.Code != 200 {
			t.Fatalf("%s：%d %s", agent, w.Code, w.Body.String())
		}
		got := keys.all()
		if len(got) != 1 {
			t.Fatalf("%s：该发一次，实际 %d 次", agent, len(got))
		}
		if got[0].Pane != "p9" {
			t.Fatalf("发错 pane 了：%q", got[0].Pane)
		}
		if got[0].Text != agent {
			t.Fatalf("%s：该敲 %q，实际 %q", agent, agent, got[0].Text)
		}
		if strings.Join(got[0].Keys, ",") != "enter" {
			t.Fatalf("%s：该只跟一个 enter，实际 %v", agent, got[0].Keys)
		}
	}

	// 白名单外的一律不发。`;` 那两条是「这个口不是一条任意命令通道」的正面例子。
	for _, bad := range []string{"", "bash", "claude; rm -rf /", "claude --dangerously-skip-permissions"} {
		s, _, keys := chatServerKeys(t, true, shell)
		w := postStart(t, s, `{"pane":"p9","agent":`+quote(bad)+`}`)
		if w.Code != 400 {
			t.Fatalf("%q 该被拒（400），实际 %d %s", bad, w.Code, w.Body.String())
		}
		if n := len(keys.all()); n != 0 {
			t.Fatalf("%q 被拒了却发出去了 %d 次", bad, n)
		}
	}

	// 已经有 agent：409 + reason，而且一个字都没发。
	s, _, keys := chatServerKeys(t, true, askPanes()) // p1 上跑着 claude
	w := postStart(t, s, `{"pane":"p1","agent":"claude"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("该回 409，实际 %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Reason string `json:"reason"`
		Agent  string `json:"agent"`
	}
	json.Unmarshal(w.Body.Bytes(), &out)
	if out.Reason != "has_agent" || out.Agent != "claude" {
		t.Fatalf("该带上机器判据：%s", w.Body.String())
	}
	if n := len(keys.all()); n != 0 {
		t.Fatalf("pane 里已经有 agent 了，却往它输入框里敲了 %d 次", n)
	}

	// 没带 pane
	s2, _, keys2 := chatServerKeys(t, true, shell)
	if w := postStart(t, s2, `{"agent":"claude"}`); w.Code != 400 {
		t.Fatalf("没带 pane 该 400，实际 %d", w.Code)
	}
	if n := len(keys2.all()); n != 0 {
		t.Fatalf("参数错却发出去了 %d 次", n)
	}
}

func postStart(t *testing.T, s *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("POST", "/api/chat/start", strings.NewReader(body))
	r.Header.Set("content-type", "application/json")
	w := httptest.NewRecorder()
	s.apiChat(w, r, []string{"chat", "start"})
	return w
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
