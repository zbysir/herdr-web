// Package transcript 把 agent 自己写在磁盘上的会话记录读成一条对话流（chat 模式的数据源）。
//
// **为什么是读文件，而不是读屏或者问 herdr。** 三条路只有这一条站得住：
//
//	herdr socket   没有任何聊天层的接口（`pane.read` 给的是渲染完的屏幕，
//	               `agent.list/get` 只给状态）—— 见 docs/dev/CHAT.md §2
//	读屏           这个项目最贵的一条路：错了**不报错**，只能靠真机抓屏钉住
//	               （internal/composer / internal/agentwatch 就是这么来的）
//	读 agent 的转录 claude 和 codex 都在往磁盘写完整的 JSONL，一行一个完整 JSON、
//	               纯 append、可 tail —— 这正是给外部消费准备的形状
//
// 落盘粒度是**一次 API 请求**（不是逐 token）：一个 agentic turn 会打很多次 API，每次连着
// thinking + text + tool_use 一起 flush。所以手机上的体感是每隔几秒冒出一块，和流式没区别；
// 唯一拿不到的是「一段连续正文内部逐字长出来」那个过程 —— 那只活在 agent 进程内存和 PTY 里，
// 从不落盘。实测数据在 docs/dev/CHAT.md §5/§6。
//
// # 四条会静默出错的
//
// ① **行内 `timestamp` 是生成时刻，不是写盘时刻**（实测同一次写入落下来的两行相差 15 秒）。
// 排序一律**按文件里的先后**，别按时间戳 —— claude 的 attachment 行时间戳常比兄弟行早 1ms。
//
// ② **工具结果是以 `user` 角色记的**（Anthropic API 的约定：`tool_result` 属于 user turn 的
// content block）。不过一层的话，对话流里「人说的话」会混进几十条工具输出。
//
// ③ **claude 的一行只是半个回复**：assistant 的每个 content block 各占一行，同一次 API 响应
// 靠 `requestId` 串起来。按行渲染的表现是 thinking / 正文 / 工具调用各成一条气泡。
//
// ④ **不能整个 load**：单个会话文件实测到 12MB+（上游报过 17MB）。这儿一律按行流式读，
// 首屏还从文件尾部开一个窗口往前找（见 Read 的 window），别让「打开面板」变成读十几兆。
//
// # 定位：pane → 会话 → 文件
//
// 钥匙是 herdr 的 `pane.report_agent_session` —— `herdr integration install claude|codex` 装的
// hook 在 `SessionStart` 里把 `session_id`（claude 还带 `transcript_path`）报给 herdr，我们从
// `pane.get` 的 `agent_session` 里读出来。**herdr 只给一个 ref**（`kind` 是 `id` 或 `path`，
// 实测 claude 给的是 `id`，虽然 hook 两个都报了），所以 id → 路径这一步得自己做，见 locate.go。
//
// 没装 hook 时 `agent_session` 是 `null`，那时**退回按 cwd 猜**，而「同一个 cwd 开着好几个
// agent pane」一律不猜（`ErrAmbiguous`）—— 猜错的表现是「chat 里显示的是隔壁那个 pane 的对话」，
// 而两边都在同一个项目里干活，看着完全正常。一台机器上几十个 pane 是常态，这种撞车不是边缘情况。
package transcript

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// Kind 是一条消息在对话流里的角色。前端按它决定长什么样（气泡 / 折起来 / 一行小字）。
type Kind string

const (
	KindHuman  Kind = "human"  // 人说的
	KindAgent  Kind = "agent"  // agent 说的
	KindThink  Kind = "think"  // 思考过程（默认折起来）
	KindTool   Kind = "tool"   // 工具调用（名字 + 一行摘要 + 成没成）
	KindNotice Kind = "notice" // 被打断 / 上下文压缩这类系统事件，一行小字
)

