package auth

import "testing"

// 命令行客户端那份令牌走 Authorization 头：认得出、而且**不是 Ambient**。
// 标成 Ambient 的话 /api 会要它带 CSRF 头 + Origin，/pty 会按「没 Origin 的 cookie 请求」拒掉。
func TestBearerAuth(t *testing.T) {
	s := newStore(t, Config{})
	code, _ := s.MintCode()
	_, tok, err := s.Redeem(code, CLIAgent+"v1 (darwin; mbp)", "1.2.3.4")
	if err != nil {
		t.Fatal(err)
	}
	r := req("8.8.8.8", "")
	r.Header.Set("Authorization", "Bearer "+tok)
	id := s.Authenticate(r)
	if id == nil || id.Kind != "device" || id.Ambient {
		t.Fatalf("bearer 该认成非 Ambient 的 device，得到 %+v", id)
	}
	if id.Label != "命令行 · mbp" {
		t.Errorf("label = %q", id.Label)
	}
	r.Header.Set("Authorization", "Bearer nope")
	if s.Authenticate(r) != nil {
		t.Error("错的令牌不该认")
	}
}
