package server

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zbysir/herdr-web/internal/config"
	"github.com/zbysir/herdr-web/internal/outbox"
)

// 地址栏里点名了 session 就敲 `herdr --session <name>`，**不看 HERDR_WEB_ONCONNECT**
// —— 包括它被显式设成空的情况（那时候「打开 /work 什么都没发生」最难查）。
func TestOnConnectLine(t *testing.T) {
	cases := []struct{ onconnect, session, want string }{
		{"herdr", "", "herdr"}, // 默认 session：老规矩
		{"", "", ""},           // ONCONNECT= 就是什么都不敲
		{"herdr", "work", "herdr --session work"},        //
		{"", "work", "herdr --session work"},             // URL 比全局默认具体
		{"tmux attach", "work", "herdr --session work"},  //
		{"herdr --remote box", "x", "herdr --session x"}, // 自定义 ONCONNECT 不参与，README 里写了
	}
	for _, c := range cases {
		if got := onConnectLine(c.onconnect, c.session); got != c.want {
			t.Errorf("onConnectLine(%q, %q) = %q，want %q", c.onconnect, c.session, got, c.want)
		}
	}
}

func TestSessionOf(t *testing.T) {
	get := func(q string) (string, error) {
		return sessionOf(httptest.NewRequest("GET", "/api/herdr/panes"+q, nil))
	}
	if name, err := get(""); name != "" || err != nil {
		t.Errorf("没带参数该是默认 session，得到 %q / %v", name, err)
	}
	if name, err := get("?session=work"); name != "work" || err != nil {
		t.Errorf("得到 %q / %v", name, err)
	}
	// 不合法**不能静默退回默认 session**：那样投稿会进另一个 herdr，而屏幕上一切正常。
	// 分号写成 %3B —— 裸分号 Go 的 Query() 直接把整个参数丢掉（那条路等于没带参数）。
	for _, q := range []string{"?session=a%3Bid", "?session=a%20b", "?session=../x", "?session=a/b", "?session=%60id%60"} {
		if name, err := get(q); err == nil {
			t.Errorf("%s 该报错，却给了 %q", q, name)
		}
	}
}

// 错误消息会被渲染进页面，而这个名字是从 URL 来的
func TestShortQuoteSanitizes(t *testing.T) {
	got := shortQuote("<img>\x07\"" + strings.Repeat("z", 40))
	for _, bad := range []string{"<", "&", "\x07", `z"`} {
		if strings.Contains(got, bad) {
			t.Errorf("%q 里还留着 %q", got, bad)
		}
	}
	if !strings.HasPrefix(got, `"`) || !strings.Contains(got, "…") {
		t.Errorf("形状不对：%q", got)
	}
}

// 配对链接是 https://host/work?pair=CODE，洗 URL 时**路径要留着** ——
// 洗成 / 的话人配完对就落在默认 session 上了。
// 但也不能原样回填：`//evil.com` 会变成协议相对的 Location（开放重定向）。
func TestSessionPath(t *testing.T) {
	cases := map[string]string{
		"/":            "/",
		"/work":        "/work",
		"/work/":       "/work",
		"//evil.com":   "/",
		"/a/b":         "/",
		"/../x":        "/",
		"/a;id":        "/",
		"/%2Fevil.com": "/", // 没解码过的路径也一样进不了白名单
	}
	for in, want := range cases {
		if got := sessionPath(in); got != want {
			t.Errorf("sessionPath(%q) = %q，want %q", in, got, want)
		}
	}
}

// 每个 session 一份（自己的 socket、自己的状态订阅），第二次问同一个名字要拿回同一份
// —— 不然每次请求都新起一条订阅。
func TestLiveCachesAndCaps(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{Dir: dir, Socket: dir + "/herdr.sock"}
	// 给一个已经取消的 ctx：每份 live 会起一条状态订阅，测试里不需要它真去连
	// （连不上就是 5 秒一次重试 + 一行日志 × 16）。
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := &Server{Cfg: cfg, Ctx: ctx, sess: map[string]*live{}}
	s.def = &live{outbox: &outbox.Outbox{}, socket: cfg.Socket}

	if l, err := s.live(""); err != nil || l != s.def {
		t.Fatalf("空名字该给默认那份：%v / %v", l, err)
	}
	a, err := s.live("work")
	if err != nil {
		t.Fatalf("live(work): %v", err)
	}
	if want := cfg.SessionSocket("work"); a.socket != want {
		t.Errorf("socket 是 %q，want %q", a.socket, want)
	}
	if b, _ := s.live("work"); b != a {
		t.Error("同一个 session 名给了两份")
	}
	if _, err := s.live("a;id"); err == nil {
		t.Error("不合法的名字该报错")
	}

	// 上限：每份都带一条状态订阅（goroutine + 重试），不能让一串手打的 URL 无限往上加
	for i := 0; len(s.sess) < maxSessions; i++ {
		if _, err := s.live("s" + itoa(i)); err != nil {
			t.Fatalf("填到上限之前就报错了: %v", err)
		}
	}
	if _, err := s.live("one-too-many"); err == nil {
		t.Error("超过上限该报错")
	}
	if l, err := s.live("work"); err != nil || l != a {
		t.Error("到上限之后已经有的那些还得能用")
	}
}
