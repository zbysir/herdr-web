// Package agentwatch 记「每个 agent pane 上次状态变化是什么时候」。
//
// 为什么要自己记：herdr 的 API 里**一个时间戳都没有**。`agent.list` 只给
// `state_change_seq`（一个全局递增的计数），事件里也不带时间。而「面板一览」要显示
// 「3 分钟前」，只能由收到事件的这一侧打时间。
//
// 排序不靠这里的时间（靠 `state_change_seq`，那个一直是对的）—— 这里只管显示。
//
// **按 terminal_id 存，不按 pane_id。** `pane_id`（`w1:p4K` 这种）是 herdr 里的位置
// 编号，pane 一开一关就会重新分配给别人；`terminal_id`（`term_65973f2a3bc511`）跟着
// 那个终端进程走，不会串。落盘也是这个原因才敢做 —— herdr-web 自己重启（升级、改配置）
// 是常事，一重启时间列全空看着就像坏了；而 herdr 重启之后所有终端都是新的 id，旧记录
// 自然对不上，也就不会张冠李戴。存盘时只写**这会儿还在的**终端，文件自己就不会长胖。
//
// **状态是「轮询保底 + 事件加速」两条路凑出来的。** 只听事件不行：实测 herdr 只对看得见的
// pane 推 `pane.updated`，背景 workspace 里的 agent 干完活能 40 秒没有一条事件（见
// pollAgents 那条注释）。事件那条路留着，是因为你正看着的那个 pane 能因此几乎实时。
//
// **听的是全局 `pane.updated`，不是 `pane.agent_status_changed`。** 后者看着才对口，
// 但实测会漏：同一个五分钟里，`pane.updated` 那条流看到 3 次状态变化，按 pane 订的
// `pane.agent_status_changed` 只来了 1 条（herdr 大概对它做了防抖，快速来回的
// working↔idle 会被吞掉）。而且它**必须带 pane_id**（不带直接报 `missing field pane_id`），
// 于是要给每个 agent pane 订一条、pane 集合一变还得整条连接重订 —— 换来的却是漏事件。
//
// `pane.updated` 的代价是量大（一个 agent 在跑时实测 20 秒 193 条，跟着输出走），但每条
// 只是几百字节、带着完整的 pane 对象（`terminal_id` / `agent_status` 都在里面），解析成本
// 可以忽略，而且这就是 herdr 自己界面用的那条流。换过来还顺手去掉了两处复杂：不用按 pane
// 订阅、不用在 pane 集合变化时重连（原来那套还被「订阅一建立就补发一个旧 `pane_created`」
// 坑过一轮 800ms 无限重连）。
//
// **只有状态和上次记的不一样才算一次变化。** 补发的、重复的事件都不是变化，拿它们刷时间
// 就是编时间。同理**状态不落盘**：重启后拿旧状态去比，会把「停机期间变的」记成「刚刚变的」。
//
// 这条状态流还养着**提示**（右上角那个弹窗 + 面板图标上的红点）：agent 变成「等你回答」
// 或者「跑完了」时攒一条，带上读屏抽出来的那段话。见 notice.go / extract.go。
package agentwatch

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/zbysir/herdr-web/internal/herdr"
)

// 重订阅之前先等一下：pane 一批批地开关（herdr 恢复布局时一次开十几个），
// 每来一个事件就重连一次纯属自己给自己制造风暴。
const settle = 800 * time.Millisecond

// 连不上时的重试间隔。herdr 没在跑是常态（用户还没起 herdr），所以不要吵。
const retry = 5 * time.Second

// 攒一会儿再落盘：状态变化本来就稀疏，但一次重订阅可能连着补好几条。
const flush = 15 * time.Second

// 多久主动对一次底（一次 pane.list）。
//
// **不能只靠事件。** 实测（真机，2026-08-22）：herdr 只对「看得见的」pane 推
// `pane.updated` —— 给背景 workspace 里的一个 agent 投一句 hi，`pane.get` 6 秒后就报
// working、13 秒报 done，而事件流里**第一条事件 40 秒后才来**。而背景里那些恰恰是提示
// 最该管的（你看得见的那个用不着提示）。
//
// 所以现在是「轮询保底 + 事件加速」：轮询保证每个 pane 的变化最多晚 3 秒被看到，事件让
// 你正看着的那个几乎实时。一次 pane.list 就是一个 socket 调用、几十 KB，比发件箱开着时
// 500ms 打三次轻得多。
//
// 是 var 不是 const：单测把它调到几十毫秒。
var pollAgents = 3 * time.Second