// Msg 是对话流里的一条。
//
// 字段刻意少：这一层只回答「谁说了什么」，工具的完整输入输出**不往手机上送** ——
// 一条 CommandExecution 的 aggregated_output 能有几十 KB，而手机上要看的是「它在干什么」。
type Msg struct {
	// ID 稳定标识：前端的 key，也是增量合并时的去重判据。取自 agent 自己的 uuid /
	// item id，所以同一条重复读到也不会变。
	ID   string `json:"id"`
	Kind Kind   `json:"kind"`
	Text string `json:"text,omitempty"`
	// Tool 工具名（Kind 是 tool 时）。
	Tool string `json:"tool,omitempty"`
	// Meta 工具的一行摘要（跑的命令 / 改的文件 / 搜的词），已经压成一行、掐过长度。
	Meta string `json:"meta,omitempty"`
	// OK 工具成没成。nil = 还不知道（结果还没落盘）—— 前端据此画「正在跑」。
	OK *bool `json:"ok,omitempty"`
	// At 这条的生成时刻（RFC3339）。**只用来显示**，不用来排序（见包注释 ①）。
	At string `json:"at,omitempty"`
	// Ref 这条工具调用的 id（claude 的 `tool_use_id`）。**结果到了之后靠它认回来**，
	// 见 Log.Updates。非工具的消息没有这个。
	Ref string `json:"ref,omitempty"`
	// Ask agent 在问你一个带选项的问题（claude 的 `AskUserQuestion`）。
	//
	// 只有这一种工具调用会把**完整负载**带出来，别的一律只给一行摘要 —— 理由是这一条
	// 人得看见全文才能答（选项有几个、第二个是什么），而那正是「在手机上被问住」时唯一
	// 缺的信息。工具的输入输出本身照旧不往手机上送（见 Msg 的注释）。
	Ask *Ask `json:"ask,omitempty"`
}

// Ask 是一次带选项的提问。字段名跟着 claude 那个工具的输入走（`questions[].options[]`）。
type Ask struct {
	Questions []AskQuestion `json:"questions"`
}

type AskQuestion struct {
	// Header 那个短标签（工具里就叫 header，界面上是问题上面那一行小字）
	Header   string `json:"header,omitempty"`
	Question string `json:"question"`
	// Multi 能多选。**能不能一键作答要看它** —— 多选在 TUI 里是空格勾选再回车，
	// 和单选那套键完全不同，所以多选只显示、不给点。
	Multi   bool        `json:"multi,omitempty"`
	Options []AskOption `json:"options"`
	// Picked 人当时选了哪个（选项的 label）。空 = 还没答。
	//
	// 答过的那张卡要把选中的那个标出来 —— 不标的话往上翻历史看到的是一排干巴巴的选项，
	// 「当时到底定了哪个」得回终端翻（用户报的）。多选是几个用「、」连起来。
	Picked string `json:"picked,omitempty"`
}

type AskOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// Log 是一次读取的结果。
type Log struct {
	Msgs []Msg `json:"msgs"`
	// Sig 这条会话的身份（herdr 报的 session id）。**变了就说明换了会话**
	// （`/clear`、`/resume`、上下文压缩都会换一个新的），前端看到它变就把手上那份丢掉重来。
	Sig string `json:"sig"`
	// Next 下次从这个字节偏移接着读。只指向**完整行的边界**，所以半行不会被读两次。
	Next int64 `json:"next"`
	// Start 这一批是从哪个字节偏移读起的（第一条完整行的位置）。
	// **往上翻更早的历史就靠它**：把它当 `before` 再问一次（见 ReadBefore）。
	Start int64 `json:"start"`
	// More 上面还有更早的内容（Start > 0）。
	More bool `json:"more"`
	// Updates 前面那些消息的结果补丁（见 Update）。
	Updates []Update `json:"updates,omitempty"`
	/*
		Gone **前面送过、但现在认定被撤回的那几条**（消息 id）。

		和 `Updates` 同一个形状、同一个理由：**证据出现在后面那一批里**。撤回的证据是
		「同父的另一条人话拿到了 assistant 回应」，而那条回应往往是下一次增量才读到的 ——
		这时候被撤的那条早就送到浏览器里了。前端只会追加不会删，所以必须指名说「这几条没了」。
	*/
	Gone  []string `json:"gone,omitempty"`
	Agent string   `json:"agent"`
	// File 转录文件名（**只有文件名，不是全路径**）。给的是「我在读哪一份」这个诊断信息，
	// 全路径没必要送到浏览器上。
	File string `json:"file"`
}

