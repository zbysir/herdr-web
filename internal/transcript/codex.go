package transcript

import (
	"encoding/json"
	"strings"
)

// codex 的转录（rollout）：`~/.codex/sessions/<年>/<月>/<日>/rollout-<时间>-<session_id>.jsonl`。
//
// 和 claude 完全不是一种形状。外层是个信封：
//
//	{"timestamp":…, "ordinal":7, "type":"event_msg"|"response_item"|"session_meta"|…, "payload":{…}}
//
// **内容读 `event_msg` / `item_completed`，不读 `response_item`。** 这是这份实现里最要紧的
// 一个选择，理由是 `response_item` 是**贴着 API 的原始层**，里面混着三种读不了的东西：
//
//	role:"developer" 的 message   整段系统提示，实测单行 55KB —— 渲染出来就是一屏
//	                              「You are Codex…」盖住真正的对话
//	reasoning                     只有 `encrypted_content`（加密的），`summary` 是空数组，
//	                              也就是**拿不到任何可读的思考内容**
//	custom_tool_call              输入是一段 JS（`tools.exec_command({...})`），
//	                              不如 item_completed 里现成的 `command` + `exit_code`
//
// 而 `item_completed` 给的是已经归好类的 item，实测（扫最近 8 个会话）有这些：
//
//	Reasoning 1719        summary_text / raw_content **实测全是空数组** —— 跳过
//	CommandExecution 1333 command / cwd / exit_code / aggregated_output，样样现成
//	AgentMessage 400      content[{type:"Text",text}]，还带 phase
//	FileChange 242        changes 是「路径 → {type:add/update, content}」的字典
//	UserMessage 146       content[{type:"text",text}]
//	ImageView 111         path
//	Extension 37          kind（如 web.search）/ query / results
//	ContextCompaction 2   上下文被压缩了
//
// 代价是 `event_msg` 会比 `response_item` 晚一点落盘（它是「这一条完成了」的事件），
// 差的是同一次 flush 里的先后，对话流上看不出来。

type cxLine struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type cxPayload struct {
	Type string          `json:"type"`
	Item json.RawMessage `json:"item"`
}

type cxItem struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	// UserMessage / AgentMessage
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	// CommandExecution
	Command  json.RawMessage `json:"command"` // 可能是数组（["/bin/zsh","-lc","…"]）也可能是字符串
	ExitCode *int            `json:"exit_code"`
	Status   string          `json:"status"`
	// FileChange
	Changes map[string]json.RawMessage `json:"changes"`
	// ImageView
	Path string `json:"path"`
	// Extension（web.search 那类）
	Kind  string `json:"kind"`
	Query string `json:"query"`
}

func parseCodex(line []byte, st *state, out *[]Msg) {
	var l cxLine
	if json.Unmarshal(line, &l) != nil {
		return
	}
	// 只认 event_msg —— session_meta（第一行，实测 22KB 的系统提示）、turn_context、
	// token_usage_record、world_state 都不是对话。
	if l.Type != "event_msg" {
		return
	}
	var p cxPayload
	if json.Unmarshal(l.Payload, &p) != nil || p.Type != "item_completed" {
		return
	}
	var it cxItem
	if json.Unmarshal(p.Item, &it) != nil {
		return
	}
	at := l.Timestamp

	switch it.Type {
	case "UserMessage":
		text := strings.TrimSpace(cxText(&it))
		if text == "" {
			return
		}
		*out = append(*out, Msg{ID: it.ID, Kind: KindHuman, Text: clip(text, 4000), At: at})

	case "AgentMessage":
		if text := strings.TrimSpace(cxText(&it)); text != "" {
			*out = append(*out, Msg{ID: it.ID, Kind: KindAgent, Text: clip(text, 8000), At: at})
		}

	case "CommandExecution":
		m := Msg{ID: it.ID, Kind: KindTool, Tool: "exec", Meta: oneLine(cxCommand(it.Command), 120), At: at}
		// exit_code 有值才算跑完了。**`status` 不能当判据** —— 它在跑的过程中也是有值的，
		// 而「还在跑」和「跑完了退 0」在界面上是两种画法。
		if it.ExitCode != nil {
			ok := *it.ExitCode == 0
			m.OK = &ok
		}
		*out = append(*out, m)

	case "FileChange":
		*out = append(*out, Msg{ID: it.ID, Kind: KindTool, Tool: "edit", Meta: cxChanges(it.Changes), OK: cxDone(it.Status), At: at})

	case "ImageView":
		*out = append(*out, Msg{ID: it.ID, Kind: KindTool, Tool: "image", Meta: oneLine(strings.TrimPrefix(it.Path, "file://"), 120), At: at})

	case "Extension":
		q := it.Query
		if q == "" {
			q = it.Kind
		}
		*out = append(*out, Msg{ID: it.ID, Kind: KindTool, Tool: cxToolName(it.Kind), Meta: oneLine(q, 120), At: at})

	case "ContextCompaction":
		// 压缩之后 codex 还往**同一份 rollout** 里写（claude 那边是换一个新文件），
		// 所以这只是一行小字，不是「会话换了」。
		*out = append(*out, Msg{ID: it.ID, Kind: KindNotice, Text: "上下文被压缩了", At: at})

	case "Reasoning":
		// 实测 summary_text / raw_content 全是空数组 —— 拿不到可读内容，占一条空气泡
		// 比不显示更糟。等哪天 codex 真往里写东西了再接（那时这儿会自然地一直是空）。
		return
	}
}

func cxText(it *cxItem) string {
	var b strings.Builder
	for _, c := range it.Content {
		// 两家大小写不一样：UserMessage 里是 "text"，AgentMessage 里是 "Text"。
		// **按小写比**，不然 agent 说的话一条都认不出来（而且完全静默）。
		switch strings.ToLower(c.Type) {
		case "text", "input_text", "output_text":
			b.WriteString(c.Text)
		}
	}
	return b.String()
}

// cxCommand 命令行可能是数组也可能是字符串。
//
// 数组那种实测是 `["/bin/zsh","-lc","真正的命令"]` —— 前两截是壳，人要看的是最后那段。
// 直接 join 的表现是每条命令前面都顶着一串 `/bin/zsh -lc`，把真正的命令挤出屏幕。
func cxCommand(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var a []string
	if json.Unmarshal(raw, &a) != nil || len(a) == 0 {
		return ""
	}
	if len(a) >= 3 && (strings.HasSuffix(a[0], "sh") || strings.HasSuffix(a[0], "bash") || strings.HasSuffix(a[0], "zsh")) {
		return a[len(a)-1]
	}
	return strings.Join(a, " ")
}

// cxChanges 把「改了哪些文件」压成一行：第一个文件名 + 还有几个。
// 全列出来的话一次十几个文件就占掉半屏，而这一层只回答「它在改什么」。
func cxChanges(m map[string]json.RawMessage) string {
	if len(m) == 0 {
		return ""
	}
	first := ""
	for p := range m {
		// 取字典序最小的那个，别用 map 的随机序 —— 不然同一条每次读出来的摘要都不一样。
		if first == "" || p < first {
			first = p
		}
	}
	s := fileBase(first)
	if len(m) > 1 {
		s += " 等 " + itoa(len(m)) + " 个文件"
	}
	return s
}

func cxDone(status string) *bool {
	switch status {
	case "completed":
		ok := true
		return &ok
	case "failed":
		ok := false
		return &ok
	}
	return nil
}

func cxToolName(kind string) string {
	if kind == "" {
		return "ext"
	}
	// `web.search` → `web.search`，但太长的截一下；这是显示名，不是判据。
	return oneLine(kind, 24)
}
