package relay

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// 一张自签证书同时给中继控制面（relay.test）和主机（mac.h.test）用，测试里信任它。
func testCert(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test"},
		DNSNames:  []string{"relay.test", "mac.h.test", "pc.h.test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, IsCA: true,
		BasicConstraintsValid: true, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: k, Leaf: leaf}, pool
}

type rig struct {
	relay *Relay
	addr  string
	cert  tls.Certificate
	pool  *x509.CertPool
}

func newRig(t *testing.T) *rig {
	cert, pool := testCert(t)
	r := &Relay{
		Domain: "relay.test", Zone: "h.test",
		TLS:   &tls.Config{Certificates: []tls.Certificate{cert}},
		Store: Store{Dir: t.TempDir()}, Log: t.Logf,
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go r.Serve(ln)
	return &rig{relay: r, addr: ln.Addr().String(), cert: cert, pool: pool}
}

// 主机的配置：中继的域名一律拨到测试里那个本机端口
func (g *rig) host(t *testing.T, name string) HostConfig {
	return HostConfig{
		URL: "https://relay.test", Host: name, Dir: t.TempDir(),
		TLS: &tls.Config{RootCAs: g.pool},
		NetDial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, g.addr)
		},
		Log: t.Logf,
	}
}

// 「手机」：按 SNI=name 连中继那个口，验证书，发一个 GET
func (g *rig) get(name string) (string, error) {
	c := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: g.pool},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, g.addr)
		},
	}}
	resp, err := c.Get("https://" + name + "/")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return string(b), err
}

func waitUp(t *testing.T, tn *Tunnel) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if tn.State() == "已连上" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("隧道没连上：%s", tn.State())
}

// 主机侧起一个「herdr-web」：TLS 在这里终止，回显它看到的对端地址
func serveHost(t *testing.T, tn *Tunnel, cert tls.Certificate, tag string) {
	ln := tls.NewListener(tn, &tls.Config{Certificates: []tls.Certificate{cert}})
	go http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, tag+" "+r.RemoteAddr+" "+r.Host)
	}))
	t.Cleanup(func() { tn.Close() })
}

