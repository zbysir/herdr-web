package transcript

import (
	"encoding/json"
	"regexp"
	"strings"
)

// claude 的转录：`~/.claude/projects/<cwd 变形>/<session-uuid>.jsonl`。
//
// 一行一个完整 JSON，每行自带完整上下文（`cwd` / `gitBranch` / `version`），所以捡起任意
// 一行都能还原环境 —— 这也是为什么从文件中间开窗口读是安全的。
//
// 实测某个 298 行会话的行类型分布（docs/dev/CHAT.md §4）：
//
//	attachment 117   system-reminder、注入的文件内容 —— **不是对话**，跳过
//	assistant   70   模型输出，一行一个 content block
//	user        35   人说的话 **+ 工具结果**（见下面第 ② 条）
//	permission-mode / mode / last-prompt / atis-latch 各 14   状态快照，取最后一条即可，这儿全跳
//	ai-title    13   herdr tab 上那个标题的来源
//
// 这一层只认 `user` 和 `assistant` 两种，其余一律跳过 —— 「多认一种」的代价是对话流里
// 混进一堆不是人说的话，而那正是 chat 模式要解决的问题。

// 认出来的类型。写成常量是因为漏一个字母的表现是**整类消息静默消失**。
const (
	clTypeUser       = "user"
	clTypeAssistant  = "assistant"
	clTypeAttachment = "attachment"
	clTypeSystem     = "system"
	// clCompactBoundary 上下文压缩在转录里留下的那条记号（`type:"system"`）。
	// 紧跟着的那条 user 行就是那份 summary（`isCompactSummary`）。
	clCompactBoundary = "compact_boundary"
)

// pastedWrap 是「粘贴进来的内容」那层壳。
//
// claude 把人粘贴的那段话包在 `<pasted_content id="8e39">…</pasted_content id="8e39">` 里
// （注意闭合标签里也带 id，不是标准的 XML 写法）。**那不是人打的字**，是记录用的包装 ——
// 原样显示在对话流里就是每条人话前后各顶一行尖括号，而在平板上说话投稿的人，
// 消息几乎全是这种（真机截图里抓到的）。
//
// 只剥壳、留里面的内容：那才是人说的话。开闭两种都认，写成一个正则（`/?` 那一处）。
var pastedWrap = regexp.MustCompile(`</?pasted_content(?:\s+[^>]*)?>`)

// 斜杠命令那层壳。
//
// 人在 claude 里敲 `/clear`，转录里记下来的是一坨标签（实测两种顺序都有）：
//
//	<command-name>/clear</command-name>
//	            <command-message>clear</command-message>
//	            <command-args></command-args>
//
// 原样显示在对话流里就是一屏尖括号（真机截图里抓到的），而人要看的只有一句 `/clear`。
// 所以认出 `command-name` 就把整条压成「命令 + 参数」，其余标签丢掉。
//
// ⚠️ **判据必须锚在开头**（`cmdHead`）—— 「压成一行」是把**整条消息**扔掉换成命令名，
// 所以不锚的话，任何**提到**这串标签的人话都会被整条吃掉。真踩过一次：上下文压缩后
// 那条几万字的 summary 里正好写着 `<command-name>/clear</command-name>`（记的就是这个坑
// 本身），于是整条 summary 在对话流里变成一个孤零零的 `/clear` 气泡（用户报的）。
// 这类错的方向很糟：不是少显示一点东西，而是**显示出一件没发生过的事**。
var (
	// cmdHead 「这条消息**就是**一个斜杠命令的壳」。宽在两头：前面容空白，标签名不写死。
	cmdHead = regexp.MustCompile(`^\s*<command-[a-z]+>`)
	cmdName = regexp.MustCompile(`<command-name>([^<]*)</command-name>`)
	cmdArgs = regexp.MustCompile(`<command-args>([^<]*)</command-args>`)
	// cmdAny 兜底：上面两个没命中但还剩别的 `<command-*>` 标签时，至少别把标签露出去。
	cmdAny = regexp.MustCompile(`</?command-[a-z]+>`)
)

