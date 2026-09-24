package transcript

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testdata 里那两份是**合成的**，不是真机抓屏 —— 和 internal/composer 那边的做法刻意不同。
// 理由：转录里装的是人和 agent 的真实对话，而这个仓库是公开发布的（GitHub + npm）。
// 形状是照真机实测对齐的（字段名、嵌套、大小写、数组/字符串两种形态都覆盖了），
// 内容是编的。改这两份之前先看 docs/dev/CHAT.md §4 和 §9。

func read(t *testing.T, name, agent string) *Log {
	t.Helper()
	l, err := Read(Source{Path: filepath.Join("testdata", name), Agent: agent, Sig: "sig"}, 0)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	return l
}

// brief 把结果压成一行一条，方便整份比 —— 断言整条流比逐条挑字段更能挡住「顺序错了」
// 和「多认出一条」这两类错，而那两类正是解析器最容易出的。
func brief(l *Log) []string {
	out := make([]string, 0, len(l.Msgs))
	for _, m := range l.Msgs {
		s := string(m.Kind)
		switch {
		case m.Kind == KindTool:
			ok := "?"
			if m.OK != nil {
				if *m.OK {
					ok = "✓"
				} else {
					ok = "✕"
				}
			}
			s += fmt.Sprintf(" %s %s %s", ok, m.Tool, m.Meta)
		default:
			s += " " + oneLine(m.Text, 40)
		}
		out = append(out, s)
	}
	return out
}

func TestClaudeParse(t *testing.T) {
	got := brief(read(t, "claude.jsonl", "claude"))
	want := []string{
		"human 把发件箱缩成一行",
		"think 先看现在那四行长什么样",
		"agent 我先看一下现在的 Compose。",
		"tool ✓ Read /w/web/src/components/Compose.tsx",
		"tool ✕ Bash go test ./internal/outbox/",
		"tool ? Grep composeEnter", // 结果还没落盘 → 「正在跑」
		"human 继续",
		"human 继续", // **连着两条一样的不去重**：人真说了两遍
		"notice 被打断了",
		"agent 好，改完了。",
		"tool ? MysteryMCPTool 按键名排序取这个", // 认不出的工具退回按键名排序的第一个字符串字段
		// **agent 正在跑时打的字走队列**，而队列那条路不产生 `user` 行 —— 只认 user 的话
		// 人在 agent 干活时说的每一句都看不见（用户报的，实测一个会话里 24 条是这种）。
		// 后面那条 humanTurn=false 的（后台任务通知）和普通 attachment（注入的文件内容）都不算。
		"human 排队时打的这句话也要出现",
		// ⚠️ 下面这两条是**这个 bug 报第二次**之后钉的（见 claude.go 的 claudeQueued）：
		// `humanTurn` 只落在一部分人话上（实测 858 条里 138 条），原来拿它当唯一判据的话，
		// 另外那 720 条（84%）在 chat 里一条都不出现，而且完全静默。
		"human 没有 humanTurn 的那一版也要出现",
		// 三个字段全认不出时**按人话放过去** —— 宁可多显示一条机器消息（一眼看得见），
		// 也不要再来一次「人话静静消失」。中间那条 origin=task-notification 照旧挡在外面。
		"human 三个字段都没有时按人话放过去",
	}
	diff(t, want, got)
}

func TestCodexParse(t *testing.T) {
	got := brief(read(t, "codex.jsonl", "codex"))
	want := []string{
		"human 把折行判据放宽到四格",
		"agent 我先量一下两种 TUI 各让了几列。",
		"tool ✓ exec node web/src/term/paths.test.ts", // 剥掉了 /bin/zsh -lc 那两截
		"tool ✕ exec go test ./internal/composer/",
		"tool ? exec sleep 30", // in_progress + 没有 exit_code → 「正在跑」
		"tool ✓ edit paths.test.ts 等 2 个文件",
		"tool ? image /w/output/shot.png",
		"tool ? web.search wrap-ansi 切词规则",
		"notice 上下文被压缩了",
		"agent 四格容差加上了。",
	}
	diff(t, want, got)
}

// session_meta / response_item(developer) 那两行加起来上百字节的系统提示，一个字都不能漏出来。
// 漏了的表现是 chat 一打开先是一屏「You are Codex…」，把真正的对话顶到看不见的地方。
func TestCodexSkipsSystemPrompt(t *testing.T) {
	l := read(t, "codex.jsonl", "codex")
	for _, m := range l.Msgs {
		for _, bad := range []string{"You are Codex", "系统提示", "加密的"} {
			if strings.Contains(m.Text, bad) || strings.Contains(m.Meta, bad) {
				t.Fatalf("系统提示/加密内容漏进对话流了：%q", oneLine(m.Text+m.Meta, 80))
			}
		}
	}
}

// 子 agent 的话不进主对话流。
func TestClaudeSkipsSidechain(t *testing.T) {
	for _, m := range read(t, "claude.jsonl", "claude").Msgs {
		if strings.Contains(m.Text, "子 agent") {
			t.Fatal("sidechain 的内容漏进主对话流了")
		}
	}
}

// 增量读：从上次的 Next 接着读，只拿到新增那几条，而且**不重复**。
func TestReadIncremental(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.jsonl")
	line := func(uuid, text string) string {
		b, _ := json.Marshal(map[string]any{
			"type": "user", "uuid": uuid, "timestamp": "2026-09-21T02:00:00.000Z",
			"message": map[string]any{"role": "user", "content": text},
		})
		return string(b) + "\n"
	}
	if err := os.WriteFile(p, []byte(line("u1", "第一句")), 0o600); err != nil {
		t.Fatal(err)
	}
	src := Source{Path: p, Agent: "claude", Sig: "sig"}

	first, err := Read(src, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Msgs) != 1 || first.Msgs[0].Text != "第一句" {
		t.Fatalf("首屏不对：%v", brief(first))
	}

	// 什么都没写时再读一次：**没有新东西不是错**（agent 正在想，实测能 15 秒零字节）。
	again, err := Read(src, first.Next)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Msgs) != 0 {
		t.Fatalf("没写东西却读出了 %d 条", len(again.Msgs))
	}
	if again.Next != first.Next {
		t.Fatalf("偏移不该动：%d → %d", first.Next, again.Next)
	}

	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString(line("u2", "第二句"))
	f.Close()

	inc, err := Read(src, first.Next)
	if err != nil {
		t.Fatal(err)
	}
	if len(inc.Msgs) != 1 || inc.Msgs[0].Text != "第二句" {
		t.Fatalf("增量不对（应该只有第二句）：%v", brief(inc))
	}
}

