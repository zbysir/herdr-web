// Package composer 从 pane 的 ANSI 快照里抽出输入框内容。
//
// 按 agent 类型分派，与 herdr 自己的 agent-detection manifest 保持一致：
//
//	claude → prompt_box_body / after_last_horizontal_rule（输入框是上下横线夹的 box）
//	codex  → after_last_prompt_marker（提示符之后；这里额外用背景色块收边，
//	         因为我们要的是精确内容而不是一个供 regex 求值的区域）
//	其他   → 通用嗅探（manifest 里另外 17 个 agent 也没有输入区规则）
//
// dim（SGR 2）的文字算占位提示，不算内容 —— Codex 空框的占位文字就是这么渲染的；
// 实测两家的真实输入都不带 dim。
//
// 这是 lib/composer.js 的移植，输入框内容必须逐字节一致（testdata 里的真机抓屏
// 是共用的验收集）。**有一处故意不一致**：认不出输入框时旧版退回「屏幕最后一行
// 非空」，这里返回 ok=false —— 见 Extract。
package composer

import (
	"regexp"
	"strings"
)

var (
	// OSC（ESC ] … BEL/ST）、CSI、字符集切换。`.` 不跨行，和 JS / Python 版一致 ——
	// 调用方已经按行切开了。
	escRe = regexp.MustCompile(`\x1b\][^\n]*?(?:\x07|\x1b\\)|\x1b\[[0-9;?]*[A-Za-z]|\x1b[()][A-Za-z0-9]`)
	sgrRe = regexp.MustCompile(`\x1b\[([0-9;]*)m`)
	bgRe  = regexp.MustCompile(`48;(?:2;\d+;\d+;\d+|5;\d+)`)
	// 四个以上的横线算一条分隔线
	ruleRe = regexp.MustCompile(`^\s*[─━┄┅┈┉]{4,}\s*$`)
	// 不认裸 > ，否则 markdown 引用行会被当成输入框起点
	markerRe = regexp.MustCompile(`^[ \t]{0,4}[❯›❱]`)
)

// Claude Code 的空输入框是 `❯` + U+00A0（NBSP）而不是空格，判空之前先归一。
func normalize(s string) string {
	s = escRe.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\r", "")
	return strings.ReplaceAll(s, "\u00a0", " ")
}

// rstrip 去掉行尾空白（含 NBSP，归一之后已经是普通空格）。
func rstrip(s string) string { return strings.TrimRight(s, " \t\n\r\v\f\u0085\u00a0") }

func blank(s string) bool { return strings.TrimSpace(s) == "" }

// dimStates 按 SGR 规则解析参数，返回这一串里依次出现的 dim 状态。
//
// 38/48/58 的 `2;r;g;b` 与 `5;n` 子参数必须整段消费掉，否则
// `38;2;153;153;153` 里的那个 2 会被误判成 dim —— Claude Code 的横线正好用
// 这个色，整个输入框会被判成占位、返回空字符串。
func dimStates(params string) []bool {
	parts := []string{"0"}
	if params != "" {
		parts = strings.Split(params, ";")
	}
	out := []bool{}
	for i := 0; i < len(parts); {
		p := parts[i]
		if p == "" {
			p = "0"
		}
		if p == "38" || p == "48" || p == "58" {
			nxt := ""
			if i+1 < len(parts) {
				nxt = parts[i+1]
			}
			switch nxt {
			case "2":
				i += 5
			case "5":
				i += 3
			default:
				i++
			}
			continue
		}
		switch p {
		case "0", "22":
			out = append(out, false)
		case "2":
			out = append(out, true)
		}
		i++
	}
	return out
}

// visibleText 剔除 dim 段之后的纯文本。
func visibleText(line string) string {
	var b strings.Builder
	dim, pos := false, 0
	for _, m := range sgrRe.FindAllStringSubmatchIndex(line, -1) {
		if !dim {
			b.WriteString(line[pos:m[0]])
		}
		for _, s := range dimStates(line[m[2]:m[3]]) {
			dim = s
		}
		pos = m[1]
	}
	if !dim {
		b.WriteString(line[pos:])
	}
	return normalize(b.String())
}

// PlainText 保留 dim 的纯文本，用于定位（横线和 marker 本身可能是 dim 的）。
func PlainText(line string) string { return normalize(line) }

