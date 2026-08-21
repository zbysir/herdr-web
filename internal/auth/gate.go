package auth

import (
	"sync"
	"time"
)

// Gate 是猜凭据这条路上的闸门：指数退避 + 按 IP 封锁 + 全局熔断。
//
// **只数猜短凭据的失败**（配对码、旧 token）。两件事故意不数：
//
//   - 设备 cookie 认不出来不算失败。撤销过、过期了都会出现这种请求，数进去就等于
//     「我刚把旧手机踢掉，结果自己被封了」。
//   - 已经认证成功的请求完全不过闸。走 frp / 反代时所有请求的源 IP 都是同一个，
//     要是连正常流量也数，封锁就是在封自己。
//
// 也正因为 frp 会把所有人的 IP 抹成一个，封锁只挡**新配对**，不动已有会话 ——
// 最坏情况是「十五分钟内配不了新设备」，而不是「正在干活的自己被踢下线」。
type Gate struct {
	// 前几次不罚：手输配对码抄错一位很正常
	FreeTries int
	// 窗口内失败到这个数就封
	Threshold int
	Window    time.Duration
	BlockFor  time.Duration
	MaxBlock  time.Duration
	// 全局熔断：窗口内所有 IP 的失败总数超过这个数（换源 IP 的分布式尝试）
	GlobalTrip int
	MaxDelay   time.Duration

	// ExemptLoopback：127.0.0.1 永不封。默认开着，为的是别把自己锁在门外
	// （解锁的入口也在门后面）。
	//
	// **但走 frp / 反代这类穿透时必须关掉**：那时候公网请求的源 IP 全是 127.0.0.1，
	// 这个豁免会把整层限速悄悄变成空操作 —— 看起来配了，实际上一次都不生效。
	// 关掉之后代价是「有人猛试的时候你自己也配不了新设备」，用 `herdr-web unlock` 解。
	ExemptLoopback bool

	Alert func(string) // 打到终端上，别让攻击悄无声息

	mu     sync.Mutex
	fails  map[string]*gateEntry
	global []time.Time
	locked bool
	now    func() time.Time
}

type gateEntry struct {
	n       int
	first   time.Time
	until   time.Time
	strikes int
}

func NewGate() *Gate {
	return &Gate{
		FreeTries: 2, Threshold: 10, Window: 15 * time.Minute,
		BlockFor: 15 * time.Minute, MaxBlock: 24 * time.Hour,
		GlobalTrip: 50, MaxDelay: 30 * time.Second,
		ExemptLoopback: true,
		fails:          map[string]*gateEntry{}, now: time.Now,
	}
}

// Check 在处理一次凭据尝试**之前**调用。
// delay 是该先睡多久（拖慢在线猜解），blocked 为真就直接回 429。
func (g *Gate) Check(ip string) (delay time.Duration, blocked bool, retryAfter time.Duration) {
	if g.exempt(ip) {
		return 0, false, 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	e := g.fails[ip]
	if e == nil {
		return 0, false, 0
	}
	now := g.now()
	if now.Before(e.until) {
		return 0, true, e.until.Sub(now)
	}
	if now.Sub(e.first) > g.Window {
		delete(g.fails, ip) // 窗口过了就既往不咎
		return 0, false, 0
	}
	if e.n <= g.FreeTries {
		return 0, false, 0
	}
	d := time.Duration(1<<uint(e.n-g.FreeTries-1)) * time.Second
	if d > g.MaxDelay {
		d = g.MaxDelay
	}
	return d, false, 0
}

// Fail 记一次失败。
func (g *Gate) Fail(ip string) {
	if g.exempt(ip) {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()

	g.global = append(g.global, now)
	for len(g.global) > 0 && now.Sub(g.global[0]) > g.Window {
		g.global = g.global[1:]
	}

	e := g.fails[ip]
	if e == nil || now.Sub(e.first) > g.Window {
		e = &gateEntry{first: now}
		if old := g.fails[ip]; old != nil {
			e.strikes = old.strikes // 重犯记录跨窗口留着，封锁时间要翻倍
		}
		g.fails[ip] = e
	}
	e.n++

	if e.n >= g.Threshold {
		d := g.BlockFor << uint(e.strikes)
		if d > g.MaxBlock || d <= 0 {
			d = g.MaxBlock
		}
		e.until = now.Add(d)
		e.n = 0
		e.first = now
		e.strikes++
		g.alert("有人在猜配对码：" + ip + " 已封 " + d.String() + "（第 " + itoa(e.strikes) + " 次）")
	}
	if !g.locked && len(g.global) >= g.GlobalTrip {
		g.locked = true
		g.alert("失败次数超过全局阈值，已停止接受新设备配对。已有会话不受影响；" +
			"在这台机器上执行 `herdr-web unlock` 解开。")
	}
}

// Reset 配对成功后清掉这个 IP 的账。
func (g *Gate) Reset(ip string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.fails, ip)
}

// Locked 全局熔断中：拒绝一切新配对，但不碰已有会话 ——
// 宁可让新设备进不来，也别把正在干活的自己踢下线。
func (g *Gate) Locked() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.locked
}

func (g *Gate) Unlock() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.locked = false
	g.global = nil
	g.fails = map[string]*gateEntry{}
}

func (g *Gate) alert(msg string) {
	if g.Alert != nil {
		go g.Alert(msg) // 持着锁，别在这儿等 I/O
	}
}

func (g *Gate) exempt(ip string) bool {
	return g.ExemptLoopback && isLoopbackIP(ip)
}

func isLoopbackIP(ip string) bool {
	return ip == "127.0.0.1" || ip == "::1" || ip == "localhost"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
