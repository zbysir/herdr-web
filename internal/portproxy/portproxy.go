// Package portproxy 把这台机器上 agent 起的本地服务（`http://127.0.0.1:7830`）代理到
// `https://7830.<域名>/`，从外面也能看，而且要先过 herdr-web 自己的认证。
//
// 设计和「为什么」在 docs/dev/PORT-PROXY.md。三条承重的：
//
//  1. **每个端口一个子域名，绝不和主站同源。** 被代理的是 agent 写的页面、跑着任意 JS；同源的话
//     它带着主站的 cookie 就能调 /api/herdr/say 往任意 pane 里敲命令。子域名是另一个 origin，
//     主站那张 host-only 的 cookie 根本不发过来（路径前缀那条路因此被否掉了，另一个理由是
//     Vite / Next 的绝对路径和 HMR 在前缀下跑不通）。
//  2. **子域名自己认人，凭据从主站「借」过来**：没登录就跳主站 `/_port/<端口>`，主站按自己的
//     cookie + passkey 重验那一套放行，签一张 60 秒、一次性、绑死端口的票，跳回子域名换成只对
//     这个子域名有效的 cookie。**每个请求都回主站的设备表核一遍**（IdentByID）：设备被撤销、
//     该重验 passkey 了，子域名当场失效。
//  3. **这张 cookie 绝不转给被代理的服务**（它是一份能进这个子域名的凭据，而那个服务是 agent 的）。
//
// 会话和票都只在内存里：进程重启之后子域名上要再跳一次主站，而主站 cookie 还在，人感觉不到。
package portproxy

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"html"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zbysir/herdr-web/internal/auth"
)

// CookieName 子域名上那张会话 cookie。`__Host-` 前缀：浏览器保证它 Secure、Path=/、**不带
// Domain**（只属于这一个子域名），别的子域名设不了也盖不掉它。
const CookieName = "__Host-hw_port"

// AuthPath 是子域名上「拿票换 cookie」的那条路。挑一个开发服务器不会用到的路径。
const AuthPath = "/_herdr/auth"

const (
	ticketTTL  = 60 * time.Second
	sessionTTL = 12 * time.Hour
)

type Proxy struct {
	// Domain：端口代理挂在它下面，`7830.<Domain>`。
	Domain string
	// Main 是主站的地址（https://herdr.example.com），没登录时往那儿跳。
	Main string
	// Deny 是不许代理的端口（herdr-web 自己那几个口）。
	Deny map[int]bool
	Auth *auth.Store
	// Reauth：这个身份是不是该重验 passkey 了（和主站 requireAuth 同一个判据）。
	Reauth func(*auth.Ident) bool

	mu       sync.Mutex
	tickets  map[string]ticket
	sessions map[string]session
}

type ticket struct {
	device string
	port   int
	exp    time.Time
}

type session struct {
	device string
	port   int
	exp    time.Time
}

// Port 认出「这是一个端口子域名」并返回端口。只认纯数字的一段，不认更深的层级。
func (p *Proxy) Port(host string) (int, bool) {
	if p == nil || p.Domain == "" {
		return 0, false
	}
	if h, _, ok := strings.Cut(host, ":"); ok {
		host = h
	}
	host = strings.ToLower(host)
	label, ok := strings.CutSuffix(host, "."+p.Domain)
	if !ok || label == "" {
		return 0, false
	}
	n, err := strconv.Atoi(label)
	if err != nil || strconv.Itoa(n) != label {
		return 0, false // 只认规整的数字：「07830」「+7830」都不算
	}
	return n, p.Allowed(n)
}

// Allowed：1024 以下（系统服务）和 herdr-web 自己的口不代理。
//
// 为什么不收窄到「确实有 pane 在上面监听的端口」：能走到这一步的人已经过了主站认证，而主站
// 后面就是一个登录 shell —— 他想看哪个本地端口，curl 一下就看了。挡这几个口是防手滑和防
// 「绕一圈打到自己」（代理 herdr-web 自己 = 同源保护白做）。
func (p *Proxy) Allowed(port int) bool {
	return port >= 1024 && port <= 65535 && !p.Deny[port]
}

