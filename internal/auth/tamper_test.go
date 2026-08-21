package auth

import (
	"os"
	"testing"
)

// devices.json 被外部改过要能发现。只能检测不能阻止，但至少不能一声不响。
func TestTamperDetected(t *testing.T) {
	s := newStore(t, Config{})
	code, _ := s.MintCode()
	if _, _, err := s.Redeem(code, "iPhone", "192.168.1.9"); err != nil {
		t.Fatal(err)
	}

	var alerts []string
	al := func(m string) { alerts = append(alerts, m) }

	s.CheckTampered(al)
	if len(alerts) != 0 {
		t.Fatalf("我们自己写的不该报警：%v", alerts)
	}

	// 模拟「被注入的 agent 往里加了一行」
	b, _ := os.ReadFile(s.file())
	if err := os.WriteFile(s.file(), append(b, ' '), 0o600); err != nil {
		t.Fatal(err)
	}
	s.CheckTampered(al)
	if len(alerts) != 1 {
		t.Fatalf("外部改动该报一次，得到 %d 次", len(alerts))
	}
	// 同一次改动别每 30 秒刷屏
	s.CheckTampered(al)
	if len(alerts) != 1 {
		t.Errorf("同一次改动只该报一次，得到 %d 次", len(alerts))
	}

	// 我们自己再写一次之后，基准要跟着更新（不然会一直报）
	s.RevokeAll()
	alerts = nil
	s.CheckTampered(al)
	if len(alerts) != 0 {
		t.Errorf("自己写完之后基准该更新，却报了：%v", alerts)
	}
}

func TestTamperDetectsDeletion(t *testing.T) {
	s := newStore(t, Config{})
	code, _ := s.MintCode()
	_, _, _ = s.Redeem(code, "iPhone", "")
	var alerts []string
	_ = os.Remove(s.file())
	s.CheckTampered(func(m string) { alerts = append(alerts, m) })
	if len(alerts) != 1 {
		t.Errorf("文件被删也要报（重启之后所有设备都进不来了），得到 %d 次", len(alerts))
	}
}
