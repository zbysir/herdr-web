package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zbysir/herdr-web/internal/config"
	"github.com/zbysir/herdr-web/internal/files"
)

func rawServer(t *testing.T) *Server {
	t.Helper()
	sign, err := files.NewSigner()
	if err != nil {
		t.Fatal(err)
	}
	return &Server{
		Cfg:   &config.Config{Files: true},
		Files: &files.Browser{Enabled: true},
		Sign:  sign,
	}
}

func getRaw(t *testing.T, s *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("GET", s.rawURL(path), nil)
	w := httptest.NewRecorder()
	s.handleFileRaw(w, r)
	return w
}

// 这个测试盯的是整个功能里唯一真正危险的地方：**从本站的源上吐出一个 agent 写的文件**。
//
// 同源的 text/html 就是一个能调 /api/herdr/say 的跳板（cookie 是 HttpOnly，但它
// 根本不需要读 cookie —— 浏览器会自动带上）。所以 content-type 只能有两种，
// 而且只有魔数认出来的图才允许 inline。
func TestRawNeverServesHTML(t *testing.T) {
	dir := t.TempDir()
	s := rawServer(t)

	evil := filepath.Join(dir, "report.html")
	if err := os.WriteFile(evil, []byte(`<script>fetch('/api/herdr/say',{method:'POST'})</script>`), 0o644); err != nil {
		t.Fatal(err)
	}
	// 「一份 HTML 里顺手嵌了个 svg」也必须走附件 —— 这条最容易被 isSVG 放过去
	wrapped := filepath.Join(dir, "wrapped.html")
	if err := os.WriteFile(wrapped, []byte(`<html><body><svg><script>alert(1)</script></svg></body></html>`), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{evil, wrapped} {
		w := getRaw(t, s, p)
		if w.Code != 200 {
			t.Fatalf("%s：HTTP %d %s", filepath.Base(p), w.Code, w.Body.String())
		}
		ct := w.Header().Get("content-type")
		if ct != "application/octet-stream" {
			t.Errorf("%s 的 content-type = %q，只能是 application/octet-stream", filepath.Base(p), ct)
		}
		if cd := w.Header().Get("content-disposition"); !strings.HasPrefix(cd, "attachment") {
			t.Errorf("%s 的 content-disposition = %q，必须是 attachment", filepath.Base(p), cd)
		}
	}
}

func TestRawImageInline(t *testing.T) {
	dir := t.TempDir()
	s := rawServer(t)
	png := filepath.Join(dir, "chart.png")
	body := append([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}, make([]byte, 64)...)
	if err := os.WriteFile(png, body, 0o644); err != nil {
		t.Fatal(err)
	}
	w := getRaw(t, s, png)
	if w.Code != 200 {
		t.Fatalf("HTTP %d %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("content-type"); ct != "image/png" {
		t.Errorf("content-type = %q", ct)
	}
	if cd := w.Header().Get("content-disposition"); !strings.HasPrefix(cd, "inline") {
		t.Errorf("图应该 inline（不然点开就是下载），拿到 %q", cd)
	}
	// 这三条是万一上面两条哪天被改坏了的兜底
	if csp := w.Header().Get("content-security-policy"); csp != "sandbox" {
		t.Errorf("这个口必须再压一条 sandbox，拿到 %q", csp)
	}
	if w.Header().Get("x-content-type-options") != "nosniff" {
		t.Error("少了 nosniff：浏览器会自己重新猜类型")
	}
	if cc := w.Header().Get("cache-control"); !strings.Contains(cc, "no-store") {
		t.Errorf("别把别人磁盘上的文件留在缓存里，拿到 %q", cc)
	}
}

func TestRawRejectsBadToken(t *testing.T) {
	s := rawServer(t)
	for _, u := range []string{"/_f/", "/_f/garbage", "/_f/a.b/x.png"} {
		w := httptest.NewRecorder()
		s.handleFileRaw(w, httptest.NewRequest("GET", u, nil))
		if w.Code != http.StatusForbidden {
			t.Errorf("%s → HTTP %d，想要 403", u, w.Code)
		}
	}
	// 关掉之后整条路 404，不是 403 —— 「没有这个功能」和「票不对」是两回事
	off := rawServer(t)
	off.Files.Enabled = false
	w := httptest.NewRecorder()
	off.handleFileRaw(w, httptest.NewRequest("GET", "/_f/x.y", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("关掉之后应该 404，拿到 %d", w.Code)
	}
}

// 文件名是磁盘上来的，可能含引号和换行 —— 直接拼进响应头就是头注入。
func TestDispositionEscapes(t *testing.T) {
	got := disposition("attachment", "a\"b\r\nX-Evil: 1.png")
	if strings.ContainsAny(got, "\r\n") {
		t.Fatalf("响应头里带了换行：%q", got)
	}
	if strings.Contains(got, `"a"b`) {
		t.Fatalf("引号没剥干净：%q", got)
	}
	if !strings.Contains(got, "filename*=UTF-8''") {
		t.Errorf("真正的名字要走 RFC 5987 的 filename*：%q", got)
	}
	// 全是控制字符时也得有个名字，不能拼出一个空 filename=""
	if !strings.Contains(disposition("inline", "\x01\x02"), `filename="download"`) {
		t.Error("名字全是控制字符时该退回 download")
	}
	// 中文名要能原样还原
	if !strings.Contains(disposition("inline", "图.png"), "filename*=UTF-8''%E5%9B%BE.png") {
		t.Errorf("中文名没编对：%q", disposition("inline", "图.png"))
	}
}

// 票只在 TTL 之内有效，而且换一个进程（换一把密钥）就全废。
func TestRawTokenExpires(t *testing.T) {
	dir := t.TempDir()
	s := rawServer(t)
	p := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(p, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := s.Sign.Sign(p, time.Now().Add(-files.TokenTTL-time.Minute))
	w := httptest.NewRecorder()
	s.handleFileRaw(w, httptest.NewRequest("GET", "/_f/"+old+"/a.txt", nil))
	if w.Code != http.StatusForbidden {
		t.Errorf("过期的票拿到 HTTP %d，想要 403", w.Code)
	}
}

// SVG 认成图之后**必须**多带一条自己的 CSP。
//
// 它和 png 不一样：png 顶层打开就是张图，而 SVG 顶层打开是一份**能跑脚本的文档**。
// 查看器里走 <img>（规范上就不跑脚本，不看响应头），这条 CSP 管的是「在新标签打开」
// 那条路：sandbox 掐掉执行、default-src 'none' 堵掉外链。
func TestRawSVGInlineWithTightCSP(t *testing.T) {
	dir := t.TempDir()
	s := rawServer(t)
	svg := filepath.Join(dir, "chart.svg")
	body := `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><script>alert(1)</script></svg>`
	if err := os.WriteFile(svg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	w := getRaw(t, s, svg)
	if w.Code != 200 {
		t.Fatalf("HTTP %d %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("content-type"); ct != "image/svg+xml" {
		t.Errorf("content-type = %q", ct)
	}
	if cd := w.Header().Get("content-disposition"); !strings.HasPrefix(cd, "inline") {
		t.Errorf("要 inline 才看得见，拿到 %q", cd)
	}
	csp := w.Header().Get("content-security-policy")
	if !strings.Contains(csp, "sandbox") {
		t.Errorf("少了 sandbox：顶层打开时 svg 里的 <script> 就跑起来了。csp=%q", csp)
	}
	if !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("少了 default-src 'none'：svg 里塞个外链就能把「你打开过这张图」发出去。csp=%q", csp)
	}
	// 图表类 svg 基本都带 style，堵死了画出来一片黑
	if !strings.Contains(csp, "style-src 'unsafe-inline'") {
		t.Errorf("style 得放行，否则 svg 渲染出来没有颜色。csp=%q", csp)
	}
}