// **半行绝不能算进 Next。** 文件正在被写时最后一行可能只落了一半，把它算进去的话
// 下次就从半行中间接着读 —— 那一条消息从此永远丢了，而且一个字都不报。
func TestReadHoldsBackPartialLine(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.jsonl")
	full := `{"type":"user","uuid":"u1","message":{"role":"user","content":"整的那行"}}` + "\n"
	half := `{"type":"user","uuid":"u2","message":{"role":"user","content":"落了一半的`
	if err := os.WriteFile(p, []byte(full+half), 0o600); err != nil {
		t.Fatal(err)
	}
	src := Source{Path: p, Agent: "claude", Sig: "sig"}
	l, err := Read(src, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Msgs) != 1 {
		t.Fatalf("半行被当成一条了：%v", brief(l))
	}
	if l.Next != int64(len(full)) {
		t.Fatalf("Next 应该停在整行的边界 %d，实际 %d", len(full), l.Next)
	}

	// 补齐那一行之后，从上次的 Next 接着读应该**完整**拿到它。
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString(`那行"}}` + "\n")
	f.Close()
	inc, err := Read(src, l.Next)
	if err != nil {
		t.Fatal(err)
	}
	if len(inc.Msgs) != 1 || !strings.Contains(inc.Msgs[0].Text, "落了一半的那行") {
		t.Fatalf("补齐之后没完整读到：%v", brief(inc))
	}
}

// 首屏从尾部开窗口：大文件只给最后那些，并且说明前面还有。
func TestReadTailWindow(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "big.jsonl")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	// 每行塞一段填充字节，好让总大小超过窗口（windowStart 是 1MB）。
	pad := strings.Repeat("x", 4000)
	for i := 0; i < 400; i++ {
		b, _ := json.Marshal(map[string]any{
			"type": "user", "uuid": fmt.Sprintf("u%d", i),
			"message": map[string]any{"role": "user", "content": fmt.Sprintf("第 %d 句 %s", i, pad)},
		})
		f.Write(append(b, '\n'))
	}
	f.Close()
	st, _ := os.Stat(p)
	if st.Size() < windowStart {
		t.Fatalf("这份测试文件得比窗口大才有意义：%d < %d", st.Size(), windowStart)
	}

	l, err := Read(Source{Path: p, Agent: "claude", Sig: "sig"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Msgs) > tailMsgs {
		t.Fatalf("首屏给了 %d 条，超过上限 %d", len(l.Msgs), tailMsgs)
	}
	if !l.More {
		t.Fatal("截过就得说 More，不然前端不知道上面还有")
	}
	// 尾部那条必须在：chat 打开时人要看的正是最后发生的事。
	last := l.Msgs[len(l.Msgs)-1]
	if !strings.HasPrefix(last.Text, "第 399 句") {
		t.Fatalf("最后一条不是文件尾那条：%q", oneLine(last.Text, 30))
	}
	if l.Next != st.Size() {
		t.Fatalf("Next 应该是文件尾 %d，实际 %d", st.Size(), l.Next)
	}
}

// 空文件、坏行、认不出的 agent：都得软失败，别 panic 也别把整份废掉。
func TestReadSoftFailures(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "e.jsonl")
	os.WriteFile(empty, nil, 0o600)
	if l, err := Read(Source{Path: empty, Agent: "claude"}, 0); err != nil || len(l.Msgs) != 0 {
		t.Fatalf("空文件应该给空结果：%v %v", l, err)
	}

	bad := filepath.Join(dir, "b.jsonl")
	os.WriteFile(bad, []byte("这不是 JSON\n{\"type\":\"user\",\"uuid\":\"u\",\"message\":{\"content\":\"好的那行\"}}\n"), 0o600)
	l, err := Read(Source{Path: bad, Agent: "claude"}, 0)
	if err != nil {
		t.Fatalf("一行坏字节不该让整份读不出来：%v", err)
	}
	if len(l.Msgs) != 1 || l.Msgs[0].Text != "好的那行" {
		t.Fatalf("坏行之后那条好行应该照旧读出来：%v", brief(l))
	}

	if _, err := Read(Source{Path: bad, Agent: "gemini"}, 0); err == nil {
		t.Fatal("认不出的 agent 应该报错")
	}
}

func diff(t *testing.T, want, got []string) {
	t.Helper()
	if len(want) == len(got) {
		same := true
		for i := range want {
			if want[i] != got[i] {
				same = false
				break
			}
		}
		if same {
			return
		}
	}
	var b strings.Builder
	b.WriteString("对话流不对：\n  想要的:\n")
	for _, s := range want {
		b.WriteString("    " + s + "\n")
	}
	b.WriteString("  实际的:\n")
	for _, s := range got {
		b.WriteString("    " + s + "\n")
	}
	t.Fatal(b.String())
}

