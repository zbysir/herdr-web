package softkeys

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zbysir/herdr-web/internal/profiles"
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

// text: 前缀发原样文本。编辑器里已经有 sticky: / act:，用户会顺手照着写 text:/new
func TestParseSpecTextPrefix(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"text:/new", "/new"},
		{"text:/new enter", "/new\r"},
		{"TEXT:/new", "/new"},
		{`text:"git status" enter`, "git status\r"}, // 带空格要引号，别在空格处劈开
		{"ctrl+b c text:hi", "\x02chi"},
		{"text:hi text:there", "hithere"},
	} {
		if got := mustParse(t, c.in); got != c.want {
			t.Errorf("ParseSpec(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// 不认识的东西要报错，而不是静默发出去
func TestParseSpecRejects(t *testing.T) {
	for _, in := range []string{"nope", "", "ctrl+ab", "text:", `text:""`, string(make([]byte, 300))} {
		if _, err := ParseSpec(in); err == nil {
			t.Errorf("ParseSpec(%q) 应当报错", in)
		}
	}
}

// 不认识的多字符 token 十有八九是想发文本，错误里得把两种写法都给出来
func TestParseSpecRejectHint(t *testing.T) {
	_, err := ParseSpec("/new")
	if err == nil {
		t.Fatal("/new 应当报错")
	}
	if !strings.Contains(err.Error(), "text:/new") || !strings.Contains(err.Error(), `"/new"`) {
		t.Errorf("错误里应当提示 text:/new 和 \"/new\"，实际: %v", err)
	}
}

func TestResolveValidation(t *testing.T) {
	bad := [][]Key{
		{{Label: "x"}},                          // 三种形态都没有
		{{Send: "esc", Sticky: "ctrl"}},         // 占了两种
		{{Sticky: "shift"}},                     // sticky 只能 ctrl/alt
		{{Act: "nope"}},                         // act 只认白名单
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
// act 是「网页端自己处理」的动作，只认白名单 —— 打错了要当场报错，
// 而不是下发一个点了没反应的键
func TestActWhitelist(t *testing.T) {
	for _, act := range []string{"kbd", "img", "panes", "clip", "paste"} {
		got, err := Resolve([]Key{{Label: "x", Act: act}})
		if err != nil {
			t.Fatalf("act:%s 应当被接受: %v", act, err)
		}
		if got[0].Act != act {
			t.Errorf("act:%s 被改成了 %q", act, got[0].Act)
		}
		if got[0].Send != "" {
			t.Errorf("act:%s 不该带任何字节，得到 %q", act, got[0].Send)
		}
	}
	if _, err := Resolve([]Key{{Label: "x", Act: "nope"}}); err == nil {
		t.Error("不认识的 act 应当报错")
	}
}

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
		{"/usage", "/usage\r"}, {"/compact", "/compact\r"},
	} {
		if got := mustParse(t, byLabel[c.label]); got != c.want {
			t.Errorf("预设 %q 的字节 = %q, want %q", c.label, got, c.want)
		}
	}
}

// 送命键必须预先打上 confirm：软键条上键挨得近，平板误触一下 pane 就没了
func TestPresetsConfirm(t *testing.T) {
	want := map[string]bool{"关 pane": true, "关标签": true, "关工作区": true, "断开": true, "/clear": true}
	seen := map[string]bool{}
	for _, g := range Presets() {
		for _, it := range g.Items {
			if it.Confirm {
				seen[it.Label] = true
			}
			if want[it.Label] && !it.Confirm {
				t.Errorf("预设「%s」应当要二次确认", it.Label)
			}
		}
	}
	for label := range seen {
		if !want[label] {
			t.Errorf("预设「%s」多了个 confirm，是不是勾错行了", label)
		}
	}
	// confirm 要活着穿过解析，别在 normalize 里被吃掉
	out, err := Resolve([]Key{{Label: "关 pane", Send: "ctrl+b x", Confirm: true}})
	if err != nil || !out[0].Confirm {
		t.Errorf("Resolve 丢了 confirm: %+v %v", out, err)
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

	// 只比 JS 版有的那几个字段。confirm 是迁移之后加的，故意不进快照 ——
	// 快照管的是「按键谱有没有抄错」，不是「后来不许改行为」。哪些预设带 confirm
	// 由 TestPresetsConfirm 单独锁。
	cmp := func(what string, got Key, want jsKey) {
		if got.Label != want.Label ||
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

	// 快照只管迁移过来的那几组，必须原样在最前面；之后新加的组允许往后追加。
	pre := Presets()
	if len(pre) < len(snap.Presets) {
		t.Fatalf("迁移过来的组少了 go=%d js=%d", len(pre), len(snap.Presets))
	}
	pre = pre[:len(snap.Presets)]
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

	// 没有文件时退回出厂配置。库里是出厂那些定义 **+ 一个「方向」弹出组**，
	// 而条上只有 DefaultBar 那些（方向键不各占一格）
	if got, want := len(s.Load(profiles.Default).Lib), len(Defaults())+1; got != want {
		t.Errorf("空目录应当退回出厂 %d 条，得到 %d", want, got)
	}
	if c := s.Load(profiles.Default); c.Rows != 1 || len(c.Bar) != 1 || len(c.Bar[0]) != len(DefaultBar()) {
		t.Errorf("出厂应当是一行、条上 %d 个: rows=%d bar=%v", len(DefaultBar()), c.Rows, c.Bar)
	}

	cfg, err := s.Save(profiles.Default, Config{Lib: []Key{
		{Label: "放大", Send: "ctrl+b z"},
		{Label: "Ctrl", Sticky: "ctrl"},
		{Label: "关 pane", Send: "ctrl+b x", Confirm: true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	saved := cfg.Lib
	if saved[0].Send != "\x02z" {
		t.Errorf("保存后返回的字节 = %q", saved[0].Send)
	}
	// ID 是存盘时补的，前端不用管
	if saved[0].ID == "" || saved[0].ID == saved[1].ID {
		t.Errorf("ID 没补上 / 撞了: %+v", saved)
	}
	// 落盘只存用户写的按键谱，不存解析结果
	raw, _ := os.ReadFile(filepath.Join(dir, "softkeys.json"))
	if want := `"send": "ctrl+b z"`; !strings.Contains(string(raw), want) {
		t.Errorf("落盘内容里应当有 %s，实际:\n%s", want, raw)
	}
	if !saved[2].Confirm {
		t.Error("保存后返回的 confirm 丢了")
	}
	back := s.Load(profiles.Default).Lib
	if len(back) != 3 || back[0].Send != "\x02z" || back[0].Spec != "ctrl+b z" || back[1].Sticky != "ctrl" {
		t.Errorf("读回来不对: %+v", back)
	}
	// confirm 要落盘，不然重开一次防误触就没了
	if !strings.Contains(string(raw), `"confirm": true`) || !back[2].Confirm {
		t.Errorf("confirm 没落盘 / 没读回来:\n%s\n%+v", raw, back[2])
	}

	/* ---------------------------------------------------- 条上的引用 */

	// 同一个键**能在两行里各放一个**：条上存的是引用，不是定义
	two, err := s.Save(profiles.Default, Config{Rows: 2, Lib: []Key{
		{ID: "a", Label: "⌨", Act: "kbd"},
		{ID: "b", Label: "Esc", Send: "esc"},
	}, Bar: [][]string{{"a", "b"}, {"b"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(two.Bar) != 2 || len(two.Bar[0]) != 2 || len(two.Bar[1]) != 1 || two.Bar[1][0] != "b" {
		t.Errorf("重复引用没存住: %v", two.Bar)
	}
	if c := s.Load(profiles.Default); len(c.Bar) != 2 || c.Bar[1][0] != "b" || len(c.Lib) != 2 {
		t.Errorf("引用读回来不对: %+v", c)
	}

	// 引用了不存在的键要报错，不能静默丢（丢了就是「保存完少了个键」）
	if _, err := s.Save(profiles.Default, Config{Rows: 1, Lib: []Key{{ID: "a", Label: "x", Send: "esc"}}, Bar: [][]string{{"nope"}}}); err == nil {
		t.Error("引用不存在的 ID 应当被拒")
	}
	if _, err := s.Save(profiles.Default, Config{Rows: 3, Lib: []Key{{Label: "x", Send: "esc"}}}); err == nil {
		t.Error("rows=3 应当被拒")
	}
	// rows=1 时第二行的引用要接到第一行末尾，不能留着「存着但不显示」
	one, err := s.Save(profiles.Default, Config{Rows: 1, Lib: []Key{
		{ID: "a", Label: "a", Send: "esc"},
		{ID: "b", Label: "b", Send: "tab"},
	}, Bar: [][]string{{"a"}, {"b"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(one.Bar) != 1 || len(one.Bar[0]) != 2 || one.Bar[0][1] != "b" {
		t.Errorf("rows=1 时第二行应当接到第一行末尾: %v", one.Bar)
	}
	// 库里留着、条上没引用 = 「我的按键」里有但没上条，完全合法
	off, err := s.Save(profiles.Default, Config{Rows: 1, Lib: []Key{
		{ID: "a", Label: "上条的", Send: "esc"},
		{ID: "b", Label: "没上条", Send: "tab"},
	}, Bar: [][]string{{"a"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(off.Lib) != 2 || len(off.Bar[0]) != 1 {
		t.Errorf("没上条的键应当留在库里: %+v", off)
	}

	// **读出来原样存回去**必须能过：编辑器就是这么干的（两个字段都回传，Send 是
	// 上次解析出的字节）。拿 Send 当谱重解一次的话 Tab 的 "\t" 会变成「谱是空的」
	if _, err := s.Save(profiles.Default, DefaultConfig()); err != nil {
		t.Fatal(err)
	}
	round := s.Load(profiles.Default)
	if _, err := s.Save(profiles.Default, round); err != nil {
		t.Fatalf("读出来原样存回去应当能过: %v", err)
	}
	if back := s.Load(profiles.Default); len(back.Lib) != len(round.Lib) || back.Lib[5].Spec != round.Lib[5].Spec {
		t.Errorf("原样存回去之后变了: %+v vs %+v", back.Lib[5], round.Lib[5])
	}

	// 老文件（row / off 长在按键上、没有 bar）要能迁过来
	oldFile := `{"keys":[{"label":"⌨","act":"kbd"},{"label":"Esc","send":"esc","row":2},{"label":"库里","send":"tab","off":true}],"rows":2}`
	_ = os.WriteFile(s.path(), []byte(oldFile), 0o600)
	mig := s.Load(profiles.Default)
	if len(mig.Lib) != 3 || len(mig.Bar) != 2 || len(mig.Bar[0]) != 1 || len(mig.Bar[1]) != 1 {
		t.Errorf("老文件没迁对: %+v", mig)
	}
	if mig.Bar[1][0] != mig.Lib[1].ID {
		t.Errorf("老文件的第二行没指到 Esc: %+v", mig)
	}

	// 非法配置不该落盘
	_, _ = s.Save(profiles.Default, Config{Rows: 1, Lib: []Key{{Label: "ok", Send: "esc"}}})
	before, _ := os.ReadFile(s.path())
	if _, err := s.Save(profiles.Default, Config{Lib: []Key{{Label: "x", Send: "乱写"}}}); err == nil {
		t.Error("非法按键谱应当被拒")
	}
	after, _ := os.ReadFile(s.path())
	if string(before) != string(after) {
		t.Error("校验失败时不应该动文件")
	}

	// 存坏了要退回出厂而不是崩
	_ = os.WriteFile(s.path(), []byte("{ 这不是 json"), 0o600)
	if got, want := len(s.Load(profiles.Default).Lib), len(Defaults())+1; got != want {
		t.Errorf("坏文件应当退回出厂 %d 条，得到 %d", want, got)
	}
}

// TestProfilesSplit 分 profile 之后的那几条：定义全局、排布各一份、老文件照旧。
func TestProfilesSplit(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Dir: dir}

	// 老文件的样子：顶层 rows/keys/bar，没有 profiles 那一层
	old := `{"rows":1,"keys":[{"id":"k1","label":"Esc","send":"esc"},{"id":"k2","label":"Tab","send":"tab"}],
	         "bar":[["k1","k2"]]}`
	if err := os.WriteFile(filepath.Join(dir, "softkeys.json"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	// 升级之后默认那一套一点没变
	if c := s.Load(profiles.Default); len(c.Bar[0]) != 2 || c.Bar[0][0] != "k1" {
		t.Fatalf("老文件的默认那一套该原样读出来: %+v", c.Bar)
	}
	// 还没排过的那一套：退到默认那一套（不是一条空栏）
	if c := s.Load("p2"); len(c.Bar[0]) != 2 {
		t.Fatalf("没排过的一套该退到默认那一份: %+v", c.Bar)
	}

	/* ---------------------------------------------- 各存一份，互不影响 */

	// 在 p2 上只留一个键（模拟手机那套）
	if _, err := s.Save("p2", Config{Rows: 1, Lib: []Key{
		{ID: "k1", Label: "Esc", Send: "esc"},
		{ID: "k2", Label: "Tab", Send: "tab"},
	}, Bar: [][]string{{"k1"}}}); err != nil {
		t.Fatal(err)
	}
	if c := s.Load("p2"); len(c.Bar[0]) != 1 {
		t.Errorf("p2 该只有一个键: %+v", c.Bar)
	}
	// **默认那一套不能被带走**：老文件的顶层字段要在写盘时收敛进 profiles，不是被挤掉
	if c := s.Load(profiles.Default); len(c.Bar[0]) != 2 {
		t.Fatalf("存 p2 不该动默认那一套: %+v", c.Bar)
	}
	// 顶层老字段是默认那一套的镜像（降级回老版本读的是它）
	b, _ := os.ReadFile(filepath.Join(dir, "softkeys.json"))
	var f file
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Bar) != 1 || len(f.Bar[0]) != 2 {
		t.Errorf("顶层该镜像默认那一套（降级用）:\n%s", b)
	}
	if len(f.Profiles) != 2 {
		t.Errorf("两套都该在 profiles 里:\n%s", b)
	}

	/* ---------------------------------------------- 定义是全局的 */

	// 在 p2 上删掉 k2 这个定义 → 默认那一套条上那个引用也得一起清掉，
	// 不然默认那套下次读出来少一个键，而且是静默的
	if _, err := s.Save("p2", Config{Rows: 1, Lib: []Key{
		{ID: "k1", Label: "Esc", Send: "esc"},
	}, Bar: [][]string{{"k1"}}}); err != nil {
		t.Fatal(err)
	}
	if c := s.Load(profiles.Default); len(c.Bar[0]) != 1 || c.Bar[0][0] != "k1" {
		t.Errorf("删掉的定义该从所有 profile 的条上清掉: %+v", c.Bar)
	}

	/* ---------------------------------------------- 读得宽松 */

	// 手改文件让 p2 引用一个不存在的 id：丢那一个引用，不是整份退回出厂
	bad := `{"keys":[{"id":"k1","label":"Esc","send":"esc"}],
	         "profiles":{"default":{"rows":1,"bar":[["k1"]]},"p2":{"rows":1,"bar":[["k1","gone"]]}}}`
	if err := os.WriteFile(filepath.Join(dir, "softkeys.json"), []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	if c := s.Load("p2"); len(c.Bar[0]) != 1 || c.Bar[0][0] != "k1" {
		t.Errorf("认不出的引用该丢掉、别的照用: %+v", c.Bar)
	}
	// 存的时候还是严格的
	if _, err := s.Save("p2", Config{Rows: 1, Lib: []Key{{ID: "k1", Label: "Esc", Send: "esc"}},
		Bar: [][]string{{"gone"}}}); err == nil {
		t.Error("存的时候引用不存在的定义该报错")
	}
}

func TestResetCopyDrop(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Dir: dir}

	// 自己攒一套：一个自定义键 + 出厂里也有的 Esc
	if _, err := s.Save(profiles.Default, Config{Rows: 1, Lib: []Key{
		{ID: "k1", Label: "我的", Send: "ctrl+b z"},
		{ID: "k2", Label: "Esc", Send: "esc"},
	}, Bar: [][]string{{"k1"}}}); err != nil {
		t.Fatal(err)
	}
	// 复制给 p2
	if err := s.Copy(profiles.Default, "p2"); err != nil {
		t.Fatal(err)
	}
	if c := s.Load("p2"); len(c.Bar[0]) != 1 || c.Bar[0][0] != "k1" {
		t.Fatalf("复制过来的排布不对: %+v", c.Bar)
	}

	// 「这一套恢复默认」：出厂那一排回到 p2 的条上，
	// 但**自己那个定义还在库里**，默认那一套也一点不动
	c, err := s.Reset("p2")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Bar[0]) != len(DefaultBar()) {
		t.Errorf("恢复默认该把出厂那一排放上条: %+v", c.Bar)
	}
	found := false
	for _, k := range c.Lib {
		if k.ID == "k1" {
			found = true
		}
	}
	if !found {
		t.Error("恢复默认不该删掉自己的定义（定义是全局的，会连带打到别的 profile）")
	}
	// 出厂里本来就有的 Esc 该复用现成那个定义，不是又加一条重的
	esc := 0
	for _, k := range c.Lib {
		if k.Label == "Esc" {
			esc++
		}
	}
	if esc != 1 {
		t.Errorf("出厂键该按「名字 + 干什么」复用现成的定义，Esc 有 %d 条", esc)
	}
	if d := s.Load(profiles.Default); len(d.Bar[0]) != 1 || d.Bar[0][0] != "k1" {
		t.Errorf("恢复 p2 不该动默认那一套: %+v", d.Bar)
	}

	// 删掉 p2 那一段之后，读它退回默认那一套
	if err := s.Drop("p2"); err != nil {
		t.Fatal(err)
	}
	if c := s.Load("p2"); len(c.Bar[0]) != 1 || c.Bar[0][0] != "k1" {
		t.Errorf("删掉那一段之后该退到默认: %+v", c.Bar)
	}
	if err := s.Drop(profiles.Default); err == nil {
		t.Error("默认那一段删不掉")
	}
}

// TestResetRefusesWhenLibFull 「恢复默认」补不进键时要**报错**，不能静默退回出厂 ——
// 那样会把用户满库的定义连着别的 profile 的条一起抹掉（定义是全局的）。
func TestResetRefusesWhenLibFull(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	lib := make([]Key, 0, MaxKeys)
	bar := make([]string, 0, MaxKeys)
	for i := 0; i < MaxKeys; i++ {
		id := fmt.Sprintf("x%d", i)
		lib = append(lib, Key{ID: id, Label: fmt.Sprintf("k%d", i), Send: "esc"})
		bar = append(bar, id)
	}
	if _, err := s.Save(profiles.Default, Config{Rows: 1, Lib: lib, Bar: [][]string{bar[:MaxBar]}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reset(profiles.Default); err == nil {
		t.Fatal("库满了还能「恢复默认」= 那 120 个定义被静默抹掉")
	}
	// 报错之后原来那份必须一个字没动
	if c := s.Load(profiles.Default); len(c.Lib) != MaxKeys {
		t.Fatalf("报错之后库被改了：%d 个", len(c.Lib))
	}
}

/* ---------------------------------------------------------------- 宽度（span） */

/* ------------------------------------------------------------ 固定块（Pad） */

/* ------------------------------------------------------------ 弹出组（Group） */

// TestGroupRoundTrip 弹出组：占一格、点开是一片网格。存得进、读得回。
func TestGroupRoundTrip(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	lib := []Key{
		{ID: "k1", Label: "↑", Send: "up"}, {ID: "k2", Label: "↓", Send: "down"},
		{ID: "k3", Label: "←", Send: "left"}, {ID: "k4", Label: "→", Send: "right"},
		// 条上只占一格的那个键
		{ID: "g1", Label: "方向", Group: &Group{Cols: 3, Cells: []string{"", "k1", "", "k3", "k2", "k4"}}},
	}
	out, err := s.Save(profiles.Default, Config{Rows: 1, Lib: lib, Bar: [][]string{{"g1"}}})
	if err != nil {
		t.Fatal(err)
	}
	g := out.Lib[4].Group
	if g == nil || g.Cols != 3 {
		t.Fatalf("组没存住：%+v", out.Lib[4])
	}
	// 格子补齐到 Cols*MaxGroupRows，前六格按行原样
	if n := len(g.Cells); n != 3*MaxGroupRows {
		t.Errorf("格子该补齐到 %d，拿到 %d", 3*MaxGroupRows, n)
	}
	if strings.Join(g.Cells[:6], ",") != ",k1,,k3,k2,k4" {
		t.Errorf("格子顺序该原样（按行读）：%q", strings.Join(g.Cells[:6], ","))
	}
	// 条上只占一格
	if strings.Join(out.Bar[0], ",") != "g1" {
		t.Errorf("条上该只有那一个组键：%v", out.Bar[0])
	}
	if got := s.Load(profiles.Default).Lib[4].Group; got == nil || strings.Join(got.Cells[:6], ",") != ",k1,,k3,k2,k4" {
		t.Errorf("读回来的组不对：%+v", got)
	}
}

// TestGroupIsItsOwnKind group 和 send / sticky / act 是**四选一**，而且必须有名字。
//
// 名字是硬要求：组键在条上只占一格，格子上那个名字是它唯一的说明。别的形态能从按键谱 / act
// 兜出一个名字，这个兜不出来 —— 空着就是一个看不出是什么的方块。
func TestGroupIsItsOwnKind(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	g := &Group{Cols: 2, Cells: []string{"k1"}}
	lib := func(k Key) []Key { return []Key{{ID: "k1", Label: "↑", Send: "up"}, k} }

	// 又是 group 又是 send
	if _, err := s.Save(profiles.Default, Config{Rows: 1,
		Lib: lib(Key{ID: "g1", Label: "两种", Send: "esc", Group: g}), Bar: [][]string{{"k1"}}}); err == nil {
		t.Error("group + send 该报错（四选一）")
	}
	// 没名字
	if _, err := s.Save(profiles.Default, Config{Rows: 1,
		Lib: lib(Key{ID: "g1", Group: g}), Bar: [][]string{{"k1"}}}); err == nil {
		t.Error("组键没名字该报错")
	}
	// 列数越界
	if _, err := s.Save(profiles.Default, Config{Rows: 1,
		Lib: lib(Key{ID: "g1", Label: "宽", Group: &Group{Cols: MaxGroupCols + 1, Cells: []string{"k1"}}}),
		Bar: [][]string{{"k1"}}}); err == nil {
		t.Error("列数超上限该报错")
	}
}

// TestGroupCannotNest 组里不能再放组 —— 一层就够，嵌套只会变成「点开还要再点开」。
func TestGroupCannotNest(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Dir: dir}
	inner := Key{ID: "g1", Label: "里", Group: &Group{Cols: 1, Cells: []string{"k1"}}}
	outer := Key{ID: "g2", Label: "外", Group: &Group{Cols: 1, Cells: []string{"g1"}}}
	lib := []Key{{ID: "k1", Label: "↑", Send: "up"}, inner, outer}

	if _, err := s.Save(profiles.Default, Config{Rows: 1, Lib: lib, Bar: [][]string{{"k1"}}}); err == nil {
		t.Fatal("组里放组该报错")
	}
	// 读盘那侧**丢掉**那一格而不是整份作废（手改过文件 / 从新版本降级回来）
	body := `{"keys":[
		{"id":"k1","label":"↑","send":"up"},
		{"id":"g1","label":"里","group":{"cols":1,"cells":["k1"]}},
		{"id":"g2","label":"外","group":{"cols":1,"cells":["g1"]}}
	],"bar":[["k1"]]}`
	if err := os.WriteFile(filepath.Join(dir, "softkeys.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c := s.Load(profiles.Default)
	if len(c.Lib) != 3 {
		t.Fatalf("整份不该作废：%+v", c.Lib)
	}
	for _, cell := range c.Lib[2].Group.Cells {
		if cell == "g1" {
			t.Error("嵌套那一格该被丢掉")
		}
	}
}

// TestDefaultArrowsAreAGroup 出厂条上**方向键只占一格**：它们是「方向」那个弹出组的成员。
//
// 摊开是 3×2 六格，393px 的手机竖屏上那是半条屏幕 —— 这条退回去（四个键各占一格）的表现
// 不是报错，是「新装一台机器，条上一半地方被方向键吃了」。
func TestDefaultArrowsAreAGroup(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	c := s.Load(profiles.Default) // 空目录 → 出厂

	byID := map[string]Key{}
	for _, k := range c.Lib {
		byID[k.ID] = k
	}
	// 条上不该有单独的方向键，而该有一个组
	groups := 0
	for _, id := range c.Bar[0] {
		k := byID[id]
		if isDefaultArrow(k) {
			t.Errorf("条上还有单独的方向键：%q", k.Label)
		}
		if k.Group != nil {
			groups++
		}
	}
	if groups != 1 {
		t.Fatalf("条上该正好有一个弹出组，数到 %d", groups)
	}
	// 那个组里就是那四个方向键，摆成十字
	var g Key
	for _, k := range c.Lib {
		if k.Group != nil {
			g = k
		}
	}
	if g.Label != "方向" || g.Group.Cols != 3 {
		t.Fatalf("组不对：%+v", g)
	}
	want := []string{"", "↑", "", "←", "↓", "→"}
	for i, w := range want {
		got := ""
		if id := g.Group.Cells[i]; id != "" {
			got = byID[id].Label
		}
		if got != w {
			t.Errorf("第 %d 格该是 %q，是 %q", i+1, w, got)
		}
	}
	// 四个方向键的**定义**照旧在库里（组里放的是引用）
	for _, want := range []string{"↑", "↓", "←", "→"} {
		found := false
		for _, k := range c.Lib {
			if k.Label == want {
				found = true
			}
		}
		if !found {
			t.Errorf("定义 %q 该还在库里", want)
		}
	}
}

// TestResetKeepsMyArrowGroup 用户把自己那个「方向」组改成 4 列之后，「恢复默认」不该
// 又补一个同名的进去（sigOf 认组时**不带列数**，就是为了这个）。
func TestResetKeepsMyArrowGroup(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	c := s.Load(profiles.Default)
	lib := append([]Key{}, c.Lib...)
	n := 0
	for i := range lib {
		if lib[i].Group != nil {
			lib[i].Group = &Group{Cols: 4, Cells: make([]string, 4*MaxGroupRows)}
			lib[i].Group.Cells[0] = lib[0].ID
			n++
		}
	}
	if n != 1 {
		t.Fatalf("出厂该只有一个组，数到 %d", n)
	}
	if _, err := s.Save(profiles.Default, Config{Rows: 1, Lib: lib, Bar: c.Bar}); err != nil {
		t.Fatal(err)
	}
	out, err := s.Reset(profiles.Default)
	if err != nil {
		t.Fatal(err)
	}
	groups := 0
	for _, k := range out.Lib {
		if k.Group != nil {
			groups++
			if k.Group.Cols != 4 {
				t.Errorf("我改过的列数该留着，拿到 %d 列", k.Group.Cols)
			}
		}
	}
	if groups != 1 {
		t.Errorf("恢复默认不该再补一个同名的组，现在有 %d 个", groups)
	}
}

/* ------------------------------------------------------------ 钉住（Pin） */

// TestPinRoundTrip 钉住存得进、读得回，而且 **Bar 里是那一行的完整顺序**。
//
// 最后那条是这个设计的全部好处：只认 Bar 的老版本读出来是同样那些键（只是全都跟着滑），
// 不会「钉住的那几个不见了」。
func TestPinRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Dir: dir}
	lib := []Key{
		{ID: "k1", Label: "⌨", Act: "kbd"}, {ID: "k2", Label: "Esc", Send: "esc"},
		{ID: "k3", Label: "Tab", Send: "tab"}, {ID: "k4", Label: "↵", Send: "enter"},
	}
	// 一行四个：头一个钉左、末一个钉右，中间两个跟着滑
	out, err := s.Save(profiles.Default, Config{Rows: 1, Lib: lib,
		Bar: [][]string{{"k1", "k2", "k3", "k4"}}, Pin: []Pin{{Left: 1, Right: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Pin) != 1 || out.Pin[0].Left != 1 || out.Pin[0].Right != 1 {
		t.Fatalf("钉住没存住：%+v", out.Pin)
	}
	// Bar 是完整顺序（降级的老版本读到的就是这个）
	if strings.Join(out.Bar[0], ",") != "k1,k2,k3,k4" {
		t.Errorf("Bar 该是那一行的完整顺序：%v", out.Bar[0])
	}
	got := s.Load(profiles.Default)
	if len(got.Pin) != 1 || got.Pin[0].Left != 1 || got.Pin[0].Right != 1 {
		t.Errorf("读回来的钉住不对：%+v", got.Pin)
	}
	// 落盘那份里 bar 也是完整的
	b, _ := os.ReadFile(filepath.Join(dir, "softkeys.json"))
	if !strings.Contains(string(b), `"k1"`) || !strings.Contains(string(b), `"k4"`) {
		t.Errorf("落盘的 bar 该是完整那一行：%s", b)
	}
}

// TestPinClamped 个数**一律夹住不报错**：Left+Right 不能超过那一行的长度。
//
// 为什么不报错：prune 会让行变短（别的设备删了个定义），那时候存进来的个数就越界了 ——
// 而那不是谁的 bug，报错只会让人存不下去。
func TestPinClamped(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	lib := []Key{{ID: "k1", Label: "A", Send: "a"}, {ID: "k2", Label: "B", Send: "b"}}

	out, err := s.Save(profiles.Default, Config{Rows: 1, Lib: lib,
		Bar: [][]string{{"k1", "k2"}}, Pin: []Pin{{Left: 5, Right: 5}}})
	if err != nil {
		t.Fatalf("越界该夹住而不是报错：%v", err)
	}
	// 左边优先（钉住的第一个多半是「呼键盘」那种最要紧的），两个位置全给左边
	if out.Pin[0].Left != 2 || out.Pin[0].Right != 0 {
		t.Errorf("该夹成 left=2 right=0，拿到 %+v", out.Pin[0])
	}
	// 负数当 0
	out, err = s.Save(profiles.Default, Config{Rows: 1, Lib: lib,
		Bar: [][]string{{"k1", "k2"}}, Pin: []Pin{{Left: -3, Right: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Pin[0].Left != 0 || out.Pin[0].Right != 1 {
		t.Errorf("负数该当 0，拿到 %+v", out.Pin[0])
	}
	// 一个都没钉就不落这个字段
	out, err = s.Save(profiles.Default, Config{Rows: 1, Lib: lib,
		Bar: [][]string{{"k1", "k2"}}, Pin: []Pin{{}}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Pin != nil {
		t.Errorf("一个都没钉不该落这个字段：%+v", out.Pin)
	}
}

// TestPinIsPerProfile 钉住是**排布**，所以每套一份。
func TestPinIsPerProfile(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	lib := []Key{{ID: "k1", Label: "A", Send: "a"}, {ID: "k2", Label: "B", Send: "b"}}
	if _, err := s.Save(profiles.Default, Config{Rows: 1, Lib: lib,
		Bar: [][]string{{"k1", "k2"}}, Pin: []Pin{{Left: 1}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save("p2", Config{Rows: 1, Lib: lib, Bar: [][]string{{"k1", "k2"}}}); err != nil {
		t.Fatal(err)
	}
	if got := s.Load("p2").Pin; got != nil {
		t.Errorf("p2 上不该有钉住：%+v", got)
	}
	if got := s.Load(profiles.Default).Pin; got == nil || got[0].Left != 1 {
		t.Errorf("默认那一套的钉住该原样在：%+v", got)
	}
}

// TestPruneShrinksRowThenPinClamps 别的设备删掉一个定义 → 这一行变短 → 钉住的个数跟着夹。
//
// 这条是「存个数」这个选择的代价，也是它必须在**读**的时候夹住的理由：不夹的话下次
// 存盘就越界，而用户什么都没改。
func TestPruneShrinksRowThenPinClamps(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	lib := []Key{
		{ID: "k1", Label: "A", Send: "a"}, {ID: "k2", Label: "B", Send: "b"},
		{ID: "k3", Label: "C", Send: "c"},
	}
	if _, err := s.Save(profiles.Default, Config{Rows: 1, Lib: lib,
		Bar: [][]string{{"k1", "k2", "k3"}}, Pin: []Pin{{Left: 2, Right: 1}}}); err != nil {
		t.Fatal(err)
	}
	// 在另一套上把两个定义删掉（定义全局 → 默认那一套的条跟着变短）
	if _, err := s.Save("p2", Config{Rows: 1, Lib: lib[:1], Bar: [][]string{{"k1"}}}); err != nil {
		t.Fatal(err)
	}
	c := s.Load(profiles.Default)
	if len(c.Bar[0]) != 1 {
		t.Fatalf("那一行该只剩一个：%v", c.Bar[0])
	}
	if l, r := c.Pin[0].Left, c.Pin[0].Right; l+r > 1 {
		t.Errorf("钉住的个数该夹到行长以内，拿到 left=%d right=%d", l, r)
	}
	// 夹干净之后该能原样存回去
	if _, err := s.Save(profiles.Default, c); err != nil {
		t.Errorf("夹干净之后该存得回去：%v", err)
	}
}
