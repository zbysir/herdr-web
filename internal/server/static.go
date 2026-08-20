package server

import (
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
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

	// 浏览器会主动探 /favicon.ico；页面里已经用 data: URI 声明过图标了，
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