// **窗口不许为了凑满 tailMsgs 而把整份文件读完。**
//
// 真踩过：一份 2.3MB 的转录，头一窗（1MB）出了 181 条，而门槛写成「凑满 200 条」，
// 于是一路翻倍读到 start == 0 —— 把整个文件读了。这是在跑着 agent 的那台机器上读十几兆，
// 正是这个窗口要避免的事，而且表现完全静默（结果是对的，只是慢且费）。
//
// 这条用例造一份「最后一窗里出得到 minTail 条但出不到 tailMsgs 条」的文件，
// 断言它**没有**回到文件头。
func TestReadTailWindowStopsEarly(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "mid.jsonl")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	// 每行 ~12KB，300 行 ≈ 3.6MB。最后 1MB 里大约 87 条：够 minTail(30)，不够 tailMsgs(200)。
	pad := strings.Repeat("y", 12000)
	for i := 0; i < 300; i++ {
		b, _ := json.Marshal(map[string]any{
			"type": "user", "uuid": fmt.Sprintf("u%d", i),
			"message": map[string]any{"role": "user", "content": fmt.Sprintf("第 %d 句 %s", i, pad)},
		})
		f.Write(append(b, '\n'))
	}
	f.Close()

	l, err := Read(Source{Path: p, Agent: "claude", Sig: "sig"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Msgs) < minTail {
		t.Fatalf("一窗该出得到至少 %d 条，实际 %d", minTail, len(l.Msgs))
	}
	if len(l.Msgs) >= 300 {
		t.Fatalf("把整份读完了（%d 条）—— 门槛又写成凑满 tailMsgs 了", len(l.Msgs))
	}
	if !l.More {
		t.Fatal("截过就得说 More")
	}
	// 最早那条不该在里面 —— 在的话说明窗口一路翻到了文件头。
	if strings.HasPrefix(l.Msgs[0].Text, "第 0 句") {
		t.Fatal("读到文件头了，窗口没起作用")
	}
}

// 往上翻：拿上一批的 Start 当 before，接着往前读一段，**不重复也不漏**。
func TestReadBefore(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.jsonl")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	// 每行 ~8KB，200 行 ≈ 1.6MB：比首屏那一窗（1MB）大，所以首屏会截断、More 为真。
	pad := strings.Repeat("z", 8000)
	for i := 0; i < 200; i++ {
		b, _ := json.Marshal(map[string]any{
			"type": "user", "uuid": fmt.Sprintf("u%d", i),
			"message": map[string]any{"role": "user", "content": fmt.Sprintf("第 %d 句 %s", i, pad)},
		})
		f.Write(append(b, '\n'))
	}
	f.Close()
	src := Source{Path: p, Agent: "claude", Sig: "sig"}

	head, err := Read(src, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !head.More || head.Start <= 0 {
		t.Fatalf("首屏该说「上面还有」并给出 Start：more=%v start=%d", head.More, head.Start)
	}

	// 往前翻一段。
	back, err := ReadBefore(src, head.Start)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Msgs) == 0 {
		t.Fatal("往前翻什么都没读到")
	}
	if back.Start >= head.Start {
		t.Fatalf("往前翻之后 Start 该更小：%d → %d", head.Start, back.Start)
	}
	// **接得上**：往前翻那批的最后一条，紧挨着首屏那批的第一条（按序号连续）。
	// 中间断一条的表现是「翻上去少了几句」，而屏幕上完全看不出来。
	lastBack := idxOf(t, back.Msgs[len(back.Msgs)-1].Text)
	firstHead := idxOf(t, head.Msgs[0].Text)
	if lastBack+1 != firstHead {
		t.Fatalf("两批接不上：往前那批最后是第 %d 句，首屏第一条是第 %d 句", lastBack, firstHead)
	}

	// 一路翻到头：Start 变 0、More 变假，再问一次给空结果（不是错）。
	cur := back.Start
	for i := 0; i < 40 && cur > 0; i++ {
		b, err := ReadBefore(src, cur)
		if err != nil {
			t.Fatal(err)
		}
		cur = b.Start
	}
	if cur != 0 {
		t.Fatalf("翻不到头（还剩 %d）", cur)
	}
	end, err := ReadBefore(src, 0)
	if err != nil {
		t.Fatalf("到头了再问一次不该报错：%v", err)
	}
	if len(end.Msgs) != 0 || end.More {
		t.Fatalf("到头了该给空结果：%d 条 more=%v", len(end.Msgs), end.More)
	}
}

// idxOf 从「第 N 句 …」里抠出那个序号。
func idxOf(t *testing.T, text string) int {
	t.Helper()
	var n int
	if _, err := fmt.Sscanf(text, "第 %d 句", &n); err != nil {
		t.Fatalf("认不出序号：%q", oneLine(text, 30))
	}
	return n
}

