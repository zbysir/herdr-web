package config

import (
	"os"
	"testing"
)

// HERDR_WEB_ONCONNECT 的三档：没设 = herdr，设了空串 = 关掉，设了别的 = 敲那个。
// 中间那档是重点：env() 会把空串当没设，所以这一项必须走 envSet。
func TestOnConnect(t *testing.T) {
	t.Setenv("HERDR_WEB_DIR", t.TempDir())

	t.Setenv("HERDR_WEB_ONCONNECT", "占位，好让 t.Setenv 记住原值")
	if err := os.Unsetenv("HERDR_WEB_ONCONNECT"); err != nil {
		t.Fatal(err)
	}
	c, err := Load()
	if err != nil || c.OnConnect != "herdr" {
		t.Errorf("没设的时候应当是 herdr，得到 %q (%v)", c.OnConnect, err)
	}

	t.Setenv("HERDR_WEB_ONCONNECT", "")
	if c, _ := Load(); c.OnConnect != "" {
		t.Errorf("显式空串应当关掉，得到 %q", c.OnConnect)
	}

	t.Setenv("HERDR_WEB_ONCONNECT", "herdr --session work")
	if c, _ := Load(); c.OnConnect != "herdr --session work" {
		t.Errorf("设了什么就敲什么，得到 %q", c.OnConnect)
	}
}

// viper 那一层的行为：前缀映射、下限、写错了退回默认、布尔的写法、BindEnv 兜底。
// 这些都是从「手写 os.Getenv」搬过来时最容易悄悄变掉的地方。
func TestEnvMapping(t *testing.T) {
	t.Setenv("HERDR_WEB_DIR", t.TempDir())

	t.Setenv("HERDR_WEB_POLL_MS", "900")
	t.Setenv("HERDR_WEB_INSECURE", "true") // 以前只认 "1"，现在 1/true/yes 都行
	t.Setenv("HERDR_WEB_HOSTNAME", "A.example.com, b.example.com")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.PollMS != 900 {
		t.Errorf("PollMS = %d, want 900", c.PollMS)
	}
	if !c.Insecure {
		t.Error("HERDR_WEB_INSECURE=true 应当算开")
	}
	if len(c.Hostnames) != 2 || c.Hostnames[0] != "a.example.com" {
		t.Errorf("域名白名单没按逗号拆 / 没转小写: %v", c.Hostnames)
	}

	// 下限：低于地板的值夹到地板，别让人把节流关到 0
	t.Setenv("HERDR_WEB_POLL_MS", "10")
	if c, _ := Load(); c.PollMS != 200 {
		t.Errorf("PollMS = %d, want 200（下限）", c.PollMS)
	}

	// 写错了退回默认值，而不是变成 0
	t.Setenv("HERDR_WEB_POLL_MS", "5OO") // 字母 O
	if c, _ := Load(); c.PollMS != 500 {
		t.Errorf("PollMS = %d, want 500（解析不了就当没设）", c.PollMS)
	}
}

// shell / socket 的兜底走的是**没有 HERDR_WEB_ 前缀**的那两个变量
func TestBindEnvFallback(t *testing.T) {
	t.Setenv("HERDR_WEB_DIR", t.TempDir())
	t.Setenv("SHELL", "/bin/fish")
	t.Setenv("HERDR_SOCKET_PATH", "/tmp/herdr-test.sock")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Shell != "/bin/fish" {
		t.Errorf("Shell = %q, want /bin/fish（$SHELL 兜底）", c.Shell)
	}
	if c.Socket != "/tmp/herdr-test.sock" {
		t.Errorf("Socket = %q, want /tmp/herdr-test.sock（$HERDR_SOCKET_PATH 兜底）", c.Socket)
	}

	// HERDR_WEB_ 那个优先
	t.Setenv("HERDR_WEB_SHELL", "/bin/dash")
	t.Setenv("HERDR_WEB_SOCKET", "/tmp/win.sock")
	if c, _ := Load(); c.Shell != "/bin/dash" || c.Socket != "/tmp/win.sock" {
		t.Errorf("HERDR_WEB_* 应当盖过兜底: shell=%q socket=%q", c.Shell, c.Socket)
	}
	if got := DefaultSocket(); got != "/tmp/win.sock" {
		t.Errorf("DefaultSocket() = %q", got)
	}
}

// passkey 的 RPID 只能是域名。这几条搞错的话表现是「注册时浏览器直接拒绝」，
// 而且报的错很难看懂，所以钉住。
func TestPasskeyRPID(t *testing.T) {
	cases := []struct {
		name string
		c    Config
		want string
	}{
		{"显式指定优先", Config{RPID: "a.example.com", Hostnames: []string{"b.example.com"}}, "a.example.com"},
		{"没指定就取 HOSTNAME 第一个", Config{Hostnames: []string{"b.example.com", "c.example.com"}}, "b.example.com"},
		{"纯本机用 localhost（规范里的特例）", Config{Loopback: true}, "localhost"},
		{"监听局域网又没有域名 → 用不了", Config{}, ""},
	}
	for _, c := range cases {
		if got := c.c.PasskeyRPID(); got != c.want {
			t.Errorf("%s：得到 %q，想要 %q", c.name, got, c.want)
		}
	}
}

func TestPasskeyOrigins(t *testing.T) {
	c := Config{Hostnames: []string{"h.example.com"}, Port: 7788, PublicURL: "https://h.example.com:12345"}
	got := c.PasskeyOrigins()
	// PublicURL 必须在里面：浏览器发的 Origin 带端口，对不上就整个流程失败
	found := false
	for _, o := range got {
		if o == "https://h.example.com:12345" {
			found = true
		}
	}
	if !found {
		t.Errorf("PublicURL 必须算进 origins，得到 %v", got)
	}
	if len((&Config{}).PasskeyOrigins()) != 0 {
		t.Error("用不了 passkey 时 origins 该是空的")
	}
}
