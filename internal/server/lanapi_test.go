package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zbysir/herdr-web/internal/auth"
	"github.com/zbysir/herdr-web/internal/config"
	"github.com/zbysir/herdr-web/internal/lan"
)

// 没开这条路的时候 /api/state 里不能有 lan 那一段 —— 前端靠「有没有它」决定要不要
// 去嗅探，给个空对象的话它会拿着零个候选跑一遍探测。
func TestLanInfoOff(t *testing.T) {
	if got := (&Server{LanPort: 0}).lanInfo(); got != nil {
		t.Errorf("没开局域网直连时应当是 nil，得到 %v", got)
	}
}

func TestLanInfoOn(t *testing.T) {
	lan.ResetCache()
	defer lan.ResetCache()
	s := &Server{LanPort: 7789}
	got := s.lanInfo()
	if got == nil {
		t.Skip("这台机器上一个私网地址都没有（CI 容器常见），这条跳过")
	}
	if got["port"] != 7789 {
		t.Errorf("port = %v", got["port"])
	}
	origins, _ := got["origins"].([]string)
	if len(origins) == 0 {
		t.Fatal("开着却给不出候选，前端会白跑一遍探测")
	}
	for _, o := range origins {
		if !strings.HasPrefix(o, "https://") || !strings.HasSuffix(o, ":7789") {
			t.Errorf("候选 %q 的形状不对", o)
		}
	}
}

// CSP 那一条是最容易漏的：connect-src 不放行的话，跨 origin 的嗅探会被**自己的 CSP**
// 挡掉，而控制台里那条错和「连不上」长得一样，会被误判成「局域网不通」。
func TestConnectSrcIncludesLan(t *testing.T) {
	lan.ResetCache()
	defer lan.ResetCache()

	if got := (&Server{LanPort: 0}).connectSrc(); got != "'self'" {
		t.Errorf("没开这条路时应当只有 'self'，得到 %q", got)
	}

	s := &Server{LanPort: 7789}
	origins := lan.Origins(7789)
	if len(origins) == 0 {
		t.Skip("没有私网地址，跳过")
	}
	got := s.connectSrc()
	if !strings.HasPrefix(got, "'self' ") {
		t.Errorf("'self' 必须还在：%q", got)
	}
	for _, o := range origins {
		if !strings.Contains(got, o) {
			t.Errorf("connect-src 里少了 %q：%q", o, got)
		}
	}
}

// 局域网口不在前置后面，转发头一定是客户端塞的。配了 TRUST_PROXY=1 的部署（前置只在
// 公网那条路上）如果照信，同一个 Wi-Fi 上的人用一串假 XFF 就能把按 IP 的限速绕干净。
func TestLanListenerStripsForwarded(t *testing.T) {
	dir := t.TempDir()
	store, err := auth.New(auth.Config{Dir: dir, TrustProxy: true})
	if err != nil {
		t.Fatal(err)
	}

	var seen string
	h := LanListener(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = store.ClientIP(r)
	}))

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "192.168.1.42:5000"
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	r.Header.Set("X-Real-Ip", "1.2.3.4")
	r.Header.Set("Forwarded", "for=1.2.3.4")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if seen != "192.168.1.42" {
		t.Errorf("局域网口上应当只认 RemoteAddr，得到 %q —— 限速能被一个头绕过去", seen)
	}

	// 对照：不套这层的话（公网那条路，前置是可信的）XFF 照旧生效
	r2 := httptest.NewRequest(http.MethodGet, "/", nil)
	r2.RemoteAddr = "127.0.0.1:5000"
	r2.Header.Set("X-Forwarded-For", "1.2.3.4")
	if got := store.ClientIP(r2); got != "1.2.3.4" {
		t.Errorf("公网那条路上 TRUST_PROXY 的行为不该被改动，得到 %q", got)
	}
}

