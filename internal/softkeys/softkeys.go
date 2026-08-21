// Package softkeys 管软键条的配置：存在 ~/.herdr-web/softkeys.json，在网页上编辑。
//
// 存服务端而不是浏览器 localStorage，是为了手机 / 平板 / 电脑共用一份 —— 和 token
// 落盘同一个道理，改一次到处生效。
//
// 配置分成两半，因为「这个键是什么」和「它排在哪」是两件事：
//
//	Lib  「我的按键」：所有按键的定义（可改名字 / 按键谱 / 宽 / 两下），每个有一个 ID
//	Bar   软键条：每行一串 **ID**，指向 Lib 里的定义
//
// Bar 存 ID 而不是整份定义，为的是「栏里的键是从我的按键里**选**出来的」：
//
//   - 同一个键**能在两行里各放一个**（Esc 放第一行也放第二行，完全合法）；
//   - 改一处定义，条上所有引用一起变；
//   - 从条上拿下来只是去掉一个引用，定义还在「我的按键」里，随时再拖上去。
//
// 每个按键三种形态之一：
//
//	{send: "<按键谱>"}  按一下发一串字节
//	{sticky: "ctrl"}    粘滞修饰键（点亮之后下一个字母组合成 ctrl+x）
//	{act: "kbd"}        显示 / 收起系统键盘
//	{act: "img"}        传图（相机 / 相册 / 剪贴板）
//
// 另外每个按键可以打上 {confirm: true}：第一下只是「举起来」，第二下才真发出去。
// 给关 pane / 关标签 / 断开这种误触代价很大的键用。
//
// 软键条**几行是个设置**（Config.Rows，1 或 2），不靠「第二行空不空」猜 —— 空的第二行
// 和「我只要一行」是两件事。两行各自横向滚动：手机上一行只放得下四五个键，横滑找键比多
// 占一行终端便宜，但「最常用的几个」和「次常用的几个」分两行、各滑各的，比十几个键排成
// 一条长龙好找。
//
// 「按键谱」是空格分隔的记号，服务端解析成字节后下发给前端，前端只管照发。
// 支持多个 token 连发，所以一个按键可以是一串操作 —— `ctrl+b c` 就是 herdr 的
// 「前缀 + c」，一下点出来。原样文本两种写法都行：`"/new" enter` 和
// `text:/new enter`。
package softkeys

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

// MaxKeys 「我的按键」最多几个定义。比软键条上能放的多得多是有意的：定义只占一行
// JSON，屏幕上不占地方 —— 「载入预设」一下就会灌进来六十多个，卡在 40 上没法用。
const MaxKeys = 120

// MaxRows 软键条最多几行。两行是**屏幕**定的上限，不是实现限制：手机竖屏上第三行
// 就该拿去当终端了（一行 28px ≈ 两行终端）。真要更多键就横滑，别往下堆。
const MaxRows = 2

// MaxBar 一行最多引用多少个键。允许重复之后这个数得有个头，不然一次误操作就能塞进
// 几千个引用。
const MaxBar = 40

// Key 是一个按键的定义。Spec/Send 只在 send 形态下有值。
type Key struct {
	ID      string `json:"id,omitempty"` // 稳定标识，软键条按这个引用（存盘时补齐）
	Label   string `json:"label"`
	Wide    bool   `json:"wide,omitempty"`
	Confirm bool   `json:"confirm,omitempty"` // 要点两下才发（防误触）
	Send    string `json:"send,omitempty"`    // 解析出来的字节（下发给前端）
	Spec    string `json:"spec,omitempty"`    // 用户写的按键谱（回显到编辑器）
	Sticky  string `json:"sticky,omitempty"`  // ctrl | alt
	Act     string `json:"act,omitempty"`     // kbd | img（网页端自己处理，不发字节）
}

// stored 是落盘的形状：只存用户写的东西，不存解析结果。
type stored struct {
	ID      string `json:"id,omitempty"`
	Label   string `json:"label"`
	Wide    bool   `json:"wide,omitempty"`
	Confirm bool   `json:"confirm,omitempty"`
	Send    string `json:"send,omitempty"`
	Sticky  string `json:"sticky,omitempty"`
	Act     string `json:"act,omitempty"`

	// 老字段，只为**读老文件**留着（新版本不再写）：那时候「排在第几行」是长在
	// 按键上的，没有 Bar 这一层。见 migrate()。
	Row int  `json:"row,omitempty"`
	Off bool `json:"off,omitempty"`
}

