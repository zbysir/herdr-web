package server

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/zbysir/herdr-web/internal/herdr"
	"github.com/zbysir/herdr-web/internal/transcript"
)

// chat 模式那条路的 HTTP 层。数据从哪儿来、为什么是读文件而不是读屏，都在
// internal/transcript 的包注释和 docs/dev/CHAT.md 里，这儿只讲路由和「读不出来时怎么说」。
//
// **读那个口（`log`）是只读的，另外两个口各只发一件形状固定的事**：`answer` 只发
// 「↓ ×n + ↵」，`start` 只发那张白名单里的一个命令名 + ↵。**这儿刻意不做「往 pane 里发任意
// 文本」的通道** —— 发言走的是现成的发件箱（`/api/herdr/say` → `agent.prompt`），那条路上
// 那几个坑（清空要 2N−1 次、`text:…enter` 的回车要隔 200ms、回车发送必须挡输入法）全是拿
// 真机换来的，另开一条通道就是把它们再踩一遍。
//
// 审批也**没有**在这儿留入口：会改状态的事留在终端里（docs/dev/TUI-VS-GUI.md §2）。

func (s *Server) apiChat(w http.ResponseWriter, r *http.Request, seg []string) {
	if s.Chat == nil || !s.Chat.Enabled() {
		fail(w, http.StatusNotFound, errf("这台机器上没找到 agent 的会话记录，或者 chat 被关掉了（HERDR_WEB_CHAT=0）"))
		return
	}
	if len(seg) < 2 {
		fail(w, http.StatusNotFound, errf("没有这个接口"))
		return
	}
	if seg[1] == "answer" && r.Method == http.MethodPost {
		s.chatAnswer(w, r)
		return
	}
	if seg[1] == "start" && r.Method == http.MethodPost {
		s.chatStart(w, r)
		return
	}
	if seg[1] != "log" || r.Method != http.MethodGet {
		fail(w, http.StatusNotFound, errf("没有这个接口"))
		return
	}
	q := r.URL.Query()
	name, err := sessionOf(r)
	if err != nil {
		fail(w, 400, err)
		return
	}
	sess, err := s.live(name)
	if err != nil {
		fail(w, 400, err)
		return
	}
	pane := q.Get("pane")
	if pane == "" {
		fail(w, 400, errf("要带上 pane"))
		return
	}

	ref, status, err := s.chatRef(sess, pane)
	if err != nil {
		chatFail(w, err)
		return
	}
	src, err := s.Chat.Find(ref)
	if err != nil {
		chatFail(w, err)
		return
	}
	// from / before 都是字节偏移。**解析失败一律当 0**（整份重来）而不是报错 —— 前端手上
	// 那个值可能来自上一个版本的响应，为这个把面板打不开不值当。
	from, _ := strconv.ParseInt(q.Get("from"), 10, 64)
	if from < 0 {
		from = 0
	}
	before, _ := strconv.ParseInt(q.Get("before"), 10, 64)

	// **偏移只在同一条会话里有意义。** 前端把手上那份的 sig 一起带上，对不上就把偏移丢掉
	// （当整份重来）—— `/clear` 之后 claude 写的是**另一个**文件，把上一份的字节偏移套在
	// 新文件上就是从中间某处开始读：前面那一截永远读不到，而且一个字都不报。
	// 带不带这个参数都行（老前端不带），不带就照旧信 from。
	if want := q.Get("sig"); want != "" && want != src.Sig {
		from, before = 0, 0
	}

	var log *transcript.Log
	if before > 0 {
		// 往上翻更早的那一段。前端拿上一批的 `start` 当 before。
		log, err = transcript.ReadBefore(src, before)
	} else {
		log, err = transcript.Read(src, from)
	}
	if err != nil {
		chatFail(w, err)
		return
	}
	// Msgs 为 nil 时编出来是 `null`，前端 `for of` 直接抛 —— 和提示那条路同一个坑。
	if log.Msgs == nil {
		log.Msgs = []transcript.Msg{}
	}
	// **状态跟这一拍一起给**，不让前端另外去问一次：这个口每拍本来就调了 `pane.get`，
	// `agent_status` 就在手上。另开一条轮询就是在跑着 agent 的那台机器上多敲一遍 herdr，
	// 而且两条轮询的节奏不一样，会出现「对话更新了但状态还是上一拍的」。
	// 「这一轮跑了多久 / 多少 token / 什么时候完的」—— 一次有界的回扫（见 transcript.TurnStat）。
	//
	// 原来只在 `working` 时算，后来「跑完了 3m 52s · 14:41」那一行也要这些数，所以一律算。
	// 代价可以接受：chat 一次只盯**一个** pane（不是那几十个），而这个口只在面板开着时被轮。
	// 算不出来就是不显示（`TurnStat` 会给 nil）—— 「空着比编一个数好」，和「几分钟前」
	// 那一列同一条规矩。
	var turn *transcript.Turn
	if t, err := transcript.TurnStat(src); err == nil {
		turn = t
	}
	writeJSON(w, 200, chatOut{
		Log: log, Status: status, Pane: pane, Turn: turn,
		Shells: s.Chat.Shells(src),
	})
}