// URL 是某个端口的子域名地址。
func (p *Proxy) URL(port int, path string) string {
	return fmt.Sprintf("https://%d.%s%s", port, p.Domain, path)
}

func randHex() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (p *Proxy) gcLocked(now time.Time) {
	for k, t := range p.tickets {
		if now.After(t.exp) {
			delete(p.tickets, k)
		}
	}
	for k, s := range p.sessions {
		if now.After(s.exp) {
			delete(p.sessions, k)
		}
	}
}

// Mint 主站给一台已认证的设备签一张进 port 的票（60 秒、一次性、绑死端口）。
func (p *Proxy) Mint(device string, port int) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.tickets == nil {
		p.tickets = map[string]ticket{}
	}
	now := time.Now()
	p.gcLocked(now)
	t := randHex()
	p.tickets[t] = ticket{device: device, port: port, exp: now.Add(ticketTTL)}
	return t
}

func (p *Proxy) redeem(t string, port int) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	for k, v := range p.tickets {
		if subtle.ConstantTimeCompare([]byte(k), []byte(t)) == 1 {
			delete(p.tickets, k) // 一次性：对不上端口也作废，别留着给人再试
			if v.port != port || now.After(v.exp) {
				return "", false
			}
			if p.sessions == nil {
				p.sessions = map[string]session{}
			}
			s := randHex()
			p.sessions[s] = session{device: v.device, port: port, exp: now.Add(sessionTTL)}
			return s, true
		}
	}
	return "", false
}

// authed 这个请求带着的子域名会话还算不算数：会话在、端口对得上、背后那台设备还在、
// 而且不到重验 passkey 的时候。
func (p *Proxy) authed(r *http.Request, port int) bool {
	c, err := r.Cookie(CookieName)
	if err != nil || c.Value == "" {
		return false
	}
	p.mu.Lock()
	s, ok := p.sessions[c.Value]
	p.mu.Unlock()
	if !ok || s.port != port || time.Now().After(s.exp) {
		return false
	}
	id := p.Auth.IdentByID(s.device)
	if id == nil {
		return false // 设备被撤销 / 过期：子域名一起失效
	}
	return p.Reauth == nil || !p.Reauth(id)
}

// SafePath 只放行「本站的一个路径」：以 / 开头、不是 //（那是协议相对 URL，会跳到别的站）。
func SafePath(to string) string {
	if !strings.HasPrefix(to, "/") || strings.HasPrefix(to, "//") || strings.HasPrefix(to, "/\\") {
		return "/"
	}
	return to
}

