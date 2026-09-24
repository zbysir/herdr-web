package remote

import (
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/term"
)

var errAborted = errors.New("取消了")

// choose 在终端里画一个上下箭头选的小菜单，返回选中的下标。
//
// ↑↓（或 k/j）移动、回车确定、数字键直接选、Esc / q / Ctrl+C 取消。
//
// 读键走的是 stdinReader 而不是直接读 os.Stdin：连着的时候中途要重验，stdin 已经被转发那个
// goroutine 占着，只能从它那儿接（见 session.go 的 ensureWith）。raw 模式是**终端**的设置，
// 谁在读都一样生效，所以这里照样自己开、自己关。
//
// 标准输入不是终端（被管道喂进来）时画不了菜单，直接返回 def —— 调用方照旧往下走。
func choose(title string, items []string, def int) (int, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return def, nil
	}
	old, err := term.MakeRaw(fd)
	if err != nil {
		return def, nil
	}
	defer func() { _ = term.Restore(fd, old) }()

	out := os.Stderr
	cur := def
	draw := func(first bool) {
		if !first {
			fmt.Fprintf(out, "\x1b[%dA", len(items)) // 回到菜单第一行重画
		}
		for i, it := range items {
			mark, style, end := "  ", "\x1b[2m", "\x1b[0m"
			if i == cur {
				mark, style = "\x1b[32m❯\x1b[0m ", "\x1b[1m"
			}
			fmt.Fprintf(out, "\r\x1b[K    %s%s%s%s\r\n", mark, style, it, end)
		}
	}
	fmt.Fprintf(out, "\r\n  %s\x1b[2m（↑↓ 选，回车确定）\x1b[0m\r\n\r\n", title)
	fmt.Fprint(out, "\x1b[?25l") // 选的时候别让光标在底下闪
	defer fmt.Fprint(out, "\x1b[?25h")
	draw(true)

	buf := make([]byte, 16)
	for {
		n, err := stdinReader.Read(buf)
		if err != nil {
			if err == io.EOF {
				return 0, errAborted
			}
			return 0, err
		}
		k := string(buf[:n])
		switch {
		case k == "\x1b[A" || k == "\x1bOA" || k == "k":
			cur = (cur + len(items) - 1) % len(items)
		case k == "\x1b[B" || k == "\x1bOB" || k == "j" || k == "\t":
			cur = (cur + 1) % len(items)
		case k == "\r" || k == "\n":
			fmt.Fprint(out, "\r\n")
			return cur, nil
		case len(k) == 1 && k[0] >= '1' && int(k[0]-'1') < len(items):
			cur = int(k[0] - '1')
			draw(false)
			fmt.Fprint(out, "\r\n")
			return cur, nil
		case k == "\x1b" || k == "q" || k == "\x03" || k == "\x04":
			fmt.Fprint(out, "\r\n")
			return 0, errAborted
		default:
			continue
		}
		draw(false)
	}
}
