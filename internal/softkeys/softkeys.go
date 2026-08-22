// Package softkeys 管软键条的配置：存在 ~/.herdr-web/softkeys.json，在网页上编辑。
//
// 存服务端而不是浏览器 localStorage，是为了手机 / 平板 / 电脑共用一份 —— 和 token
// 落盘同一个道理，改一次到处生效。
//
// **但「排布」是按 profile 分的**（Lib 全局、Rows/Bar 每套一份，见 internal/profiles）：
// 定义共用一份是对的，排布共用一份是错的 —— 平板上二十个键排两行，手机竖屏上放得下五个。
// 所以 Load / Save 都要带一个 profile ID。
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
//	{act: "panes"}      面板一览（点一行跳过去 + 全屏）
//	{act: "files"}      文件浏览（看 agent 生成的图 / 翻目录）
//	{act: "clip"}       把机器上的剪贴板取到手机剪贴板（herdr 复制的东西在那儿）
//	{act: "paste"}      把手机剪贴板粘进终端
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
	"sync"
	"unicode/utf8"

	"github.com/zbysir/herdr-web/internal/profiles"
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
// acts 是 act 的白名单。前端拿到不认识的 act 只能画一个点了没反应的键，所以这里挡住。
var acts = map[string]bool{"kbd": true, "img": true, "panes": true, "files": true, "clip": true, "paste": true}

type Key struct {
	ID      string `json:"id,omitempty"` // 稳定标识，软键条按这个引用（存盘时补齐）
	Label   string `json:"label"`
	Wide    bool   `json:"wide,omitempty"`
	Confirm bool   `json:"confirm,omitempty"` // 要点两下才发（防误触）
	Send    string `json:"send,omitempty"`    // 解析出来的字节（下发给前端）
	Spec    string `json:"spec,omitempty"`    // 用户写的按键谱（回显到编辑器）
	Sticky  string `json:"sticky,omitempty"`  // ctrl | alt
	Act     string `json:"act,omitempty"`     // kbd | img | panes | files | clip | paste（网页端自己处理，不发字节）
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
	// Rows / Bar 是**默认那一套**的排布，同时也是「老形状」：分 profile 之前整份文件就是
	// Rows + Keys + Bar。新版本把每一套写进 Profiles，但默认那一套**照旧往这儿也写一份**
	// —— 降级回老版本时它只认得顶层这几个字段，不镜像的话降级看到的是「软键条恢复出厂」，
	// 而那份配置明明还在文件里。冗余一份换这个，值。
	Rows int        `json:"rows,omitempty"`
	Keys []stored   `json:"keys"`
	Bar  [][]string `json:"bar,omitempty"`
	// Profiles 每套排布一段（键是 profiles.json 里那个 ID）。
	// nil = 老文件，整份就是默认那一套（见 sections）。
	Profiles map[string]lane `json:"profiles,omitempty"`
}