// slashCmd 把那坨标签压成一行 `/clear`（带参数就是 `/foo 参数`）。
// 不是斜杠命令就返回空串，调用方照原文走。
func slashCmd(s string) string {
	if !cmdHead.MatchString(s) {
		return "" // 只是提到了这串标签，不是命令本身 —— 别把整条消息吃掉（见上）
	}
	m := cmdName.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	out := strings.TrimSpace(m[1])
	if out == "" {
		return ""
	}
	if a := cmdArgs.FindStringSubmatch(s); a != nil {
		if arg := strings.TrimSpace(a[1]); arg != "" {
			out += " " + arg
		}
	}
	return out
}

// interruptMark 是 ESC 打断留下的记号。
//
// **按 ESC 打断不触发任何 hook**（docs/dev/CHAT.md §3.5），所以这是唯一认得出「人把它掐了」
// 的地方。不认的话对话流里看到的是「agent 说了半句就没了」，而屏幕上其实写着被打断了。
const interruptMark = "[Request interrupted by user]"

type clLine struct {
	Type        string          `json:"type"`
	UUID        string          `json:"uuid"`
	Timestamp   string          `json:"timestamp"`
	IsSidechain bool            `json:"isSidechain"`
	IsMeta      bool            `json:"isMeta"`
	Message     json.RawMessage `json:"message"`
	// Subtype 只有 `type == "system"` 那种行有（`compact_boundary` 是我们要的那一个）。
	Subtype string `json:"subtype"`
	// IsCompactSummary 这条 user 行是**压缩后那份 summary**，不是人说的话。
	//
	// 判据用这个字段而不是嗅正文开头那句英文（"This session is being continued…"）：
	// 那句话是宿主的措辞，改了我们就会把一份几万字的 summary 当人话显示出来。
	// 同一行上还有 `isVisibleInTranscriptOnly`，意思一致 —— claude 自己的界面也不把它
	// 摊在对话流里。
	IsCompactSummary bool `json:"isCompactSummary"`
	// ToolUseResult 是工具的**结构化**结果（和 message.content 里那条 tool_result 并存）。
	// 提问那种工具靠它拿「人选了哪个」，见 fillPicked。
	ToolUseResult json.RawMessage `json:"toolUseResult"`
	Attachment    *clAttachment   `json:"attachment"`
}

// clAttachment 是注入进对话的那些东西。绝大多数**不是对话**（system-reminder、文件内容），
// 但**排队发的人话也走这条路**，见 parseClaude 的第 ④ 条。
type clAttachment struct {
	Type string `json:"type"`
	// Prompt 排队那条人话的原文（`type == "queued_command"` 时）
	Prompt string `json:"prompt"`
	// HumanTurn 这条是不是人说的。**判据用它**，别去嗅 prompt 的开头 ——
	// 后台任务完成的通知也走 queued_command 这条路（那些是机器发的）。
	HumanTurn bool `json:"humanTurn"`
}

