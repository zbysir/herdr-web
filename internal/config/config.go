// Package config 集中所有环境变量和路径，顺带管落盘的 token。
package config

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	Host      string
	Port      int
	Token     string
	Dir       string // ~/.herdr-web
	Shell     string
	SSHBin    string
	CopyIDBin string
	Socket    string // herdr 的 unix socket
	PollMS    int
	PushMS    int
	SettleMS  int
	Loopback  bool
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def, min int) int {
	if v, err := strconv.Atoi(os.Getenv(k)); err == nil {
		if v < min {
			return min
		}
		return v
	}
	return def
}

func home() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}

// DefaultSocket 是 herdr socket 的兜底路径。
//
// 别依赖 HERDR_SOCKET_PATH 存在：PTY 那边会把 HERDR_* 清掉（防嵌套启动），
// 而这个进程自己也可能根本不是从 herdr pane 里起的。
func DefaultSocket() string {
	if v := os.Getenv("HERDR_WEB_SOCKET"); v != "" {
		return v
	}
	if v := os.Getenv("HERDR_SOCKET_PATH"); v != "" {
		return v
	}
	return filepath.Join(home(), ".config", "herdr", "herdr.sock")
}

func Load() (*Config, error) {
	c := &Config{
		Host:      env("HERDR_WEB_HOST", "127.0.0.1"),
		Port:      envInt("HERDR_WEB_PORT", 7788, 1),
		Dir:       env("HERDR_WEB_DIR", filepath.Join(home(), ".herdr-web")),
		Shell:     env("HERDR_WEB_SHELL", env("SHELL", "/bin/zsh")),
		SSHBin:    env("HERDR_WEB_SSH", "/usr/bin/ssh"),
		CopyIDBin: env("HERDR_WEB_SSH_COPY_ID", "/usr/bin/ssh-copy-id"),
		Socket:    DefaultSocket(),
		// 500ms 是实测挑的：切 pane 到 textarea 更新的中位延迟约 500ms，
		// 再往下调收益递减（地板是一次 sync 的 ~150-300ms）。
		PollMS: envInt("HERDR_WEB_POLL_MS", 500, 200),
		PushMS: envInt("HERDR_WEB_PUSH_MS", 700, 100),
		// 两次 pane.read 之间等多久。**不能是 0**：实测调成 0 时整个清空循环
		// 会读到同一帧陈旧内容，6 轮全跑完仍然清不空（27ms 就返回了）。
		SettleMS: envInt("HERDR_WEB_SETTLE_MS", 120, 0),
	}
	c.Loopback = c.Host == "127.0.0.1" || c.Host == "localhost" || c.Host == "::1"

	tok, err := persistedToken(c.Dir)
	if err != nil {
		return nil, err
	}
	c.Token = tok
	return c, nil
}

// persistedToken 落盘，重启后不变 —— 否则手机上存的书签每次重启都失效。
func persistedToken(dir string) (string, error) {
	if v := os.Getenv("HERDR_WEB_TOKEN"); v != "" {
		return v, nil
	}
	file := filepath.Join(dir, "token")
	if b, err := os.ReadFile(file); err == nil {
		if t := strings.TrimSpace(string(b)); t != "" {
			return t, nil
		}
	}
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	t := hex.EncodeToString(buf)
	if err := os.MkdirAll(dir, 0o700); err == nil {
		_ = os.WriteFile(file, []byte(t+"\n"), 0o600) // 写不了就退回一次性 token
	}
	return t, nil
}
