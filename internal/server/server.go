// Package server 是 HTTP + WebSocket 层。
//
// 认证：cookie 里的设备凭据（internal/auth），配对走一次性码。`?token=` 只剩兼容用途。
// 每个请求还要过 guard.go 那一层（Host 白名单、跨站检查、安全响应头）。
package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/zbysir/herdr-web/internal/agentwatch"
	"github.com/zbysir/herdr-web/internal/auth"
	"github.com/zbysir/herdr-web/internal/clip"
	"github.com/zbysir/herdr-web/internal/config"
	"github.com/zbysir/herdr-web/internal/herdr"
	"github.com/zbysir/herdr-web/internal/outbox"
	"github.com/zbysir/herdr-web/internal/selfupdate"
	"github.com/zbysir/herdr-web/internal/softkeys"
	"github.com/zbysir/herdr-web/internal/uploads"
)

type Server struct {
	Cfg      *config.Config
	Auth     *auth.Store
	Gate     *auth.Gate
	Outbox   *outbox.Outbox
	Softkeys *softkeys.Store
	Uploads  *uploads.Store
	Web      fs.FS // 前端产物（嵌进二进制，或 -web 指向的目录）

	Passkeys *auth.Passkeys
	// ReauthAfter：注册过 passkey 之后，一份会话在「上次生物验证」之后还能用多久。
	// 0 = 不要求重验。
	ReauthAfter time.Duration
	RPID        string

	TLS   bool            // 浏览器眼里是不是 https（决定 cookie 的 Secure）
	names map[string]bool // Host 头里允许出现的域名，见 guard.go

	// Agents 盯着 agent 状态变化打时间戳（面板一览的「几分钟前」）。可以为 nil：
	// 那时候时间列空着，排序退回按 state_change_seq。
	Agents *agentwatch.Watcher

	// Ctx 决定后台那些 goroutine（按 session 起的状态订阅）活多久。nil = Background。
	Ctx context.Context

	// 按 session 分派的那一套：一个 URL 一个 herdr session，每个 session 有自己的
	// socket，所以发件箱和状态订阅都得分开（见 session.go 的包内注释）。
	// def 是默认 session（`/`）那一份，直接用上面的 Outbox / Agents。
	mu   sync.Mutex
	sess map[string]*live
	def  *live

	// Version / Updates 给设置面板显示「当前版本 / 有没有新的」。
	// 为什么也给网页端而不只给管理页：网页里就有一个终端，看到提示的人正好能就地
	// 敲 herdr-web update —— 而管理页只有坐在机器前的人能开。
	Version string
	Updates *selfupdate.Checker
}

// Options 是 main 那边算出来的东西：证书里有哪些域名、浏览器看到的是不是 https。
type Options struct {
	Ctx          context.Context
	BrowserHTTPS bool
	Hostnames    []string
	Passkeys     *auth.Passkeys
	ReauthAfter  time.Duration
	RPID         string
	Version      string
	Updates      *selfupdate.Checker
	Agents       *agentwatch.Watcher
}

func New(cfg *config.Config, web fs.FS, a *auth.Store, g *auth.Gate, opt Options) *Server {
	c := herdr.New(cfg.Socket)
	ob := &outbox.Outbox{C: c, SettleMS: cfg.SettleMS}
	if opt.Agents != nil {
		ob.Seen = opt.Agents.At
	}
	s := &Server{
		Cfg:         cfg,
		Auth:        a,
		Gate:        g,
		Outbox:      ob,
		Softkeys:    &softkeys.Store{Dir: cfg.Dir},
		Uploads:     &uploads.Store{Dir: cfg.Dir},
		Web:         web,
		Passkeys:    opt.Passkeys,
		ReauthAfter: opt.ReauthAfter,
		RPID:        opt.RPID,
		TLS:         opt.BrowserHTTPS,
		Version:     opt.Version,
		Updates:     opt.Updates,
		Agents:      opt.Agents,
		Ctx:         opt.Ctx,
		names:       map[string]bool{"localhost": true},
		sess:        map[string]*live{},
	}
	s.def = &live{socket: cfg.Socket, outbox: ob, agents: opt.Agents}
	for _, n := range opt.Hostnames {
		if n = strings.ToLower(strings.TrimSpace(n)); n != "" {
			s.names[n] = true
		}
	}
	return s
}

func errf(msg string) error { return errors.New(msg) }

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/pty", s.handlePTY)
	mux.HandleFunc("/api/", s.handleAPI)
	mux.HandleFunc("/", s.handleRoot)
	return s.guard(mux)
}

