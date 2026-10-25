package relay

import (
	"crypto/rand"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"
)

// Relay 是跑在 VPS 上的那一头。
//
// 一个监听（通常 :443）上收所有连接，先读 ClientHello 里的 SNI：
//
//   - SNI 是中继自己的域名 → 自己终止 TLS，这是**控制面**（主机从这儿连进来）；
//   - SNI 是注册过的主机名 → 开一条通往那台主机的子流，连同已经读出来的 ClientHello
//     **原样**转过去，之后只拷字节。TLS 在主机上终止，中继看不到明文；
//   - 别的一律断开，不回任何东西（不给扫描器一个指纹）。
type Relay struct {
	// Domain 是中继自己的名字（控制面，如 relay.example.com）。
	Domain string
	// TLS 是控制面那张证书的配置（autocert 或文件）。nil = 控制面不走这个口，由前面那层
	// （VPS 上的 Traefik）终止 TLS 后转到 ControlHandler —— 那时 SNI 是中继自己名字的连接
	// 根本不会到这儿，到了就断开。
	TLS   *tls.Config
	Store Store
	Log   func(format string, args ...any)

	mu       sync.Mutex
	sessions map[string]*yamux.Session

	ctl  chan net.Conn
	once sync.Once
}

func (r *Relay) logf(format string, args ...any) {
	if r.Log != nil {
		r.Log(format, args...)
		return
	}
	log.Printf(format, args...)
}

func (r *Relay) init() {
	r.once.Do(func() {
		r.sessions = map[string]*yamux.Session{}
		r.ctl = make(chan net.Conn)
		if r.TLS != nil {
			go r.serveControl()
		}
		go r.sweep()
	})
}

// Serve 在 ln 上收连接（ln 是**裸 TCP**，TLS 由各条路自己处理）。
func (r *Relay) Serve(ln net.Listener) error {
	r.init()
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		go r.route(c)
	}
}

// helloTimeout：连上来多久之内必须把 ClientHello 发完。不设的话一个只连不说话的客户端
// 能占着一个协程和一个文件描述符一直不走。
const helloTimeout = 10 * time.Second

func (r *Relay) route(c net.Conn) {
	_ = c.SetReadDeadline(time.Now().Add(helloTimeout))
	sni, head, err := peekSNI(c)
	_ = c.SetReadDeadline(time.Time{})
	if err != nil {
		c.Close()
		return
	}
	sni = strings.ToLower(sni)
	conn := withPrefix(c, head)

	if sni == r.Domain {
		if r.TLS == nil {
			c.Close()
			return
		}
		r.ctl <- conn
		return
	}
	sess := r.session(sni)
	if sess == nil {
		sess = r.session(portParent(sni))
	}
	if sess == nil {
		c.Close() // 没注册 / 主机不在线：一律安静地断开
		return
	}
	st, err := sess.Open()
	if err != nil {
		c.Close()
		return
	}
	if err := writeHeader(st, streamHeader{Src: c.RemoteAddr().String()}); err != nil {
		st.Close()
		c.Close()
		return
	}
	pipe(conn, st)
}

func (r *Relay) session(host string) *yamux.Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.sessions[host]
	if s != nil && s.IsClosed() {
		delete(r.sessions, host)
		return nil
	}
	return s
}

// Online 现在连着的主机名（给命令行 / 日志看）。
func (r *Relay) Online() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for h, s := range r.sessions {
		if !s.IsClosed() {
			out = append(out, h)
		}
	}
	return out
}

// sweep 定期把「已经被删掉 / 换了钥匙」的主机的隧道掐掉。
// 不做的话 `herdr-relay remove` 只对下一次重连生效，而那台主机可能几天都不掉线。
func (r *Relay) sweep() {
	for range time.Tick(30 * time.Second) {
		r.mu.Lock()
		names := make([]string, 0, len(r.sessions))
		for h := range r.sessions {
			names = append(names, h)
		}
		r.mu.Unlock()
		for _, h := range names {
			if got, err := r.Store.Lookup(h); err == nil && got == nil {
				r.drop(h, nil)
				r.logf("中继：%s 已经被删掉，掐掉它的隧道", h)
			}
		}
	}
}

func (r *Relay) drop(host string, only *yamux.Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s := r.sessions[host]; s != nil && (only == nil || s == only) {
		s.Close()
		delete(r.sessions, host)
	}
}

/* ------------------------------------------------------------------ 控制面 */

type chanListener struct {
	ch   chan net.Conn
	addr net.Addr
}

func (l chanListener) Accept() (net.Conn, error) {
	c, ok := <-l.ch
	if !ok {
		return nil, net.ErrClosed
	}
	return c, nil
}
func (chanListener) Close() error     { return nil }
func (l chanListener) Addr() net.Addr { return l.addr }

// ControlHandler 是控制面（主机连进来的那条 WebSocket）。中继自己终止 TLS 时由 Serve 内部
// 挂上；前面另有一层终止 TLS 时（TLS == nil），调用方自己把它挂在一个明文监听上。
func (r *Relay) ControlHandler() http.Handler {
	r.init()
	return r.controlMux()
}

