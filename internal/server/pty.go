package server

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
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
// dropEnv 是**不能进 PTY 子进程**的环境变量。两类：
//
//  1. 终端多路复用器的痕迹。herdr 检测到 HERDR_* 就拒绝启动（"nested herdr is disabled"），
//     所以本服务在 herdr pane 里起的时候必须清掉。
//  2. **凭据。** PTY 里跑的是登录 shell，agent 就在那里面 —— 它 `echo $CLOUDFLARE_DNS_API_TOKEN`
//     就能把你的云账号密钥读走，一次 prompt injection 就够了。这些变量是为了 ACME 签证书
//     才进到本进程环境里的（见 internal/acme），没有任何理由传给子进程。
//
// 清掉不影响你自己用：PTY 起的是 `-l` 登录 shell，会重新 source 你的 profile，
// 你在 rc 里 export 的那些照样在。
var dropEnv = regexp.MustCompile(`^(` + strings.Join([]string{
	// 多路复用器 / 终端痕迹
	`HERDR_`, `TMUX$`, `TMUX_`, `ZELLIJ`, `STY$`, `ITERM_`,
	`TERM_PROGRAM`, `TERM_SESSION_ID`, `TERM_FEATURES`, `LC_TERMINAL`,
	`CLAUDECODE$`, `CLAUDE_CODE_`,
	// DNS 服务商的凭据（ACME 用）
	`CLOUDFLARE_`, `CF_`, `ALICLOUD_`, `ALIBABA_CLOUD_`, `ALIYUN_`,
	`TENCENTCLOUD_`, `DNSPOD_`, `AWS_`, `DO_AUTH_TOKEN$`, `DIGITALOCEAN_`,
	`HUAWEICLOUD_`, `HUAWEI_`,
}, `|`) + `)`)

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
	// 真正的跨站判断在 handlePTY 里做（要看凭据是哪种），这里只做同源比对。
	CheckOrigin: func(r *http.Request) bool {
		o := r.Header.Get("Origin")
		if o == "" {
			return true
		}
		u, err := url.Parse(o)
		return err == nil && strings.EqualFold(u.Host, r.Host)
	},
}

// maxPTY 是同时开着的登录 shell 上限。一条 WebSocket 就是一个 shell，本来没有上限
// （README 里「连接按钮随时能按」那个坑就是这么来的）。手机 + 平板 + 电脑各一条也就三条。
const maxPTY = 8

var livePTY atomic.Int64

var seq int64

