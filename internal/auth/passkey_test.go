package auth

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
)

func newPasskeys(t *testing.T, rpid string) *Passkeys {
	t.Helper()
	p, err := NewPasskeys(PasskeyConfig{
		Dir: t.TempDir(), RPID: rpid, Origins: []string{"http://localhost:7788"},
	})
	if err != nil {
		t.Fatalf("NewPasskeys: %v", err)
	}
	return p
}

// 用 IP 访问的部署压根用不了 passkey（WebAuthn 规定 RPID 必须是域名）。
// 这时候不能报错崩掉，而是要「安静地不可用」：按钮不出现、口返回可读的错。
func TestPasskeyUnavailableWithoutDomain(t *testing.T) {
	p := newPasskeys(t, "")
	if p.Available() {
		t.Error("没有域名时不该是可用状态")
	}
	if _, _, err := p.BeginRegister(); err != ErrNoPasskey {
		t.Errorf("要给一个说得清的错误，得到 %v", err)
	}
	if p.Count() != 0 {
		t.Error("Count 该是 0")
	}
}

// challenge 必须用一次就废，否则可以重放。
func TestCeremonyIsSingleUse(t *testing.T) {
	p := newPasskeys(t, "localhost")
	id := p.stashLocked(&webauthn.SessionData{Challenge: "x"})
	if p.takeLocked(id) == nil {
		t.Fatal("第一次该取得到")
	}
	if p.takeLocked(id) != nil {
		t.Error("同一个 ceremony 不能用第二次")
	}
}

func TestCeremonyExpires(t *testing.T) {
	p := newPasskeys(t, "localhost")
	id := p.stashLocked(&webauthn.SessionData{Challenge: "x"})
	p.now = func() time.Time { return time.Now().Add(ceremonyTTL + time.Second) }
	if p.takeLocked(id) != nil {
		t.Error("过期的 ceremony 不能用")
	}
}

// user handle 变了的话，已经注册的 passkey 全部作废 —— 所以它必须跟着落盘。
func TestUserHandleSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	p, err := NewPasskeys(PasskeyConfig{Dir: dir, RPID: "localhost", Origins: []string{"http://localhost:7788"}})
	if err != nil {
		t.Fatal(err)
	}
	p.keys = append(p.keys, &Passkey{ID: "abcdef123456", Label: "iPhone · Safari", Created: time.Now()})
	p.saveLocked()
	want := string(p.handle)

	p2, err := NewPasskeys(PasskeyConfig{Dir: dir, RPID: "localhost", Origins: []string{"http://localhost:7788"}})
	if err != nil {
		t.Fatal(err)
	}
	if string(p2.handle) != want {
		t.Error("user handle 必须跨重启不变，否则已注册的 passkey 全废")
	}
	if p2.Count() != 1 {
		t.Errorf("注册过的 passkey 该还在，得到 %d", p2.Count())
	}
}

func TestPasskeyDeleteByPrefix(t *testing.T) {
	p := newPasskeys(t, "localhost")
	p.keys = append(p.keys, &Passkey{ID: "abcdef123456", Label: "iPad · Safari"})
	if _, ok := p.Delete("abcd"); !ok {
		t.Fatal("前缀该能删掉")
	}
	if p.Count() != 0 {
		t.Error("删完该空了")
	}
	if _, ok := p.Delete("nope"); ok {
		t.Error("不存在的 id 不该报成功")
	}
}

func TestReauthNeeded(t *testing.T) {
	now := time.Now()
	dev := &Device{ID: "d1", Created: now.Add(-48 * time.Hour)}
	fresh := &Ident{Kind: "device", Device: dev, VerifiedAt: now.Add(-time.Hour)}
	stale := &Ident{Kind: "device", Device: dev, VerifiedAt: now.Add(-48 * time.Hour)}

	cases := []struct {
		name  string
		id    *Ident
		after time.Duration
		keys  int
		want  bool
	}{
		{"一把 passkey 都没注册时完全不生效", stale, 24 * time.Hour, 0, false},
		{"关掉重验（0）时不生效", stale, 0, 1, false},
		{"本机豁免那种身份不涉及", &Ident{Kind: "loopback"}, 24 * time.Hour, 1, false},
		{"旧 token 那种身份不涉及", &Ident{Kind: "legacy"}, 24 * time.Hour, 1, false},
		{"刚验过 → 放行", fresh, 24 * time.Hour, 1, false},
		{"太久没验 → 要求重验", stale, 24 * time.Hour, 1, true},
		{"nil 身份", nil, 24 * time.Hour, 1, false},
	}
	for _, c := range cases {
		if got := ReauthNeeded(c.id, c.after, c.keys, now); got != c.want {
			t.Errorf("%s：得到 %v，想要 %v", c.name, got, c.want)
		}
	}

	// 这个功能上线之前配的对没有 VerifiedAt，拿配对时间兜底 —— 不能当成「1970 年验的」
	// 直接把人锁在门外，也不能当成「刚验过」白放行。
	old := &Ident{Kind: "device", Device: &Device{ID: "d2", Created: now.Add(-time.Minute)}}
	if ReauthNeeded(old, 24*time.Hour, 1, now) {
		t.Error("零值 VerifiedAt 该退回配对时间：刚配的对不该马上要求重验")
	}
	older := &Ident{Kind: "device", Device: &Device{ID: "d3", Created: now.Add(-72 * time.Hour)}}
	if !ReauthNeeded(older, 24*time.Hour, 1, now) {
		t.Error("零值 VerifiedAt 且配对时间也很久了 → 该要求重验")
	}
}

// 注册和登录的口在「没有域名」时都要拒得干净，别 panic
func TestPasskeyLoginNeedsRegistered(t *testing.T) {
	p := newPasskeys(t, "localhost")
	if _, _, err := p.BeginLogin(); err == nil {
		t.Error("一把都没注册时不该能开始登录")
	}
	r := httptest.NewRequest("POST", "/api/auth/passkey/login/finish", nil)
	if _, err := p.FinishLogin("nonexistent", r); err == nil {
		t.Error("不存在的 ceremony 该报错")
	}
}

// passkey 能不能用是**按 origin** 算的，不是按「这个部署配了 RPID 没有」。
// 开了局域网直连之后同一个部署同时有域名 origin 和裸 IP origin —— 判错的表现是
// 裸 IP 那一侧画出一个按下去只会抛 SecurityError 的按钮。
func TestUsableOn(t *testing.T) {
	cases := []struct {
		rpid, host string
		want       bool
	}{
		{"example.com", "example.com", true},
		{"example.com", "example.com:7788", true},
		{"example.com", "herdr.example.com", true},          // 子域算
		{"herdr.example.com", "herdr.example.com:443", true},
		{"herdr.example.com", "example.com", false},         // 上级域不算
		{"herdr.example.com", "evilexample.com", false},     // 后缀像但不是子域
		{"herdr.example.com", "xherdr.example.com", false},
		{"example.com", "192.168.1.5", false},               // 裸 IP 永远不行
		{"example.com", "192.168.1.5:7790", false},          // 局域网直连那条路
		{"example.com", "127.0.0.1", false},
		{"example.com", "[::1]:7788", false},
		{"localhost", "localhost:7788", true},               // 规范里的特例
		{"", "example.com", false},                          // 没配 RPID
		{"example.com", "", false},
		{"example.com", "EXAMPLE.COM:7788", true},           // 大小写不敏感
	}
	for _, c := range cases {
		if got := UsableOn(c.rpid, c.host); got != c.want {
			t.Errorf("UsableOn(%q, %q) = %v，想要 %v", c.rpid, c.host, got, c.want)
		}
	}
}
