package agentwatch

import (
	"log"
	"strings"
	"time"

	"github.com/zbysir/herdr-web/internal/herdr"
)

// 提示（notice）：agent 从「在跑」变成「轮到你了」的时候，攒一条带**那段话**的记录，
// 前端轮询取走，弹到右上角。
//
// 为什么是攒着让前端来取，不是推：herdr-web 这一侧本来就没有到浏览器的推送通道
// （只有 /pty 那条二进制流，塞 JSON 进去等于给终端加一套协议），而这个界面每 500ms
// 就为发件箱打一次 socket —— 再加一条几秒一次的 GET 微不足道。见 README「是轮询，不是推送」。
//
// 只报**两种**变化（worthNotice）：
//   - `blocked` → 等你回答。这是最该弹的一种：agent 停在那儿问话，你不看就一直停着。
//   - `working` → `idle` / `done` → 跑完了。
//
// **`→ working` 不报**：那多半是你自己刚投进去的，弹一下纯属回声。
//
// 状态从哪儿来见 watch.go（全局 `pane.updated`）。这里只加两件事：**防抖**（状态会抖，
// 抖回去的不算变化）和**读屏抽话**（extract.go）。

// 状态变了之后等多久再读屏。
//
// 两个理由都是实测的：claude / codex 干活时状态会短暂抖回 idle（立刻读会读到上一个任务的
// 尾巴，还会为一次抖动弹一个假的「跑完了」）；而 `pane.read` 的快照本身就有一帧延迟
// （HERDR-API.md）。2.5s 是 herdr-sight 那边试出来的值，这里沿用。
//
// 代价是提示比状态点晚 2.5 秒 —— 值得：弹错一次的成本比晚两秒高得多。
//
// 是 var 不是 const：单测把它调到几十毫秒，否则每个用例都要真等 2.5 秒。
var settleNotice = 2500 * time.Millisecond

// 留多少条提示。前端拿 `since` 增量取，正常只会取到最近一两条；这个环是为「手机锁屏
// 半小时回来」准备的 —— 再多就没意义了（那时候要看的是面板一览，不是补看弹窗）。
const keepNotices = 50

// 弹窗只有右上角一小块地方，抽到的话超过这个就截断（后面缀一个 …）。
const noticeLines, noticeChars = 12, 600

// 读几行屏。整屏就是几十行，给 200 是留给「一屏被 wrap 撑长」的余量。
const readLines = 200

// Notice 是一条提示。
//
// **Pane 和 Term 两个都带**：Pane（`w9:p1`）是点一下要跳过去的地址，而 Term
// （`term_659755cf342fb43`）跟着终端进程走，pane 一开一关 pane_id 就会被重新分配给别人 ——
// 前端要「同一个 agent 的旧提示换成新的」，认的必须是 Term。
type Notice struct {
	Seq    uint64 `json:"seq"` // 自增号，前端拿它当 since 做增量
	At     int64  `json:"at"`  // unix 毫秒
	Pane   string `json:"pane"`
	Term   string `json:"term"`
	Agent  string `json:"agent"`  // claude / codex
	Status string `json:"status"` // blocked（等你）/ idle / done（跑完了）
	Title  string `json:"title"`  // agent 自己写的会话标题（「图片识别」那种）
	// Text 是抽出来的那段话。**可能是空的**（读屏失败、或者一屏全是装饰行）——
	// 那时候前端只报状态，不硬凑一句话出来。
	Text string `json:"text"`
}

// worthNotice 说这次状态变化值不值得弹一下。
//
// `blocked`（等你回答）和 `done`（跑完了）**不管从哪儿来都算** —— 这两个是 herdr 明确
// 表态的「轮到你了」，从哪个状态跳过来不影响这件事成不成立。
//
// **`done` 一开始要求「必须从 working 来」，那是错的**：真机上戳一个 agent 说 hi，herdr
// 的状态是 `idle → done`，中间**压根没报过 working**（它的屏幕识别是保守的，短任务来不及
// 判成在跑）。于是短任务一条提示都弹不出来 —— 这个 bug 是端到端试出来的，单测里那条
// 「working → done」用例照样绿。
//
// `idle` 是**歇着**，不是「干完了」，所以只在从 working 过来时才算一次「跑完了」：不加这个
// 限制的话，你回答完一个提问（blocked → idle）也会收到一条「跑完了」，而那一刻它其实刚要
// 开始干活。
func worthNotice(old, cur string) bool {
	if cur == "blocked" || cur == "done" {
		return true
	}
	return old == "working" && cur == "idle"
}

// settled 说防抖等完之后这一下还该不该报，报的又是哪个状态（空字符串 = 不报）。
//
// want 是当初挂上的那个状态，cur 是这会儿的。分四种：
//
//   - **又在跑了** → 不报。这就是防抖的全部意义：working↔idle 的抖动实测只隔 100ms，
//     每抖一次报一条的话，一个长任务能刷出十几条假的「跑完了」。
//   - **现在是 blocked** → 报 blocked。它停在那儿等你，这是最该说的一种。
//     （比如 done 之后紧接着又问你话，那就报后面这个。）
//   - **当初是 blocked、现在不是了** → 不报。那是**你自己刚回答完**它才不 blocked 的，
//     再弹一条「跑完了」是句假话。
//   - 剩下的（done ↔ idle 这种收尾抖动）→ 报**现在**这个。原来这儿是「和当初不一样就
//     整条丢掉」，于是 `done` 之后 2.5 秒内落到 `idle` 的那些真·跑完了全被吞了。
func settled(want, cur string) string {
	switch {
	case cur == "" || cur == "working":
		return ""
	case cur == "blocked":
		return "blocked"
	case want == "blocked":
		return ""
	default:
		return cur
	}
}

