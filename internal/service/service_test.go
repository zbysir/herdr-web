package service

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// plist / unit 里的转义。这几条错了的表现都是「配置静默丢了一半」，装完看不出来，
// 要等到某个功能莫名其妙不工作才发现。
func TestUnitEscaping(t *testing.T) {
	m := &Manager{
		Kind: Systemd,
		Exec: "/opt/herdr-web",
		Dir:  "/home/u/.herdr-web",
		Env: map[string]string{
			// 带空格：不加引号会被 systemd 切成两个变量
			"HERDR_WEB_ONCONNECT": "herdr --session work",
			// 带 $：systemd 会做 $ 展开，不转义就静默丢字符
			"HERDR_WEB_ACME_TOKEN": "a$b$$c",
			// 带引号和反斜杠
			"HERDR_WEB_WEIRD": `a"b\c`,
		},
		homeDir: "/home/u",
	}
	u := m.unit()
	if !strings.Contains(u, `Environment="HERDR_WEB_ONCONNECT=herdr --session work"`) {
		t.Errorf("带空格的值没被引号包住:\n%s", u)
	}
	if !strings.Contains(u, `HERDR_WEB_ACME_TOKEN=a$$b$$$$c`) {
		t.Errorf("$ 没转成 $$:\n%s", u)
	}
	if !strings.Contains(u, `HERDR_WEB_WEIRD=a\"b\\c`) {
		t.Errorf("引号 / 反斜杠没转义:\n%s", u)
	}
	// 顺序必须稳定，否则每次 install 生成的文件都不一样，没法 diff
	if m.unit() != u {
		t.Error("同样输入生成的 unit 不一样（环境变量没排序？）")
	}

	p := (&Manager{Kind: Launchd, Exec: "/opt/x", Dir: "/d", homeDir: "/h",
		Env: map[string]string{"K": `a<b>&c"d`}}).plist()
	if !strings.Contains(p, `<string>a&lt;b&gt;&amp;c&quot;d</string>`) {
		t.Errorf("plist 里的 XML 特殊字符没转义:\n%s", p)
	}
	if !strings.Contains(p, "<key>ProcessType</key>") {
		t.Error("plist 少了 ProcessType（不写会被 macOS 按后台任务限速）")
	}
}

// PATH 必须被抄进去。这是装成服务之后最常见的故障：launchd 给的 PATH 里没有 herdr，
// HERDR_WEB_ONCONNECT=herdr 就变成 command not found，而页面上只看到一个空 shell。
func TestEnvSnapshotIncludesPath(t *testing.T) {
	t.Setenv("PATH", "/my/bin:/usr/bin")
	t.Setenv("HERDR_WEB_PORT", "9999")
	t.Setenv("HERDR_SOCKET_PATH", "/tmp/h.sock")
	t.Setenv("SOME_UNRELATED_VAR", "x")

	env := EnvSnapshot(nil)
	if env["PATH"] != "/my/bin:/usr/bin" {
		t.Errorf("PATH 没抄进去: %q", env["PATH"])
	}
	if env["HERDR_WEB_PORT"] != "9999" {
		t.Errorf("HERDR_WEB_* 没抄进去: %q", env["HERDR_WEB_PORT"])
	}
	if env["HERDR_SOCKET_PATH"] != "/tmp/h.sock" {
		t.Errorf("HERDR_SOCKET_PATH 没抄进去: %q", env["HERDR_SOCKET_PATH"])
	}
	if _, ok := env["SOME_UNRELATED_VAR"]; ok {
		t.Error("不该把无关的环境变量都抄进去")
	}
	if env["TERM"] == "" {
		t.Error("TERM 该有个兜底值")
	}

	// extra（--env-file）优先级更高
	env = EnvSnapshot(map[string]string{"HERDR_WEB_PORT": "1234"})
	if env["HERDR_WEB_PORT"] != "1234" {
		t.Errorf("--env-file 应当盖过当前环境: %q", env["HERDR_WEB_PORT"])
	}
}

func TestParseEnvFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".env")
	// 最后一条是重点：值里带 # 不能被当成注释切掉（token 里出现 # 很正常）
	body := `# 注释
HERDR_WEB_HOST=0.0.0.0
export HERDR_WEB_PORT=7788

HERDR_WEB_ONCONNECT="herdr --session work"
HERDR_WEB_EMPTY=
HERDR_WEB_QUOTED='single'
HERDR_WEB_TOKEN=ab#cd
`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ParseEnvFile(p)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"HERDR_WEB_HOST":      "0.0.0.0",
		"HERDR_WEB_PORT":      "7788",
		"HERDR_WEB_ONCONNECT": "herdr --session work",
		"HERDR_WEB_EMPTY":     "",
		"HERDR_WEB_QUOTED":    "single",
		"HERDR_WEB_TOKEN":     "ab#cd",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("多解析出东西了: %v", got)
	}

	// 空串**要算数**（config 那边 HERDR_WEB_ONCONNECT= 就是「什么都不敲」），
	// 所以 EMPTY 必须存在于 map 里，不能因为值是空就丢掉
	if _, ok := got["HERDR_WEB_EMPTY"]; !ok {
		t.Error("显式的空值必须保留 —— 有默认值的开关靠它关掉")
	}
}

// unit / plist 里存着 install 那一刻抄进来的全部环境变量，走 ACME 那条路上就包括
// DNS provider 的 token。0644 等于把它摊给这台机器上的每个用户看。
func TestUnitFileIsPrivate(t *testing.T) {
	p := filepath.Join(t.TempDir(), "herdr-web.service")
	// 先按老权限造一个，模拟从旧版本升上来 —— WriteFile 的 mode 只在新建时生效，
	// 少了那次 Chmod 的话这里就还是 644
	if err := os.WriteFile(p, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeUnit(p, "new"); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if mode := fi.Mode().Perm(); mode != 0o600 {
		t.Errorf("unit 权限是 %04o，want 0600 —— 里面有 DNS token", mode)
	}
}

// Windows 上给一条能用的路（去 WSL），不是一句「不支持」。
func TestUnsupportedGivesReason(t *testing.T) {
	m := &Manager{Kind: None, Why: "x"}
	if m.Supported() {
		t.Error("Kind=None 不该算支持")
	}
	if err := m.Install(func(string) {}); err == nil {
		t.Error("不支持的平台上 Install 要报错")
	}
}

// 生成的 plist 必须能被 macOS 自己解析。手写 XML 最容易出的错就是「看着对、
// launchd 不认」，而那时候的表现是 bootstrap 报一句没头没尾的 Load failed。
// 用系统的 plutil 来判，比我们自己判可靠。
func TestPlistIsValid(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("plutil 只有 macOS 上有")
	}
	if _, err := exec.LookPath("plutil"); err != nil {
		t.Skip("没有 plutil")
	}
	m := &Manager{
		Kind: Launchd, Exec: "/opt/herdr-web", Dir: "/Users/x/.herdr-web", homeDir: "/Users/x",
		Env: map[string]string{
			"PATH":                "/usr/local/bin:/usr/bin",
			"HERDR_WEB_ONCONNECT": "herdr --session work",
			"HERDR_WEB_WEIRD":     `a<b>&c"d'e`,
		},
	}
	p := filepath.Join(t.TempDir(), "test.plist")
	if err := os.WriteFile(p, []byte(m.plist()), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("plutil", "-lint", p).CombinedOutput(); err != nil {
		t.Fatalf("plutil -lint 不认这份 plist: %v\n%s\n---\n%s", err, out, m.plist())
	}
	// 值要能原样读回来（转义没吃掉字符）
	out, err := exec.Command("plutil", "-extract", "EnvironmentVariables.HERDR_WEB_WEIRD", "raw", "-o", "-", p).Output()
	if err != nil {
		t.Fatalf("读不回那个变量: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != `a<b>&c"d'e` {
		t.Errorf("值被转义搞坏了: %q", got)
	}
}
