package relay

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"
)

// HostConfig 是主机这一侧连中继要的东西。
type HostConfig struct {
	// URL 是中继控制面的地址，https://relay.example.com
	URL string
	// Host 是这台主机注册的名字，mac.h.example.com
	Host string
	// Dir 放这台主机的身份私钥（~/.herdr-web/relay）。
	Dir string
	// TLS 和 NetDial 只给测试用（信任测试里那张自签证书、把中继的域名拨到本机端口上）。
	// 生产上都是 nil：系统信任的证书、正常解析域名。
	TLS     *tls.Config
	NetDial func(ctx context.Context, network, addr string) (net.Conn, error)
	Log     func(format string, args ...any)
}

func (c HostConfig) logf(format string, args ...any) {
	if c.Log != nil {
		c.Log(format, args...)
		return
	}
	log.Printf(format, args...)
}

func (c HostConfig) keyFile() string { return filepath.Join(c.Dir, "key.pem") }

// relayDomain 从 URL 里拿中继自己的域名（签名里要带它，见 proto.go 的 signed）。
func (c HostConfig) relayDomain() (string, error) {
	u, err := url.Parse(c.URL)
	if err != nil || u.Hostname() == "" {
		return "", fmt.Errorf("中继地址 %q 不对（要的是 https://relay.example.com 这种）", c.URL)
	}
	if u.Scheme != "https" {
		// 控制面上走的是身份签名，明文也偷不走钥匙；但隧道里的元数据和「这台主机在线」
		// 没必要给路上的人看，而且 https 是中继本来就有的
		return "", fmt.Errorf("中继地址只认 https://（给的是 %s）", u.Scheme)
	}
	return strings.ToLower(u.Hostname()), nil
}

func (c HostConfig) wsURL() string {
	u, _ := url.Parse(c.URL)
	u.Scheme = "wss"
	u.Path = strings.TrimRight(u.Path, "/") + TunnelPath
	return u.String()
}

