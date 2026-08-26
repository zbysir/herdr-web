// Package outbox 是语音投稿的发件箱：列目标 pane、拉回远端输入框、覆盖式投稿。
//
// 「发件箱，不是镜像」—— 每次整段覆盖，发完清空本地框，不发增量。
package outbox

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zbysir/herdr-web/internal/composer"
	"github.com/zbysir/herdr-web/internal/herdr"
)

// Follow 是「投给我此刻在 herdr 里激活的那个 pane」的哨兵值。默认走这条：
// 一般人是先在 herdr 里点到某个 pane，再去网页说话，不想再选一次。
// 解析放在服务端做，这样投的永远是**按下按钮那一刻**的焦点。
const Follow = "__focused"

type Outbox struct {
	C        *herdr.Client
	SettleMS int
	// Seen 给「这个 pane 上次状态变化是什么时候」（unix 毫秒，0 = 不知道）。
	// 由 internal/agentwatch 提供，可以为 nil —— herdr 的 API 里没有任何时间戳，
	// 时间是那边盯着事件自己打的，拿不到就当不知道，只影响显示不影响排序。
	Seen func(paneID string) int64
}

// Target 是一个可投的 pane。
//
// Seq / Changed 是给「面板一览」排序用的：**Seq（state_change_seq）一直是对的**，
// Changed（上次状态变化的 unix 毫秒）只有 herdr-web 在盯的这段时间里才有 —— 所以
// 排序以 Seq 兜底，Changed 只管显示「3 分钟前」。
type Target struct {
	ID     string `json:"id"`
	Agent  string `json:"agent"`
	Status string `json:"status"`
	// Workspace / Tab 是**给人看的标签**（herdr 的 workspace.list / tab.list，拿不到就退回 id）。
	// **别拿它们分组** —— 两个工作空间同名是常态（标签多半就是目录名），而前端有几处要问
	// 「这个 pane 是不是当前这个工作空间的」（改动面板的候选仓库、文件面板的起点排序）。
	// 那件事认下面这个 id。
	Workspace   string `json:"workspace"`
	WorkspaceID string `json:"workspaceId"`
	Tab         string `json:"tab"`
	Title       string `json:"title"`
	CWD         string `json:"cwd"`
	Focused     bool   `json:"focused"`
	Seq         uint64 `json:"seq,omitempty"`
	Changed     int64  `json:"changed,omitempty"`
}

// Info 是一个 pane 的可显示身份（workspace / tab 的好看标签由前端用缓存补）。
type Info struct {
	Target      string `json:"target"`
	Followed    bool   `json:"followed"`
	Agent       string `json:"agent"`
	Status      string `json:"status"`
	WorkspaceID string `json:"workspaceId"`
	TabID       string `json:"tabId"`
	Title       string `json:"title"`
	CWD         string `json:"cwd"`
}

type PullResult struct {
	Info
	Text string `json:"text,omitempty"`
	// NoBox：这一屏上认不出输入框（没有提示符字形）。和「输入框是空的」不是一回事，
	// 前端要区别对待 —— 不然「认不出」看起来就像「远端把框清空了」。
	NoBox  bool     `json:"noBox,omitempty"`
	Screen []string `json:"screen,omitempty"`
}

type DraftResult struct {
	Info
	Pushed  int    `json:"pushed,omitempty"`
	Skipped string `json:"skipped,omitempty"`
}

type ClearResult struct {
	Rounds int   `json:"rounds"`
	Empty  *bool `json:"empty"` // nil = 没法判断（shell pane）
	NoBox  bool  `json:"noBox,omitempty"`
}

type SayResult struct {
	Info
	Cleared ClearResult `json:"cleared"`
	Chars   int         `json:"chars"`
	Lines   int         `json:"lines"`
}

