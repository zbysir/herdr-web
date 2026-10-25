package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zbysir/herdr-web/internal/auth"
	"github.com/zbysir/herdr-web/internal/config"
	"github.com/zbysir/herdr-web/internal/lan"
)

// 没开这条路的时候 /api/state 里不能有 lan 那一段 —— App 的壳靠「有没有它」决定要不要
// 去探，给个空对象的话它会拿着零个候选跑一遍探测。
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
		t.Fatal("开着却给不出候选，壳会白跑一遍探测")
	}
	for _, o := range origins {
		if !strings.HasPrefix(o, "https://") || !strings.HasSuffix(o, ":7789") {
			t.Errorf("候选 %q 的形状不对", o)
		}
	}
}

// 开着局域网直连口时 CSP 的 connect-src 也只能是 'self'：放行那几个 IP origin 就是给
// 「网页自己去嗅探直连口」开门，而那次嗅探一旦带着证书错误成功（人在 IP 上点过「继续
// 访问」），Chromium 就把整页记成不安全、passkey 再也按不动。见 guard.go 的 secHeaders。
func TestCSPSelfOnly(t *testing.T) {
	lan.ResetCache()
	defer lan.ResetCache()
	w := httptest.NewRecorder()
	(&Server{LanPort: 7789}).secHeaders(w)
	csp := w.Header().Get("content-security-policy")
	if !strings.Contains(csp, "connect-src 'self';") {
		t.Errorf("connect-src 只能是 'self'：%q", csp)
	}
	for _, o := range lan.Origins(7789) {
		if strings.Contains(csp, o) {
			t.Errorf("CSP 里不该有局域网 origin %q", o)
		}
	}
}

// 局域网口不在前置后面，转发头一定是客户端塞的。ClientIP 已经不看 XFF 了，这里盯的是
// 「局域网口上认的只有 RemoteAddr」这条性质别因为谁把 XFF 加回来而悄悄破掉。
func TestLanListenerStripsForwarded(t *testing.T) {
	dir := t.TempDir()
	store, err := auth.New(auth.Config{Dir: dir})
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

// 直连口对**非本地对端**要一个字节都不服务 —— 端口一旦被暴露（端口转发 / 全局 IPv6），
// 这道检查就是那个口上唯一一道「你在不在本地网络里」的门。
func TestLanListenerRefusesNonLocalPeer(t *testing.T) {
	var reached bool
	h := LanListener(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		reached = true
	}))

	for _, peer := range []string{"[240e:39d:5a:6d20::9fd]:5000", "1.2.3.4:5000"} {
		reached = false
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = peer
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if reached {
			t.Errorf("对端 %s 竟然进到了 handler", peer)
		}
		if w.Code != http.StatusForbidden {
			t.Errorf("对端 %s：状态码 = %d，想要 403", peer, w.Code)
		}
	}

	// 本地对端照常进
	reached = false
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "192.168.31.78:5000"
	h.ServeHTTP(httptest.NewRecorder(), r)
	if !reached {
		t.Error("本地对端应当照常进")
	}
}