// seed 记下「开工那一刻屏幕上最后一段话是什么」。
//
// 为什么要它：`lastText` 平时是「上次弹过的那条」，可 herdr-web 刚起来时它是空的 ——
// 那时候你投一句又取消，emit 拿不到东西比，照样会弹一条旧回答。所以 agent **第一次**
// 开工时先读一屏垫底，之后就一直有得比了。
//
// 只在没有底的时候读，所以一个终端最多读这么一次；working↔idle 那种 100ms 的抖动
// （实测有）不会反复触发。
func (w *Watcher) seed(p herdr.Pane) {
	w.mu.Lock()
	_, ok := w.lastText[p.TerminalID]
	if ok || w.seeding[p.TerminalID] {
		w.mu.Unlock()
		return
	}
	w.seeding[p.TerminalID] = true
	w.mu.Unlock()

	text, err := w.c.ReadText(p.PaneID, "visible", readLines)
	body := ""
	if err == nil && strings.TrimSpace(text) != "" {
		body = extract(text, "idle", noticeLines, noticeChars)
	}

	w.mu.Lock()
	if _, ok := w.lastText[p.TerminalID]; !ok && body != "" {
		w.lastText[p.TerminalID] = body
	}
	delete(w.seeding, p.TerminalID)
	w.mu.Unlock()
}

// arm 挂一条待发的提示：记下「要报哪个状态」，没有收集协程在跑就起一个。
//
// pend 存的是**状态**而不是 bool：防抖那 2.5 秒里状态可能又变了一次
// （blocked → done 是常见的一串），协程醒来要报的是**最后那个**。
func (w *Watcher) arm(p herdr.Pane) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pend[p.TerminalID] = p.AgentStatus
	if w.busy[p.TerminalID] {
		return // 已经有一个在等了，它醒来会读到新的 pend
	}
	w.busy[p.TerminalID] = true
	go w.collect(p.TerminalID)
}

// collect 等状态稳下来，读一屏，抽出那段话，攒成一条提示。
//
// 循环是为了「防抖期间又变了一次」：那一轮报完（或者丢掉）之后如果又有新的 pend，
// 接着走一轮，而不是收工 —— 收工的话那次变化就永远没人报了。
func (w *Watcher) collect(term string) {
	for {
		time.Sleep(settleNotice)

		w.mu.Lock()
		want := w.pend[term]
		delete(w.pend, term)
		cur := w.status[term]
		p := w.pane[term]
		w.mu.Unlock()

		if want != "" && p.PaneID != "" {
			if st := settled(want, cur); st != "" {
				w.emit(p, st)
			} else {
				// 丢掉的这一下值得留一行：提示「该弹没弹」查起来没有别的抓手
				log.Printf("提示：%s 的 %s 在防抖里变成了 %s，这条不弹", p.PaneID, want, cur)
			}
		}

		w.mu.Lock()
		if w.pend[term] == "" {
			delete(w.busy, term)
			w.mu.Unlock()
			return
		}
		w.mu.Unlock() // 防抖期间又变了一次，接着盯
	}
}

// emit 读屏 + 抽话 + 入环。读不到就带空 Text 照样发一条 —— 「那个 agent 在等你」这件事
// 本身就值得弹，抽不出话不该把提示也一起吞掉。
//
// **抽出来的话和上次一模一样就不弹**（`lastText`）。这一条是用出来的：你投了一句话又按
// **Esc 取消**，herdr 那边就是一次干干净净的 `working → idle`，和「跑完了」在状态上毫无
// 区别；而屏幕上 claude **不留任何「被打断」的记号**（实测抓屏确认：最后一个 `⏺` 块还是
// 上一轮的回答，你那句话被放回输入框）。于是弹出来的是一段**旧回答**，还挂着「跑完了」。
//
// 「这一趟有没有新东西」才是真正的判据：一个字都没变，就没有什么可告诉你的。
func (w *Watcher) emit(p herdr.Pane, status string) {
	text, err := w.c.ReadText(p.PaneID, "visible", readLines)
	if err != nil {
		log.Printf("提示：读 %s 的屏失败（这条提示只报状态，没有内容）: %v", p.PaneID, err)
	}
	body := ""
	if s := strings.TrimSpace(text); s != "" {
		body = extract(text, status, noticeLines, noticeChars)
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	// 和上次一模一样 = 这一趟什么都没产出（多半是被 Esc 取消了），不弹。
	// 空的不比：那是读屏失败，两次都失败不代表「没变化」。
	if body != "" && body == w.lastText[p.TerminalID] {
		log.Printf("提示：%s 这一趟没有新输出（多半是取消 / 打断了），不弹", p.PaneID)
		return
	}
	w.lastText[p.TerminalID] = body
	w.seq++
	w.notes = append(w.notes, Notice{
		Seq: w.seq, At: time.Now().UnixMilli(),
		Pane: p.PaneID, Term: p.TerminalID, Agent: p.Agent,
		Status: status, Title: p.Title, Text: body,
	})
	if n := len(w.notes) - keepNotices; n > 0 {
		w.notes = append(w.notes[:0], w.notes[n:]...)
	}
}

// Notices 给 seq 比 since 大的那些提示，外加此刻最新的 seq（前端下一拍拿它当 since）。
//
// **第二个返回值不能由前端从列表里推**：一条都没有的时候列表是空的，而 seq 得原样带回去，
// 不然下一拍又会把整个环重新取一遍、把老提示当新的弹一次。
func (w *Watcher) Notices(since uint64) ([]Notice, uint64) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	out := make([]Notice, 0, len(w.notes))
	for _, n := range w.notes {
		if n.Seq > since {
			out = append(out, n)
		}
	}
	return out, w.seq
}
