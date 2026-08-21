package agentwatch

import "strings"

// 从一屏终端文字里抽出「这条提示要说的那段话」。
//
// 为什么必须读屏抽：herdr 的 API 里没有「agent 最后说了什么」这种字段（和
// COMPOSER.md 里「没有输入框内容字段」是同一回事，grep 过整份 schema）。而整屏原样塞进
// 弹窗没法看 —— 一屏里除了要看的那段，还躺着上一个任务的尾巴、spinner（`✻ Baked for 20s`）、
// recap（`※ recap: …`）、输入框那一圈 `────` / `❯`、底下的状态栏（`~/path git:(master)`、
// `⏵⏵ auto mode`）。
//
// **抽法按状态分两种** —— 同一屏文字，「等你回答」和「跑完了」要看的地方完全不是一处：
//
//   - **等你回答（blocked）**：问题在屏幕**最下面**那一块（`☐ 要不要装服务` 那种小标题，
//     或者 `╭ … ╰` 围起来的框），后面跟着选项和一行操作提示（`Enter to select · ↑/↓ …`）。
//     所以从最后一个 `☐` / `╭` 往下取，取到操作提示那行为止。
//   - **跑完了（idle / done）**：claude 的最终回复带 `⏺` 前缀，所以取**最后一个** `⏺` 块，
//     取到 spinner / recap / 输入框那一圈为止。codex 这类没有标记的，退化成取尾部若干行。
//
// 抽法参照 herdr-sight 的 `internal/hub.extractResult`（那边只有「跑完了」这一种：它是
// 「任务交活了收成果」，而这里还要管「等你回答」——弹窗最该弹的恰恰是后者）。另一处不同是
// 读屏只读 `visible` 一跳，理由见 `herdr.ReadText`。
//
// 三个 testdata 是真机抓屏（`pane.read` + `strip_ansi`）：一屏 blocked 的选择框、两屏
// 跑完了的回答。**改这儿必须跑 `go test ./internal/agentwatch/`** —— 这些规则全是照着
// 真屏幕形状挑的，凭想象改一条就会静默抽错（抽错的表现是弹窗里只有一行 `❯` 或者一段
// 状态栏，而日志里什么都不报）。

// extract 按状态挑抽法，然后收拾成最多 maxLines 行 / maxChars 个字。
func extract(text, status string, maxLines, maxChars int) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if status == "blocked" {
		return tidy(askBlock(lines, maxLines), true, maxLines, maxChars)
	}
	return tidy(answerBlock(lines, maxLines), false, maxLines, maxChars)
}

// answerBlock 取最后一个「它**说**的话」那种 `⏺` 块，到 spinner / recap / 输入框那一圈为止。
//
// **带 `⎿` 的 `⏺` 块要跳过**：那个记号是工具输出（`⏺ Searching for 1 pattern…` 后面跟着
// 一屏 `⎿  $ cd …`），是「它在干活」的流水，不是它对你说的话。真机上抓到过一次：状态刚翻成
// idle 的那一瞬间屏幕最下面是一串 shell 命令，弹窗里于是只有一段 `cd /private/tmp/…` ——
// 完全看不出发生了什么。跳过工具块之后拿到的是上面那句「Go 侧完成（测试通过）。现在写前端。」
//
// 一个 `⏺` 都没有的（codex、或者回复被刷出屏了）退化成取尾部：那是「不知道从哪儿开始」时
// 最不容易错的选择 —— 屏幕最下面总是最新的。
func answerBlock(lines []string, maxLines int) []string {
	last := -1 // 最后一个 ⏺ 块，哪怕它是工具块（整屏全是工具块时的退路）
	for i := len(lines) - 1; i >= 0; i-- {
		if !strings.HasPrefix(strings.TrimSpace(lines[i]), "⏺") {
			continue
		}
		if last < 0 {
			last = i
		}
		if !isToolBlock(lines, i) {
			return cutAt(lines, i)
		}
	}
	if last >= 0 {
		return cutAt(lines, last) // 全是工具块：至少说清它这会儿在干什么
	}

	start := len(lines) - maxLines*2 // 多给一倍，装饰行剪掉之后还够 tidy 截
	if start < 0 {
		start = 0
	}
	for start < len(lines) && isDecor(lines[start]) {
		start++
	}
	out := lines[start:]
	for len(out) > 0 && (isDecor(out[len(out)-1]) || strings.TrimSpace(out[len(out)-1]) == "") {
		out = out[:len(out)-1]
	}
	return out
}

