package agentwatch

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 用一个假 herdr socket 把整条路走一遍：订阅 → 收到状态变化事件 → 记下时间。
//
// 为什么要假的：真机上验不到这一步 —— 得正好有个 agent 在那一刻变状态，而挨个去戳
// 用户正在用的 agent 不合适。协议就三句话（pane.list、events.subscribe、事件行），
// 假一个比等一个划算。
func TestWatchRecordsStatusEvent(t *testing.T) {
	// 放 /tmp 而不是 t.TempDir()：unix socket 路径有 104 字节上限，
	// macOS 的 TempDir 前缀又长又深，踩过一次就不想再踩（见 internal/runlock 的注释）。
	dir, err := os.MkdirTemp("/tmp", "aw")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "h.sock")

	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serve(conn)
		}
	}()

	w := New(sock, filepath.Join(dir, "seen.json"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if at := w.At("w1:pA"); at != 0 {
			if !w.Live() {
				t.Error("收到事件的时候订阅应当是连着的")
			}
			// shell pane 不订阅，也就永远没有时间
			if w.At("w1:pS") != 0 {
				t.Error("没有 agent 的 pane 不该有状态变化时间")
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("3 秒内没把状态变化记下来")
}

// serve 是「一个连接一个请求」的假 herdr（真的那个也是这样，见 internal/herdr 的包注释）。
func serve(conn net.Conn) {
	defer conn.Close()
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return
	}
	var req struct {
		ID     string `json:"id"`
		Method string `json:"method"`
	}
	if json.Unmarshal(line, &req) != nil {
		return
	}
	switch req.Method {
	case "pane.list":
		_, _ = conn.Write([]byte(`{"id":"` + req.ID + `","result":{"panes":[` +
			`{"pane_id":"w1:pA","terminal_id":"term_A","agent":"claude","agent_status":"idle"},` +
			`{"pane_id":"w1:pS","terminal_id":"term_S","agent_status":"unknown"}]}}` + "\n"))
	case "events.subscribe":
		_, _ = conn.Write([]byte(`{"id":"` + req.ID + `","result":{"type":"subscription_started"}}` + "\n"))
		// 先补发一个旧的 pane_created（真 herdr 每次订阅都会来这么一条），再推两条
		// pane.updated：状态没变的那条不该记时间，变了的那条要记。
		//
		// 注意事件名故意用点号：真机上按 pane 订的那条回的就是点号形式，而全局的是
		// 下划线 —— 客户端会统一成下划线，两种都得能吃下。
		_, _ = conn.Write([]byte(`{"event":"pane_created","data":{"type":"pane_created","pane":{"pane_id":"w1:pS","terminal_id":"term_S"}}}` + "\n"))
		_, _ = conn.Write([]byte(`{"event":"pane.updated","data":{"pane":{"pane_id":"w1:pA","terminal_id":"term_A","agent":"claude","agent_status":"idle"}}}` + "\n"))
		_, _ = conn.Write([]byte(`{"event":"pane_updated","data":{"pane":{"pane_id":"w1:pA","terminal_id":"term_A","agent":"claude","agent_status":"blocked"}}}` + "\n"))
		// 订阅是长连，别主动关：关了 watcher 会当成断线去重连
		time.Sleep(5 * time.Second)
	}
}
