package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/zbysir/herdr-web/internal/auth"
	"github.com/zbysir/herdr-web/internal/config"
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

// 探活的回音。前端锁屏回来时就靠这一帧判断连接是不是僵的（见 web/src/term/session.ts
// 的 probe）：**没人回它的表现是「每次解锁都白重连一次」**，屏幕上看不出异常，所以
// 这条要端到端地验，而不是去测那个 switch 里的一行。
func TestPTYAnswersProbe(t *testing.T) {
	store, err := auth.New(auth.Config{Dir: t.TempDir(), TrustLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	// OnConnect 留空：这个测试只关心那一帧，不想真起一个 herdr
	s := &Server{Cfg: &config.Config{Shell: "/bin/sh"}, Auth: store}
	srv := httptest.NewServer(http.HandlerFunc(s.handlePTY))
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/pty?cols=80&rows=24", nil)
	if err != nil {
		t.Fatalf("连不上 /pty：%v", err)
	}
	defer conn.Close()

	// shell 的输出（二进制帧）会和控制帧混在一起，所以是「读到为止」而不是读固定几帧
	waitFor := func(what string) {
		t.Helper()
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		for {
			typ, data, err := conn.ReadMessage()
			if err != nil {
				t.Fatalf("等 %s 的时候断了：%v", what, err)
			}
			if typ != websocket.TextMessage {
				continue
			}
			var m struct{ T string }
			if json.Unmarshal(data, &m) == nil && m.T == what {
				return
			}
		}
	}

	waitFor("ready")
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"t":"p"}`)); err != nil {
		t.Fatal(err)
	}
	waitFor("p")
}
