package acme

import (
	"log"
	"time"

	"github.com/go-acme/lego/v4/challenge/dns01"
	"github.com/miekg/dns"
)

// 本机的 53 端口被代理劫持时（Clash / sing-box / Surge 的 TUN 模式 + dns-hijack），
// lego 默认那道「等 DNS 传播」的自检**永远等不到**，签发必然超时失败。
//
// 原因（在一台开着 sing-box TUN 的 Mac 上实测）：lego 刚写完 TXT 就直接去问权威服务器
// （`rocco.ns.cloudflare.com`）有没有这条记录。而 TUN 劫持的是**发往任何 IP 的 53 端口**，
// UDP 和 TCP 都劫：连 `dig @108.162.195.121`（Cloudflare 权威的真 IP）回来的都是代理
// 自己缓存里的答案（带 `ra`、TTL 一直在往下走）。于是这次查询拿到的要么是缓存的旧答案，
// 要么是**否定缓存**：「没有这条 TXT」能被缓存到 SOA minimum 那么久，Cloudflare 上是
// 1800 秒。lego 在超时之前一直看不到这条记录。
//
// 而本机这道自检本来就是可有可无的：真正去查 TXT 的是 Let's Encrypt **自己的**解析器，
// 它们不在你的代理后面。所以认出劫持之后改成「写完 TXT 固定等一段时间就交给 LE 去验」。
// 不去找一条绕过代理的查询路：53 端口整个被劫走了，DoH 走的又是递归解析器（会缓存
// 我们第一次查到的「没有」），没有一条路能问到权威服务器的原话。

// probeAddr 是 TEST-NET-1（RFC 5737，文档专用地址），真实的互联网上**不会有任何东西
// 在这个地址上回应**。所以只要它答了，就说明 53 端口在本机这一侧被截走了。
// 判据跟用的是哪个代理、哪家 DNS 服务商都无关：权威服务器带不带 `ra`、NS 是不是解析成
// 198.18/15 的 fake-ip，这些都是具体某个代理的长相，换一个代理就不灵了。
var probeAddr = "192.0.2.1:53"

// hijackWait 是认出劫持之后，写完 TXT 到交给 LE 验证之间固定等的时间。
// 各家 DNS 服务商（Cloudflare / 阿里云 / DNSPod）写入后全网生效一般在十几秒以内，
// 等 60 秒留足余量。代价是每次签发多等一分钟，而签发三个月才一次。
var hijackWait = 60 * time.Second

// dnsHijacked 往一个不该有人应答的地址发一个 DNS 查询，收到回答就说明 53 端口被劫持了。
// 没被劫持时要等满超时，不过这只在签发时跑一次。
func dnsHijacked() bool {
	m := new(dns.Msg)
	m.SetQuestion("example.com.", dns.TypeSOA)
	c := &dns.Client{Net: "udp", Timeout: 1500 * time.Millisecond}
	r, _, err := c.Exchange(m, probeAddr)
	return err == nil && r != nil
}

// propagationOpts 按本机 DNS 的情况决定「等 DNS 传播」那道自检怎么做。
// 正常情况下不改，lego 默认的自检是有用的：它能挡掉「记录还没生效就交给 LE，于是验证
// 失败、白白占掉一次失败配额」这种情况。
func propagationOpts() []dns01.ChallengeOption {
	if !dnsHijacked() {
		return nil
	}
	log.Printf("本机的 DNS 查询被代理截走了（TUN 模式？），没法在本机确认 TXT 记录已经生效；"+
		"改成写完等 %s 直接交给 Let's Encrypt 验证", hijackWait)
	return []dns01.ChallengeOption{dns01.PropagationWait(hijackWait, true)}
}
