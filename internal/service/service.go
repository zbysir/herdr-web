// Package service 把 herdr-web 装成开机自启的常驻服务：macOS 用 launchd 的
// LaunchAgent，Linux 用 systemd 的 **user** unit。
//
// 为什么是 user 级而不是系统级：这个进程会开一个**你的** shell。跑成 root 的系统服务
// 意味着浏览器里那个终端是 root 的，权限一步到位放到最大，而且 ~/.herdr-web、
// ~/.config/herdr/herdr.sock 这些路径全都指到别人家去了。user 级服务跑在你自己名下，
// 环境和你手起的时候一致。
//
// 配置照旧只从环境变量来（见 internal/config）—— 这里做的事就是把「现在这个 shell 里
// 生效的那套 HERDR_WEB_*」抄进 plist / unit。抄而不是引用文件是刻意的：
// 装完之后 `launchctl print` / `systemctl --user cat` 就能看到全部生效配置，
// 不用再去猜某个 .env 当时是什么内容。
package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// Label 是 launchd 的 Label，也是 plist 的文件名。反向域名是 launchd 的惯例。
const Label = "io.github.zbysir.herdr-web"

// Unit 是 systemd user unit 的名字。
const Unit = "herdr-web.service"

// Kind 是这台机器上用哪套。
type Kind string

const (
	Launchd Kind = "launchd"
	Systemd Kind = "systemd"
	None    Kind = ""
)

// Manager 是某台机器上的服务管理器。用 New 拿。
type Manager struct {
	Kind Kind
	// Why 在 Kind == None 时说明为什么不支持，直接给用户看。
	Why string

	Exec    string // 二进制绝对路径
	Dir     string // 数据目录（cfg.Dir），日志放它下面
	Env     map[string]string
	homeDir string
	uid     string
}

// New 探测这台机器该用哪套。exec 是要常驻的二进制路径，dir 是 cfg.Dir。
func New(exe, dir string, env map[string]string) *Manager {
	m := &Manager{Exec: exe, Dir: dir, Env: env, uid: strconv.Itoa(os.Getuid())}
	m.homeDir, _ = os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		m.Kind = Launchd
	case "linux":
		if _, err := exec.LookPath("systemctl"); err != nil {
			m.Why = "这台 Linux 上没有 systemctl。手动常驻的办法见 README 的「守护进程」一节" +
				"（supervisor / 直接 nohup 都行，关键是把 HERDR_WEB_* 传进去）。"
			return m
		}
		// 有 systemctl 不等于 user 实例能用：容器、WSL1、systemd 没跑成 PID 1 的
		// 环境里 `systemctl --user` 会直接失败。这里先问一句，别等装完了才发现。
		if err := exec.Command("systemctl", "--user", "is-system-running").Run(); err != nil {
			if out, err2 := exec.Command("systemctl", "--user", "show", "-p", "Version").Output(); err2 != nil || len(out) == 0 {
				m.Why = "systemctl --user 用不了（容器 / WSL1 / systemd 不是 PID 1 都会这样）。\n" +
					"  WSL2 可以在 /etc/wsl.conf 里加 [boot] systemd=true 然后 wsl --shutdown 重开。"
				return m
			}
		}
		m.Kind = Systemd
	case "windows":
		m.Why = "Windows 上跑不了：终端要 PTY，herdr 走的是 unix socket。\n" +
			"  在 WSL 里装（那边就是 Linux 版，systemd user unit 一样能用）。"
	default:
		m.Why = runtime.GOOS + " 没适配。手动常驻的办法见 README。"
	}
	return m
}

// Supported 表示这台机器能不能装。
func (m *Manager) Supported() bool { return m.Kind != None }

// UnitPath 是要写的那个文件。
func (m *Manager) UnitPath() string {
	switch m.Kind {
	case Launchd:
		return filepath.Join(m.homeDir, "Library", "LaunchAgents", Label+".plist")
	case Systemd:
		return filepath.Join(m.homeDir, ".config", "systemd", "user", Unit)
	}
	return ""
}

// LogPath 是 stdout / stderr 落到哪。两个平台故意用同一个路径，这样文档和
// `herdr-web service logs` 只有一套说法。
func (m *Manager) LogPath() string { return filepath.Join(m.Dir, "logs", "herdr-web.log") }

// Install 写好文件并把服务起起来。已经装过就是覆盖 + 重启（幂等）。
// 每一步做了什么都通过 say 报出来 —— 这条链路里有 launchctl / systemctl / loginctl
// 三个外部命令，静默成功和静默失败都不可接受。
func (m *Manager) Install(say func(string)) error {
	if !m.Supported() {
		return fmt.Errorf("%s", m.Why)
	}
	p := m.UnitPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	// 日志目录得先有：systemd 的 append: 和 launchd 的 StandardOutPath 都不会自己建
	if err := os.MkdirAll(filepath.Dir(m.LogPath()), 0o700); err != nil {
		return err
	}
	var body string
	switch m.Kind {
	case Launchd:
		body = m.plist()
	case Systemd:
		body = m.unit()
	}
	if err := writeUnit(p, body); err != nil {
		return err
	}
	say("写了 " + p)

	switch m.Kind {
	case Launchd:
		// 先 bootout 再 bootstrap：不这样的话改了 plist 也是老的那份在跑。
		// 没装过时 bootout 会报错，忽略。
		_ = run("launchctl", "bootout", "gui/"+m.uid+"/"+Label)
		if err := run("launchctl", "bootstrap", "gui/"+m.uid, p); err != nil {
			// 老系统（< macOS 11）只有 load。bootstrap 失败就退回去试它。
			if err2 := run("launchctl", "load", "-w", p); err2 != nil {
				return fmt.Errorf("launchctl 起不来: %v（load 也失败: %v）", err, err2)
			}
		}
		say("launchctl 已加载，下次登录自动起")
	case Systemd:
		if err := run("systemctl", "--user", "daemon-reload"); err != nil {
			return err
		}
		if err := run("systemctl", "--user", "enable", "--now", Unit); err != nil {
			return fmt.Errorf("systemctl enable --now 失败: %w", err)
		}
		say("systemd 已 enable + 起来了")
		// linger：不开的话 user 服务是「你登录时才起、退出登录就停」。对一台
		// 要随时能连进去的机器来说，这基本等于没常驻 —— 所以默认开，失败只提示。
		if err := run("loginctl", "enable-linger", os.Getenv("USER")); err != nil {
			say("⚠️  loginctl enable-linger 没成功（" + err.Error() + "）。\n" +
				"     不开 linger 的话，你 ssh 退出登录之后这个服务会被停掉。\n" +
				"     手动开：sudo loginctl enable-linger " + os.Getenv("USER"))
		} else {
			say("已开 linger（没人登录也常驻，开机就起）")
		}
	}
	return nil
}