type file struct {
	// Rows 软键条几行（1 / 2）。0 = 老文件没这个字段，按一行算
	Rows int      `json:"rows,omitempty"`
	Keys []stored `json:"keys"`
	// Bar 每行一串 ID。nil = 老文件（没有这一层），按 Keys 里的 row/off 迁移
	Bar [][]string `json:"bar,omitempty"`
}

// Config 是一整份软键条配置。
type Config struct {
	Rows int        `json:"rows"`
	Lib  []Key      `json:"lib"` // 我的按键：所有定义
	Bar  [][]string `json:"bar"` // 每行一串 Lib 里的 ID（允许重复）
}

// PresetGroup 是编辑器「常用」下拉里的一组。
type PresetGroup struct {
	Group string `json:"group"`
	Items []Key  `json:"items"`
}

// named 具名键 → 字节。方向键用 CSI 形式。
var named = map[string]string{
	"esc": "\x1b", "tab": "\t", "enter": "\r", "cr": "\r", "lf": "\n", "space": " ",
	"bs": "\x7f", "backspace": "\x7f", "del": "\x1b[3~", "delete": "\x1b[3~", "ins": "\x1b[2~",
	"up": "\x1b[A", "down": "\x1b[B", "right": "\x1b[C", "left": "\x1b[D",
	"home": "\x1b[H", "end": "\x1b[F", "pgup": "\x1b[5~", "pgdn": "\x1b[6~",
	"f1": "\x1bOP", "f2": "\x1bOQ", "f3": "\x1bOR", "f4": "\x1bOS",
	"f5": "\x1b[15~", "f6": "\x1b[17~", "f7": "\x1b[18~", "f8": "\x1b[19~",
	"f9": "\x1b[20~", "f10": "\x1b[21~", "f11": "\x1b[23~", "f12": "\x1b[24~",
}

// ctrlOf ctrl+<单字符> → 控制码。按终端惯例，不是简单减 96。
func ctrlOf(ch string) (string, error) {
	c := strings.ToLower(ch)
	if len(c) == 1 {
		b := c[0]
		switch {
		case b >= 'a' && b <= 'z':
			return string(rune(b - 96)), nil
		case b == ' ' || b == '@':
			return "\x00", nil
		case strings.ContainsRune("[\\]^_", rune(b)):
			return string(rune(b - 64)), nil
		case b == '?':
			return "\x7f", nil
		}
	}
	return "", fmt.Errorf("ctrl+ 不支持 %s", ch)
}

func token(t string) (string, error) {
	low := strings.ToLower(t)
	// text:xxx 原样发送后面这一串，和双引号等价。多这一种写法是因为编辑器里
	// 已经有 sticky: / act: 前缀，手输 text:/new 比去找引号顺手（尤其平板）。
	// 带空格的还是得写 text:"a b" 或 "a b"。
	if strings.HasPrefix(low, "text:") {
		lit := strings.Trim(t[5:], `"`)
		if lit == "" {
			return "", fmt.Errorf("text: 后面是空的：%s", t)
		}
		return lit, nil
	}
	if strings.HasPrefix(low, "ctrl+") || strings.HasPrefix(low, "^") {
		rest := t[1:]
		if strings.HasPrefix(low, "ctrl+") {
			rest = t[5:]
		}
		// 允许 ctrl+space：具名键先解出来，只要落到单个字符就能再套 ctrl
		base := rest
		if utf8.RuneCountInString(rest) != 1 {
			base = named[strings.ToLower(rest)]
		}
		if base == "" || utf8.RuneCountInString(base) != 1 {
			return "", fmt.Errorf("ctrl+ 后面只能跟一个字符（或 space）：%s", t)
		}
		return ctrlOf(base)
	}
	if strings.HasPrefix(low, "alt+") {
		rest, err := token(t[4:])
		if err != nil {
			return "", err
		}
		return "\x1b" + rest, nil
	}
	if low == "shift+tab" {
		return "\x1b[Z", nil
	}
	if v, ok := named[low]; ok {
		return v, nil
	}
	if utf8.RuneCountInString(t) == 1 {
		return t, nil // 单个字面字符
	}
	return "", fmt.Errorf("不认识的按键：%s（要发这串原样文本就写 text:%s 或 \"%s\"）", t, t, t)
}

var tokenRe = regexp.MustCompile(`(?i:text:)?"[^"]*"|\S+`)