// gateCheck 拦在每一次「猜得到的凭据」前面（配对码、旧 token）。
// 返回 false 表示已经把响应写出去了，调用方直接 return。
func (s *Server) gateCheck(w http.ResponseWriter, r *http.Request) bool {
	if s.Gate == nil {
		return true
	}
	if s.Gate.Locked() {
		fail(w, http.StatusTooManyRequests,
			errf("失败次数太多，已暂停接受新设备配对。到跑 herdr-web 的机器上执行 `herdr-web unlock`"))
		return false
	}
	delay, blocked, retry := s.Gate.Check(s.Auth.ClientIP(r))
	if blocked {
		w.Header().Set("retry-after", itoa(int(retry.Seconds())+1))
		fail(w, http.StatusTooManyRequests, errf("试得太多了，等 "+retry.Truncate(time.Second).String()+" 再来"))
		return false
	}
	if delay > 0 {
		time.Sleep(delay) // 拖慢在线猜解；不占别的请求
	}
	return true
}

func itoa(n int) string {
	if n <= 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
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
	seg := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api"), "/"), "/")
	if len(seg) == 0 || seg[0] == "" {
		fail(w, http.StatusNotFound, errf("没有这个接口"))
		return
	}
	// /api/auth/* 是唯一一组不要求已认证的口（配对页自己得能用）
	if seg[0] == "auth" {
		s.apiAuth(w, r, seg)
		return
	}
	if s.requireAuth(w, r) == nil {
		return
	}

	switch seg[0] {
	case "state":
		s.apiState(w, r)
	case "softkeys":
		s.apiSoftkeys(w, r)
	case "clip":
		s.apiClip(w, r)
	case "herdr":
		s.apiHerdr(w, r, seg)
	default:
		fail(w, http.StatusNotFound, errf("没有这个接口"))
	}
}

func (s *Server) apiState(w http.ResponseWriter, r *http.Request) {
	host, _ := os.Hostname()
	user := os.Getenv("USER")
	// session 名不合法就直接说，别在这儿静默退回默认 session —— 前端拿这个响应
	// 确认「我这个页面对着的是哪个 herdr」，给错的话后面每一条投稿都投错地方。
	name, err := sessionOf(r)
	if err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{
		"shell":         baseName(s.Cfg.Shell),
		"user":          user,
		"hostname":      host,
		"secureContext": s.TLS || s.Cfg.Loopback,
		"compose":       map[string]int{"pollMs": s.Cfg.PollMS, "pushMs": s.Cfg.PushMS, "settleMs": s.Cfg.SettleMS},
		"session":       name, // 空 = 默认 session
		"herdrSocket":   s.Cfg.SessionSocket(name),
		"version":       s.versionInfo(),
	})
}

// versionInfo 只读查更新的缓存，不在这个请求里发出站请求 —— 面板是随手点开的，
// 不该因为 GitHub 慢而转圈。
func (s *Server) versionInfo() map[string]any {
	out := map[string]any{"current": s.Version}
	if s.Updates == nil {
		return out
	}
	st := s.Updates.State()
	if !selfupdate.Newer(strings.TrimPrefix(s.Version, "v"), st.Latest) {
		return out
	}
	out["latest"] = st.Latest
	out["outdated"] = true
	out["how"] = "herdr-web update"
	if inst, err := selfupdate.Detect(); err == nil {
		if c := inst.Command(); c != "" {
			out["how"] = c
		}
	}
	return out
}

func baseName(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// apiClip 把**这台机器**的剪贴板交给浏览器。
//
// 为什么需要它：herdr 的复制（选中即复制 / COPY 模式）落在跑 herdr 那台机器的剪贴板上，
// 浏览器一无所知 —— 实测手机上拖选一段，herdr 报「copied 84 chars」，那 84 个字进的是
// Mac 的剪贴板，手机上哪儿都粘不出来。所以手机上要拿到它，只能由这一侧读出来。
//
// 只有读，没有写：写这一侧的剪贴板暂时没有用处（手机剪贴板里的东西要进终端，浏览器直接
// 发给 PTY 就行，不用绕这台机器一圈）。
func (s *Server) apiClip(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		fail(w, 405, errf("方法不对"))
		return
	}
	text, err := clip.Read()
	if err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"text": text, "bytes": len(text)})
}