// 开了局域网直连之后同一个部署同时有域名 origin 和裸 IP origin，而 passkey 只在域名那侧
// 可能成立。whoami / passkeys 里那个 available 必须**按请求的 Host** 算 —— 判错的表现是
// 裸 IP 那一侧画出一个按下去只会抛 SecurityError 的按钮。
func TestPasskeyOKPerOrigin(t *testing.T) {
	dir := t.TempDir()
	pk, err := auth.NewPasskeys(auth.PasskeyConfig{
		Dir: dir, RPID: "herdr.example.com",
		Origins: []string{"https://herdr.example.com"}, Display: "t",
	})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Cfg: &config.Config{}, Passkeys: pk, RPID: "herdr.example.com"}
	if !pk.Available() {
		t.Fatal("配了 RPID 就该是 available（全局那一层）")
	}

	for host, want := range map[string]bool{
		"herdr.example.com":     true,
		"herdr.example.com:443": true,
		"192.168.31.214:7790":   false, // 局域网直连那条路
		"127.0.0.1:7788":        false,
	} {
		r := httptest.NewRequest(http.MethodGet, "/api/auth/whoami", nil)
		r.Host = host
		if got := s.passkeyOK(r); got != want {
			t.Errorf("Host=%s：passkeyOK = %v，想要 %v", host, got, want)
		}
	}
}

// 裸 IP 上那几个 ceremony 的口要早拒 + 说清楚，别让 WebAuthn 库在后面报一个看不懂的 origin 错。
func TestPasskeyGateRejectsIP(t *testing.T) {
	s := &Server{Cfg: &config.Config{}, RPID: "herdr.example.com"}
	r := httptest.NewRequest(http.MethodPost, "/api/auth/passkey/login/begin", nil)
	r.Host = "192.168.31.214:7790"
	w := httptest.NewRecorder()
	if s.passkeyGate(w, r) {
		t.Fatal("裸 IP 上应当直接拒掉")
	}
	if w.Code != http.StatusConflict {
		t.Errorf("状态码 = %d，想要 409", w.Code)
	}
	if !strings.Contains(w.Body.String(), "域名") {
		t.Errorf("错误信息得说清为什么：%s", w.Body)
	}
}

// 交接令牌的兑换门槛：**只有落在直连那个监听上的请求算**。
//
// 这是整块里最要命的一处判据。用 Host 判断的话，从公网那条路发一个
// `Host: 192.168.1.5:7790` 就能把「看起来像内网」伪造出来 —— 而 hostOK 对 IP 字面量
// 一律放行（那本身是对的）。所以这条钉死：伪造 Host 不管用，盖过章的才管用。
func TestHandoffOnlyRedeemableFromLanListener(t *testing.T) {
	var sawLan bool
	inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { sawLan = FromLan(r) })

	// 公网那条路 + 伪造的内网 Host
	r := httptest.NewRequest(http.MethodGet, "/?handoff=x", nil)
	r.Host = "192.168.1.5:7790"
	inner.ServeHTTP(httptest.NewRecorder(), r)
	if sawLan {
		t.Error("伪造 Host 竟然被当成直连口进来的 —— 交接令牌那道门等于不存在")
	}

	// 真的从直连口进来（对端也得是本地地址 —— httptest 默认给的是 192.0.2.1，公网段）
	r2 := httptest.NewRequest(http.MethodGet, "/?handoff=x", nil)
	r2.Host = "192.168.1.5:7790"
	r2.RemoteAddr = "192.168.1.9:5000"
	LanListener(inner).ServeHTTP(httptest.NewRecorder(), r2)
	if !sawLan {
		t.Error("从直连监听进来的请求应当被认出来")
	}
}