// lane 是一套排布：几行 + 每行一串 ID。
//
// 「我的按键」（Keys）**不在这儿**：定义是全局的，rows/bar 才按 profile 分 —— 平板上
// 二十个键排两行、手机上五个键一行，但改一个按键谱两边一起变。理由见
// internal/profiles 的包注释。
type lane struct {
	Rows int        `json:"rows"`
	Bar  [][]string `json:"bar"`
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
		// clip / paste 是剪贴板那两条：**两个方向各要一次点击**，因为浏览器只在用户手势里
		// 给读写剪贴板（见 web/src/lib/clipboard.ts）。
		if !acts[k.Act] {
			return Key{}, fmt.Errorf("%s 的 act 目前只支持 kbd（键盘）/ img（传图）/ panes（面板一览）/ files（文件浏览）"+
				"/ clip（取机器剪贴板）/ paste（粘手机剪贴板）", at)
		}
		out.Act = k.Act
	default:
		// **认 Spec 优先**：编辑器回传的那份往往两个字段都在（Spec 是用户写的 "tab"，
		// Send 是服务端上次解析出来的字节 "\t"）。拿 Send 当谱再解一次的话，"\t"
		// TrimSpace 之后是空串 —— 报「按键谱是空的」，而用户什么都没改（踩过）。
		out.Spec = strings.TrimSpace(firstNonEmpty(k.Spec, k.Send))
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

type Store struct {
	Dir string
	// 写路径全是「读整份 → 改一段 → 写回」（别的 profile 那几段要原样保住），而两台设备
	// 各自在自己的 profile 上点保存是常事。没有这把锁就是「谁后写谁把对方那一段吃掉」。
	mu sync.Mutex
}

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

// Load 读某一套排布（「我的按键」是全局的，每套都拿到同一份）。
//
// 文件不存在 / 存坏了 → 出厂配置。**引用了不存在的定义时丢掉那一个引用，不是整份作废**：
// 「我的按键」是全局的，在平板上删掉一个定义就会打到手机那一套的条上，那时候整份退回
// 出厂等于连带把没坏的部分也抹了。存的时候是严格的（见 Save），宽松只留给读。
func (s *Store) Load(profile string) Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load(profile)
}

func (s *Store) load(profile string) Config {
	fallback := func() Config {
		out, _ := resolveConfig(DefaultConfig(), false)
		return out
	}
	f, ok := s.read()
	if !ok {
		return fallback()
	}
	lib := keysOf(f)
	ln, has := pick(f, profile)
	if !has {
		// 这一套还没排过（名册里有、排布没有：手改过文件，或者复制那一步没成）。
		// 给出厂那一排，别给一条空栏 —— 空栏看着就像坏了，而且软键条上一个键都没有时
		// 手机上连键盘都呼不出来。
		out, err := factory(lib)
		if err != nil {
			return fallback()
		}
		return out
	}
	bar := ln.Bar
	if bar == nil {
		bar = migrate(f.Keys, &lib)
	}
	out, err := resolveConfig(Config{Rows: ln.Rows, Lib: lib, Bar: bar}, true)
	if err != nil {
		return fallback()
	}
	return out
}

// read 读文件。第二个返回值是「这份文件能用吗」—— 不在、坏了、连 keys 都没有都算不能用。
func (s *Store) read() (file, bool) {
	b, err := os.ReadFile(s.path())
	if err != nil {
		return file{}, false
	}
	var f file
	if err := json.Unmarshal(b, &f); err != nil || f.Keys == nil {
		return file{}, false
	}
	return f, true
}

// keysOf 把落盘的定义摊成 Key（Send 这时候还是用户写的谱，resolveConfig 里才解析）。
func keysOf(f file) []Key {
	lib := make([]Key, len(f.Keys))
	for i, k := range f.Keys {
		lib[i] = Key{
			ID: k.ID, Label: k.Label, Wide: k.Wide, Confirm: k.Confirm,
			Send: k.Send, Sticky: k.Sticky, Act: k.Act,
		}
	}
	return lib
}

// pick 挑这一套的排布。
//
// 找不到就退到**默认那一套**（老文件里默认那一套就是顶层那几个字段）—— 名册里新建的一套
// 通常在建的时候就复制好了排布，退到默认是给「手改文件」和「复制那一步没成」兜底。
// 两个都没有才算「这一套还没排过」。
func pick(f file, profile string) (lane, bool) {
	if ln, ok := f.Profiles[profile]; ok {
		return ln, true
	}
	if ln, ok := f.Profiles[profiles.Default]; ok {
		return ln, true
	}
	// f.Profiles == nil 是老文件：顶层就是默认那一套（Bar 为 nil 时更老，交给 migrate）
	if f.Profiles == nil || f.Bar != nil {
		return lane{Rows: f.Rows, Bar: f.Bar}, true
	}
	return lane{}, false
}