// Update 是「**前面某一条**的结果到了」。
//
// # 为什么必须有这个
//
// 回填（工具成没成、提问选了哪个）靠的是 `tool_use_id → 这一次扫描里的下标`，而扫描状态是
// **每批新建的**。流式过程中，工具调用落在前一批、结果落在后一批 —— 后一批里那张表是空的，
// 于是回填不到：工具行永远显示「正在跑」，提问那张卡永远显示「没选」。
// 只有整份重读（刷新页面）才对，而那正是用户报的现象。
//
// 一开始我把这条写成注释里「有意的取舍」放过了 —— 那个判断是错的：它不是「少一点信息」，
// 是**显示的状态和事实相反**。
//
// 所以增量那批里除了新消息，还带上这些补丁；前端拿 `Ref` 在**自己手上那份**里认回那条，
// 打上去。服务端不需要记住任何跨批的状态。
type Update struct {
	// Ref 对应 Msg.Ref（claude 的 tool_use_id）
	Ref string `json:"ref"`
	// OK 这条工具成没成
	OK *bool `json:"ok,omitempty"`
	// Answers 提问的答案（问题 → 选中的那个 label）。
	//
	// 这儿给的是**原样的答案表**而不是「第几个选项」：那条提问的问题文本在前一批里，
	// 服务端这会儿手上没有，而前端手上有 —— 让它自己按问题对上。
	Answers map[string]string `json:"answers,omitempty"`
}

// 定位失败的几种，前端要分开说 —— 「没装 hook」和「读不到文件」的处理方式完全不同。
var (
	// ErrNoSession pane 上没有会话信息，而且按 cwd 也没找着。
	ErrNoSession = errors.New("这个 pane 还没报过会话")
	// ErrAmbiguous 同一个 cwd 下有好几个 agent pane，没装 hook 的话分不出是哪一个。
	// **这种一律不猜**，见包注释。
	ErrAmbiguous = errors.New("同一个目录下开着多个 agent pane，分不出是哪一个")
	// ErrUnsupported 这个 agent 的转录格式还没支持。
	ErrUnsupported = errors.New("还不支持这个 agent 的会话记录")
)

// 首屏从文件尾部往前开多大的窗口。一档不够（尾部正好是一大段工具输出）就翻倍，
// 最多到 Cap —— 再大就不如承认「这份太大了，只给最后这些」。
const (
	windowStart = 1 << 20 // 1MB
	windowCap   = 16 << 20
	// tailMsgs 首屏最多给多少条。手机上一屏放不下十条，给多了只是白传。
	tailMsgs = 200
	// backWindow 往上翻一次读多少字节。
	//
	// 比首屏那一窗小得多，而且**不截条数** —— 这样返回的消息和 Start 这个偏移严格对应，
	// 人点一次「看更早的」拿到的就是紧挨着当前最上面那条的一段。256KB 的 claude JSONL
	// 实测出几十条，在手机上是好几屏。
	backWindow = 256 << 10
	// lookBack 增量那一拍**为了认撤回**多往前看多少字节（见 Read 里那段）。
	// 撤回的总是「刚发出去那条」，覆盖最近几十条记录就够；取大了每拍白解析。
	lookBack = 128 << 10
	// minTail 一窗里至少要出几条才算够。
	//
	// **这个门槛不能是 tailMsgs。** 写成「凑满 200 条才算够」的话，一份 2.3MB 的转录里
	// 头一窗出了 181 条就判成不够，于是一路翻倍到**把整个文件读完**（实测踩到了）——
	// 那正是这个窗口要避免的事，而且是在跑着 agent 的那台机器上读十几兆。
	// 窗口的目的是「拿到最近这一段」，30 条在手机上已经是翻好几屏；真出不到 30 条
	// （尾部正好是一大段工具输出）才值得再往前挖一窗。
	minTail = 30
)

