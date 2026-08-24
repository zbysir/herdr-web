package lan

import (
	"strings"
	"testing"
	"time"
)

// Origins 是 CSP 和「网页去嗅探哪个地址」的共同来源。这两条钉住的都是**静默出错**的
// 边界：端口是 0 时必须什么都不给（否则 CSP 里会出现 `https://ip:0`，整条 CSP 报废），
// 公网 IP 必须滤掉（那等于把「从任何地方都能直连我」写进 CSP）。
func TestOriginsPortZero(t *testing.T) {
	if got := Origins(0); got != nil {
		t.Errorf("端口 0 应当什么都不给，得到 %v", got)
	}
	if got := Origins(-1); got != nil {
		t.Errorf("负端口应当什么都不给，得到 %v", got)
	}
}

func TestOriginsOnlyPrivate(t *testing.T) {
	ResetCache()
	defer ResetCache()
	for _, o := range Origins(7789) {
		if !strings.HasPrefix(o, "https://") {
			t.Errorf("%q 不是 https —— 明文的口嗅探不到（mixed content）", o)
		}
		host := strings.TrimSuffix(strings.TrimPrefix(o, "https://"), ":7789")
		if !IsPrivate(host) {
			t.Errorf("%q 不是私网地址，不该出现在候选里", o)
		}
	}
}

// 缓存：CSP 头要在每个响应上带当前地址，没有缓存的话一次开页面就是几十次
// net.Interfaces()。同时它**必须会过期** —— 换个网之后地址得跟上。
func TestCurrentCached(t *testing.T) {
	ResetCache()
	defer func() { Now = time.Now; ResetCache() }()

	base := time.Now()
	Now = func() time.Time { return base }
	first := Current()
	Now = func() time.Time { return base.Add(CacheTTL / 2) }
	if &first[0] != &Current()[0] && len(first) > 0 {
		t.Error("TTL 之内应当返回同一份缓存")
	}
	Now = func() time.Time { return base.Add(CacheTTL + time.Second) }
	if len(first) > 0 && &first[0] == &Current()[0] {
		t.Error("过了 TTL 应当重新枚举一遍")
	}
}

// 虚拟网卡（bridge / utun / vmnet / docker / OrbStack 那段）必须不进候选：手机碰不到
// 它们，留着只会白占一个嗅探名额，还把每个响应的 CSP 头拉长。
func TestVirtualNotCandidate(t *testing.T) {
	ResetCache()
	defer ResetCache()
	for _, a := range Current() {
		if !a.Virtual {
			continue
		}
		for _, o := range Origins(7789) {
			if strings.Contains(o, a.Address) {
				t.Errorf("虚拟网卡 %s 上的 %s 不该出现在候选里", a.Name, a.Address)
			}
		}
	}
}

// 「从直连口进来」要真的等于「对端在本地网络里」，不能是拓扑假设。
// 最要紧的一行是那个全局 IPv6：这台机器上 lsof 实测直连口是**双栈**（`*:port`），
// 而家宽 IPv6 通常没有 NAT —— 不查对端的话，公网可以直接连上这个口。
func TestPeerIsLocal(t *testing.T) {
	local := []string{
		"192.168.1.42:5000", "10.1.2.3:1", "172.16.0.9:80", // RFC1918
		"127.0.0.1:5000", "[::1]:5000", // 本机
		"[fd00::1]:5000",             // ULA
		"[fe80::1cff:fe00:1]:5000",   // 链路本地
		"[::ffff:192.168.1.42]:5000", // 双栈套接字上的 IPv4 客户端就是这个形状
		"192.168.1.42",               // 不带端口也要认
	}
	for _, a := range local {
		if !PeerIsLocal(a) {
			t.Errorf("%s 应当算本地", a)
		}
	}
	remote := []string{
		"[240e:39d:5a:6d20::9fd]:5000", // 全局 IPv6 —— 这条最要命
		"1.2.3.4:5000", "8.8.8.8:53",
		"[2001:db8::1]:443",
		"100.64.0.1:5000", // CGNAT 不算（理由见 PeerIsLocal 的注释）
		"", "不是地址:80", "example.com:443",
	}
	for _, a := range remote {
		if PeerIsLocal(a) {
			t.Errorf("%s 不该算本地", a)
		}
	}
}

// 主口的准入判据比直连口宽一档：多收 CGNAT（Tailscale / Headscale 那段）。
// 收错的表现是「走 VPN 的人页面打不开」—— 而 VPN 是文档里推荐的第一档形态。
func TestPeerIsPrivateOrVPN(t *testing.T) {
	allow := []string{
		"127.0.0.1:5000", "[::1]:5000",
		"192.168.1.42:5000", "10.1.2.3:1", "172.16.0.9:80",
		"[fd00::1]:5000", "[fd7a:115c:a1e0::1]:5000", // ULA，Tailscale 的 IPv6 也在这儿
		"[fe80::1cff:fe00:1]:5000",
		"100.64.0.1:5000", "100.101.102.103:5000", "100.127.255.254:5000", // CGNAT
	}
	for _, a := range allow {
		if !PeerIsPrivateOrVPN(a) {
			t.Errorf("%s 应当放行", a)
		}
	}
	deny := []string{
		"1.2.3.4:5000", "8.8.8.8:53",
		"[240e:39d:5a:6d20::9fd]:5000", "[2001:db8::1]:443",
		"100.63.255.255:5000", "100.128.0.1:5000", // CGNAT 段的两侧边界，都是公网
		"", "不是地址:80", "example.com:443",
	}
	for _, a := range deny {
		if PeerIsPrivateOrVPN(a) {
			t.Errorf("%s 不该放行", a)
		}
	}
}
