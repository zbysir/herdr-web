package auth

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newStore(t *testing.T, c Config) *Store {
	t.Helper()
	if c.Dir == "" {
		c.Dir = t.TempDir()
	}
	s, err := New(c)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

// req 造一个带 cookie 的请求；from 是源地址（用来验本机豁免）。
// Host 默认写成 loopback：本机免配对要求源地址和 Host 都是 loopback。
func req(from, token string) *http.Request {
	r := httptest.NewRequest("GET", "/api/state", nil)
	r.Host = "127.0.0.1:7788"
	r.RemoteAddr = from + ":54321"
	if token != "" {
		r.AddCookie(&http.Cookie{Name: CookieName, Value: token})
	}
	return r
}

func TestCodeIsSingleUse(t *testing.T) {
	s := newStore(t, Config{})
	code, _ := s.MintCode()

	if _, _, err := s.Redeem(code, "iPhone; Safari", "192.168.1.9"); err != nil {
		t.Fatalf("第一次换应该成功: %v", err)
	}
	if _, _, err := s.Redeem(code, "", ""); err == nil {
		t.Fatal("同一个配对码换第二次必须失败")
	}
}

func TestCodeExpires(t *testing.T) {
	s := newStore(t, Config{})
	code, _ := s.MintCode()
	s.now = func() time.Time { return time.Now().Add(CodeTTL + time.Second) }
	if _, _, err := s.Redeem(code, "", ""); err == nil {
		t.Fatal("过期的配对码不能再用")
	}
}

func TestDeviceTokenAuth(t *testing.T) {
	s := newStore(t, Config{})
	code, _ := s.MintCode()
	dev, token, err := s.Redeem(code, "Mozilla/5.0 (iPad; CPU OS 17) Safari/605", "192.168.1.9")
	if err != nil {
		t.Fatal(err)
	}
	if dev.Label != "iPad · Safari" {
		t.Errorf("标签认错了: %q", dev.Label)
	}
	// 明文不落盘：结构体里只有哈希
	if dev.Hash == token || dev.Hash != hashToken(token) {
		t.Error("落盘的必须是哈希而不是明文")
	}

	id := s.Authenticate(req("192.168.1.9", token))
	if id == nil || id.Kind != "device" {
		t.Fatalf("凭据应该认得出来: %+v", id)
	}
	if !id.Ambient {
		t.Error("cookie 是浏览器自动带的，必须标成 Ambient（要防 CSRF）")
	}
	if s.Authenticate(req("192.168.1.9", token+"x")) != nil {
		t.Error("令牌错一个字符就不该认")
	}
	// 换了 IP 照样认 —— 手机换 Wi-Fi / 租约变了不该被踢下线
	if s.Authenticate(req("10.0.0.7", token)) == nil {
		t.Error("凭据不该绑 IP")
	}
}

func TestTTLZeroNeverExpires(t *testing.T) {
	s := newStore(t, Config{TTL: 0})
	code, _ := s.MintCode()
	_, token, _ := s.Redeem(code, "", "")
	s.now = func() time.Time { return time.Now().AddDate(5, 0, 0) }
	if s.Authenticate(req("192.168.1.9", token)) == nil {
		t.Error("TTL=0 就是永不过期")
	}
}

func TestTTLSlidingRenewal(t *testing.T) {
	s := newStore(t, Config{TTL: 24 * time.Hour})
	code, _ := s.MintCode()
	_, token, _ := s.Redeem(code, "", "")

	// 20 小时后用一次 → 续期到「那时候 +24h」
	s.now = func() time.Time { return time.Now().Add(20 * time.Hour) }
	if s.Authenticate(req("192.168.1.9", token)) == nil {
		t.Fatal("还没到期就该认")
	}
	// 再过 20 小时（累计 40h > 24h），因为续过期所以仍然有效
	s.now = func() time.Time { return time.Now().Add(40 * time.Hour) }
	if s.Authenticate(req("192.168.1.9", token)) == nil {
		t.Error("滑动续期没生效：天天用的设备不该掉线")
	}
	// 一直不用就该过期
	s.now = func() time.Time { return time.Now().Add(40*time.Hour + 25*time.Hour) }
	if s.Authenticate(req("192.168.1.9", token)) != nil {
		t.Error("超过 TTL 没活动就该失效")
	}
}

func TestRevoke(t *testing.T) {
	s := newStore(t, Config{})
	code, _ := s.MintCode()
	dev, token, _ := s.Redeem(code, "", "")

	if _, ok := s.Revoke(dev.ID[:4]); !ok { // 前缀就够，命令行上不用抄全
		t.Fatal("按 ID 前缀撤销应该成功")
	}
	if s.Authenticate(req("192.168.1.9", token)) != nil {
		t.Error("撤销之后下一个请求就该被拒")
	}
	if len(s.Devices()) != 0 {
		t.Error("设备列表该空了")
	}
}

func TestPersistAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	s := newStore(t, Config{Dir: dir})
	code, _ := s.MintCode()
	_, token, _ := s.Redeem(code, "Mozilla/5.0 (iPhone) Safari", "192.168.1.9")

	s2 := newStore(t, Config{Dir: dir}) // 重启
	if s2.Authenticate(req("192.168.1.9", token)) == nil {
		t.Error("重启之后设备凭据必须还认 —— 否则就是「每次都要重新配对」")
	}
	// 配对码只在内存里，重启就没了
	if _, _, err := s2.Redeem(code, "", ""); err == nil {
		t.Error("配对码不该跨重启存活")
	}
}