// Source 是一份定位好的转录文件。
type Source struct {
	Path  string
	Agent string
	Sig   string
}

// Read 读一份转录。
//
// from > 0 时是**增量**：从那个偏移接着读到文件尾，返回这一段里的消息。from == 0 是首屏：
// 从文件尾部开一个窗口往前读，只给最后 tailMsgs 条。
//
// 两种情况会退回首屏（返回的 More 说明截过）：`from` 比文件还大（文件被换掉了 ——
// `/clear` 之后 claude 写的是**另一个** session 文件，但 codex 的 rollout 是同一份，
// 而 herdr 报的 sig 会变），或者 `from` 落在文件中间但那一段已经被截断。
func Read(src Source, from int64) (*Log, error) {
	f, err := os.Open(src.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := st.Size()

	out := &Log{
		Sig:   src.Sig,
		Agent: src.Agent,
		File:  fileBase(src.Path),
		Next:  size,
	}
	if size == 0 {
		out.Msgs = []Msg{}
		return out, nil
	}

	parse := parserFor(src.Agent)
	if parse == nil {
		return nil, fmt.Errorf("%w: %s", ErrUnsupported, src.Agent)
	}

	// 增量：偏移还在文件里就从那儿接着读。**没读到东西也不是错** ——
	// agent 正在想，文件就是不动（实测能 15 秒零字节）。
	if from > 0 && from <= size {
		msgs, ups, _, _, end, err := scan(f, from, size, parse, 0)
		if err != nil {
			return nil, err
		}
		/*
			**认撤回要多往前看一段。** 撤回的证据是「同父的另一条人话拿到了回应」，而被撤的
			那条**在增量这一窗之前** —— 只看这一窗的话它压根不在，说不出它的 id，前端屏幕上
			那条就一直挂着。所以另做一次**只为认撤回**的扫描，从 `from - lookBack` 起。
			**刻意不把回看那段的消息并进 Msgs**：前端拿「增量批次里冒出人话」当「投稿落地了」
			的判据（`dropLanded` 的 fifo），重叠送旧人话会把还没落地的回显误撤掉。
		*/
		lb := from - lookBack
		if lb < 0 {
			lb = 0
		}
		if _, _, g, _, _, err2 := scan(f, lb, size, parse, lb); err2 == nil {
			out.Gone = g
		}
		// 增量那一段的 Start 没有意义（人手上已经有更早的了），照旧把首屏那次的值留给前端管。
		// **Updates 是这一段最要紧的东西之一**：工具调用常常落在上一段里（见 Update）。
		out.Msgs, out.Updates, out.Next = msgs, ups, end
		return out, nil
	}

	// 首屏：从尾部开窗口。一档连 minTail 条都出不来就翻倍 —— 尾部可能正好是一整段
	// 几十 KB 的工具输出，那一窗里一条人话都没有。
	for w := int64(windowStart); ; w *= 4 {
		start := size - w
		if start < 0 {
			start = 0
		}
		msgs, _, gone, begin, end, err := scan(f, start, size, parse, start)
		if err != nil {
			return nil, err
		}
		enough := len(msgs) >= minTail || start == 0 || w >= windowCap
		if !enough {
			continue
		}
		// 截掉前面那些时 Start **还是这一窗的起点**，于是往上翻一次会和手上这批有重叠 ——
		// 那是有意的：截掉的那几条的字节偏移我们并不知道，而前端按 id 去重，重叠没有代价。
		// 反过来（把 Start 报成截断处）就会**漏掉**中间那几条，而且完全看不出来。
		if len(msgs) > tailMsgs {
			msgs = msgs[len(msgs)-tailMsgs:]
		}
		out.Msgs, out.Next, out.Start, out.Gone = msgs, end, begin, gone
		out.More = begin > 0
		return out, nil
	}
}

// ReadBefore 往上翻：读 before 之前那一窗。
//
// 前端拿上一批的 `Start` 当 before 再问一次，把结果**接在手上那批的前面**。和首屏那次不一样，
// 这儿**不截条数** —— 返回的消息和 Start 严格对应，人点一次「看更早的」拿到的就是紧挨着
// 最上面那条的一段。
//
// before <= 0 就是已经到文件头了，给空结果（不是错）。
func ReadBefore(src Source, before int64) (*Log, error) {
	f, err := os.Open(src.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := st.Size()

	out := &Log{Sig: src.Sig, Agent: src.Agent, File: fileBase(src.Path), Next: size, Msgs: []Msg{}}
	if before <= 0 {
		return out, nil
	}
	// before 比文件还大（文件被换掉了）就当到头了，别去读一段不存在的区间。
	if before > size {
		return out, nil
	}
	parse := parserFor(src.Agent)
	if parse == nil {
		return nil, fmt.Errorf("%w: %s", ErrUnsupported, src.Agent)
	}

	// **一窗读不出东西就继续往前扩。**
	//
	// 真机上踩到了（6.3MB 的转录）：往前翻到第二页就原地打转 —— 回来的 `start` 和问过去的
	// `before` 一模一样、0 条，前端拿它再问一次，永远停在那儿。表现和「翻不到历史」一样。
	//
	// 原因是那一窗**整个落在一行超长的行里**（转录里有几百 KB 一行的工具输出 / 附件）：
	// 窗口起点是按字节切的，落在行中间，那半行要整条丢掉（不然是坏 JSON）—— 于是这一窗
	// 一条消息都没有，而「读到哪儿了」也没往前走。
	//
	// 所以判据不是「读完了这一窗」，而是**「要么读到了东西，要么到了文件头」**；两者都没有
	// 就把窗口翻大再来一遍。翻到上限还是空的话，也要把 Start 报成这一窗的起点（那至少是
	// 往前走了），别让前端卡住。
	var (
		msgs  []Msg
		gone  []string
		begin int64
	)
	for w := int64(backWindow); ; w *= 4 {
		start := before - w
		if start < 0 {
			start = 0
		}
		var err error
		msgs, _, gone, begin, _, err = scan(f, start, before, parse, start)
		if err != nil {
			return nil, err
		}
		if start == 0 {
			begin = 0 // 到文件头了：没有半行要丢，上面也没有更早的了
			break
		}
		if len(msgs) > 0 {
			break
		}
		if w >= windowCap {
			begin = start // 读不出东西，但至少往前挪了一整窗，别让前端原地打转
			break
		}
	}
	out.Msgs, out.Start, out.Gone = msgs, begin, gone
	out.More = begin > 0
	// **Next 不能动。** 这一批是往前翻出来的，前端手上那个「下次从哪儿接着读」指的是文件尾，
	// 拿这儿的值去盖它的话增量就会从中间某处重读一大段（表现是消息成片重复）。
	// 所以这儿给的 Next 是文件当前大小，前端对往前翻的响应**只取 Msgs / Start / More**。
	if out.Msgs == nil {
		out.Msgs = []Msg{}
	}
	return out, nil
}

// scan 从 start 读到 end，按行喂给 parse。
//
// skipPartial > 0 时**丢掉第一行**：窗口起点是按字节切的，落在一行中间，那半行既不是合法
// JSON 也不该被当成一条消息。start == 0 时不能丢 —— 那是真正的第一行。
//
// 返回的第二个值是**最后一个完整行的结束偏移**。文件正在被写时最后一行可能只有一半，
// 把它算进 Next 的话下次就从半行中间接着读，**那一条消息从此永远丢了**（而且不报错）。
func scan(f *os.File, start, end int64, parse parseFunc, skipPartial int64) ([]Msg, []Update, []string, int64, int64, error) {
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return nil, nil, nil, start, start, err
	}
	rd := io.LimitReader(f, end-start)

	var (
		msgs  []Msg
		st    = newState()
		off   = start
		done  = start
		buf   []byte
		tmp   = make([]byte, 64<<10)
		first = skipPartial > 0
		// begin 第一条**完整**行从哪个偏移开始。丢掉半行时它不等于 start ——
		// 往上翻靠这个值当下一次的 before，差一点就会把那半行再切一次。
		begin = start
	)
	for {
		n, rerr := rd.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			for {
				i := indexByte(buf, '\n')
				if i < 0 {
					break
				}
				line := buf[:i]
				buf = buf[i+1:]
				off += int64(i) + 1
				done = off
				if first {
					first = false // 半行，丢掉
					begin = off   // 完整内容从这儿起
					continue
				}
				if len(strings.TrimSpace(string(line))) == 0 {
					continue
				}
				parse(line, st, &msgs)
			}
		}
		if rerr != nil {
			if rerr == io.EOF {
				break
			}
			return dropRetracted(st, msgs), st.ups, st.gone, begin, done, rerr
		}
	}
	kept := dropRetracted(st, msgs)
	return kept, st.ups, st.gone, begin, done, nil
}

