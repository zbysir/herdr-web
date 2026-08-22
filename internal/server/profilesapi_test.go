package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zbysir/herdr-web/internal/config"
	"github.com/zbysir/herdr-web/internal/profiles"
	"github.com/zbysir/herdr-web/internal/softkeys"
	"github.com/zbysir/herdr-web/internal/topbar"
)

func profServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	return &Server{
		Cfg:      &config.Config{Dir: dir},
		Softkeys: &softkeys.Store{Dir: dir},
		Topbar:   &topbar.Store{Dir: dir},
		Profiles: &profiles.Store{Dir: dir},
	}
}

// call 直接打 API 那一层（认证在外面一层，这儿不掺和）。
func call(t *testing.T, s *Server, method, path, body string) (int, map[string]any) {
	t.Helper()
	var rd *strings.Reader
	if body != "" {
		rd = strings.NewReader(body)
	} else {
		rd = strings.NewReader("")
	}
	r := httptest.NewRequest(method, path, rd)
	r.Header.Set("User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0) Safari/605.1")
	w := httptest.NewRecorder()
	seg := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api"), "/"), "/")
	switch seg[0] {
	case "profiles":
		s.apiProfiles(w, r, seg)
	case "softkeys":
		s.apiSoftkeys(w, r)
	case "topbar":
		s.apiTopbar(w, r)
	default:
		t.Fatalf("没这个口: %s", path)
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("%s %s 的响应不是 JSON: %s", method, path, w.Body.String())
	}
	return w.Code, out
}

// TestProfilesEndToEnd 走一遍真实的那条路：报到 → 新建（复制）→ 绑过来 → 改自己那一套
// → 别的设备那一套没被动 → 删掉之后落回默认。
func TestProfilesEndToEnd(t *testing.T) {
	s := profServer(t)
	const me = "install-aaaaaa"
	const q = "?install=" + me

	// 报到：第一次来，落到默认那一套
	code, out := call(t, s, "POST", "/api/profiles/hello"+q, `{"kind":"phone"}`)
	if code != 200 || out["current"] != profiles.Default {
		t.Fatalf("报到该落到默认: %d %+v", code, out)
	}
	// Label 是服务端从 UA 猜的，不收前端给的
	insts, _ := out["installs"].([]any)
	if len(insts) != 1 {
		t.Fatalf("设备表该有一台: %+v", out["installs"])
	}
	if lbl, _ := insts[0].(map[string]any)["label"].(string); !strings.Contains(lbl, "iPhone") {
		t.Errorf("Label 该从 UA 猜出来，拿到 %q", lbl)
	}

	// 先把默认那一套的顶栏改一下（当作「平板那套」）
	if code, _ = call(t, s, "PUT", "/api/topbar"+q, `{"items":["panes","files","kbd"]}`); code != 200 {
		t.Fatalf("存默认那一套的顶栏失败: %d", code)
	}

	// 新建「手机」并从默认那套复制排布
	code, out = call(t, s, "POST", "/api/profiles"+q, `{"name":"手机","kind":"phone","copyFrom":"default"}`)
	if code != 200 {
		t.Fatalf("新建失败: %d %+v", code, out)
	}
	// 建完还没绑，这台设备用的仍是默认那一套 —— 绑是单独一下（一个口一件事）
	if out["current"] != profiles.Default {
		t.Errorf("建完不该自动跟着切: %+v", out["current"])
	}
	code, out = call(t, s, "POST", "/api/profiles/bind"+q, `{"profile":"p2"}`)
	if code != 200 || out["current"] != "p2" {
		t.Fatalf("绑过来失败: %d %+v", code, out)
	}

	// 不带 ?profile= 的请求现在拿到的是 p2，而且是复制过来的那一份
	code, out = call(t, s, "GET", "/api/topbar"+q, "")
	if code != 200 || out["profile"] != "p2" {
		t.Fatalf("该按绑定算到 p2: %d %+v", code, out)
	}
	if got := ids(out["items"]); got != "panes,files,kbd,settings" {
		t.Errorf("复制过来的顶栏不对: %v", got)
	}

	// 在手机那套上收成一个键
	if code, _ = call(t, s, "PUT", "/api/topbar"+q, `{"items":["kbd"]}`); code != 200 {
		t.Fatalf("存 p2 的顶栏失败: %d", code)
	}
	// 默认那一套一点没动（显式指定看得到）
	_, out = call(t, s, "GET", "/api/topbar"+q+"&profile=default", "")
	if got := ids(out["items"]); got != "panes,files,kbd,settings" {
		t.Errorf("改 p2 不该动默认那一套: %v", got)
	}

	// 删掉 p2：这台设备落回默认，而且请求跟着回到默认那一份
	if code, out = call(t, s, "DELETE", "/api/profiles/p2"+q, ""); code != 200 {
		t.Fatalf("删除失败: %d %+v", code, out)
	}
	if out["current"] != profiles.Default {
		t.Errorf("删掉之后该落回默认: %+v", out["current"])
	}
	_, out = call(t, s, "GET", "/api/topbar"+q, "")
	if got := ids(out["items"]); got != "panes,files,kbd,settings" {
		t.Errorf("删掉之后该拿到默认那一份: %v", got)
	}

	// 写到一套已经不存在的排布上要 404，**不能静默存到别的地方去**
	if code, _ = call(t, s, "PUT", "/api/topbar"+q+"&profile=p2", `{"items":["kbd"]}`); code != 404 {
		t.Errorf("存到已删的一套该 404，拿到 %d", code)
	}
	if code, _ = call(t, s, "PUT", "/api/softkeys"+q+"&profile=p2", `{"rows":1,"lib":[],"bar":[[]]}`); code != 404 {
		t.Errorf("软键条存到已删的一套该 404，拿到 %d", code)
	}
	// 但 GET 不挑食：退回这台设备该用的那一套，并在响应里说清是哪一套
	code, out = call(t, s, "GET", "/api/softkeys"+q+"&profile=p2", "")
	if code != 200 || out["profile"] != "p2" {
		t.Fatalf("GET 该照旧给一份能用的: %d %+v", code, out)
	}
}

func TestPrefsRoundTrip(t *testing.T) {
	s := profServer(t)
	const q = "?install=install-aaaaaa"
	call(t, s, "POST", "/api/profiles/hello"+q, `{"kind":"desktop"}`)

	code, out := call(t, s, "PUT", "/api/profiles/default/prefs"+q, `{"prefs":{"fontSize":"15"}}`)
	if code != 200 {
		t.Fatalf("存开关失败: %d %+v", code, out)
	}
	if code, out = call(t, s, "GET", "/api/profiles"+q, ""); code != 200 {
		t.Fatal(code)
	}
	p, _ := out["prefs"].(map[string]any)
	if p["fontSize"] != "15" {
		t.Errorf("开关没读回来: %+v", out["prefs"])
	}
	// 白名单外的键要报错，不是静默丢
	if code, _ = call(t, s, "PUT", "/api/profiles/default/prefs"+q, `{"prefs":{"kbdFullErr":"x"}}`); code != 400 {
		t.Errorf("白名单外的键该 400，拿到 %d", code)
	}
}

func ids(v any) string {
	l, _ := v.([]any)
	out := make([]string, 0, len(l))
	for _, it := range l {
		s, _ := it.(string)
		out = append(out, s)
	}
	return strings.Join(out, ",")
}