func TestLoopbackTrust(t *testing.T) {
	s := newStore(t, Config{TrustLoopback: true})
	if off := newStore(t, Config{}); off.Authenticate(req("127.0.0.1", "")) != nil {
		t.Error("本机免配对默认必须是关的（走 frp 时源地址就是 127.0.0.1）")
	}
	if id := s.Authenticate(req("127.0.0.1", "")); id == nil || id.Kind != "loopback" {
		t.Error("本机应该免配对")
	}
	if s.Authenticate(req("192.168.1.9", "")) != nil {
		t.Error("局域网没凭据就得配对")
	}

	// 套反代时 RemoteAddr 就是 127.0.0.1，这时候「本机免配对」等于谁来都放行
	r := req("127.0.0.1", "")
	r.Header.Set("X-Forwarded-For", "203.0.113.7")
	if s.Authenticate(r) != nil {
		t.Error("带 XFF 时不能再信任 loopback")
	}

	off := newStore(t, Config{TrustLoopback: false})
	if off.Authenticate(req("127.0.0.1", "")) != nil {
		t.Error("关掉本机豁免之后本机也要配对")
	}
}

// frp 这类内网穿透：frpc 在本机，所以每个公网请求的源地址都是 127.0.0.1。
// 浏览器地址栏里是域名，所以 Host 头是域名 —— 靠这个把「真本机」和「穿透进来的」分开。
func TestLoopbackTrustNotFooledByTunnel(t *testing.T) {
	s := newStore(t, Config{TrustLoopback: true})
	r := req("127.0.0.1", "")
	r.Host = "herdr.example.com"
	if s.Authenticate(r) != nil {
		t.Fatal("穿透进来的请求不能算本机 —— 算了就等于把 shell 挂在公网上")
	}
	// 端口不同也一样
	r.Host = "herdr.example.com:7788"
	if s.Authenticate(r) != nil {
		t.Error("带端口的域名同理")
	}
}

// 没有可信前置时不能读 XFF：否则攻击者自带一个头就能伪造源 IP，把按 IP 的限速绕干净。
func TestClientIPIgnoresUntrustedXFF(t *testing.T) {
	s := newStore(t, Config{})
	r := req("192.168.1.9", "")
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	if got := s.ClientIP(r); got != "192.168.1.9" {
		t.Errorf("不信任前置时应该用真实源地址，得到 %q", got)
	}
	tp := newStore(t, Config{TrustProxy: true})
	if got := tp.ClientIP(r); got != "1.2.3.4" {
		t.Errorf("信任前置时才读 XFF，得到 %q", got)
	}
}

func TestLegacyTokenModes(t *testing.T) {
	withTok := func(mode string) *Store {
		return newStore(t, Config{LegacyToken: mode, Token: "deadbeef"})
	}
	get := func(s *Store, from, tok string) *Ident {
		r := httptest.NewRequest("GET", "/api/state?token="+tok, nil)
		r.Host = "127.0.0.1:7788"
		r.RemoteAddr = from + ":1234"
		return s.Authenticate(r)
	}

	if id := get(withTok("on"), "192.168.1.9", "deadbeef"); id == nil || id.Kind != "legacy" {
		t.Error("on：旧书签从局域网进来应该还能用")
	}
	if id := get(withTok("on"), "192.168.1.9", "deadbeef"); id != nil && id.Ambient {
		t.Error("?token= 是显式凭据，不该按 Ambient 处理（否则老脚本会被 CSRF 检查挡住）")
	}
	if get(withTok("on"), "192.168.1.9", "wrong") != nil {
		t.Error("token 不对就不该认")
	}
	if get(withTok("loopback"), "192.168.1.9", "deadbeef") != nil {
		t.Error("loopback 档位：局域网上的旧 token 必须失效")
	}
	if get(withTok("loopback"), "127.0.0.1", "deadbeef") == nil {
		t.Error("loopback 档位：本机上仍然要能用")
	}
	if get(withTok("off"), "127.0.0.1", "deadbeef") != nil {
		t.Error("off：哪儿都不能用")
	}
	// 没有 token 文件时，随便传个空的不能蒙进来
	if get(newStore(t, Config{LegacyToken: "on"}), "127.0.0.1", "") != nil {
		t.Error("没有旧 token 时不能被空值蒙过去")
	}
}

