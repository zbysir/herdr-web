package service

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// 除了 HERDR_WEB_*，这几个也得抄进 unit —— 少了它们的表现都很难查：
//
//   - PATH：**最容易踩的一个**。launchd 给的默认 PATH 只有 /usr/bin:/bin:/usr/sbin:/sbin，
//     于是 HERDR_WEB_ONCONNECT=herdr 变成「herdr: command not found」，而页面上只看到
//     一个空 shell，看不出为什么。抄当前 shell 的 PATH 能解决 99% 的情况。
//   - SHELL：config 里 shell 的兜底就是它。
//   - HOME / USER / LOGNAME：systemd user service 有，launchd 也有，但抄一份不亏。
//   - LANG / LC_ALL：不设的话 locale 是 C，中文和框线字符在终端里会花。
//   - HERDR_SOCKET_PATH：herdr 自己的 socket 位置，不带 HERDR_WEB_ 前缀但会被读到。
//   - TERM：给 PTY 用。
var copyThrough = []string{
	"PATH", "SHELL", "HOME", "USER", "LOGNAME", "LANG", "LC_ALL", "TERM",
	"HERDR_SOCKET_PATH",
}

// EnvSnapshot 收集要写进 unit / plist 的环境变量：当前进程里所有 HERDR_WEB_*
// 加上上面那张白名单，再叠上 extra（一般是 --env-file 读出来的，优先级更高）。
func EnvSnapshot(extra map[string]string) map[string]string {
	out := map[string]string{}
	for _, kv := range os.Environ() {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if strings.HasPrefix(k, "HERDR_WEB_") {
			out[k] = v
		}
	}
	for _, k := range copyThrough {
		if v, ok := os.LookupEnv(k); ok {
			out[k] = v
		}
	}
	for k, v := range extra {
		out[k] = v
	}
	// TERM 在 launchd 下没有意义（没有终端），但 PTY 那边会给子进程重新设，
	// 这里给个能用的兜底，免得 locale 一样是空的。
	if out["TERM"] == "" {
		out["TERM"] = "xterm-256color"
	}
	return out
}

// ParseEnvFile 读 .env（就是项目里 .env.example 那种写法）。
//
// 支持的就这些：`KEY=value`、`export KEY=value`、`#` 开头的整行注释、单/双引号包住的值。
// **不做**变量展开、不做行内 `#` 截断 —— 值里带 `#` 是正常的（比如 token），
// 按 shell 规则截断会静默切掉半个密码。Makefile 那边用 shell 的 `set -a` 来读同一个
// 文件，规则不可能完全一致，所以这里宁可少支持几种写法，也不要「看着解析对了其实不对」。
func ParseEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	line := 0
	for sc.Scan() {
		line++
		s := strings.TrimSpace(sc.Text())
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		s = strings.TrimPrefix(s, "export ")
		k, v, ok := strings.Cut(s, "=")
		if !ok {
			return nil, fmt.Errorf("%s 第 %d 行不是 KEY=VALUE: %s", path, line, s)
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		}
		out[k] = v
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
