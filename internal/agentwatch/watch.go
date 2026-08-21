// Package agentwatch 记「每个 agent pane 上次状态变化是什么时候」。
//
// 为什么要自己记：herdr 的 API 里**一个时间戳都没有**。`agent.list` 只给
// `state_change_seq`（一个全局递增的计数），事件里也不带时间。而「面板一览」要显示
// 「3 分钟前」，只能由收到事件的这一侧打时间。
//
// 为什么**不落盘**：`pane_id` 是 herdr 重启之后会重新分配的（这次的 `w1:p4K` 下次可能
// 是别的 pane），而 herdr 一重启所有 agent 会话本来也都换了 —— 存下来的时间只会张冠李戴。
// 所以只在内存里：herdr-web 刚起来时时间列是空的，随着一次次状态变化填回来。**排序不受
// 影响** —— 拿不到时间时按 `state_change_seq` 排，那个一直是对的。
//
// 订阅粒度是踩出来的：`pane.agent_status_changed` **必须带 pane_id**（每个 pane 一条
// 订阅，所以 pane 集合一变就得重订阅），而全局那条 `pane.updated` 实测 20 秒来 193 条
// （跟着输出走，agent 一忙就是刷屏级），不能拿来当状态变化用。
//
// 还有两条更阴的：
//
//   - **订阅一建立，herdr 会先补一个旧的 `pane_created`**（实测每次都是同一个 pane，
//     revision 0）。一开始「收到全局事件就重订阅」，于是每 800ms 重连一次、永远在重连。
//     所以现在收到全局事件先**核对 agent pane 集合真的变了没有**（另开一条连接问
//     `pane.list`），没变就继续听。
//   - 状态事件也可能是补发的，所以**只有状态和上次记的不一样才算一次变化**。不然一次
//     重订阅就会把所有 pane 的时间刷成「刚刚」—— 那是编时间，比空着糟得多。
package agentwatch

import (
	"context"
	"encoding/json"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zbysir/herdr-web/internal/herdr"
)

// 重订阅之前先等一下：pane 一批批地开关（herdr 恢复布局时一次开十几个），
// 每来一个事件就重连一次纯属自己给自己制造风暴。
const settle = 800 * time.Millisecond

// 连不上时的重试间隔。herdr 没在跑是常态（用户还没起 herdr），所以不要吵。
const retry = 5 * time.Second

type Watcher struct {
	c *herdr.Client

	mu     sync.RWMutex
	at     map[string]int64  // pane_id → 上次状态变化的 unix 毫秒
	status map[string]string // pane_id → 上次记下的状态（用来认出补发的事件）
	live   bool              // 订阅是不是连着（决定「没有时间」该怎么解释）
}

func New(socket string) *Watcher {
	return &Watcher{c: herdr.New(socket), at: map[string]int64{}, status: map[string]string{}}
}

// At 给某个 pane 上次状态变化的时间（unix 毫秒）；没记到过给 0。
func (w *Watcher) At(paneID string) int64 {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.at[paneID]
}

// Start 起一个 goroutine 盯着，自己重连、自己按 pane 集合的变化重订阅。
func (w *Watcher) Start(ctx context.Context) {
	go w.loop(ctx)
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

// agents 列出这会儿有 agent 的 pane（id → 状态），顺带给出一个可比的 key。
func (w *Watcher) agents() (map[string]string, string, error) {
	panes, err := w.c.PaneList()
	if err != nil {
		return nil, "", err
	}
	out := map[string]string{}
	ids := make([]string, 0, len(panes))
	for _, p := range panes {
		if p.Agent == "" {
			continue // 普通 shell 没有 agent 状态可言
		}
		out[p.PaneID] = p.AgentStatus
		ids = append(ids, p.PaneID)
	}
	sort.Strings(ids)
	return out, strings.Join(ids, ","), nil
}

// once 订阅一轮：为每个 agent pane 订一条状态变化，再顺带订几条「pane 集合可能变了」的
// 全局事件。收到全局事件时先核对集合真的变了，变了才收工让 loop 重订一轮。
func (w *Watcher) once(ctx context.Context) error {
	cur, key, err := w.agents()
	if err != nil {
		w.setLive(false)
		return err
	}
	subs := []any{
		map[string]any{"type": "pane.created"},
		map[string]any{"type": "pane.closed"},
		map[string]any{"type": "pane.exited"},
		map[string]any{"type": "pane.agent_detected"},
	}
	for id := range cur {
		subs = append(subs, map[string]any{"type": "pane.agent_status_changed", "pane_id": id})
	}
	w.seed(cur)
	w.setLive(true)
	defer w.setLive(false)

	return w.c.Subscribe(ctx, subs, func(kind string, data json.RawMessage) bool {
		if kind != "pane_agent_status_changed" {
			// 可能只是订阅刚建立时补发的那个旧 pane_created。真的变了才重订。
			_, now, err := w.agents()
			return err == nil && now == key
		}
		var ev struct {
			PaneID string `json:"pane_id"`
			Status string `json:"agent_status"`
		}
		if json.Unmarshal(data, &ev) != nil || ev.PaneID == "" {
			return true
		}
		w.record(ev.PaneID, ev.Status)
		return true
	})
}

// record 记一次状态变化。**状态没变就不记时间** —— 补发的事件不是变化，拿它刷时间
// 等于编时间。
func (w *Watcher) record(paneID, status string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if old, ok := w.status[paneID]; ok && old == status {
		return
	}
	w.status[paneID] = status
	w.at[paneID] = time.Now().UnixMilli()
}

// seed 用 pane.list 的当前状态对一遍底。
//
// 第一次见到的 pane 只记状态**不记时间**：它上次变化是 herdr-web 起来之前的事，
// 没法知道是什么时候，空着才是实话。而如果和上次记的不一样，那就是在两轮订阅之间
// 变的（就那么几百毫秒），按刚刚算。
func (w *Watcher) seed(cur map[string]string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for id, st := range cur {
		old, ok := w.status[id]
		w.status[id] = st
		if ok && old != st {
			w.at[id] = time.Now().UnixMilli()
		}
	}
}

func (w *Watcher) setLive(v bool) {
	w.mu.Lock()
	w.live = v
	w.mu.Unlock()
}

// Live 说订阅这会儿连着没有。前端拿它区分「这个 pane 还没变过状态」和「压根没在盯」。
func (w *Watcher) Live() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.live
}
