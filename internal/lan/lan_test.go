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
