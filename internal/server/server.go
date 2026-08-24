// Package server 是 HTTP + WebSocket 层。
//
// 认证：cookie 里的设备凭据（internal/auth），配对走一次性码。`?token=` 只剩兼容用途。
// 每个请求还要过 guard.go 那一层（Host 白名单、跨站检查、安全响应头）。
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zbysir/herdr-web/internal/agentwatch"
	"github.com/zbysir/herdr-web/internal/auth"
	"github.com/zbysir/herdr-web/internal/clip"
	"github.com/zbysir/herdr-web/internal/config"
	"github.com/zbysir/herdr-web/internal/files"
	"github.com/zbysir/herdr-web/internal/herdr"
	"github.com/zbysir/herdr-web/internal/outbox"
	"github.com/zbysir/herdr-web/internal/profiles"
	"github.com/zbysir/herdr-web/internal/selfupdate"
	"github.com/zbysir/herdr-web/internal/softkeys"
	"github.com/zbysir/herdr-web/internal/topbar"
	"github.com/zbysir/herdr-web/internal/uploads"
)

type Server struct {
	Cfg      *config.Config
	Auth     *auth.Store
	Gate     *auth.Gate
	Outbox   *outbox.Outbox
	Softkeys *softkeys.Store
	Topbar   *topbar.Store
	// Profiles 「这台设备用哪一套排布」的名册 + 绑定。软键条 / 顶栏的每个请求都要先过它
	// 算出 profile（见 profileOf）。
	Profiles *profiles.Store
	Uploads  *uploads.Store
	Web      fs.FS // 前端产物（嵌进二进制，或 -web 指向的目录）

	// Files 文件浏览（看 agent 生成的图）。Sign 出 /_f/ 那种短时签名链接 ——
	// <img src> 设不了 CSRF 头，所以图片这条路必须换一种凭据，见 internal/files/sign.go。
	Files *files.Browser
	Sign  *files.Signer

	Passkeys *auth.Passkeys
	// ReauthAfter：注册过 passkey 之后，一份会话在「上次生物验证」之后还能用多久。
	// 0 = 不要求重验。
	ReauthAfter time.Duration
	RPID        string

	TLS   bool            // 浏览器眼里是不是 https（决定 cookie 的 Secure）
	names map[string]bool // Host 头里允许出现的域名，见 guard.go

	// LanPort 局域网直连口的端口（0 = 这个部署没这条路）。网页拿它 + 当前局域网
	// 地址拼出候选去嗅探，见 lanapi.go。
	LanPort int

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
	LanPort      int
}

