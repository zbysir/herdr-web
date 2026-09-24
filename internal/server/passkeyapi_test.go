package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/zbysir/herdr-web/internal/auth"
	"github.com/zbysir/herdr-web/internal/config"
)

// 造一个「已经注册过 passkey」的账号。直接写盘是因为 Passkey 的公钥细节在 auth 包里是
// 非导出的，而这儿要测的是 server 这一层的接线，不是 webauthn 本身。
func passkeysWithOne(t *testing.T, dir string) *auth.Passkeys {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	const f = `{"handle":"AAAA","keys":[{"id":"k1","label":"手机"}]}`
	if err := os.WriteFile(filepath.Join(dir, "passkeys.json"), []byte(f), 0o600); err != nil {
		t.Fatal(err)
	}
	pk, err := auth.NewPasskeys(auth.PasskeyConfig{
		Dir: dir, RPID: "herdr.example.com",
		Origins: []string{"https://herdr.example.com"}, Display: "t",
	})
	if err != nil {
		t.Fatal(err)
	}
	if pk.Count() != 1 {
		t.Fatalf("该有 1 把，得到 %d", pk.Count())
	}
	return pk
}

// **刚拿配对码进来的设备不能直接给自己再注册一把 passkey。**
//
// 这条测的是接线，不是判据本身（判据在 auth.TestStepUpNeeded）：漏接的表现是
// 一切正常、注册成功，而 `revoke all` 从此赶不走那个人 —— 完全静默，见 SECURITY.md §4(d)。
func TestRegisterPasskeyNeedsStepUp(t *testing.T) {
	dir := t.TempDir()
	store, err := auth.New(auth.Config{Dir: filepath.Join(dir, "auth")})
	if err != nil {
		t.Fatal(err)
	}
	code, _ := store.MintCode()
	dev, token, err := store.Redeem(code, "刚配上的设备", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	// 配对那一刻 VerifiedAt 就是当下 —— 所以这台设备在 reauth 那一层是「新鲜」的，
	// 唯一拦得住它的只有 step-up 认的 PasskeyAt。
	if !dev.PasskeyAt.IsZero() {
		t.Fatal("配对不该写 PasskeyAt —— 写了的话 step-up 对刚进来的人就失效了")
	}

	s := &Server{
		Cfg: &config.Config{}, Auth: store,
		Passkeys: passkeysWithOne(t, filepath.Join(dir, "pk")),
		RPID:     "herdr.example.com",
	}

	for _, step := range []string{"begin", "finish"} {
		r := httptest.NewRequest(http.MethodPost, "/api/auth/passkey/register/"+step, nil)
		r.Host = "herdr.example.com"
		r.Header.Set(CSRFHeader, "1")
		r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})
		w := httptest.NewRecorder()
		s.apiPasskey(w, r, []string{"auth", "passkey", "register", step})

		if w.Code != http.StatusForbidden {
			t.Fatalf("%s：想要 403，得到 %d（body=%s）", step, w.Code, w.Body.String())
		}
		var got struct{ Reason string }
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		// 必须是 403+stepup 而不是 401：401 在前端会广播 UNAUTHED，把整个应用弹回登录屏
		if got.Reason != "stepup" {
			t.Errorf("%s：reason = %q，想要 stepup", step, got.Reason)
		}
	}
}

// 一把 passkey 都没有时**不设门**，否则第一把永远注册不上（把自己锁在门外）。
func TestRegisterFirstPasskeyNotBlocked(t *testing.T) {
	dir := t.TempDir()
	store, err := auth.New(auth.Config{Dir: filepath.Join(dir, "auth")})
	if err != nil {
		t.Fatal(err)
	}
	code, _ := store.MintCode()
	_, token, err := store.Redeem(code, "第一台", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	pk, err := auth.NewPasskeys(auth.PasskeyConfig{
		Dir: filepath.Join(dir, "pk"), RPID: "herdr.example.com",
		Origins: []string{"https://herdr.example.com"}, Display: "t",
	})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Cfg: &config.Config{}, Auth: store, Passkeys: pk, RPID: "herdr.example.com"}

	r := httptest.NewRequest(http.MethodPost, "/api/auth/passkey/register/begin", nil)
	r.Host = "herdr.example.com"
	r.Header.Set(CSRFHeader, "1")
	r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})
	w := httptest.NewRecorder()
	s.apiPasskey(w, r, []string{"auth", "passkey", "register", "begin"})

	if w.Code == http.StatusForbidden {
		t.Fatalf("一把都没有时不该挡（会把人锁在门外）：%s", w.Body.String())
	}
}
