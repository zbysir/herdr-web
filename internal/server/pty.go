package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
)

// 只开本机 shell。要连别的机器直接在 herdr 里 ssh —— herdr 自己就能干这事，
// 没必要在这一层再实现一套主机管理 + 托管私钥（那还得管密钥落盘和 ssh-keygen）。
//
// herdr 检测到 HERDR_* 就会拒绝启动（"nested herdr is disabled"）。如果本服务是在
// herdr / tmux 的 pane 里起的，必须把这些痕迹从子进程环境里清掉。
var dropEnv = regexp.MustCompile(`^(HERDR_|TMUX$|TMUX_|ZELLIJ|STY$|ITERM_|TERM_PROGRAM|TERM_SESSION_ID|TERM_FEATURES|LC_TERMINAL|CLAUDECODE$|CLAUDE_CODE_)`)

func childEnv() []string {
	out := []string{}
	for _, kv := range os.Environ() {
		k := kv
		if i := indexByte(kv, '='); i >= 0 {
			k = kv[:i]
		}
		if dropEnv.MatchString(k) || k == "NODE_OPTIONS" {
			continue
		}
		out = append(out, kv)
	}
	out = append(out, "TERM=xterm-256color", "COLORTERM=truecolor", "TERM_PROGRAM=herdr-web")
	if os.Getenv("LANG") == "" {
		out = append(out, "LANG=en_US.UTF-8")
	}
	return out
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  32 << 10,
	WriteBufferSize: 32 << 10,
	CheckOrigin: func(r *http.Request) bool {
		o := r.Header.Get("Origin")
		if o == "" {
			return true // 非浏览器客户端
		}
		u, err := url.Parse(o)
		return err == nil && u.Host == r.Host
	},
}

var seq int64

// HERDR_WEB_DEBUG_INPUT=1 时把写进 PTY 的每一批字节 hex 打出来。
// 排「某个键到底发了什么」这类问题时，这是唯一不用猜的办法。
var debugInput = os.Getenv("HERDR_WEB_DEBUG_INPUT") == "1"

func logInput(where string, b []byte) {
	if !debugInput {
		return
	}
	var hex, txt string
	for _, c := range b {
		hex += fmt.Sprintf("%02x ", c)
		if c >= 0x20 && c < 0x7f {
			txt += string(rune(c))
		} else {
			txt += "."
		}
	}
	log.Printf("[input:%s] %d 字节  %s |%s|", where, len(b), hex, txt)
}

type ctrlMsg struct {
	T    string `json:"t"`
	D    string `json:"d"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

func (s *Server) handlePTY(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if !s.tokenOK(q.Get("token")) {
		http.Error(w, "token 不对", http.StatusUnauthorized)
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	sendJSON := func(v any) {
		b, _ := json.Marshal(v)
		_ = conn.WriteMessage(websocket.TextMessage, b)
	}

	cols, rows := atoiDef(q.Get("cols"), 120), atoiDef(q.Get("rows"), 34)
	label := filepath.Base(s.Cfg.Shell)

	cmd := exec.Command(s.Cfg.Shell, "-l")
	home, _ := os.UserHomeDir()
	cmd.Dir = home
	cmd.Env = childEnv()

	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		sendJSON(map[string]any{"t": "fatal", "msg": "开 PTY 失败：" + err.Error()})
		return
	}
	defer f.Close()

	id := atomic.AddInt64(&seq, 1)
	log.Printf("[herdr-web] #%d 打开 %s (pid %d, %dx%d)", id, label, cmd.Process.Pid, cols, rows)
	sendJSON(map[string]any{"t": "ready", "label": label, "pid": cmd.Process.Pid, "mode": "local"})

	done := make(chan struct{})

	// PTY → 浏览器（二进制帧）
	go func() {
		defer close(done)
		buf := make([]byte, 32<<10)
		for {
			n, err := f.Read(buf)
			if n > 0 {
				if conn.WriteMessage(websocket.BinaryMessage, buf[:n]) != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	go func() {
		for range ping.C {
			if conn.WriteMessage(websocket.PingMessage, nil) != nil {
				return
			}
		}
	}()

	go func() {
		<-done
		code := -1
		if st := cmd.ProcessState; st != nil {
			code = st.ExitCode()
		}
		sendJSON(map[string]any{"t": "exit", "code": code, "signal": ""})
		_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		_ = conn.Close()
	}()

	// 浏览器 → PTY
	for {
		typ, data, err := conn.ReadMessage()
		if err != nil {
			break
		}
		if typ == websocket.BinaryMessage {
			logInput("bin", data)
			_, _ = f.Write(data)
			continue
		}
		var m ctrlMsg
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		switch m.T {
		case "i":
			logInput("i", []byte(m.D))
			_, _ = f.Write([]byte(m.D))
		case "r":
			if m.Cols > 0 && m.Rows > 0 {
				_ = pty.Setsize(f, &pty.Winsize{Cols: uint16(min(m.Cols, 1000)), Rows: uint16(min(m.Rows, 1000))})
			}
		}
	}

	_ = cmd.Process.Kill()
	_, _ = cmd.Process.Wait()
	log.Printf("[herdr-web] #%d 前端断开，关闭 PTY", id)
}

func atoiDef(s string, def int) int {
	if v, err := strconv.Atoi(s); err == nil && v > 0 {
		return v
	}
	return def
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
