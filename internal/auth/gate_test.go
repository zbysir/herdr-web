package auth

import (
	"testing"
	"time"
)

func TestGateBackoffThenBlock(t *testing.T) {
	g := NewGate()
	base := time.Now()
	g.now = func() time.Time { return base }

	// 前两次不罚：手抄配对码错一位很正常
	for i := 0; i < g.FreeTries; i++ {
		g.Fail("192.168.1.77")
		if d, blocked, _ := g.Check("192.168.1.77"); d != 0 || blocked {
			t.Fatalf("第 %d 次失败后不该有惩罚（delay=%v）", i+1, d)
		}
	}
	g.Fail("192.168.1.77")
	if d, _, _ := g.Check("192.168.1.77"); d != time.Second {
		t.Errorf("第 3 次之后该退避 1s，得到 %v", d)
	}
	g.Fail("192.168.1.77")
	if d, _, _ := g.Check("192.168.1.77"); d != 2*time.Second {
		t.Errorf("退避该翻倍，得到 %v", d)
	}

	for i := 0; i < g.Threshold; i++ {
		g.Fail("192.168.1.77")
	}
	d, blocked, retry := g.Check("192.168.1.77")
	if !blocked || retry <= 0 || d != 0 {
		t.Fatalf("到阈值就该封：blocked=%v retry=%v", blocked, retry)
	}
	// 封锁到期后自动放开
	g.now = func() time.Time { return base.Add(g.BlockFor + time.Second) }
	if _, blocked, _ := g.Check("192.168.1.77"); blocked {
		t.Error("封锁期过了就该放开")
	}
}

func TestGateNeverBlocksLoopback(t *testing.T) {
	g := NewGate()
	for i := 0; i < g.Threshold*3; i++ {
		g.Fail("127.0.0.1")
	}
	if _, blocked, _ := g.Check("127.0.0.1"); blocked {
		t.Error("本机永远不能被封 —— 解锁的入口也在门后面")
	}
}

func TestGateResetOnSuccess(t *testing.T) {
	g := NewGate()
	for i := 0; i < 5; i++ {
		g.Fail("192.168.1.77")
	}
	g.Reset("192.168.1.77")
	if d, _, _ := g.Check("192.168.1.77"); d != 0 {
		t.Errorf("配对成功后该清账，得到 delay=%v", d)
	}
}

func TestGateGlobalTrip(t *testing.T) {
	g := NewGate()
	var alerts []string
	g.Alert = func(m string) { alerts = append(alerts, m) }
	// 换源 IP 的分布式尝试：每个 IP 都没到阈值，但总数超了
	for i := 0; i < g.GlobalTrip; i++ {
		g.Fail("10.0." + itoa(i/250) + "." + itoa(i%250+1))
	}
	if !g.Locked() {
		t.Fatal("失败总数超过全局阈值就该熔断")
	}
	g.Unlock()
	if g.Locked() {
		t.Error("Unlock 之后该恢复")
	}
}

func TestGateWindowExpiry(t *testing.T) {
	g := NewGate()
	base := time.Now()
	g.now = func() time.Time { return base }
	for i := 0; i < 5; i++ {
		g.Fail("192.168.1.77")
	}
	g.now = func() time.Time { return base.Add(g.Window + time.Minute) }
	if d, _, _ := g.Check("192.168.1.77"); d != 0 {
		t.Errorf("窗口过了就既往不咎，得到 delay=%v", d)
	}
}

// 走 frp / 反代时公网请求的源 IP 全是 127.0.0.1。这时候还留着「本机永不封」，
// 整层限速就是个空操作 —— 看起来配了，一次都不生效。
func TestGateBlocksLoopbackWhenExposed(t *testing.T) {
	g := NewGate()
	g.ExemptLoopback = false
	for i := 0; i < g.Threshold; i++ {
		g.Fail("127.0.0.1")
	}
	if _, blocked, _ := g.Check("127.0.0.1"); !blocked {
		t.Fatal("声明暴露之后 loopback 也要能被封，否则限速形同虚设")
	}
	// 逃生口：机器上 unlock 之后立刻能配对
	g.Unlock()
	if _, blocked, _ := g.Check("127.0.0.1"); blocked {
		t.Error("unlock 之后该放开")
	}
}
