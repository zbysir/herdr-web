package remote

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/term"
)

// resetModes 把 herdr 在对面打开的那些终端模式在**本地**关掉：鼠标上报（1000/1002/1003/1006）、
// 焦点上报、bracketed paste、kitty 键盘协议（`CSI < u` 弹栈、`CSI = 0 ; 1 u` 清零）、
// 光标键 / 小键盘应用模式、备用屏、隐藏光标、SGR。
//
// 连接一断对面就没人来关它们了，不复位的话本地 shell 里一动鼠标就往命令行灌
// `35;120;36M`（网页那边踩过同一个坑）。终端不认的序列会被忽略，多发不亏。
const resetModes = "\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l\x1b[?1015l" +
	"\x1b[?1004l\x1b[?2004l\x1b[<99u\x1b[=0;1u\x1b[?1l\x1b>" +
	"\x1b[?1049l\x1b[?25h\x1b[0m"

// 探活：对面每 25 秒 ping 一次，我们每 probeEvery 发一帧 `{"t":"p"}`（服务端回音）。
// deadAfter 内什么都没收到就当断了 —— 锁屏 / 换网之后 TCP 常常是僵的，不主动探的话
// 要等内核超时（几分钟到十几分钟），人面对的是一个敲什么都没反应的窗口。
const (
	probeEvery = 15 * time.Second
	deadAfter  = 40 * time.Second
	writeWait  = 10 * time.Second
)

type outcome int

const (
	outExit    outcome = iota // 对面的 shell 退出了
	outQuit                   // 人按了 ~.
	outDropped                // 断了，可以重连
	outAuth                   // 凭据不认了（撤销 / 到了重验）
	outFatal                  // 重连也没用（session 名不对、会话数满了……）
)

type result struct {
	kind outcome
	code int
	msg  string
}

// Run 连上去，直到对面的 shell 退出或者人按了 `~.`。返回对面 shell 的退出码。
func Run(t Target) (int, error) {
	in, out := int(os.Stdin.Fd()), int(os.Stdout.Fd())
	if !term.IsTerminal(in) || !term.IsTerminal(out) {
		return 1, errors.New("要在终端里跑（标准输入输出不是终端）")
	}
	c, err := ensure(t)
	if err != nil {
		return 1, err
	}

	old, err := term.MakeRaw(in)
	if err != nil {
		return 1, err
	}
	restore := func() {
		_, _ = os.Stdout.WriteString(resetModes)
		_ = term.Restore(in, old)
	}
	defer restore()

	// 被 kill / 关窗口时也要把本地终端还回去
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		<-sig
		restore()
		os.Exit(1)
	}()

	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)

	// stdin 只有一个读者，一直读到进程退出；重连期间的键也从这儿过（看有没有 ~. / Ctrl+C）
	input := make(chan []byte, 64)
	go func() {
		buf := make([]byte, 8<<10)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				input <- append([]byte(nil), buf[:n]...)
			}
			if err != nil {
				close(input)
				return
			}
		}
	}()

	esc := newEscaper()
	wait := 500 * time.Millisecond
	for {
		r := session(t, c, input, esc, winch, func() { wait = 500 * time.Millisecond })
		switch r.kind {
		case outExit:
			return r.code, nil
		case outQuit:
			say("连接已断开")
			return 0, nil
		case outFatal:
			return 1, errors.New(r.msg)
		case outAuth:
			// 回到普通模式问配对码（raw 模式下回显和回车都不对）
			_ = term.Restore(in, old)
			c, err = ensureWith(t, input)
			if err != nil {
				return 1, err
			}
			if old, err = term.MakeRaw(in); err != nil {
				return 1, err
			}
			continue
		}
		// outDropped：等一会儿再连，期间 ~. / Ctrl+C 能退出
		_, _ = os.Stdout.WriteString(resetModes)
		say(fmt.Sprintf("连接断了，%s 后重连（~. 或 Ctrl+C 退出）", wait))
		timer := time.NewTimer(wait)
	backoff:
		for {
			select {
			case <-timer.C:
				break backoff
			case b, ok := <-input:
				if !ok {
					return 1, nil
				}
				if _, quit := esc.feed(b); quit || strings.ContainsAny(string(b), "\x03\x04") {
					timer.Stop()
					say("不连了")
					return 1, nil
				}
			}
		}
		wait = min(wait*2, 8*time.Second)
	}
}

