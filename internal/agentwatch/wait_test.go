package agentwatch

import (
	"context"
	"testing"
	"time"

	"github.com/zbysir/herdr-web/internal/herdr"
)

// 长轮询：挂着的时候来一条就当场回，不等满时限；没有新的就等满再回空的；对面断了立刻回。
func TestWaitNotices(t *testing.T) {
	w := New("", "")

	go func() {
		time.Sleep(50 * time.Millisecond)
		w.mu.Lock()
		w.addLocked(Notice{Pane: "p1", Status: "blocked"})
		w.mu.Unlock()
	}()
	t0 := time.Now()
	list, seq := w.WaitNotices(context.Background(), 0, 5*time.Second)
	if len(list) != 1 || seq != 1 || time.Since(t0) > 2*time.Second {
		t.Fatalf("来了一条应该当场回：%v seq=%d 用了 %v", list, seq, time.Since(t0))
	}

	// since 已经是最新：没有新的，等满时限回空的，seq 原样带回
	t0 = time.Now()
	list, seq = w.WaitNotices(context.Background(), 1, 100*time.Millisecond)
	if len(list) != 0 || seq != 1 || time.Since(t0) < 90*time.Millisecond {
		t.Fatalf("没有新的应该等满再回空的：%v seq=%d 用了 %v", list, seq, time.Since(t0))
	}

	// 对面断了（ctx 取消）：别一直挂着
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()
	t0 = time.Now()
	w.WaitNotices(ctx, 1, 5*time.Second)
	if time.Since(t0) > 2*time.Second {
		t.Fatalf("ctx 断了应该立刻回，用了 %v", time.Since(t0))
	}
}

// 状态一览的长轮询：头一回见到、状态变了都算「变了」；没变（同一个状态又推一遍）不算。
func TestWaitStatus(t *testing.T) {
	w := New("", "")
	p := herdr.Pane{PaneID: "p1", TerminalID: "t1", AgentStatus: "working"}
	w.observe(p)
	v1 := w.WaitStatus(context.Background(), 0, time.Second)
	if v1 == 0 {
		t.Fatal("头一回见到这个 agent 应该算变了")
	}

	// herdr 重画时会把同一个状态再推一遍：不该叫醒
	w.observe(p)
	t0 := time.Now()
	if v := w.WaitStatus(context.Background(), v1, 100*time.Millisecond); v != v1 || time.Since(t0) < 90*time.Millisecond {
		t.Fatalf("状态没变不该回新版本：%d→%d，用了 %v", v1, v, time.Since(t0))
	}

	go func() {
		time.Sleep(50 * time.Millisecond)
		p.AgentStatus = "blocked"
		w.observe(p)
	}()
	t0 = time.Now()
	if v := w.WaitStatus(context.Background(), v1, 5*time.Second); v == v1 || time.Since(t0) > 2*time.Second {
		t.Fatalf("变成等你了应该当场回：%d→%d，用了 %v", v1, v, time.Since(t0))
	}
}

// 标题变了（agent 给会话改了名）也要叫醒一览 —— 一览里每行的正文就是标题
func TestWaitStatusTitle(t *testing.T) {
	w := New("", "")
	p := herdr.Pane{PaneID: "p1", TerminalID: "t1", AgentStatus: "working", Title: "旧标题"}
	w.observe(p)
	v1 := w.WaitStatus(context.Background(), 0, time.Second)
	p.Title = "新标题"
	w.observe(p)
	if v := w.WaitStatus(context.Background(), v1, 100*time.Millisecond); v == v1 {
		t.Fatal("标题变了应该算一览变了")
	}
}
