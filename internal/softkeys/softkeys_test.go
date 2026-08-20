package softkeys

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustParse(t *testing.T, spec string) string {
	t.Helper()
	v, err := ParseSpec(spec)
	if err != nil {
		t.Fatalf("ParseSpec(%q) 报错: %v", spec, err)
	}
	return v
}

func TestParseSpecCtrl(t *testing.T) {
	// ctrl+ 按终端惯例，不是简单减 96
	for _, c := range []struct{ in, want string }{
		{"ctrl+b", "\x02"}, {"ctrl+c", "\x03"}, {"ctrl+u", "\x15"},
		{"ctrl+B", "\x02"}, {"^b", "\x02"},
		{"ctrl+space", "\x00"}, {"ctrl+[", "\x1b"}, {"ctrl+?", "\x7f"},
	} {
		if got := mustParse(t, c.in); got != c.want {
			t.Errorf("ParseSpec(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseSpecNamedAndMods(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"esc", "\x1b"}, {"tab", "\t"}, {"enter", "\r"},
		{"up", "\x1b[A"}, {"pgdn", "\x1b[6~"}, {"f5", "\x1b[15~"}, {"f1", "\x1bOP"},
		{"shift+tab", "\x1b[Z"},
		{"alt+1", "\x1b1"}, {"alt+g", "\x1bg"}, {"alt+up", "\x1b\x1b[A"},
	} {
		if got := mustParse(t, c.in); got != c.want {
			t.Errorf("ParseSpec(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// 空格分隔 = 连发多下，herdr 的前缀键就靠这个
func TestParseSpecSequences(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"ctrl+b c", "\x02c"},
		{"ctrl+b n", "\x02n"},
		{"ctrl+b X", "\x02X"},    // prefix+shift+x
		{"ctrl+b -", "\x02-"},    // prefix+minus
		{"ctrl+b tab", "\x02\t"}, // prefix+tab
		{"esc esc", "\x1b\x1b"},
		{`"herdr" enter`, "herdr\r"},
		{`"git status" enter`, "git status\r"},
		{`"a b"`, "a b"},
		{"c", "c"}, {"/", "/"},
	} {
		if got := mustParse(t, c.in); got != c.want {
			t.Errorf("ParseSpec(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// 不认识的东西要报错，而不是静默发出去
func TestParseSpecRejects(t *testing.T) {
	for _, in := range []string{"nope", "", "ctrl+ab", string(make([]byte, 300))} {
		if _, err := ParseSpec(in); err == nil {
			t.Errorf("ParseSpec(%q) 应当报错", in)
		}
	}
}

func TestResolveValidation(t *testing.T) {
	bad := [][]Key{
		{{Label: "x"}},                          // 三种形态都没有
		{{Send: "esc", Sticky: "ctrl"}},         // 占了两种
		{{Sticky: "shift"}},                     // sticky 只能 ctrl/alt
		{{Act: "nope"}},                         // act 只支持 kbd
		{{Send: "esc", Label: "xxxxxxxxxxxxx"}}, // 13 个字符，超上限
	}
	for i, keys := range bad {
		if _, err := Resolve(keys); err == nil {
			t.Errorf("第 %d 组应当被拒", i)
		}
	}
	if _, err := Resolve([]Key{{Send: "esc", Label: "xxxxxxxxxxxx"}}); err != nil {
		t.Errorf("12 个字符是上限，应当合法: %v", err)
	}
	// 没写标签就用按键谱兜底
	out, err := Resolve([]Key{{Send: "ctrl+b c"}})
	if err != nil || out[0].Label != "ctrl+b c" {
		t.Errorf("标签兜底失败: %+v %v", out, err)
	}
}

// 出厂配置的字节必须和最早写死在 index.html 里的一致
func TestDefaultsBytes(t *testing.T) {
	out, err := Resolve(Defaults())
	if err != nil {
		t.Fatalf("出厂配置自己解析不过: %v", err)
	}
	byLabel := map[string]string{}
	sticky, kbd := 0, 0
	for _, k := range out {
		if k.Send != "" {
			byLabel[k.Label] = k.Send
		}
		if k.Sticky != "" {
			sticky++
		}
		if k.Act == "kbd" {
			kbd++
		}
	}
	for _, c := range []struct{ label, want string }{
		{"⌃B 前缀", "\x02"}, {"Esc", "\x1b"}, {"Tab", "\t"},
		{"↑", "\x1b[A"}, {"↓", "\x1b[B"}, {"←", "\x1b[D"}, {"→", "\x1b[C"},
		{"PgUp", "\x1b[5~"}, {"PgDn", "\x1b[6~"}, {"⌃C", "\x03"}, {"↵", "\r"},
	} {
		if byLabel[c.label] != c.want {
			t.Errorf("出厂键 %q = %q, want %q", c.label, byLabel[c.label], c.want)
		}
	}
	if sticky != 2 || kbd != 1 {
		t.Errorf("粘滞键应当 2 个、键盘键 1 个，得到 %d/%d", sticky, kbd)
	}
}

// 「常用」下拉里每一条都得能解析，并抽查关键字节
func TestPresetsParse(t *testing.T) {
	n := 0
	byLabel := map[string]string{}
	for _, g := range Presets() {
		for _, it := range g.Items {
			n++
			if _, err := Resolve([]Key{it}); err != nil {
				t.Errorf("预设「%s / %s」解析失败: %v", g.Group, it.Label, err)
			}
			if it.Send != "" {
				byLabel[it.Label] = it.Send
			}
		}
	}
	if n < 20 {
		t.Errorf("预设只有 %d 条，是不是漏了", n)
	}
	for _, c := range []struct{ label, want string }{
		{"放大", "\x02z"}, {"关标签", "\x02X"}, {"横分屏", "\x02-"},
		{"下个 pane", "\x02\t"}, {"敲 herdr", "herdr\r"},
	} {
		if got := mustParse(t, byLabel[c.label]); got != c.want {
			t.Errorf("预设 %q 的字节 = %q, want %q", c.label, got, c.want)
		}
	}
}

// data.go 是从 lib/softkeys.js 生成的，这里逐条比对快照，保证没抄漏抄错。
// JS 版删掉之后这个测试仍然有意义：它锁住了迁移那一刻的行为。
func TestMatchesJSSnapshot(t *testing.T) {
	type jsKey struct {
		Label  string `json:"label"`
		Wide   bool   `json:"wide"`
		Send   string `json:"send"`
		Sticky string `json:"sticky"`
		Act    string `json:"act"`
	}
	var snap struct {
		Defaults []jsKey `json:"defaults"`
		Presets  []struct {
			Group string  `json:"group"`
			Items []jsKey `json:"items"`
		} `json:"presets"`
	}
	b, err := os.ReadFile(filepath.Join("testdata", "js-snapshot.json"))
	if err != nil {
		t.Fatalf("读不到快照: %v", err)
	}
	if err := json.Unmarshal(b, &snap); err != nil {
		t.Fatal(err)
	}

	cmp := func(what string, got Key, want jsKey) {
		if got.Label != want.Label || got.Wide != want.Wide ||
			got.Send != want.Send || got.Sticky != want.Sticky || got.Act != want.Act {
			t.Errorf("%s 不一致\n go %+v\n js %+v", what, got, want)
		}
	}

	def := Defaults()
	if len(def) != len(snap.Defaults) {
		t.Fatalf("出厂配置条数 go=%d js=%d", len(def), len(snap.Defaults))
	}
	for i := range def {
		cmp("出厂配置", def[i], snap.Defaults[i])
	}

	pre := Presets()
	if len(pre) != len(snap.Presets) {
		t.Fatalf("预设组数 go=%d js=%d", len(pre), len(snap.Presets))
	}
	for i := range pre {
		if pre[i].Group != snap.Presets[i].Group {
			t.Errorf("第 %d 组组名 go=%q js=%q", i, pre[i].Group, snap.Presets[i].Group)
		}
		if len(pre[i].Items) != len(snap.Presets[i].Items) {
			t.Fatalf("组 %q 条数 go=%d js=%d", pre[i].Group, len(pre[i].Items), len(snap.Presets[i].Items))
		}
		for j := range pre[i].Items {
			cmp(pre[i].Group, pre[i].Items[j], snap.Presets[i].Items[j])
		}
	}
}

func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Dir: dir}

	// 没有文件时退回出厂配置
	if got, want := len(s.Load()), len(Defaults()); got != want {
		t.Errorf("空目录应当退回出厂 %d 条，得到 %d", want, got)
	}

	saved, err := s.Save([]Key{
		{Label: "放大", Send: "ctrl+b z"},
		{Label: "Ctrl", Sticky: "ctrl"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if saved[0].Send != "\x02z" {
		t.Errorf("保存后返回的字节 = %q", saved[0].Send)
	}
	// 落盘只存用户写的按键谱，不存解析结果
	raw, _ := os.ReadFile(filepath.Join(dir, "softkeys.json"))
	if want := `"send": "ctrl+b z"`; !strings.Contains(string(raw), want) {
		t.Errorf("落盘内容里应当有 %s，实际:\n%s", want, raw)
	}
	back := s.Load()
	if len(back) != 2 || back[0].Send != "\x02z" || back[0].Spec != "ctrl+b z" || back[1].Sticky != "ctrl" {
		t.Errorf("读回来不对: %+v", back)
	}

	// 非法配置不该落盘
	before, _ := os.ReadFile(s.path())
	if _, err := s.Save([]Key{{Label: "x", Send: "乱写"}}); err == nil {
		t.Error("非法按键谱应当被拒")
	}
	after, _ := os.ReadFile(s.path())
	if string(before) != string(after) {
		t.Error("校验失败时不应该动文件")
	}

	// 存坏了要退回出厂而不是崩
	_ = os.WriteFile(s.path(), []byte("{ 这不是 json"), 0o600)
	if got, want := len(s.Load()), len(Defaults()); got != want {
		t.Errorf("坏文件应当退回出厂 %d 条，得到 %d", want, got)
	}
}
