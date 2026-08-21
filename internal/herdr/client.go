// Package herdr 是 herdr socket API 的极简客户端。
//
// 换行分隔 JSON，请求 {id, method, params}，响应 {id, result} 或
// {id, error:{code,message}}。
//
// 服务端**一个连接只处理一个请求**，不支持 pipeline（同连接塞两个请求，只回
// 第一个的响应，第二个直接丢）。所以每次调用都开一条新连接。
//
// 注意延迟：请求字节必须在服务端 accept 的那一刻已经在 socket 缓冲区里，否则要
// 等下一个 ~100ms 的 tick。实测 connect 之后哪怕只隔 0.5ms 再发，就要 ~106ms。
// 这里 Dial 完立刻 Write，能赢就赢，赢不了也只是慢一跳（不影响正确性）。
package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"
)

type Client struct {
	Socket  string
	Timeout time.Duration
}

func New(socket string) *Client {
	return &Client{Socket: socket, Timeout: 10 * time.Second}
}

// Error 是服务端返回的业务错误（区别于连不上之类的传输错误）。
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return e.Code
}

type response struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *Error          `json:"error"`
}

// Call 发一个请求，把 result 解到 out（out 可以是 nil）。
func (c *Client) Call(method string, params any, out any) error {
	if params == nil {
		params = map[string]any{}
	}
	req, err := json.Marshal(map[string]any{"id": "web", "method": method, "params": params})
	if err != nil {
		return err
	}

	conn, err := net.DialTimeout("unix", c.Socket, c.Timeout)
	if err != nil {
		return fmt.Errorf("连不上 herdr socket %s（herdr server 没在跑？）: %w", c.Socket, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(c.Timeout))

	if _, err := conn.Write(append(req, '\n')); err != nil {
		return fmt.Errorf("写 herdr socket 失败: %w", err)
	}

	line, err := bufio.NewReaderSize(conn, 1<<20).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return fmt.Errorf("herdr 没给响应: %w", err)
	}
	var r response
	if err := json.Unmarshal(line, &r); err != nil {
		return fmt.Errorf("herdr 返回的不是合法 JSON: %w", err)
	}
	if r.Error != nil {
		return r.Error
	}
	if out != nil && len(r.Result) > 0 {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

/* ------------------------------------------------------------- 用到的响应形状 */

// Pane 是 pane.get / pane.current / pane.list 里的 pane 对象。
type Pane struct {
	PaneID      string `json:"pane_id"`
	WorkspaceID string `json:"workspace_id"`
	TabID       string `json:"tab_id"`
	Focused     bool   `json:"focused"`
	CWD         string `json:"cwd"`
	Agent       string `json:"agent"`
	AgentStatus string `json:"agent_status"`
	Title       string `json:"terminal_title_stripped"`
}

type paneWrap struct {
	Pane *Pane `json:"pane"`
}

type PaneList struct {
	Panes []Pane `json:"panes"`
}

type WorkspaceList struct {
	Workspaces []struct {
		WorkspaceID string `json:"workspace_id"`
		Number      int    `json:"number"`
		Label       string `json:"label"`
	} `json:"workspaces"`
}

type TabList struct {
	Tabs []struct {
		TabID  string `json:"tab_id"`
		Number int    `json:"number"`
		Label  string `json:"label"`
	} `json:"tabs"`
}

type readWrap struct {
	Read struct {
		Text string `json:"text"`
	} `json:"read"`
}

func (c *Client) PaneGet(id string) (*Pane, error) {
	var w paneWrap
	if err := c.Call("pane.get", map[string]any{"pane_id": id}, &w); err != nil {
		return nil, err
	}
	if w.Pane == nil {
		return nil, fmt.Errorf("pane.get 没返回 pane")
	}
	return w.Pane, nil
}

func (c *Client) PaneCurrent() (*Pane, error) {
	var w paneWrap
	if err := c.Call("pane.current", nil, &w); err != nil {
		return nil, err
	}
	if w.Pane == nil || w.Pane.PaneID == "" {
		return nil, fmt.Errorf("herdr 里没有激活的 pane")
	}
	return w.Pane, nil
}

func (c *Client) PaneList() ([]Pane, error) {
	var l PaneList
	err := c.Call("pane.list", nil, &l)
	return l.Panes, err
}

// Agent 是 agent.list 里的一项。比 pane.list 多一个 **state_change_seq**：herdr 里
// 每次 agent 状态变化都会推高这个全局计数，用来排「谁最近动过」。API 里没有任何时间戳，
// 这个计数是唯一一个「一直对」的排序依据（时间要自己记，见 internal/agentwatch）。
type Agent struct {
	PaneID      string `json:"pane_id"`
	Agent       string `json:"agent"`
	AgentStatus string `json:"agent_status"`
	Seq         uint64 `json:"state_change_seq"`
}

type agentList struct {
	Agents []Agent `json:"agents"`
}

func (c *Client) AgentList() ([]Agent, error) {
	var l agentList
	err := c.Call("agent.list", nil, &l)
	return l.Agents, err
}

// Subscribe 开一条**长连**收事件。
//
// 不能走 Call：herdr 的订阅是**连接级**的 —— 发一个 events.subscribe 之后，事件就一直
// 从这条连接上来，连接一关订阅就没了。所以这里握手完就把读超时**清掉**（事件可能几分钟
// 才来一个），靠 herdr 关连接或者 ctx 结束来收工。
//
// on 返回 false = 收工（调用方用它表达「pane 集合变了，我要重新订阅」）。
//
// 注意订阅粒度：`pane.agent_status_changed` **要带 pane_id**（每个 pane 一条订阅），
// 而全局那个 `pane.updated` 实测 20 秒来 193 条（跟着输出走），不能拿来当状态变化用。
func (c *Client) Subscribe(ctx context.Context, subs []any, on func(kind string, data json.RawMessage) bool) error {
	conn, err := net.DialTimeout("unix", c.Socket, c.Timeout)
	if err != nil {
		return fmt.Errorf("连不上 herdr socket %s: %w", c.Socket, err)
	}
	defer conn.Close()
	go func() { // ctx 一结束就把连接拆了，好让底下那个 ReadBytes 立刻返回
		<-ctx.Done()
		_ = conn.Close()
	}()

	req, err := json.Marshal(map[string]any{
		"id": "web-sub", "method": "events.subscribe",
		"params": map[string]any{"subscriptions": subs},
	})
	if err != nil {
		return err
	}
	_ = conn.SetDeadline(time.Now().Add(c.Timeout))
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return fmt.Errorf("写 herdr socket 失败: %w", err)
	}

	r := bufio.NewReaderSize(conn, 1<<20)
	line, err := r.ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("订阅没有响应: %w", err)
	}
	var ack response
	if err := json.Unmarshal(line, &ack); err != nil {
		return fmt.Errorf("订阅响应不是合法 JSON: %w", err)
	}
	if ack.Error != nil {
		return ack.Error
	}

	_ = conn.SetDeadline(time.Time{}) // 事件之间可以隔很久，别按超时算
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		var ev struct {
			Event string          `json:"event"`
			Data  json.RawMessage `json:"data"`
		}
		if json.Unmarshal(line, &ev) != nil || ev.Event == "" {
			continue // 心跳 / 别的响应，不是事件
		}
		if !on(ev.Event, ev.Data) {
			return nil
		}
	}
}