// controlMux 不碰 init：serveControl 就是在 init 里起的，两边互相调用的话靠的是
// 「恰好在另一个协程里」才没锁死。
func (r *Relay) controlMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(TunnelPath, r.handleTunnel)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "herdr-relay")
	})
	return mux
}

func (r *Relay) serveControl() {
	ln := tls.NewListener(chanListener{ch: r.ctl, addr: &net.TCPAddr{}}, r.TLS)
	srv := &http.Server{Handler: r.controlMux(), ReadHeaderTimeout: 10 * time.Second}
	if err := srv.Serve(ln); err != nil {
		r.logf("中继控制面挂了: %v", err)
	}
}

var upgrader = websocket.Upgrader{
	// 只有主机（不是浏览器）连这条路，没有 Origin 可核。认证在下面的签名上。
	CheckOrigin: func(*http.Request) bool { return true },
}

// handshakeTimeout：握手三句话必须在这么久之内说完。
const handshakeTimeout = 15 * time.Second

func (r *Relay) handleTunnel(w http.ResponseWriter, req *http.Request) {
	ws, err := upgrader.Upgrade(w, req, nil)
	if err != nil {
		return
	}
	host, err := r.handshake(ws)
	if err != nil {
		_ = ws.WriteJSON(verdict{Err: err.Error()})
		ws.Close()
		r.logf("中继：%s 握手没过：%v", req.RemoteAddr, err)
		return
	}
	if err := ws.WriteJSON(verdict{OK: true}); err != nil {
		ws.Close()
		return
	}
	_ = ws.SetReadDeadline(time.Time{})

	// 中继这一头是 yamux 的 client：子流都是中继开（手机连上来一条就开一条）
	cfg := yamux.DefaultConfig()
	cfg.LogOutput = nil
	cfg.Logger = log.New(discard{}, "", 0)
	sess, err := yamux.Client(newWSStream(ws), cfg)
	if err != nil {
		ws.Close()
		return
	}

	// 同一个名字只认一条隧道，新的顶掉旧的：Mac 睡醒之后旧连接常常是僵的，等它超时要几分钟
	r.mu.Lock()
	if old := r.sessions[host]; old != nil {
		old.Close()
	}
	r.sessions[host] = sess
	r.mu.Unlock()
	r.logf("中继：%s 上线了（%s）", host, req.RemoteAddr)

	<-sess.CloseChan()
	r.drop(host, sess)
	r.logf("中继：%s 下线了", host)
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

func (r *Relay) handshake(ws *websocket.Conn) (string, error) {
	_ = ws.SetReadDeadline(time.Now().Add(handshakeTimeout))
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	if err := ws.WriteJSON(challenge{Nonce: nonce}); err != nil {
		return "", err
	}
	var h hello
	if err := ws.ReadJSON(&h); err != nil {
		return "", err
	}
	if h.V != 1 {
		return "", fmt.Errorf("不认识的协议版本 %d", h.V)
	}
	host, err := CleanHost(h.Host)
	if err != nil {
		return "", err
	}
	// 不按域名再筛一道（原来的 HERDR_RELAY_ZONE）：能注册哪个名字由邀请码决定，码只有管理员出得了、
	// 而且绑死名字。注册了一个不归自己管的名字也没用 —— 那个名字的 DNS 不指这台 VPS 就没流量进来。
	if host == r.Domain {
		// 控制面的名字被注册成主机的话，按 SNI 分路时两边打架（谁先匹配谁说话）
		return "", fmt.Errorf("%s 是中继自己的名字，不能注册成主机", host)
	}
	// 先验签名再碰邀请码：签不对的人连「这个码对不对」都试不出来
	if !verify(r.Domain, h, nonce) {
		return "", errors.New("签名不对")
	}
	if h.Invite != "" {
		if err := r.Store.Redeem(host, h.Invite, h.Key); err != nil {
			return "", err
		}
		r.logf("中继：%s 用邀请码注册了", host)
		return host, nil
	}
	got, err := r.Store.Lookup(host)
	if err != nil {
		return "", err
	}
	if got == nil || string(got.Key) != string(h.Key) {
		// 「没注册」和「钥匙不对」同一句话：别让外面的人试出哪些名字注册过
		return "", errors.New("这台主机没注册，或者钥匙对不上（重新 join 一次）")
	}
	return host, nil
}

// portParent：`7830.herdr.example.com` → `herdr.example.com`。主机的端口代理挂在自己名字下面一层
// （internal/portproxy），那些子域名不单独注册，跟着主机走 —— 主机拥有自己名下的这一层。
// 只认「第一段是纯数字」：别的子域名不是端口代理，不该被顺手路由给谁。
func portParent(sni string) string {
	label, rest, ok := strings.Cut(sni, ".")
	if !ok || label == "" || rest == "" {
		return ""
	}
	for _, c := range label {
		if c < '0' || c > '9' {
			return ""
		}
	}
	return rest
}