// anchorOf 最后一个提示符行的下标，没有返回 -1。
func anchorOf(plain []string) int {
	for i := len(plain) - 1; i >= 0; i-- {
		if markerRe.MatchString(plain[i]) {
			return i
		}
	}
	return -1
}

// boxBounds claude 式：最近的上下两条横线之间。
func boxBounds(plain []string, anchor int) (int, int) {
	u := anchor - 1
	for u >= 0 && !ruleRe.MatchString(plain[u]) {
		u--
	}
	d := anchor + 1
	for d < len(plain) && !ruleRe.MatchString(plain[d]) {
		d++
	}
	if u >= 0 && d < len(plain) {
		return u + 1, d - 1
	}
	return anchor, anchor
}

// bandBounds codex 式：与 marker 行同背景色的连续段（状态栏不在色块内）。
func bandBounds(raw []string, anchor int) (int, int) {
	key := func(l string) string {
		found := bgRe.FindAllString(l, -1)
		seen := map[string]bool{}
		uniq := []string{}
		for _, f := range found {
			if !seen[f] {
				seen[f] = true
				uniq = append(uniq, f)
			}
		}
		sortStrings(uniq)
		return strings.Join(uniq, ",")
	}
	bg := make([]string, len(raw))
	for i, l := range raw {
		bg[i] = key(l)
	}
	if bg[anchor] == "" {
		return anchor, anchor
	}
	lo, hi := anchor, anchor
	for lo-1 >= 0 && bg[lo-1] == bg[anchor] {
		lo--
	}
	for hi+1 < len(raw) && bg[hi+1] == bg[anchor] {
		hi++
	}
	return lo, hi
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// finish 去掉提示符字形、去掉两格缩进、掐掉首尾空行。
func finish(vis []string, lo, hi int) string {
	out := make([]string, hi-lo+1)
	copy(out, vis[lo:hi+1])

	for k := range out {
		if loc := markerRe.FindStringIndex(out[k]); loc != nil {
			out[k] = out[k][:loc[0]] + out[k][loc[1]:]
			if strings.HasPrefix(out[k], " ") {
				out[k] = out[k][1:]
			}
			break
		}
	}
	for k := range out {
		out[k] = rstrip(strings.TrimPrefix(out[k], "  "))
	}
	for len(out) > 0 && blank(out[0]) {
		out = out[1:]
	}
	for len(out) > 0 && blank(out[len(out)-1]) {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
}

// Extract 从 pane.read 的 ANSI 快照里抽出输入框内容。
// agent 是 pane.get 的 agent 字段（"claude" / "codex" / ""）。
//
// 两种「空」必须分清：ok=true + "" 是**认出了输入框、里面是空的**；
// ok=false 是**这一屏上认不出输入框**（没有提示符字形）。调用方对这两种的处理
// 完全不同 —— 前者可以放心覆盖式投稿，后者不知道文字会落到哪，只能拒投。
func Extract(ansiText, agent string) (string, bool) {
	raw := strings.Split(ansiText, "\n")
	plain := make([]string, len(raw))
	vis := make([]string, len(raw))
	for i, l := range raw {
		plain[i] = PlainText(l)
		vis[i] = visibleText(l)
	}

	anchor := anchorOf(plain)
	if anchor < 0 {
		// 认不出输入框就说认不出。原来这里退回「最后一行非空」，那纯粹是猜：
		// 屏幕最后一行往往是状态栏、上一条命令的输出、或者 agent 画的某个控件，
		// 拉回来就是把这行垃圾塞进发件箱，用户还以为那是远端输入框里的字。
		return "", false
	}

	var lo, hi int
	switch agent {
	case "claude":
		lo, hi = boxBounds(plain, anchor)
	case "codex":
		lo, hi = bandBounds(raw, anchor)
	default:
		// 未知 agent：先色块，再横线，最后单行
		lo, hi = bandBounds(raw, anchor)
		if lo == anchor && hi == anchor {
			lo, hi = boxBounds(plain, anchor)
		}
	}
	return finish(vis, lo, hi), true
}

// ScreenLines 整屏纯文本（压掉空行），给「原始屏幕」调试视图用。
func ScreenLines(ansiText string) []string {
	out := []string{}
	for _, l := range strings.Split(ansiText, "\n") {
		if s := rstrip(PlainText(l)); !blank(s) {
			out = append(out, s)
		}
	}
	return out
}
