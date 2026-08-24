package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zbysir/herdr-web/internal/auth"
)

// 主口只服务本地网络。**公网 IP 直接连上来必须拒**：这种连接说明这个口已经被转发
// 出去了（路由器端口转发 / UPnP / 全局 IPv6），而主口的整套宽松默认（本机免配对、
// 不强制 TLS）建立在「公网碰不到」上面。
func TestPrivateListenerRefusesPublicPeer(t *testing.T) {
	var served bool
	h := PrivateListener(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { served = true }))

	for _, from := range []string{"1.2.3.4:5000", "[240e:39d:5a:6d20::9fd]:5000"} {
		served = false
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = from
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if served {
			t.Errorf("来自 %s 的请求被服务了 —— 主口那道门等于不存在", from)
		}
		if w.Code != http.StatusForbidden {
			t.Errorf("来自 %s：状态码 = %d，想要 403", from, w.Code)
		}
	}

	// 本机 / 局域网 / Tailscale（CGNAT 段）都得放进来 —— 拒错了的表现是「页面打不开」，
	// 而 VPN 那档恰恰是文档里推荐的第一形态。
	for _, from := range []string{"127.0.0.1:5000", "192.168.1.9:5000", "100.101.102.103:5000", "[fd7a:115c:a1e0::1]:5000"} {
		served = false
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = from
		h.ServeHTTP(httptest.NewRecorder(), r)
		if !served {
			t.Errorf("来自 %s 的请求被拒了，它应当算本地", from)
		}
	}
}

// 主口在私网模式下前面没有任何可信前置，所以转发头一定是客户端自己塞的。
// 留着的话，配了 TRUST_PROXY=1 的部署里局域网的人自带一串 XFF 就能把按 IP 的限速绕干净。
func TestPrivateListenerStripsForwardHeaders(t *testing.T) {
	var got http.Header
	h := PrivateListener(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = r.Header.Clone() }))
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "192.168.1.9:5000"
	r.Header.Set("X-Forwarded-For", "8.8.8.8")
	r.Header.Set("X-Real-Ip", "8.8.8.8")
	r.Header.Set("Forwarded", "for=8.8.8.8")
	h.ServeHTTP(httptest.NewRecorder(), r)
	for _, k := range []string{"X-Forwarded-For", "X-Real-Ip", "Forwarded"} {
		if got.Get(k) != "" {
			t.Errorf("%s 应当被摘掉，还剩 %q", k, got.Get(k))
		}
	}
}

// 公网口上的请求必须**盖得上章**，而且判据只能是「落在哪个监听上」：穿透进来的源地址
// 就是 127.0.0.1、Host 又是客户端自己说的，两者都伪造得出来。
// 认证那边所有「因为你在本机」的豁免都压在这个章上，看错一处等于那些豁免在公网上开着。
func TestPublicListenerMarksRequest(t *testing.T) {
	var marked bool
	inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { marked = auth.FromPublicPort(r) })

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "127.0.0.1:5000"
	r.Host = "localhost:7788" // 伪造成「本机」的形状
	inner.ServeHTTP(httptest.NewRecorder(), r)
	if marked {
		t.Error("没经过公网口的请求不该带章")
	}

	PublicListener(inner).ServeHTTP(httptest.NewRecorder(), r)
	if !marked {
		t.Error("从公网口进来的请求必须带章")
	}
}
