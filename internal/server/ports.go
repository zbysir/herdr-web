package server

import (
	"log"
	"net/http"
	"sync"

	"github.com/zbysir/herdr-web/internal/auth"
	"github.com/zbysir/herdr-web/internal/lan"
)

// 端口分工。四个监听，规则各不一样，**判据一律是「落在哪个监听上」**，不是源 IP、
// 更不是 Host（Host 是客户端说的，源 IP 在隧道后面全是 127.0.0.1）：
//
//	主口 HERDR_WEB_PORT（默认 7788）     只服务本地网络     PrivateListener
//	公网口 HERDR_WEB_PUBLIC_PORT（默认关） 隧道 / 反代该指这儿  PublicListener
//	局域网直连口 HERDR_WEB_LAN_PORT       自签 TLS、听 0.0.0.0  LanListener（lanapi.go）
//	管理口 主口+1                        只绑 127.0.0.1       internal/admin
//
// 为什么公网要单独一个口，而不是在主口上加一个 `EXPOSED=1` 开关（那个还留着兼容）：
// 开关是**声明**，而声明会漏。在这台机器上写代码的人（尤其是 agent）看到的是
// `127.0.0.1:7788`，它没有任何办法知道这台机器上还跑着一条隧道正把 7788 转到公网 ——
// 于是「反正只有本机能连」这个前提下做的每个决定（打开本机免配对、不配 TLS、临时关掉
// 鉴权调一下）都变成公网上的一个洞，而本地看起来一切正常。
//
// 换成「公网要另开一个显式端口」之后，这类事故的表现从「公网上一个免鉴权的 shell」
// 变成「隧道那头 connection refused」—— 一个可用性问题换掉一个送 shell 的问题。

// PrivateListener 套在主口上：对端不在本地网络里就一个字节都不服务，并且摘掉转发头。
//
// 一、**对端必须在私网 / 本机 / 链路本地 / CGNAT 里**（见 lan.PeerIsPrivateOrVPN）。
// 「绑 127.0.0.1 或者绑通配地址 = 公网碰不到」是拓扑假设，不是代码保证：路由器端口
// 转发、UPnP、或者一台有全局 IPv6 的机器（Go 对 0.0.0.0 开的是双栈套接字，家宽 IPv6
// 通常没有 NAT）都能让公网直接连上来。真收到这种连接就说明这个口已经在公网上了，
// 而主口的整套宽松默认建立在「碰不到」上面，所以当场拒掉、并且**吼一声**（静默拒绝
// 会变成「远程打不开，也不知道为什么」）。
//
// 挡不住的那种要说清楚：隧道（frp / Cloudflare Tunnel）从本机连过来，源地址就是
// 127.0.0.1，这道检查看不出来。挡它的是「隧道该指公网口」这条分工 —— 主口没被谁转发
// 出去，是这套设计的前提，不是这个函数能验证的事。
//
// 二、摘掉 X-Forwarded-For / X-Real-Ip / Forwarded。主口在私网模式下**前面没有任何
// 可信前置**（有的话那是公网口的活），所以到这儿的转发头一定是客户端自己塞的；而配了
// HERDR_WEB_TRUST_PROXY=1 的部署里 ClientIP 会照信 —— 局域网里的人用一串假 XFF 就能
// 把按 IP 的限速绕干净。
func PrivateListener(next http.Handler) http.Handler {
	var once sync.Once
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !lan.PeerIsPrivateOrVPN(r.RemoteAddr) {
			once.Do(func() {
				log.Printf("\n  ⚠️  主口收到了来自 %s 的连接 —— 那不是本地地址，已拒绝。\n"+
					"     这个口只服务本地网络（这也是它的所有默认值成立的前提）。\n"+
					"     真要从公网访问：开一个公网口 HERDR_WEB_PUBLIC_PORT=<另一个端口>，\n"+
					"     让隧道 / 端口转发 / 反代指那个口，别指这个。\n", r.RemoteAddr)
			})
			w.Header().Set("cache-control", "no-store")
			http.Error(w, "这个口只服务本地网络。公网访问请走 HERDR_WEB_PUBLIC_PORT 配的那个口。",
				http.StatusForbidden)
			return
		}
		r.Header.Del("X-Forwarded-For")
		r.Header.Del("X-Real-Ip")
		r.Header.Del("Forwarded")
		next.ServeHTTP(w, r)
	})
}

// PublicListener 套在公网口上：盖一个「从公网口进来」的章，认证那边的几个「因为你在
// 本机所以放你进来」的豁免全部据此关掉（见 auth.FromPublicPort）。
//
// 这里**不摘转发头**：公网口后面确实可能有一个自己的反代（HERDR_WEB_TRUST_PROXY=1
// 那档），XFF 是那时候唯一能拿到真实客户端 IP 的东西。没有可信前置就别开那个变量。
func PublicListener(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, auth.MarkPublicPort(r))
	})
}
