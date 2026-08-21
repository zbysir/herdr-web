// Package clip 读**跑 herdr-web 那台机器**的剪贴板。
//
// 为什么需要这么一个口：herdr 的复制落在**它自己那台机器**的剪贴板上，浏览器一无所知。
// 实测（手机上长按拖选一段，herdr 报「copied 84 chars to clipboard」）：那 84 个字进的是
// Mac 的剪贴板 —— `pbpaste` 读出来一字不差，而手机上哪儿都粘不出来。所以「手机上复制不
// 出来」不是浏览器权限那一档的问题，是**文本压根没到浏览器**。
//
// 于是手机上要拿到它只有这一条路：这一侧把它读出来交给浏览器，浏览器在一次点击里写进
// 手机剪贴板（写剪贴板必须有用户手势，见 web/src/lib/clipboard.ts）。
//
// 安全上不新增暴露面：能调这个口的会话本来就有这台机器上的一个登录 shell，自己敲
// `pbpaste` 就读到了。真正不能做的是**别把它挂在不需要认证的路径上**。
package clip

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"
)

// MaxBytes 是一次读的上限。剪贴板里可能是一整个文件的内容，而这东西要经过 JSON、
// 再进手机剪贴板 —— 超过就截断（截断比「整个请求失败」有用）。
const MaxBytes = 1 << 20

// 命令有卡住的（比如 X11 转发断了的 xclip），别把 HTTP 请求一起拖死。
const timeout = 3 * time.Second

// Read 读机器上的剪贴板。返回的字符串**不做任何加工** —— 结尾的换行也是内容
// （复制一整行时它就在那儿）。
func Read() (string, error) {
	argv, err := readCmd(runtime.GOOS, exec.LookPath, os.Getenv)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := errb.String()
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s 读剪贴板失败: %s", argv[0], msg)
	}
	if out.Len() > MaxBytes {
		return out.String()[:MaxBytes], nil
	}
	return out.String(), nil
}

// readCmd 挑一个能用的命令。参数化 lookPath / getenv 是为了能测（真跑一遍要有 GUI 会话）。
//
// Linux 上先看 Wayland 再看 X11：`wl-paste` 在 X11 会话里存在也用不了，而
// `WAYLAND_DISPLAY` 是「现在这个会话是 Wayland」的直接证据。`--no-newline` 必须给 ——
// 不给它会在内容末尾**补一个换行**，粘进终端就等于替你按了一次回车。
func readCmd(goos string, lookPath func(string) (string, error), getenv func(string) string) ([]string, error) {
	has := func(bin string) bool { _, err := lookPath(bin); return err == nil }

	switch goos {
	case "darwin":
		if has("pbpaste") {
			return []string{"pbpaste"}, nil
		}
		return nil, errors.New("找不到 pbpaste（macOS 自带，PATH 被改坏了？）")
	case "linux":
		if getenv("WAYLAND_DISPLAY") != "" && has("wl-paste") {
			return []string{"wl-paste", "--no-newline"}, nil
		}
		if has("xclip") {
			return []string{"xclip", "-selection", "clipboard", "-o"}, nil
		}
		if has("xsel") {
			return []string{"xsel", "--clipboard", "--output"}, nil
		}
		if has("wl-paste") {
			return []string{"wl-paste", "--no-newline"}, nil
		}
		return nil, errors.New("这台机器上没有剪贴板工具：装 xclip（X11）或 wl-clipboard（Wayland）")
	}
	return nil, fmt.Errorf("%s 上还没做剪贴板读取", goos)
}
