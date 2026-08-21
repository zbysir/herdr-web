package server

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/zbysir/herdr-web/internal/agentwatch"
	"github.com/zbysir/herdr-web/internal/config"
	"github.com/zbysir/herdr-web/internal/herdr"
	"github.com/zbysir/herdr-web/internal/outbox"
)

// 一个 URL 一个 herdr session：`https://host/` 是默认 session，`https://host/work`
// 是 `herdr --session work`（没有就新建）。前端把地址栏里那一段当 `?session=` 挂在
// 每个请求上，服务端这边按名字分派。
//
// **socket 是每个 session 一个**（`~/.config/herdr/sessions/<name>/herdr.sock`，
// 见 config.SessionSocket），所以发件箱和面板一览必须跟着 session 走 —— 拿默认
// session 的 socket 去投一个命名 session 的 pane，投出去的话会**静默进另一个 herdr**，
// 而屏幕上一切正常。这是这个功能里唯一真正危险的地方。

// maxSessions 是同时盯着的 session 数上限。每个都带一条状态订阅（一个 goroutine +
// 连不上时 5 秒一次的重试），所以不能让一串手打的 URL 无限往上加。
const maxSessions = 16

// live 是一个 session 在这一侧的全套东西：发件箱（连它自己那个 socket）+ 状态订阅。
type live struct {
	name   string
	socket string
	outbox *outbox.Outbox
	agents *agentwatch.Watcher // 可以为 nil：那时候「几分钟前」那一列空着
}

// watching 说状态订阅这会儿连着没有。前端拿它区分「这个 pane 还没变过状态」和
// 「压根没在盯」——不然空着的时间列看着像坏了。
func (l *live) watching() bool { return l.agents != nil && l.agents.Live() }

// sessionOf 取请求里的 session 名（空 = 默认 session）。
//
// **名字不合法一律报错，不退回默认 session。** 静默退回等于「你要 /work，我给你
// 默认那个」，接着投出去的话就进了另一个 herdr —— 宁可报错。
func sessionOf(r *http.Request) (string, error) {
	name := strings.TrimSpace(r.URL.Query().Get("session"))
	if name == "" || config.ValidSessionName(name) {
		return name, nil
	}
	return "", errf("session 名 " + shortQuote(name) + " 不合法：只能是字母数字和 ._-（首字符是字母数字），最长 " +
		itoa(config.MaxSessionName) + " 个字符")
}

// shortQuote 把用户给的名字放进错误消息里：截短，而且**不原样回显**控制字符。
// 这个字符串是从 URL 来的，错误消息又会被渲染进页面。
func shortQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i, r := range s {
		if i >= 24 {
			b.WriteString("…")
			break
		}
		if r < 0x20 || r == 0x7f || r == '"' || r == '<' || r == '&' {
			b.WriteByte('?')
			continue
		}
		b.WriteRune(r)
	}
	b.WriteByte('"')
	return b.String()
}

// live 给某个 session 的那一套，第一次见到的名字就地建起来（连带起一条状态订阅）。
func (s *Server) live(name string) (*live, error) {
	if name == "" {
		return s.def, nil
	}
	if !config.ValidSessionName(name) {
		return nil, errf("session 名不合法")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if l := s.sess[name]; l != nil {
		return l, nil
	}
	if len(s.sess) >= maxSessions {
		return nil, errf("同时盯着的 herdr session 太多了（上限 " + itoa(maxSessions) +
			"）。重启 herdr-web 会清掉这份名单")
	}
	sock := s.Cfg.SessionSocket(name)
	// 「几分钟前」那一列的时间**按 session 分开存**：terminal_id 只在它自己那个
	// herdr server 里唯一，混在一个文件里迟早张冠李戴。
	w := agentwatch.New(sock, filepath.Join(s.Cfg.Dir, "agent-seen-"+name+".json"))
	w.Start(s.ctx())
	l := &live{
		name: name, socket: sock, agents: w,
		outbox: &outbox.Outbox{C: herdr.New(sock), SettleMS: s.Cfg.SettleMS, Seen: w.At},
	}
	s.sess[name] = l
	return l, nil
}

func (s *Server) ctx() context.Context {
	if s.Ctx == nil {
		return context.Background()
	}
	return s.Ctx
}

// onConnectLine 是连上之后自动敲的那一行。
//
// URL 里点名了 session 就敲 `herdr --session <name>`，**不看 HERDR_WEB_ONCONNECT**
// （包括它被显式设成空的情况）：地址栏里写着要哪个 session，比一个全局默认具体得多，
// 而「打开 /work 什么都没发生」是最难查的那种表现。默认 session（`/`）还是老规矩。
func onConnectLine(onConnect, session string) string {
	if session == "" {
		return onConnect
	}
	return "herdr --session " + session
}