// parseFunc 解析一行，把认出来的消息 append 到 out。
//
// 传 state 进来是因为两家都有**跨行的关系**要记：claude 要按 tool_use_id 把后来的
// tool_result 回填到前面那条工具调用上；codex 要认出同一个 turn。
type parseFunc func(line []byte, st *state, out *[]Msg)

// state 是解析时跨行的那点记忆。
type state struct {
	// tool 记 tool_use_id → 在 out 里的下标，用来把结果回填到调用上。
	// **只在这一次 scan 里有效** —— 增量读时调用在上一段里，回填不到，那条工具就一直是
	// 「正在跑」。这是有意的取舍：为此把整个文件的索引留在内存里不值当，而前端下次整份
	// 刷新（sig 变了）时自然就对了。
	tool map[string]int
	// ups 这一批里见到的结果补丁（见 Update）。**不管那条工具在不在这一批里都攒** ——
	// 在的话回填 + 补丁都做（幂等），不在的话补丁就是唯一的路。
	ups []Update

	/*
		下面这几个是认「被撤回的消息」用的（只有 claude；codex 的 rollout 是平的）。

		**转录是树**（`parentUuid` → `uuid`，见 docs/dev/CHAT.md §4）。人在 TUI 里按 Esc
		撤回一条**还没被回复**的消息时，那条照旧留在文件里，只是会话从它的父亲那儿另开一支。

		判据**不能是「它不在最后那条的链上」** —— 我按那个做过一版，把还活着的消息也删了
		（排队中的消息挂在入队那刻的叶子上，被打断之后后面的记录自然绕过它；一条 assistant
		带两个工具结果时那两条 `user` 也同父）。**判据是语义的**：同父的几条人话里，
		**有 assistant 后代的那条是活的，没有的才是被撤回的**。全机最近 12 个会话实测
		6 组同父人话，**每组恰好 1 条有 assistant 后代** —— 那条就是真发生过的那次。
	*/
	// parent uuid → parentUuid（不收 sidechain：那是另一条分支）
	parent map[string]string
	// kids parentUuid → 孩子们的 uuid，按文件顺序
	kids map[string][]string
	// asst 这个 uuid 是不是一条 assistant 记录
	asst map[string]bool
	// said 这个 uuid 是不是「一条人话」（`user` 记录里人真说的话，不含工具结果、
	// 不含排队中那种 attachment —— 那两类都不参与这个判断）
	said map[string]bool
	// seq uuid → 文件顺序（挑「留哪一条」时按它定先后）
	seq map[string]int
	// gone 认定被撤回、因此没送出去的那几条消息 id（见 Log.Gone）
	gone []string
}

