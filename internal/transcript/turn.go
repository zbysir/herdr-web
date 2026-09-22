package transcript

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"time"
)

// 「这一轮跑了多久 / 花了多少 token」—— chat 里那条「在跑…」后面括号里的东西，
// 照着 claude 自己那条状态行来（`✳ Ebbing… (3m 40s · ↓ 10.3k tokens · thinking with xhigh effort)`）。
//
// # 为什么时间在服务端算
//
// 转录里的时间戳是**跑 agent 那台机器**写的，而看页面的是手机 —— 两边时钟差几分钟是常事
// （手机没对时、时区没关系但偏移有）。在前端拿 `Date.now()` 减那个时间戳，得到的是一个
// 看着完全像真的错数字。所以这儿直接给「多少秒」，前端只负责把它往前走。
//
// # 为什么是从「上一条人话」起算
//
// 一轮 = 人说一句、agent 干到停。转录里没有「turn 开始」这种标记，但**最后一条人话**就是
// 这一轮的起点（工具结果虽然也记成 user 角色，但那是数组形态的 content，认得出来，
// 见 claude.go 的第 ② 条）。
//
// 不拿 herdr 的状态变化时间（`internal/agentwatch` 那份）当起点，尽管它现成：那是「上次
// **状态**变过」的时刻，而一轮里状态会抖（跑工具时 working/idle 来回、短任务压根不报
// working，见 CLAUDE.md），抖一次这个数就缩回去一次。
//
// # 代价：一次有界的回扫
//
// 每拍都要重算（token 一直在涨），所以从文件尾开一个窗口往前扫。**只在 agent 真的在跑时
// 才算**（见 server/chatapi.go）—— 闲着的时候这个数没人看，而那台机器上同时开着几十个
// pane，白扫是白扫。

// turnWindow 回扫多少字节找「上一条人话」。
//
// 一轮的量级实测是几十到一两百 KB（工具输出占大头），256KB 够覆盖绝大多数；找不到就再翻一次，
// 两次都找不到就**什么都不给**（宁可不显示，别给一个偏小的数 —— 那比没有更误导）。
const (
	turnWindow = 256 << 10
	turnCap    = 2 << 20
)

// Turn 是这一轮的几个数。零值 = 算不出来（那时前端只画状态词，不画后面那个括号）。
type Turn struct {
	// Secs 这一轮**到现在**跑了多久（秒）。在跑的时候看它。**服务端算的**，见本文件开头。
	Secs int `json:"secs,omitempty"`
	// Ran 这一轮从人说话到 agent 最后一次落笔用了多久（秒）。**跑完之后看它** ——
	// 那时候 Secs 还在往上涨（它算到「现在」），拿它显示就成了「跑了 20 分钟」，
	// 而其实人只是二十分钟没再说话。
	Ran int `json:"ran,omitempty"`
	// DoneAt agent 最后一次落笔的时刻（RFC3339）。前端按**自己的时区**格式化成 `14:41`。
	//
	// 这儿给的是**绝对时刻**而不是「多少秒前」：显示一个钟点不需要做差，所以手机和这台
	// 机器的时钟偏差不影响它（和 Secs 那个正相反，那个必须服务端算）。
	DoneAt string `json:"doneAt,omitempty"`
	// Idle agent 最后一次落笔到**现在**多少秒。**服务端算的**（和 Secs 同理：手机和这台
	// 机器的时钟差几分钟是常事，前端拿 DoneAt 减 Date.now() 会得出一个看着像真的错数字）。
	//
	// 界面上它答的是「我现在看的这段，是不是已经旧了」。有两种情况非它不可：
	//
	//   - codex 的 `/clear`：**不新建 rollout、旧文件也不再长**，而 herdr 的
	//     `agent_session` 要等新会话写盘才更新（hook 挂在 SessionStart 上，而 codex 的
	//     clear 不触发它）—— 这段窗口里 chat 显示的是**已经作废的上一段对话**，而数据
	//     层面没有任何东西能认出这件事（实测：没有结束标记，`.codex` 下也没有「当前活跃
	//     会话」的记录）。能做的就是把「最后更新是多久以前」摆出来让人自己判断。
	//   - 回头看一个昨天的会话：同一条信息，顺带也有用。
	Idle int `json:"idle,omitempty"`
	// Tokens 这一轮吐出来多少 token（只算输出，和 claude 自己那条状态行的 `↓` 一致）。
	Tokens int `json:"tokens,omitempty"`
	// Effort 这一轮的思考档位（claude 的 `effort`，如 `xhigh`）。拿不到就空着。
	Effort string `json:"effort,omitempty"`
	Model  string `json:"model,omitempty"`
}