// cutAt 从 start 取**一个块**：到下一个 `⏺` 或者第一个装饰行为止。
//
// 「到下一个 `⏺` 为止」是必须的：选中的那个块后面可能还跟着别的块（工具调用就是这样被
// 跳过的），不切的话跳过工具块的努力白费 —— 它照样跟在正文后面进了弹窗。
func cutAt(lines []string, start int) []string {
	out := lines[start:]
	for i := 1; i < len(out); i++ {
		if isDecor(out[i]) || strings.HasPrefix(strings.TrimSpace(out[i]), "⏺") {
			return out[:i]
		}
	}
	return out
}

// isToolBlock 说这个 `⏺` 块是不是工具调用（块里有 `⎿` 那种输出行）。
func isToolBlock(lines []string, start int) bool {
	for i := start + 1; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if strings.HasPrefix(t, "⎿") {
			return true
		}
		if strings.HasPrefix(t, "⏺") || isDecor(lines[i]) {
			return false // 块到这儿就结束了，没见到工具输出
		}
	}
	return false
}

// askBlock 取屏幕最下面那个「问你话」的块。
//
// **这里不能拿 isDecor 当结束条件**：被选中的那个选项就是 `❯ 1. 装，指向 ~/.local/bin`，
// 而 `❯` 在「跑完了」那条路上是输入框的标志。所以结束条件另一套（isAskEnd）：操作提示
// （`Enter to select`、`Esc to cancel`）和状态栏。
func askBlock(lines []string, maxLines int) []string {
	start := -1
	for i := len(lines) - 1; i >= 0; i-- {
		t := strings.TrimSpace(lines[i])
		if strings.HasPrefix(t, "☐") || strings.HasPrefix(t, "╭") {
			start = i
			break
		}
	}
	if start < 0 {
		// 屏幕上没有对话框。**这不是异常**：`agent_status` 判断不了「正开着对话框」
		// （实测同一个选择器，一次报 idle 一次报 blocked，见 HERDR-API.md），所以
		// blocked 常常配着一屏普通输出。这时候按「跑完了」那套抽最后一段话 —— 至少说得出
		// 它最后说了什么，比把屏幕最底下那二十行原样端上来强得多（那多半是 spinner 和状态栏）。
		return answerBlock(lines, maxLines)
	}
	out := lines[start:]
	for i := 1; i < len(out); i++ {
		if isAskEnd(out[i]) {
			return out[:i]
		}
	}
	return out
}

