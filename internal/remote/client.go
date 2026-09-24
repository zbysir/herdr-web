package remote

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/zbysir/herdr-web/internal/auth"
	"github.com/zbysir/herdr-web/internal/version"
)

var httpc = &http.Client{Timeout: 20 * time.Second}

// stdinReader 是问配对码时读的那个东西。平时就是 stdin；连上之后 stdin 被转发那个
// goroutine 占着，中途要重验时由 ensureWith 换成从它那儿接出来的管子。
var stdinReader io.Reader = os.Stdin

func userAgent() string {
	host, _ := os.Hostname()
	return fmt.Sprintf("%s%s (%s; %s)", auth.CLIAgent, version.Version, runtime.GOOS, host)
}

// state 是「这份凭据现在能不能用」。
type state int

const (
	stOK     state = iota
	stPair         // 没配过 / 被撤销了 / 过期了：要一个新配对码
	stReauth       // 配过，但到了重验的时候（注册过 passkey 才会有）：浏览器里 passkey 批准
)

// check 拿一个要认证的轻接口探一下。/pty 的握手失败只给一个状态码，分不清这几种。
func check(t Target, c Cred) (state, error) {
	if c.Token == "" {
		return stPair, nil
	}
	req, _ := http.NewRequest(http.MethodGet, t.Origin+"/api/auth/devices", nil)
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("User-Agent", userAgent())
	resp, err := httpc.Do(req)
	if err != nil {
		return 0, fmt.Errorf("连不上 %s：%w", t.Origin, err)
	}
	defer resp.Body.Close()
	var b struct{ Error, Need string }
	_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&b)
	switch {
	case resp.StatusCode == 200:
		return stOK, nil
	case resp.StatusCode == http.StatusUnauthorized && b.Need != "":
		return stReauth, nil
	case resp.StatusCode == http.StatusUnauthorized:
		return stPair, nil
	}
	return 0, fmt.Errorf("%s 回了 %d：%s", t.Origin, resp.StatusCode, orStatus(b.Error, resp))
}

// pairCode 用配对码换一台新设备。first 是在上一步菜单里已经敲进来的那个码（可以为空）。
//
// 配对码**只管第一次进来**：到期重验不走这里，只走 passkey 批准（approve）—— 不然
// 命令行设备每天输个码就能一直续，从头到尾不碰 passkey（第一版就是这样）。
func pairCode(t Target, in *bufio.Reader, first string) (Cred, error) {
	code := first
	for try := 0; ; try++ {
		if code == "" {
			fmt.Fprintf(os.Stderr, "  配对码（在跑 herdr-web 的机器上 `herdr-web pair`）：")
			line, err := in.ReadString('\n')
			code = strings.TrimSpace(line)
			if err != nil && code == "" {
				return Cred{}, fmt.Errorf("没读到配对码")
			}
			if code == "" {
				continue
			}
		}
		var b struct{ Error, Label, DeviceID, Token string }
		resp, err := post(t, "/api/auth/pair", "", map[string]string{"code": code, "client": "cli"}, &b)
		if err != nil {
			return Cred{}, err
		}
		if resp.StatusCode == 200 {
			if b.Token == "" {
				return Cred{}, fmt.Errorf("服务端没给令牌 —— 那边的 herdr-web 太旧了，先更新它")
			}
			fmt.Fprintf(os.Stderr, "  ✓ 配好了，设备列表里叫「%s」\n\n", b.Label)
			return Cred{Token: b.Token, DeviceID: b.DeviceID, Label: b.Label}, nil
		}
		msg := orStatus(b.Error, resp)
		// 码错了可以再输；限流 / 锁住了再输也没用
		if resp.StatusCode != http.StatusUnauthorized || try >= 4 {
			return Cred{}, fmt.Errorf("%s", msg)
		}
		fmt.Fprintf(os.Stderr, "  ✗ %s\n", msg)
		code = ""
	}
}

var errNoPasskey = errors.New("那边还没注册 passkey")