func (s *Server) apiSoftkeys(w http.ResponseWriter, r *http.Request) {
	// rows / lib / bar 一起进出一个请求：行数变了往往就是为了把某几个键挪到第二行，
	// 分几次存的话中间那一下必然是个自相矛盾的状态（两行的引用 + 一行的设置）
	out := func(c softkeys.Config) {
		writeJSON(w, 200, map[string]any{"lib": c.Lib, "bar": c.Bar, "rows": c.Rows,
			"max": softkeys.MaxKeys, "maxBar": softkeys.MaxBar})
	}
	switch r.Method {
	case http.MethodGet:
		c := s.Softkeys.Load()
		writeJSON(w, 200, map[string]any{
			"lib": c.Lib, "bar": c.Bar, "rows": c.Rows,
			"max": softkeys.MaxKeys, "maxBar": softkeys.MaxBar, "presets": softkeys.Presets(),
		})
	case http.MethodPut:
		var body softkeys.Config
		if err := readJSON(r, &body); err != nil {
			fail(w, 400, err)
			return
		}
		c, err := s.Softkeys.Save(body)
		if err != nil {
			fail(w, 400, err)
			return
		}
		out(c)
	case http.MethodDelete:
		c, err := s.Softkeys.Save(softkeys.DefaultConfig())
		if err != nil {
			fail(w, 400, err)
			return
		}
		out(c)
	default:
		fail(w, 405, errf("方法不对"))
	}
}

// apiHerdr 是语音投稿的发件箱。
//
// target 省略或传 "__focused" = 投给此刻在 herdr 里激活的那个 pane。
// socket 在**跑 herdr server 的那台机器**上；现在只连本机（或 HERDR_WEB_SOCKET
// 指到的路径）。
//
// `?session=` 决定连哪个 socket：命名 session 有自己的一个（见 session.go）。
// 解析不了就报错**不退回默认 session** —— 投错 herdr 是完全静默的。
func (s *Server) apiHerdr(w http.ResponseWriter, r *http.Request, seg []string) {
	if len(seg) < 2 {
		fail(w, 404, errf("没有这个接口"))
		return
	}
	q := r.URL.Query()
	name, err := sessionOf(r)
	if err != nil {
		fail(w, 400, err)
		return
	}
	sess, err := s.live(name)
	if err != nil {
		fail(w, 400, err)
		return
	}
	switch {
	case seg[1] == "panes" && r.Method == http.MethodGet:
		list, err := sess.outbox.ListTargets()
		if err != nil {
			fail(w, 400, err)
			return
		}
		// watching 说「状态变化的订阅这会儿连着没有」——前端拿它区分「这个 pane 还没
		// 变过状态」和「压根没在盯」，不然空着的时间列看着像坏了。
		writeJSON(w, 200, map[string]any{
			"panes": list, "socket": sess.socket, "session": sess.name,
			"watching": sess.watching(),
		})

	// 跳到某个 pane：切焦点 + 全屏，一次调用（herdr 的 pane.zoom 按 pane_id 寻址，
	// 跨 workspace / tab 也一起切过去）。手机上「面板一览」点一行走的就是这个口 ——
	// 按键那条通道只能表达相对导航，说不出「让 w5:p3 全屏」。
	case seg[1] == "goto" && r.Method == http.MethodPost:
		var b struct {
			Target string
			Zoom   *bool // 省略 = 要全屏
		}
		if err := readJSON(r, &b); err != nil {
			fail(w, 400, err)
			return
		}
		out, err := sess.outbox.Goto(b.Target, b.Zoom == nil || *b.Zoom)
		respond(w, out, err)

	case seg[1] == "pull" && r.Method == http.MethodGet:
		out, err := sess.outbox.Pull(q.Get("target"), q.Get("mode"))
		respond(w, out, err)

	// 自动拉回的轮询口：一次给「焦点在哪」+「那个输入框里是什么」
	case seg[1] == "sync" && r.Method == http.MethodGet:
		out, err := sess.outbox.Pull(q.Get("target"), "")
		respond(w, out, err)

	case seg[1] == "say" && r.Method == http.MethodPost:
		var b struct{ Target, Text string }
		if err := readJSON(r, &b); err != nil {
			fail(w, 400, err)
			return
		}
		out, err := sess.outbox.Say(b.Target, b.Text)
		respond(w, out, err)

	// 双向同步的本地→远端那半边：写进远端输入框但不回车
	case seg[1] == "draft" && r.Method == http.MethodPost:
		var b struct{ Target, Text string }
		if err := readJSON(r, &b); err != nil {
			fail(w, 400, err)
			return
		}
		out, err := sess.outbox.Draft(b.Target, b.Text)
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