type Watcher struct {
	c    *herdr.Client
	file string // 落盘的位置；空字符串 = 不落盘

	mu     sync.RWMutex
	term   map[string]string // pane_id → terminal_id（每轮订阅对一遍）
	status map[string]string // terminal_id → 上次记下的状态（用来认出补发的事件）
	at     map[string]int64  // terminal_id → 上次状态变化的 unix 毫秒
	live   bool              // 订阅是不是连着（决定「没有时间」该怎么解释）
	dirty  bool

	// 提示那一套（见 notice.go）。pane 是最后见到的那个 pane 对象：提示要报 pane_id
	// 和会话标题，而防抖那 2.5 秒之后事件早过去了，得留一份。
	pane  map[string]herdr.Pane
	pend  map[string]string // terminal_id → 等着报的状态（防抖期间又变了就覆盖）
	busy  map[string]bool   // terminal_id → 已经有一个收集协程在等了
	notes []Notice
	seq   uint64
}

// New：file 是存时间的 JSON（传空字符串就只在内存里）。
func New(socket, file string) *Watcher {
	w := &Watcher{
		c: herdr.New(socket), file: file,
		term: map[string]string{}, status: map[string]string{}, at: map[string]int64{},
		pane: map[string]herdr.Pane{}, pend: map[string]string{}, busy: map[string]bool{},
	}
	w.load()
	return w
}

// At 给某个 pane 上次状态变化的时间（unix 毫秒）；没记到过给 0。
func (w *Watcher) At(paneID string) int64 {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.at[w.term[paneID]]
}

// Live 说订阅这会儿连着没有。前端拿它区分「这个 pane 还没变过状态」和「压根没在盯」。
func (w *Watcher) Live() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.live
}

// Start 起 goroutine：一个盯事件（自己重连、按 pane 集合的变化重订阅），一个定时对底
// （事件漏掉的靠它，见 pollAgents），一个攒着落盘。
func (w *Watcher) Start(ctx context.Context) {
	go w.loop(ctx)
	go w.poller(ctx)
	go w.flusher(ctx)
}

// poller 每 pollAgents 拉一次 pane.list，把变化按「刚刚发生」处理。
//
// **连不上之后回来的那一拍不发提示**：herdr server 停过一阵（或者笔记本合盖），回来时
// 一屏 pane 的状态都和停机前不一样，那些变化是「这半小时里发生的」，当场弹成一片
// 「刚刚跑完了」就是编时间。同理**第一拍也不发**（进程刚起来，看到的是历史）。
func (w *Watcher) poller(ctx context.Context) {
	t := time.NewTicker(pollAgents)
	defer t.Stop()
	stale := true
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		cur, err := w.agents()
		if err != nil {
			stale = true // 断过一次，回来那一拍只对底
			continue
		}
		for _, p := range cur {
			if stale {
				w.observe(p)
			} else {
				w.observeLive(p)
			}
		}
		stale = false
	}
}