// sections 把文件里的排布摊成「每套一段」，并且**保证默认那一套一定在里面**：老文件的
// 顶层字段、更老的 row+off 都在这儿收敛掉。
//
// 所有写路径都得先过这一下。不然「升级之后第一件事是新建第二套」会把还留在顶层、还没
// 搬进 Profiles 的默认那一套挤掉（写回去时顶层被覆盖，而 Profiles 里没有它）。
func sections(f file) map[string]lane {
	out := map[string]lane{}
	for id, ln := range f.Profiles {
		out[id] = ln
	}
	if _, ok := out[profiles.Default]; !ok && f.Keys != nil {
		bar := f.Bar
		if bar == nil {
			lib := keysOf(f)
			bar = migrate(f.Keys, &lib)
		}
		out[profiles.Default] = normLane(lane{Rows: f.Rows, Bar: bar})
	}
	return out
}

// normLane 把一段排布收拾成「落盘的样子和读出来的样子一致」。
//
// 为什么需要：老文件没有 rows 字段（0 = 按一行算），而更老的文件里「排第几行」长在按键上
// —— 迁过来就是「rows 说一行、条上却有两行引用」。读的时候 resolveConfig 会把第二行接到
// 第一行末尾，所以显示是对的，但**落盘那份是自相矛盾的**，正是注释里一直在防的那种
// 「存着、不显示」的状态（下次谁改了折叠逻辑就会冒出一批幽灵键）。
func normLane(ln lane) lane {
	if ln.Rows == 0 {
		ln.Rows = 1
	}
	if ln.Rows == 1 && len(ln.Bar) > 1 {
		first := append([]string{}, ln.Bar[0]...)
		for _, row := range ln.Bar[1:] {
			first = append(first, row...)
		}
		ln.Bar = [][]string{first}
	}
	return ln
}

// factory 出厂那一排：把出厂的键**补进**现有的「我的按键」（按名字 + 干什么认，已经有的
// 就复用），再全摆到第一行。
//
// 为什么不是「整份恢复出厂」：定义是全局的，整份恢复会把别的 profile 条上引用的定义一起
// 抹掉 —— 在手机上点一下「恢复默认」，平板上的软键条跟着少一半，这种连带损害用户完全
// 预料不到。
//
// **出错就报错，绝不静默退回出厂配置**：补键会顶到 MaxKeys（库里已经有 120 个的时候），
// 那时候退回出厂等于把用户那 120 个定义连着别的 profile 的条一起抹了 —— 一个「恢复默认」
// 按钮不该有这种权力。读文件那条路自己兜底（见 load），写盘那条路把错报出去（见 Reset）。
func factory(lib []Key) (Config, error) {
	have := map[string]string{} // 签名 → ID
	for _, k := range lib {
		if k.ID != "" {
			have[sigOf(k)] = k.ID
		}
	}
	ids := newIDs(lib)
	row := make([]string, 0, len(Defaults()))
	for _, d := range Defaults() {
		id, ok := have[sigOf(d)]
		if !ok {
			d.ID = ids()
			id = d.ID
			lib = append(lib, d)
			have[sigOf(d)] = id
		}
		row = append(row, id)
	}
	if len(lib) > MaxKeys {
		return Config{}, fmt.Errorf("要往「我的按键」里补几个出厂键，但已经到上限 %d 了 —— 先删几个再来", MaxKeys)
	}
	return resolveConfig(Config{Rows: 1, Lib: lib, Bar: [][]string{row}}, true)
}