// approve 在浏览器里用 passkey 批准（服务端 /api/auth/cli/*，流程见 internal/auth/cliapprove.go）。
// old 有令牌 = 给这台设备重验；没有 = 要一台新设备。
func approve(t Target, old Cred) (Cred, error) {
	var st struct {
		Error, Reason, ID, Poll, Code, URL string
		Expires                            time.Time
	}
	resp, err := post(t, "/api/auth/cli/start", old.Token, map[string]string{}, &st)
	if err != nil {
		return Cred{}, err
	}
	switch {
	case resp.StatusCode == http.StatusConflict && st.Reason == "nopasskey":
		return Cred{}, errNoPasskey
	case resp.StatusCode == http.StatusNotFound:
		return Cred{}, fmt.Errorf("那边的 herdr-web 太旧了，不认 passkey 批准 —— 先更新它")
	case resp.StatusCode != 200:
		return Cred{}, fmt.Errorf("%s", orStatus(st.Error, resp))
	}
	code := st.Code
	if len(code) == 8 {
		code = code[:4] + "-" + code[4:]
	}
	fmt.Fprintf(os.Stderr, "\n  在一个能用 passkey 的浏览器里打开（手机就行）：\n\n    %s\n\n", st.URL)
	fmt.Fprintf(os.Stderr, "  输入这个码，看清是这台电脑，再刷一次 passkey：\n\n    %s\n\n", code)
	fmt.Fprintf(os.Stderr, "  等你批准……（%d 分钟内有效，Ctrl+C 取消）\n", int(time.Until(st.Expires).Round(time.Minute).Minutes()))

	for {
		time.Sleep(2 * time.Second)
		var p struct{ Error, State, Label, DeviceID, Token string }
		resp, err := post(t, "/api/auth/cli/poll", "", map[string]string{"id": st.ID, "poll": st.Poll}, &p)
		if err != nil {
			continue // 网络抖一下别放弃，过期由服务端说了算
		}
		if resp.StatusCode != 200 {
			return Cred{}, fmt.Errorf("%s", orStatus(p.Error, resp))
		}
		switch p.State {
		case "denied":
			return Cred{}, fmt.Errorf("在浏览器里被拒绝了")
		case "approved":
			if old.Token != "" {
				fmt.Fprintf(os.Stderr, "  ✓ 验好了\n\n")
				return old, nil
			}
			fmt.Fprintf(os.Stderr, "  ✓ 批准了，设备列表里叫「%s」\n\n", p.Label)
			return Cred{Token: p.Token, DeviceID: p.DeviceID, Label: p.Label}, nil
		}
	}
}

// post 发一个 JSON 请求、把响应解进 out。bearer 非空就带上。
func post(t Target, path, bearer string, body, out any) (*http.Response, error) {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, t.Origin+path, bytes.NewReader(b))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("User-Agent", userAgent())
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := httpc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("连不上 %s：%w", t.Origin, err)
	}
	defer resp.Body.Close()
	_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(out)
	return resp, nil
}

// hasPasskeys 问一下那边注册过 passkey 没有（whoami 不要求认证）。问不到就当没有 ——
// 退回配对码那条路总是走得通的。
func hasPasskeys(t Target) bool {
	req, _ := http.NewRequest(http.MethodGet, t.Origin+"/api/auth/whoami", nil)
	req.Header.Set("User-Agent", userAgent())
	resp, err := httpc.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var b struct{ Passkeys int }
	_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&b)
	return b.Passkeys > 0
}

func orStatus(msg string, resp *http.Response) string {
	if msg != "" {
		return msg
	}
	return resp.Status
}

// ensure 保证手里有一份能用的凭据：没有就配，到期就重验，都存盘。
func ensure(t Target) (Cred, error) {
	creds, err := loadCreds()
	if err != nil {
		return Cred{}, err
	}
	c := creds[t.Origin]
	st, err := check(t, c)
	if err != nil {
		return Cred{}, err
	}
	switch st {
	case stOK:
		return c, nil
	case stReauth:
		// 注册过 passkey 才会走到这儿，所以只有 passkey 这一条路（理由见 pairCode）
		fmt.Fprintf(os.Stderr, "\n  这台设备到了重验的时候（HERDR_WEB_REAUTH_HOURS），要在浏览器里用 passkey 批准一下。\n")
		c, err = approve(t, c)
	default:
		if c.Token != "" {
			fmt.Fprintf(os.Stderr, "\n  原来那份凭据不认了（被撤销或过期了）。\n")
		}
		in := bufio.NewReader(stdinReader)
		if !hasPasskeys(t) {
			fmt.Fprintf(os.Stderr, "\n  要登录 %s。\n", t.Origin)
			c, err = pairCode(t, in, "")
			break
		}
		var pick int
		pick, err = choose(fmt.Sprintf("要登录 %s，选一种方式", t.Origin), []string{
			"在浏览器里用 passkey 批准",
			"输入配对码（在那台机器上 herdr-web pair）",
		}, 0)
		if err != nil {
			break
		}
		if pick == 1 {
			c, err = pairCode(t, in, "")
		} else if c, err = approve(t, Cred{}); err == errNoPasskey {
			c, err = pairCode(t, in, "")
		}
	}
	if err != nil {
		return Cred{}, err
	}
	return c, saveCred(t.Origin, c)
}

// Logout 撤销这台设备（服务端那条 logout 就是撤销自己）并删掉本地凭据。
func Logout(t Target) error {
	creds, err := loadCreds()
	if err != nil {
		return err
	}
	c, ok := creds[t.Origin]
	if !ok {
		return fmt.Errorf("没有 %s 的凭据", t.Origin)
	}
	req, _ := http.NewRequest(http.MethodPost, t.Origin+"/api/auth/logout", nil)
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("User-Agent", userAgent())
	resp, err := httpc.Do(req)
	if err != nil {
		// 连不上也照样删本地那份，但要说清楚服务端那台设备还在
		_ = saveCred(t.Origin, Cred{})
		return fmt.Errorf("本地凭据删了，但连不上服务端（%v）—— 那台设备还在，去网页的设备页撤销它", err)
	}
	resp.Body.Close()
	return saveCred(t.Origin, Cred{})
}