// chatOut = 一份对话 + 这个 pane 此刻的 agent 状态。
//
// 嵌一个指针进来，JSON 里那些字段会摊平到同一层（前端就一个对象）。
type chatOut struct {
	*transcript.Log
	// Status herdr 的 `agent_status`（实测 `idle` / `working` / `blocked` / `done`）。
	//
	// **它是 chat 模式里唯一能说出「agent 正在干活」的东西** —— 转录的落盘粒度是一次 API
	// 请求，agent 想事情的时候文件一个字节都不动（实测能 15.58 秒），那段时间里对话流是
	// 完全静止的，没有这一档的话看着像卡住了。
	Status string `json:"status,omitempty"`
	// Pane 把 pane id 回一遍：前端换 pane 时上一拍的响应可能后到，靠它认出来丢掉。
	Pane string `json:"pane,omitempty"`
	// Turn 这一轮跑了多久 / 多少 token / 什么思考档（只在 Status 是 working 时有）。
	// 时间是**服务端算的秒数**，不是时间戳 —— 手机和这台机器的时钟能差几分钟，
	// 在前端拿 Date.now() 减出来的是个看着像真的错数字。
	Turn *transcript.Turn `json:"turn,omitempty"`
	// Shells 这个会话里还有几个后台任务在跑（claude 自己那条状态行最后那截）。
	// 判据在 transcript/shells.go —— 转录里两头都记着，不用读屏。
	Shells int `json:"shells,omitempty"`
}

// chatRef 从 herdr 那边把这个 pane 的会话身份问出来。
//
// **`Siblings` 只有在「没报过会话」时才有意义**，所以只在那种情况下才去 `pane.list`
// 数一遍 —— 装了 hook 的正常情况下一次 `pane.get` 就够了，而这是在跑着 agent 的那台
// 机器上按秒问的东西（轮询），能省一次调用就省一次。
func (s *Server) chatRef(sess *live, pane string) (transcript.Ref, string, error) {
	p, err := sess.outbox.C.PaneGet(pane)
	if err != nil {
		return transcript.Ref{}, "", err
	}
	if p.Agent == "" {
		return transcript.Ref{}, "", errNoAgent
	}
	ref := transcript.Ref{Agent: p.Agent, CWD: p.CWD}
	if as := p.AgentSession; as != nil && as.Value != "" {
		ref.Kind, ref.Value = as.Kind, as.Value
		return ref, p.AgentStatus, nil
	}
	// 没报过：数一下同一个 cwd 下还有几个跑着同一个 agent 的 pane。>1 就一律不猜
	// （transcript.ErrAmbiguous）—— 猜错的表现是「显示的是隔壁那个 pane 的对话」，
	// 而两边都在同一个项目里干活，屏幕上看着完全正常。
	if list, err := sess.outbox.C.PaneList(); err == nil {
		for _, x := range list {
			if x.Agent == p.Agent && x.CWD == p.CWD {
				ref.Siblings++
			}
		}
	}
	return ref, p.AgentStatus, nil
}

