// Package remote 是 `herdr-web connect`：在另一台电脑的**原生终端**里用 herdr-web，
// 认证走的是网页那一套（配对码 → 设备令牌，设备列表里看得见、一点撤销就失效）。
//
// # 为什么不是「开个 SSH 就完了」
//
// SSH 是另一套认证：密钥管理在 herdr-web 之外，设备页上看不见、撤销不了，sshd 还得
// 挂在公网上。这条路复用的是网页那条现成的 `/pty` WebSocket，走同一个公网口 + TLS，
// 服务端只多认一个 `Authorization: Bearer`（理由见 auth.Authenticate 里那段）。
//
// # 几条会静默出错的
//
//   - **令牌不能走明文**：`http://` 只放行本机 / 私网地址（LAN 直连那种），公网一律要
//     https —— 这份令牌就是一个登录 shell。
//   - **断开时要自己把本地终端的模式复位**：herdr 在对面开了鼠标上报、kitty 键盘协议、
//     备用屏，这些开关落在的是**本地**这个终端上。连接一断对面没人来关，于是本地 shell
//     里一动鼠标就灌 `35;120;36M` 这种字（网页那边同一个坑，见 CLAUDE.md「重连必须先把
//     终端复位」）。见 resetModes。
//   - **命令行里调不了 WebAuthn**，所以 passkey 那一步借一个浏览器做（device flow：终端
//     显示一个码，人在浏览器里手输它、刷一次 passkey）。第一次登录可以走它也可以用配对码；
//     **到期重验只能走它** —— 允许用配对码重验的话，命令行设备每天输个码就能一直续，
//     「长期靠 passkey」就被架空了（第一版就是这么做的）。
package remote

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/zbysir/herdr-web/internal/config"
)

// Target 是要连的那个 herdr-web：origin（`https://host[:port]`）+ 可选的 herdr session 名。
type Target struct {
	Origin  string
	Session string
}

// Parse 认 `https://host[:port][/session]`，没写 scheme 就当 https。
func Parse(raw string) (Target, error) {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return Target{}, fmt.Errorf("认不出这个地址：%q", raw)
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !plainOK(u.Hostname()) {
			return Target{}, fmt.Errorf("公网地址必须用 https：设备令牌就是一个登录 shell，不能明文走")
		}
	default:
		return Target{}, fmt.Errorf("只认 http / https，不认 %q", u.Scheme)
	}
	t := Target{Origin: u.Scheme + "://" + u.Host}
	if p := strings.Trim(u.Path, "/"); p != "" {
		if !config.ValidSessionName(p) {
			return Target{}, fmt.Errorf("路径 %q 不是合法的 session 名（只能是一段字母数字和 ._-）", p)
		}
		t.Session = p
	}
	return t, nil
}

// plainOK：明文只给本机和私网（局域网直连那个口本来就是 TLS，这里放宽是给开发实例用的）。
func plainOK(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate())
}

/* ------------------------------------------------------------------ 凭据 */

// Cred 是一个 origin 上配出来的那台「设备」。
type Cred struct {
	Token    string `json:"token"`
	DeviceID string `json:"deviceId,omitempty"`
	Label    string `json:"label,omitempty"`
}

// credPath：放在 HERDR_WEB_DIR 下（默认 ~/.herdr-web），和服务端那份设备表不是一个文件 ——
// 同一台机器上既跑服务又当客户端时两边互不相干。
func credPath() (string, error) {
	cfg, err := config.Load()
	if err != nil {
		return "", err
	}
	return filepath.Join(cfg.Dir, "remotes.json"), nil
}

func loadCreds() (map[string]Cred, error) {
	p, err := credPath()
	if err != nil {
		return nil, err
	}
	m := map[string]Cred{}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s 坏了（删掉它重新配对就行）：%w", p, err)
	}
	return m, nil
}

// saveCred 写一个 origin 的凭据（c 为零值 = 删掉）。0600 + 同目录临时文件 rename。
func saveCred(origin string, c Cred) error {
	m, err := loadCreds()
	if err != nil {
		return err
	}
	if c.Token == "" {
		delete(m, origin)
	} else {
		m[origin] = c
	}
	p, _ := credPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