func TestNormalizeCode(t *testing.T) {
	code, _ := (&Store{codes: map[string]time.Time{}, now: time.Now}).MintCode()
	if len(code) != codeLen {
		t.Fatalf("配对码长度应该是 %d：%q", codeLen, code)
	}
	for _, c := range code {
		if c == '0' || c == '1' || c == 'I' || c == 'L' || c == 'O' {
			t.Errorf("配对码里不该出现容易看错的字符：%q", code)
		}
	}

	ok := []string{"23456789", "2345-6789", " 2345 6789 ", "23456789"}
	for _, in := range ok {
		if normalizeCode(in) != "23456789" {
			t.Errorf("%q 应该被接受", in)
		}
	}
	bad := []string{"", "2345678", "234567890", "2345678O", "2345678I", "2345678!", "中文中文中文中文"}
	for _, in := range bad {
		if normalizeCode(in) != "" {
			t.Errorf("%q 应该被拒", in)
		}
	}
}

// 目录分层的迁移：老位置有文件、新位置没有 → 搬过去，凭据不能失效。
// 这条要是错了，用户的表现是「升级之后所有设备都要重新配对」。
func TestMigrateFromLegacyLocation(t *testing.T) {
	base := t.TempDir()
	legacy := filepath.Join(base, "devices.json")
	dataDir := filepath.Join(base, "data")

	// 先用老布局造一份真凭据出来
	old, err := New(Config{Dir: base})
	if err != nil {
		t.Fatal(err)
	}
	code, _ := old.MintCode()
	_, token, err := old.Redeem(code, "iPhone", "192.168.1.9")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Fatalf("老位置该有文件：%v", err)
	}

	// 换成新布局
	moved, err := New(Config{Dir: dataDir, LegacyFile: legacy})
	if err != nil {
		t.Fatal(err)
	}
	if moved.Authenticate(req("192.168.1.9", token)) == nil {
		t.Error("迁移之后原来的凭据必须还认 —— 否则就是逼所有设备重新配对")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "devices.json")); err != nil {
		t.Errorf("新位置该有文件了：%v", err)
	}
	if _, err := os.Stat(legacy); err == nil {
		t.Error("老位置该已经空了（用的是 rename）")
	}

	// 再跑一次不能出事（幂等）
	if _, err := New(Config{Dir: dataDir, LegacyFile: legacy}); err != nil {
		t.Errorf("重复迁移不该报错：%v", err)
	}
}

// 新位置已经有文件时，绝不能被老位置的覆盖掉。
func TestMigrateDoesNotClobber(t *testing.T) {
	base := t.TempDir()
	dataDir := filepath.Join(base, "data")
	legacy := filepath.Join(base, "devices.json")

	cur, err := New(Config{Dir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	code, _ := cur.MintCode()
	_, token, _ := cur.Redeem(code, "iPad", "")
	if err := os.WriteFile(legacy, []byte(`{"Devices":null}`), 0o600); err != nil {
		t.Fatal(err)
	}

	again, err := New(Config{Dir: dataDir, LegacyFile: legacy})
	if err != nil {
		t.Fatal(err)
	}
	if again.Authenticate(req("192.168.1.9", token)) == nil {
		t.Error("新位置的数据被老文件覆盖了")
	}
}

// 这是整套端口分工要钉住的那一条：**开着本机免配对，公网口上照样进不来**。
//
// 场景就是这台机器上真实发生过的事：有人（或者一个 agent）在本机开发，随手
// `HERDR_WEB_TRUST_LOOPBACK=1` 图个省事，而机器上还跑着一条隧道。穿透进来的请求源地址
// 是 127.0.0.1，Host 也能自己写成 localhost —— 两个判据全被伪造得出来，唯一靠得住的
// 是「这个请求落在哪个监听上」。
func TestPublicPortIgnoresLoopbackTrust(t *testing.T) {
	s := newStore(t, Config{TrustLoopback: true})

	local := req("127.0.0.1", "")
	if id := s.Authenticate(local); id == nil || id.Kind != "loopback" {
		t.Fatalf("主口上本机免配对应当生效，得到 %+v", id)
	}

	pub := MarkPublicPort(req("127.0.0.1", ""))
	if id := s.Authenticate(pub); id != nil {
		t.Errorf("公网口上本机免配对必须不生效，得到 %+v —— 这就是一个公网免鉴权的 shell", id)
	}
}

// 旧 token 的 loopback 档同理：它的理由是「泄露给已经能在你机器上跑代码的东西不算泄露」，
// 而公网口上那个「本机」是 frpc，不是人。
func TestPublicPortIgnoresLegacyLoopbackToken(t *testing.T) {
	s := newStore(t, Config{LegacyToken: "loopback", Token: "tok123"})

	r := httptest.NewRequest("GET", "/api/state?token=tok123", nil)
	r.Host = "127.0.0.1:7788"
	r.RemoteAddr = "127.0.0.1:54321"
	if id := s.Authenticate(r); id == nil || id.Kind != "legacy" {
		t.Fatalf("主口上旧 token 的 loopback 档应当生效，得到 %+v", id)
	}

	r2 := httptest.NewRequest("GET", "/api/state?token=tok123", nil)
	r2.Host = "127.0.0.1:7788"
	r2.RemoteAddr = "127.0.0.1:54321"
	if id := s.Authenticate(MarkPublicPort(r2)); id != nil {
		t.Errorf("公网口上不该认这把 token，得到 %+v", id)
	}
}