// isDecor 认「答案到这儿就结束了」的装饰行：spinner、recap、评分问卷、输入框那一圈、状态栏。
func isDecor(l string) bool {
	t := strings.TrimSpace(l)
	if t == "" {
		return false // 空行由 tidy 收拾；拿它当结束条件会把答案里的段落切断
	}
	// `⎿` 是工具输出的记号：它跟在工具 `⏺` 后面，不该跟着正文进弹窗（见 isToolBlock）
	for _, p := range []string{"✻", "✽", "✢", "✳", "∗", "※", "⎿", "❯", "⏵", "~/", ">_", "╭", "╰"} {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	// claude 的 recap 尾巴、「new task? /clear to save」提示条、会话评分问卷
	if strings.Contains(t, "disable recaps") || strings.Contains(t, "new task?") ||
		strings.Contains(t, "How is Claude doing") {
		return true
	}
	return isRule(t)
}

// isAskEnd 认选择框底下那几行：操作提示和状态栏。
func isAskEnd(l string) bool {
	t := strings.TrimSpace(l)
	if t == "" {
		return false
	}
	for _, s := range []string{"Enter to select", "to navigate", "Esc to cancel", "esc to interrupt", "to cycle"} {
		if strings.Contains(t, s) {
			return true
		}
	}
	return strings.HasPrefix(t, "~/") || strings.HasPrefix(t, "⏵")
}

// isRule 是「整行只有线」的分隔线：输入框上下那两条 `────`、框的上下沿 `╭───╮` /
// `╰───╯`、表格的横边、以及框里那种只有两根竖线的空行。
//
// 竖线也算进来（`│`）：`╭ … ╰` 框里的空行长这样 `│            │`，不算的话弹窗里会
// 多出一堆看不出是什么的行。有内容的表格行不会被误伤 —— 那些行里除了线还有字。
func isRule(t string) bool {
	return t != "" && strings.Trim(t, "─—-=═╌_ ╭╮╰╯├┤┌┐└┘┬┴┼│|") == ""
}

// tidy 把抽出来的那几行收拾成一段能塞进弹窗的文字：
// 去框线、去分隔线、并掉连续空行、去掉整段共有的左缩进，最后按行数 / 字数截断。
//
// box 为真（选择框）时还要剥掉每行两端的 `│`：`╭ … ╰` 框里每行都带一根，原样进弹窗只是噪音。
// 「跑完了」那条路**不剥** —— 答案里的表格行（`│ ● │ 助理 │`）剥了就散架了。
func tidy(seg []string, box bool, maxLines, maxChars int) string {
	out := make([]string, 0, len(seg))
	for _, l := range seg {
		if box {
			l = unbox(l)
		}
		l = strings.TrimRight(l, " \t")
		if isRule(strings.TrimSpace(l)) {
			continue
		}
		if strings.TrimSpace(l) == "" {
			if len(out) == 0 || out[len(out)-1] == "" {
				continue // 开头的空行、连续的空行都不要
			}
			out = append(out, "")
			continue
		}
		out = append(out, l)
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	if len(out) == 0 {
		return ""
	}
	// 第一行那个记号（`⏺` 回复标记 / `☐` 提问标记）在弹窗里没有信息 —— 状态标签已经说了
	// 这是回答还是提问。剥掉它，剩下的行再按共有缩进对齐（claude 的续行缩进两格）。
	out[0] = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(out[0]), "⏺☐"))
	out = dedent(out)
	cut := false
	if len(out) > maxLines {
		out = out[:maxLines]
		cut = true
	}
	s := strings.Join(out, "\n")
	if r := []rune(s); len(r) > maxChars {
		s = strings.TrimRight(string(r[:maxChars]), " \n")
		cut = true
	}
	if cut {
		s += " …"
	}
	return s
}

// unbox 剥掉一行两端的框线（`│ 内容 │`）。只剥两端 —— 行里面的 `│` 是内容（表格）。
func unbox(l string) string {
	t := strings.TrimRight(l, " ")
	t = strings.TrimSuffix(t, "│")
	if i := strings.Index(t, "│"); i >= 0 && strings.TrimSpace(t[:i]) == "" {
		t = t[i+len("│"):]
	}
	return t
}

// dedent 去掉第 2 行往后共有的左缩进。
//
// 只看第 2 行往后：第一行的记号刚被剥掉，它的缩进已经不代表这一段的左边距了（拿它一起算
// 会得出 0，于是整段该去的缩进一格都不去）。
func dedent(lines []string) []string {
	min := -1
	for _, l := range lines[1:] {
		if strings.TrimSpace(l) == "" {
			continue
		}
		n := len(l) - len(strings.TrimLeft(l, " "))
		if min < 0 || n < min {
			min = n
		}
	}
	if min <= 0 {
		return lines
	}
	for i := 1; i < len(lines); i++ {
		if len(lines[i]) >= min {
			lines[i] = lines[i][min:]
		} else {
			lines[i] = strings.TrimLeft(lines[i], " ")
		}
	}
	return lines
}