// 这儿**刻意不做「连着两条一样就去重」**。agentwatch 那边有这么一条，但它的前提是
// 读屏会重复读到同一屏；转录是纯 append 的事件流，每一行都是一件真发生过的事 ——
// 而「继续」连说两遍是再正常不过的用法，去重就是把人真说过的话吞掉。

func newState() *state {
	return &state{
		tool:   map[string]int{},
		parent: map[string]string{},
		kids:   map[string][]string{},
		asst:   map[string]bool{},
		said:   map[string]bool{},
		seq:    map[string]int{},
	}
}

/*
dropRetracted：把「在 TUI 里被撤回的那几条人话」挑掉。

判据（在真数据上验过，见 state 里那段）：**同一个父亲下有多条人话时，有 assistant 后代的
那条是活的，没有的是被撤回的。** 后代要顺着树往下找 —— 撤回那条下面仍然挂着自动附件
（`total_tokens_reminder` 的 parent 就是它，真机上核过），所以「有没有孩子」不能当判据，
得看有没有 **assistant** 后代。

三条保守处理，宁可多留也不错杀（上一版就是错杀了才回滚的）：

  - **没有兄弟就不动**。最新那条永远没有兄弟，所以刚发出去的消息绝不会被误删。
  - **一组里没有任何一条有 assistant 后代时整组留着**。那说明这一轮还没开始（人连发几条、
    agent 还没动），不是撤回。
  - **只认 `user` 记录里的人话**。排队中的那种是 attachment（`queued_command`），
    工具结果虽然也是 `user` 角色但不算人话 —— 这两类压根不参与，所以上一版那两个错杀在
    这儿是结构上不可能的。
*/
func dropRetracted(st *state, msgs []Msg) []Msg {
	if len(msgs) == 0 || len(st.said) == 0 {
		return msgs
	}
	// 顺着树找有没有 assistant 后代
	var hasAsst func(uuid string, depth int) bool
	hasAsst = func(uuid string, depth int) bool {
		if depth > 64 { // 防环 / 防太深
			return false
		}
		for _, k := range st.kids[uuid] {
			if st.asst[k] || hasAsst(k, depth+1) {
				return true
			}
		}
		return false
	}

	dead := map[string]bool{}
	for _, sibs := range st.kids {
		// 这个父亲下的人话有哪几条
		var hs []string
		for _, u := range sibs {
			if st.said[u] {
				hs = append(hs, u)
			}
		}
		if len(hs) < 2 {
			continue // 没有兄弟：不动（最新那条走的就是这一支）
		}
		live := ""
		for _, u := range hs {
			if hasAsst(u, 0) {
				// 有多条都有后代（理论上不该出现）就认**最后**那条，前面的照旧留着
				if live == "" || st.seq[u] > st.seq[live] {
					live = u
				}
			}
		}
		if live == "" {
			continue // 一条都没被回复过：这一轮还没开始，整组留着
		}
		for _, u := range hs {
			if u != live {
				dead[u] = true
			}
		}
	}
	if len(dead) == 0 {
		return msgs
	}
	out := msgs[:0:0]
	for _, m := range msgs {
		if dead[m.ID] {
			st.gone = append(st.gone, m.ID)
			continue
		}
		out = append(out, m)
	}
	return out
}

func parserFor(agent string) parseFunc {
	switch agent {
	case "claude":
		return parseClaude
	case "codex":
		return parseCodex
	}
	return nil
}

// oneLine 把多行压成一行、掐到 n 个字符（按 rune 数，别把中文切成半个）。
// 工具摘要和标题都过这儿 —— 手机上一行就是一行。
func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// clip 掐正文长度。比 oneLine 松得多（正文要保留换行），但也不能不管 ——
// 一条 assistant 正文正常几百字，而工具输出塞进 text 的话能有几十 KB。
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "\n…（还有 " + itoa(len(r)-n) + " 个字，回终端里看）"
}

func fileBase(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}

func itoa(n int) string { return fmt.Sprintf("%d", n) }

// jsonStr 从 map 里取一个字符串字段，不在 / 不是字符串就给空。
// 转录格式是**别人的**，字段随时可能变形状 —— 一律软失败，别 panic 也别整份读不出来。
func jsonStr(m map[string]json.RawMessage, k string) string {
	raw, ok := m[k]
	if !ok {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return s
}
