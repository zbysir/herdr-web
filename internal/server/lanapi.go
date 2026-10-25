package server

import (
	"log"
	"net/http"
	"sync"

	"github.com/zbysir/herdr-web/internal/lan"
)

// 局域网直连：人在家时，连接不绕公网、直接走这台机器的局域网口。一次按键的往返从
// 「两跳公网」变成「一跳交换机」。
//
// **只有安卓 App 走这条路**（`LanRoute.kt`）：壳拿这几个地址按页面的域名做 TLS 握手
// （真证书、验域名），通了就让 WebView 的连接从那儿走 —— 地址栏、cookie、passkey 全不变。
//
// 网页自己那条（嗅探 → 带交接令牌跳到 `https://192.168.x.x:7790`）2026-10-25 下掉了。
// 它注定要碰一张「浏览器眼里不对」的证书（签给域名、按 IP 连），而人在那个 IP 上点过一次
// 「继续访问」之后，嗅探请求带着证书错误成功，Chromium 就把**公网那一页**也记成「加载过
// 证书有错的内容」—— passkey 从此一律 NotAllowedError、刷新都不好（用户报的「浏览器不给我
// 签 passkey 了」）。换 origin 本身就是那串麻烦的来路，见 docs/dev/RELAY.md §4。
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

// LanListener 套在局域网直连那个监听上的中间件。
//
// 一、摘掉转发头。那个口**不在任何前置后面**（前置只在公网那条路上），所以到它这儿的
// `X-Forwarded-For` 一定是客户端自己塞的。而配了 `HERDR_WEB_TRUST_PROXY=1` 的部署里
// ClientIP 会照信 —— 于是同一个 Wi-Fi 上的人用一串假 XFF 就能把按 IP 的限速绕干净，
// 而配对码猜解那道门正好在它后面（全局熔断还在，但那是最后一道，不该让它变成唯一一道）。
//
// 摘掉之后 ClientIP 只能拿到真的 RemoteAddr —— 直连口上那本来就是准的，这也是它比
// 隧道那条路强的地方（frp 的 tcp 模式下所有人都是 127.0.0.1）。
func LanListener(next http.Handler) http.Handler {
	var once sync.Once
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 二、**对端必须在本地网络里**，否则这个口一个字节都不服务。
		//
		// 「绑通配地址 = 只有局域网碰得到」是拓扑假设，不是代码保证：端口转发、
		// UPnP、或者一台有全局 IPv6 的机器（Go 对 0.0.0.0 开的是双栈套接字，而家宽
		// IPv6 通常没有 NAT）都能让公网直接连上来。见 lan.PeerIsLocal。
		//
		// 有了这道检查，「从直连口进来」才真的等于「对端在本地网络里」，不是假设。
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
		next.ServeHTTP(w, r)
	})
}