// 没有设备的会话（本机免配对 / 旧 token）不能交接：没有东西能级联撤销，
// 而那两种会话本来就在机器上，压根不需要局域网直连。
func TestHandoffNeedsDeviceParent(t *testing.T) {
	dir := t.TempDir()
	store, err := auth.New(auth.Config{Dir: dir, TrustLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Cfg: &config.Config{}, Auth: store, Gate: auth.NewGate(), LanPort: 7790}
	r := httptest.NewRequest(http.MethodPost, "/api/handoff", nil)
	r.RemoteAddr = "127.0.0.1:5000"
	r.Host = "127.0.0.1:7788"
	r.Header.Set(CSRFHeader, "1")
	w := httptest.NewRecorder()
	s.apiHandoff(w, r)
	if w.Code != http.StatusConflict {
		t.Errorf("本机免配对那种会话应当 409，得到 %d %s", w.Code, w.Body)
	}
}

// handoff 出的是**交接令牌**，不是配对码。四条一起钉：
//  1. 字段叫 handoff（前端按它取）、不再有 code；
//  2. **压根不碰配对码那套** —— 横幅上那枚码不能被顶掉（换成交接令牌之后这是天然的，
//     但这条网留着：哪天有人图省事改回 MintCode，这里会当场红）；
//  3. 兑出来的设备记着 parent（撤销级联靠它，见 auth 那边的测试）；
//  4. 一次性。
func TestHandoffMintsTokenNotPairCode(t *testing.T) {
	dir := t.TempDir()
	store, err := auth.New(auth.Config{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	first, _ := store.MintCode()
	dev, token, err := store.Redeem(first, "ua", "1.2.3.4")
	if err != nil {
		t.Fatal(err)
	}
	banner, _ := store.MintCode() // 横幅上「当前那一枚」

	s := &Server{Cfg: &config.Config{}, Auth: store, Gate: auth.NewGate(), LanPort: 7790}
	r := httptest.NewRequest(http.MethodPost, "/api/handoff", nil)
	r.Host = "herdr.example.com"
	r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})
	r.Header.Set(CSRFHeader, "1")
	w := httptest.NewRecorder()
	s.apiHandoff(w, r)
	if w.Code != 200 {
		t.Fatalf("出令牌失败：%d %s", w.Code, w.Body)
	}
	var out struct{ Handoff, Code string }
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Handoff == "" {
		t.Error("响应里应当有 handoff")
	}
	if out.Code != "" {
		t.Error("不该再出 code —— 那正是 SECURITY.md §11 禁的那条路")
	}
	if _, _, err := store.Redeem(banner, "ua", "1.2.3.4"); err != nil {
		t.Errorf("横幅上那枚配对码被顶掉了：%v", err)
	}
	child, _, err := store.RedeemHandoff(out.Handoff, "ua", "192.168.1.9")
	if err != nil {
		t.Fatalf("交接令牌应当兑得动：%v", err)
	}
	if child.Parent != dev.ID {
		t.Errorf("parent = %q，想要 %q —— 没有它撤销就级联不了", child.Parent, dev.ID)
	}
	if _, _, err := store.RedeemHandoff(out.Handoff, "ua", "192.168.1.9"); err == nil {
		t.Error("同一枚令牌兑了两次都成功，那就不是一次性的")
	}
}

// 直连口对**非本地对端**要一个字节都不服务，而且不能盖上「从直连口进来」那个章
// —— 否则端口一旦被暴露（端口转发 / 全局 IPv6），交接令牌那道门就等于不存在。
func TestLanListenerRefusesNonLocalPeer(t *testing.T) {
	var reached, sawLan bool
	h := LanListener(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		reached, sawLan = true, FromLan(r)
	}))

	for _, peer := range []string{"[240e:39d:5a:6d20::9fd]:5000", "1.2.3.4:5000"} {
		reached, sawLan = false, false
		r := httptest.NewRequest(http.MethodGet, "/?handoff=x", nil)
		r.RemoteAddr = peer
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if reached || sawLan {
			t.Errorf("对端 %s 竟然进到了 handler（sawLan=%v）", peer, sawLan)
		}
		if w.Code != http.StatusForbidden {
			t.Errorf("对端 %s：状态码 = %d，想要 403", peer, w.Code)
		}
	}

	// 本地对端照常进
	reached, sawLan = false, false
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "192.168.31.78:5000"
	h.ServeHTTP(httptest.NewRecorder(), r)
	if !reached || !sawLan {
		t.Errorf("本地对端应当照常进（reached=%v sawLan=%v）", reached, sawLan)
	}
}