// ServeHTTP 处理落在端口子域名上的请求（调用方已经用 Port 认过 Host）。
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	port, ok := p.Port(r.Host)
	if !ok {
		http.Error(w, "这个端口不代理", http.StatusForbidden)
		return
	}
	w.Header().Set("Referrer-Policy", "no-referrer") // 票在 URL 里，别经 Referer 带给被代理的页面引的外站

	if r.URL.Path == AuthPath {
		s, ok := p.redeem(r.URL.Query().Get("t"), port)
		if !ok {
			// 票过期 / 用过了：从头再来一遍（主站 cookie 还在的话人感觉不到）
			http.Redirect(w, r, p.entry(port, SafePath(r.URL.Query().Get("to"))), http.StatusFound)
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name: CookieName, Value: s, Path: "/",
			// Lax 不是 Strict：从别的 App（聊天软件、系统浏览器的历史）点进来的链接也得带上它，
			// 不然每次都要绕主站一圈。写操作的防护是被代理的那个服务自己的事
			Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
			MaxAge: int(sessionTTL / time.Second),
		})
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, SafePath(r.URL.Query().Get("to")), http.StatusFound)
		return
	}

	// 别的站发来的跨源请求一律拒（带 Origin、又不是我们自己的端口子域名）。
	//
	// 为什么要这一道：开发服务器常常对任意 Origin 回 `Access-Control-Allow-Origin: <你>` +
	// `Allow-Credentials: true`（图省事的 CORS 配置），而子域名那张 cookie 是 Lax —— 同站
	// （bysir.top 下别的站，比如托管在第三方平台上的博客）发来的请求会带上它。两样凑一起就是
	// 「bysir.top 下任何一个页面都能读你本机开发服务的内容」。端口之间互相调（前端 7830 调
	// 后端 7831）照样放行。没有 Origin 的（地址栏打开、普通 GET 子资源）不受影响
	if o := r.Header.Get("Origin"); o != "" && !p.ownOrigin(o) {
		http.Error(w, "跨站请求被拒", http.StatusForbidden)
		return
	}

	if !p.authed(r, port) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method == http.MethodGet && r.Header.Get("Upgrade") == "" {
			http.Redirect(w, r, p.entry(port, r.URL.RequestURI()), http.StatusFound)
			return
		}
		// 子资源 / WebSocket 跳不过去（跳了也没人看），直接拒；页面那一下会带人去登录
		http.Error(w, "先在 herdr-web 上登录", http.StatusUnauthorized)
		return
	}
	p.proxy(port).ServeHTTP(w, r)
}

// ownOrigin：Origin 是不是我们自己的某个端口子域名（https://<端口>.<Domain>）。
func (p *Proxy) ownOrigin(o string) bool {
	u, err := url.Parse(o)
	if err != nil || u.Scheme != "https" {
		return false
	}
	_, ok := p.Port(u.Host)
	return ok
}

// entry 是主站上「给这个端口签票」的那条路。
func (p *Proxy) entry(port int, to string) string {
	return fmt.Sprintf("%s/_port/%d?to=%s", strings.TrimRight(p.Main, "/"), port, url.QueryEscape(to))
}

func (p *Proxy) proxy(port int) http.Handler {
	target := &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", port)}
	local := fmt.Sprintf("localhost:%d", port)
	public := strings.TrimSuffix(p.URL(port, ""), "/")
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.SetXForwarded()
			// Host 写成 localhost：Vite / Next 这类开发服务器会拦不认识的 Host（Vite 5 起
			// 「Blocked request. This host is not allowed」），它们只认 localhost
			pr.Out.Host = local
			// Origin 同理（Vite 的 HMR WebSocket 核 Origin）。只换我们自己这个子域名的，别的原样
			if pr.Out.Header.Get("Origin") == public {
				pr.Out.Header.Set("Origin", "http://"+local)
			}
			stripCookie(pr.Out, CookieName)
		},
		ModifyResponse: func(resp *http.Response) error {
			// 开发服务器跳转写的是它自己眼里的地址（http://localhost:7830/xxx），换回子域名
			if loc := resp.Header.Get("Location"); loc != "" {
				for _, pre := range []string{"http://" + local, fmt.Sprintf("http://127.0.0.1:%d", port)} {
					if rest, ok := strings.CutPrefix(loc, pre); ok {
						resp.Header.Set("Location", public+rest)
					}
				}
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprintf(w, `<!doctype html><meta charset=utf-8><meta name=viewport content="width=device-width">`+
				`<body style="font:15px system-ui;padding:24px;background:#111;color:#ddd">`+
				`<p>这台机器的 <code>127.0.0.1:%d</code> 上没有服务在听。</p><p style="color:#888">%s</p>`,
				port, html.EscapeString(err.Error()))
		},
	}
}

// stripCookie 从发给被代理服务的 Cookie 头里去掉我们自己那张。
func stripCookie(r *http.Request, name string) {
	cs := r.Cookies()
	r.Header.Del("Cookie")
	for _, c := range cs {
		if c.Name != name {
			r.AddCookie(c)
		}
	}
}