/*
chatAnswer：**替人答那个选择框**（`AskUserQuestion`）。

这是这条路上唯一一个会改状态的口，所以先说清它为什么长这样。

# 它只发得出那几个键

请求里给的是**每题选了哪几个选项（下标）**，不是按键 —— 键序列在服务端按下标算出来
（见 askKeys）。这样即使前端被人改了、或者这个口被别的东西调，它也只可能发出「序号 /
enter / tab / 方向」这几下，发不出别的任何东西。（前端传一串按键过来是最自然的写法，但那就等于
开了一个「往任意 pane 打任意按键」的口。）

唯一带字的是「自己写」那一格（TUI 列表里的 `Type something.`，`Other`）：那串字只会在
**光标已经落在那一格上**之后才发，而且换行 / 控制字符在 cleanOther 里剥掉了 —— 一个 `\r`
混进去就是在输入框里按了回车。

# 按 pane 寻址，不走焦点

用 `pane.send_input` 指名那个 pane，**不是**把字节打进当前终端的 PTY。后者依赖「herdr 此刻
焦点在哪儿」，而 chat 面板看的 pane 和 herdr 的焦点完全可以不是同一个 —— 那种错发是
「替你在另一个 pane 里选了一个选项」，而两边屏幕上都看不出来。

# 「选择框真的开着吗」用**数据**判，不读屏

判据是：**最后一条工具调用是 `AskUserQuestion` 而且还没有结果**。它成立 ⟺ agent 发出了提问、
人还没答 —— 这是转录里记着的事实，不是猜的。

为什么不能用 `agent_status`：实测过一个**正在显示选择器**的 pane 报的是 `idle`，另一次同样
的对话框又报 `blocked`（见 docs/dev/COMPOSER.md 和 HERDR-API.md 那张表）。拿它当闸门就是
静默地往一个没有选择框的 pane 里打 ↵ —— 而那一下会把输入框里的草稿提交出去。

# 原来那条「高亮停在哪」的假设已经没了

老版本发的是 ↓ ×n + ↵，那串键假设**高亮此刻停在第一个选项上** —— 人先在终端里按过方向键
就会选错，所以当时界面上要二次确认、而且只敢做「单题单选」。现在发的是**选项序号**，
和高亮停在哪完全无关（实测见 askKeys），那个假设整个不成立了：多题 / 多选都能答，
界面上那道二次确认也跟着去掉了。

剩下的闸门只有一条，在 askKeys 那边：**序号是单个数字字符**，所以选项超过 9 个这条路发不了
（pendingAsk 里挡着，前端判据要跟它一样，不然是「点了报错」）。
*/
func (s *Server) chatAnswer(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Pane string
		// Picks 每题选了哪几个选项（下标）。多选那题可以给多个。
		Picks [][]int
		// Other 每题「自己写」那一格的字（空 = 没写）。单选题写了它就不能再选选项；
		// 多选题是和勾选的并存的。
		Other []string
		// Index 老前端那条路：单题单选时的那一个序号。Picks 有值就不看它。
		Index int
	}
	if err := readJSON(r, &b); err != nil {
		fail(w, 400, err)
		return
	}
	if b.Pane == "" {
		fail(w, 400, errf("要带上 pane"))
		return
	}
	if b.Index < 0 {
		fail(w, 400, errf("选项序号不能是负的"))
		return
	}
	name, err := sessionOf(r)
	if err != nil {
		fail(w, 400, err)
		return
	}
	sess, err := s.live(name)
	if err != nil {
		fail(w, 400, err)
		return
	}
	ref, _, err := s.chatRef(sess, b.Pane)
	if err != nil {
		chatFail(w, err)
		return
	}
	src, err := s.Chat.Find(ref)
	if err != nil {
		chatFail(w, err)
		return
	}
	log, err := transcript.Read(src, 0)
	if err != nil {
		chatFail(w, err)
		return
	}
	ask, err := pendingAsk(log.Msgs)
	if err != nil {
		// 409：这不是参数错，是「此刻不该发这个」。前端据此说「问题已经被答过了 / 变了，
		// 刷新一下看看」，而不是报一个像 bug 的错。
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "reason": "not_pending"})
		return
	}
	other, err := cleanOthers(ask, b.Other)
	if err != nil {
		fail(w, 400, err)
		return
	}
	picks, err := resolvePicks(ask, b.Picks, b.Index, other)
	if err != nil {
		fail(w, 400, err)
		return
	}
	keys := askKeys(ask, picks, other)
	if err := sendOneByOne(sess.outbox.C, b.Pane, keys); err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"pane": b.Pane, "picked": pickedLabels(ask, picks, other), "keys": len(keys)})
}

