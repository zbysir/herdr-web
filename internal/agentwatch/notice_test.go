package agentwatch

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// 该不该弹，一张表说清。
//
// 最要紧的两条是后面两个：**`→ working` 不弹**（那是你自己刚投进去的回声），
// **`blocked → idle` 不弹**（你刚回答完，它这会儿正要开工，报「跑完了」是假话）。
func TestWorthNotice(t *testing.T) {
	for _, c := range []struct {
		old, cur string
		want     bool
		why      string
	}{
		{"working", "idle", true, "跑完了"},
		{"working", "done", true, "跑完了"},
		// 这条是端到端试出来的：真机上戳一个 agent 说 hi，herdr 报的是 idle → done，
		// **中间压根没有 working**（屏幕识别保守，短任务来不及判成在跑）。
		// 原来要求 done 必须从 working 来，于是短任务一条都弹不出来。
		{"idle", "done", true, "短任务：herdr 没报过 working 就直接 done"},
		{"blocked", "done", true, "回答完接着就跑完了"},
		{"working", "blocked", true, "等你回答"},
		{"idle", "blocked", true, "等你回答（从闲着直接问话也算）"},
		{"idle", "working", false, "开工了：多半是你自己刚投进去的"},
		{"blocked", "working", false, "你回答完它开工了"},
		{"blocked", "idle", false, "你回答完了，它这会儿要开工，不是「跑完了」"},
		{"idle", "idle", false, "没变"},
	} {
		if got := worthNotice(c.old, c.cur); got != c.want {
			t.Errorf("%s → %s：want %v got %v（%s）", c.old, c.cur, c.want, got, c.why)
		}
	}
}

// 防抖等完之后报哪个状态。**这张表是「该弹没弹 / 不该弹却弹了」的全部判据**。
func TestSettled(t *testing.T) {
	for _, c := range []struct {
		want, cur, out string
		why            string
	}{
		{"idle", "idle", "idle", "稳住了，照报"},
		{"done", "done", "done", "稳住了，照报"},
		{"done", "idle", "idle", "收尾抖动：done 之后落到 idle，报现在这个（原来整条丢掉，真·跑完了被吞）"},
		{"idle", "working", "", "又开工了：这一下不是「轮到你了」"},
		{"blocked", "working", "", "你回答完它开工了"},
		{"blocked", "idle", "", "你自己刚回答完，再报「跑完了」是假话"},
		{"idle", "blocked", "blocked", "跑完之后紧接着又问你话：报后面这个"},
		{"done", "", "", "pane 没了"},
	} {
		if got := settled(c.want, c.cur); got != c.out {
			t.Errorf("settled(%q,%q) = %q，want %q（%s）", c.want, c.cur, got, c.out, c.why)
		}
	}
}

// 整条路走一遍：假 herdr 推 working → idle，攒出一条提示，内容是屏幕上抽出来的那段话。
func TestNoticeOnFinish(t *testing.T) {
	f := newFake(t, screenAnswer, "working", "idle")
	f.working = screenBefore // 开工时屏幕上是上一轮的回答，干完才变成 screenAnswer
	w := f.watch(t)

	n := waitNotice(t, w, 1)[0]
	if n.Status != "idle" {
		t.Errorf("状态该是 idle，got %q", n.Status)
	}
	if n.Pane != "w1:pA" || n.Term != "term_A" || n.Agent != "claude" {
		t.Errorf("pane / terminal / agent 没带对：%+v", n)
	}
	if n.Title != "图片识别" {
		t.Errorf("会话标题要带上（弹窗里认得出是哪个 agent），got %q", n.Title)
	}
	if n.At == 0 || n.Seq == 0 {
		t.Errorf("时间和自增号都得有：%+v", n)
	}
	if want := "改完了，两个文件"; !strings.Contains(n.Text, want) {
		t.Errorf("正文该是屏幕上抽出来的那段话（含 %q），got：\n%s", want, n.Text)
	}
	if strings.Contains(n.Text, "❯") || strings.Contains(n.Text, "auto mode") {
		t.Errorf("输入框 / 状态栏不该进正文：\n%s", n.Text)
	}

	// since 是增量取的依据：取过一次之后不该再给同一条
	if rest, seq := w.Notices(n.Seq); len(rest) != 0 || seq != n.Seq {
		t.Errorf("since 之后不该再有东西：%d 条，seq %d", len(rest), seq)
	}
}

