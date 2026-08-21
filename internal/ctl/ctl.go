// Package ctl 是命令行子命令和正在跑着的服务之间的通道。
//
// 认证靠**文件权限**（socket 0600），不需要密码 —— 能读这个 socket 的人已经是这台机器上
// 的你了。这和项目里连 herdr 用的是同一个套路。
//
// 为什么必须走 socket 而不是直接改文件：配对码只活在服务进程的内存里（故意的，见
// internal/auth），进程外拿不到，也不该落盘。
package ctl

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"time"

	"git.huglight.cn/bysir/herdr-web/internal/auth"
)

type Request struct {
	Cmd string `json:"cmd"` // pair | devices | revoke
	Arg string `json:"arg"`
}

type Response struct {
	Err     string        `json:"err,omitempty"`
	Msg     string        `json:"msg,omitempty"`
	Code    string        `json:"code,omitempty"`
	URL     string        `json:"url,omitempty"`
	Expires time.Time     `json:"expires,omitempty"`
	Devices []auth.Device `json:"devices,omitempty"`
	N       int           `json:"n,omitempty"`
}

func Path(dir string) string { return filepath.Join(dir, "ctl.sock") }

// Listen 起 ctl socket。已经有别的实例占着就返回 nil（不报错）：同一个数据目录跑两个
// 服务时，让第一个拥有这个 socket 就行，第二个少一个命令行入口不影响正常使用。
func Listen(dir string, fn func(Request) Response) (net.Listener, error) {
	p := Path(dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	// 先探一下：连得上说明真有实例在跑，别把人家的 socket 删了
	if c, err := net.DialTimeout("unix", p, 300*time.Millisecond); err == nil {
		c.Close()
		log.Printf("[herdr-web] %s 已被另一个实例占着，本进程不开命令行通道", p)
		return nil, nil
	}
	_ = os.Remove(p) // 上次没清干净的死 socket
	ln, err := net.Listen("unix", p)
	if err != nil {
		// unix socket 的路径有长度上限（macOS 104 字节），超了报的是没头没尾的
		// "invalid argument"。HERDR_WEB_DIR 指得很深时会撞上。
		if len(p) > 100 {
			return nil, fmt.Errorf("%w（这个路径 %d 字节，unix socket 撑不住，把 HERDR_WEB_DIR 换浅一点）", err, len(p))
		}
		return nil, err
	}
	if err := os.Chmod(p, 0o600); err != nil {
		ln.Close()
		return nil, err
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go serve(c, fn)
		}
	}()
	return ln, nil
}

func serve(c net.Conn, fn func(Request) Response) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	var req Request
	if json.NewDecoder(c).Decode(&req) != nil {
		return
	}
	_ = json.NewEncoder(c).Encode(fn(req))
}

var ErrNoServer = errors.New("服务没在跑")

// Call 给命令行用。连不上就返回 ErrNoServer，调用方自己决定要不要退回直接改文件。
func Call(dir string, req Request) (*Response, error) {
	c, err := net.DialTimeout("unix", Path(dir), time.Second)
	if err != nil {
		return nil, ErrNoServer
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if err := json.NewEncoder(c).Encode(req); err != nil {
		return nil, err
	}
	var res Response
	if err := json.NewDecoder(c).Decode(&res); err != nil {
		return nil, err
	}
	if res.Err != "" {
		return &res, errors.New(res.Err)
	}
	return &res, nil
}