// sigOf 「同一个键」的判定：名字 + 干什么。和前端 SoftkeysPanel 里的 sig 是同一套。
func sigOf(k Key) string {
	kind := "send:" + strings.TrimSpace(firstNonEmpty(k.Spec, k.Send))
	if k.Sticky != "" {
		kind = "sticky:" + k.Sticky
	} else if k.Act != "" {
		kind = "act:" + k.Act
	}
	return k.Label + "\x00" + kind
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

// Save 存某一套排布 + 那份全局的定义。先全部校验通过再落盘。
//
// 定义是全局的，所以这一下**会打到别的 profile 上**：这次删掉的定义，别的 profile 条上
// 那些引用也一起清掉（prune）。不清的后果是别人下次读出来少一个键，而且是静默的。
func (s *Store) Save(profile string, c Config) (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.write(profile, c)
}

func (s *Store) write(profile string, c Config) (Config, error) {
	out, err := resolveConfig(c, false)
	if err != nil {
		return Config{}, err
	}
	f, _ := s.read() // 读不出来就当从零开始（别的 profile 本来也没有）
	secs := sections(f)
	secs[profile] = lane{Rows: out.Rows, Bar: out.Bar}
	live := map[string]bool{}
	for _, k := range out.Lib {
		live[k.ID] = true
	}
	prune(secs, live)

	raw := make([]stored, len(out.Lib))
	for i, k := range out.Lib {
		raw[i] = stored{ID: k.ID, Label: k.Label, Wide: k.Wide, Confirm: k.Confirm, Sticky: k.Sticky, Act: k.Act}
		if k.Sticky == "" && k.Act == "" {
			raw[i].Send = strings.TrimSpace(firstNonEmpty(k.Spec, k.Send))
		}
	}
	nf := file{Keys: raw, Profiles: secs}
	// 默认那一套镜像到顶层老字段（降级用，见 file 的注释）
	if d, ok := secs[profiles.Default]; ok {
		nf.Rows, nf.Bar = d.Rows, d.Bar
	}
	b, err := json.MarshalIndent(nf, "", "  ")
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

// prune 把条上引用了已经不存在的定义的那些引用去掉（所有 profile 一起）。
func prune(secs map[string]lane, live map[string]bool) {
	for id, ln := range secs {
		bar := make([][]string, 0, len(ln.Bar))
		for _, row := range ln.Bar {
			keep := make([]string, 0, len(row))
			for _, k := range row {
				if live[k] {
					keep = append(keep, k)
				}
			}
			bar = append(bar, keep)
		}
		secs[id] = lane{Rows: ln.Rows, Bar: bar}
	}
}

// Reset 「这一套恢复默认」：出厂那一排回到条上，「我的按键」里缺的那几个补上，
// **别的定义、别的 profile 一个都不动**（理由见 factory）。
func (s *Store) Reset(profile string) (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := factory(s.load(profile).Lib)
	if err != nil {
		return Config{}, err
	}
	return s.write(profile, c)
}

// Copy 把一套排布复制成另一套（新建时用）。from 没排过就复制它读出来的那一份。
func (s *Store) Copy(from, to string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.load(from)
	_, err := s.write(to, c)
	return err
}

// Drop 删掉一套排布那一段（profile 删掉之后清场）。默认那一套删不掉 —— 它是所有
// 兜底的落点，见 internal/profiles。
func (s *Store) Drop(profile string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if profile == profiles.Default {
		return fmt.Errorf("默认那一套的软键条删不掉")
	}
	f, ok := s.read()
	if !ok {
		return nil
	}
	secs := sections(f)
	if _, has := secs[profile]; !has {
		return nil
	}
	delete(secs, profile)
	f.Profiles = secs
	if d, ok := secs[profiles.Default]; ok {
		f.Rows, f.Bar = d.Rows, d.Bar
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path(), append(b, '\n'), 0o600)
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
// dropUnknown 为真时，条上引用了不存在的定义就**丢掉那个引用**（读文件走这条：定义是
// 全局的，别的 profile 删过定义）；为假时报错（存盘走这条：编辑器该自己把引用清掉，
// 静默丢只会变成「保存完少了个键」）。
func resolveConfig(c Config, dropUnknown bool) (Config, error) {
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
				if dropUnknown {
					continue
				}
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