func New(cfg *config.Config, web fs.FS, a *auth.Store, g *auth.Gate, opt Options) *Server {
	c := herdr.New(cfg.Socket)
	ob := &outbox.Outbox{C: c, SettleMS: cfg.SettleMS}
	if opt.Agents != nil {
		ob.Seen = opt.Agents.At
	}
	s := &Server{
		Cfg:      cfg,
		Auth:     a,
		Gate:     g,
		Outbox:   ob,
		Softkeys: &softkeys.Store{Dir: cfg.Dir},
		Topbar:   &topbar.Store{Dir: cfg.Dir},
		Profiles: &profiles.Store{Dir: cfg.Dir},
		Uploads:  &uploads.Store{Dir: cfg.Dir},
		Files: &files.Browser{
			Enabled: cfg.Files,
			Roots:   cfg.FileRoots,
			Home:    userHome(),
			Tmp:     os.TempDir(),
			Uploads: filepath.Join(cfg.Dir, "uploads"),
		},
		Web:         web,
		Passkeys:    opt.Passkeys,
		ReauthAfter: opt.ReauthAfter,
		RPID:        opt.RPID,
		TLS:         opt.BrowserHTTPS,
		Version:     opt.Version,
		Updates:     opt.Updates,
		Agents:      opt.Agents,
		LanPort:     opt.LanPort,
		Ctx:         opt.Ctx,
		names:       map[string]bool{"localhost": true},
		sess:        map[string]*live{},
	}
	s.def = &live{socket: cfg.Socket, outbox: ob, agents: opt.Agents}
	// 顶栏可以放「我的按键」（items 里的 `key:k3`，见 internal/topbar）。存盘时要核一下
	// 那个定义还在不在，而 topbar 那个包**故意不 import softkeys**（两个文件两个口），
	// 所以在这儿把线接上。
	s.Topbar.Keys = s.Softkeys.LibIDs
	// 签名密钥在这儿生成：一个进程一把、只在内存里，重启就把所有旧链接作废。
	// 生成不出来（拿不到系统熵）就让 Sign 留 nil —— 那时候 /_f/ 直接 404，
	// 文件浏览退化成「列得出来但打不开」，而不是拿一把可预测的密钥继续跑。
	if sign, err := files.NewSigner(); err == nil {
		s.Sign = sign
	} else {
		log.Printf("文件浏览：%v，图片链接这条路关掉了", err)
	}

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
	// /_f/ 是签名链接那条路（不带 cookie，见 filesapi.go）。前缀带下划线是**故意**的：
	// 地址栏第一段是 herdr session 名，而 session 名的首字符只能是字母数字
	// （config.ValidSessionName），所以 `_f` 永远不可能和某个 session 撞上。
	mux.HandleFunc("/_f/", s.handleFileRaw)
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
	case "topbar":
		s.apiTopbar(w, r)
	case "profiles":
		s.apiProfiles(w, r, seg)
	case "clip":
		s.apiClip(w, r)
	case "herdr":
		s.apiHerdr(w, r, seg)
	case "files":
		s.apiFiles(w, r, seg)
	case "handoff":
		s.apiHandoff(w, r)
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
		// 提示的轮询间隔。0 = 这个部署把提示关了，前端那边就别轮询、也别画红点。
		"notice": map[string]int{"pollMs": s.Cfg.NoticeMS},
		// 文件浏览关掉时（HERDR_WEB_FILES=0）前端得知道，不然顶栏那个按钮点开就是一片 404
		"files":       s.Files != nil && s.Files.Enabled && s.Sign != nil,
		"session":     name, // 空 = 默认 session
		"herdrSocket": s.Cfg.SessionSocket(name),
		"version":     s.versionInfo(),
		// 局域网直连的候选（没开这条路时是 nil，前端据此什么都不做）
		"lan": s.lanInfo(),
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

// apiSoftkeys 是软键条：rows / lib / bar。
//
// **哪一套**由 profileOf 决定（`?profile=` 显式指定，否则按这台设备的绑定算）。
// 响应里回一句 profile，前端好在编辑器上写「正在改：平板」—— 存到别的地方去是这个功能
// 最容易出的错，而它完全静默。
func (s *Server) apiSoftkeys(w http.ResponseWriter, r *http.Request) {
	// rows / lib / bar 一起进出一个请求：行数变了往往就是为了把某几个键挪到第二行，
	// 分几次存的话中间那一下必然是个自相矛盾的状态（两行的引用 + 一行的设置）
	prof := s.profileOf(r)
	out := func(c softkeys.Config) {
		writeJSON(w, 200, map[string]any{"lib": c.Lib, "bar": c.Bar, "rows": c.Rows,
			"pin": c.Pin, "max": softkeys.MaxKeys, "maxBar": softkeys.MaxBar, "profile": prof})
	}
	// 删掉一个定义之后，顶栏上指向它的 `key:` 引用也得清掉 —— 顶栏和软键条现在共用同一份
	// 「我的按键」（见 internal/topbar 的包注释）。软键条自己那一侧（条上的引用）在
	// Save 里已经 prune 过了，这儿补的是另一个界面。
	//
	// 失败**不改这次请求的结果**：软键条已经存好了，回一个 400 只会让人再点一次保存
	// （而那一次一样会失败）。留下的顶栏幽灵项在编辑器里下一次保存时自然消失。
	prune := func(c softkeys.Config) {
		keep := make(map[string]bool, len(c.Lib))
		for _, k := range c.Lib {
			keep[k.ID] = true
		}
		if err := s.Topbar.PruneKeys(keep); err != nil {
			log.Printf("顶栏清引用：%v", err)
		}
	}
	switch r.Method {
	case http.MethodGet:
		// GET 不挑食：`?profile=` 指到一套已经被别的设备删掉的排布时，退回这台设备该用
		// 的那一套并在响应里说清是哪一套（前端照着改标题）。写才严格，见下面。
		c := s.Softkeys.Load(prof)
		writeJSON(w, 200, map[string]any{
			"lib": c.Lib, "bar": c.Bar, "rows": c.Rows, "pin": c.Pin, "profile": prof,
			"max": softkeys.MaxKeys, "maxBar": softkeys.MaxBar, "presets": softkeys.Presets(),
		})
	case http.MethodPut:
		if err := s.mustProfile(prof); err != nil {
			fail(w, 404, err)
			return
		}
		var body softkeys.Config
		if err := readJSON(r, &body); err != nil {
			fail(w, 400, err)
			return
		}
		c, err := s.Softkeys.Save(prof, body)
		if err != nil {
			fail(w, 400, err)
			return
		}
		prune(c)
		out(c)
	case http.MethodDelete:
		if err := s.mustProfile(prof); err != nil {
			fail(w, 404, err)
			return
		}
		// 「恢复默认」只管**这一套**的排布：出厂那一排回到条上，「我的按键」里缺的补上。
		// 不整份恢复出厂 —— 定义是全局的，那样会把别的 profile 条上引用的定义一起抹掉
		// （在手机上点一下，平板上的软键条少一半）。见 softkeys.Store.Reset。
		c, err := s.Softkeys.Reset(prof)
		if err != nil {
			fail(w, 400, err)
			return
		}
		prune(c)
		out(c)
	default:
		fail(w, 405, errf("方法不对"))
	}
}

// mustProfile 写操作前确认这一套还在：另一台设备可能刚把它删了，而这边的编辑器还开着。
// 静默存到别的地方去是这里最坏的结果 —— 排布看着「保存成功」，回头一看没变。
func (s *Server) mustProfile(id string) error {
	if s.Profiles.Load().Has(id) {
		return nil
	}
	return fmt.Errorf("没有这一套排布（%s）—— 可能在别的设备上删掉了，重开一下设置", id)
}

// apiTopbar 是顶栏那排图标按钮「放哪几个、什么顺序」。
//
// 和软键条**分成两个口**：各自一个 PUT 收自己那一整份。混在一个口里的话两个编辑器都在
// PUT「一整份配置」，谁后存谁把对方那一半清掉（见 internal/topbar 的包注释）。
//
// GET 顺带把白名单和上限也给出去：编辑器要拿它和自己那份按钮目录对一遍，服务端不认的
// 就别画出来让人拖 —— 拖得上去、一存报错是最难受的那种交互。
func (s *Server) apiTopbar(w http.ResponseWriter, r *http.Request) {
	prof := s.profileOf(r)
	out := func(c topbar.Config) {
		writeJSON(w, 200, map[string]any{
			"items": c.Items, "actions": topbar.Actions, "pinned": topbar.Pinned,
			"max": topbar.MaxItems, "profile": prof,
		})
	}
	switch r.Method {
	case http.MethodGet:
		out(s.Topbar.Load(prof))
	case http.MethodPut:
		if err := s.mustProfile(prof); err != nil {
			fail(w, 404, err)
			return
		}
		var body topbar.Config
		if err := readJSON(r, &body); err != nil {
			fail(w, 400, err)
			return
		}
		c, err := s.Topbar.Save(prof, body)
		if err != nil {
			fail(w, 400, err)
			return
		}
		out(c)
	case http.MethodDelete:
		if err := s.mustProfile(prof); err != nil {
			fail(w, 404, err)
			return
		}
		c, err := s.Topbar.Save(prof, topbar.DefaultConfig())
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

	// 提示：agent 变成「等你回答」/「跑完了」时攒下的那些（右上角那个弹窗 + 面板图标上的
	// 红点）。`since` 是上一拍拿到的 seq，做增量 —— 不带就把环里还留着的都给你。
	//
	// 这一拍**不打 herdr socket**：读的是 agentwatch 内存里那个环（状态是那条长连订阅
	// 推来的）。所以几秒问一次没什么代价，见 README「是轮询，不是推送」。
	case seg[1] == "notices" && r.Method == http.MethodGet:
		since, _ := strconv.ParseUint(q.Get("since"), 10, 64) // 解析不了就当 0（全给）
		list, seq := sess.notices(since)
		writeJSON(w, 200, map[string]any{
			"notices": list, "seq": seq, "watching": sess.watching(),
		})

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
