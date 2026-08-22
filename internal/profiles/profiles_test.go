package profiles

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const inst = "install-aaaaaa"

func TestLoadIsLenient(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	// 文件不在
	if c := s.Load(); len(c.Profiles) != 1 || c.Profiles[0].ID != Default {
		t.Fatalf("没有文件时该只有「默认」一套，拿到 %+v", c.Profiles)
	}
	// 存坏了
	if err := os.WriteFile(filepath.Join(s.Dir, "profiles.json"), []byte("{ not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if c := s.Load(); len(c.Profiles) != 1 || c.Profiles[0].ID != Default {
		t.Fatalf("坏文件时该退回出厂，拿到 %+v", c.Profiles)
	}
	// 说不通的内容：没有 default、重复 ID、名字空的、认不出的 kind、指向已删 profile 的设备
	body := `{"profiles":[{"id":"p2","name":"","kind":"watch"},{"id":"p2","name":"重的"}],
	          "installs":{"install-aaaaaa":{"profile":"gone"},"短":{"profile":"p2"}}}`
	if err := os.WriteFile(filepath.Join(s.Dir, "profiles.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c := s.Load()
	if len(c.Profiles) != 2 || c.Profiles[0].ID != Default {
		t.Fatalf("default 该被补在最前面，拿到 %+v", c.Profiles)
	}
	if c.Profiles[1].Name != "p2" {
		t.Errorf("名字丢了该退到 ID（下拉里总得点得到它），拿到 %q", c.Profiles[1].Name)
	}
	if c.Profiles[1].Kind != "" {
		t.Errorf("认不出的 kind 该清掉，拿到 %q", c.Profiles[1].Kind)
	}
	if got := c.Installs[inst].Profile; got != Default {
		t.Errorf("指向已删 profile 的设备该落回默认，拿到 %q", got)
	}
	if _, ok := c.Installs["短"]; ok {
		t.Error("不合法的 installId 该丢掉")
	}
}

func TestHelloBindsOnceAndGuessesByKind(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	if _, _, err := s.Create("手机", "phone"); err != nil {
		t.Fatal(err)
	}
	// 第一次来：按 kind 猜到「手机」那套
	c, cur := s.Hello(inst, "phone", "iPhone · Safari")
	if cur != "p2" {
		t.Fatalf("kind 对得上时该挑那一套，拿到 %q", cur)
	}
	if c.Installs[inst].Label != "iPhone · Safari" {
		t.Errorf("Label 没记住: %+v", c.Installs[inst])
	}
	// 人手动换到默认那套之后，**再报到不能又被猜回去** —— 猜只发生在还没绑的那一下
	if _, err := s.Bind(inst, Default); err != nil {
		t.Fatal(err)
	}
	if _, cur = s.Hello(inst, "phone", ""); cur != Default {
		t.Fatalf("绑过之后该照绑定来，拿到 %q", cur)
	}
	// 猜不着（没有 desktop 那一套）→ 默认
	if _, cur = s.Hello("install-bbbbbb", "desktop", "Mac · Chrome"); cur != Default {
		t.Fatalf("猜不着该落到默认，拿到 %q", cur)
	}
	// install 不合法：什么都不写，给默认那一套
	before, _ := os.ReadFile(s.path())
	if _, cur = s.Hello("x", "phone", "谁"); cur != Default {
		t.Fatalf("install 不合法时该给默认，拿到 %q", cur)
	}
	after, _ := os.ReadFile(s.path())
	if string(before) != string(after) {
		t.Error("install 不合法时不该写盘")
	}
}

func TestHelloForgetsOldestWhenFull(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	for i := 0; i < MaxInstalls+5; i++ {
		s.Hello(strings.Repeat("a", 6)+string(rune('A'+i%26))+string(rune('a'+i/26)), "phone", "x")
	}
	c := s.Load()
	if len(c.Installs) > MaxInstalls {
		t.Fatalf("设备表该收在 %d 以内，拿到 %d", MaxInstalls, len(c.Installs))
	}
	// 最后报到的那台一定还在（那正是现在坐在这儿用的人）
	last := strings.Repeat("a", 6) + string(rune('A'+(MaxInstalls+4)%26)) + string(rune('a'+(MaxInstalls+4)/26))
	if _, ok := c.Installs[last]; !ok {
		t.Errorf("刚报到的那台被忘了: %q", last)
	}
}

func TestCreateRenameDelete(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	_, p, err := s.Create("平板", "tablet")
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "p2" {
		t.Errorf("ID 该接着 default 往下发（p2），拿到 %q", p.ID)
	}
	if _, _, err := s.Create("平板", ""); err == nil {
		t.Error("重名该被拒 —— 下拉里两个「平板」分不出来")
	}
	if _, _, err := s.Create("   ", ""); err == nil {
		t.Error("空名字该被拒")
	}
	// 名字掐到 MaxName
	_, long, err := s.Create(strings.Repeat("长", MaxName+5), "")
	if err != nil {
		t.Fatal(err)
	}
	if n := len([]rune(long.Name)); n != MaxName {
		t.Errorf("名字该掐到 %d 个字，拿到 %d", MaxName, n)
	}
	if _, err := s.Rename("p2", "手机"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Load().Get("p2"); got.Name != "手机" {
		t.Errorf("改名没生效: %+v", got)
	}
	if _, err := s.Rename("p2", long.Name); err == nil {
		t.Error("改成已有的名字该被拒")
	}
	if _, err := s.Rename("nope", "x"); err == nil {
		t.Error("改一套不存在的该报错")
	}
	// 删掉时绑在它上面的设备落回默认
	if _, err := s.Bind(inst, "p2"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Delete("p2"); err != nil {
		t.Fatal(err)
	}
	if got := s.Load().Installs[inst].Profile; got != Default {
		t.Errorf("删掉之后绑在上面的设备该落回默认，拿到 %q", got)
	}
	if _, err := s.Delete(Default); err == nil {
		t.Error("默认那一套删不掉 —— 所有兜底都落在它身上")
	}
}

func TestBindChecks(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	if _, err := s.Bind(inst, "nope"); err == nil {
		t.Error("绑到不存在的一套该被拒（绑上就是静默用错排布）")
	}
	if _, err := s.Bind("短", Default); err == nil {
		t.Error("不合法的 installId 该被拒")
	}
	// 没报到过的设备也能绑：在平板上把手机那台先设好，手机第一次来就是对的
	if _, err := s.Bind("install-cccccc", Default); err != nil {
		t.Fatal(err)
	}
}

func TestSetPrefsMergesAndChecks(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	if _, err := s.SetPrefs(Default, map[string]string{"fontSize": "13"}); err != nil {
		t.Fatal(err)
	}
	// 合并而不是替换：另一台设备改别的键时不该把这一个清掉
	p, err := s.SetPrefs(Default, map[string]string{"kbdFull": "0"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Prefs["fontSize"] != "13" || p.Prefs["kbdFull"] != "0" {
		t.Fatalf("该是合并，拿到 %+v", p.Prefs)
	}
	// kbdFullErr 是**故意留在本机**的那一类（上次全屏为什么失败），同步过去只会误导人
	if _, err := s.SetPrefs(Default, map[string]string{"kbdFullErr": "x"}); err == nil {
		t.Error("白名单外的键该被拒 —— 静默丢的表现是「点了没反应也没报错」")
	}
	if _, err := s.SetPrefs(Default, map[string]string{"fontSize": strings.Repeat("9", 99)}); err == nil {
		t.Error("过长的值该被拒")
	}
	if _, err := s.SetPrefs("nope", map[string]string{"fontSize": "9"}); err == nil {
		t.Error("往不存在的一套上写该报错")
	}
	// 落盘的形状：prefs 挂在那一套里面
	b, _ := os.ReadFile(s.path())
	var f Config
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	if f.Profiles[0].Prefs["fontSize"] != "13" {
		t.Errorf("prefs 没落盘:\n%s", b)
	}
}

// TestPrefsMatchJS 白名单要和前端那份镜像键名**一字不差**。
//
// 为什么值得一个测试：前端那侧是「服务端为准 + localStorage 镜像」，两边同名才能让
// 原来那些直接读 localStorage 的地方（终端回调里有几处）一行都不用改。只在一边加一个
// 键的后果很难查 —— 前端存了，服务端 400（或者反过来，服务端存着但没人读），而界面上
// 看起来就是「这个开关换台设备就忘」。
func TestPrefsMatchJS(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "web", "src", "lib", "prefs.ts"))
	if err != nil {
		t.Skipf("读不到前端那份（源码包里可能没有 web/）: %v", err)
	}
	m := regexp.MustCompile(`(?s)PREF_KEYS = \[(.*?)\]`).FindStringSubmatch(string(b))
	if m == nil {
		t.Fatal("prefs.ts 里找不到 PREF_KEYS")
	}
	// 键名里有数字（sync2026），别把字符集写窄了 —— 这个测试第一次跑就是这么挂的
	ids := regexp.MustCompile(`'([A-Za-z0-9]+)'`).FindAllStringSubmatch(m[1], -1)
	got := make([]string, 0, len(ids))
	for _, id := range ids {
		got = append(got, id[1])
	}
	if strings.Join(got, ",") != strings.Join(Prefs, ",") {
		t.Fatalf("白名单和前端不一致\n go: %v\n js: %v", Prefs, got)
	}
}

func TestCopyPrefs(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	if _, err := s.SetPrefs(Default, map[string]string{"fontSize": "13"}); err != nil {
		t.Fatal(err)
	}
	_, p, err := s.Create("手机", "phone")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CopyPrefs(Default, p.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Load().Get(p.ID)
	if got.Prefs["fontSize"] != "13" {
		t.Fatalf("开关没复制过来: %+v", got.Prefs)
	}
	// 复制出来的是**另一份**：改新那套不该动到源那套
	if _, err := s.SetPrefs(p.ID, map[string]string{"fontSize": "11"}); err != nil {
		t.Fatal(err)
	}
	if src, _ := s.Load().Get(Default); src.Prefs["fontSize"] != "13" {
		t.Errorf("源那一套被带跑了: %+v", src.Prefs)
	}
	if err := s.CopyPrefs("nope", p.ID); err == nil {
		t.Error("源不存在该报错")
	}
}
