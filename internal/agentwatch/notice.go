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
// `idle` / `done` 只在**从 working 过来**时算「跑完了」。不加这个限制的话，你回答完一个
// 提问（blocked → idle）也会收到一条「跑完了」—— 而那一刻它其实刚要开始干活。
func worthNotice(old, cur string) bool {
	if cur == "blocked" {
		return true // 等你回答：不管从哪儿来
	}
	return old == "working" && (cur == "idle" || cur == "done")
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

		// 抖回去了 / 又开工了：这一下不是「轮到你了」，什么都不说。
		// 这是防抖的全部意义 —— 报一条假的「跑完了」比晚报两秒糟得多。
		if want != "" && want == cur && p.PaneID != "" {
			w.emit(p, cur)
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
