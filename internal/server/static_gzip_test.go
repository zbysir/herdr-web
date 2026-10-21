package server

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// assets 下的 JS 要按 gzip 回（隧道很窄时冷启动等二三十秒就是没压缩）；不认 gzip 的、
// 带 Range 的、太小的照旧回原文。
func TestStaticGzip(t *testing.T) {
	big := strings.Repeat("console.log('herdr');\n", 500)
	s := &Server{Web: fstest.MapFS{
		"index.html":       {Data: []byte("<html></html>")},
		"assets/index.js":  {Data: []byte(big)},
		"assets/small.css": {Data: []byte("a{}")},
	}}
	get := func(p string, hdr map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, p, nil)
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		s.handleStatic(w, r)
		return w
	}

	w := get("/assets/index.js", map[string]string{"Accept-Encoding": "gzip, br"})
	if w.Header().Get("content-encoding") != "gzip" {
		t.Fatalf("应该 gzip，头是 %v", w.Header())
	}
	zr, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := io.ReadAll(zr); string(b) != big {
		t.Fatal("解压出来和原文不一样")
	}
	if w.Body.Len() > len(big)/3 {
		t.Fatalf("压完 %d 字节，没起作用", w.Body.Len())
	}

	for _, c := range []struct {
		p   string
		hdr map[string]string
	}{
		{"/assets/index.js", nil},
		{"/assets/index.js", map[string]string{"Accept-Encoding": "gzip", "Range": "bytes=0-9"}},
		{"/assets/small.css", map[string]string{"Accept-Encoding": "gzip"}},
	} {
		w := get(c.p, c.hdr)
		if w.Header().Get("content-encoding") != "" {
			t.Fatalf("%s %v 不该压", c.p, c.hdr)
		}
		if w.Body.Len() == 0 {
			t.Fatalf("%s %v 回了空的", c.p, c.hdr)
		}
	}
}
