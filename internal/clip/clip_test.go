package clip

import (
	"errors"
	"strings"
	"testing"
)

func lookOnly(present ...string) func(string) (string, error) {
	set := map[string]bool{}
	for _, p := range present {
		set[p] = true
	}
	return func(bin string) (string, error) {
		if set[bin] {
			return "/usr/bin/" + bin, nil
		}
		return "", errors.New("not found")
	}
}

func env(kv map[string]string) func(string) string {
	return func(k string) string { return kv[k] }
}

func TestReadCmdPicksTool(t *testing.T) {
	cases := []struct {
		name  string
		goos  string
		bins  []string
		env   map[string]string
		want  string
		isErr bool
	}{
		{name: "mac", goos: "darwin", bins: []string{"pbpaste"}, want: "pbpaste"},
		{name: "mac 少了 pbpaste", goos: "darwin", isErr: true},
		// Wayland 会话优先认 wl-paste：xclip 在 Wayland 上装着也用不了
		{name: "wayland", goos: "linux", bins: []string{"wl-paste", "xclip"},
			env: map[string]string{"WAYLAND_DISPLAY": "wayland-0"}, want: "wl-paste --no-newline"},
		// 反过来：X11 会话里 wl-paste 存在也不能用它
		{name: "x11 装了两个", goos: "linux", bins: []string{"wl-paste", "xclip"},
			want: "xclip -selection clipboard -o"},
		{name: "只有 xsel", goos: "linux", bins: []string{"xsel"}, want: "xsel --clipboard --output"},
		{name: "linux 什么都没装", goos: "linux", isErr: true},
		{name: "windows", goos: "windows", bins: []string{"pbpaste"}, isErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			argv, err := readCmd(c.goos, lookOnly(c.bins...), env(c.env))
			if c.isErr {
				if err == nil {
					t.Fatalf("该报错，却给了 %v", argv)
				}
				return
			}
			if err != nil {
				t.Fatalf("不该报错: %v", err)
			}
			if got := strings.Join(argv, " "); got != c.want {
				t.Errorf("挑了 %q，want %q", got, c.want)
			}
		})
	}
}

// `wl-paste` 不给 --no-newline 会在末尾补一个换行 —— 那东西粘进终端等于替你按了回车。
func TestWlPasteAlwaysSuppressesNewline(t *testing.T) {
	for _, e := range []map[string]string{{"WAYLAND_DISPLAY": "wayland-0"}, {}} {
		argv, err := readCmd("linux", lookOnly("wl-paste"), env(e))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(strings.Join(argv, " "), "--no-newline") {
			t.Errorf("%v 少了 --no-newline", argv)
		}
	}
}