// Zoom 是 pane.zoom 的结果。
//
// `zoomed` 是**整个 tab 的**状态，不是某个 pane 的：herdr 放大的永远是「当前焦点
// pane」，快照里根本没有「哪个 pane 被放大了」这种字段（`pane.layout` /
// `layout.export` 里只有 tab 级的 zoomed + focused_pane_id，实测确认）。所以同一个
// tab 里换焦点时 `zoom_changed` 是 false、`reason` 是 already_zoomed —— **那不是失败**，
// 放大的对象已经跟着焦点换过去了。
type Zoom struct {
	PaneID        string `json:"pane_id"`
	FocusedPaneID string `json:"focused_pane_id"`
	Zoomed        bool   `json:"zoomed"`
	ZoomChanged   bool   `json:"zoom_changed"`
	FocusChanged  bool   `json:"focus_changed"`
	Reason        string `json:"reason"`
}

type zoomWrap struct {
	Zoom *Zoom `json:"zoom"`
}

// PaneZoom 跳到某个 pane，并按 mode 开 / 关放大（"on" | "off" | "toggle"）。
//
// **一次调用就能跨 workspace + tab + pane**：实测对另一个 workspace 里的 pane 发
// mode:"on"，焦点连着 workspace 和 tab 一起切过去（focus_changed=true），不用先
// workspace.focus 再 tab.focus。这条是这个功能的全部理由 —— 软键条发的是按键，而
// 按键只能表达「下一个 tab」「往右切一格」这种**相对**动作，「让 w5:p3 全屏」说不出来；
// socket 这层是按 pane_id 寻址的。
//
// 两个不是错误的返回：
//   - tab 里只有一个 pane 时给 zoomed=false + reason=single_pane（那个 pane 本来就占满
//     整个 tab，焦点已经切过去了）；
//   - 同 tab 内换 pane 时给 zoom_changed=false + reason=already_zoomed（见 Zoom 注释）。
func (c *Client) PaneZoom(id, mode string) (*Zoom, error) {
	var w zoomWrap
	if err := c.Call("pane.zoom", map[string]any{"pane_id": id, "mode": mode}, &w); err != nil {
		return nil, err
	}
	if w.Zoom == nil {
		return nil, fmt.Errorf("pane.zoom 没返回结果")
	}
	return w.Zoom, nil
}

// ReadANSI 拿带转义序列的整屏。必须 format:"ansi" + strip_ansi:false ——
// format:"text" 即使 strip_ansi:false 也不含 ESC。
func (c *Client) ReadANSI(id string) (string, error) {
	var w readWrap
	err := c.Call("pane.read", map[string]any{
		"pane_id": id, "source": "visible", "format": "ansi", "strip_ansi": false,
	}, &w)
	return w.Read.Text, err
}

func (c *Client) SendKeys(id string, keys []string) error {
	return c.Call("pane.send_input", map[string]any{"pane_id": id, "keys": keys}, nil)
}

// SendText 一个请求里的顺序是 text 先、keys 后，所以 text+enter 可以合成一次。
func (c *Client) SendText(id, text string, keys []string) error {
	p := map[string]any{"pane_id": id, "text": text}
	if len(keys) > 0 {
		p["keys"] = keys
	}
	return c.Call("pane.send_input", p, nil)
}

// AgentPrompt 会按 pane 当前的 bracketed-paste 模式正确编码 Enter，别自己拼 \r。
func (c *Client) AgentPrompt(target, text string) error {
	return c.Call("agent.prompt", map[string]any{"target": target, "text": text}, nil)
}
