package transcript

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// 会话「搬走了」：claude 在 pane 里 `/resume` 一个**后台会话**（`claude --bg` 起的那种）时，
// 前台这个进程的会话文件只留下几行（`/resume` 那条命令 + 「Opening "…", running in the
// background (2219da0d)」），最后补一条 `{"type":"continued-in", ...}` 就再也不写了 ——
// 对话全写在后台那个会话的文件里。herdr 报的是前台进程 SessionStart 时那个 id，所以 chat
// 读到的是一份空壳，表现是「这个会话明明有消息，chat 里一条都没有」（用户报的）。
//
// 去哪儿接着读，两条线索，按顺序认：
//
//	① `continuedInSessionId` 对得上一份文件 —— 直接过去（后台会话被 resume 回前台时是这样）
//	② 对不上（实测前台 resume 后台时写的是**前台自己被挂起的那个 job**，没有对应的文件）
//	   就认那句「running in the background (<job>)」：job 是后台会话 id 的前 8 位，同目录下
//	   按前缀找，**恰好一份**才认 —— 两份以上就不猜
//
// 最多跟 4 跳（防环）；跟过去的文件 cwd 对不上 pane 的就不跟（和 checkOwner 同一道）。

var bgJobRe = regexp.MustCompile(`running in the background \(([0-9a-f]{6,16})\)`)

// followTail 确认是搬走的之后，读多少尾巴来找那句 stdout（就在 continued-in 前面几行）。
const followTail = 64 << 10

// follow 顺着「搬走了」找到真在写的那份。不是搬走的会话原样返回。
func (s *Store) follow(ref Ref, src Source) Source {
	if src.Agent != "claude" {
		return src
	}
	for range 4 {
		next, id := continuedIn(src.Path)
		if next == "" {
			return src
		}
		if ref.CWD != "" {
			if c := claudeCWD(next); c != "" && !sameProject(c, ref.CWD) {
				return src
			}
		}
		src = Source{Path: next, Agent: src.Agent, Sig: id}
	}
	return src
}

// continuedIn 这份转录如果以 continued-in 收尾，返回接着写的那份文件和它的 session id。
func continuedIn(path string) (string, string) {
	f, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", ""
	}
	tail := func(n int64) []string {
		off := max(st.Size()-n, 0)
		buf := make([]byte, st.Size()-off)
		if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
			return nil
		}
		return strings.Split(strings.TrimRight(string(buf), "\n"), "\n")
	}
	// 每拍都要问一次（在缓存外面，见 Find），所以先只读 4KB 看最后一行：绝大多数会话
	// 不是搬走的，到这儿就结束了。最后一行比 4KB 长时解不开 JSON —— 那也说明它不是
	// continued-in（那条很短），照样当没搬走
	var last struct {
		Type string `json:"type"`
		Next string `json:"continuedInSessionId"`
	}
	lines := tail(4 << 10)
	if len(lines) == 0 || json.Unmarshal([]byte(lines[len(lines)-1]), &last) != nil || last.Type != "continued-in" {
		return "", ""
	}
	lines = tail(followTail)
	dir := filepath.Dir(path)
	if idRe.MatchString(last.Next) {
		if p := filepath.Join(dir, last.Next+".jsonl"); p != path {
			if _, err := os.Stat(p); err == nil {
				return p, last.Next
			}
		}
	}
	for i := len(lines) - 1; i >= 0; i-- {
		m := bgJobRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		hits, _ := filepath.Glob(filepath.Join(dir, m[1]+"*.jsonl"))
		if len(hits) != 1 || hits[0] == path {
			return "", ""
		}
		return hits[0], strings.TrimSuffix(filepath.Base(hits[0]), ".jsonl")
	}
	return "", ""
}