// 整条路走一遍：邀请码注册 → 隧道连上 → 手机按主机名连中继 → TLS 在主机上终止 → 主机
// 看到的对端地址是手机的真实地址。两台主机（同一个人的两台机器）各走各的。
func TestEndToEndTwoHosts(t *testing.T) {
	g := newRig(t)
	ctx := context.Background()

	for _, name := range []string{"mac.h.test", "pc.h.test"} {
		hc := g.host(t, name)
		code, err := g.relay.Store.NewInvite(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := Join(ctx, hc, code); err != nil {
			t.Fatalf("%s join: %v", name, err)
		}
		// 码用一次就废
		if err := Join(ctx, hc, code); err == nil {
			t.Fatal("用过的邀请码必须作废")
		}
		tn, err := Start(hc)
		if err != nil {
			t.Fatal(err)
		}
		serveHost(t, tn, g.cert, strings.Split(name, ".")[0])
		waitUp(t, tn)
	}

	for _, name := range []string{"mac.h.test", "pc.h.test"} {
		body, err := g.get(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		tag := strings.Split(name, ".")[0]
		if !strings.HasPrefix(body, tag+" 127.0.0.1:") {
			t.Fatalf("%s 该被送到自己那台主机、看到手机的地址，拿到的是 %q", name, body)
		}
		if !strings.HasSuffix(body, " "+name) {
			t.Fatalf("Host 头该原样到主机：%q", body)
		}
	}
}

// 中继手里没有主机证书：TLS 必须是手机和主机之间的，中继只是转字节。
// 这里换一张中继认、但主机没有的证书去连 —— 握手对面是主机，所以手机看到的必须是主机那张。
func TestTLSTerminatesOnHost(t *testing.T) {
	g := newRig(t)
	hc := g.host(t, "mac.h.test")
	code, _ := g.relay.Store.NewInvite("mac.h.test")
	if err := Join(context.Background(), hc, code); err != nil {
		t.Fatal(err)
	}
	tn, err := Start(hc)
	if err != nil {
		t.Fatal(err)
	}
	hostCert, hostPool := testCert(t) // 主机用另一张证书，中继的那张它没有
	serveHost(t, tn, hostCert, "mac")
	waitUp(t, tn)

	c, err := tls.Dial("tcp", g.addr, &tls.Config{ServerName: "mac.h.test", RootCAs: hostPool})
	if err != nil {
		t.Fatalf("手机该和主机握手（用主机那张证书）: %v", err)
	}
	defer c.Close()
	if !c.ConnectionState().PeerCertificates[0].Equal(hostCert.Leaf) {
		t.Fatal("握手对面不是主机")
	}
}

// 没注册的名字、不在 zone 下的名字、钥匙不对的主机，一律进不来。
func TestRejects(t *testing.T) {
	g := newRig(t)
	ctx := context.Background()

	// 没注册 + 没邀请码
	if err := Join(ctx, g.host(t, "mac.h.test"), ""); err == nil {
		t.Fatal("没注册的主机不能连上")
	}
	// 邀请码是给别的名字的
	code, _ := g.relay.Store.NewInvite("pc.h.test")
	var rej *RejectedError
	if err := Join(ctx, g.host(t, "mac.h.test"), code); !errors.As(err, &rej) {
		t.Fatalf("邀请码绑死主机名：%v", err)
	}
	// 不在 zone 下
	code, _ = g.relay.Store.NewInvite("evil.example.com")
	if err := Join(ctx, g.host(t, "evil.example.com"), code); err == nil {
		t.Fatal("不在 zone 下的名字不能注册")
	}

	// 注册成功之后，换一把钥匙冒充同一个名字
	hc := g.host(t, "mac.h.test")
	code, _ = g.relay.Store.NewInvite("mac.h.test")
	if err := Join(ctx, hc, code); err != nil {
		t.Fatal(err)
	}
	impostor := g.host(t, "mac.h.test") // 另一个目录 = 另一把钥匙
	if _, err := LoadKey(impostor.Dir, true); err != nil {
		t.Fatal(err)
	}
	if _, err := impostor.dial(ctx, mustKey(t, impostor.Dir), ""); !errors.As(err, &rej) {
		t.Fatalf("钥匙对不上必须被拒：%v", err)
	}

	// 没在线的名字：中继安静地断开，手机握手失败
	if _, err := g.get("pc.h.test"); err == nil {
		t.Fatal("没在线的主机不该有任何应答")
	}
}

func mustKey(t *testing.T, dir string) []byte {
	k, err := LoadKey(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// 同一个名字的新隧道顶掉旧的（Mac 睡醒之后旧连接是僵的）。
func TestNewTunnelReplacesOld(t *testing.T) {
	g := newRig(t)
	hc := g.host(t, "mac.h.test")
	code, _ := g.relay.Store.NewInvite("mac.h.test")
	if err := Join(context.Background(), hc, code); err != nil {
		t.Fatal(err)
	}
	a, _ := Start(hc)
	serveHost(t, a, g.cert, "old")
	waitUp(t, a)
	b, _ := Start(hc)
	serveHost(t, b, g.cert, "new")
	waitUp(t, b)

	time.Sleep(50 * time.Millisecond)
	body, err := g.get("mac.h.test")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(body, "new ") {
		t.Fatalf("新隧道该顶掉旧的：%q", body)
	}
}

func TestCleanHost(t *testing.T) {
	for in, want := range map[string]string{
		"Mac.H.Example.com.": "mac.h.example.com",
		" pc.h.example.com ": "pc.h.example.com",
	} {
		if got, err := CleanHost(in); err != nil || got != want {
			t.Errorf("CleanHost(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "1.2.3.4", "a..b", "-a.b", "a_b.c", "中文.com"} {
		if _, err := CleanHost(bad); err == nil {
			t.Errorf("CleanHost(%q) 该报错", bad)
		}
	}
}

// 前面有 Traefik 占着 443 的形态：控制面由前面那层终止 TLS、转到 ControlHandler（明文），
// SNI 那个口只管主机名。中继自己没有证书（TLS == nil），SNI 是它自己名字的连接要直接断。
func TestControlBehindFrontProxy(t *testing.T) {
	cert, pool := testCert(t)
	r := &Relay{Domain: "relay.test", Zone: "h.test", Store: Store{Dir: t.TempDir()}, Log: t.Logf}
	sni, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer sni.Close()
	go r.Serve(sni)

	front := httptest.NewUnstartedServer(r.ControlHandler()) // 扮演 Traefik
	front.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	front.StartTLS()
	defer front.Close()

	hc := HostConfig{
		URL: "https://relay.test", Host: "mac.h.test", Dir: t.TempDir(), Log: t.Logf,
		TLS: &tls.Config{RootCAs: pool},
		NetDial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, front.Listener.Addr().String())
		},
	}
	code, _ := r.Store.NewInvite("mac.h.test")
	if err := Join(context.Background(), hc, code); err != nil {
		t.Fatal(err)
	}
	tn, err := Start(hc)
	if err != nil {
		t.Fatal(err)
	}
	serveHost(t, tn, cert, "mac")
	waitUp(t, tn)

	g := &rig{relay: r, addr: sni.Addr().String(), cert: cert, pool: pool}
	if body, err := g.get("mac.h.test"); err != nil || !strings.HasPrefix(body, "mac ") {
		t.Fatalf("主机那条路该通：%q %v", body, err)
	}
	if _, err := g.get("relay.test"); err == nil {
		t.Fatal("中继自己没证书时，SNI 是它自己名字的连接该断开")
	}
}

// 中继自己的名字不能被注册成主机（zone 是整个域名时它就落在 zone 里）。
func TestRelayDomainNotRegistrable(t *testing.T) {
	g := newRig(t)
	g.relay.Zone = "test" // relay.test 落在 zone 里
	code, _ := g.relay.Store.NewInvite("relay.test")
	if err := Join(context.Background(), g.host(t, "relay.test"), code); err == nil {
		t.Fatal("中继自己的名字不该能注册")
	}
}

// 隧道上跑 WebSocket（终端就是它）。普通 HTTP 测不出来的那个坑：net/http 在 Hijack 时用一个
// 「早就过期」的读期限打断后台那次读，而 yamux 的超时错误 Temporary()==false，crypto/tls 见到
// 非临时的读错误会**永久记住**，于是这条连接上之后的每次读都失败 —— 表现是终端连上就断
// （服务端日志「前端断开」，前端「会话结束了」），而同一条路上的普通请求一切正常。真机上撞上的。
func TestWebSocketThroughTunnel(t *testing.T) {
	g := newRig(t)
	hc := g.host(t, "mac.h.test")
	code, _ := g.relay.Store.NewInvite("mac.h.test")
	if err := Join(context.Background(), hc, code); err != nil {
		t.Fatal(err)
	}
	tn, err := Start(hc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tn.Close() })
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	ln := tls.NewListener(tn, &tls.Config{Certificates: []tls.Certificate{g.cert}})
	go http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			typ, b, err := c.ReadMessage()
			if err != nil {
				return
			}
			_ = c.SetWriteDeadline(time.Now().Add(5 * time.Second)) // 和 pty.go 的 wsWriter 一样
			if c.WriteMessage(typ, b) != nil {
				return
			}
		}
	}))
	waitUp(t, tn)

	d := websocket.Dialer{
		TLSClientConfig: &tls.Config{RootCAs: g.pool},
		NetDialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, g.addr)
		},
	}
	c, _, err := d.Dial("wss://mac.h.test/", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for i := 0; i < 3; i++ {
		if err := c.WriteMessage(websocket.TextMessage, []byte("hi")); err != nil {
			t.Fatal(err)
		}
		_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
		if _, b, err := c.ReadMessage(); err != nil || string(b) != "hi" {
			t.Fatalf("第 %d 次回显失败：%q %v（隧道上的 WebSocket 连上就断）", i, b, err)
		}
	}
}