type clBlock struct {
	Type string `json:"type"`
	// text / thinking
	Text     string `json:"text"`
	Thinking string `json:"thinking"`
	// tool_use
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
	// tool_result
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

func parseClaude(line []byte, st *state, out *[]Msg) {
	var l clLine
	if json.Unmarshal(line, &l) != nil {
		return // 半行 / 坏行，跳过。别让一行坏字节把整份对话废掉
	}
	// **子 agent（sidechain）不进主对话流。** 一次 Task 调用里子 agent 自己也写几十行，
	// 混进来的表现是「对话里突然冒出一段没头没尾的活」，而主线上只是一条 Task 工具调用。
	// 子 agent（sidechain）不进主对话流；isMeta 是状态快照那类。
	// **注意 attachment 不在这道闸后面被放过** —— 排队的人话正是 attachment，见 ④。
	if l.IsSidechain || l.IsMeta {
		return
	}
	switch l.Type {
	case clTypeUser:
		claudeUser(&l, st, out)
	case clTypeAssistant:
		claudeAssistant(&l, st, out)
	case clTypeAttachment:
		claudeQueued(&l, out)
	case clTypeSystem:
		// 上下文压缩：一行小字。codex 那边早就有这条（codex.go），claude 这边一直空着 ——
		// 表现是对话流里「凭空断了一截」，而断掉的正是最该有个交代的地方。
		if l.Subtype == clCompactBoundary {
			*out = append(*out, Msg{ID: l.UUID, Kind: KindNotice, Text: "上下文被压缩了", At: l.Timestamp})
		}
	}
}

// claudeQueued 认「排队发出去的那句人话」。
//
// ④ **agent 正在跑的时候打的字走队列，而队列那条路压根不产生 `user` 行。** 转录里是这样：
//
//	queue-operation / enqueue    人打字那一刻（文本在**顶层** content 里）
//	queue-operation / remove     出队
//	attachment / queued_command  真正注入对话的那一条（文本在 attachment.prompt 里）
//
// 所以只认 `user` 行的话，**人在 agent 干活时说的每一句都看不见** —— 而那恰恰是最常见的
// 用法（用户报的：「为什么我在其他终端发送的内容不会在 chat 里出现」，实测那一个会话里
// 有 24 条是这种）。和「在哪个终端打的」无关，变量是**排不排队**。
//
// 认 `attachment` 那条而不是 `enqueue` 那条：后者在打字那一刻就有（看着更快），但排队的
// 消息是可以被撤掉的，那时候 chat 里会留下一句从没发出去的话。`queued_command` 是它
// **真的进了对话**的记录。
//
// 判据用 `humanTurn`，不去嗅 prompt 开头：后台任务完成的通知也走 queued_command 这条路。
func claudeQueued(l *clLine, out *[]Msg) {
	a := l.Attachment
	if a == nil || a.Type != "queued_command" || !a.HumanTurn {
		return
	}
	// 和直接打的那条走同一套剥壳（排队发的也会被包 pasted_content / 斜杠命令壳）
	text := humanText(a.Prompt)
	if text == "" {
		return
	}
	*out = append(*out, Msg{ID: l.UUID, Kind: KindHuman, Text: clip(text, 4000), At: l.Timestamp})
}

// humanText 把人话上那几层壳剥掉（`<pasted_content …>`、斜杠命令那坨标签）。
// 两条路（直接打的 / 排队的）共用这一份 —— 各写一遍的话迟早只有一边剥。
func humanText(raw string) string {
	text := strings.TrimSpace(pastedWrap.ReplaceAllString(raw, ""))
	// 命令壳那套只对「整条就是个命令壳」的消息做（`cmdHead`）—— 抹标签这一下也一样会
	// 悄悄改掉人家引用的原文，而人自己写的 `<command-args>` 该原样显示。
	if !cmdHead.MatchString(text) {
		return text
	}
	if cmd := slashCmd(text); cmd != "" {
		return cmd
	}
	return strings.TrimSpace(cmdAny.ReplaceAllString(text, ""))
}

// claudeUser 处理 user 行。
//
// ② **工具结果是以 `user` 角色记的**（Anthropic API 的约定），所以这儿要分两种：
// `message.content` 是**字符串**才是人说的话；是**数组**的话里面装的是 `tool_result`。
// 不分的表现是对话流里「人」说了几十条工具输出。
func claudeUser(l *clLine, st *state, out *[]Msg) {
	// 压缩后那份 summary 是**写给 agent 自己看的**，不是人说的话 —— 它以 user 角色记着，
	// 不挡掉就是对话流里突然插进一条几万字的「人话」。上下文压缩这件事由前面那条
	// `compact_boundary` 交代（一行小字），这儿只管别把它当人话。
	if l.IsCompactSummary {
		return
	}
	var m struct {
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(l.Message, &m) != nil {
		return
	}
	var s string
	if json.Unmarshal(m.Content, &s) == nil {
		text := humanText(s)
		if text == "" {
			return
		}
		// 打断记号自己占一条小字，不当成人说的话 —— 它是 claude 写进去的，不是人打的。
		if strings.Contains(text, interruptMark) {
			*out = append(*out, Msg{ID: l.UUID, Kind: KindNotice, Text: "被打断了", At: l.Timestamp})
			return
		}
		*out = append(*out, Msg{ID: l.UUID, Kind: KindHuman, Text: clip(text, 4000), At: l.Timestamp})
		return
	}

	var blocks []clBlock
	if json.Unmarshal(m.Content, &blocks) != nil {
		return
	}
	for _, b := range blocks {
		if b.Type != "tool_result" {
			continue
		}
		ok2 := !b.IsError

		// **一律攒一条补丁。** 那条工具调用可能落在**前一批**里（流式过程中就是这样），
		// 那时候下面那个回填找不到它 —— 补丁是唯一的路，前端拿 Ref 在自己手上那份里认回去。
		// 在这一批里的话两件都做，幂等。
		if b.ToolUseID != "" {
			st.ups = append(st.ups, Update{
				Ref: b.ToolUseID, OK: &ok2,
				Answers: answersOf(l.ToolUseResult, b.Content),
			})
		}

		// 回填到前面那条工具调用上：这一层只送「成没成」，不送输出本身。
		// 一条 Bash 的输出能有几十 KB，而手机上要看的是「它在干什么、成没成」。
		i, ok := st.tool[b.ToolUseID]
		if !ok || i >= len(*out) {
			continue
		}
		(*out)[i].OK = &ok2
		// 提问那种：把「人选了哪个」填回那张卡上。
		if a := (*out)[i].Ask; a != nil {
			fillPicked(a, l.ToolUseResult, b.Content)
		}
	}
}

// answerLine 从那句话里抠「问题=选项」。
//
// 结构化那份（`toolUseResult.answers`）拿不到时才走这条：`tool_result.content` 是一句
// `Your questions have been answered: "问题"="选中的那个". You can now…`。
var answerLine = regexp.MustCompile(`"([^"]*)"="([^"]*)"`)

// fillPicked 把「人选了哪个」填到每个问题上（**同一批里**那种；跨批的走 Update）。
func fillPicked(a *Ask, structured json.RawMessage, content json.RawMessage) {
	answers := answersOf(structured, content)
	for i := range a.Questions {
		if v := answers[a.Questions[i].Question]; v != "" {
			a.Questions[i].Picked = v
		}
	}
}

// answersOf 把「人选了哪个」抠成一张 `问题 → 选中的 label` 表。
//
// 两个来源，优先结构化那份：
//
//	toolUseResult.answers   `{"问题": "选中的 label"}` —— 干净、不用解析句子
//	tool_result.content     那句 `"问题"="选项"`，老版本 / 拿不到结构化时的退路
//
// 拿不到就给空表（界面上只说「答过了」，不编一个选项出来）—— 问题文案是 agent 写的，
// 两处理论上一字不差，但那是别人的实现细节，不该拿它当硬判据。
func answersOf(structured json.RawMessage, content json.RawMessage) map[string]string {
	answers := map[string]string{}

	var r struct {
		Answers map[string]json.RawMessage `json:"answers"`
	}
	if json.Unmarshal(structured, &r) == nil {
		for q, raw := range r.Answers {
			// 单选是字符串，多选实测没遇到，但按数组兜一手（用「、」连起来）
			var one string
			if json.Unmarshal(raw, &one) == nil {
				answers[q] = one
				continue
			}
			var many []string
			if json.Unmarshal(raw, &many) == nil {
				answers[q] = strings.Join(many, "、")
			}
		}
	}

	if len(answers) == 0 {
		var s string
		if json.Unmarshal(content, &s) != nil {
			// content 也可能是块数组，那就把里面的文本拼起来再找
			var blocks []clBlock
			if json.Unmarshal(content, &blocks) == nil {
				var b strings.Builder
				for _, x := range blocks {
					b.WriteString(x.Text)
				}
				s = b.String()
			}
		}
		for _, m := range answerLine.FindAllStringSubmatch(s, -1) {
			answers[m[1]] = m[2]
		}
	}
	if len(answers) == 0 {
		return nil // 空表编出来是 `{}`，给 nil 让它在 JSON 里整个消失
	}
	return answers
}

func claudeAssistant(l *clLine, st *state, out *[]Msg) {
	var m struct {
		Content []clBlock `json:"content"`
	}
	if json.Unmarshal(l.Message, &m) != nil {
		return
	}
	// ③ **一行只是半个回复**：assistant 的每个 content block 各占一行（同一次 API 响应靠
	// `requestId` 串起来）。这儿不按 requestId 合并 —— thinking / 正文 / 工具调用在界面上
	// 本来就该分开显示（折起来的、气泡、一行摘要），合并反而要再拆一次。
	for j, b := range m.Content {
		// id 要带上块的下标：同一行里可能有多个块，光用行的 uuid 会撞，
		// 而前端拿它当 key —— 撞了的表现是 React 少画一条。
		id := l.UUID
		if j > 0 {
			id = l.UUID + ":" + itoa(j)
		}
		switch b.Type {
		case "text":
			if t := strings.TrimSpace(b.Text); t != "" {
				*out = append(*out, Msg{ID: id, Kind: KindAgent, Text: clip(t, 8000), At: l.Timestamp})
			}
		case "thinking":
			// thinking 可能是空的（这一档关掉、或者被 redact 掉了），空的就别占一条。
			if t := strings.TrimSpace(b.Thinking); t != "" {
				*out = append(*out, Msg{ID: id, Kind: KindThink, Text: clip(t, 4000), At: l.Timestamp})
			}
		case "tool_use":
			*out = append(*out, Msg{
				ID:   id,
				Kind: KindTool,
				Tool: b.Name,
				Meta: toolMeta(b.Name, b.Input),
				Ask:  askOf(b.Name, b.Input),
				// Ref 让后面那批的结果认得回来（见 Update）
				Ref: b.ID,
				At:  l.Timestamp,
			})
			if b.ID != "" {
				st.tool[b.ID] = len(*out) - 1
			}
		}
	}
}

// askOf 认出「agent 在问你一个带选项的问题」，把完整负载抠出来。
//
// 为什么只有这一种工具例外（别的都只给一行摘要）：被问住的时候，人在手机上缺的恰恰是
// **选项有几个、第二个是什么** —— 没有这些，屏幕上那条 `AskUserQuestion …` 等于什么都没说，
// 只能回终端看。而这份负载就在转录里，白拿。
//
// 解析失败一律给 nil（退回那条普通工具行）：这是别人的工具输入格式，会变。
func askOf(name string, input json.RawMessage) *Ask {
	if name != "AskUserQuestion" {
		return nil
	}
	var a Ask
	if json.Unmarshal(input, &a) != nil || len(a.Questions) == 0 {
		return nil
	}
	// multiSelect 在 JSON 里是这个名字，而我们的字段叫 Multi（json tag 是 `multi`，
	// 因为这个结构还要发给前端）—— 所以得单独再取一次，不然多选会被当成单选，
	// 而那两种在 TUI 里的作答按键完全不同。
	var raw struct {
		Questions []struct {
			MultiSelect bool `json:"multiSelect"`
		} `json:"questions"`
	}
	if json.Unmarshal(input, &raw) == nil {
		for i := range a.Questions {
			if i < len(raw.Questions) {
				a.Questions[i].Multi = raw.Questions[i].MultiSelect
			}
		}
	}
	return &a
}

// toolMeta 从工具的输入里抠出一行摘要。
//
// 按工具名挑字段，不是「把 input 整个 JSON 打印出来」—— 后者在手机上是一坨没法读的花括号，
// 而人要看的就是「跑了什么命令」「读了哪个文件」。认不出的工具退回「第一个字符串字段」，
// 所以新工具 / MCP 工具也有个能看的摘要（这条很重要：工具名单是别人的，会一直变）。
func toolMeta(name string, input json.RawMessage) string {
	var m map[string]json.RawMessage
	if json.Unmarshal(input, &m) != nil {
		return ""
	}
	// 按工具名优先挑那个「最说明它在干什么」的字段。
	var keys []string
	switch name {
	case "Bash", "BashOutput":
		keys = []string{"command", "description"}
	case "Read", "Write", "NotebookEdit":
		keys = []string{"file_path", "notebook_path"}
	case "Edit":
		keys = []string{"file_path"}
	case "Glob", "Grep":
		keys = []string{"pattern", "query"}
	case "WebFetch", "WebSearch":
		keys = []string{"url", "query"}
	case "Task", "Agent":
		keys = []string{"description", "prompt"}
	case "AskUserQuestion":
		// 完整负载走 Ask 那个字段，这一行只要个短标题；退回「按键名排序取第一个字符串」
		// 的话这儿会抠出某个选项的 description，看着像 agent 自己说的话。
		return ""
	case "TodoWrite":
		return ""
	}
	for _, k := range keys {
		if v := jsonStr(m, k); v != "" {
			return oneLine(v, 120)
		}
	}
	// 退路：任意一个非空字符串字段。**按键名排序取第一个**，不是 map 的随机序 ——
	// 不然同一条工具调用每次读出来的摘要都不一样，前端看着像内容在变。
	best := ""
	bestKey := ""
	for k, raw := range m {
		var s string
		if json.Unmarshal(raw, &s) != nil || strings.TrimSpace(s) == "" {
			continue
		}
		if bestKey == "" || k < bestKey {
			bestKey, best = k, s
		}
	}
	return oneLine(best, 120)
}