// LoadKey 读这台主机的身份私钥。create=true 时没有就生成一把（只有 join 这么做）。
//
// 这把私钥就是「这台主机是 mac.h.example.com」的全部依据。它躺在这台机器上，被注入的
// agent 读得到 —— 那和证书私钥、DNS token 是同一档（SECURITY.md A6 的老结论：能读你文件的
// 东西早就有你的 shell 了）。所以 0600、不进日志、不进横幅。
func LoadKey(dir string, create bool) (ed25519.PrivateKey, error) {
	p := filepath.Join(dir, "key.pem")
	if b, err := os.ReadFile(p); err == nil {
		blk, _ := pem.Decode(b)
		if blk == nil || len(blk.Bytes) != ed25519.SeedSize {
			return nil, fmt.Errorf("%s 读不出来（坏了的话删掉重新 join）", p)
		}
		return ed25519.NewKeyFromSeed(blk.Bytes), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if !create {
		return nil, fmt.Errorf("还没加入中继（%s 不存在）：先跑一次 herdr-web relay join", p)
	}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	b := pem.EncodeToMemory(&pem.Block{Type: "HERDR RELAY KEY", Bytes: key.Seed()})
	if err := os.WriteFile(p, b, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

// dial 连上中继、走完握手，返回一条可以在上面跑 yamux 的字节流。
func (c HostConfig) dial(ctx context.Context, key ed25519.PrivateKey, invite string) (*wsStream, error) {
	domain, err := c.relayDomain()
	if err != nil {
		return nil, err
	}
	host, err := CleanHost(c.Host)
	if err != nil {
		return nil, err
	}
	d := websocket.Dialer{
		// 认 HTTPS_PROXY：包一层 WebSocket 就是为了穿出口代理（见 wsStream），这里不认就白包了
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: handshakeTimeout,
		TLSClientConfig:  c.TLS,
		NetDialContext:   c.NetDial,
	}
	ws, _, err := d.DialContext(ctx, c.wsURL(), nil)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*wsStream, error) {
		ws.Close()
		return nil, err
	}
	_ = ws.SetReadDeadline(time.Now().Add(handshakeTimeout))
	var ch challenge
	if err := ws.ReadJSON(&ch); err != nil {
		return fail(fmt.Errorf("中继没按协议说话: %w", err))
	}
	h := hello{
		V: 1, Host: host, Key: key.Public().(ed25519.PublicKey),
		Sig: ed25519.Sign(key, signed(domain, host, ch.Nonce)), Invite: invite,
	}
	if err := ws.WriteJSON(h); err != nil {
		return fail(err)
	}
	var v verdict
	if err := ws.ReadJSON(&v); err != nil {
		return fail(err)
	}
	if !v.OK {
		return fail(&RejectedError{Msg: v.Err})
	}
	_ = ws.SetReadDeadline(time.Time{})
	return newWSStream(ws), nil
}

// RejectedError 中继明确拒绝了（钥匙不对、没注册、邀请码无效）。和「连不上」分开：
// 连不上该一直重试，被拒了重试一万次也一样，该停下来让人去 join。
type RejectedError struct{ Msg string }

func (e *RejectedError) Error() string { return "中继拒绝了：" + e.Msg }

// Join 拿邀请码把这台主机注册到中继上。没有身份私钥就当场生成一把。
func Join(ctx context.Context, c HostConfig, invite string) error {
	key, err := LoadKey(c.Dir, true)
	if err != nil {
		return err
	}
	s, err := c.dial(ctx, key, strings.TrimSpace(invite))
	if err != nil {
		return err
	}
	return s.Close()
}

/* ------------------------------------------------------------------ 隧道 = 一个监听 */

// Tunnel 是主机这一侧：一直连着中继，把中继开过来的子流当成一个 net.Listener 交出去。
//
// 交给谁、套什么规则是调用方的事 —— 在 herdr-web 里它被当成**公网口**（server.PublicListener），
// 不能接到主口上：主口那道「只服务本地网络」看的是对端地址，而这里的对端地址是中继说的。
type Tunnel struct {
	cfg  HostConfig
	key  ed25519.PrivateKey
	conn chan net.Conn

	mu     sync.Mutex
	state  string
	closed chan struct{}
	once   sync.Once
}

// Start 读身份私钥（没有就报错，不自己生成 —— 生成了也没注册过）然后在后台一直连着。
func Start(c HostConfig) (*Tunnel, error) {
	key, err := LoadKey(c.Dir, false)
	if err != nil {
		return nil, err
	}
	if _, err := c.relayDomain(); err != nil {
		return nil, err
	}
	t := &Tunnel{cfg: c, key: key, conn: make(chan net.Conn), closed: make(chan struct{}), state: "正在连接"}
	go t.run()
	return t, nil
}

// State 一句话说现在怎么样（横幅 / 管理页用）。
func (t *Tunnel) State() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state
}

func (t *Tunnel) setState(s string) {
	t.mu.Lock()
	t.state = s
	t.mu.Unlock()
}

// 退避：0.5 秒起翻倍到 30 秒。连上并且撑过一分钟就从头数 —— 不然一条「每隔几小时断一次」
// 的线路会一直卡在 30 秒那档上。
const (
	backoffMin = 500 * time.Millisecond
	backoffMax = 30 * time.Second
	stableFor  = time.Minute
)

func (t *Tunnel) run() {
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-t.closed; cancel() }()

	wait := backoffMin
	lastErr := ""
	for {
		start := time.Now()
		err := t.once1(ctx)
		select {
		case <-t.closed:
			return
		default:
		}
		if time.Since(start) > stableFor {
			wait = backoffMin
		}
		msg := err.Error()
		var rej *RejectedError
		if errors.As(err, &rej) {
			t.setState(msg)
			// 被拒绝时照样重试，只是慢：中继的主人可能马上就会重新出邀请码 / 修好配置，
			// 而这边进程不重启。但别刷日志
			wait = backoffMax
		} else {
			t.setState("断开了，正在重连：" + msg)
		}
		if msg != lastErr { // 同一个原因只说一次（断网时每 30 秒一行没人看）
			t.cfg.logf("中继 %s：%s", t.cfg.URL, msg)
			lastErr = msg
		}
		select {
		case <-t.closed:
			return
		case <-time.After(wait):
		}
		if wait *= 2; wait > backoffMax {
			wait = backoffMax
		}
	}
}