// logInput 把写进 PTY 的每一批字节 hex 打出来（HERDR_WEB_DEBUG_INPUT=1 时）。
// 排「某个键到底发了什么」这类问题时，这是唯一不用猜的办法。
//
// 开关从 Config 里传进来而不是在这儿读环境变量：配置**只在 internal/config 收口**，
// 散在各处 os.Getenv 的话「有哪些配置」就没法一眼数清楚。
func logInput(on bool, where string, b []byte) {
	if !on {
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
	id := s.Auth.Authenticate(r)
	if id == nil {
		http.Error(w, "这台设备还没配对", http.StatusUnauthorized)
		return
	}
	if !s.originOK(r) {
		http.Error(w, "跨站请求被拒", http.StatusForbidden)
		return
	}
	// 和 /api 同一个档：发件箱本身就是代码执行，所以没道理只卡终端不卡它，
	// 反过来也一样 —— 两边一起卡
	if s.reauthNeeded(id) {
		http.Error(w, "太久没验证了，先在网页上用 passkey 验一次", http.StatusUnauthorized)
		return
	}
	// 浏览器发 WebSocket 握手时一定带 Origin，所以「没有 Origin」只能是非浏览器客户端。
	// 那种客户端不该靠 cookie 认证（cookie 是浏览器自动带上的），要连就用 ?token=。
	if r.Header.Get("Origin") == "" && id.Kind == "device" {
		http.Error(w, "缺 Origin：非浏览器客户端请用 ?token=", http.StatusForbidden)
		return
	}
	if livePTY.Load() >= maxPTY {
		http.Error(w, "同时开着的会话太多了（上限 "+itoa(maxPTY)+"）", http.StatusServiceUnavailable)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	// gorilla/websocket **不允许并发写**：两个 goroutine 同时写同一条连接会 panic，
	// 而 panic 发生在 handler 自己起的 goroutine 里，net/http 兜不住 —— 整个进程挂掉，
	// 别人的终端跟着一起断。这里有三个写者（PTY 数据、25 秒的 ping、退出时的 exit/close），
	// 所以全部收口到这一个函数上。
	//
	// 实测炸过一次：ping 正好撞上一批二进制帧。跟「开了几个浏览器」无关 —— 每条连接各有
	// 自己的 conn 和 goroutine，但连接开得越多、重连越频繁，撞上的机会越大。
	ws := &wsWriter{conn: conn}
	write := ws.write
	sendJSON := ws.json

	// `?session=` 是地址栏里那一段（`https://host/work` → `work`）：连上之后敲的是
	// `herdr --session work`，没有这个 session 就新建一个。名字不合法**不能静默退回
	// 默认 session** —— 那样人以为自己在 work 里，其实在默认那个 herdr 上乱敲。
	// 这时候连接已经升级完了，所以用 fatal 帧说话（前端会弹遮罩显示这句）。
	session, err := sessionOf(r)
	if err != nil {
		sendJSON(map[string]any{"t": "fatal", "msg": err.Error()})
		return
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

	livePTY.Add(1)
	defer livePTY.Add(-1)

	sid := atomic.AddInt64(&seq, 1)
	where := "默认 session"
	if session != "" {
		where = "session " + session
	}
	log.Printf("[herdr-web] #%d 打开 %s (pid %d, %dx%d, %s) —— %s", sid, label, cmd.Process.Pid, cols, rows, where, id.Label)
	sendJSON(map[string]any{"t": "ready", "label": label, "pid": cmd.Process.Pid, "mode": "local", "session": session})

	done := make(chan struct{})
	firstOut := make(chan struct{})
	var once sync.Once

	// PTY → 浏览器（二进制帧）
	go func() {
		defer close(done)
		buf := make([]byte, 32<<10)
		for {
			n, err := f.Read(buf)
			if n > 0 {
				once.Do(func() { close(firstOut) })
				if write(websocket.BinaryMessage, buf[:n]) != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	autoType(f, onConnectLine(s.Cfg.OnConnect, session), s.Cfg.OnConnectMS, s.Cfg.DebugInput, firstOut, done)

	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	go func() {
		// select 到 done 上，不是 `for range ping.C`：Stop() 不会关掉那个 channel，
		// 光 Stop 的话这个 goroutine 会永远卡在接收上（连着 conn 一起泄漏）。
		// 手机上频繁重连，一条一个地攒起来。
		for {
			select {
			case <-done:
				return
			case <-ping.C:
				if write(websocket.PingMessage, nil) != nil {
					return
				}
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
		_ = write(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		_ = conn.Close()
	}()

	// 浏览器 → PTY
	for {
		typ, data, err := conn.ReadMessage()
		if err != nil {
			break
		}
		if typ == websocket.BinaryMessage {
			logInput(s.Cfg.DebugInput, "bin", data)
			_, _ = f.Write(data)
			continue
		}
		var m ctrlMsg
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		switch m.T {
		case "i":
			logInput(s.Cfg.DebugInput, "i", []byte(m.D))
			_, _ = f.Write([]byte(m.D))
		case "p":
			// 前端的「你还活着吗」。手机锁屏回来时 WebSocket 常常是僵的：readyState
			// 还是 OPEN、send 也不报错，但对面早就没了。协议层的 ping/pong 是 UA 自己
			// 处理的，网页里读不到（也就没法拿它判断），所以在应用层补一发回音。
			sendJSON(map[string]any{"t": "p"})
		case "r":
			if m.Cols > 0 && m.Rows > 0 {
				_ = pty.Setsize(f, &pty.Winsize{Cols: uint16(min(m.Cols, 1000)), Rows: uint16(min(m.Rows, 1000))})
			}
		}
	}

	_ = cmd.Process.Kill()
	_, _ = cmd.Process.Wait()
	log.Printf("[herdr-web] #%d 前端断开，关闭 PTY", sid)
}

// autoType 连上之后自动敲一行（默认 `herdr`，空串就不敲）。
//
// 为什么要等：shell 刚起来时 rc 还没跑完。tty 缓冲区理论上会替我们把早敲的字符留到
// shell 来读，但实测靠不住 —— rc 里动 stty、或者补全 / 提示插件初始化时清一次输入，
// 早敲的那行就没了，而且是**静默**没的（页面上什么都不会显示）。所以先等 shell 吐出
// 第一批东西（登录横幅或 prompt，说明它已经在跑了），再多等一小会儿让 prompt 画完。
//
// 敲的是 \r 不是 \n：prompt 那会儿终端多半已经进了 raw 模式，回车键本来就是 \r。
func autoType(w io.Writer, line string, delayMS int, debug bool, firstOut, done <-chan struct{}) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	go func() {
		select {
		case <-firstOut:
		case <-done:
			return
		case <-time.After(3 * time.Second): // shell 半天不吭声也别把这一行吞了
		}
		select {
		case <-time.After(time.Duration(delayMS) * time.Millisecond):
		case <-done:
			return
		}
		b := []byte(line + "\r")
		logInput(debug, "onconnect", b)
		_, _ = w.Write(b)
	}()
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

// wsWriter 把一条 WebSocket 连接上的所有写入串成一列。
//
// gorilla/websocket **不允许并发写**：两个 goroutine 同时写同一条连接会 panic，而那个
// panic 发生在 handler 自己起的 goroutine 里，net/http 兜不住 —— **整个进程挂掉，
// 别人的终端跟着一起断**。
//
// 一条 PTY 连接上有三个写者：PTY 数据、25 秒一次的 ping、退出时的 exit + close。
// 实测炸过一次，是 ping 撞上一批二进制帧。和「开了几个浏览器」无关（每条连接各有自己的
// conn 和 goroutine），但连接越多、重连越频繁，撞上的机会越大。
type wsWriter struct {
	mu   sync.Mutex
	conn *websocket.Conn
}

func (w *wsWriter) write(typ int, b []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	// 写超时：手机断网时 TCP 缓冲填满，WriteMessage 会一直阻塞、把锁也占着，
	// 那样连 PTY 的读循环都推不动。让它超时失败，然后正常拆连接。
	_ = w.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return w.conn.WriteMessage(typ, b)
}

func (w *wsWriter) json(v any) {
	b, _ := json.Marshal(v)
	_ = w.write(websocket.TextMessage, b)
}
