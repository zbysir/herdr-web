package transcript

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// 定位：从 herdr 报的那个 ref 找到磁盘上那份转录。
//
// # 为什么这一层要自己夹边界，而不借文件浏览那一处
//
// 看 diff 那条路是借 `files.Browser.Check` 当唯一的鉴权点（见 internal/gitdiff），这儿**不能**
// 照抄：转录在 `~/.claude` / `~/.codex` 下，而 `HERDR_WEB_FILE_ROOTS` 一配就是个 jail，
// 那两个目录几乎肯定不在里面 —— 借它的话「配了 FILE_ROOTS 的部署上 chat 永远打不开」。
//
// 换来的义务是这一层自己把路径空间钉死，两条都得有：
//
//	① **id 必须过正则**（只认 UUID 那种字符）。它要拼进路径和 glob，`../../etc/passwd`
//	   这种一放进去就是任意文件读取，而这个服务是从公网点得到的
//	② **path 必须落在对应 agent 的根底下**。`kind:"path"` 是那个 hook 报上来的，herdr 只是
//	   转发、不校验 —— 也就是说「相信 herdr」等于「相信任何能连上 herdr socket 的东西」，
//	   而那个 socket 上什么都能调。落不到根底下就当没有
//
// 第 ② 条不是假想：hook 脚本是明文的 shell + python，装在用户自己目录下（
// `~/.claude/hooks/herdr-agent-state.sh`），任何跑在那台机器上的东西都改得动它。

// idRe 是 session id 认得出的形状。
//
// claude 是标准 UUID，codex 是 UUIDv7 那种（都只有 16 进制和横线），这儿放宽到
// 「字母数字 + 横线 + 下划线」以免哪天换了格式就整个用不了 —— 但**点和斜杠一个都不放**，
// 那两个正是穿目录用的。
var idRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

// Ref 是 herdr 那边报上来的会话身份 + 这个 pane 的 cwd。
type Ref struct {
	Agent string // "claude" / "codex"
	Kind  string // "id" / "path"（herdr 的 AgentSessionRefKind）；空 = 没报过
	Value string
	CWD   string
	// Siblings 同一个 cwd 下还有几个跑着同一个 agent 的 pane（含自己）。
	// 只在**没报过会话**时用得上：>1 就一律不猜，见 ErrAmbiguous。
	Siblings int
}

// Store 定位 + 一点缓存。
//
// 缓存是必需的而不是优化：轮询每几秒来一次，而定位在最坏情况下要 glob 上千个文件、
// 还要读几十个文件的头一行 —— **那是在跑着 agent 的那台机器上**。同一条 ref 的结果不会变
// （id 变了就是另一条 ref），所以缓存只用来挡重复的目录扫描。
type Store struct {
	// ClaudeRoot / CodexRoot 两个根。留成字段是为了测试能指到 t.TempDir()，
	// 生产上就是 ~/.claude/projects 和 ~/.codex/sessions。
	ClaudeRoot string
	CodexRoot  string

	mu sync.Mutex
	// cache 懒初始化 —— **零值的 Store 必须能用**：两个根是导出字段，直接用结构体字面量
	// 建（测试、或者哪天要把根指到别处）是正常用法，而那时 cache 是 nil map，
	// 往 nil map 里写是 panic。NewStore 里建一次不算数，那只覆盖一条构造路径。
	cache map[string]cacheEnt
	// shells 「还有几个后台任务在跑」的增量记账，按转录文件的路径存。见 shells.go
	shells map[string]*shellState
}

type cacheEnt struct {
	src Source
	at  time.Time
}

// cacheTTL 定位结果留多久。
//
// 取 30 秒：既挡掉轮询那几拍的重复扫描，又不至于在「换了会话」时卡着老文件太久
// （换会话时 id 会变，key 也就跟着变，这个 TTL 只影响**猜**出来的那条路）。
const cacheTTL = 30 * time.Second

func NewStore() *Store {
	home, _ := os.UserHomeDir()
	return &Store{
		ClaudeRoot: filepath.Join(home, ".claude", "projects"),
		CodexRoot:  filepath.Join(home, ".codex", "sessions"),
		cache:      map[string]cacheEnt{},
	}
}

// Enabled 这台机器上有没有转录可读。两个根一个都不在就说明既没装 claude 也没装 codex。
//
// **nil 接收者要答 false**（和 gitdiff.Runner.Enabled 同一个做法）：HERDR_WEB_CHAT=0 时
// Store 压根不建，而 /api/state 那边照旧会问一句「chat 开着没有」。
func (s *Store) Enabled() bool {
	if s == nil {
		return false
	}
	for _, r := range []string{s.ClaudeRoot, s.CodexRoot} {
		if st, err := os.Stat(r); err == nil && st.IsDir() {
			return true
		}
	}
	return false
}

