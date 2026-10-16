package transcript

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// 冒报之后把真正那段找回来（见 ErrForeign）。
//
// # 为什么要找，而不是让人重开 agent
//
// 第一版遇到冒报只报 foreign、请人「把 claude 重开一次」。可冒报的来路（agent 跑测试 /
// 用 annulo 这类工具起 `claude -p` 子进程）是**反复发生**的：前一秒 chat 还好好的，子进程
// 一起来就盖掉，重开一次过一会儿又被盖（用户报的「总是发生」）。herdr 那边只记最后一个报的，
// 所以真身份只能从 herdr 以外的地方拿。
//
// # 从哪儿拿
//
// claude 自己给每个活着的进程写一份 `~/.claude/sessions/<pid>.json`（sessionId / cwd /
// entrypoint），而 pane 里那个 claude 的环境变量里带着 `HERDR_PANE_ID`（herdr 给 pane 的
// shell 设的，一路继承下来）。两样一对就是「这个 pane 里那个交互式 claude 此刻的会话」。
//
// 三道筛，少一道都会认错：
//
//	① 进程还活着（pid 文件不一定删得干净）
//	② **entrypoint 是 `cli`** —— 冒报的那个子进程 `HERDR_PANE_ID` 也是这个 pane（它就是
//	   这么冒报的），区别只在它是 `sdk-cli`（`claude -p`）。cwd 那道一般也能挡，但子进程
//	   开在项目子目录里时挡不住
//	③ cwd 和 pane 是同一个项目
//
// 只在 foreign 那条路上走（要 fork 一次 ps），结果跟着 Find 的缓存走。

// liveSession 一份 `~/.claude/sessions/<pid>.json` 里用得上的那几项。
type liveSession struct {
	PID        int    `json:"pid"`
	SessionID  string `json:"sessionId"`
	CWD        string `json:"cwd"`
	Entrypoint string `json:"entrypoint"`
	StartedAt  int64  `json:"startedAt"`
}

// paneClaude 找这个 pane 里那个交互式 claude 的 session id。找不到回空串。
func (s *Store) paneClaude(ref Ref) string {
	if ref.Pane == "" || ref.CWD == "" {
		return ""
	}
	dir := filepath.Join(filepath.Dir(s.ClaudeRoot), "sessions")
	m, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	var cands []liveSession
	for _, f := range m {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var l liveSession
		if json.Unmarshal(b, &l) != nil || l.PID <= 0 || !idRe.MatchString(l.SessionID) {
			continue
		}
		if l.SessionID == ref.Value || (l.Entrypoint != "" && l.Entrypoint != "cli") {
			continue
		}
		if l.CWD == "" || !sameProject(l.CWD, ref.CWD) || !alive(l.PID) {
			continue
		}
		cands = append(cands, l)
	}
	if len(cands) == 0 {
		return ""
	}
	pids := make([]int, len(cands))
	for i, c := range cands {
		pids[i] = c.PID
	}
	paneOf := s.PaneOf
	if paneOf == nil {
		paneOf = paneOfPIDs
	}
	panes := paneOf(pids)
	// 同一个 pane 里先后开过两个还活着的（少见：一个挂在后台）就取后开的那个。
	sort.Slice(cands, func(i, j int) bool { return cands[i].StartedAt > cands[j].StartedAt })
	for _, c := range cands {
		if panes[c.PID] == ref.Pane {
			return c.SessionID
		}
	}
	return ""
}

func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// paneOfPIDs 读每个进程环境里的 HERDR_PANE_ID。
//
// Linux 读 /proc/<pid>/environ；macOS 没有 /proc，`ps eww` 会把同一用户进程的环境接在
// 命令行后面（一次 fork 问完全部）。读不到的就不在结果里 —— 那就当没找到，退回报 foreign。
func paneOfPIDs(pids []int) map[int]string {
	out := map[int]string{}
	if runtime.GOOS == "linux" {
		for _, pid := range pids {
			b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ")
			if err != nil {
				continue
			}
			for _, kv := range bytes.Split(b, []byte{0}) {
				if v, ok := bytes.CutPrefix(kv, []byte("HERDR_PANE_ID=")); ok {
					out[pid] = string(v)
				}
			}
		}
		return out
	}
	ss := make([]string, len(pids))
	for i, p := range pids {
		ss[i] = strconv.Itoa(p)
	}
	b, err := exec.Command("ps", "eww", "-o", "pid=,command=", "-p", strings.Join(ss, ",")).Output()
	if err != nil && len(b) == 0 {
		return out
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		pid, err := strconv.Atoi(f[0])
		if err != nil {
			continue
		}
		for _, w := range f[1:] {
			if v, ok := strings.CutPrefix(w, "HERDR_PANE_ID="); ok {
				out[pid] = v
			}
		}
	}
	return out
}
