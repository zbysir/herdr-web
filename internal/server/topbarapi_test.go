package server

import (
	"testing"

	"github.com/zbysir/herdr-web/internal/config"
	"github.com/zbysir/herdr-web/internal/profiles"
	"github.com/zbysir/herdr-web/internal/softkeys"
	"github.com/zbysir/herdr-web/internal/topbar"
)

// keyServer 和 profServer 一样是手搭的，但**多接一根线**：顶栏那个 Store 的 Keys 钩子。
// 真实的那根接在 New 里（topbar 故意不 import softkeys），TestTopbarKeysHookIsWired 盯着它。
func keyServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	s := &Server{
		Cfg:      &config.Config{Dir: dir},
		Softkeys: &softkeys.Store{Dir: dir},
		Topbar:   &topbar.Store{Dir: dir},
		Profiles: &profiles.Store{Dir: dir},
	}
	s.Topbar.Keys = s.Softkeys.LibIDs
	return s
}

// TestTopbarKeysHookIsWired 光有 keyServer 里那一行不够 —— 真正生效的是 New 里的那一行，
// 删掉的表现是「顶栏上能存进指向不存在定义的引用」，而那要等到别的设备上打开顶栏才现形。
func TestTopbarKeysHookIsWired(t *testing.T) {
	s := New(&config.Config{Dir: t.TempDir()}, nil, nil, nil, Options{})
	if s.Topbar.Keys == nil {
		t.Fatal("New 里没把 Topbar.Keys 接上：顶栏存 key: 引用时不会核定义还在不在")
	}
}

// TestTopbarAcceptsMyKeys 「我的按键」上顶栏那条路：能存进去、读得回来、指向空处的存不进去。
func TestTopbarAcceptsMyKeys(t *testing.T) {
	s := keyServer(t)

	// 出厂那份「我的按键」里的第一个定义
	code, sk := call(t, s, "GET", "/api/softkeys", "")
	if code != 200 {
		t.Fatalf("读软键条失败：%d %+v", code, sk)
	}
	lib, _ := sk["lib"].([]any)
	if len(lib) == 0 {
		t.Fatal("出厂配置里该有「我的按键」")
	}
	first, _ := lib[0].(map[string]any)
	id, _ := first["id"].(string)
	if id == "" {
		t.Fatalf("定义没有 ID：%+v", first)
	}

	code, out := call(t, s, "PUT", "/api/topbar", `{"items":["panes","`+topbar.KeyPrefix+id+`"]}`)
	if code != 200 {
		t.Fatalf("存不进去：%d %+v", code, out)
	}
	items, _ := out["items"].([]any)
	if len(items) != 3 || items[1] != topbar.KeyPrefix+id || items[2] != "settings" {
		t.Fatalf("存回来的不对（⚙ 该补在末尾）：%+v", items)
	}

	// 指向不存在的定义：拒。编辑器只会从库里拖，走到这儿就是前端有 bug，静默修掉只会
	// 让 bug 留在那儿
	code, out = call(t, s, "PUT", "/api/topbar", `{"items":["`+topbar.KeyPrefix+`k9999"]}`)
	if code != 400 {
		t.Fatalf("指向不存在的定义该 400：%d %+v", code, out)
	}
}

// TestSoftkeysSaveClearsTopbarRefs 删掉一个定义，顶栏上指向它的那一项要跟着走。
//
// 这条是端到端才盯得住的：prune 挂在**软键条那个口**上（内部两个包互不 import），
// 漏了的表现是顶栏上留一个画不出来的幽灵项 —— 占着 MaxItems 的名额，下次打开编辑器
// 又静悄悄消失，于是「我什么都没动，顶栏怎么少了一个」。
func TestSoftkeysSaveClearsTopbarRefs(t *testing.T) {
	s := keyServer(t)

	// 只留一个定义（k1），条上也只引用它
	code, out := call(t, s, "PUT", "/api/softkeys",
		`{"rows":1,"lib":[{"id":"k1","label":"Esc","send":"esc"},{"id":"k2","label":"Tab","send":"tab"}],"bar":[["k1","k2"]]}`)
	if code != 200 {
		t.Fatalf("存软键条失败：%d %+v", code, out)
	}
	// 两个都放上顶栏
	code, out = call(t, s, "PUT", "/api/topbar",
		`{"items":["`+topbar.KeyPrefix+`k1","`+topbar.KeyPrefix+`k2"]}`)
	if code != 200 {
		t.Fatalf("存顶栏失败：%d %+v", code, out)
	}

	// 把 k2 删掉（软键条那边存一份不含它的定义）
	code, out = call(t, s, "PUT", "/api/softkeys",
		`{"rows":1,"lib":[{"id":"k1","label":"Esc","send":"esc"}],"bar":[["k1"]]}`)
	if code != 200 {
		t.Fatalf("删定义失败：%d %+v", code, out)
	}

	code, out = call(t, s, "GET", "/api/topbar", "")
	if code != 200 {
		t.Fatalf("读顶栏失败：%d %+v", code, out)
	}
	items, _ := out["items"].([]any)
	if len(items) != 2 || items[0] != topbar.KeyPrefix+"k1" || items[1] != "settings" {
		t.Fatalf("k2 的引用该被清掉、k1 该留着：%+v", items)
	}
}
