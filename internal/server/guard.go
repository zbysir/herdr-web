package server

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/zbysir/herdr-web/internal/lan"
)

// 门卫层：Host 白名单 + 跨站检查 + 安全响应头。
//
// 这一层和认证同等重要，因为它挡的是「浏览器帮攻击者带上凭据」这类攻击 —— 认证本身
// 一点问题都没有，凭据也没泄露，但请求是恶意页面发起的。

// hostOK 是 DNS rebinding 的唯一防线。
//
// 攻击长这样：攻击者把 evil.com 解析到 127.0.0.1（或你的局域网 IP），你一打开那个页面，
// 浏览器眼里它就和本服务同源了，于是 cookie 照发、响应也读得到。**IP 字面量一律放行**
// 是安全的：Host 头永远等于地址栏里的主机名，所以 Host 是 IP 就说明用户真的是照着 IP
// 访问的，那时候攻击者的页面和我们不同源。只有**域名**需要白名单。
func (s *Server) hostOK(r *http.Request) bool {
	if r.Host == "" {
		return false // HTTP/1.1 必须有 Host
	}
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(strings.Trim(host, "[]"))
	if net.ParseIP(host) != nil {
		return true
	}
	return s.names[host]
}

// originOK：带了 Origin 就必须同源。浏览器在跨源请求和 WebSocket 握手上一定会带这个头，
// 所以这条检查配合 SameSite=Strict 足够挡住跨站。没带 Origin 的是非浏览器客户端。
func (s *Server) originOK(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

// CSRFHeader 是给 /api 加的第三道：跨站请求设不了自定义头（会触发 preflight，而本服务
// 压根不答 preflight）。只在凭据是「浏览器自动带上的」时要求它 —— `?token=` 那种显式
// 凭据外站猜不到，老脚本不该被这条挡住。
const CSRFHeader = "X-Herdr-Web"

func csrfHeaderOK(r *http.Request) bool { return r.Header.Get(CSRFHeader) != "" }

// connectSrc 是 CSP 的 connect-src。默认只有 'self' —— 前端被注入也没法把终端内容外发。
//
// 开了局域网直连就得多放行**本机自己在局域网里的那几个 origin**，否则那次嗅探会被自己的
// CSP 挡掉。放宽的边界很窄：只有私网地址（见 lan.Origins），而且那头就是本进程。
//
// 为什么每个响应都现算而不是启动时拼死：IP 会变。拼死的表现是换个网之后 CSP 里还是旧
// 地址，嗅探被挡，而控制台里那条 CSP 报错和「连不上」长得一模一样。
func (s *Server) connectSrc() string {
	origins := lan.Origins(s.LanPort)
	if len(origins) == 0 {
		return "'self'"
	}
	return "'self' " + strings.Join(origins, " ")
}

func (s *Server) secHeaders(w http.ResponseWriter) {
	h := w.Header()
	// connect-src 'self' 顺带保证「万一前端被注入」也没法把终端内容外发。
	// style-src 要 'unsafe-inline'：xterm.js 自己插 <style>，面板拖动也在用行内 style。
	h.Set("content-security-policy", strings.Join([]string{
		"default-src 'self'",
		"connect-src " + s.connectSrc(),
		"img-src 'self' data: blob:",
		"style-src 'self' 'unsafe-inline'",
		"font-src 'self' data:",
		"object-src 'none'",
		"frame-ancestors 'none'",
		"base-uri 'none'",
		"form-action 'none'",
	}, "; "))
	h.Set("x-content-type-options", "nosniff")
	h.Set("x-frame-options", "DENY")
	h.Set("referrer-policy", "no-referrer")
	h.Set("permissions-policy", "geolocation=(), payment=(), usb=()")
	// 故意不发 HSTS：自签证书配上 HSTS 会把「继续访问」那个口也堵掉，而且清不掉。
}

func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.hostOK(r) {
			w.Header().Set("content-type", "text/plain; charset=utf-8")
			w.Header().Set("cache-control", "no-store")
			w.WriteHeader(http.StatusMisdirectedRequest)
			fmt.Fprintf(w, "Host 头里的 %q 不在白名单里，拒绝服务。\n\n"+
				"直接用 IP 访问就行；确实要用这个域名的话，启动时加\n"+
				"  HERDR_WEB_HOSTNAME=%s\n\n"+
				"这道检查挡的是 DNS rebinding：把一个域名解析到你的地址，"+
				"浏览器就会把它当成同源、连带 cookie 一起交出去。\n", r.Host, hostOnly(r.Host))
			return
		}
		s.secHeaders(w)
		next.ServeHTTP(w, r)
	})
}

func hostOnly(h string) string {
	if x, _, err := net.SplitHostPort(h); err == nil {
		return x
	}
	return h
}