// Find 把一条 ref 变成一份定位好的转录。
func (s *Store) Find(ref Ref) (Source, error) {
	root, err := s.rootFor(ref.Agent)
	if err != nil {
		return Source{}, err
	}
	key := strings.Join([]string{ref.Agent, ref.Kind, ref.Value, ref.CWD}, "\x00")
	s.mu.Lock()
	if e, ok := s.cache[key]; ok && time.Since(e.at) < cacheTTL {
		s.mu.Unlock()
		// 缓存里那份文件可能已经没了（会话被删 / 目录被清），所以还要看一眼。
		if _, err := os.Stat(e.src.Path); err == nil {
			return e.src, nil
		}
		s.mu.Lock()
		delete(s.cache, key)
	}
	s.mu.Unlock()

	src, err := s.find(ref, root)
	if err != nil {
		return Source{}, err
	}
	s.mu.Lock()
	if s.cache == nil {
		s.cache = map[string]cacheEnt{}
	}
	s.cache[key] = cacheEnt{src: src, at: time.Now()}
	s.mu.Unlock()
	return src, nil
}

func (s *Store) rootFor(agent string) (string, error) {
	switch agent {
	case "claude":
		return s.ClaudeRoot, nil
	case "codex":
		return s.CodexRoot, nil
	}
	return "", fmt.Errorf("%w: %s", ErrUnsupported, agent)
}

func (s *Store) find(ref Ref, root string) (Source, error) {
	switch ref.Kind {
	case "path":
		p, err := underRoot(root, ref.Value)
		if err != nil {
			return Source{}, err
		}
		if _, err := os.Stat(p); err != nil {
			return Source{}, err
		}
		return Source{Path: p, Agent: ref.Agent, Sig: ref.Value}, nil

	case "id":
		if !idRe.MatchString(ref.Value) {
			return Source{}, fmt.Errorf("会话 id 的形状不对：%q", oneLine(ref.Value, 40))
		}
		p, err := s.byID(ref.Agent, root, ref.Value, ref.CWD)
		if err != nil {
			return Source{}, err
		}
		return Source{Path: p, Agent: ref.Agent, Sig: ref.Value}, nil
	}

	// 没报过会话（hook 没装，或者这个 pane 里的 agent 是在装 hook 之前起来的 ——
	// hook 只在 SessionStart 那一下报，已经跑着的那些要重开才报）。
	if ref.Siblings > 1 {
		return Source{}, ErrAmbiguous
	}
	p, err := s.guess(ref.Agent, root, ref.CWD)
	if err != nil {
		return Source{}, err
	}
	// 猜出来的那份，sig 用文件路径而不是 session id —— 前端只拿 sig 判「换了没换」，
	// 而这条路上「换了会话」的表现正是换了一份文件。
	return Source{Path: p, Agent: ref.Agent, Sig: "path:" + p}, nil
}

// byID 按 session id 找文件。
//
// **不靠目录名变形规则。** claude 的项目目录名是 cwd 变形来的（`/` 换成 `-`，而且实测
// 点号之类也一起折成了 `-`），照抄那条规则就是给自己埋一个静默坑：变形错一个字符的表现是
// 「chat 永远打不开」，而报错只会说文件不存在。session id 是 UUID、全机唯一，所以：
//
//	快路   先按变形拼一次（命中的话一次 stat 就完了）
//	正路   拼不中就 glob 一层（实测 45 个项目目录，代价可以忽略）
func (s *Store) byID(agent, root, id, cwd string) (string, error) {
	if agent == "claude" {
		if cwd != "" {
			p := filepath.Join(root, claudeSlug(cwd), id+".jsonl")
			if _, err := os.Stat(p); err == nil {
				return p, nil
			}
		}
		if m, _ := filepath.Glob(filepath.Join(root, "*", id+".jsonl")); len(m) > 0 {
			return m[0], nil
		}
		return "", fmt.Errorf("找不到会话 %s 的转录文件：%w", id, ErrNoTranscript)
	}

	// codex：**session id 就在文件名里**（`rollout-<时间>-<id>.jsonl`），所以按后缀 glob。
	// 年/月/日 三层是分开的目录，而我们不知道这个会话是哪天开的（能跨天），所以三层都放开。
	m, _ := filepath.Glob(filepath.Join(root, "*", "*", "*", "rollout-*-"+id+".jsonl"))
	if len(m) > 0 {
		sort.Strings(m)
		return m[len(m)-1], nil
	}
	return "", fmt.Errorf("找不到会话 %s 的 rollout 文件：%w", id, ErrNoTranscript)
}

