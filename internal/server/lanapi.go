package server

import (
	"context"
	"log"
	"net/http"
	"sync"

	"github.com/zbysir/herdr-web/internal/lan"
)

// 局域网直连：网页从公网那条路（隧道 / 反代）加载之后，去嗅探「同一个局域网里能不能
// 直接连到我」，能就换过去。一次按键的往返从「两跳公网」变成「一跳交换机」。
//
// 这条路上有三个地方**必须一起对**，少一个就是静默不生效：
//
//  1. 局域网那个口必须是 **TLS**。https 页面对 http 目标的 fetch 一律算 active mixed
//     content 被浏览器拦死（`mode:'no-cors'` 也拦），所以明文的口连嗅探都发不出去。
//     见 config.Config.LanPort。
//  2. 那些 origin 必须进 **CSP 的 connect-src**。默认是 `'self'`，跨 origin 的嗅探会被
//     自己的 CSP 挡掉 —— 而 CSP 拦下来的 fetch 报的错和「连不上」长得一样，很容易
//     误判成「局域网不通」。见 guard.go 的 secHeaders。
//  3. 换过去是**换了 origin**，cookie 是 host-only 的，所以新 origin 上一份凭据都没有。
//     靠 apiHandoff 出一个一次性配对码带过去（就是二维码那套机制），落地时
//     handleRoot 的 `?pair=` 自己换成 cookie 再把 URL 洗干净。
//
// 为什么候选是「服务端每次现报」而不是配一个固定地址：局域网 IP 会变（DHCP、换 Wi-Fi、
// 插网线）。配死一个地址的表现是「某天开始就是不加速了，而且没有任何报错」。
func (s *Server) lanInfo() map[string]any {
	if s.LanPort <= 0 {
		return nil
	}
	origins := lan.Origins(s.LanPort)
	if len(origins) == 0 {
		return nil // 一个私网地址都没有（纯本机 / 全是虚拟网卡）
	}
	return map[string]any{"port": s.LanPort, "origins": origins}
}

// apiHandoff 出一枚**局域网直连专用**的交接令牌，只给已认证的会话。
//
// 以前这里出的是一枚正常的配对码（`MintCode`），那是个洞：SECURITY.md §11 明确写了
// 「网页上不出配对码」，理由是配对码创造的是一份**不随创造者一起被撤销**的独立凭据 ——
// 于是一份被偷的 cookie 就成了无限发凭据的机器，`revoke <id>` 变成打地鼠。现在换成
// auth.MintHandoff：60 秒、一次性、**只能在直连那个监听上兑换**、兑出来的设备随上级
// 一起被撤销。三条里少任何一条，§11 那条理由就原样成立。
//
// 要求上级是一台**真设备**：本机免配对 / 旧 token 那两种会话没有设备 ID，也就没有东西
// 能级联撤销 —— 而那两种都在机器上，压根不需要局域网直连。
func (s *Server) apiHandoff(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, errf("只收 POST"))
		return
	}
	if s.LanPort <= 0 {
		fail(w, 404, errf("这个部署没开局域网直连"))
		return
	}
	id := s.requireAuth(w, r)
	if id == nil {
		return
	}
	if id.Device == nil {
		fail(w, http.StatusConflict, errf("这种会话不能交接（没有可级联撤销的上级凭据）"))
		return
	}
	tok, exp := s.Auth.MintHandoff(id.Device.ID)
	writeJSON(w, 200, map[string]any{"handoff": tok, "expires": exp})
}

type ctxKey int

// lanKey 标记「这个请求是从局域网直连那个监听进来的」。
const lanKey ctxKey = iota

// FromLan 这个请求是不是落在局域网直连那个监听上。
//
// **判据只能是「落在哪个监听上」，绝不能看 Host。** Host 是客户端说的，而 hostOK 对 IP
// 字面量一律放行（那本身是对的，见 guard.go），所以从公网那条路发一个
// `Host: 192.168.1.5:7790` 就能把「看起来像内网」伪造出来。交接令牌的兑换门槛压在这上面，
// 看错一处等于那道门不存在。
func FromLan(r *http.Request) bool {
	v, _ := r.Context().Value(lanKey).(bool)
	return v
}

// LanListener 套在局域网直连那个监听上的中间件，干两件事。
//
// 一、摘掉转发头。那个口**不在任何前置后面**（前置只在公网那条路上），所以到它这儿的
// `X-Forwarded-For` 一定是客户端自己塞的。而配了 `HERDR_WEB_TRUST_PROXY=1` 的部署里
// ClientIP 会照信 —— 于是同一个 Wi-Fi 上的人用一串假 XFF 就能把按 IP 的限速绕干净，
// 而配对码猜解那道门正好在它后面（全局熔断还在，但那是最后一道，不该让它变成唯一一道）。
//
// 摘掉之后 ClientIP 只能拿到真的 RemoteAddr —— 直连口上那本来就是准的，这也是它比
// 隧道那条路强的地方（frp 的 tcp 模式下所有人都是 127.0.0.1）。
//
// 二、盖一个「从直连口进来的」的章，供 FromLan 读。交接令牌只在这种请求上才兑得动。
func LanListener(next http.Handler) http.Handler {
	var once sync.Once
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 三、**对端必须在本地网络里**，否则这个口一个字节都不服务。
		//
		// 「绑通配地址 = 只有局域网碰得到」是拓扑假设，不是代码保证：端口转发、
		// UPnP、或者一台有全局 IPv6 的机器（Go 对 0.0.0.0 开的是双栈套接字，而家宽
		// IPv6 通常没有 NAT）都能让公网直接连上来。见 lan.PeerIsLocal。
		//
		// 有了这道检查，「从直连口进来」才真的等于「对端在本地网络里」—— 交接令牌的
		// 兑换门槛压在这上面，不能是假设。
		if !lan.PeerIsLocal(r.RemoteAddr) {
			once.Do(func() {
				log.Printf("\n  ⚠️  局域网直连口收到了来自 %s 的连接 —— 那不是本地地址，已拒绝。\n"+
					"     这个口大概是被暴露到公网了（端口转发 / UPnP / 全局 IPv6 没被防火墙挡）。\n",
					r.RemoteAddr)
			})
			w.Header().Set("cache-control", "no-store")
			http.Error(w, "这个口只服务本地网络。", http.StatusForbidden)
			return
		}
		r.Header.Del("X-Forwarded-For")
		r.Header.Del("X-Real-Ip")
		r.Header.Del("Forwarded")
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), lanKey, true)))
	})
}
