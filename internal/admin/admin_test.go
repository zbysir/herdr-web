package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"git.huglight.cn/bysir/herdr-web/internal/auth"
	"git.huglight.cn/bysir/herdr-web/internal/config"
)

func testHandler(t *testing.T) http.Handler {
	t.Helper()
	store, err := auth.New(auth.Config{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	pk, err := auth.NewPasskeys(auth.PasskeyConfig{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return Handler(Deps{
		Cfg:   &config.Config{Port: 7788, DataDir: t.TempDir()},
		Store: store, Passkeys: pk, Gate: auth.NewGate(),
	})
}

func do(h http.Handler, method, target, host string, headers map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, nil)
	r.Host = host
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// 只绑 loopback 挡住了网络上的别人，但**挡不住浏览器**：你在这台机器上打开的任何网页
// 都能 fetch http://127.0.0.1:PORT/。把域名解析到 127.0.0.1（DNS rebinding）之后，
// 浏览器眼里那就是同源 —— 那时候 Host 头是攻击者的域名，所以只认 loopback 字面量。
func TestRejectsNonLoopbackHost(t *testing.T) {
	h := testHandler(t)
	for _, host := range []string{"evil.com", "evil.com:7789", "herdr.bysir.top", "192.168.1.5:7789"} {
		if got := do(h, "GET", "/api/state", host, nil).Code; got != http.StatusMisdirectedRequest {
			t.Errorf("Host %q 该被拒（421），得到 %d", host, got)
		}
	}
	for _, host := range []string{"127.0.0.1:7789", "localhost:7789", "[::1]:7789", "127.0.0.1"} {
		if got := do(h, "GET", "/api/state", host, nil).Code; got != 200 {
			t.Errorf("Host %q 该放行，得到 %d", host, got)
		}
	}
}

// 这个口不认证（能连上 loopback 的东西已经有你的 shell 了），所以写操作只能靠
// 自定义头挡跨站 —— 恶意网页设不了它（会触发 preflight，而我们不答 preflight）。
func TestWritesNeedCustomHeader(t *testing.T) {
	h := testHandler(t)
	if got := do(h, "POST", "/api/pair", "127.0.0.1:7789", nil).Code; got != http.StatusForbidden {
		t.Errorf("没带自定义头的写操作该被拒，得到 %d", got)
	}
	if got := do(h, "POST", "/api/pair", "127.0.0.1:7789", map[string]string{Header: "1"}).Code; got != 200 {
		t.Errorf("带了头该放行，得到 %d", got)
	}
	// 读操作不要求头（浏览器直接打开这个页面就是普通 GET）
	if got := do(h, "GET", "/api/state", "127.0.0.1:7789", nil).Code; got != 200 {
		t.Errorf("GET 不该要求头，得到 %d", got)
	}
}

func TestRejectsForeignOrigin(t *testing.T) {
	h := testHandler(t)
	got := do(h, "POST", "/api/pair", "127.0.0.1:7789", map[string]string{
		Header: "1", "Origin": "http://evil.com",
	}).Code
	if got != http.StatusForbidden {
		t.Errorf("外站 Origin 该被拒，得到 %d", got)
	}
}

// 没配 ACME 的时候点「续期」不能崩，要给一句能看懂的话。
func TestRenewWithoutACME(t *testing.T) {
	h := testHandler(t)
	w := do(h, "POST", "/api/cert/renew", "127.0.0.1:7789", map[string]string{Header: "1"})
	if w.Code != 400 {
		t.Errorf("没配 ACME 该回 400，得到 %d", w.Code)
	}
	if !contains(w.Body.String(), "HERDR_WEB_ACME_DNS") {
		t.Errorf("错误信息该点出要配哪个变量：%s", w.Body.String())
	}
}

// 页面本身要能在「前端产物没 build」和「证书坏了」的时候打开 —— 它是自带的 HTML。
func TestPageServesStandalone(t *testing.T) {
	h := testHandler(t)
	w := do(h, "GET", "/", "127.0.0.1:7789", nil)
	if w.Code != 200 {
		t.Fatalf("首页该出得来，得到 %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{"herdr-web 管理", "/api/state", "X-Herdr-Admin"} {
		if !contains(body, want) {
			t.Errorf("页面里该有 %q", want)
		}
	}
	if contains(body, "http://") && contains(body, "cdn") {
		t.Error("页面不该引外部资源 —— 它要在没网 / 证书坏的时候也能用")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
