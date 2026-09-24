package auth

import (
	"testing"
	"time"
)

// 新设备：批准之前领不到；批准之后领一次就删，而且设备是**领的时候**才签的。
func TestCLIApproveNewDevice(t *testing.T) {
	s := newStore(t, Config{})
	req, poll, err := s.StartCLI(CLIAgent+"v1 (darwin; mbp)", "8.8.8.8", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := s.PollCLI(req.ID, poll); p.State != "pending" {
		t.Fatalf("没批准就该是 pending，得到 %q", p.State)
	}
	if _, err := s.PollCLI(req.ID, "别人猜的"); err == nil {
		t.Fatal("轮询令牌不对不该认 —— 知道显示码的人不该能领走凭据")
	}
	if _, err := s.ApproveCLI("AAAA-AAAA"); err == nil {
		t.Fatal("码不对不该批准")
	}
	if _, err := s.ApproveCLI(req.Code); err != nil {
		t.Fatal(err)
	}
	if n := len(s.Devices()); n != 0 {
		t.Fatalf("批准那一下不该签设备（没人来领就什么都不留），现在 %d 台", n)
	}
	p, err := s.PollCLI(req.ID, poll)
	if err != nil || p.State != "approved" || p.Token == "" {
		t.Fatalf("批准之后该领到令牌：%+v %v", p, err)
	}
	if p.Device.Label != "命令行 · mbp" || !p.Device.PasskeyAt.IsZero() {
		t.Errorf("设备：%+v（PasskeyAt 不该写，刷 passkey 的是批准的那台）", p.Device)
	}
	if _, err := s.PollCLI(req.ID, poll); err == nil {
		t.Error("领过一次就该删")
	}
}

// 重验：批准当场续上那台的 VerifiedAt，不新建设备，不给新令牌。
func TestCLIApproveReauth(t *testing.T) {
	now := time.Now()
	s := newStore(t, Config{})
	s.now = func() time.Time { return now }
	code, _ := s.MintCode()
	d, _, _ := s.Redeem(code, CLIAgent+"v1 (linux; box)", "")

	now = now.Add(48 * time.Hour)
	req, poll, _ := s.StartCLI(CLIAgent+"v1 (linux; box)", "", false, d.ID)
	if req.DevName != d.Label {
		t.Errorf("批准页该看得到要重验的是哪台：%q", req.DevName)
	}
	if _, err := s.ApproveCLI(req.Code); err != nil {
		t.Fatal(err)
	}
	if got := s.Devices()[0].VerifiedAt; !got.Equal(now) {
		t.Errorf("VerifiedAt = %v，想要 %v", got, now)
	}
	p, _ := s.PollCLI(req.ID, poll)
	if p.State != "approved" || p.Token != "" || len(s.Devices()) != 1 {
		t.Errorf("重验不该给新令牌、不该多出设备：%+v，%d 台", p, len(s.Devices()))
	}
}

// 拒绝之后显示码不再认，发起方问到的是 denied。
func TestCLIDeny(t *testing.T) {
	s := newStore(t, Config{})
	req, poll, _ := s.StartCLI("x", "", false, "")
	if err := s.DenyCLI(req.Code); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApproveCLI(req.Code); err == nil {
		t.Error("拒绝过的不该还能批准")
	}
	if p, _ := s.PollCLI(req.ID, poll); p.State != "denied" {
		t.Errorf("该是 denied，得到 %q", p.State)
	}
}

// 「全部踢掉」要连已经批准、还没领走的一起清 —— 不然领的那一下会新签一台出来。
func TestRevokeAllClearsCLI(t *testing.T) {
	s := newStore(t, Config{})
	req, poll, _ := s.StartCLI("x", "", false, "")
	_, _ = s.ApproveCLI(req.Code)
	s.RevokeAll()
	if _, err := s.PollCLI(req.ID, poll); err == nil {
		t.Fatal("全部踢掉之后还领得到")
	}
	if len(s.Devices()) != 0 {
		t.Fatal("冒出了一台设备")
	}
}

// 过期和上限：发起那一步不要求认证，不能是个无限往内存里塞东西的口。
func TestCLIExpiryAndCap(t *testing.T) {
	now := time.Now()
	s := newStore(t, Config{})
	s.now = func() time.Time { return now }
	for i := 0; i < cliMax; i++ {
		if _, _, err := s.StartCLI("x", "", false, ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := s.StartCLI("x", "", false, ""); err != ErrCLIBusy {
		t.Fatalf("满了该报 ErrCLIBusy，得到 %v", err)
	}
	now = now.Add(CLITTL + time.Second)
	if _, _, err := s.StartCLI("x", "", false, ""); err != nil {
		t.Fatalf("过期的该被清掉腾出位置：%v", err)
	}
}