// ParseSpec 解析按键谱。双引号里的内容原样发送（可以带空格）。
// 例：`ctrl+b c`、`esc`、`"herdr" enter`、`text:/new enter`、`up`、`alt+1`
func ParseSpec(spec string) (string, error) {
	s := strings.TrimSpace(spec)
	if s == "" {
		return "", fmt.Errorf("按键谱是空的")
	}
	if len(s) > 200 {
		return "", fmt.Errorf("按键谱太长")
	}
	var b strings.Builder
	for _, m := range tokenRe.FindAllString(s, -1) {
		if strings.HasPrefix(m, `"`) {
			b.WriteString(strings.Trim(m, `"`))
			continue
		}
		v, err := token(m)
		if err != nil {
			return "", err
		}
		b.WriteString(v)
	}
	if b.Len() == 0 {
		return "", fmt.Errorf("按键谱解析出来是空的")
	}
	return b.String(), nil
}

// normalize 校验并规整一条。错误消息是给用户看的。
func normalize(k Key, i int) (Key, error) {
	at := fmt.Sprintf("第 %d 个按键", i+1)
	label := strings.TrimSpace(k.Label)
	if utf8.RuneCountInString(label) > 12 {
		return Key{}, fmt.Errorf("%s 的名字太长（最多 12 个字符）", at)
	}

	kinds := 0
	for _, s := range []string{k.Send, k.Sticky, k.Act} {
		if s != "" {
			kinds++
		}
	}
	if kinds != 1 {
		return Key{}, fmt.Errorf("%s 必须正好是 send / sticky / act 中的一种", at)
	}

	// confirm 对 sticky / act 也照样透传：粘滞键和键盘键误触无所谓，但这里不拦，
	// 少一条规则就少一句要背的话，前端一视同仁处理。
	out := Key{ID: k.ID, Label: label, Wide: k.Wide, Confirm: k.Confirm}
	switch {
	case k.Sticky != "":
		if k.Sticky != "ctrl" && k.Sticky != "alt" {
			return Key{}, fmt.Errorf("%s 的 sticky 只能是 ctrl 或 alt", at)
		}
		out.Sticky = k.Sticky
	case k.Act != "":
		// act 是「网页端自己处理」的动作，不发任何字节。这里只认白名单里的几个：
		// 前端拿到不认识的 act 就只能画一个点了没反应的键。
		if k.Act != "kbd" && k.Act != "img" {
			return Key{}, fmt.Errorf("%s 的 act 目前只支持 kbd（键盘）/ img（传图）", at)
		}
		out.Act = k.Act
	default:
		out.Spec = strings.TrimSpace(k.Send)
		send, err := ParseSpec(out.Spec)
		if err != nil {
			return Key{}, fmt.Errorf("%s：%w", at, err)
		}
		out.Send = send
	}
	if out.Label == "" {
		out.Label = firstNonEmpty(out.Spec, out.Sticky, out.Act)
	}
	return out, nil
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// Resolve 全部校验 + 解析，任一条不合法就整体失败。
func Resolve(keys []Key) ([]Key, error) {
	out := make([]Key, 0, len(keys))
	for i, k := range keys {
		n, err := normalize(k, i)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

type Store struct{ Dir string }

func (s *Store) path() string { return filepath.Join(s.Dir, "softkeys.json") }

// DefaultConfig 出厂配置：一行，键就是 Defaults()，全都摆在条上。
func DefaultConfig() Config {
	lib := Defaults()
	ids := make([]string, len(lib))
	for i := range lib {
		lib[i].ID = fmt.Sprintf("k%d", i+1)
		ids[i] = lib[i].ID
	}
	return Config{Rows: 1, Lib: lib, Bar: [][]string{ids}}
}

// Load 读配置；文件不存在或存坏了都退回出厂配置。
func (s *Store) Load() Config {
	fallback := func() Config {
		out, _ := resolveConfig(DefaultConfig())
		return out
	}
	b, err := os.ReadFile(s.path())
	if err != nil {
		return fallback()
	}
	var f file
	if err := json.Unmarshal(b, &f); err != nil || f.Keys == nil {
		return fallback()
	}
	lib := make([]Key, len(f.Keys))
	for i, k := range f.Keys {
		lib[i] = Key{
			ID: k.ID, Label: k.Label, Wide: k.Wide, Confirm: k.Confirm,
			Send: k.Send, Sticky: k.Sticky, Act: k.Act,
		}
	}
	bar := f.Bar
	if bar == nil {
		bar = migrate(f.Keys, &lib)
	}
	out, err := resolveConfig(Config{Rows: f.Rows, Lib: lib, Bar: bar})
	if err != nil {
		return fallback()
	}
	return out
}

// migrate 把老文件（「排第几行」长在按键上：row / off）翻成 Lib + Bar 这一层。
//
// 老配置里一个键只可能出现在一个地方，所以迁移就是「按 row 分两桶，off 的不进桶」。
// 顺手补 ID —— 老文件里没这个字段。已经调好的软键条不该因为升级白丢。
func migrate(old []stored, lib *[]Key) [][]string {
	ids := newIDs(*lib)
	for i := range *lib {
		if (*lib)[i].ID == "" {
			(*lib)[i].ID = ids()
		}
	}
	bar := [][]string{{}, {}}
	for i, k := range old {
		if k.Off {
			continue
		}
		row := 0
		if k.Row == MaxRows {
			row = 1
		}
		bar[row] = append(bar[row], (*lib)[i].ID)
	}
	return bar
}

// Save 先全部校验通过再落盘。
func (s *Store) Save(c Config) (Config, error) {
	out, err := resolveConfig(c)
	if err != nil {
		return Config{}, err
	}
	raw := make([]stored, len(out.Lib))
	for i, k := range out.Lib {
		raw[i] = stored{ID: k.ID, Label: k.Label, Wide: k.Wide, Confirm: k.Confirm, Sticky: k.Sticky, Act: k.Act}
		if k.Sticky == "" && k.Act == "" {
			raw[i].Send = strings.TrimSpace(firstNonEmpty(k.Spec, k.Send))
		}
	}
	b, err := json.MarshalIndent(file{Rows: out.Rows, Keys: raw, Bar: out.Bar}, "", "  ")
	if err != nil {
		return Config{}, err
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return Config{}, err
	}
	if err := os.WriteFile(s.path(), append(b, '\n'), 0o600); err != nil {
		return Config{}, err
	}
	return out, nil
}

// newIDs 发 ID：k1、k2……跳过已经用掉的号。
//
// 用递增的小字符串而不是随机串：这份 JSON 是人会去看、偶尔会手改的（README 里写了
// 路径），`"bar": [["k1","k3"]]` 一眼能对上是哪几个键，随机 UUID 就只能靠搜。
func newIDs(lib []Key) func() string {
	used := make(map[string]bool, len(lib))
	for _, k := range lib {
		if k.ID != "" {
			used[k.ID] = true
		}
	}
	n := 0
	return func() string {
		for {
			n++
			id := fmt.Sprintf("k%d", n)
			if !used[id] {
				used[id] = true
				return id
			}
		}
	}
}

// resolveConfig 校验一整份配置：行数、每个定义、每行的引用。
//
// Rows 是 1 的时候把第二行的引用**接到第一行末尾**，而不是留着不显示 —— 界面上看不见
// 的键是最烦人的一种状态（存着、不显示、下次改行数又冒出来）。所见即所得。
func resolveConfig(c Config) (Config, error) {
	rows := c.Rows
	if rows == 0 {
		rows = 1
	}
	if rows < 1 || rows > MaxRows {
		return Config{}, fmt.Errorf("软键条只能是 1 或 2 行")
	}
	if len(c.Lib) > MaxKeys {
		return Config{}, fmt.Errorf("「我的按键」最多 %d 个", MaxKeys)
	}
	lib, err := Resolve(c.Lib)
	if err != nil {
		return Config{}, err
	}

	// 补 ID / 去重 ID。重复的 ID 会让「改一处、条上所有引用一起变」变成「改到了别人」，
	// 所以后来的那个直接换一个新号，而不是报错 —— 这种冲突用户看不见，报错也没法改。
	ids := newIDs(lib)
	seen := make(map[string]bool, len(lib))
	for i := range lib {
		if lib[i].ID == "" || seen[lib[i].ID] {
			lib[i].ID = ids()
		}
		seen[lib[i].ID] = true
	}

	bar := make([][]string, MaxRows)
	for i := range bar {
		bar[i] = []string{}
	}
	for i, row := range c.Bar {
		if i >= MaxRows {
			return Config{}, fmt.Errorf("软键条最多 %d 行", MaxRows)
		}
		if len(row) > MaxBar {
			return Config{}, fmt.Errorf("第 %d 行最多 %d 个按键", i+1, MaxBar)
		}
		for _, id := range row {
			if !seen[id] {
				// 不静默丢掉：前端删定义时该把引用一起清掉，丢了只会变成「保存完少了个键」
				return Config{}, fmt.Errorf("第 %d 行引用了不存在的按键（%s）", i+1, id)
			}
			bar[i] = append(bar[i], id)
		}
	}
	if rows == 1 && len(bar[1]) > 0 {
		bar[0] = append(bar[0], bar[1]...)
		bar[1] = []string{}
	}
	return Config{Rows: rows, Lib: lib, Bar: bar[:rows]}, nil
}