// **人话上的那几层壳要剥掉。** 都是真机截图里抓到的：
//
//	<pasted_content id="…">…</pasted_content id="…">   投稿进去的话 claude 当粘贴处理，套这个
//	<command-name>/clear</command-name> + message/args  斜杠命令记成一坨标签
//
// 原样显示的后果是每条人话前后各顶一行尖括号 —— 而在平板上说话投稿的人，消息几乎全是
// 粘贴进去的。顺带：剥壳之后投出去的原文和转录里记的才对得上，chat 那边的乐观回显
// 才撤得掉（不然屏幕上同一句话出现两次）。
func TestClaudeStripsWrappers(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.jsonl")
	line := func(uuid, text string) string {
		b, _ := json.Marshal(map[string]any{
			"type": "user", "uuid": uuid, "timestamp": "2026-09-21T02:00:00.000Z",
			"message": map[string]any{"role": "user", "content": text},
		})
		return string(b) + "\n"
	}
	body := line("u1", `<pasted_content id="13e3">工具调用如果有多条，默认收起来</pasted_content id="13e3">`)
	body += line("u2", "<command-name>/clear</command-name>\n            <command-message>clear</command-message>\n            <command-args></command-args>")
	body += line("u3", "<command-message>grill-me</command-message>\n<command-name>/grill-me</command-name>")
	body += line("u4", "<command-name>/loop</command-name>\n<command-args>5m /foo</command-args>")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	l, err := Read(Source{Path: p, Agent: "claude", Sig: "sig"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"工具调用如果有多条，默认收起来", "/clear", "/grill-me", "/loop 5m /foo"}
	if len(l.Msgs) != len(want) {
		t.Fatalf("该有 %d 条，实际 %d：%v", len(want), len(l.Msgs), brief(l))
	}
	for i, w := range want {
		if l.Msgs[i].Text != w {
			t.Errorf("第 %d 条该是 %q，实际 %q", i+1, w, l.Msgs[i].Text)
		}
	}
	// 一个尖括号标签都不许漏出去
	for _, m := range l.Msgs {
		if strings.Contains(m.Text, "<command") || strings.Contains(m.Text, "pasted_content") {
			t.Errorf("壳没剥干净：%q", m.Text)
		}
	}
}

// 「这一轮跑了多久 / 多少 token」。
//
// 两条判据最容易写错，都钉在这儿：
//   - **工具结果不能当成新一轮的起点**（它也是 user 角色，但 content 是数组）。
//     搞错的表现是屏幕上那个秒数永远停在几秒 —— 每条工具结果都把计时清零一次。
//   - **token 只算最后一条人话之后的**，前面那些是上一轮的。
func TestTurnStat(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.jsonl")
	now := time.Now().UTC()
	at := func(d time.Duration) string { return now.Add(d).Format(time.RFC3339) }

	human := func(uuid, text, ts string) string {
		b, _ := json.Marshal(map[string]any{
			"type": "user", "uuid": uuid, "timestamp": ts,
			"message": map[string]any{"role": "user", "content": text},
		})
		return string(b) + "\n"
	}
	assistant := func(uuid string, out int, effort, ts string) string {
		b, _ := json.Marshal(map[string]any{
			"type": "assistant", "uuid": uuid, "timestamp": ts, "effort": effort,
			"message": map[string]any{
				"role": "assistant", "model": "claude-opus-5",
				"content": []map[string]any{{"type": "text", "text": "说点什么"}},
				"usage":   map[string]any{"output_tokens": out},
			},
		})
		return string(b) + "\n"
	}
	toolResult := func(uuid, ts string) string {
		b, _ := json.Marshal(map[string]any{
			"type": "user", "uuid": uuid, "timestamp": ts,
			"message": map[string]any{"role": "user", "content": []map[string]any{{
				"type": "tool_result", "tool_use_id": "toolu_x", "content": "输出", "is_error": false,
			}}},
		})
		return string(b) + "\n"
	}

	body := human("u1", "上一轮的话", at(-10*time.Minute))
	body += assistant("a1", 9999, "high", at(-9*time.Minute)) // 上一轮的 token，不该算进来
	body += human("u2", "这一轮的话", at(-100*time.Second))
	body += assistant("a2", 1200, "xhigh", at(-90*time.Second))
	body += toolResult("t1", at(-80*time.Second)) // **不是新一轮的起点**
	body += assistant("a3", 445, "xhigh", at(-10*time.Second))
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := TurnStat(Source{Path: p, Agent: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("该算得出来")
	}
	if got.Tokens != 1645 {
		t.Errorf("token 该只算这一轮的 1200+445=1645，实际 %d（9999 是上一轮的）", got.Tokens)
	}
	// 起点是 u2（100 秒前），不是那条工具结果（80 秒前）
	if got.Secs < 95 || got.Secs > 115 {
		t.Errorf("该是 100 秒上下，实际 %d —— 落在 80 附近就是把工具结果当成了新一轮的起点", got.Secs)
	}
	if got.Effort != "xhigh" || got.Model != "claude-opus-5" {
		t.Errorf("effort/model 不对：%+v", got)
	}
}

// 回扫窗口里找不到那条人话时**什么都不给**（不是给一个偏小的数 —— 那比没有更误导）。
func TestTurnStatGivesUpWhenTurnTooLong(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "long.jsonl")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	// 只有 assistant 行，没有人话，而且塞到比 turnCap 还大
	pad := strings.Repeat("q", 20000)
	for i := 0; i < 160; i++ {
		b, _ := json.Marshal(map[string]any{
			"type": "assistant", "uuid": fmt.Sprintf("a%d", i), "timestamp": time.Now().UTC().Format(time.RFC3339),
			"message": map[string]any{
				"role":    "assistant",
				"content": []map[string]any{{"type": "text", "text": pad}},
				"usage":   map[string]any{"output_tokens": 100},
			},
		})
		f.Write(append(b, '\n'))
	}
	f.Close()
	st, _ := os.Stat(p)
	if st.Size() <= turnCap {
		t.Fatalf("这份测试文件得比回扫上限大才有意义：%d <= %d", st.Size(), turnCap)
	}
	got, err := TurnStat(Source{Path: p, Agent: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("找不到起点该给 nil，实际 %+v（那个 token 数是从窗口起点算的，偏小）", got)
	}
}

// 答过的提问要带出**当时选了哪个**。
//
// 不带的话往上翻历史看到的是一排干巴巴的选项，「当时到底定了哪个」还得回终端翻（用户报的）。
// 两个来源都测：结构化那份（`toolUseResult.answers`）和退路那句
// （`tool_result.content` 里的 `"问题"="选项"`）。
func TestClaudeAskPicked(t *testing.T) {
	dir := t.TempDir()
	q := "那份文档在哪儿？"
	ask, _ := json.Marshal(map[string]any{
		"type": "assistant", "uuid": "a1", "timestamp": "2026-09-21T02:00:00.000Z",
		"message": map[string]any{"role": "assistant", "content": []map[string]any{{
			"type": "tool_use", "id": "toolu_ask", "name": "AskUserQuestion",
			"input": map[string]any{"questions": []map[string]any{{
				"header": "文档", "question": q, "options": []map[string]any{
					{"label": "第一个"}, {"label": "第二个"}, {"label": "第三个"},
				},
			}}},
		}}},
	})

	// ① 结构化那份
	structured, _ := json.Marshal(map[string]any{
		"type": "user", "uuid": "u1", "timestamp": "2026-09-21T02:00:01.000Z",
		"message": map[string]any{"role": "user", "content": []map[string]any{{
			"type": "tool_result", "tool_use_id": "toolu_ask", "content": "答过了",
		}}},
		"toolUseResult": map[string]any{"answers": map[string]any{q: "第二个"}},
	})
	// ② 只有那句话（老版本 / 拿不到结构化）
	sentence := `Your questions have been answered: "` + q + `"="第三个". You can now continue.`
	plain, _ := json.Marshal(map[string]any{
		"type": "user", "uuid": "u1", "timestamp": "2026-09-21T02:00:01.000Z",
		"message": map[string]any{"role": "user", "content": []map[string]any{{
			"type": "tool_result", "tool_use_id": "toolu_ask", "content": sentence,
		}}},
	})
	// ③ 两个都没有 → 留空（不编一个出来）
	bare, _ := json.Marshal(map[string]any{
		"type": "user", "uuid": "u1", "timestamp": "2026-09-21T02:00:01.000Z",
		"message": map[string]any{"role": "user", "content": []map[string]any{{
			"type": "tool_result", "tool_use_id": "toolu_ask", "content": "没说选了什么",
		}}},
	})

	for _, tc := range []struct {
		why    string
		result []byte
		want   string
	}{
		{"结构化那份", structured, "第二个"},
		{"退回那句话", plain, "第三个"},
		{"两个都没有", bare, ""},
	} {
		p := filepath.Join(dir, "s.jsonl")
		if err := os.WriteFile(p, append(append(ask, '\n'), append(tc.result, '\n')...), 0o600); err != nil {
			t.Fatal(err)
		}
		l, err := Read(Source{Path: p, Agent: "claude", Sig: "sig"}, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(l.Msgs) != 1 || l.Msgs[0].Ask == nil {
			t.Fatalf("%s：该有一条带 ask 的：%+v", tc.why, l.Msgs)
		}
		got := l.Msgs[0].Ask.Questions[0].Picked
		if got != tc.want {
			t.Errorf("%s：picked 该是 %q，实际 %q", tc.why, tc.want, got)
		}
		// 答过了这件事照旧要认出来（OK 回填），不然界面上还当它在等你答
		if l.Msgs[0].OK == nil {
			t.Errorf("%s：该认出「答过了」", tc.why)
		}
	}
}

// **往前翻绝不能原地打转。**
//
// 真机上踩到的（6.3MB 的转录）：第二页回来的 `start` 和问过去的 `before` 一模一样、0 条，
// 前端拿它再问一次，永远停在那儿 —— 表现和「翻不到历史」一模一样。
//
// 原因是那一窗整个落在**一行超长的行**里（转录里有几百 KB 一行的工具输出 / 附件）：窗口
// 起点按字节切、落在行中间，那半行整条丢掉，于是这一窗一条都没有、偏移也没往前走。
//
// 这条用例在中间塞一行比 backWindow 还大的行，然后一路往前翻到头，断言**每一页都有进展**。
func TestReadBeforeAlwaysMakesProgress(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "huge.jsonl")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	line := func(i int, pad int) []byte {
		b, _ := json.Marshal(map[string]any{
			"type": "user", "uuid": fmt.Sprintf("u%d", i),
			"message": map[string]any{"role": "user", "content": fmt.Sprintf("第 %d 句 %s", i, strings.Repeat("p", pad))},
		})
		return append(b, '\n')
	}
	for i := 0; i < 20; i++ {
		f.Write(line(i, 2000))
	}
	// 一行顶掉好几个窗口（backWindow 是 256KB）
	f.Write(line(999, backWindow*3))
	for i := 20; i < 40; i++ {
		f.Write(line(i, 2000))
	}
	f.Close()
	src := Source{Path: p, Agent: "claude", Sig: "sig"}

	head, err := Read(src, 0)
	if err != nil {
		t.Fatal(err)
	}
	cur := head.Start
	for i := 0; cur > 0; i++ {
		if i > 40 {
			t.Fatalf("翻了 40 页还没到头（卡在 %d）—— 又原地打转了", cur)
		}
		b, err := ReadBefore(src, cur)
		if err != nil {
			t.Fatal(err)
		}
		if b.Start >= cur {
			t.Fatalf("第 %d 页没有进展：before=%d 回来的 start=%d（前端会拿它无限重问）", i+1, cur, b.Start)
		}
		cur = b.Start
	}
	if cur != 0 {
		t.Fatalf("最后该停在 0，实际 %d", cur)
	}
}

// 「还有几个后台任务在跑」—— 判据是转录里那两个标记（见 shells.go）。
//
// 这条最要紧的是**通知之前不能算完**，以及**增量扫不能漏**：漏报看着就像这个功能没做。
func TestShells(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.jsonl")
	st := &Store{ClaudeRoot: filepath.Join(dir, "p")}
	src := Source{Path: p, Agent: "claude"}

	start := func(id string) string {
		b, _ := json.Marshal(map[string]any{
			"type": "user", "uuid": "u" + id,
			"message":       map[string]any{"role": "user", "content": []map[string]any{{"type": "tool_result", "tool_use_id": "t" + id}}},
			"toolUseResult": map[string]any{"backgroundTaskId": id},
		})
		return string(b) + "\n"
	}
	// **字面**那种（真机上的转录就是这个形态 —— 那一行不是 Go 写的）
	notify := func(id string) string {
		return `{"type":"queue-operation","operation":"enqueue","content":"<task-notification>\n<task-id>` +
			id + `</task-id>\n</task-notification>"}` + "\n"
	}
	// **转义**那种：Go 的 json.Marshal 默认把 `<` `>` 转成 \u003c —— 别的实现也可能这么写，
	// 只认字面那种的后果是完成通知静默认不出来，那个任务永远挂在「还在跑」上
	notifyEsc := func(id string) string {
		b, _ := json.Marshal(map[string]any{
			"type": "queue-operation", "operation": "enqueue",
			"content": "<task-notification>\n<task-id>" + id + "</task-id>\n</task-notification>",
		})
		return string(b) + "\n"
	}
	write := func(body string) {
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	body := start("aaa111")
	write(body)
	if n := st.Shells(src); n != 1 {
		t.Fatalf("启动一个该报 1，实际 %d", n)
	}

	// 再起一个（**增量**扫：只读新长出来的那段，不能漏）
	body += start("bbb222")
	write(body)
	if n := st.Shells(src); n != 2 {
		t.Fatalf("两个在跑该报 2，实际 %d", n)
	}

	// 第一个完成了
	body += notify("aaa111")
	write(body)
	if n := st.Shells(src); n != 1 {
		t.Fatalf("一个完成后该报 1，实际 %d", n)
	}

	// 同一个 id 的通知会出现好几次（enqueue / remove / attachment），不能算成负数
	body += notify("aaa111") + notify("aaa111")
	write(body)
	if n := st.Shells(src); n != 1 {
		t.Fatalf("重复通知不该影响，实际 %d", n)
	}

	// 第二个用**转义形态**的通知（同样要认出来）
	body += notifyEsc("bbb222")
	write(body)
	if n := st.Shells(src); n != 0 {
		t.Fatalf("都完成了该报 0（第二个是转义形态的通知），实际 %d", n)
	}

	// 文件变短（截断过）→ 记账作废、从头重扫，别拿旧账继续算
	write(start("ccc333"))
	if n := st.Shells(src); n != 1 {
		t.Fatalf("截断重写之后该按新内容算（1），实际 %d", n)
	}

	// codex 一律 0（它的后台机制不是这一套，编一个数比不显示更糟）
	if n := st.Shells(Source{Path: p, Agent: "codex"}); n != 0 {
		t.Fatalf("codex 该给 0，实际 %d", n)
	}
}

// **标记出现在别人的文字里不算。**
//
// `pick` 故意不解析 JSON（为了便宜：一整份转录只做几遍子串查找），代价就是这个 —— 比如
// shells.go 那段注释本身要是被复述进了转录，`"backgroundTaskId"` 就会被认成一次启动。
// 靠 id 的形状（只认 `[a-z0-9]`）把这种排掉。
//
// 单开一个 Store：这条测的是标记形状，不是缓存作废。
func TestShellsIgnoresProse(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.jsonl")
	st := &Store{}
	for _, body := range []string{
		// JSON 字符串里被转义的引号：`\"backgroundTaskId\"` 不等于 `"backgroundTaskId"`
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"判据是 \"backgroundTaskId\": 那个字段"}]}}`,
		// id 位置上不是那个形状（省略号 / 带空格 / 带引号）
		`{"toolUseResult":{"backgroundTaskId":"不是 id"}}`,
		`{"content":"<task-id>…</task-id>"}`,
		// 工具**定义**里的说明文字（真转录里就有，见 CLAUDE.md 那条）
		`{"type":"attachment","attachment":{"type":"file","content":"run_in_background 把命令放到后台"}}`,
	} {
		if err := os.WriteFile(p, []byte(body+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		fresh := &Store{}
		if n := fresh.Shells(Source{Path: p, Agent: "claude"}); n != 0 {
			t.Errorf("该是 0，实际 %d：%s", n, body[:60])
		}
	}
	_ = st
}

// **跨批的结果必须靠 Updates 带出来。**
//
// 回填靠「tool_use_id → 这一次扫描里的下标」，而扫描状态是每批新建的 —— 流式过程中工具调用
// 落在前一批、结果落在后一批，那时回填找不到它。表现不是「少一点信息」，是**显示的状态和
// 事实相反**：工具永远「正在跑」、提问那张卡永远「没选」，只有刷新页面才对（用户报的）。
func TestUpdatesCrossBatch(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.jsonl")
	q := "选哪个？"
	askLine, _ := json.Marshal(map[string]any{
		"type": "assistant", "uuid": "a1", "timestamp": "2026-09-21T02:00:00.000Z",
		"message": map[string]any{"role": "assistant", "content": []map[string]any{{
			"type": "tool_use", "id": "toolu_ask", "name": "AskUserQuestion",
			"input": map[string]any{"questions": []map[string]any{{
				"question": q, "options": []map[string]any{{"label": "甲"}, {"label": "乙"}},
			}}},
		}}},
	})
	bashLine, _ := json.Marshal(map[string]any{
		"type": "assistant", "uuid": "a2", "timestamp": "2026-09-21T02:00:01.000Z",
		"message": map[string]any{"role": "assistant", "content": []map[string]any{{
			"type": "tool_use", "id": "toolu_bash", "name": "Bash",
			"input": map[string]any{"command": "true"},
		}}},
	})
	// 第一批：两条工具调用，都还没有结果
	if err := os.WriteFile(p, append(append(askLine, '\n'), append(bashLine, '\n')...), 0o600); err != nil {
		t.Fatal(err)
	}
	src := Source{Path: p, Agent: "claude", Sig: "sig"}
	first, err := Read(src, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Msgs) != 2 {
		t.Fatalf("该有两条工具行：%v", brief(first))
	}
	for _, m := range first.Msgs {
		if m.OK != nil {
			t.Fatalf("还没有结果，OK 该是 nil：%+v", m)
		}
		if m.Ref == "" {
			t.Fatalf("工具行必须带 Ref，不然后面的结果认不回来：%+v", m)
		}
	}
	if len(first.Updates) != 0 {
		t.Fatalf("这一批里没有结果行，不该有补丁：%v", first.Updates)
	}

	// 第二批：两条结果（提问答了「乙」，Bash 成功）
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600)
	res1, _ := json.Marshal(map[string]any{
		"type": "user", "uuid": "u1", "timestamp": "2026-09-21T02:00:02.000Z",
		"message": map[string]any{"role": "user", "content": []map[string]any{{
			"type": "tool_result", "tool_use_id": "toolu_ask", "content": "答过了",
		}}},
		"toolUseResult": map[string]any{"answers": map[string]any{q: "乙"}},
	})
	res2, _ := json.Marshal(map[string]any{
		"type": "user", "uuid": "u2", "timestamp": "2026-09-21T02:00:03.000Z",
		"message": map[string]any{"role": "user", "content": []map[string]any{{
			"type": "tool_result", "tool_use_id": "toolu_bash", "content": "ok", "is_error": false,
		}}},
	})
	f.Write(append(res1, '\n'))
	f.Write(append(res2, '\n'))
	f.Close()

	inc, err := Read(src, first.Next)
	if err != nil {
		t.Fatal(err)
	}
	if len(inc.Msgs) != 0 {
		t.Fatalf("结果行本身不该变成消息：%v", brief(inc))
	}
	if len(inc.Updates) != 2 {
		t.Fatalf("该有两条补丁，实际 %d：%+v", len(inc.Updates), inc.Updates)
	}
	byRef := map[string]Update{}
	for _, u := range inc.Updates {
		byRef[u.Ref] = u
	}
	if u := byRef["toolu_bash"]; u.OK == nil || !*u.OK {
		t.Fatalf("Bash 那条该带 OK=true：%+v", u)
	}
	if u := byRef["toolu_ask"]; u.Answers[q] != "乙" {
		t.Fatalf("提问那条该带上答案：%+v", u)
	}

	// 整份重读时照旧在同一批里回填好（补丁只是跨批那条路）
	full, err := Read(src, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range full.Msgs {
		if m.OK == nil {
			t.Fatalf("整份重读时该已经回填：%+v", m)
		}
		if m.Ask != nil && m.Ask.Questions[0].Picked != "乙" {
			t.Fatalf("整份重读时 picked 该是「乙」：%+v", m.Ask.Questions[0])
		}
	}
}

// 上下文压缩那一段，两条都是真机上踩出来的：
//
//   - **提到 `<command-name>` 的人话不许被吞掉。** 压缩后那份 summary 里正好写着这串标签
//     （记的就是「命令壳要剥掉」这个坑本身），而剥壳那一下是「整条换成命令名」——
//     不锚在开头的话，几万字的 summary 在对话流里变成一个孤零零的 `/clear`（用户报的）。
//     这个错的方向特别糟：不是少显示，而是**显示出一件没发生过的事**。
//   - **压缩本身要有个交代**，而那份 summary 不算人话（它以 user 角色记着）。
func TestClaudeCompactBoundary(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.jsonl")
	raw := func(m map[string]any) string {
		m["timestamp"] = "2026-09-21T02:00:00.000Z"
		b, _ := json.Marshal(m)
		return string(b) + "\n"
	}
	user := func(uuid, text string, extra map[string]any) string {
		m := map[string]any{
			"type": "user", "uuid": uuid,
			"message": map[string]any{"role": "user", "content": text},
		}
		for k, v := range extra {
			m[k] = v
		}
		return raw(m)
	}
	// 那份 summary 的真实形状：user 角色、带 isCompactSummary、正文里提到了命令壳。
	summary := "This session is being continued from a previous conversation.\n\n" +
		"4. Errors and fixes:\n   - `<command-name>/clear</command-name>` 被原样显示了 → 压成 `/clear`"

	body := user("u1", "把登录页的表单校验补上", nil)
	body += raw(map[string]any{
		"type": "system", "subtype": "compact_boundary", "uuid": "b1",
		"content": "Conversation compacted",
	})
	body += user("u2", summary, map[string]any{"isCompactSummary": true, "isVisibleInTranscriptOnly": true})
	// 真的敲了 /clear 那条照旧要压成一行
	body += user("u3", "<command-name>/clear</command-name>\n<command-args></command-args>", nil)
	// 人自己**引用**这串标签：原样留着，一个字都不许改
	quote := "我们把 `<command-name>/clear</command-name>` 原样显示了，得剥掉"
	body += user("u4", quote, nil)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	l, err := Read(Source{Path: p, Agent: "claude", Sig: "sig"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	type want struct {
		kind Kind
		text string
	}
	exp := []want{
		{KindHuman, "把登录页的表单校验补上"},
		{KindNotice, "上下文被压缩了"},
		{KindHuman, "/clear"},
		{KindHuman, quote},
	}
	if len(l.Msgs) != len(exp) {
		t.Fatalf("该有 %d 条，实际 %d：%v", len(exp), len(l.Msgs), brief(l))
	}
	for i, w := range exp {
		got := l.Msgs[i]
		if got.Kind != w.kind || got.Text != w.text {
			t.Errorf("第 %d 条该是 %s/%q，实际 %s/%q", i+1, w.kind, w.text, got.Kind, got.Text)
		}
	}
}

// **数组 content 不等于「这是工具结果」。** 人说的话只要带了附件（贴图）就也是数组，
// 原来这儿只捞 `tool_result`，于是带图的人话一个字都不进对话流（用户报的：
// 「我在电脑上终端发的这条，手机 chat 里看不到」，而纯文字那几条好端端在），完全静默。
//
// 同一个根因还坑掉了打断记号：它**只以数组形态出现**（这台机器上的转录实测「字符串 0 条 /
// 数组 24 条」），所以那行「被打断了」的小字一次都没出现过。
func TestClaudeArrayContentIsNotAlwaysToolResult(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.jsonl")
	row := func(uuid string, content any) string {
		b, _ := json.Marshal(map[string]any{
			"type": "user", "uuid": uuid, "timestamp": "2026-09-21T02:00:00.000Z",
			"message": map[string]any{"role": "user", "content": content},
		})
		return string(b) + "\n"
	}
	img := map[string]any{"type": "image", "source": map[string]any{
		"type": "base64", "media_type": "image/png", "data": "iVBORw0KGgo=",
	}}

	body := row("u1", "纯文字那条")
	// 带图的人话：正文里 claude 自己留了 `[Image #10]` 这个记号
	body += row("u2", []any{map[string]any{"type": "text", "text": "[Image #10] 这条是在终端里发的"}, img})
	// 顺序反过来也要认（实测 claude 把 image 放前面、codex 也是）
	body += row("u3", []any{img, map[string]any{"type": "text", "text": "图在前面那条"}})
	// 只贴图、一个字没打
	body += row("u4", []any{img})
	// 打断记号：数组形态
	body += row("u5", []any{map[string]any{"type": "text", "text": "[Request interrupted by user]"}})
	// 工具结果照旧不是人话
	body += row("u6", []any{map[string]any{"type": "tool_result", "tool_use_id": "t1", "content": "输出"}})
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	l, err := Read(Source{Path: p, Agent: "claude", Sig: "sig"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	type want struct {
		kind Kind
		text string
	}
	exp := []want{
		{KindHuman, "纯文字那条"},
		{KindHuman, "[Image #10] 这条是在终端里发的"},
		{KindHuman, "图在前面那条"},
		{KindHuman, "[图片]"},
		{KindNotice, "被打断了"},
	}
	if len(l.Msgs) != len(exp) {
		t.Fatalf("该有 %d 条，实际 %d：%v", len(exp), len(l.Msgs), brief(l))
	}
	for i, w := range exp {
		if l.Msgs[i].Kind != w.kind || l.Msgs[i].Text != w.text {
			t.Errorf("第 %d 条该是 %s/%q，实际 %s/%q", i+1, w.kind, w.text, l.Msgs[i].Kind, l.Msgs[i].Text)
		}
	}
	// 图片本身绝不能带出去：一张贴图是几百 KB 的 base64，而这条路按秒轮询、要过隧道到手机上
	for _, m := range l.Msgs {
		if strings.Contains(m.Text, "iVBORw0KGgo") {
			t.Errorf("把图片数据带出去了：%q", m.Text)
		}
	}
}

// codex 那边贴图是 `local_image` + **路径**（不是 base64），而 cxText 本来就只挑文本块，
// 所以带文字那种原本就对；这儿钉的是「只贴图不打字」别整条消失。
func TestCodexImageOnly(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "r.jsonl")
	row := func(id string, content []any) string {
		b, _ := json.Marshal(map[string]any{
			"timestamp": "2026-09-21T03:00:00.000Z", "type": "event_msg",
			"payload": map[string]any{"type": "item_completed", "item": map[string]any{
				"type": "UserMessage", "id": id, "content": content,
			}},
		})
		return string(b) + "\n"
	}
	shot := map[string]any{"type": "local_image", "path": "/tmp/codex-clipboard-x.png"}
	body := row("m1", []any{shot, map[string]any{"type": "text", "text": "[Image #1] 这种"}})
	body += row("m2", []any{shot})
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := Read(Source{Path: p, Agent: "codex", Sig: "sig"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"[Image #1] 这种", "[图片]"}
	if len(l.Msgs) != len(want) {
		t.Fatalf("该有 %d 条，实际 %d：%v", len(want), len(l.Msgs), brief(l))
	}
	for i, w := range want {
		if l.Msgs[i].Kind != KindHuman || l.Msgs[i].Text != w {
			t.Errorf("第 %d 条该是 human/%q，实际 %s/%q", i+1, w, l.Msgs[i].Kind, l.Msgs[i].Text)
		}
	}
}

// 机器注入的那几种块（以 user 角色记着，但不是人说的话）。用户报的是后台任务通知
// —— `<task-notification><task-id>…` 原样占了整屏一个气泡，而人要看的只有那句 summary。
//
// **判据是标签名白名单**：人话里真的有 `<https://…>` 这种写法，一刀切会把它吃掉
// （和 cmdHead 那条锚定同一个教训），所以这儿专门有一条钉它。
func TestClaudeMachineBlocks(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.jsonl")
	row := func(uuid, text string) string {
		b, _ := json.Marshal(map[string]any{
			"type": "user", "uuid": uuid, "timestamp": "2026-09-21T02:00:00.000Z",
			"message": map[string]any{"role": "user", "content": text},
		})
		return string(b) + "\n"
	}
	note := "<task-notification>\n<task-id>b79kmrnjb</task-id>\n" +
		"<tool-use-id>toolu_01YBHFaHTm2vpWqWwRBegF9X</tool-use-id>\n" +
		"<output-file>/private/tmp/x/tasks/b79kmrnjb.output</output-file>\n" +
		"<status>completed</status>\n" +
		"<summary>Background command \"Watch the babbage build\" completed (exit code 0)</summary>\n" +
		"</task-notification>"
	body := row("u1", note)
	// 形状变了（没有 summary）也别退回显示标签
	body += row("u2", "<task-notification><task-id>zz</task-id><status>failed</status></task-notification>")
	// 斜杠命令的输出里真的带 ANSI（实测 `/permissions` 回的就是这样），要剥掉
	body += row("u3", "<local-command-stdout>Set model to \x1b[1mOpus 5\x1b[22m</local-command-stdout>")
	body += row("u4", "<bash-input>ls -la /tmp</bash-input>")
	body += row("u5", "<bash-stdout></bash-stdout><bash-stderr>boom</bash-stderr>")
	// **这条是真的人话**，一个字都不许动
	url := "<https://maersk.longbridge-inc.com/application/v2/core> 这个地址"
	body += row("u6", url)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	l, err := Read(Source{Path: p, Agent: "claude", Sig: "sig"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	type want struct {
		kind Kind
		text string
	}
	exp := []want{
		{KindNotice, `后台任务跑完了：Background command "Watch the babbage build" completed (exit code 0)`},
		{KindNotice, "后台任务failed：zz failed"},
		{KindNotice, "Set model to Opus 5"},
		{KindHuman, "! ls -la /tmp"},
		{KindNotice, "boom"},
		{KindHuman, url},
	}
	if len(l.Msgs) != len(exp) {
		t.Fatalf("该有 %d 条，实际 %d：%v", len(exp), len(l.Msgs), brief(l))
	}
	for i, w := range exp {
		if l.Msgs[i].Kind != w.kind || l.Msgs[i].Text != w.text {
			t.Errorf("第 %d 条该是 %s/%q，实际 %s/%q", i+1, w.kind, w.text, l.Msgs[i].Kind, l.Msgs[i].Text)
		}
	}
	// 一个标签都不许漏到屏幕上（除了那条真人话里的 URL）
	for i, m := range l.Msgs {
		if i == len(l.Msgs)-1 {
			continue
		}
		if strings.Contains(m.Text, "<task-") || strings.Contains(m.Text, "<bash-") || strings.Contains(m.Text, "<local-") {
			t.Errorf("第 %d 条漏了标签：%q", i+1, m.Text)
		}
	}
}
