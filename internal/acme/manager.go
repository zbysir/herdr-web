package acme

import (
	"sync"
	"time"
)

// Manager 把「上次续期干了什么」记下来。
//
// 为什么需要：续期失败原来只在终端打一行告警，你不盯着终端就不知道 —— 直到某天早上
// 所有设备一起报证书过期。管理页要能显示这个，所以状态得有个地方存。
type Manager struct {
	cfg Config

	mu   sync.Mutex
	last Attempt
}

// Attempt 是一次签发 / 续期的结果。
type Attempt struct {
	At      time.Time `json:"at"`
	Renewed bool      `json:"renewed"` // 真的签了新的（false = 现有的还够用）
	Staging bool      `json:"staging"`
	Err     string    `json:"err,omitempty"`
}

func NewManager(cfg Config) *Manager { return &Manager{cfg: cfg} }

func (m *Manager) Config() Config { return m.cfg }

// Ensure 保证磁盘上有一张够用的证书。
//
// force 会跳过「还够用就别动」那个判断 —— 只给管理页上那个「立刻续期」按钮用。
// 注意正式环境同一组域名一周只给 5 张，别拿它当刷新按钮。
func (m *Manager) Ensure(force bool) Attempt {
	return m.record(run(m.cfg, force))
}

// EnsureWith 用临时改过的 staging 档位跑一次（管理页上「用 staging 试一次」）。
// Manager 自己的配置不动，而且 staging 的证书是另一组文件，不会盖掉正式那张。
func (m *Manager) EnsureWith(staging bool) Attempt {
	cfg := m.cfg
	cfg.Staging = staging
	return m.record(run(cfg, true))
}

func (m *Manager) record(a Attempt) Attempt {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.last = a
	return a
}

func run(cfg Config, force bool) Attempt {
	a := Attempt{At: time.Now(), Staging: cfg.Staging}
	if force {
		certFile, keyFile := cfg.Files()
		if err := cfg.obtain(certFile, keyFile); err != nil {
			a.Err = err.Error()
		} else {
			a.Renewed = true
		}
		return a
	}
	if _, _, renewed, err := cfg.Ensure(); err != nil {
		a.Err = err.Error()
	} else {
		a.Renewed = renewed
	}
	return a
}

func (m *Manager) Last() Attempt {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.last
}
