package topbar

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zbysir/herdr-web/internal/profiles"
)

func TestLoadFallsBackToDefaults(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	// 文件不在
	if got := s.Load(profiles.Default).Items; strings.Join(got, ",") != strings.Join(Defaults(), ",") {
		t.Fatalf("没有文件时该给出厂配置，给的是 %v", got)
	}
	// 文件存坏了
	if err := os.WriteFile(filepath.Join(s.Dir, "topbar.json"), []byte("{ not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := s.Load(profiles.Default).Items; strings.Join(got, ",") != strings.Join(Defaults(), ",") {
		t.Fatalf("坏文件时该给出厂配置，给的是 %v", got)
	}
}

func TestLoadDropsUnknownAndKeepsRest(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	// 「从新版本降级回来」的样子：里面有这个版本不认识的 id
	body := `{"items":["panes","teleport","settings","panes"]}`
	if err := os.WriteFile(filepath.Join(s.Dir, "topbar.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got := s.Load(profiles.Default).Items
	if strings.Join(got, ",") != "panes,settings" {
		t.Fatalf("不认识的 id 该丢掉、重复的该去掉，剩下的照用；拿到 %v", got)
	}
}

func TestLoadAllUnknownGivesDefaults(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	body := `{"items":["teleport","warp"]}`
	if err := os.WriteFile(filepath.Join(s.Dir, "topbar.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := s.Load(profiles.Default).Items; strings.Join(got, ",") != strings.Join(Defaults(), ",") {
		t.Fatalf("一个都认不出时该给出厂配置，给的是 %v", got)
	}
}

func TestLoadKeepsEmptyBarButPinsSettings(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	// 空栏：合法（用户可以把顶栏清到只剩设置），但 ⚙ 得补回来
	if err := os.WriteFile(filepath.Join(s.Dir, "topbar.json"), []byte(`{"items":["full"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := s.Load(profiles.Default).Items; strings.Join(got, ",") != "full,settings" {
		t.Fatalf("设置该被补回来，拿到 %v", got)
	}
}

func TestSaveValidates(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	if _, err := s.Save(profiles.Default, Config{Items: []string{"panes", "teleport"}}); err == nil {
		t.Fatal("不认识的 id 该报错")
	}
	if _, err := s.Save(profiles.Default, Config{Items: []string{"panes", "panes"}}); err == nil {
		t.Fatal("重复该报错")
	}
	long := make([]string, 0, MaxItems+1)
	for i := 0; i <= MaxItems; i++ {
		long = append(long, "panes") // 长度先超了，重复的检查在后面
	}
	if _, err := s.Save(profiles.Default, Config{Items: long}); err == nil {
		t.Fatal("超过上限该报错")
	}
}

func TestSaveRoundTripAndPins(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	out, err := s.Save(profiles.Default, Config{Items: []string{"compose", "panes"}})
	if err != nil {
		t.Fatal(err)
	}
	// 存的时候没给设置，落盘时补在末尾 —— 不然就把自己锁在配置外面了
	if strings.Join(out.Items, ",") != "compose,panes,settings" {
		t.Fatalf("返回的顺序不对：%v", out.Items)
	}
	if got := s.Load(profiles.Default).Items; strings.Join(got, ",") != "compose,panes,settings" {
		t.Fatalf("读回来的顺序不对：%v", got)
	}
	// 文件是给人看 / 偶尔手改的，落盘就该是那一串 id
	b, err := os.ReadFile(filepath.Join(s.Dir, "topbar.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f Config
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	if strings.Join(f.Items, ",") != "compose,panes,settings" {
		t.Fatalf("落盘的内容不对：%s", b)
	}
}

func TestPinnedAreKnown(t *testing.T) {
	for _, p := range Pinned {
		if !known(p) {
			t.Fatalf("Pinned 里的 %q 不在 Actions 里", p)
		}
	}
	for _, d := range Defaults() {
		if !known(d) {
			t.Fatalf("出厂配置里的 %q 不在 Actions 里", d)
		}
	}
}

// TestProfilesSplit 顶栏按 profile 分段之后的那几条。
func TestProfilesSplit(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Dir: dir}

	// 老文件：只有顶层 items
	if err := os.WriteFile(filepath.Join(dir, "topbar.json"),
		[]byte(`{"items":["panes","files","settings"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := s.Load(profiles.Default).Items; strings.Join(got, ",") != "panes,files,settings" {
		t.Fatalf("老文件该原样读出来，拿到 %v", got)
	}
	// 没排过的那一套退到默认
	if got := s.Load("p2").Items; strings.Join(got, ",") != "panes,files,settings" {
		t.Fatalf("没排过该退到默认，拿到 %v", got)
	}

	// p2 上只留两个
	if _, err := s.Save("p2", Config{Items: []string{"kbd"}}); err != nil {
		t.Fatal(err)
	}
	if got := s.Load("p2").Items; strings.Join(got, ",") != "kbd,settings" {
		t.Fatalf("p2 该是自己那一份（⚙ 补回来），拿到 %v", got)
	}
	if got := s.Load(profiles.Default).Items; strings.Join(got, ",") != "panes,files,settings" {
		t.Fatalf("存 p2 不该动默认那一套，拿到 %v", got)
	}
	// 顶层是默认那一套的镜像（降级用）
	b, _ := os.ReadFile(filepath.Join(dir, "topbar.json"))
	var f file
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	if strings.Join(f.Items, ",") != "panes,files,settings" || len(f.Profiles) != 2 {
		t.Errorf("顶层该镜像默认那一套、profiles 里两套都在:\n%s", b)
	}

	// 复制 / 删段
	if err := s.Copy("p2", "p3"); err != nil {
		t.Fatal(err)
	}
	if got := s.Load("p3").Items; strings.Join(got, ",") != "kbd,settings" {
		t.Fatalf("复制过来的不对，拿到 %v", got)
	}
	if err := s.Drop("p3"); err != nil {
		t.Fatal(err)
	}
	if got := s.Load("p3").Items; strings.Join(got, ",") != "panes,files,settings" {
		t.Fatalf("删掉那一段之后该退到默认，拿到 %v", got)
	}
	if err := s.Drop(profiles.Default); err == nil {
		t.Error("默认那一段删不掉")
	}
}

/* ---------------------------------------------- 「我的按键」上顶栏（key: 引用） */

func TestKeyRefShape(t *testing.T) {
	for _, ok := range []string{"key:k1", "key:k99", "key:a-b_c.d"} {
		if _, is := KeyRef(ok); !is {
			t.Errorf("%q 该认成引用", ok)
		}
	}
	// 内置 id 不是引用；畸形的一律不认（ID 是从客户端原样收下来的，见 KeyRef 那段注释）
	bad := []string{"panes", "font+", "key:", "key:a:b", "key:a b", "key:" + strings.Repeat("x", 65), "keyk1", ":k1"}
	for _, b := range bad {
		if id, is := KeyRef(b); is {
			t.Errorf("%q 不该认成引用（认出了 %q）", b, id)
		}
	}
}

func TestSaveChecksKeyRefsAgainstLib(t *testing.T) {
	s := &Store{Dir: t.TempDir(), Keys: func() map[string]bool { return map[string]bool{"k1": true} }}

	if _, err := s.Save(profiles.Default, Config{Items: []string{"panes", "key:k1"}}); err != nil {
		t.Fatalf("存在的定义该收下：%v", err)
	}
	if got := s.Load(profiles.Default).Items; strings.Join(got, ",") != "panes,key:k1,settings" {
		t.Fatalf("引用该原样读回来，拿到 %v", got)
	}
	// 不存在的定义：存盘那一侧是严格的（编辑器只会从库里拖，走到这儿就是前端有 bug）
	if _, err := s.Save(profiles.Default, Config{Items: []string{"key:k9"}}); err == nil {
		t.Error("指向不存在的定义该报错")
	}
	// 畸形引用按「不认识的按钮」拒
	if _, err := s.Save(profiles.Default, Config{Items: []string{"key:a b"}}); err == nil {
		t.Error("畸形引用该报错")
	}
	// 同一个键放两次照旧拒（顶栏上一个按钮只有一个）
	if _, err := s.Save(profiles.Default, Config{Items: []string{"key:k1", "key:k1"}}); err == nil {
		t.Error("重复的引用该报错")
	}

	// 钩子是 nil（单测 / 降级路径）时只查形状，别把配置卡在门外
	s2 := &Store{Dir: t.TempDir()}
	if _, err := s2.Save(profiles.Default, Config{Items: []string{"key:k9"}}); err != nil {
		t.Errorf("没有 Keys 钩子时该只查形状：%v", err)
	}
}

// TestLoadKeepsDanglingKeyRefs 读盘**故意不核**引用还在不在。
//
// 核的话一次 softkeys.json 读失败（或者钩子回了一份出厂 lib）就能把人家配好的键从顶栏上
// 抹掉，而读失败是暂时的、配置不是。认不出的引用交给前端渲染时丢掉。
func TestLoadKeepsDanglingKeyRefs(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Dir: dir, Keys: func() map[string]bool { return map[string]bool{} }}
	body := `{"items":["panes","key:k7","teleport","settings"]}`
	if err := os.WriteFile(filepath.Join(dir, "topbar.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	// key:k7 留着（定义此刻不在也留），teleport 丢掉（内置白名单里没有）
	if got := s.Load(profiles.Default).Items; strings.Join(got, ",") != "panes,key:k7,settings" {
		t.Fatalf("拿到 %v", got)
	}
}

func TestPruneKeys(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Dir: dir, Keys: func() map[string]bool { return map[string]bool{"k1": true, "k2": true} }}
	if _, err := s.Save(profiles.Default, Config{Items: []string{"panes", "key:k1", "key:k2"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save("p2", Config{Items: []string{"key:k2"}}); err != nil {
		t.Fatal(err)
	}

	// k2 被删了：**所有套**里的引用一起清掉（定义是全局的）
	if err := s.PruneKeys(map[string]bool{"k1": true}); err != nil {
		t.Fatal(err)
	}
	if got := s.Load(profiles.Default).Items; strings.Join(got, ",") != "panes,key:k1,settings" {
		t.Errorf("默认那一套：拿到 %v", got)
	}
	// p2 上清完只剩空的，⚙ 得补回来（不然就把自己锁在配置外面了）
	if got := s.Load("p2").Items; strings.Join(got, ",") != "settings" {
		t.Errorf("p2：拿到 %v", got)
	}

	// 没有一处要清就别写盘 —— 快捷键条每存一次都会调这儿一遍
	before, err := os.ReadFile(filepath.Join(dir, "topbar.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PruneKeys(map[string]bool{"k1": true}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "topbar.json"))
	if string(before) != string(after) {
		t.Error("没得清的时候不该改文件")
	}

	// 内置 id 一个都不能被 prune 带走
	if err := s.PruneKeys(map[string]bool{}); err != nil {
		t.Fatal(err)
	}
	if got := s.Load(profiles.Default).Items; strings.Join(got, ",") != "panes,settings" {
		t.Errorf("内置 id 不该被清掉：拿到 %v", got)
	}
}