/*
askKeys：把「每题选了哪几个」编成按键序列。

# 这套协议是**量出来的**，不是猜的

拿一个隔离的 tmux + 真 claude 2.1.278 逐键试出来的（和 CLAUDE.md 里量 codex 那个粘贴判据
一个办法）。实测：

	单选题      发该选项的**序号**（1-based）→ 选中 + **自动进入下一题**
	带 preview  序号**只把光标移过去**，要再来一下 `enter` 才算选中（见下面那段）
	多选题      发每个要选的序号 → **切换勾选**，高亮不动、不跳题
	换页        `tab` → 下一个标签；最后一题之后是 Submit 页
	提交        Submit 页上发 `1`（那页第一项就是 `1. Submit answers`）
	例外        **只有一个问题且是单选**时，序号本身就提交了，没有 Submit 页

# 带 preview 的那一题要多发一下 enter

选项带 `preview`（工具那边给的 ASCII 预览图）时，TUI 换成**左右分栏**：左边一列选项、
右边画预览。那一屏里**数字键只是把光标移过去**，不选中、也不跳题 —— 判据是屏幕最下面
那行提示自己说的：

	普通题        Enter to select · Tab/Arrow keys to navigate · Esc to cancel
	带 preview    Enter to select · ↑/↓ to navigate · n to add notes · Tab to switch questions

漏了这一下是**完全静默**的：herdr 不报错、这个口回 200、界面上说「提交成功」，而那串键
从这一题起全变成「在同一题里挪光标」——**一题都没提交**，TUI 停在原地。用户报的就是这个：
「点了提交，终端里什么都没反应，还停留在第一个问题」。

当初量这套协议时用的是「一道多选 + 一道单选」，两道都没有 preview，所以这条一直没露头 ——
**preview 只在单选题上有**（工具那边的限制），于是它和「单选发个序号就完事」正好撞在一起。
2026-09-22 在 herdr 里拿真 claude 2.1.278 复现并量过：四道全单选、第二道带 preview，
`1`（A 选中并跳到 B）→ `2`（**只是把光标移到 b2**）→ `enter`（选中 b2 并跳到 C）→ …… →
Submit 页发 `1` 提交，转录里四题的答案一字不差。

# 为什么改用数字而不是原来那串 ↓×n + ↵

**数字键和高亮停在哪完全无关**（实测：先按两下 ↓ 把高亮移到第 3 个，再发 `2`，记下来的是
第 2 个）。原来那串 ↓ 假设「高亮此刻停在第一个选项上」——人只要先在终端里按过方向键就会选错，
而那正是当初只敢做单题、还要在界面上提醒「别按方向键」的原因。这个假设现在整个没了。

# 序号怎么对应

TUI 的列表里 payload 的选项排在前面，后面还跟着 `Type something` / `Chat about this` 这些
它自己加的行 —— 所以 payload 第 i 个 ⇒ 数字 i+1。**因此选项不能超过 9 个**（一个数字字符），
pendingAsk 里挡着。

# 「自己写」那一格（`Type something.`，就排在选项后面、第 n+1 行）

2026-09-25 拿真 claude 2.1.282 在单独的 herdr session 里量的，两种题**完全不是一个走法**：

	单选  发 `n+1` → 光标落到那一格、直接进编辑态 → 打字 → `enter`（记下这串字 + 跳题；
	      只有这一题时就直接提交了，和选序号一样）
	多选  数字键只**切换勾选、光标不动**，所以 `n+1` 只会勾上一个空格子、字没处去。
	      得把光标**挪过去**：`down` × n（从第 1 行起）→ 打字（**打字自己就会勾上**，
	      别再按 `n+1`，那是切换、会把它取消）→ `down` 落到这一题自己的 Submit 行 →
	      `enter` 答完这一题、翻到下一页。**不能用 `tab` 翻页**：光标在那一格上时 tab 只是
	      往下挪一行，挪到 Submit 行上再按 tab / right 都不动（实测，一个字都不报）。

`down` × n 假设「光标此刻在第 1 行」。从上一题跳过来（单选自动跳 / 多选 enter）时确实在
第 1 行（实测）；**第一题不一定** —— 人可能在终端里按过方向键。所以第一题先 `right` + `left`
出去再回来：重进一页光标一定回到第 1 行（实测）。只在第一题做，因为光标要是正好停在那一格上
（编辑态），左右键挪的是字里的光标，不翻页 —— 后面的题不会是这个状态。
（`home` / `pageup` herdr 不认，`up` 到顶会绕回底下，都当不了「回到第 1 行」。）
*/
func askKeys(ask *transcript.Ask, picks [][]int, other []string) []askKey {
	var keys []askKey
	k := func(names ...string) {
		for _, n := range names {
			keys = append(keys, askKey{Key: n})
		}
	}
	for qi, q := range ask.Questions {
		text := other[qi]
		if text != "" && !q.Multi {
			// 单选：序号直接把光标带进编辑态，打完 enter 就是选中 + 跳题
			k(strconv.Itoa(len(q.Options) + 1))
			keys = append(keys, askKey{Text: text})
			k("enter")
			continue
		}
		if text != "" && qi == 0 {
			k("right", "left") // 回到第 1 行，见上面
		}
		for _, oi := range picks[qi] {
			k(strconv.Itoa(oi + 1))
			// 带 preview 的单选：序号只把光标移过去，enter 才是「选中并跳到下一题」。
			// **多选那边不补** —— preview 只在单选题上有（工具那边的限制），而多选的
			// enter 在 TUI 里是「答完这一题」，补上去就把后面几个勾选一起吞了。
			if q.Preview && !q.Multi {
				k("enter")
			}
		}
		switch {
		case q.Multi && text != "":
			for range q.Options {
				k("down")
			}
			keys = append(keys, askKey{Text: text})
			k("down", "enter")
		case q.Multi:
			// 多选不会自己跳题，得手动翻页
			k("tab")
		}
	}
	// 单题单选那种发完序号就已经提交了，再补一个 `1` 会被打进输入框
	if !(len(ask.Questions) == 1 && !ask.Questions[0].Multi) {
		k("1")
	}
	return keys
}