// TurnStat 算「这一轮」的几个数。算不出来给 nil（不是错）。
func TurnStat(src Source) (*Turn, error) {
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
	if size == 0 {
		return nil, nil
	}
	acc := turnAccFor(src.Agent)
	if acc == nil {
		return nil, nil
	}

	for w := int64(turnWindow); ; w *= 4 {
		start := size - w
		if start < 0 {
			start = 0
		}
		t, found, err := turnScan(f, start, size, start > 0, acc)
		if err != nil {
			return nil, err
		}
		// 找到了那条人话就算成了；没找到但已经到文件头，那就是这份转录里压根没有人话。
		if found || start == 0 {
			if !found {
				return nil, nil
			}
			return t, nil
		}
		if w >= turnCap {
			// 翻到上限还没找到起点：**什么都不给**。这一轮真有那么长的话，
			// 给一个「从窗口起点算」的数就是偏小，而人会当它是真的。
			return nil, nil
		}
	}
}

// turnAcc 一行一行喂进来，自己攒这一轮的数。
//
// 见到人话就**清零重来**（那是新一轮的起点），所以扫完之后攒着的正好是「最后一条人话之后」
// 的那些 —— 一趟正扫就够，不用先找起点再回头。
type turnAcc interface {
	// line 喂一行。返回真 = 这一行是人话（= 一轮的起点）。
	line(raw []byte) bool
	// turn 把攒下来的结果拿出来，start 是那条人话的时间。
	turn() *Turn
}

func turnAccFor(agent string) turnAcc {
	switch agent {
	case "claude":
		return &claudeTurn{}
	case "codex":
		return &codexTurn{}
	}
	return nil
}

func turnScan(f *os.File, start, end int64, skipPartial bool, acc turnAcc) (*Turn, bool, error) {
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return nil, false, err
	}
	rd := io.LimitReader(f, end-start)
	var (
		buf   []byte
		tmp   = make([]byte, 64<<10)
		first = skipPartial
		found bool
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
				if first {
					first = false // 半行，丢掉（窗口起点是按字节切的）
					continue
				}
				if len(strings.TrimSpace(string(line))) == 0 {
					continue
				}
				if acc.line(line) {
					found = true
				}
			}
		}
		if rerr != nil {
			if rerr == io.EOF {
				break
			}
			return nil, found, rerr
		}
	}
	return acc.turn(), found, nil
}

/* ------------------------------------------------------------------ claude */

type claudeTurn struct {
	startAt time.Time
	// lastAt agent 最后一次落笔的时刻。**这一轮的时长是它减 startAt**，不是「现在减 startAt」。
	lastAt time.Time
	tokens int
	effort string
	model  string
}

