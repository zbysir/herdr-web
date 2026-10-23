package relay

import (
	"bytes"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

/* ------------------------------------------------------------------ SNI */

var errPeeked = errors.New("peeked")

// peekSNI 读出 ClientHello 里的 SNI，**不完成握手**；返回读掉的那些字节，调用方要把它们
// 原样补在后面转出去。
//
// 借标准库的 TLS 状态机来解析（GetConfigForClient 拿到 ClientHello 就喊停），不自己解析
// TLS 记录：ClientHello 可能拆在好几个记录里、扩展的排布各家浏览器都不一样，手写的
// 解析器正是会在某个浏览器上静默认不出 SNI 的那种代码。
func peekSNI(r io.Reader) (string, []byte, error) {
	var buf bytes.Buffer
	var sni string
	var got bool
	err := tls.Server(readOnly{io.TeeReader(r, &buf)}, &tls.Config{
		GetConfigForClient: func(h *tls.ClientHelloInfo) (*tls.Config, error) {
			sni, got = h.ServerName, true
			return nil, errPeeked
		},
	}).Handshake()
	if !got {
		return "", nil, err
	}
	return sni, buf.Bytes(), nil
}

// readOnly 只给 peekSNI 用：握手被我们在半路喊停，那之前标准库不会往回写任何东西 ——
// 万一写了，报错比悄悄吞掉强。
type readOnly struct{ r io.Reader }

func (c readOnly) Read(p []byte) (int, error)     { return c.r.Read(p) }
func (readOnly) Write([]byte) (int, error)        { return 0, io.ErrClosedPipe }
func (readOnly) Close() error                     { return nil }
func (readOnly) LocalAddr() net.Addr              { return nil }
func (readOnly) RemoteAddr() net.Addr             { return nil }
func (readOnly) SetDeadline(time.Time) error      { return nil }
func (readOnly) SetReadDeadline(time.Time) error  { return nil }
func (readOnly) SetWriteDeadline(time.Time) error { return nil }

// replayConn 把 peekSNI 读掉的那段字节补回到连接前面。
type replayConn struct {
	net.Conn
	r io.Reader
}

func (c *replayConn) Read(p []byte) (int, error) { return c.r.Read(p) }

func withPrefix(c net.Conn, prefix []byte) net.Conn {
	return &replayConn{Conn: c, r: io.MultiReader(bytes.NewReader(prefix), c)}
}

/* ------------------------------------------------------------------ WebSocket → 字节流 */

// wsStream 把一条 WebSocket 当成字节流用（yamux 要的是 io.ReadWriteCloser）。
//
// 主机到中继这一段为什么要包 WebSocket 而不是直接一条 TLS：公司网络里常见那种只放行
// HTTP(S) 的出口代理，裸 TLS 上跑自定义协议过不去，WebSocket 过得去。
//
// 写要上锁：gorilla 的连接**不能并发写**，撞上了是 panic、整个进程退出（CLAUDE.md 里
// wsWriter 那条踩过一次）。yamux 自己只有一个发送协程，但关闭时还有一次写，别赌。
type wsStream struct {
	ws *websocket.Conn
	mu sync.Mutex
	r  io.Reader
}

func newWSStream(ws *websocket.Conn) *wsStream { return &wsStream{ws: ws} }

func (s *wsStream) Read(p []byte) (int, error) {
	for {
		if s.r == nil {
			_, r, err := s.ws.NextReader()
			if err != nil {
				return 0, err
			}
			s.r = r
		}
		n, err := s.r.Read(p)
		if err == io.EOF {
			s.r = nil
			if n > 0 {
				return n, nil
			}
			continue
		}
		return n, err
	}
}

func (s *wsStream) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// 写也要有个期限：对面断网时 TCP 缓冲填满，WriteMessage 会一直阻塞（pty.go 那条坑）
	_ = s.ws.SetWriteDeadline(time.Now().Add(30 * time.Second))
	if err := s.ws.WriteMessage(websocket.BinaryMessage, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (s *wsStream) Close() error { return s.ws.Close() }

/* ------------------------------------------------------------------ 双向拷 */

// pipe 在两条连接之间双向拷字节，任何一边断了就两边一起关。
func pipe(a, b net.Conn) {
	done := make(chan struct{}, 2)
	cp := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		done <- struct{}{}
	}
	go cp(a, b)
	go cp(b, a)
	<-done
	a.Close()
	b.Close()
	<-done
}
