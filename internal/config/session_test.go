package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// 名字会被拼进一条**敲进登录 shell 的命令行**（`herdr --session <name>`）和一个
// socket 路径。所以这份用例的重点不是「常见名字能过」，而是「危险的一律不过」。
func TestValidSessionName(t *testing.T) {
	ok := []string{"work", "a", "A1", "my-proj", "my_proj", "v0.1", "9", strings.Repeat("x", MaxSessionName)}
	for _, s := range ok {
		if !ValidSessionName(s) {
			t.Errorf("%q 该是合法的", s)
		}
	}
	bad := []string{
		"", " ", "  work ", // 空 = 默认 session，调用方自己判；空格不收（拼进命令行就是两个参数）
		"-work", ".work", "_x", // 首字符必须字母数字，否则会被当成命令行选项
		"a;rm -rf ~", "a b", "a$(id)", "a`id`", "a|b", "a&b", "a>b", "a'b", `a"b`, "a\nb",
		"a/b", "../x", "a..b", "/", "..", // 路径穿越
		"工作", "a\x00b", // 非 ASCII / NUL
		strings.Repeat("x", MaxSessionName+1),
	}
	for _, s := range bad {
		if ValidSessionName(s) {
			t.Errorf("%q 不该是合法的", s)
		}
	}
}

// 路径是照 `herdr session list --json` 的 socket_path 排的（实测）：
// 默认在 <dir>/herdr.sock，命名的在 <dir>/sessions/<name>/herdr.sock。
func TestSessionSocket(t *testing.T) {
	c := &Config{Socket: "/home/u/.config/herdr/herdr.sock"}
	if got := c.SessionSocket(""); got != c.Socket {
		t.Errorf("默认 session 该原样给 %q，给了 %q", c.Socket, got)
	}
	want := filepath.FromSlash("/home/u/.config/herdr/sessions/work/herdr.sock")
	if got := c.SessionSocket("work"); got != want {
		t.Errorf("命名 session 给了 %q，want %q", got, want)
	}

	// HERDR_WEB_SOCKET 已经指到某个命名 session 时要退回上层，否则拼出
	// sessions/sight/sessions/work 这种不存在的路径，报出来只是「连不上 socket」。
	c = &Config{Socket: "/home/u/.config/herdr/sessions/sight/herdr.sock"}
	if got := c.SessionSocket("work"); got != want {
		t.Errorf("从命名 session 的 socket 出发给了 %q，want %q", got, want)
	}

	// socket 文件名不写死：HERDR_WEB_SOCKET 指到别的名字时跟着它
	c = &Config{Socket: "/tmp/h/api.sock"}
	if got, want := c.SessionSocket("x"), filepath.FromSlash("/tmp/h/sessions/x/api.sock"); got != want {
		t.Errorf("给了 %q，want %q", got, want)
	}
}