// askKey 那串序列里的一下：要么是一个键名，要么是「自己写」那一格里的一串字。
type askKey struct {
	Key  string
	Text string
}

// maxOther 「自己写」那一格最多收多少个字。TUI 那一格是单行的，这儿只是别让一个超长的
// 请求往 pane 里灌上几兆。
const maxOther = 2000

// cleanOthers 把每题「自己写」的字理一遍：换行和控制字符换成空格（**一个 `\r` 混进去
// 就是在那一格里按了回车**，后面的键全对不上位置）、去掉首尾空白、限长。
func cleanOthers(ask *transcript.Ask, raw []string) ([]string, error) {
	out := make([]string, len(ask.Questions))
	if len(raw) == 0 {
		return out, nil
	}
	if len(raw) != len(ask.Questions) {
		return nil, fmt.Errorf("有 %d 个问题，「自己写」给了 %d 份", len(ask.Questions), len(raw))
	}
	for i, t := range raw {
		t = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return ' '
			}
			return r
		}, t)
		t = strings.TrimSpace(t)
		if n := utf8.RuneCountInString(t); n > maxOther {
			return nil, fmt.Errorf("第 %d 个问题自己写的字太长了（%d 个字，最多 %d）", i+1, n, maxOther)
		}
		// 单选那条路要按 n+1 这个序号，所以同样受「一个数字字符」限制
		if t != "" && !ask.Questions[i].Multi && len(ask.Questions[i].Options)+1 > 9 {
			return nil, fmt.Errorf("第 %d 个问题选项太多，「自己写」这一格按不到 —— 回终端答", i+1)
		}
		out[i] = t
	}
	return out, nil
}

