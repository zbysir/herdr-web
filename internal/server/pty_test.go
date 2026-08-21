package server

import (
	"bytes"
	"sync"
	"testing"
	"time"
)

type safeBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// 等到 buf 里有东西（或超时），别用固定 sleep 赌调度
func waitFor(t *testing.T, buf *safeBuf, want string) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := buf.String(); got == want {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	return buf.String()
}

func TestAutoTypeWritesAfterFirstOutput(t *testing.T) {
	buf := &safeBuf{}
	firstOut, done := make(chan struct{}), make(chan struct{})
	defer close(done)
	autoType(buf, "herdr", 0, false, firstOut, done)

	// shell 还没吭声之前不许敲：早敲的那行会被 rc 静默吞掉
	time.Sleep(30 * time.Millisecond)
	if got := buf.String(); got != "" {
		t.Fatalf("shell 还没输出就敲了 %q", got)
	}

	close(firstOut)
	if got := waitFor(t, buf, "herdr\r"); got != "herdr\r" {
		t.Errorf("敲出去的是 %q，want %q", got, "herdr\r")
	}
}

// 空串 = 关掉这个功能（HERDR_WEB_ONCONNECT=）
func TestAutoTypeEmptyDoesNothing(t *testing.T) {
	for _, line := range []string{"", "   "} {
		buf := &safeBuf{}
		firstOut, done := make(chan struct{}), make(chan struct{})
		close(firstOut)
		autoType(buf, line, 0, false, firstOut, done)
		time.Sleep(30 * time.Millisecond)
		close(done)
		if got := buf.String(); got != "" {
			t.Errorf("line=%q 不该敲任何东西，敲了 %q", line, got)
		}
	}
}

// PTY 已经关了就别再往里写
func TestAutoTypeStopsWhenDone(t *testing.T) {
	buf := &safeBuf{}
	firstOut, done := make(chan struct{}), make(chan struct{})
	autoType(buf, "herdr", 10_000, false, firstOut, done)
	close(firstOut)
	close(done)
	time.Sleep(30 * time.Millisecond)
	if got := buf.String(); got != "" {
		t.Errorf("连接都断了还敲 %q", got)
	}
}
