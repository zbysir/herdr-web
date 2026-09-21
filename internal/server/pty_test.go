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

// 「结尾那个回车要隔一会儿才写进 PTY」这条（`gap`）。
//
// 为什么端到端验、为什么这一段必须等在**服务端**：见 web/src/term/keysend.ts。
// 简单说，快捷键条上 `text:/clear enter` 那种键，回车和前面那串字挤在一起的话
// codex 会把它当成粘贴里的换行 —— 命令不提交，而屏幕上一个字都不报（用户报的
// 「claude 下好好的，codex 按了只换行」）。前端把两帧背靠背发出来，间隔是这一侧
// 撑开的，所以**这一行 sleep 掉了是完全静默的**：typecheck 过、前端测试过、
// 只有真在 codex 上按那个键才看得出来。
func TestPTYGapDelaysInput(t *testing.T) {
	store, err := auth.New(auth.Config{Dir: t.TempDir(), TrustLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Cfg: &config.Config{Shell: "/bin/sh"}, Auth: store}
	srv := httptest.NewServer(http.HandlerFunc(s.handlePTY))
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/pty?cols=80&rows=24", nil)
	if err != nil {
		t.Fatalf("连不上 /pty：%v", err)
	}
	defer conn.Close()

	// 等 ready，顺手把开场那堆输出（提示符）读掉
	var seen []byte
	readUntil := func(what byte, limit time.Duration) time.Duration {
		t.Helper()
		start := time.Now()
		_ = conn.SetReadDeadline(time.Now().Add(limit))
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				t.Fatalf("等 %q 回显的时候断了：%v", string(what), err)
			}
			seen = append(seen, data...)
			if bytes.IndexByte(data, what) >= 0 {
				return time.Since(start)
			}
		}
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		typ, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("等 ready 的时候断了：%v", err)
		}
		var m struct{ T string }
		if typ == websocket.TextMessage && json.Unmarshal(data, &m) == nil && m.T == "ready" {
			break
		}
	}

	// 两帧**背靠背**发出去（前端就是这么发的），间隔完全由服务端撑开
	const gap = 400 * time.Millisecond
	for _, f := range []string{`{"t":"i","d":"Q"}`, `{"t":"i","d":"Z","gap":400}`} {
		if err := conn.WriteMessage(websocket.TextMessage, []byte(f)); err != nil {
			t.Fatal(err)
		}
	}

	// tty 的行规程会把敲进去的字回显出来，拿它当「PTY 收到了」的时刻
	if d := readUntil('Q', 3*time.Second); d > gap/2 {
		t.Fatalf("前面那串字也被拖住了（%v）—— gap 不该影响它", d)
	}
	if d := readUntil('Z', 3*time.Second); d < gap*3/4 {
		t.Fatalf("回车只隔了 %v 就写进去了（要 ≥ %v）：gap 没生效，codex 那边会把它当换行", d, gap)
	}
}