// keyGap 两下之间隔多久。见 sendOneByOne 的 ③。
const keyGap = 80 * time.Millisecond

/*
sendOneByOne：把那串按键发出去。三条**全是在真 pane 上量出来的**，每一条都对应一种
「静默失效」，所以都别改：

① **数字必须走 `keys`，不能走 `text`。** herdr 的 `pane.send_input` 在给 `text` 时会**按那个

	pane 当前的 bracketed-paste 状态**编码（CLAUDE.md 里就写着它会这么干）。claude 的 TUI
	开着 DEC 2004，于是一个数字被当成「粘进来的一段文字」—— **选择器压根不理**。
	表现极具误导性：herdr 不报错、往 `cat -v` 那种没开 2004 的 pane 里发看到的又是裸字节，
	而真实后果是「点了提交，TUI 里一个选项都没选上，但标签还是往后翻了」（用户报的，
	因为 `tab` 走的是 `keys`、照样生效）。同一张卡上 A/B 过：
	`send_input{text:"1"}` 无效，`send_text{text:"2"}` / `send_keys{["3"]}` /
	`send_input{keys:["4"]}` 三个都有效。**数字本身是合法键名**，所以统一走 `keys`。

② **一个键一次调用。** 一次 `send_keys{["2","3","4"]}` 实测只有**最后一个**生效

	（`[✔][✔][✔]` → `[✔][✔][ ]`），另外两个静默丢掉。

③ **两下之间要留间隔。** 背靠背三次调用（总共 1ms）只有**第一下**生效；实测 10ms 就够，

	这儿取 80ms 留足余量（6 个键也才 0.5 秒，而丢一下就是答错）。

顺带解释了为什么原来那串 `↓↓⏎` 一次发能用：那些是转义序列，herdr 按键编码之后 claude
逐个解析，不走「粘贴」那条路 —— 所以老那条路从来没暴露过 ①。
*/
//
// 带字的那一下（「自己写」）走 `text`：那一格是个真输入框，粘贴进去是认的（实测）。
func sendOneByOne(c *herdr.Client, pane string, keys []askKey) error {
	for i, k := range keys {
		if i > 0 {
			time.Sleep(keyGap)
		}
		var err error
		if k.Text != "" {
			err = c.SendText(pane, k.Text, nil)
		} else {
			err = c.SendKeys(pane, []string{k.Key})
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// resolvePicks 把请求里那份选择核一遍。`index` 是老前端那条路（单题单选）。
//
// **每题都必须有选择**：缺一题的话 Submit 页会拒（那边要求全答完），而我们已经把前面几题
// 的按键发出去了 —— 停在一个半填的选择器上比什么都没发更糟。
//
// 「自己写」了字的那题（`other[qi] != ""`）可以一个选项都不选；单选题写了字就**不能**再选
// 选项（TUI 里那是同一个单选框里的两行）。
func resolvePicks(ask *transcript.Ask, picks [][]int, index int, other []string) ([][]int, error) {
	if len(picks) == 0 && hasOther(other) {
		picks = make([][]int, len(ask.Questions))
	}
	if len(picks) == 0 {
		// 老前端：只传了一个序号，那时候只可能是单题单选
		if len(ask.Questions) != 1 {
			return nil, fmt.Errorf("这次问了 %d 个问题，得每题都给一个选择", len(ask.Questions))
		}
		picks = [][]int{{index}}
	}
	if len(picks) != len(ask.Questions) {
		return nil, fmt.Errorf("有 %d 个问题，给了 %d 份选择", len(ask.Questions), len(picks))
	}
	for qi, q := range ask.Questions {
		got := picks[qi]
		if len(got) == 0 && other[qi] == "" {
			return nil, fmt.Errorf("第 %d 个问题还没选", qi+1)
		}
		if !q.Multi && len(got) > 0 && other[qi] != "" {
			return nil, fmt.Errorf("第 %d 个问题是单选，选了选项又自己写了字", qi+1)
		}
		if !q.Multi && len(got) > 1 {
			return nil, fmt.Errorf("第 %d 个问题是单选，给了 %d 个", qi+1, len(got))
		}
		seen := map[int]bool{}
		for _, oi := range got {
			if oi < 0 || oi >= len(q.Options) {
				return nil, fmt.Errorf("第 %d 个问题只有 %d 个选项，给的是第 %d 个", qi+1, len(q.Options), oi+1)
			}
			if seen[oi] {
				// 多选是**切换**，同一个发两次等于没选（实测），所以这儿挡掉
				return nil, fmt.Errorf("第 %d 个问题里第 %d 个选项给了两次", qi+1, oi+1)
			}
			seen[oi] = true
		}
	}
	return picks, nil
}

func hasOther(other []string) bool {
	for _, t := range other {
		if t != "" {
			return true
		}
	}
	return false
}

// pickedLabels 回一句「选了什么」给前端做反馈（每题用 `,` 连、题之间用 ` / `）。
// 自己写的字排在勾选的后面 —— 和 claude 记进转录的顺序一样（`苹果, 荔枝`）。
func pickedLabels(ask *transcript.Ask, picks [][]int, other []string) string {
	var qs []string
	for qi, q := range ask.Questions {
		var one []string
		for _, oi := range picks[qi] {
			one = append(one, q.Options[oi].Label)
		}
		if other[qi] != "" {
			one = append(one, other[qi])
		}
		qs = append(qs, strings.Join(one, ", "))
	}
	return strings.Join(qs, " / ")
}

// pendingAsk 认「此刻真有一个没答的选择框」。
//
// 判据是**最后一条工具调用**是 `AskUserQuestion` 且没有结果（`OK == nil`）。一定要是最后
// 那条：中间那些早就答过了，而「有没有结果」这件事只有在整份读的那一遍里才回填得到
// （见 transcript 的 state.tool 注释）—— 所以这儿一律 `Read(src, 0)`，不走增量。
//
// 只支持**单个问题 + 单选**：多个问题要在 TUI 里一题一题走，多选是空格勾选再回车，
// 两种的按键序列都和「↓ ×n + ↵」不一样。宁可不给点，别替人选错。
func pendingAsk(msgs []transcript.Msg) (*transcript.Ask, error) {
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Kind != transcript.KindTool {
			continue
		}
		if m.Ask == nil || m.OK != nil {
			return nil, errf("这个 pane 此刻没有在等你选（最后一条工具调用不是没答的提问）")
		}
		if len(m.Ask.Questions) == 0 {
			return nil, errf("这次提问里一个问题都没有")
		}
		// **选项超过 9 个就不接**：按键是单个数字字符（见 askKeys 的注释），十位数没法发。
		// AskUserQuestion 的 schema 本身上限是 4 个，所以这只是道保险 —— 真撞上了宁可
		// 让人回终端答，别发出一串意思完全不同的按键。
		for i, q := range m.Ask.Questions {
			if len(q.Options) == 0 {
				return nil, fmt.Errorf("第 %d 个问题没有选项", i+1)
			}
			if len(q.Options) > 9 {
				return nil, fmt.Errorf("第 %d 个问题有 %d 个选项，超过 9 个这条路发不了 —— 回终端答", i+1, len(q.Options))
			}
		}
		return m.Ask, nil
	}
	return nil, errf("这个 pane 此刻没有在等你选")
}

var errNoAgent = errors.New("这个 pane 里没有 agent")

// chatFail 把读不出来的原因分开报。
//
// **这几种的处理方式完全不同**，混成一句「打不开」的话，最常见那种（hook 还没装 / agent
// 是装之前起来的）就永远查不出来：
//
//	409 + need_install   herdr 还没拿到会话身份 → 界面上要说「装 integration / 重开这个 agent」
//	409 + ambiguous      同一个目录好几个 agent pane → 说清为什么不猜
//	409 + no_transcript  会话有了、文件还没有 → 多半是「还没对话」，界面上是空状态不是错误
//	404                  这个 agent 的格式还不支持（只有 claude / codex）
//	400                  这个 pane 里压根没有 agent
func chatFail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errNoAgent):
		fail(w, 400, err)
	case errors.Is(err, transcript.ErrUnsupported):
		fail(w, 404, err)
	case errors.Is(err, transcript.ErrAmbiguous):
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":  err.Error(),
			"reason": "ambiguous",
		})
	case errors.Is(err, transcript.ErrNoTranscript):
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":  err.Error(),
			"reason": "no_transcript",
		})
	case errors.Is(err, transcript.ErrNoSession):
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":  err.Error(),
			"reason": "need_install",
		})
	default:
		fail(w, 400, err)
	}
}

