package portproxy

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/zbysir/herdr-web/internal/auth"
)

type rig struct {
	p       *Proxy
	store   *auth.Store
	port    int
	host    string
	gotHost string
	gotCk   string
}

func newRig(t *testing.T) *rig {
	g := &rig{}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.gotHost, g.gotCk = r.Host, r.Header.Get("Cookie")
		if r.URL.Path == "/go" {
			http.Redirect(w, r, "http://localhost:"+strconv.Itoa(g.port)+"/there", http.StatusFound)
			return
		}
		io.WriteString(w, "hello from "+r.URL.Path)
	}))
	t.Cleanup(up.Close)
	_, ps, _ := net.SplitHostPort(up.Listener.Addr().String())
	g.port, _ = strconv.Atoi(ps)
	st, err := auth.New(auth.Config{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	g.store = st
	g.p = &Proxy{Domain: "h.test", Main: "https://h.test", Auth: st, Deny: map[int]bool{7788: true}}
	g.host = strconv.Itoa(g.port) + ".h.test"
	return g
}

func (g *rig) do(path, cookie string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "https://"+g.host+path, nil)
	if cookie != "" {
		r.AddCookie(&http.Cookie{Name: CookieName, Value: cookie})
		r.AddCookie(&http.Cookie{Name: "app", Value: "keep"}) // 被代理那个服务自己的 cookie
	}
	w := httptest.NewRecorder()
	g.p.ServeHTTP(w, r)
	return w
}

func (g *rig) login(t *testing.T, device string) string {
	tk := g.p.Mint(device, g.port)
	w := g.do(AuthPath+"?t="+tk+"&to="+url.QueryEscape("/x?y=1"), "")
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/x?y=1" {
		t.Fatalf("换 cookie 那一跳不对：%d %s", w.Code, w.Header().Get("Location"))
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == CookieName {
			if !c.Secure || !c.HttpOnly || c.Path != "/" || c.Domain != "" {
				t.Fatalf("子域名 cookie 必须 Secure + HttpOnly + Path=/ + 不带 Domain：%+v", c)
			}
			return c.Value
		}
	}
	t.Fatal("没发 cookie")
	return ""
}

// 整条路：没登录跳主站签票 → 拿票换 cookie → 代理到 127.0.0.1:<端口>。
func TestFlow(t *testing.T) {
	g := newRig(t)
	d, _ := g.store.NewDevice("test", "1.2.3.4", "")

	w := g.do("/a/b?c=1", "")
	if w.Code != http.StatusFound || !strings.HasPrefix(w.Header().Get("Location"), "https://h.test/_port/"+strconv.Itoa(g.port)+"?to=") {
		t.Fatalf("没登录该跳主站签票：%d %s", w.Code, w.Header().Get("Location"))
	}

	ck := g.login(t, d.ID)
	w = g.do("/page", ck)
	if w.Code != 200 || w.Body.String() != "hello from /page" {
		t.Fatalf("代理不通：%d %q", w.Code, w.Body.String())
	}
	if g.gotHost != "localhost:"+strconv.Itoa(g.port) {
		t.Errorf("Host 该写成 localhost（开发服务器只认它）：%q", g.gotHost)
	}
	if strings.Contains(g.gotCk, CookieName) || !strings.Contains(g.gotCk, "app=keep") {
		t.Errorf("我们那张 cookie 绝不能转给被代理的服务，它自己的要留着：%q", g.gotCk)
	}

	// 开发服务器跳到它自己眼里的地址 → 换回子域名
	w = g.do("/go", ck)
	if got := w.Header().Get("Location"); got != "https://"+g.host+"/there" {
		t.Errorf("Location 没换回子域名：%q", got)
	}
}

// 票一次性、绑死端口；设备撤销后子域名当场失效。
func TestTicketAndRevoke(t *testing.T) {
	g := newRig(t)
	d, _ := g.store.NewDevice("test", "1.2.3.4", "")

	other := g.p.Mint(d.ID, g.port+1)
	if w := g.do(AuthPath+"?t="+other, ""); len(w.Result().Cookies()) != 0 {
		t.Fatal("别的端口的票不能在这个端口上换 cookie")
	}
	tk := g.p.Mint(d.ID, g.port)
	g.do(AuthPath+"?t="+tk, "")
	if w := g.do(AuthPath+"?t="+tk, ""); len(w.Result().Cookies()) != 0 {
		t.Fatal("票用过一次就作废")
	}

	ck := g.login(t, d.ID)
	g.store.Revoke(d.ID)
	if w := g.do("/page", ck); w.Code != http.StatusFound {
		t.Fatalf("设备撤销之后子域名必须当场失效：%d", w.Code)
	}

	// 重验 passkey 的时候到了：同样失效
	d2, _ := g.store.NewDevice("test", "1.2.3.4", "")
	ck = g.login(t, d2.ID)
	g.p.Reauth = func(*auth.Ident) bool { return true }
	if w := g.do("/page", ck); w.Code != http.StatusFound {
		t.Fatalf("该重验 passkey 了，子域名必须一起失效：%d", w.Code)
	}
}

func TestPortAndSafePath(t *testing.T) {
	p := &Proxy{Domain: "h.test", Deny: map[int]bool{7788: true}}
	for host, want := range map[string]bool{
		"7830.h.test": true, "7830.h.test:443": true, "7788.h.test": false, "80.h.test": false,
		"07830.h.test": false, "a.7830.h.test": false, "h.test": false, "7830.evil.test": false,
	} {
		if _, ok := p.Port(host); ok != want {
			t.Errorf("Port(%q) = %v，想要 %v", host, ok, want)
		}
	}
	for in, want := range map[string]string{
		"/x?y=1": "/x?y=1", "//evil.com/x": "/", "https://evil.com": "/", "/\\evil.com": "/", "": "/",
	} {
		if got := SafePath(in); got != want {
			t.Errorf("SafePath(%q) = %q，想要 %q", in, got, want)
		}
	}
}

// 别的站（哪怕同站，bysir.top 下别的子域名）发来的跨源请求一律拒：开发服务器常常对任意
// Origin 回带凭据的 CORS，Lax cookie 在同站请求上又会带着 —— 不拦就是「同站任何页面都能读」。
func TestForeignOriginRejected(t *testing.T) {
	g := newRig(t)
	d, _ := g.store.NewDevice("test", "1.2.3.4", "")
	ck := g.login(t, d.ID)
	try := func(origin string) int {
		r := httptest.NewRequest(http.MethodGet, "https://"+g.host+"/page", nil)
		r.AddCookie(&http.Cookie{Name: CookieName, Value: ck})
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		g.p.ServeHTTP(w, r)
		return w.Code
	}
	for origin, want := range map[string]int{
		"":                     200, // 地址栏打开
		"https://" + g.host:    200, // 自己
		"https://7831.h.test":  200, // 别的端口（前端调后端）
		"https://blog.h.test":  403, // 同站别的站
		"https://evil.example": 403,
		"http://" + g.host:     403, // 不是 https
	} {
		if got := try(origin); got != want {
			t.Errorf("Origin %q：%d，想要 %d", origin, got, want)
		}
	}
}
