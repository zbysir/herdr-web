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

// 局域网直连口。三档的判据不一样，而搞错的表现都是「静默不生效」：
// 明文口嗅探不到（mixed content），真证书对私网 IP 无效（SAN 不匹配）。
func TestLanDirectPort(t *testing.T) {
	cases := []struct {
		name string
		c    Config
		want int
	}{
		{"显式配的优先", Config{LanPort: 7789, Port: 7788, TLSMode: "auto"}, 7789},
		{"主口自签 + 听着局域网 → 就是主口", Config{Port: 7788, TLSMode: "auto"}, 7788},
		{"自签但只听本机 → 没这条路", Config{Port: 7788, TLSMode: "auto", Loopback: true}, 0},
		{"前置终止 TLS：主口是明文，嗅探不到", Config{Port: 7788, TLSMode: "proxy"}, 0},
		{"真证书只对域名有效，照 IP 连是 SAN 不匹配", Config{Port: 7788, TLSMode: "files"}, 0},
		{"明文部署", Config{Port: 7788, TLSMode: "off"}, 0},
	}
	for _, c := range cases {
		if got := c.c.LanDirectPort(); got != c.want {
			t.Errorf("%s：得到 %d，想要 %d", c.name, got, c.want)
		}
	}
}

// 撞口只会表现成「第二个监听起不来」的一行日志，夹在启动横幅里看不见 —— 所以直接拒绝启动。
func TestLanPortConflict(t *testing.T) {
	t.Setenv("HERDR_WEB_DIR", t.TempDir())
	t.Setenv("HERDR_WEB_PORT", "7788")

	t.Setenv("HERDR_WEB_LAN_PORT", "7788")
	if _, err := Load(); err == nil {
		t.Error("和主口撞了应当报错")
	}
	t.Setenv("HERDR_WEB_LAN_PORT", "7789") // 管理口
	if _, err := Load(); err == nil {
		t.Error("和管理口撞了应当报错")
	}
	t.Setenv("HERDR_WEB_LAN_PORT", "7790")
	c, err := Load()
	if err != nil || c.LanPort != 7790 {
		t.Errorf("LanPort = %d (%v)", c.LanPort, err)
	}
	if !c.LanNeedsListener() {
		t.Error("显式配了就该另起一个监听")
	}
}
