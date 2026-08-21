package config

import (
	"path/filepath"
	"regexp"
	"strings"
)

// herdr 的**命名 session**（`herdr --session <name>`）在这一侧的两件事：名字怎么算合法、
// socket 在哪。网页那边 `https://host/{name}` 就是「开（或接上）这个 session」。
//
// 为什么名字要卡这么死：它有两个去处，两个都危险 ——
//
//  1. 被拼进一条**敲进登录 shell 的命令行**（`herdr --session <name>`，见 server/pty.go）。
//     一个 `;` 或者反引号就是任意命令执行，而这个字符串来自 URL。
//  2. 被拼进 socket 路径。`/` 和 `..` 能把它指到任何地方去。
//
// 所以是白名单，不是黑名单：只放字母数字和 `._-`，首字符必须是字母数字。
// 前端有一份一样的（web/src/lib/api.ts 的 SESSION），改一边记得改另一边 ——
// 那边只是为了不把明显不合法的东西发过来，**真正的判据是这里**。
const MaxSessionName = 40

var sessionName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ValidSessionName：空字符串（= 默认 session）不算合法名字，调用方自己先判空。
func ValidSessionName(s string) bool {
	if len(s) == 0 || len(s) > MaxSessionName {
		return false
	}
	// `a..b` 当目录名是无害的，但它是「路径里有 ..」的唯一形状，一律不收
	return sessionName.MatchString(s) && !strings.Contains(s, "..")
}

// SessionSocket 给某个命名 session 的 socket 路径（name 为空就是默认 session 那个）。
//
// herdr 的布局（`herdr session list --json` 的 socket_path 就是这么排的，实测）：
//
//	默认         ~/.config/herdr/herdr.sock
//	命名 <name>  ~/.config/herdr/sessions/<name>/herdr.sock
//
// 所以从默认 socket 的目录往下拼，文件名跟着 HERDR_WEB_SOCKET 走（别写死 herdr.sock）。
// **HERDR_WEB_SOCKET 本身已经指到某个命名 session 时先退回上层**：否则会拼出
// `sessions/a/sessions/b/` 这种根本不存在的路径，表现是「连不上 herdr socket」而
// 完全看不出是配置和 URL 打架了。
func (c *Config) SessionSocket(name string) string {
	if name == "" {
		return c.Socket
	}
	dir, file := filepath.Dir(c.Socket), filepath.Base(c.Socket)
	if filepath.Base(filepath.Dir(dir)) == "sessions" {
		dir = filepath.Dir(filepath.Dir(dir))
	}
	return filepath.Join(dir, "sessions", name, file)
}