// guess 没有会话 id 时按 cwd 猜最近那一份。
//
// 这条路**只在没装 hook 时**走，而且调用方已经保证了「这个 cwd 下只有一个 agent pane」。
// 即便如此它仍然可能猜错（同一个目录下先后开过好几个会话，人正看的是早先那个），
// 所以前端要说清「这是猜的」—— 界面上那句「装上 herdr 的 integration 才能精确对上」。
func (s *Store) guess(agent, root, cwd string) (string, error) {
	if cwd == "" {
		return "", ErrNoSession
	}
	if agent == "claude" {
		dir := filepath.Join(root, claudeSlug(cwd))
		p, err := newestJSONL(dir)
		if err != nil {
			return "", ErrNoSession
		}
		return p, nil
	}
	// codex 的 rollout 不按目录分项目（只按日期），所以只能读每份的头一行看 cwd。
	// **只看最近那几十份**：实测本机 1061 份，全读一遍是几十兆，而那台机器上正跑着 agent。
	return newestCodexFor(root, cwd)
}

// claudeSlug 把 cwd 变成 claude 的项目目录名。
//
// 实测出来的形状：`/Users/bysir/dev/bysir/herdr-web` → `-Users-bysir-dev-bysir-herdr-web`
// （开头那个 `-` 是根斜杠变来的）。点号之类也折成 `-`，所以这儿一律「非字母数字都换成 `-`」。
//
// **这只是快路和「猜」那条路在用**，按 id 找的正路不依赖它（见 byID）—— 规则是别人的，
// 哪天变了这儿会静默地拼不中，而那时正路照旧能找到。
func claudeSlug(cwd string) string {
	var b strings.Builder
	for _, r := range cwd {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

func newestJSONL(dir string) (string, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	best, bestAt := "", time.Time{}
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		if fi.ModTime().After(bestAt) {
			best, bestAt = filepath.Join(dir, e.Name()), fi.ModTime()
		}
	}
	if best == "" {
		return "", os.ErrNotExist
	}
	return best, nil
}

// guessScan 猜 codex 时最多翻多少份 rollout。
const guessScan = 40

func newestCodexFor(root, cwd string) (string, error) {
	m, _ := filepath.Glob(filepath.Join(root, "*", "*", "*", "rollout-*.jsonl"))
	if len(m) == 0 {
		return "", ErrNoSession
	}
	// 文件名里带 ISO 时间戳，所以**字典序就是时间序** —— 不用 stat 一千次去拿 mtime。
	sort.Strings(m)
	for i := len(m) - 1; i >= 0 && i > len(m)-1-guessScan; i-- {
		if codexCWD(m[i]) == cwd {
			return m[i], nil
		}
	}
	return "", ErrNoSession
}

// codexCWD 读 rollout 的头一行（`session_meta`）拿 cwd。
//
// 那一行实测 22KB（里面是整段系统提示），所以**只读头 64KB 就够**，而且要在这儿掐住 ——
// 不掐的话「猜一次」就是读几十份 × 每份几十 KB。
func codexCWD(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, 64<<10)
	n, _ := f.Read(buf)
	if n <= 0 {
		return ""
	}
	line := buf[:n]
	if i := indexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	var l cxLine
	if json.Unmarshal(line, &l) != nil || l.Type != "session_meta" {
		return ""
	}
	var p struct {
		CWD string `json:"cwd"`
	}
	if json.Unmarshal(l.Payload, &p) != nil {
		return ""
	}
	return p.CWD
}

// underRoot 把 hook 报上来的路径夹在根底下。
//
// 三道：解析符号链接之前先 Clean（挡 `..`）、必须以根 + 分隔符开头、必须是 `.jsonl`。
// 最后那条不是洁癖 —— 少了它，一个被改过的 hook 报 `~/.claude/.credentials.json`
// 就能把凭据渲染到页面上（那个文件真在 `~/.claude` 底下）。
func underRoot(root, p string) (string, error) {
	if p == "" {
		return "", ErrNoSession
	}
	if !strings.HasSuffix(p, ".jsonl") {
		return "", fmt.Errorf("转录文件必须是 .jsonl：%q", oneLine(p, 60))
	}
	abs, err := filepath.Abs(filepath.Clean(p))
	if err != nil {
		return "", err
	}
	rootAbs, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", err
	}
	// EvalSymlinks 只在两边都解得开时用来比 —— 解不开（文件刚被删）就退回按字符串比，
	// 别因为这个把正常情况挡掉。
	if r1, err1 := filepath.EvalSymlinks(abs); err1 == nil {
		if r2, err2 := filepath.EvalSymlinks(rootAbs); err2 == nil {
			abs, rootAbs = r1, r2
		}
	}
	if abs != rootAbs && !strings.HasPrefix(abs, rootAbs+string(filepath.Separator)) {
		return "", fmt.Errorf("转录文件不在 %s 底下", root)
	}
	return abs, nil
}