// once1 连一次、一直服务到断开为止。
func (t *Tunnel) once1(ctx context.Context) error {
	s, err := t.cfg.dial(ctx, t.key, "")
	if err != nil {
		return err
	}
	cfg := yamux.DefaultConfig()
	cfg.LogOutput = nil
	cfg.Logger = log.New(discard{}, "", 0)
	sess, err := yamux.Server(s, cfg)
	if err != nil {
		s.Close()
		return err
	}
	defer sess.Close()
	t.setState("已连上")
	t.cfg.logf("中继 %s：已连上，https://%s/ 能从外面打开了", t.cfg.URL, t.cfg.Host)

	go func() {
		select {
		case <-t.closed:
			sess.Close()
		case <-sess.CloseChan():
		}
	}()
	for {
		st, err := sess.Accept()
		if err != nil {
			return errors.New("隧道断了")
		}
		go t.admit(st)
	}
}

// admit 读掉子流开头那个头（手机的真实地址），再交给 Accept。
// 放在单独的协程里：一条子流的头迟迟不来，不能挡住后面别的连接。
func (t *Tunnel) admit(st net.Conn) {
	_ = st.SetReadDeadline(time.Now().Add(helloTimeout))
	h, err := readHeader(st)
	_ = st.SetReadDeadline(time.Time{})
	if err != nil {
		st.Close()
		return
	}
	c := &srcConn{Conn: st, src: parseAddr(h.Src)}
	select {
	case t.conn <- c:
	case <-t.closed:
		st.Close()
	}
}

func (t *Tunnel) Accept() (net.Conn, error) {
	select {
	case c := <-t.conn:
		return c, nil
	case <-t.closed:
		return nil, net.ErrClosed
	}
}

func (t *Tunnel) Close() error {
	t.once.Do(func() { close(t.closed) })
	return nil
}

func (t *Tunnel) Addr() net.Addr { return relayAddr(t.cfg.Host) }

type relayAddr string

func (relayAddr) Network() string  { return "relay" }
func (a relayAddr) String() string { return string(a) }

// srcConn 把 RemoteAddr 换成中继报的手机地址（限速按它算），顺带把 yamux 的超时错误换成标准库那个。
type srcConn struct {
	net.Conn
	src net.Addr
}

func (c *srcConn) RemoteAddr() net.Addr { return c.src }

// Read / Write 把「读写期限到了」统一成 os.ErrDeadlineExceeded。**这一层不能省**，否则隧道上的
// WebSocket（终端）连上就断，而普通请求一切正常（真机上撞上的，TestWebSocketThroughTunnel 盯着）：
//
// net/http 在 Hijack（WebSocket 升级）时，用一个早就过期的读期限去打断后台那次读，打断完再把
// 期限清掉 —— 期限超时在这里是**正常流程**。可 yamux 的 ErrTimeout 报 Temporary()==false，而
// crypto/tls 见到非临时的读错误会**永久记住**（conn.go 里 `!e.Temporary()` 那两处），这条 TLS
// 连接之后的每次读都原样返回这个错。标准库自己的 os.ErrDeadlineExceeded 报 Temporary()==true，
// tls 就不记 —— 普通 TCP 连接（局域网那条路）就是这样，所以只有中继这条路坏。
func (c *srcConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	return n, deadline(err)
}

func (c *srcConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	return n, deadline(err)
}

func deadline(err error) error {
	var ne net.Error
	if err != nil && errors.As(err, &ne) && ne.Timeout() {
		return os.ErrDeadlineExceeded
	}
	return err
}

func parseAddr(s string) net.Addr {
	if a, err := net.ResolveTCPAddr("tcp", s); err == nil && a.IP != nil {
		return a
	}
	// 头里没有 / 填坏了：给一个绝不会被当成「本机」或「局域网」的地址。
	// 用 0.0.0.0 而不是 127.0.0.1 —— 后者撞上「本机永不封」就把限速白白豁免掉了。
	return &net.TCPAddr{IP: net.IPv4zero}
}