// 状态抖一下（working → idle → working）不该弹：claude 干活时会短暂抖回 idle，
// 每抖一次弹一个「跑完了」的话，一个长任务能弹出十几个假提示。
func TestNoticeSkipsJitter(t *testing.T) {
	f := newFake(t, screenAnswer, "working", "idle", "working")
	w := f.watch(t)

	time.Sleep(20 * settleNotice)
	if got, _ := w.Notices(0); len(got) != 0 {
		t.Errorf("抖回去的不该弹，got %d 条：%+v", len(got), got)
	}
}

// 防抖那一会儿里又变了一次（idle → blocked）：报的是**最后**那个状态，而且只报一条。
func TestNoticeReportsLatestStatus(t *testing.T) {
	f := newFake(t, screenAsk, "working", "idle", "blocked")
	f.working = screenBefore
	w := f.watch(t)

	got := waitNotice(t, w, 1)
	time.Sleep(4 * settleNotice) // 再等一会儿，确认没有第二条跟上来
	got, _ = w.Notices(0)
	if len(got) != 1 {
		t.Fatalf("该只有一条（最后那个状态），got %d 条：%+v", len(got), got)
	}
	if got[0].Status != "blocked" {
		t.Errorf("该报最后那个状态 blocked，got %q", got[0].Status)
	}
	if !strings.Contains(got[0].Text, "Do you want to proceed?") {
		t.Errorf("blocked 该抽屏幕底下那个问题，got：\n%s", got[0].Text)
	}
}

// **一条事件都不来也要弹。** 真机上 herdr 只对看得见的 pane 推 `pane.updated`：给背景
// workspace 里的 agent 投一句 hi，`pane.get` 6 秒就报 working、13 秒报 done，而事件流里
// 第一条 40 秒后才来 —— 只听事件的话，最该提醒的那些 pane 一条提示都没有。
// 这条用例把事件全掐掉，只留 pane.list 那条轮询。
func TestNoticeFromPollingWhenNoEvents(t *testing.T) {
	f := newFake(t, screenAnswer)
	f.working = screenBefore
	f.mute = true
	f.setStatus("working")
	w := f.watch(t)

	// 先让轮询把 working 记下来（第一拍只对底不弹），再让它干完
	time.Sleep(10 * pollAgents)
	f.setStatus("idle")

	n := waitNotice(t, w, 1)[0]
	if n.Status != "idle" || n.Pane != "w1:pA" {
		t.Errorf("轮询也该攒出提示，got %+v", n)
	}
}

// **投了一句又按 Esc 取消：不该弹。**
//
// herdr 那边这就是一次干干净净的 `working → idle`，和「跑完了」在状态上毫无区别；而屏幕上
// claude **不留任何「被打断」的记号**（真机抓屏确认，testdata/idle-interrupted.txt：最后一个
// `⏺` 块还是上一轮的回答，你那句话被放回了输入框）。唯一分得清的判据是「这一趟有没有新
// 东西」—— 这里就把开工那屏和干完那屏做成同一屏。
func TestNoticeSkipsCancelled(t *testing.T) {
	f := newFake(t, screenAnswer, "working", "idle") // working 留空 = 两次读到同一屏
	w := f.watch(t)

	time.Sleep(20 * settleNotice)
	if got, _ := w.Notices(0); len(got) != 0 {
		t.Errorf("这一趟一个字都没变，不该弹，got %d 条：%+v", len(got), got)
	}
}

// 同一段话不会弹第二遍（同上，只是这回是连着两次「跑完了」）。
func TestNoticeSkipsSameTextTwice(t *testing.T) {
	f := newFake(t, screenAnswer, "working", "idle", "working", "idle")
	f.working = screenBefore
	w := f.watch(t)

	waitNotice(t, w, 1)
	time.Sleep(20 * settleNotice)
	if got, _ := w.Notices(0); len(got) != 1 {
		t.Errorf("内容没变的第二趟不该再弹，got %d 条", len(got))
	}
}

