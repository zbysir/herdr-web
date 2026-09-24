package remote

import "testing"

func TestParse(t *testing.T) {
	ok := map[string]Target{
		"herdr.example.com":             {Origin: "https://herdr.example.com"},
		"https://herdr.example.com/":    {Origin: "https://herdr.example.com"},
		"https://h.example.com:8443/wk": {Origin: "https://h.example.com:8443", Session: "wk"},
		"http://127.0.0.1:7811":         {Origin: "http://127.0.0.1:7811"},
		"http://192.168.1.5:7788/a.b":   {Origin: "http://192.168.1.5:7788", Session: "a.b"},
	}
	for in, want := range ok {
		got, err := Parse(in)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %+v, %v；想要 %+v", in, got, err, want)
		}
	}
	// 公网明文会把令牌（= 一个登录 shell）裸着发出去
	for _, in := range []string{"http://herdr.example.com", "http://8.8.8.8", "ftp://x", "https://h/a/b", "https://h/.."} {
		if _, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) 该报错", in)
		}
	}
}

func TestEscaper(t *testing.T) {
	cases := []struct {
		in   []string
		out  string
		quit bool
	}{
		{[]string{"~."}, "", true},              // 一开始就算行首
		{[]string{"ls\r~."}, "ls\r", true},      // 回车之后
		{[]string{"a~."}, "a~.", false},         // 行中不算
		{[]string{"~/foo"}, "~/foo", false},     // 行首的 ~ 只是晚一个键发
		{[]string{"~~x"}, "~x", false},          // ~~ = 一个 ~
		{[]string{"\r~", "."}, "\r", true},      // 跨两批
		{[]string{"\r~", "\r"}, "\r~\r", false}, // ~ 后面跟回车：原样发
	}
	for _, c := range cases {
		e := newEscaper()
		var out string
		var quit bool
		for _, b := range c.in {
			o, q := e.feed([]byte(b))
			out += string(o)
			if q {
				quit = true
				break
			}
		}
		if out != c.out || quit != c.quit {
			t.Errorf("%q → %q quit=%v；想要 %q quit=%v", c.in, out, quit, c.out, c.quit)
		}
	}
}