// writeUnit 把 plist / unit 落盘。
//
// **0600**：这份文件里存着 install 那一刻抄进来的全部环境变量 —— 走 ACME 的话，
// DNS provider 的 token（`HERDR_WEB_CLOUDFLARE_DNS_API_TOKEN` 这种，跟着前缀一起抄进来）
// 就明文躺在里面。
// 这台机器上跑着读不可信内容的 agent，磁盘上别的地方都没留能直接用的明文，
// 这儿也不该是个例外。
//
// 补一次 Chmod 是因为 WriteFile 的 mode **只在新建时生效**：覆盖装的时候管不到
// 已经存在的那个文件，而上一版写的是 0644 —— 升上来的机器只能靠这一下收窄。
func writeUnit(p, body string) error {
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		return err
	}
	return os.Chmod(p, 0o600)
}

// Uninstall 停掉并删掉文件。没装过也算成功。
func (m *Manager) Uninstall(say func(string)) error {
	if !m.Supported() {
		return fmt.Errorf("%s", m.Why)
	}
	switch m.Kind {
	case Launchd:
		if err := run("launchctl", "bootout", "gui/"+m.uid+"/"+Label); err != nil {
			_ = run("launchctl", "unload", "-w", m.UnitPath())
		}
	case Systemd:
		_ = run("systemctl", "--user", "disable", "--now", Unit)
	}
	if err := os.Remove(m.UnitPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	say("已停掉并删掉 " + m.UnitPath())
	if m.Kind == Systemd {
		_ = run("systemctl", "--user", "daemon-reload")
	}
	// 日志和数据目录不动：卸掉服务不代表要扔掉设备凭据
	return nil
}

// Restart 重起。换了二进制（herdr-web update）之后必须做这一步才生效。
func (m *Manager) Restart() error {
	if !m.Supported() {
		return fmt.Errorf("%s", m.Why)
	}
	switch m.Kind {
	case Launchd:
		// kickstart -k：杀掉再起。比 bootout+bootstrap 稳，不用重读 plist
		return run("launchctl", "kickstart", "-k", "gui/"+m.uid+"/"+Label)
	case Systemd:
		return run("systemctl", "--user", "restart", Unit)
	}
	return nil
}

// Status 是给人看的一段状态。装没装、跑没跑，以及 unit 文件在哪。
type Status struct {
	Installed bool
	Running   bool
	PID       int
	Detail    string // 平台原生命令的输出，看不懂就看这个
}

func (m *Manager) Status() (Status, error) {
	var st Status
	if !m.Supported() {
		return st, fmt.Errorf("%s", m.Why)
	}
	if _, err := os.Stat(m.UnitPath()); err == nil {
		st.Installed = true
	}
	switch m.Kind {
	case Launchd:
		out, err := exec.Command("launchctl", "list", Label).CombinedOutput()
		st.Detail = strings.TrimSpace(string(out))
		if err != nil {
			return st, nil // 没加载，不是错误
		}
		st.Running = true
		// `launchctl list <label>` 吐的是 plist 文本，里面有 "PID" = 1234;
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(line, `"PID"`) {
				if i := strings.LastIndex(line, "= "); i > 0 {
					st.PID, _ = strconv.Atoi(strings.Trim(strings.TrimSpace(line[i+2:]), ";"))
				}
			}
		}
		// 没有 PID 说明加载了但没在跑（比如起来就崩，launchd 正在退避）
		st.Running = st.PID > 0
	case Systemd:
		out, _ := exec.Command("systemctl", "--user", "show", Unit,
			"-p", "ActiveState", "-p", "MainPID", "-p", "SubState", "-p", "UnitFileState").Output()
		st.Detail = strings.TrimSpace(string(out))
		for _, line := range strings.Split(string(out), "\n") {
			k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
			if !ok {
				continue
			}
			switch k {
			case "ActiveState":
				st.Running = v == "active"
			case "MainPID":
				st.PID, _ = strconv.Atoi(v)
			}
		}
	}
	return st, nil
}

// LogsCmd 返回一条「跟日志」的命令，让调用方直接 exec 它。
// 不在这儿自己实现 tail -f：外部命令的 Ctrl-C、行缓冲、颜色都已经对了。
func (m *Manager) LogsCmd() []string {
	return []string{"tail", "-n", "80", "-f", m.LogPath()}
}

func run(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		if s := strings.TrimSpace(string(out)); s != "" {
			return fmt.Errorf("%s: %s", err, s)
		}
		return err
	}
	return nil
}

// keys 把环境变量排序输出，这样重复生成的 unit 文件内容是稳定的（能 diff）。
func (m *Manager) keys() []string {
	ks := make([]string, 0, len(m.Env))
	for k := range m.Env {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