// **重连进来时 herdr 补推的旧状态不该弹。**
//
// 这条是用出来的：每次重连进页面都会冒出一条「跑完了」，而那个对话其实是很久以前完成的。
// herdr 重画 pane 时会把当前状态（`done` 这种）**重新推一遍事件**，而 `pane.updated` 里
// 没有 `state_change_seq`（实测只有 `revision`）—— 只看「和上次记的不一样」就会把补推当成
// 刚发生。判据是 herdr 那个全局计数有没有往前走。
func TestNoticeSkipsStaleStatusOnReconnect(t *testing.T) {
	f := newFake(t, screenAnswer)
	f.mute = true          // 事件那条路不推，全靠轮询（和重连时的形状一致）
	f.setStatus("working") // 计数 = 1
	w := f.watch(t)

	time.Sleep(10 * pollAgents) // 让 watcher 把「计数 1」记成已处理
	f.pushStale("done")         // 状态变了但**计数没动** = 补推

	time.Sleep(20 * settleNotice)
	if got, _ := w.Notices(0); len(got) != 0 {
		t.Errorf("计数没动的补发不该弹，got %d 条：%+v", len(got), got)
	}

	// 计数往前走了才是真的变化（用 blocked：done → idle 本来就不算「跑完了」）
	f.setStatus("blocked")
	n := waitNotice(t, w, 1)[0]
	if n.Status != "blocked" {
		t.Errorf("真变化该弹，got %+v", n)
	}
}

// **同一个提问不该反复弹。**
//
// 用出来的：agent 停下来问你话，你切过去正在想怎么答，它又弹一遍「等你回答」。根子是
// herdr 的状态识别会抖（同一个对话框一会儿 blocked 一会儿 idle），而你正在打字、屏幕内容
// 一直在变，「内容一样就不弹」那条去重也失效了。判据是**中间有没有重新开过工**。
func TestNoticeSkipsSameThingUntilItWorksAgain(t *testing.T) {
	f := newFake(t, screenAsk)
	f.mute = true
	f.setStatus("working")
	w := f.watch(t)
	time.Sleep(10 * pollAgents)

	f.setStatus("blocked") // 它问你话
	first := waitNotice(t, w, 1)[0]
	if first.Status != "blocked" {
		t.Fatalf("第一条该是 blocked，got %+v", first)
	}

	// 你正在打字：屏幕变了（连内容去重都躲不掉），herdr 的状态又抖了一下
	f.screen = screenAsk + "\n你已经打了半句话"
	f.setStatus("idle")
	time.Sleep(4 * settleNotice)
	f.setStatus("blocked")
	time.Sleep(20 * settleNotice)

	if got, _ := w.Notices(0); len(got) != 1 {
		t.Errorf("同一个提问不该再弹，got %d 条：%+v", len(got), got)
	}

	// 你答完了、它跑起来、**换了个问题**再来问你 —— 这一条是该弹的
	f.setStatus("working")
	time.Sleep(4 * pollAgents)
	f.screen = strings.ReplaceAll(screenAsk, "Do you want to proceed?", "换一个问题：要不要顺手把测试也跑了？")
	f.setStatus("blocked")
	got := waitNotice(t, w, 2)
	if len(got) != 2 {
		t.Errorf("重新开过工之后的提问该弹，got %d 条", len(got))
	}
}

// 对底那条路（重新订上时的 pane.list）**不弹**：那些变化可能是 herdr 停机期间发生的，
// 当场弹一片「刚刚跑完了」就是在编时间。
func TestNoticeNotFromReconcile(t *testing.T) {
	shortSettle(t)
	w := New("/nonexistent.sock", "")
	w.observe(pane("w1:pA", "term_A", "working"))
	w.observe(pane("w1:pA", "term_A", "idle")) // 对底时发现状态变了：打时间，但不弹
	if w.At("w1:pA") == 0 {
		t.Error("对底发现状态变了还是要打时间")
	}
	time.Sleep(2 * settleNotice)
	if got, _ := w.Notices(0); len(got) != 0 {
		t.Errorf("对底不该弹，got %+v", got)
	}
}

