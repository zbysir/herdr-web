package agentwatch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func screen(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// 真机抓的一屏「等你回答」：底下是 claude 的选择框（`☐ 要不要装服务` + 问题 + 四个选项），
// 上面是上一段回答的尾巴，最下面是操作提示和输入行。要的是那个问题，不是别的。
func TestExtractAskBlock(t *testing.T) {
	got := extract(screen(t, "blocked-ask.txt"), "blocked", noticeLines, noticeChars)
	t.Logf("抽到的：\n%s", got)

	if !strings.HasPrefix(got, "要不要装服务") {
		t.Errorf("应当从选择框的小标题开始（`☐` 记号剥掉），got 开头：%q", head(got, 40))
	}
	if !strings.Contains(got, "要把它换成开机自启的 launchd 服务吗？") {
		t.Error("问题本身必须在里面")
	}
	if !strings.Contains(got, "1. 装，指向 ~/.local/bin（推荐）") {
		t.Error("选项要留着 —— 「有哪几个选择」是这条提示最有用的部分")
	}
	if strings.Contains(got, "Enter to select") || strings.Contains(got, "Esc to cancel") {
		t.Errorf("操作提示不该进来：\n%s", got)
	}
	if strings.Contains(got, "这个删掉吧") {
		t.Errorf("最下面那行输入框里的草稿不该进来：\n%s", got)
	}
	if strings.Contains(got, "顺带一句") || strings.Contains(got, "重启机器不会自己起来") {
		t.Errorf("选择框上面那段旧回答不该进来：\n%s", got)
	}
}

// 真机抓的一屏「跑完了」：最后一个 `⏺` 块是回答，后面跟着 spinner、输入框那一圈、状态栏；
// 屏幕上面还有两段更早的 `⏺` 块和一段 recap。
func TestExtractAnswerBlock(t *testing.T) {
	got := extract(screen(t, "idle-answer.txt"), "idle", noticeLines, noticeChars)
	t.Logf("抽到的：\n%s", got)

	if !strings.HasPrefix(got, "后台通知：") {
		t.Errorf("应当从**最后**一个 ⏺ 块开始，got 开头：%q", head(got, 40))
	}
	if !strings.Contains(got, "没有需要动的东西") {
		t.Error("同一块里的续行要留着")
	}
	for _, bad := range []string{"Baked for", "─────", "⏵⏵", "git:(master)", "recap"} {
		if strings.Contains(got, bad) {
			t.Errorf("装饰行 %q 不该进来：\n%s", bad, got)
		}
	}
	if strings.Contains(got, "提交好了") {
		t.Errorf("上一个 ⏺ 块（更早的回答）不该进来：\n%s", got)
	}
}

// 同样是「跑完了」，但这一屏的回答有 30 多行、后面还跟着 recap 的两行。
// 要的是**回答的开头**（第一句最有信息量）+ 一个截断记号。
func TestExtractAnswerTruncates(t *testing.T) {
	got := extract(screen(t, "idle-recap.txt"), "idle", noticeLines, noticeChars)
	t.Logf("抽到的：\n%s", got)

	if !strings.HasPrefix(got, "PR 开好了") {
		t.Errorf("应当从最后一个 ⏺ 块的第一行开始，got 开头：%q", head(got, 40))
	}
	if n := len(strings.Split(got, "\n")); n > noticeLines {
		t.Errorf("超了行数上限：%d 行", n)
	}
	if !strings.HasSuffix(got, "…") {
		t.Error("截断了要有记号，不然看不出后面还有")
	}
	if strings.Contains(got, "recap") || strings.Contains(got, "Worked for") {
		t.Errorf("recap / spinner 不该进来：\n%s", got)
	}
}

// `╭ … ╰` 那种框（claude 的权限确认框）：框线要剥掉，框里的内容要留着。
// **这一屏是手写的**，不是抓屏 —— 上面三个用例都是真机形状，这个形状手上没有抓到过。
func TestExtractAskRoundedBox(t *testing.T) {
	text := strings.Join([]string{
		"⏺ 我去改一下那个文件。",
		"",
		"╭──────────────────────────────────────╮",
		"│ Edit file                            │",
		"│ web/src/App.tsx                      │",
		"│                                      │",
		"│ Do you want to make this edit?       │",
		"│ ❯ 1. Yes                             │",
		"│   2. No, and tell Claude what to do  │",
		"╰──────────────────────────────────────╯",
		"  Esc to cancel",
		"",
		"  ~/dev/bysir/herdr-web git:(master)",
	}, "\n")

	got := extract(text, "blocked", noticeLines, noticeChars)
	t.Logf("抽到的：\n%s", got)

	if !strings.Contains(got, "Do you want to make this edit?") {
		t.Errorf("框里的问题必须在：\n%s", got)
	}
	if !strings.Contains(got, "❯ 1. Yes") {
		t.Errorf("选项必须在（`❯` 标的是当前选中那个）：\n%s", got)
	}
	if strings.Contains(got, "│") || strings.Contains(got, "╭") || strings.Contains(got, "╰") {
		t.Errorf("框线该剥掉：\n%s", got)
	}
	if strings.Contains(got, "我去改一下") {
		t.Errorf("框上面那段回答不该进来：\n%s", got)
	}
	if strings.Contains(got, "Esc to cancel") {
		t.Errorf("操作提示不该进来：\n%s", got)
	}
}

// 没有 `⏺` 记号（codex 这类）：退化成取尾部，两头的装饰行剪掉。
func TestExtractNoMarker(t *testing.T) {
	text := strings.Join([]string{
		"  earlier output",
		"  改完了，两个文件：internal/x.go 和 web/src/y.ts。",
		"  测试全过。",
		"",
		"❯ ",
		"───────────",
		"  ~/workspace git:(main)",
	}, "\n")

	got := extract(text, "idle", noticeLines, noticeChars)
	t.Logf("抽到的：\n%s", got)

	if !strings.Contains(got, "测试全过") {
		t.Errorf("尾巴上的正文要留着：\n%s", got)
	}
	if strings.Contains(got, "❯") || strings.Contains(got, "git:(main)") {
		t.Errorf("输入框 / 状态栏不该进来：\n%s", got)
	}
}

// 整屏都是装饰行（agent 刚起来、还没说过话）：给空字符串。
// **空着是实话** —— 弹窗那边看到空的就只报状态，不硬凑一段文字出来。
func TestExtractEmptyWhenNothingToSay(t *testing.T) {
	text := strings.Join([]string{
		"",
		"─────────────",
		"❯ ",
		"─────────────",
		"  ~/dev/bysir/herdr-web git:(master)",
		"  ⏵⏵ auto mode on (shift+tab to cycle)",
	}, "\n")
	if got := extract(text, "idle", noticeLines, noticeChars); got != "" {
		t.Errorf("没有内容时应当给空字符串，got %q", got)
	}
}

func head(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// 真机抓屏：状态刚翻的那一瞬间，屏幕最下面是一串**工具调用**（`⏺ Searching for …` +
// `⎿  $ cd …`）。要的是上面那句它**说**的话，不是那段 shell 流水。
//
// 这一条是线上抓出来的 bug：第一版取「最后一个 ⏺ 块」，于是弹窗里只有一段
// `cd /private/tmp/claude-501/…`，完全看不出发生了什么。
func TestExtractSkipsToolBlocks(t *testing.T) {
	got := extract(screen(t, "idle-toolcall.txt"), "idle", noticeLines, noticeChars)
	t.Logf("抽到的：\n%s", got)

	if !strings.HasPrefix(got, "Now the softkeys backend allowlist") {
		t.Errorf("该取最后一个**不是**工具调用的 ⏺ 块，got 开头：%q", head(got, 50))
	}
	for _, bad := range []string{"⎿", "cd /private/tmp", "claude-in-chrome 28 times", "Nebulizing"} {
		if strings.Contains(got, bad) {
			t.Errorf("工具流水 / spinner 不该进来（%q）：\n%s", bad, got)
		}
	}
}

// 整屏只有工具块（它一直在干活、一句话都还没说）：退回最后那个块，至少说清在干什么。
func TestExtractToolOnlyFallsBackToLatest(t *testing.T) {
	text := strings.Join([]string{
		"⏺ Read 4 files",
		"  ⎿  web/src/App.tsx",
		"",
		"⏺ Running 16 shell commands…",
		"  ⎿  $ go test ./...",
		"",
		"✢ Nebulizing… (30m 1s)",
		"────────────",
		"❯ ",
	}, "\n")

	got := extract(text, "idle", noticeLines, noticeChars)
	t.Logf("抽到的：\n%s", got)
	if got != "Running 16 shell commands…" {
		t.Errorf("该退回最后那个工具块的标题行，got %q", got)
	}
}

// blocked 但屏幕上没有对话框（`agent_status` 判断不了「正开着对话框」，见 HERDR-API.md）：
// 按「跑完了」那套抽最后一段话，而不是把屏幕最底下二十行原样端上来。
func TestExtractBlockedWithoutDialog(t *testing.T) {
	got := extract(screen(t, "idle-answer.txt"), "blocked", noticeLines, noticeChars)
	t.Logf("抽到的：\n%s", got)

	if !strings.HasPrefix(got, "后台通知：") {
		t.Errorf("没有对话框时该退回抽最后一个 ⏺ 块，got 开头：%q", head(got, 40))
	}
	if strings.Contains(got, "⏵⏵") || strings.Contains(got, "git:(master)") {
		t.Errorf("状态栏不该进来：\n%s", got)
	}
}
