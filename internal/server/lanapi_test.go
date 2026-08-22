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

// handoff 出的是一次性配对码。三条：必须已认证、必须 POST、没开这条路时不给。
func TestHandoff(t *testing.T) {
	dir := t.TempDir()
	store, err := auth.New(auth.Config{Dir: dir, TrustLoopback: true})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Cfg: &config.Config{}, Auth: store, Gate: auth.NewGate(), LanPort: 7789}

	// 没开这条路
	off := &Server{Cfg: &config.Config{}, Auth: store, Gate: auth.NewGate()}
	w := httptest.NewRecorder()
	off.apiHandoff(w, httptest.NewRequest(http.MethodPost, "/api/handoff", nil))
	if w.Code != 404 {
		t.Errorf("没开局域网直连时应当 404，得到 %d", w.Code)
	}

	// GET 不收
	w = httptest.NewRecorder()
	s.apiHandoff(w, httptest.NewRequest(http.MethodGet, "/api/handoff", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET 应当 405，得到 %d", w.Code)
	}

	// 正常出码，而且**能兑换成一台设备**（跳过去落地靠的就是这一步）
	w = httptest.NewRecorder()
	s.apiHandoff(w, httptest.NewRequest(http.MethodPost, "/api/handoff", nil))
	if w.Code != 200 {
		t.Fatalf("出码失败：%d %s", w.Code, w.Body)
	}
	var out struct{ Code string }
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || out.Code == "" {
		t.Fatalf("响应里没有 code：%s (%v)", w.Body, err)
	}
	if _, _, err := store.Redeem(out.Code, "ua", "192.168.1.9"); err != nil {
		t.Errorf("handoff 出的码应当能兑换：%v", err)
	}
	// 一次性：第二次必须失败
	if _, _, err := store.Redeem(out.Code, "ua", "192.168.1.9"); err == nil {
		t.Error("同一个码兑换了两次都成功，那就不是一次性的了")
	}
}

// 横幅上那个码不能被 handoff 顶掉 —— 不然「网页切了一次局域网」就等于把终端里
// 印出来的配对码作废，而人正拿着手机在扫它。
func TestHandoffKeepsBannerCode(t *testing.T) {
	dir := t.TempDir()
	store, err := auth.New(auth.Config{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	banner, _ := store.MintCode()
	s := &Server{Cfg: &config.Config{}, Auth: store, Gate: auth.NewGate(), LanPort: 7789}
	w := httptest.NewRecorder()
	s.apiHandoff(w, httptest.NewRequest(http.MethodPost, "/api/handoff", nil))
	if w.Code != 200 {
		t.Fatalf("出码失败：%d", w.Code)
	}
	if _, _, err := store.Redeem(banner, "ua", "127.0.0.1"); err != nil {
		t.Errorf("横幅上那个码被顶掉了：%v", err)
	}
}

// 局域网口不在前置后面，转发头一定是客户端塞的。配了 TRUST_PROXY=1 的部署（前置只在
// 公网那条路上）如果照信，同一个 Wi-Fi 上的人用一串假 XFF 就能把按 IP 的限速绕干净。
func TestStripForwarded(t *testing.T) {
	dir := t.TempDir()
	store, err := auth.New(auth.Config{Dir: dir, TrustProxy: true})
	if err != nil {
		t.Fatal(err)
	}

	var seen string
	h := StripForwarded(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
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