// 环满了先丢最老的，seq 不回头。
func TestNoticeRingKeepsNewest(t *testing.T) {
	w := New("/nonexistent.sock", "")
	for i := 0; i < keepNotices+5; i++ {
		// 每条之间都「重新开过工」：不然同类去重会把后面的全挡掉（那条规则见 emit）
		w.observe(pane("w1:pA", "term_A", "working"))
		w.emit(pane("w1:pA", "term_A", "idle"), "idle")
	}
	got, seq := w.Notices(0)
	if len(got) != keepNotices {
		t.Errorf("环里该只留 %d 条，got %d", keepNotices, len(got))
	}
	if seq != uint64(keepNotices+5) {
		t.Errorf("seq 该一直往上走，got %d", seq)
	}
	if got[0].Seq != 6 {
		t.Errorf("丢的该是最老的那几条，剩下的第一条 seq=%d", got[0].Seq)
	}
}

/* --------------------------------------------------------------- 假 herdr */

// 一屏「跑完了」：最后一个 ⏺ 块是回答，后面是 spinner、输入框、状态栏。
const screenAnswer = "" +
	"⏺ 先看一眼那个文件。\n\n  Ran 2 shell commands\n\n" +
	"⏺ 改完了，两个文件：internal/x.go 和 web/src/y.ts。\n\n  测试全过。\n\n" +
	"✻ Baked for 20s\n\n────────────\n❯ \n────────────\n" +
	"  ~/dev/bysir/herdr-web git:(master)\n  ⏵⏵ auto mode on (shift+tab to cycle)\n"

// 开工时那一屏：上一轮的回答还挂在上面（真机上就是这样，见 testdata/idle-interrupted.txt）。
const screenBefore = "" +
	"⏺ 上一轮的回答：那个 bug 修好了。\n\n✻ Baked for 12s\n\n────────────\n❯ \n────────────\n" +
	"  ~/dev/bysir/herdr-web git:(master)\n"

// 一屏「等你回答」：底下是选择框。
const screenAsk = "" +
	"⏺ 我去改一下那个文件。\n\n" +
	"────────────\n ☐ 改不改\n\nDo you want to proceed?\n\n❯ 1. Yes\n  2. No\n" +
	"────────────\nEnter to select · ↑/↓ to navigate\n\n❯ \n" +
	"  ~/dev/bysir/herdr-web git:(master)\n"

type fake struct {
	sock   string
	screen string
	// working 是「正在干活时」那一屏（空 = 和 screen 同一屏）。
	// 真机上开工和干完看到的当然不是同一屏，而「一模一样」恰恰是取消/打断的特征 ——
	// 两个用例都要能造出来。
	working string
	states  []string // 订上之后按顺序推的 agent_status（事件那条路）

	// mu/list 是**轮询**那条路：pane.list 每次回 list 里的当前值，测试自己改它。
	// 真机上背景 pane 的变化就只能这么看见（herdr 不给它们推事件）。
	mu   sync.Mutex
	list string
	seq  uint64 // agent.list 的 state_change_seq：herdr 每次**真的**状态变化才推高
	mute bool   // true = 订上之后一条事件都不推（模拟「看不见的 pane」）

	// polls 每答一次 agent.list 塞一个。订阅那条路靠它**等轮询跑完一拍**才开始推事件，
	// 见 serve 里 events.subscribe 那段 —— 原来是干等 8 拍的 sleep，CI 上偶发失败。
	polls chan struct{}
}

// setStatus 换状态并推高计数 —— 真 herdr 就是这样。
func (f *fake) setStatus(st string) {
	f.mu.Lock()
	f.list = st
	f.seq++
	f.mu.Unlock()
}

// pushStale 只换状态**不推计数**：herdr 重画 pane 时把当前状态重新推一遍就是这样，
// 「重连进来弹出一条很久以前的『跑完了』」就是这么来的。
func (f *fake) pushStale(st string) {
	f.mu.Lock()
	f.list = st
	f.mu.Unlock()
}

func (f *fake) stateSeq() uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.seq == 0 {
		return 1
	}
	return f.seq
}

func (f *fake) status() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.list == "" {
		return "idle"
	}
	return f.list
}

func newFake(t *testing.T, screen string, states ...string) *fake {
	t.Helper()
	// 放 /tmp 而不是 t.TempDir()：unix socket 路径有 104 字节上限，macOS 的 TempDir
	// 前缀又长又深（见 socket_test.go 里同一条注释）。
	dir, err := os.MkdirTemp("/tmp", "aw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	f := &fake{
		sock: filepath.Join(dir, "h.sock"), screen: screen, states: states, list: "idle",
		polls: make(chan struct{}, 64),
	}
	ln, err := net.Listen("unix", f.sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(conn)
		}
	}()
	return f
}

