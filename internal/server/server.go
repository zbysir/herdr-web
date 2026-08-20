// Package server 是 HTTP + WebSocket 层。
//
// 接口都在 /api 下，统一用 ?token= 认证（和 JS 版一致，前端不用改协议）。
package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"strings"

	"git.huglight.cn/bysir/herdr-web/internal/config"
	"git.huglight.cn/bysir/herdr-web/internal/herdr"
	"git.huglight.cn/bysir/herdr-web/internal/outbox"
	"git.huglight.cn/bysir/herdr-web/internal/softkeys"
	"git.huglight.cn/bysir/herdr-web/internal/uploads"
)

type Server struct {
	Cfg      *config.Config
	Outbox   *outbox.Outbox
	Softkeys *softkeys.Store
	Uploads  *uploads.Store
	Web      fs.FS // 前端产物（嵌进二进制，或 -web 指向的目录）
}

func New(cfg *config.Config, web fs.FS) *Server {
	c := herdr.New(cfg.Socket)
	return &Server{
		Cfg:      cfg,
		Outbox:   &outbox.Outbox{C: c, SettleMS: cfg.SettleMS},
		Softkeys: &softkeys.Store{Dir: cfg.Dir},
		Uploads:  &uploads.Store{Dir: cfg.Dir},
		Web:      web,
	}
}

func errf(msg string) error { return errors.New(msg) }

func (s *Server) tokenOK(given string) bool {
	return subtle.ConstantTimeCompare([]byte(given), []byte(s.Cfg.Token)) == 1
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/pty", s.handlePTY)
	mux.HandleFunc("/api/", s.handleAPI)
	mux.HandleFunc("/", s.handleStatic)
	return mux
}

/* ------------------------------------------------------------------ helpers */

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.Header().Set("cache-control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func readJSON(r *http.Request, out any) error {
	b, err := io.ReadAll(io.LimitReader(r.Body, 256<<10))
	if err != nil {
		return err
	}
	if len(b) == 0 {
		return nil
	}
	if err := json.Unmarshal(b, out); err != nil {
		return errf("请求体不是合法 JSON")
	}
	return nil
}

/* ------------------------------------------------------------------ API */

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request) {
	if !s.tokenOK(r.URL.Query().Get("token")) {
		fail(w, http.StatusUnauthorized, errf("token 不对"))
		return
	}
	seg := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api"), "/"), "/")
	if len(seg) == 0 || seg[0] == "" {
		fail(w, http.StatusNotFound, errf("没有这个接口"))
		return
	}

	switch seg[0] {
	case "state":
		s.apiState(w, r)
	case "softkeys":
		s.apiSoftkeys(w, r)
	case "herdr":
		s.apiHerdr(w, r, seg)
	default:
		fail(w, http.StatusNotFound, errf("没有这个接口"))
	}
}

func (s *Server) apiState(w http.ResponseWriter, r *http.Request) {
	host, _ := os.Hostname()
	user := os.Getenv("USER")
	writeJSON(w, 200, map[string]any{
		"shell":         baseName(s.Cfg.Shell),
		"user":          user,
		"hostname":      host,
		"secureContext": s.Cfg.Loopback,
		"compose":       map[string]int{"pollMs": s.Cfg.PollMS, "pushMs": s.Cfg.PushMS, "settleMs": s.Cfg.SettleMS},
		"herdrSocket":   s.Cfg.Socket,
	})
}

func baseName(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

func (s *Server) apiSoftkeys(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, 200, map[string]any{
			"keys": s.Softkeys.Load(), "max": softkeys.MaxKeys, "presets": softkeys.Presets(),
		})
	case http.MethodPut:
		var body struct{ Keys []softkeys.Key }
		if err := readJSON(r, &body); err != nil {
			fail(w, 400, err)
			return
		}
		out, err := s.Softkeys.Save(body.Keys)
		if err != nil {
			fail(w, 400, err)
			return
		}
		writeJSON(w, 200, map[string]any{"keys": out})
	case http.MethodDelete:
		out, err := s.Softkeys.Save(softkeys.Defaults())
		if err != nil {
			fail(w, 400, err)
			return
		}
		writeJSON(w, 200, map[string]any{"keys": out})
	default:
		fail(w, 405, errf("方法不对"))
	}
}

// apiHerdr 是语音投稿的发件箱。
//
// target 省略或传 "__focused" = 投给此刻在 herdr 里激活的那个 pane。
// socket 在**跑 herdr server 的那台机器**上；现在只连本机（或 HERDR_WEB_SOCKET
// 指到的路径）。
func (s *Server) apiHerdr(w http.ResponseWriter, r *http.Request, seg []string) {
	if len(seg) < 2 {
		fail(w, 404, errf("没有这个接口"))
		return
	}
	q := r.URL.Query()
	switch {
	case seg[1] == "panes" && r.Method == http.MethodGet:
		list, err := s.Outbox.ListTargets()
		if err != nil {
			fail(w, 400, err)
			return
		}
		writeJSON(w, 200, map[string]any{"panes": list, "socket": s.Cfg.Socket})

	case seg[1] == "pull" && r.Method == http.MethodGet:
		out, err := s.Outbox.Pull(q.Get("target"), q.Get("mode"))
		respond(w, out, err)

	// 自动拉回的轮询口：一次给「焦点在哪」+「那个输入框里是什么」
	case seg[1] == "sync" && r.Method == http.MethodGet:
		out, err := s.Outbox.Pull(q.Get("target"), "")
		respond(w, out, err)

	case seg[1] == "say" && r.Method == http.MethodPost:
		var b struct{ Target, Text string }
		if err := readJSON(r, &b); err != nil {
			fail(w, 400, err)
			return
		}
		out, err := s.Outbox.Say(b.Target, b.Text)
		respond(w, out, err)

	// 双向同步的本地→远端那半边：写进远端输入框但不回车
	case seg[1] == "draft" && r.Method == http.MethodPost:
		var b struct{ Target, Text string }
		if err := readJSON(r, &b); err != nil {
			fail(w, 400, err)
			return
		}
		out, err := s.Outbox.Draft(b.Target, b.Text)
		respond(w, out, err)

	// 图片落盘，返回绝对路径 —— 前端把路径插进提示词，agent 自己去读文件
	case seg[1] == "upload" && r.Method == http.MethodPost:
		buf, err := io.ReadAll(io.LimitReader(r.Body, uploads.MaxBytes+1))
		if err != nil {
			fail(w, 400, err)
			return
		}
		if len(buf) > uploads.MaxBytes {
			fail(w, 400, errf("上传超过上限 25 MB"))
			return
		}
		out, err := s.Uploads.Save(buf)
		respond(w, out, err)

	default:
		fail(w, 404, errf("没有这个接口"))
	}
}

func respond(w http.ResponseWriter, out any, err error) {
	if err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, out)
}