// startable 在这个 pane 里开得出来的 agent。**这张表就是这个口能发出去的全部东西。**
//
// 写死两个而不是收前端传来的命令行，理由和 `answer` 那个口一样：它往一个**登录 shell**
// 里敲字并回车，等于远程执行命令。收任意字符串的话这一层就成了第二条 PTY，而那条已经有了
// （带鉴权、带 Origin 检查、带审计意义上的「人自己在敲」）—— 白送一条更省事的出来不值当。
//
// 想要别的命令：快捷键条上配一个 `text:xxx enter` 的键（设置 → 快捷键条 → 我的按键）。
// 那条路本来就是干这个的，而且带着「回车隔 200ms」那道（见 keysend.ts 的 splitEnter）。
var startable = map[string]string{
	"claude": "claude",
	"codex":  "codex",
}

// chatStart 在一个**没有 agent 的** pane 里开一个 agent。
//
// 为什么这个口存在：chat 看的永远是焦点那个 pane，而焦点落在一个 shell pane 上时那一屏
// 只能说「这儿没有 agent」+ 一个「回到终端」（用户报的：希望能直接在这儿开一个）。
//
// # 两条判据
//
// ① **pane 里已经有 agent 就拒**（409 `has_agent`）。不拒的后果不是「白开一个」，而是
//
//	把 `claude` 这五个字母**当成一句话投进正在跑的那个 agent 的输入框**。前端只在那一屏
//	给按钮，但它手上那份 pane 列表最多 3 秒旧 —— 这期间人可能自己在终端里把 agent 开起来了。
//
// ② **不走 PTY 那条路**（前端 `sendKeyBytes`），而是 herdr 的 `pane.send_input`：chat 模式
//
//	在终端 WebSocket 断着的时候照旧能用（那正是左上角那个状态点要分开说的事），按钮跟着
//	终端连接一起失效就说不通了。
//
// 顺带一条：这儿**不需要**「回车隔 200ms」那道。那道是给 codex 的输入框看的
// （`paste_burst.rs` 把「连着 3 个字符、间隔 <8ms」当粘贴），而开 agent 这一下敲的对面是
// **zsh 的提示符**，没有这个启发式。
func (s *Server) chatStart(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Pane  string
		Agent string
	}
	if err := readJSON(r, &b); err != nil {
		fail(w, 400, err)
		return
	}
	if b.Pane == "" {
		fail(w, 400, errf("要带上 pane"))
		return
	}
	cmd, ok := startable[b.Agent]
	if !ok {
		fail(w, 400, fmt.Errorf("开不了 %q —— 这个口只认 claude / codex", b.Agent))
		return
	}
	name, err := sessionOf(r)
	if err != nil {
		fail(w, 400, err)
		return
	}
	sess, err := s.live(name)
	if err != nil {
		fail(w, 400, err)
		return
	}

	// ①：现问一次 herdr，别信前端那份列表（见上）。
	p, err := sess.outbox.C.PaneGet(b.Pane)
	if err != nil {
		fail(w, 400, err)
		return
	}
	if p.Agent != "" {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":  fmt.Sprintf("这个 pane 里已经跑着 %s 了", p.Agent),
			"reason": "has_agent",
			"agent":  p.Agent,
		})
		return
	}

	if err := sess.outbox.C.SendText(b.Pane, cmd, []string{"enter"}); err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"pane": b.Pane, "agent": b.Agent})
}
