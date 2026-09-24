package softkeys

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestIconsMatchJS 白名单要和前端那份画得出来的**一字不差**（顺序也一样）。
//
// 和 capability 那个测试同一个道理：这两份是同一件事的两半，而只在一边加一个的后果都是
// **静默**的 —— 只在前端加，选择器里挑得到、一存报「图标不认识」；只在这儿加，存得下去
// 但条上画不出来（退回文字标签，用户以为没生效）。
func TestIconsMatchJS(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "web", "src", "keyicons.tsx"))
	if err != nil {
		t.Skipf("读不到前端那份（源码包里可能没有 web/）: %v", err)
	}
	re := regexp.MustCompile(`(?m)^\s*\{ id: '([a-z-]+)'`)
	var got []string
	for _, m := range re.FindAllStringSubmatch(string(b), -1) {
		got = append(got, m[1])
	}
	if strings.Join(got, ",") != strings.Join(KeyIcons, ",") {
		t.Fatalf("图标清单和前端那份不一致\n go: %v\n js: %v", KeyIcons, got)
	}
}

// TestIconReadLenientWriteStrict 图标这一档：读盘丢掉认不出的（退回文字标签），存盘报错。
func TestIconReadLenientWriteStrict(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Dir: dir}

	// 存：不认识的图标该报错（选择器只发白名单里的，走到这儿就是前端有 bug）
	if _, err := s.Save("default", Config{Rows: 1,
		Lib: []Key{{ID: "k1", Label: "A", Send: "a", Icon: "no-such-icon"}},
		Bar: [][]string{{"k1"}}}); err == nil {
		t.Error("不认识的图标该报错")
	}
	// 存：白名单里的收下
	out, err := s.Save("default", Config{Rows: 1,
		Lib: []Key{{ID: "k1", Label: "键盘", Send: "a", Icon: "keyboard"}},
		Bar: [][]string{{"k1"}}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Lib[0].Icon != "keyboard" {
		t.Errorf("图标没存住：%+v", out.Lib[0])
	}
	// 读：从新版本降级回来的文件里有这个版本不认识的 id → 当没挑，**Label 还在**
	body := `{"keys":[{"id":"k1","label":"键盘","send":"a","icon":"from-the-future"}],"bar":[["k1"]]}`
	if err := os.WriteFile(filepath.Join(dir, "softkeys.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got := s.Load("default").Lib[0]
	if got.Icon != "" {
		t.Errorf("认不出的图标该当没挑，拿到 %q", got.Icon)
	}
	if got.Label != "键盘" {
		t.Errorf("名字该一直在（退化成画文字），拿到 %q", got.Label)
	}
}

// TestUnknownActReadDropsOnlyThatKey act 白名单会缩（`pull` 被删过）：读盘时只丢那一个键，
// 其余照用 —— 整份退回出厂的话，人一点保存就把原来那条快捷键条覆盖掉了。存盘照旧报错。
func TestUnknownActReadDropsOnlyThatKey(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Dir: dir}
	body := `{"keys":[{"id":"k1","label":"A","send":"a"},{"id":"k2","label":"拉回","act":"pull"}],"bar":[["k1","k2"]]}`
	if err := os.WriteFile(filepath.Join(dir, "softkeys.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got := s.Load("default")
	if len(got.Lib) != 1 || got.Lib[0].ID != "k1" {
		t.Fatalf("该只剩 k1，拿到 %+v", got.Lib)
	}
	if strings.Join(got.Bar[0], ",") != "k1" {
		t.Errorf("条上对它的引用也该清掉：%v", got.Bar)
	}
	if _, err := s.Save("default", Config{Rows: 1, Lib: []Key{{ID: "k2", Label: "x", Act: "pull"}}, Bar: [][]string{{"k2"}}}); err == nil {
		t.Error("存盘时不认识的 act 该报错")
	}
}
