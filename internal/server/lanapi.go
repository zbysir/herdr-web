package server

import (
	"net/http"

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

// apiHandoff 出一个一次性配对码，只给**已经认证的会话**，专门用来把凭据带到局域网那个
// origin 上去。
//
// 直接复用配对码而不是另发一种令牌：Redeem 那条路已经是「一次性 + 会过期 + 兑换出来的
// 是一台正常的设备（能在设备面板里看到、能撤销）」，另造一种只会多一套要维护的过期和
// 撤销语义。MintCode 不作废已有的码，所以这个调用不会把终端横幅上那个码顶掉。
//
// 落地那边已经认证过的话这个码根本不会被兑换（handleRoot 的 enter 先看有没有凭据），
// 于是它自己过期 —— 所以「每次切过去都要一个码」不会堆出一串设备。
func (s *Server) apiHandoff(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, errf("只收 POST"))
		return
	}
	if s.LanPort <= 0 {
		fail(w, 404, errf("这个部署没开局域网直连"))
		return
	}
	code, exp := s.Auth.MintCode()
	writeJSON(w, 200, map[string]any{"code": code, "expires": exp})
}
