package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
)

// 并发写同一条 WebSocket 连接：gorilla 会 panic，而那个 panic 在 handler 自己起的
// goroutine 里，net/http 兜不住 —— 整个进程挂掉、所有人的终端一起断。线上炸过一次
// （ping 撞上二进制帧），所以这条要一直有人盯着。
//
// 去掉 wsWriter 里那把锁，这个测试就会 panic 失败。
func TestWSWriterAllowsConcurrentCallers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatalf("连不上测试服务端: %v", err)
	}
	defer c.Close()

	ws := &wsWriter{conn: c}
	payload := make([]byte, 4<<10)

	var wg sync.WaitGroup
	// 三种写者各来一堆，和真实情况对上：数据帧、ping、控制消息
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 60; j++ {
				_ = ws.write(websocket.BinaryMessage, payload)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 60; j++ {
			_ = ws.write(websocket.PingMessage, nil)
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 60; j++ {
			ws.json(map[string]any{"t": "exit", "code": 0})
		}
	}()
	wg.Wait()
}