func (w *Watcher) loop(ctx context.Context) {
	var loggedErr string // 同一个错误只说一次，别把日志刷满
	for ctx.Err() == nil {
		err := w.once(ctx)
		if ctx.Err() != nil {
			return
		}
		wait := settle
		if err != nil {
			wait = retry
			if msg := err.Error(); msg != loggedErr {
				loggedErr = msg
				log.Printf("agent 状态订阅断了（面板一览的时间列会空着）: %v", err)
			}
		} else {
			loggedErr = ""
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

func (w *Watcher) flusher(ctx context.Context) {
	t := time.NewTicker(flush)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			w.save()
			return
		case <-t.C:
			w.save()
		}
	}
}

// agents 列出这会儿有 agent 的 pane（一次 pane.list，只用来对底）。
func (w *Watcher) agents() ([]herdr.Pane, error) {
	panes, err := w.c.PaneList()
	if err != nil {
		return nil, err
	}
	out := make([]herdr.Pane, 0, len(panes))
	for _, p := range panes {
		if p.Agent == "" {
			continue // 普通 shell 没有 agent 状态可言
		}
		out = append(out, p)
	}
	return out, nil
}

// once 对一遍底，然后挂在 `pane.updated` 上听到断为止。
func (w *Watcher) once(ctx context.Context) error {
	cur, err := w.agents()
	if err != nil {
		w.setLive(false)
		return err
	}
	w.mu.Lock()
	w.term = make(map[string]string, len(cur)) // 重建一次，把关掉的 pane 清出去
	w.mu.Unlock()
	for _, p := range cur {
		w.observe(p)
	}

	w.setLive(true)
	defer w.setLive(false)
	return w.c.Subscribe(ctx, []any{map[string]any{"type": "pane.updated"}},
		func(kind string, data json.RawMessage) bool {
			if kind != "pane_updated" {
				return true // 别的事件（补发的 pane_created 之类）不关心，继续听
			}
			var ev struct {
				Pane herdr.Pane `json:"pane"`
			}
			if json.Unmarshal(data, &ev) != nil || ev.Pane.PaneID == "" || ev.Pane.Agent == "" {
				return true
			}
			w.observeLive(ev.Pane)
			return true
		})
}

// observe 收一个 pane 的当前样子：更新 pane → terminal 的对应关系，**状态变了才打时间**。
// 返回「这次和上次记的不一样吗」+ 上次那个状态（提示那条路拿它判断该不该弹，见 worthNotice）。
//
// 第一次见到的终端只记状态不记时间：它上次变化是本进程起来之前的事，没法知道是什么时候
// （存盘里有的话那条还留着，见 At）。空着是实话，编一个时间比空着糟得多。
func (w *Watcher) observe(p herdr.Pane) (bool, string) {
	if p.TerminalID == "" {
		return false, ""
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.term[p.PaneID] = p.TerminalID
	w.pane[p.TerminalID] = p
	old, ok := w.status[p.TerminalID]
	w.status[p.TerminalID] = p.AgentStatus
	if ok && old != p.AgentStatus {
		w.at[p.TerminalID] = time.Now().UnixMilli()
		w.dirty = true
		return true, old
	}
	return false, old
}

// observeLive 是**事件**那条路：除了打时间，值得弹的变化还挂一条提示（见 notice.go）。
//
// **对底那条路（once 里的循环）走的是光秃秃的 observe，不发提示**，这是故意的：herdr
// server 停过一阵（或者一直没起，5 秒一次地重试）之后重新连上时，对底会发现一屏 pane 的
// 状态都和停机前不一样 —— 那些变化是「这半小时里发生的」，当场弹成一片「刚刚跑完了」就是
// 在编时间，和 at 那一列不肯给旧变化编时间是同一个道理。代价是重订阅那 800ms 窗口里真的
// 变化会漏报一条，那个窗口短、而且下一次状态变化就补上了。
func (w *Watcher) observeLive(p herdr.Pane) {
	changed, old := w.observe(p)
	if changed && worthNotice(old, p.AgentStatus) {
		w.arm(p)
	}
}

func (w *Watcher) setLive(v bool) {
	w.mu.Lock()
	w.live = v
	w.mu.Unlock()
}

/* ------------------------------------------------------------------ 落盘 */

type snapshot struct {
	At map[string]int64 `json:"at"` // terminal_id → unix 毫秒
}

func (w *Watcher) load() {
	if w.file == "" {
		return
	}
	b, err := os.ReadFile(w.file)
	if err != nil {
		return // 没有这个文件是正常的（第一次跑）
	}
	var snap snapshot
	if json.Unmarshal(b, &snap) != nil {
		return // 存坏了就当没有，别为这个拒绝启动
	}
	for k, v := range snap.At {
		w.at[k] = v
	}
}

// save 把时间写下来，**只写这会儿还在的终端**：herdr 重启之后终端 id 全是新的，
// 旧记录再也对不上，留着只会让文件一直长。
func (w *Watcher) save() {
	if w.file == "" {
		return
	}
	w.mu.Lock()
	if !w.dirty || len(w.term) == 0 {
		w.mu.Unlock()
		return
	}
	keep := map[string]int64{}
	for _, term := range w.term {
		if at := w.at[term]; at != 0 {
			keep[term] = at
		}
	}
	w.dirty = false
	w.mu.Unlock()

	b, err := json.Marshal(snapshot{At: keep})
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(w.file), 0o700); err != nil {
		return
	}
	// 先写临时文件再 rename：断电 / 被 kill 时不会留下一个截断的 JSON
	tmp := w.file + ".tmp"
	if os.WriteFile(tmp, b, 0o600) != nil {
		return
	}
	if err := os.Rename(tmp, w.file); err != nil {
		_ = os.Remove(tmp)
	}
}