func (c *claudeTurn) line(raw []byte) bool {
	var l struct {
		Type        string          `json:"type"`
		Timestamp   string          `json:"timestamp"`
		IsSidechain bool            `json:"isSidechain"`
		Effort      string          `json:"effort"`
		Message     json.RawMessage `json:"message"`
	}
	if json.Unmarshal(raw, &l) != nil || l.IsSidechain {
		return false
	}
	switch l.Type {
	case clTypeUser:
		// 只有**字符串** content 才是人话（数组那种是工具结果，见 claude.go 的第 ② 条）。
		// 不分的话每条工具结果都会把这一轮的计时清零，屏幕上那个秒数永远停在几秒。
		var m struct {
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(l.Message, &m) != nil {
			return false
		}
		var s string
		if json.Unmarshal(m.Content, &s) != nil {
			return false
		}
		*c = claudeTurn{startAt: parseAt(l.Timestamp)}
		return true

	case clTypeAssistant:
		var m struct {
			Model string `json:"model"`
			Usage struct {
				Output int `json:"output_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal(l.Message, &m) == nil {
			c.tokens += m.Usage.Output
			if m.Model != "" {
				c.model = m.Model
			}
		}
		if l.Effort != "" {
			c.effort = l.Effort
		}
		if at := parseAt(l.Timestamp); !at.IsZero() {
			c.lastAt = at
		}
	}
	return false
}

func (c *claudeTurn) turn() *Turn {
	return &Turn{
		Secs: since(c.startAt), Ran: span(c.startAt, c.lastAt), DoneAt: stamp(c.lastAt), Idle: since(c.lastAt),
		Tokens: c.tokens, Effort: c.effort, Model: c.model,
	}
}

/* ------------------------------------------------------------------- codex */

type codexTurn struct {
	startAt time.Time
	lastAt  time.Time
	// base 是那条人话之前最后见到的累计 token 数。
	//
	// codex 的 `token_count` 给的是**整个会话的累计**，不是这一轮的 —— 所以这一轮 =
	// 最新那个累计减去起点那个累计。直接拿累计显示的话，一轮刚开始就是「几十万 token」。
	base   int
	latest int
	model  string
}

func (c *codexTurn) line(raw []byte) bool {
	var l cxLine
	if json.Unmarshal(raw, &l) != nil || l.Type != "event_msg" {
		return false
	}
	var p struct {
		Type string          `json:"type"`
		Item json.RawMessage `json:"item"`
		Info struct {
			Total struct {
				Output int `json:"output_tokens"`
			} `json:"total_token_usage"`
		} `json:"info"`
	}
	if json.Unmarshal(l.Payload, &p) != nil {
		return false
	}
	switch p.Type {
	case "token_count":
		c.latest = p.Info.Total.Output
		if at := parseAt(l.Timestamp); !at.IsZero() {
			c.lastAt = at
		}
	case "item_completed":
		var it struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(p.Item, &it) == nil && it.Type == "UserMessage" {
			*c = codexTurn{startAt: parseAt(l.Timestamp), base: c.latest, latest: c.latest}
			return true
		}
	case "thread_settings_applied":
		var s struct {
			Settings struct {
				Model string `json:"model"`
			} `json:"thread_settings"`
		}
		if json.Unmarshal(l.Payload, &s) == nil && s.Settings.Model != "" {
			c.model = s.Settings.Model
		}
	}
	return false
}

func (c *codexTurn) turn() *Turn {
	n := c.latest - c.base
	if n < 0 {
		n = 0 // 累计变小了（换了会话之类）—— 给 0 而不是负数
	}
	return &Turn{
		Secs: since(c.startAt), Ran: span(c.startAt, c.lastAt), DoneAt: stamp(c.lastAt), Idle: since(c.lastAt),
		Tokens: n, Model: c.model,
	}
}

/* -------------------------------------------------------------------- 小工具 */

func parseAt(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// span 两个时刻之间几秒。任一个缺、或者倒过来了，都给 0（宁可不显示）。
func span(from, to time.Time) int {
	if from.IsZero() || to.IsZero() {
		return 0
	}
	d := int(to.Sub(from).Seconds())
	if d < 0 {
		return 0
	}
	return d
}

// stamp 编成 RFC3339 给前端按它自己的时区格式化。零值给空串。
func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

// since 从那一刻到现在几秒。
//
// 算不出来（没时间戳）给 0，**负数也给 0** —— 时间戳比现在晚的情况真会出现
// （行内那个 `timestamp` 记的是生成时刻，而同一次 flush 里的几行能差十几秒，见包注释 ①）。
// 负数显示出来是 `-3s`，看着像坏了。
func since(t time.Time) int {
	if t.IsZero() {
		return 0
	}
	d := int(time.Since(t).Seconds())
	if d < 0 {
		return 0
	}
	return d
}
