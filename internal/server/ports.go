package server

import (
	"log"
	"net/http"
	"sync"

	"github.com/zbysir/herdr-web/internal/auth"
	"github.com/zbysir/herdr-web/internal/lan"
)

// 端口分工。几个入口，规则各不一样，**判据一律是「落在哪个监听上」**，不是源 IP、
// 更不是 Host（Host 是客户端说的，源 IP 在隧道后面全是 127.0.0.1 或者中继说的那个）：
//
//	主口 HERDR_WEB_PORT（默认 7788）     只服务本地网络       PrivateListener
//	中继隧道 HERDR_WEB_RELAY_*            不监听端口、主动连出去  PublicListener
//	局域网直连口 HERDR_WEB_LAN_PORT       自签 TLS、听 0.0.0.0    LanListener（lanapi.go）
//	管理口 主口+1                        只绑 127.0.0.1         internal/admin
//
// 从公网进来**只有中继一条路**（docs/dev/RELAY.md）。以前还有一个给 frp / 反代指的公网口
// （PUBLIC_PORT）和一个「主口就是公网口」的声明（EXPOSED），2026-10-23 一起下线：那两条路
// 上 TLS 常常在前面那层解密，那一层被拿下就是整个终端。
//
// 主口那道门**没有任何开关能让开**：在这台机器上写代码的人（尤其是 agent）看到的是
// `127.0.0.1:7788`，没有任何办法知道机器上还跑着一条隧道正把它转到公网 —— 于是「反正
// 只有本机能连」这个前提下做的每个决定（打开本机免配对、不配 TLS、临时关掉鉴权调一下）
// 都会变成公网上的一个洞。

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
// 127.0.0.1，这道检查看不出来。主口没被谁转发出去，是这套设计的前提，不是这个函数能
// 验证的事 —— 要从公网进来就走中继。
//
// 二、摘掉 X-Forwarded-For / X-Real-Ip / Forwarded。主口前面**没有任何可信前置**，到这儿
// 的转发头一定是客户端自己塞的。ClientIP 已经不看它们了，摘掉是为了 trustLoopback 那道
// 「带了代理头就不算本机」别被一个假头触发得莫名其妙。
func PrivateListener(next http.Handler) http.Handler {
	var once sync.Once
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !lan.PeerIsPrivateOrVPN(r.RemoteAddr) {
			once.Do(func() {
				log.Printf("\n  ⚠️  主口收到了来自 %s 的连接 —— 那不是本地地址，已拒绝。\n"+
					"     这个口只服务本地网络（这也是它的所有默认值成立的前提）。\n"+
					"     真要从公网访问：走云端中继（HERDR_WEB_RELAY_*，见 docs/dev/RELAY.md），\n"+
					"     别把隧道 / 端口转发指到这个口上。\n", r.RemoteAddr)
			})
			w.Header().Set("cache-control", "no-store")
			http.Error(w, "这个口只服务本地网络。公网访问请走云端中继。",
				http.StatusForbidden)
			return
		}
		r.Header.Del("X-Forwarded-For")
		r.Header.Del("X-Real-Ip")
		r.Header.Del("Forwarded")
		next.ServeHTTP(w, r)
	})
}

// PublicListener 套在中继隧道上：盖一个「从公网进来」的章，认证那边的几个「因为你在
// 本机所以放你进来」的豁免全部据此关掉（见 auth.FromPublicPort）。真实客户端 IP 由隧道
// 填进 RemoteAddr（relay.srcConn），不看任何转发头。
func PublicListener(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, auth.MarkPublicPort(r))
	})
}
