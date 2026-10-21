package server

import (
	"bytes"
	"compress/gzip"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
)

var mimeByExt = map[string]string{
	".html":  "text/html; charset=utf-8",
	".js":    "text/javascript; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".json":  "application/json; charset=utf-8",
	".svg":   "image/svg+xml",
	".woff2": "font/woff2",
	".map":   "application/json",
	".ico":   "image/x-icon",
	".png":   "image/png",
	// 少了这一条安卓不认 manifest（Chrome 只吃 application/manifest+json）
	".webmanifest": "application/manifest+json",
}

// handleStatic 伺候前端产物。SPA：认不出的路径一律回 index.html。
func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if s.Web == nil {
		http.Error(w, "没有前端产物：要么用 -web 指一个目录，要么 make build 把 dist 嵌进来", http.StatusNotFound)
		return
	}
	p := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if p == "" {
		p = "index.html"
	}

	// 浏览器会主动探 /favicon.ico；页面里已经用 <link rel=icon> 指到 /logo.svg 了，
	// 这里回 204 只是别让控制台留一条 404。
	if p == "favicon.ico" {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	f, err := s.Web.Open(p)
	if err != nil {
		// 带扩展名的当成真的缺文件（别把缺失的 .js 也回成 HTML，那样报错很难查）
		if path.Ext(p) != "" {
			http.NotFound(w, r)
			return
		}
		if f, err = s.Web.Open("index.html"); err != nil {
			http.NotFound(w, r)
			return
		}
		p = "index.html"
	}
	defer f.Close()

	if st, err := f.Stat(); err == nil && st.IsDir() {
		f.Close()
		if f, err = s.Web.Open("index.html"); err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		p = "index.html"
	}

	if ct := mimeByExt[path.Ext(p)]; ct != "" {
		w.Header().Set("content-type", ct)
	}
	// index.html 不缓存（要能立刻拿到新的资源哈希）；带哈希的静态资源可以长缓存
	if p == "index.html" {
		w.Header().Set("cache-control", "no-cache")
	} else if strings.HasPrefix(p, "assets/") {
		w.Header().Set("cache-control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("cache-control", "no-cache")
	}

	if strings.HasPrefix(p, "assets/") && serveGzip(w, r, s, p, f) {
		return
	}

	if rs, ok := f.(io.ReadSeeker); ok {
		if st, err := f.Stat(); err == nil {
			http.ServeContent(w, r, p, st.ModTime(), rs)
			return
		}
	}
	_, _ = io.Copy(w, f)
}

// SubFS 从 embed 的根里取出子目录，找不到就返回 nil（当成"没有前端产物"）。
func SubFS(root fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(root, dir)
	if err != nil {
		return nil
	}
	if _, err := sub.Open("index.html"); err != nil {
		return nil
	}
	return sub
}

// gzipped 缓存压过的 assets：key 是 (哪个 Server, 路径)。不拿 FS 本身当 key —— 有的 FS
// 是 map（fstest.MapFS），拿来当 key 直接 panic。assets 的文件名带内容哈希、内容不可变，
// 所以压一次永远有效，不用管失效。
var gzipped sync.Map

type gzKey struct {
	srv *Server
	p   string
}

// gzipExt 压了划算的那几种（图片 / 字体本身就是压过的，再压一遍白费 CPU）。
var gzipExt = map[string]bool{".js": true, ".css": true, ".svg": true, ".json": true, ".map": true}

// serveGzip 对能压的 assets 回 gzip，回了返回 true。
//
// 为什么要：主包将近 1MB，而这个服务常常挂在一条很窄的隧道后面（实测 frp 那条 60KB/s），
// 不压的话**冷启动要等二三十秒才出第一帧**（用户在原生 App 里报的「输完地址就黑屏」——
// WebView 第一次打开没有缓存；浏览器里每次部署换了哈希也一样要整份重下）。gzip 之后
// JS 大约剩三分之一。
//
// 带 Range 的请求不走这条（压过的字节对不上原文件的偏移），照旧 ServeContent。
func serveGzip(w http.ResponseWriter, r *http.Request, srv *Server, p string, f fs.File) bool {
	if !gzipExt[path.Ext(p)] || r.Header.Get("Range") != "" ||
		!strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		return false
	}
	k := gzKey{srv, p}
	v, ok := gzipped.Load(k)
	if !ok {
		// 先看大小再读：读掉了再退回去的话，上层那份文件已经在末尾了
		if st, err := f.Stat(); err != nil || st.Size() < 1024 {
			return false // 太小的不值得压
		}
		raw, err := io.ReadAll(f)
		if err != nil {
			return false
		}
		var buf bytes.Buffer
		zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		_, _ = zw.Write(raw)
		_ = zw.Close()
		v, _ = gzipped.LoadOrStore(k, buf.Bytes())
	}
	b := v.([]byte)
	w.Header().Set("content-encoding", "gzip")
	w.Header().Add("vary", "Accept-Encoding")
	w.Header().Set("content-length", strconv.Itoa(len(b)))
	if r.Method != http.MethodHead {
		_, _ = w.Write(b)
	}
	return true
}
