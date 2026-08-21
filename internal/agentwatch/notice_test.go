package agentwatch

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
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

// 整条路走一遍：假 herdr 推 working → idle，攒出一条提示，内容是屏幕上抽出来的那段话。
func TestNoticeOnFinish(t *testing.T) {
	f := newFake(t, screenAnswer, "working", "idle")
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

// 一屏「等你回答」：底下是选择框。
const screenAsk = "" +
	"⏺ 我去改一下那个文件。\n\n" +
	"────────────\n ☐ 改不改\n\nDo you want to proceed?\n\n❯ 1. Yes\n  2. No\n" +
	"────────────\nEnter to select · ↑/↓ to navigate\n\n❯ \n" +
	"  ~/dev/bysir/herdr-web git:(master)\n"

type fake struct {
	sock   string
	screen string
	states []string // 订上之后按顺序推的 agent_status
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

	f := &fake{sock: filepath.Join(dir, "h.sock"), screen: screen, states: states}
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
			"panes": []any{paneObj("idle")},
		}})
	case "pane.read":
		send(map[string]any{"id": req.ID, "result": map[string]any{
			"read": map[string]any{"text": f.screen},
		}})
	case "events.subscribe":
		send(map[string]any{"id": req.ID, "result": map[string]any{"type": "subscription_started"}})
		for _, st := range f.states {
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