// shortSettle 把防抖调到几十毫秒 —— 真那 2.5 秒在单测里只是干等。
func shortSettle(t *testing.T) {
	t.Helper()
	old := settleNotice
	settleNotice = 40 * time.Millisecond
	t.Cleanup(func() { settleNotice = old })
}

func (f *fake) watch(t *testing.T) *Watcher {
	t.Helper()
	shortSettle(t)
	oldPoll := pollAgents
	pollAgents = 30 * time.Millisecond
	t.Cleanup(func() { pollAgents = oldPoll })
	w := New(f.sock, "")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	w.Start(ctx)
	return w
}

func (f *fake) serve(conn net.Conn) {
	defer conn.Close()
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return
	}
	var req struct {
		ID     string `json:"id"`
		Method string `json:"method"`
	}
	if json.Unmarshal(line, &req) != nil {
		return
	}
	send := func(v any) {
		b, _ := json.Marshal(v)
		_, _ = conn.Write(append(b, '\n'))
	}
	paneObj := func(status string) map[string]any {
		return map[string]any{
			"pane_id": "w1:pA", "terminal_id": "term_A", "agent": "claude",
			"agent_status": status, "terminal_title_stripped": "图片识别",
		}
	}
	switch req.Method {
	case "pane.list":
		send(map[string]any{"id": req.ID, "result": map[string]any{
			"panes": []any{paneObj(f.status())},
		}})
	case "agent.list":
		send(map[string]any{"id": req.ID, "result": map[string]any{
			"agents": []any{map[string]any{
				"pane_id": "w1:pA", "agent": "claude",
				"agent_status": f.status(), "state_change_seq": f.stateSeq(),
			}},
		}})
		select { // 告诉订阅那条路「轮询又跑了一拍」；满了就丢，别把 serve 卡住
		case f.polls <- struct{}{}:
		default:
		}
	case "pane.read":
		text := f.screen
		if f.status() == "working" && f.working != "" {
			text = f.working
		}
		send(map[string]any{"id": req.ID, "result": map[string]any{
			"read": map[string]any{"text": text},
		}})
	case "events.subscribe":
		send(map[string]any{"id": req.ID, "result": map[string]any{"type": "subscription_started"}})
		if f.mute {
			time.Sleep(10 * time.Second) // 一条都不推：模拟背景 pane
			return
		}
		// 先让 watcher 轮**两拍**再推事件。
		//
		// 为什么必须等：那一拍会把这个终端当下的状态计数记成「已处理」（真机上是进程起来
		// 时记的），没记上的话第一次状态变化会被当成「计数没动」而整条丢掉（判据见
		// notice.go 的 emit）—— 表现就是这个用例超时报「没攒出提示」。
		//
		// 为什么不是 sleep：原来写的是 `time.Sleep(8 * pollAgents)`（240ms）干等，在 CI
		// 上偶发不够 —— 实测本地 20 次里也会挂 1 次。现在改成**等 agent.list 被问到**
		// （轮询每拍都会问一次，见 poller），拿真事件同步，不猜时间。等两次是因为第一拍的
		// 「记成已处理」是在响应之后写的：第二次请求进来时，第一拍必然已经收尾。
		for i := 0; i < 2; i++ {
			select {
			case <-f.polls:
			case <-time.After(5 * time.Second):
				return // 轮询压根没来：让用例自己超时报错，比在这儿干等更说得清
			}
		}
		for _, st := range f.states {
			// 轮询那条路（pane.list）要跟着一起变 —— 真机上两条路看到的是同一个 herdr，
			// 只让事件动的话，轮询会拿旧状态把刚判掉的抖动又「纠正」回来。
			f.setStatus(st)
			send(map[string]any{"event": "pane_updated", "data": map[string]any{"pane": paneObj(st)}})
			time.Sleep(2 * time.Millisecond) // 一串状态挤在防抖窗口里，这就是要验的
		}
		time.Sleep(10 * time.Second) // 订阅是长连，别主动关（关了 watcher 会去重连）
	}
}

func waitNotice(t *testing.T, w *Watcher, n int) []Notice {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got, _ := w.Notices(0); len(got) >= n {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("5 秒内没攒出 %d 条提示", n)
	return nil
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