// ensureWith 在 stdin 已经被 input 那个 goroutine 占着的情况下问配对码：
// 再开一个 bufio 读 os.Stdin 会和它抢字节，所以把 input 接成一个 Reader 喂给 pair。
func ensureWith(t Target, input <-chan []byte) (Cred, error) {
	pr, pw := io.Pipe()
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-stop:
				_ = pw.Close()
				return
			case b, ok := <-input:
				if !ok {
					_ = pw.Close()
					return
				}
				if _, err := pw.Write(b); err != nil {
					return
				}
			}
		}
	}()
	stdinReader = pr
	defer func() { stdinReader = os.Stdin }()
	return ensure(t)
}

// say 在 raw 模式下打一行提示（要自己补 \r）
func say(s string) { fmt.Fprintf(os.Stderr, "\r\n[herdr-web] %s\r\n", s) }

func session(t Target, c Cred, input <-chan []byte, esc *escaper, winch <-chan os.Signal, connected func()) result {
	cols, rows, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || cols <= 0 || rows <= 0 {
		cols, rows = 120, 34
	}
	q := url.Values{"cols": {strconv.Itoa(cols)}, "rows": {strconv.Itoa(rows)}}
	if t.Session != "" {
		q.Set("session", t.Session)
	}
	u := "ws" + strings.TrimPrefix(t.Origin, "http") + "/pty?" + q.Encode()
	h := http.Header{"Authorization": {"Bearer " + c.Token}, "User-Agent": {userAgent()}}
	d := websocket.Dialer{HandshakeTimeout: 15 * time.Second, Proxy: http.ProxyFromEnvironment}
	conn, resp, err := d.Dial(u, h)
	if err != nil {
		if resp == nil {
			return result{kind: outDropped}
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		resp.Body.Close()
		msg := strings.TrimSpace(string(body))
		switch resp.StatusCode {
		case http.StatusUnauthorized:
			return result{kind: outAuth}
		case http.StatusForbidden, http.StatusServiceUnavailable, http.StatusMisdirectedRequest:
			return result{kind: outFatal, msg: fmt.Sprintf("%s：%s", resp.Status, msg)}
		}
		return result{kind: outDropped}
	}
	defer conn.Close()
	connected()

	// 写者有三个（键、改尺寸、探活），gorilla 不许并发写 —— 同服务端 wsWriter 那条
	var mu sync.Mutex
	write := func(typ int, b []byte) error {
		mu.Lock()
		defer mu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
		return conn.WriteMessage(typ, b)
	}
	alive := func() { _ = conn.SetReadDeadline(time.Now().Add(deadAfter)) }
	alive()
	conn.SetPingHandler(func(data string) error {
		alive()
		mu.Lock()
		defer mu.Unlock()
		return conn.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(writeWait))
	})

	done := make(chan result, 1)
	go func() {
		for {
			typ, data, err := conn.ReadMessage()
			if err != nil {
				done <- result{kind: outDropped}
				return
			}
			alive()
			if typ == websocket.BinaryMessage {
				_, _ = os.Stdout.Write(data)
				continue
			}
			var m struct {
				T, Msg string
				Code   int
			}
			if json.Unmarshal(data, &m) != nil {
				continue
			}
			switch m.T {
			case "exit":
				// 服务端发这一帧时常常还没 Wait 到那个进程，码是 -1（「不知道」）——
				// 原样 os.Exit(-1) 会变成 255，看着像出了错
				done <- result{kind: outExit, code: max(m.Code, 0)}
				return
			case "fatal":
				done <- result{kind: outFatal, msg: m.Msg}
				return
			}
		}
	}()

	probe := time.NewTicker(probeEvery)
	defer probe.Stop()
	for {
		select {
		case r := <-done:
			return r
		case b, ok := <-input:
			if !ok {
				return result{kind: outQuit}
			}
			b, quit := esc.feed(b)
			if len(b) > 0 && write(websocket.BinaryMessage, b) != nil {
				return result{kind: outDropped}
			}
			if quit {
				_ = write(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				return result{kind: outQuit}
			}
		case <-winch:
			if w, h, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
				m, _ := json.Marshal(map[string]any{"t": "r", "cols": w, "rows": h})
				_ = write(websocket.TextMessage, m)
			}
		case <-probe.C:
			_ = write(websocket.TextMessage, []byte(`{"t":"p"}`))
		}
	}
}
