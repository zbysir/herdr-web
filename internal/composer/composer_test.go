package composer

import (
	"os"
	"path/filepath"
	"testing"
)

// fixtures 和 JS 版共用同一批真机抓屏（test/fixtures/*.ansi），
// 所以这里的期望值必须和 lib/composer.js 的输出逐字节一致。
func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name+".ansi"))
	if err != nil {
		t.Fatalf("读不到 fixture %s: %v", name, err)
	}
	return string(b)
}

func TestRealScreens(t *testing.T) {
	cases := []struct{ name, agent, want string }{
		{"claude-empty", "claude", ""},
		{"claude-typed", "claude", "帮我看看 README，里面「三种连法」那节。"},
		{"claude-multiline", "claude", "第一行：改这个函数\n> 这行以尖括号开头，不能被当成输入框起点\n第三行结束"},
		{"codex-empty", "codex", ""},
		{"codex-typed", "codex", "看一下 composer.js 的 dim 处理，别改。"},
		{"codex-multiline", "codex", "第一行：codex 多行\n> 引用行不能当起点\n第三行"},
		{"shell-empty", "", "➜  herdr-web git:(master) ✗"},
		{"shell-typed", "", "➜  herdr-web git:(master) ✗ echo 你好，世界"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Extract(fixture(t, c.name), c.agent); got != c.want {
				t.Errorf("Extract(%s, %q)\n got %q\nwant %q", c.name, c.agent, got, c.want)
			}
		})
	}
}

// 未知 agent 走嗅探，结果应当和显式指定一致
func TestUnknownAgentSniffs(t *testing.T) {
	for _, c := range []struct{ name, agent string }{
		{"codex-typed", "codex"},
		{"claude-typed", "claude"},
	} {
		if got, want := Extract(fixture(t, c.name), ""), Extract(fixture(t, c.name), c.agent); got != want {
			t.Errorf("%s: 嗅探 %q != 指定 %q", c.name, got, want)
		}
	}
}

const (
	dim   = "\x1b[2m"
	off   = "\x1b[0m"
	rule  = "────────────────────────────────────────"
	bgCol = "\x1b[48;2;49;52;57m" // codex 输入区背景色
)

func TestPitfallDimIsChrome(t *testing.T) {
	screen := rule + "\n❯ " + dim + "Run /review on my current changes" + off + "\n" + rule
	if got := Extract(screen, "claude"); got != "" {
		t.Errorf("纯 dim 占位应该返回空，得到 %q", got)
	}
}

// 按分号朴素切分会把 38;2;153;153;153 里的 2 读成 dim，
// 而 Claude Code 的横线正好用这个色，整个输入框会被判成占位、返回空
func TestPitfallTruecolorTwoIsNotDim(t *testing.T) {
	color := "\x1b[38;2;153;153;153m"
	screen := color + rule + off + "\n" + color + "❯" + off + " 真实内容，前面挂着 38;2 前景色\n" + color + rule + off
	if got, want := Extract(screen, "claude"), "真实内容，前面挂着 38;2 前景色"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestDimStates(t *testing.T) {
	eq := func(got []bool, want ...bool) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}
	if !eq(dimStates("38;2;153;153;153")) {
		t.Error("38;2;r;g;b 的子参数应当被整段消费，不产生 dim")
	}
	if !eq(dimStates("48;5;236")) {
		t.Error("48;5;n 同理")
	}
	if !eq(dimStates("2"), true) {
		t.Error("裸 2 是 dim")
	}
	if !eq(dimStates("38;2;1;2;3;2"), true) {
		t.Error("消费完之后剩下的那个 2 才是 dim")
	}
	if !eq(dimStates("2;22"), true, false) {
		t.Error("2;22 应当是 dim 然后取消")
	}
	if !eq(dimStates(""), false) {
		t.Error("空参数（ESC[m）等于 reset")
	}
}

// 真正吃劲的不是判空（rstrip 顺手就把 NBSP 干掉了），
// 而是有内容时「去掉提示符后那一个分隔空格」
func TestPitfallNBSP(t *testing.T) {
	if got := Extract(rule+"\n❯\u00a0\n"+rule, "claude"); got != "" {
		t.Errorf("空框应当返回空，得到 %q", got)
	}
	if got, want := Extract(rule+"\n❯\u00a0真实内容\n"+rule, "claude"), "真实内容"; got != want {
		t.Errorf("got %q want %q —— NBSP 没归一，正文前面挂了个 NBSP", got, want)
	}
}

func TestCodexBandExcludesStatusBar(t *testing.T) {
	screen := "  上面的历史输出\n" + bgCol + "❯ 输入的内容" + off + "\n  gpt-5 · ~/repo · master · 状态栏在色块外"
	if got, want := Extract(screen, "codex"), "输入的内容"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
	multi := "  历史输出\n" + bgCol + "❯ 第一行" + off + "\n" + bgCol + "  第二行" + off + "\n  状态栏"
	if got, want := Extract(multi, "codex"), "第一行\n第二行"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestClaudeQuoteLineNotAnchor(t *testing.T) {
	screen := rule + "\n❯ 请看这段引用：\n  > 被引用的话\n  收尾一句\n" + rule
	if got, want := Extract(screen, "claude"), "请看这段引用：\n> 被引用的话\n收尾一句"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestEmptyInputs(t *testing.T) {
	for _, s := range []string{"\n\n   \n", ""} {
		if got := Extract(s, ""); got != "" {
			t.Errorf("Extract(%q) = %q，应当是空", s, got)
		}
	}
}