func identify(p *herdr.Pane, followed bool) Info {
	return Info{
		Target: p.PaneID, Followed: followed, Agent: p.Agent,
		Status: orUnknown(p.AgentStatus), WorkspaceID: p.WorkspaceID,
		TabID: p.TabID, Title: p.Title, CWD: p.CWD,
	}
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// resolve 把 target 解析成具体 pane：哨兵值 / 空 → 当前激活的 pane。
func (o *Outbox) resolve(target string) (*herdr.Pane, bool, error) {
	if target != "" && target != Follow {
		p, err := o.C.PaneGet(target)
		return p, false, err
	}
	p, err := o.C.PaneCurrent()
	return p, true, err
}

// readSettled 读两次取后一次。
//
// pane.read 的快照有一帧延迟（写完立刻读会拿到上一帧）。拉回 / 轮询也得走这条：
// 单次读会拿到上一帧，表现就是「切了 pane，框里还是旧内容」，更糟的是自动拉回会
// 把这一帧陈旧内容记成「已对齐」，用户接着在陈旧内容上编辑。
//
// 别把 settle 当成可以省的开销：herdr 的响应有时只要 1-2ms，两次读会落在同一帧上，
// 读到的就是写之前的内容。实测 settle=0 时清空循环 6 轮跑完还是清不空。
func (o *Outbox) readSettled(id string, settle int) (string, error) {
	if _, err := o.C.ReadANSI(id); err != nil {
		return "", err
	}
	if settle > 0 {
		time.Sleep(time.Duration(settle) * time.Millisecond)
	}
	return o.C.ReadANSI(id)
}

// minClearSettle 是清空循环的保底间隔：那里的正确性最要紧（读到陈旧帧会让
// 「清不空」误判，进而拒绝投稿或者把残留一起发出去），所以不受配置调低影响。
const minClearSettle = 120

func (o *Outbox) settle() int { return o.SettleMS }

// ReadComposer 读远端输入框。ok=false 表示这一屏上认不出输入框，此时的 "" 不能
// 当成「框是空的」用。
func (o *Outbox) ReadComposer(id, agent string) (text string, ok bool, err error) {
	s := o.SettleMS
	if s < minClearSettle {
		s = minClearSettle
	}
	ansi, err := o.readSettled(id, s)
	if err != nil {
		return "", false, err
	}
	text, ok = composer.Extract(ansi, agent)
	return text, ok, nil
}

// ListTargets 目标列表：pane.list 的顺序 + workspace / tab 的可读标签。
func (o *Outbox) ListTargets() ([]Target, error) {
	panes, err := o.C.PaneList()
	if err != nil {
		return nil, err
	}
	wsLabel, tabLabel := map[string]string{}, map[string]string{}
	var wl herdr.WorkspaceList
	if o.C.Call("workspace.list", nil, &wl) == nil { // 标签只是好看，拿不到就退回 id
		for _, w := range wl.Workspaces {
			wsLabel[w.WorkspaceID] = orElse(w.Label, fmt.Sprintf("w%d", w.Number))
		}
	}
	var tl herdr.TabList
	if o.C.Call("tab.list", nil, &tl) == nil {
		for _, t := range tl.Tabs {
			tabLabel[t.TabID] = orElse(t.Label, fmt.Sprintf("t%d", t.Number))
		}
	}
	// agent.list 是唯一带 state_change_seq 的口（pane.list 不带），拿不到就算了：
	// 那时候前端的「优先级」排序退化成只按状态分档。
	seq := map[string]uint64{}
	if ags, err := o.C.AgentList(); err == nil {
		for _, a := range ags {
			seq[a.PaneID] = a.Seq
		}
	}
	out := make([]Target, 0, len(panes))
	for _, p := range panes {
		t := Target{
			ID: p.PaneID, Agent: p.Agent, Status: orUnknown(p.AgentStatus),
			Workspace:   orElse(wsLabel[p.WorkspaceID], p.WorkspaceID),
			WorkspaceID: p.WorkspaceID,
			Tab:         orElse(tabLabel[p.TabID], p.TabID),
			Title:       p.Title, CWD: p.CWD, Focused: p.Focused,
			Seq: seq[p.PaneID],
		}
		if o.Seen != nil {
			t.Changed = o.Seen(p.PaneID)
		}
		out = append(out, t)
	}
	return out, nil
}

// GotoResult 是「跳到某个 pane」的结果。
//
// Zoomed 是 tab 级的放大状态（见 herdr.Zoom）；SinglePane 是「这个 tab 只有一个 pane，
// 没什么可放大的」—— 前端要把它和「放大失败」分开说，不然用户会以为按钮没生效。
type GotoResult struct {
	Target       string `json:"target"`
	Zoomed       bool   `json:"zoomed"`
	FocusChanged bool   `json:"focusChanged"`
	SinglePane   bool   `json:"singlePane,omitempty"`
}

// Goto 跳到某个 pane：切焦点（跨 workspace / tab 也一次到位）并按 zoom 开关放大。
//
// 这是给手机用的。手机上未放大的多 pane 布局根本读不了，而快捷键条只能发按键、按键只能
// 表达相对导航（下一个 tab、往右一格），要走到另一个 workspace 里的某个 pane 得盲敲
// 一串 —— 中间每一步的屏幕正好都是那个读不了的状态。socket 这层按 pane_id 寻址，所以
// 「跳过去 + 全屏」是一次调用，界面上就是点一下。
//
// 不接受 Follow：「跳到我此刻激活的那个 pane」没有意义。
func (o *Outbox) Goto(target string, zoom bool) (*GotoResult, error) {
	if target == "" || target == Follow {
		return nil, fmt.Errorf("要跳到哪个 pane：target 得是具体的 pane_id")
	}
	mode := "off"
	if zoom {
		mode = "on"
	}
	z, err := o.C.PaneZoom(target, mode)
	if err != nil {
		return nil, err
	}
	// 用 focused_pane_id 而不是自己传进去的 id：herdr 说了才算数（pane 刚好在这一刻
	// 被关掉之类的情况下，两者会不一样）。
	return &GotoResult{
		Target:       z.FocusedPaneID,
		Zoomed:       z.Zoomed,
		FocusChanged: z.FocusChanged,
		SinglePane:   z.Reason == "single_pane",
	}, nil
}

func orElse(v, def string) string {
	if v != "" {
		return v
	}
	return def
}

// Pull 拉回远端输入框内容（mode=="screen" 时给整屏纯文本，纯调试用）。
func (o *Outbox) Pull(target, mode string) (*PullResult, error) {
	p, followed, err := o.resolve(target)
	if err != nil {
		return nil, err
	}
	ansi, err := o.readSettled(p.PaneID, o.settle())
	if err != nil {
		return nil, err
	}
	r := &PullResult{Info: identify(p, followed)}
	if mode == "screen" {
		r.Screen = composer.ScreenLines(ansi)
	} else {
		var ok bool
		r.Text, ok = composer.Extract(ansi, p.Agent)
		r.NoBox = !ok
	}
	return r, nil
}

// Clear 清空远端输入行。
//
// agent.prompt / pane.send_text 都是**追加**语义，不是「把输入框设为这段文字」。
// 用户在远端按过 Tab 补全或上下键历史之后，输入行上已经有残留，直接投就变成
// 「残留 + 新文本」一起回车。
//
// 实测：N 行输入需要 **2N−1 次** ctrl+u（一次删掉本行内容，再一次删掉这个空行），
// Claude Code 和 Codex 都是这个规律。所以固定次数只够两行 —— 这里按实际残留
// 动态收敛，读一次、按行数补够、再读一次确认。
func (o *Outbox) Clear(id, agent string) (ClearResult, error) {
	if agent == "" {
		// 普通 shell：抽不出可靠的输入框（提示符五花八门，认出来的多半也是提示符行
		// 本身），没法拿「空」当判据；而 zsh / bash 的 ctrl+u 一次就干掉整个 buffer，
		// 打 3 次足够。所以这条路不读屏，也就不受「认不出框」影响 —— shell 照样能投。
		err := o.C.SendKeys(id, []string{"ctrl+u", "ctrl+u", "ctrl+u"})
		return ClearResult{Rounds: 1, Empty: nil}, err
	}
	const rounds = 6
	for i := 1; i <= rounds; i++ {
		cur, ok, err := o.ReadComposer(id, agent)
		if err != nil {
			return ClearResult{}, err
		}
		if !ok {
			// 认不出输入框：**别当成已经空了**。这条路上的 "" 只说明「没看见框」，
			// 不说明「框里没字」；当成空的话下一步就把整段文本追加进一个不知道是
			// 什么的界面里（追加语义，见 Say）。当成「清不空」，拒投。
			f := false
			return ClearResult{Rounds: i - 1, Empty: &f, NoBox: true}, nil
		}
		if cur == "" {
			t := true
			return ClearResult{Rounds: i - 1, Empty: &t}, nil
		}
		need := len(strings.Split(cur, "\n"))*2 - 1
		if need > 24 {
			need = 24
		}
		keys := make([]string, need)
		for j := range keys {
			keys[j] = "ctrl+u"
		}
		if err := o.C.SendKeys(id, keys); err != nil {
			return ClearResult{}, err
		}
	}
	cur, ok, err := o.ReadComposer(id, agent)
	empty := ok && cur == ""
	return ClearResult{Rounds: rounds, Empty: &empty, NoBox: !ok}, err
}

// Draft 把草稿推到远端输入框，但**不回车**。给「双向同步」的本地→远端那半边用。
//
// 只对有 agent 的 pane 干这件事：agent 有真正的输入框，写进去就只是文本。
// 普通 pane 里跑的可能是 vim / 某个选择器，那里的字符是**命令**不是文本，
// 跟着焦点乱推会直接触发操作。
func (o *Outbox) Draft(target, text string) (*DraftResult, error) {
	p, followed, err := o.resolve(target)
	if err != nil {
		return nil, err
	}
	r := &DraftResult{Info: identify(p, followed)}
	if p.Agent == "" {
		r.Skipped = "not-agent"
		return r, nil
	}
	cleared, err := o.Clear(p.PaneID, p.Agent)
	if err != nil {
		return nil, err
	}
	if cleared.NoBox {
		r.Skipped = "no-box"
		return r, nil
	}
	if cleared.Empty != nil && !*cleared.Empty {
		r.Skipped = "busy"
		return r, nil
	}
	if text != "" {
		if err := o.C.SendText(p.PaneID, text, nil); err != nil {
			return nil, err
		}
	}
	r.Pushed = utf8.RuneCountInString(text)
	return r, nil
}

// Say 覆盖式投稿：先清空，再整段提交。
//
// 清空和提交是两次连接（服务端一个连接只处理一个请求），顺序调用天然有序。
func (o *Outbox) Say(target, text string) (*SayResult, error) {
	body := strings.TrimRight(text, " \t\r\n")
	if body == "" {
		return nil, fmt.Errorf("空文本，不发")
	}
	p, followed, err := o.resolve(target)
	if err != nil {
		return nil, err
	}
	cleared, err := o.Clear(p.PaneID, p.Agent)
	if err != nil {
		return nil, err
	}
	// 清不空就别投。追加语义下投进去就是「残留 + 新文本」一起回车。
	// 最常见的原因是那个 pane 正开着一个选择框 / 确认框（agent 会把它画在输入框
	// 那块区域里），此时 agent_status 仍然可能是 idle，光看状态区分不出来 ——
	// 「清不空」反而是个可靠信号。
	if cleared.NoBox {
		return nil, fmt.Errorf("那个 pane 上认不出输入框，没敢投：屏幕上可能正开着全屏界面（分页器 / 编辑器 / 某个控件）。先回到输入框再投。")
	}
	if cleared.Empty != nil && !*cleared.Empty {
		return nil, fmt.Errorf("远端输入框清不空，没敢投：那个 pane 可能正开着选择框 / 确认框。先去按 Esc 收掉再投。")
	}

	if p.Agent != "" {
		// agent.prompt 会按 pane 当前的 bracketed-paste 模式正确编码 Enter
		if err := o.C.AgentPrompt(p.PaneID, body); err != nil {
			return nil, err
		}
	} else {
		if err := o.C.SendText(p.PaneID, body, []string{"enter"}); err != nil {
			return nil, err
		}
	}
	return &SayResult{
		Info: identify(p, followed), Cleared: cleared,
		Chars: utf8.RuneCountInString(body), Lines: len(strings.Split(body, "\n")),
	}, nil
}
