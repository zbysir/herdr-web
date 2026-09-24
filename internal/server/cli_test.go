package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"

	"github.com/zbysir/herdr-web/internal/auth"
	"github.com/zbysir/herdr-web/internal/config"
)

// `herdr-web connect` 那条路的接线：配对码换来的令牌在响应体里、不发 cookie；
// 拿它走 Authorization 头能开 /pty（没有 Origin）；同样的令牌放 cookie 里没 Origin 照旧拒。
func TestCLIPairAndPTY(t *testing.T) {
	store, err := auth.New(auth.Config{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Cfg: &config.Config{Shell: "/bin/sh"}, Auth: store, Gate: auth.NewGate()}

	pair := func(code, bearer string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/auth/pair",
			strings.NewReader(`{"code":"`+code+`","client":"cli"}`))
		r.Header.Set("content-type", "application/json")
		r.Header.Set("user-agent", auth.CLIAgent+"v1 (darwin; mbp)")
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		w := httptest.NewRecorder()
		s.apiAuth(w, r, []string{"auth", "pair"})
		return w
	}

	code, _ := store.MintCode()
	w := pair(code, "")
	if w.Code != 200 {
		t.Fatalf("配对失败：%d %s", w.Code, w.Body.String())
	}
	if len(w.Result().Cookies()) != 0 {
		t.Error("命令行那条不该发 cookie")
	}
	var got struct{ Token, DeviceID string }
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.Token == "" {
		t.Fatalf("响应里没令牌：%s", w.Body.String())
	}

	srv := httptest.NewServer(http.HandlerFunc(s.handlePTY))
	defer srv.Close()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/pty?cols=80&rows=24"

	h := http.Header{"Authorization": {"Bearer " + got.Token}}
	conn, resp, err := websocket.DefaultDialer.Dial(url, h)
	if err != nil {
		t.Fatalf("bearer 连 /pty 失败：%v（%v）", err, resp)
	}
	conn.Close()

	h = http.Header{"Cookie": {auth.CookieName + "=" + got.Token}}
	if _, resp, err := websocket.DefaultDialer.Dial(url, h); err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("没 Origin 的 cookie 请求该 403，得到 %v %v", err, resp)
	}
}

// 批准那一下必须**刚过了一把 passkey**：光有一个登录着的浏览器不够（手机被拿去一次的情形）。
// 漏接的表现是一切正常、批准成功 —— 完全静默，所以测接线。
func TestCLIApproveNeedsFreshPasskey(t *testing.T) {
	dir := t.TempDir()
	store, err := auth.New(auth.Config{Dir: dir + "/auth"})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Cfg: &config.Config{}, Auth: store, Gate: auth.NewGate(),
		Passkeys: passkeysWithOne(t, dir+"/pk"), RPID: "herdr.example.com"}

	call := func(op, body, cookie string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/auth/cli/"+op, strings.NewReader(body))
		r.Host = "herdr.example.com"
		r.Header.Set(CSRFHeader, "1")
		r.Header.Set("user-agent", auth.CLIAgent+"v1 (darwin; mbp)")
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: cookie})
		}
		w := httptest.NewRecorder()
		s.apiAuth(w, r, []string{"auth", "cli", op})
		return w
	}

	w := call("start", `{}`, "")
	var st struct{ ID, Poll, Code string }
	_ = json.Unmarshal(w.Body.Bytes(), &st)
	if w.Code != 200 || st.Code == "" {
		t.Fatalf("start：%d %s", w.Code, w.Body.String())
	}

	// 一台刚配对进来的浏览器（VerifiedAt 新鲜，但从没刷过 passkey）
	code, _ := store.MintCode()
	dev, tok, _ := store.Redeem(code, "iPhone Safari", "")
	if w := call("peek", `{"code":"`+st.Code+`"}`, tok); w.Code != 200 {
		t.Fatalf("peek 该能看：%d %s", w.Code, w.Body.String())
	}
	w = call("approve", `{"code":"`+st.Code+`"}`, tok)
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "stepup") {
		t.Fatalf("没刷 passkey 该 403 stepup，得到 %d %s", w.Code, w.Body.String())
	}
	if w := call("poll", `{"id":"`+st.ID+`","poll":"`+st.Poll+`"}`, ""); !strings.Contains(w.Body.String(), "pending") {
		t.Fatalf("被挡住之后该还是 pending：%s", w.Body.String())
	}

	// 刷过一把（passkey login/finish 就是调这个）之后就放行，发起方领到令牌
	store.MarkVerified(dev.ID)
	if w := call("approve", `{"code":"`+st.Code+`"}`, tok); w.Code != 200 {
		t.Fatalf("刷过 passkey 之后该放行：%d %s", w.Code, w.Body.String())
	}
	w = call("poll", `{"id":"`+st.ID+`","poll":"`+st.Poll+`"}`, "")
	if !strings.Contains(w.Body.String(), `"token"`) {
		t.Fatalf("批准之后该领到令牌：%s", w.Body.String())
	}
}
