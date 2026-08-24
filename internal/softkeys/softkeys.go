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
//	{group: {...}}      点开一小片浮窗，里面还是键（方向键盘那种）
//
// 另外每个按键可以打上 {confirm: true}：第一下只是「举起来」，第二下才真发出去。
// 给关 pane / 关标签 / 断开这种误触代价很大的键用。
//
// # 弹出组（Group）
//
// 一个键**只占一格**，点一下在它上方弹一小片网格，里面才是那几个键。方向键盘就该是这个 ——
// 摊在条上要 3×2 六格，手机竖屏上那是半条屏幕（用户的原话：「一下占掉 4 个位置太离谱」）。
//
// 它是 Lib 里的**第四种形态**，不是 lane 上的东西 —— 也就是说它就是一个普通键：能拖到条上
// 占一格、能塞进固定块、**也能上顶栏**（`key:<定义ID>`）。和固定块正好组合：1 格的固定块里
// 放一个组键 = 一个永远不滑走的格子，点开是方向键盘。
//
// 组里放的还是**引用**（和 Bar / Pad 一样），所以「改一处定义处处变」照旧。
// **组里不能再放组**（一层就够，嵌套只会让「点开还要再点开」）—— 在 resolveConfig 里挡。
//
// 软键条**几行是个设置**（Config.Rows，1 或 2），不靠「第二行空不空」猜 —— 空的第二行
// 和「我只要一行」是两件事。两行各自横向滚动：手机上一行只放得下四五个键，横滑找键比多
// 占一行终端便宜，但「最常用的几个」和「次常用的几个」分两行、各滑各的，比十几个键排成
// 一条长龙好找。
//
// # 固定块（Pad）
//
// 条的一端可以钉一小片**对齐的网格**（方向键那种），它**不跟着横滑**。
//
// 为什么要单独一种东西，而不是「把 ← ↓ → 放到 ↑ 底下」：那样也摆得出来，但**两行各自
// 横滑**（上面那条）和**跨行对齐**是互斥的 —— 滑一下其中一行，对齐当场就没了。所以对齐
// 只能存在于「作为一个整体参与滑动的原子」里面，而最有用的那种原子就是「压根不滑」。
// 顺带解决手机上真正的痛点：方向键永远在拇指底下，不会被滑走。
//
// 它是**排布**不是定义，所以**每套一份**（长在 lane 上，和 Rows/Bar 一起）—— 平板上
// 摆一个 4 列的、手机竖屏摆 3 列的，是两回事。格子里放的还是全局定义的 ID。
//
// 宽度对得齐靠的是 Key.Span 那个「格」（见 MaxSpan）：一格 = 前端的 --sk-w。
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

	"github.com/zbysir/herdr-web/internal/capability"
	"github.com/zbysir/herdr-web/internal/profiles"
)

// MaxKeys 「我的按键」最多几个定义。比软键条上能放的多得多是有意的：定义只占一行
// JSON，屏幕上不占地方 —— 「载入预设」一下就会灌进来六十多个，卡在 40 上没法用。
const MaxKeys = 120

// MaxRows 软键条最多几行。两行是**屏幕**定的上限，不是实现限制：手机竖屏上第三行
// 就该拿去当终端了（一行 28px ≈ 两行终端）。真要更多键就横滑，别往下堆。
const MaxRows = 2

// MaxSpan 一个键最多占几格宽。3 格（≈144px，手机上 ≈120px）已经是半条屏幕，
// 再宽就该拆成两个键了。前端那份上限在 web/src/lib/keys.ts。
const MaxSpan = 3

// spanOf 把「宽」这个老字段收敛成格数。**只给读路径用**（读得宽松，见 Load）。
//
// span 缺失而 wide=true 就算 2 格：老文件、以及从新版本降级回去写过的文件，都只有 wide。
// 不认它的话升级一次所有宽键一起变窄 —— 而那是用户自己调过的东西。
func spanOf(span int, wide bool) int {
	if span <= 0 {
		if wide {
			return 2
		}
		return 1
	}
	if span > MaxSpan {
		return MaxSpan
	}
	return span
}

// MaxPadCols 固定块最多几列。4 列 ≈ 194px，在 393px 的手机竖屏上已经占掉半条，
// 剩下的给横滑那部分就不够了。
const MaxPadCols = 4

// MaxGroupCols 弹出组最多几列。浮窗要能落在屏幕里，5 列 ≈ 244px 已经接近手机竖屏的宽度。
const MaxGroupCols = 5

// MaxGroupRows 弹出组最多几行。浮窗是浮在条上方的，太高就把终端盖住了。
const MaxGroupRows = 3

// Group 是弹出组里那片网格（见包注释）。
//
// Cells 按行读，长度固定 Cols*MaxGroupRows，空串 = 空格子 —— 方向键盘上方那两个空位就是
// 靠它占出来的。和 Pad 一样存引用，不存定义。
type Group struct {
	Cols  int      `json:"cols"`
	Cells []string `json:"cells"`
}

// Pad 是钉在条一端的对齐网格（见包注释）。
//
// Cells **按行读**，长度固定 Cols*MaxRows —— 和画面上看到的顺序一样，这份 JSON 是人会
// 去看的（`["", "k7", "", "k9", "k8", "k10"]` 一眼就是那个方向键盘）。空字符串 = 空格子，
// 空格子是有意义的：方向键盘上方那两个空位就是靠它占出来的。
//
// 条只有一行时（Rows==1），**第二行的格子接到第一行后面** —— 和 Bar 那条规则一个道理
// （所见即所得，不留「存着、不显示」的状态）。那时候谈不上对齐，但「不跟着滑」还在。
type Pad struct {
	Cols  int      `json:"cols"`
	Cells []string `json:"cells"`
	Side  string   `json:"side,omitempty"` // left | right（空 = right）
}

// MaxBar 一行最多引用多少个键。允许重复之后这个数得有个头，不然一次误操作就能塞进
// 几千个引用。
const MaxBar = 40

// Key 是一个按键的定义。Spec/Send 只在 send 形态下有值。

type Key struct {
	ID    string `json:"id,omitempty"` // 稳定标识，软键条按这个引用（存盘时补齐）
	Label string `json:"label"`
	// Span 占几格宽（1..MaxSpan）。一格 = 前端那个 --sk-w（44px，手机 36px）。
	// 存「几格」而不是像素：跨行对齐要的就是整数格 —— 一个 2 格的键和它上面两个 1 格的
	// 键左右边缘对得齐，而这是固定块（对齐网格）唯一站得住的前提。
	Span int `json:"span,omitempty"`
	// Wide 是 Span 的**降级镜像**，只为老版本留着（span>=2 时写 true）。
	// 老版本只认得它，不写的话降级看到的是「我调过的宽键全变窄了」。
	// 读的时候反过来认（见 spanOf）：从新版本降级回去再升上来，宽度不该丢。
	Wide bool `json:"wide,omitempty"`
	// Icon 条上画哪个**内置图标**（空 = 画 Label 那段文字）。白名单见 icons.go ——
	// 字形（`⌨` 这种）在很多字体里压根缺（显示成方框）、有的字体里很难看、大小和基线还跟
	// 旁边的字母对不齐；图标是 SVG，三个问题一起没了。
	// **Label 照旧是名字**（编辑器认它、组键靠它、title 里显示它），Icon 只决定条上画什么。
	Icon    string `json:"icon,omitempty"`
	Confirm bool   `json:"confirm,omitempty"` // 要点两下才发（防误触）
	Send    string `json:"send,omitempty"`    // 解析出来的字节（下发给前端）
	Spec    string `json:"spec,omitempty"`    // 用户写的按键谱（回显到编辑器）
	Sticky  string `json:"sticky,omitempty"`  // ctrl | alt
	Act     string `json:"act,omitempty"`     // kbd | img | panes | files | clip | paste（网页端自己处理，不发字节）
	Group   *Group `json:"group,omitempty"`   // 点开一小片浮窗，见包注释
}

// stored 是落盘的形状：只存用户写的东西，不存解析结果。
type stored struct {
	ID      string `json:"id,omitempty"`
	Label   string `json:"label"`
	Span    int    `json:"span,omitempty"`
	Wide    bool   `json:"wide,omitempty"` // Span 的降级镜像，见 Key.Wide
	Icon    string `json:"icon,omitempty"`
	Confirm bool   `json:"confirm,omitempty"`
	Send    string `json:"send,omitempty"`
	Sticky  string `json:"sticky,omitempty"`
	Act     string `json:"act,omitempty"`
	Group   *Group `json:"group,omitempty"`

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
	Pad  *Pad       `json:"pad,omitempty"`
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
	Pad  *Pad       `json:"pad,omitempty"` // 固定块，见 Pad
}

// Config 是一整份软键条配置。
type Config struct {
	Rows int        `json:"rows"`
	Lib  []Key      `json:"lib"` // 我的按键：所有定义
	Bar  [][]string `json:"bar"` // 每行一串 Lib 里的 ID（允许重复）
	Pad  *Pad       `json:"pad,omitempty"`
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
	if k.Group != nil {
		kinds++
	}
	if kinds != 1 {
		return Key{}, fmt.Errorf("%s 必须正好是 send / sticky / act / group 中的一种", at)
	}

	// confirm 对 sticky / act 也照样透传：粘滞键和键盘键误触无所谓，但这里不拦，
	// 少一条规则就少一句要背的话，前端一视同仁处理。
	// 宽度：存盘这一侧**严格**。段控件只发 1/2/3，超出范围就是前端有 bug，
	// 静默夹到边界只会让那个 bug 留在那儿（读路径那侧是 spanOf 在夹，见 Load）。
	span := k.Span
	if span == 0 {
		// 只给了老字段（降级回去存过一版，或者手改的文件）
		if k.Wide {
			span = 2
		} else {
			span = 1
		}
	}
	if span < 1 || span > MaxSpan {
		return Key{}, fmt.Errorf("%s 的宽只能是 1 到 %d 格", at, MaxSpan)
	}
	// 图标：存盘这一侧**严格**。选择器只发白名单里的 id，超出去就是前端有 bug，
	// 静默丢掉只会让 bug 留在那儿（读路径那侧是 keysOf 在丢，见 Load）
	if !IconOK(k.Icon) {
		return Key{}, fmt.Errorf("%s 的图标不认识：%q", at, k.Icon)
	}
	out := Key{ID: k.ID, Label: label, Span: span, Wide: span >= 2, Icon: k.Icon, Confirm: k.Confirm}
	switch {
	case k.Group != nil:
		// 只查形状。格子里那些引用要等整份 Lib 的 ID 都定下来才查得了（和 Bar / Pad 一样
		// 在 resolveConfig 里），所以这儿先原样带过去。
		if k.Group.Cols < 1 || k.Group.Cols > MaxGroupCols {
			return Key{}, fmt.Errorf("%s 的弹出组只能是 1 到 %d 列", at, MaxGroupCols)
		}
		if label == "" {
			// 组键在条上只占一格，格子上那个名字是它唯一的说明 —— 空着就是一个看不出
			// 是什么的方块。别的形态可以从按键谱 / act 兜出一个名字，这个兜不出来。
			return Key{}, fmt.Errorf("%s 是弹出组，得给它一个名字（条上只占一格，全靠这个认）", at)
		}
		out.Group = &Group{Cols: k.Group.Cols, Cells: k.Group.Cells}
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
		// 白名单在 internal/capability（和顶栏那份、前端那份是同一张表 —— 为什么合成一份见
		// 那个包的注释）。前端拿到不认识的 act 只能画一个点了没反应的键，所以这里挡住。
		if !capability.IsKeyAct(k.Act) {
			return Key{}, fmt.Errorf("%s 的 act 只能是这几个：%s", at, strings.Join(capability.KeyActs(), " / "))
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
	lib, bar := wireDefaults(nil, newIDs(nil))
	return Config{Rows: 1, Lib: lib, Bar: [][]string{bar}}
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

// LibIDs 是「我的按键」现在有哪些定义 ID。**不分 profile**：定义是全局的。
//
// 给顶栏那边核 `key:` 引用用（见 internal/topbar 的 Store.Keys）。给一份 map 而不是让
// 那边自己拿 Load().Lib 摊一遍，是因为顶栏只关心「在不在」—— 别把整份定义（按键谱解析
// 出来的字节都在里面）漏到那个包里去，那两份配置是刻意分开的两个口。
func (s *Store) LibIDs() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.load(profiles.Default)
	out := make(map[string]bool, len(c.Lib))
	for _, k := range c.Lib {
		if k.ID != "" {
			out[k.ID] = true
		}
	}
	return out
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
	out, err := resolveConfig(Config{Rows: ln.Rows, Lib: lib, Bar: bar, Pad: ln.Pad}, true)
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
			ID: k.ID, Label: k.Label, Span: spanOf(k.Span, k.Wide), Confirm: k.Confirm,
			// 认不出的图标就当没挑（画文字标签）—— 整份退回出厂太贵，而 Label 一直在
			Icon: iconOf(k.Icon),
			Send: k.Send, Sticky: k.Sticky, Act: k.Act, Group: k.Group,
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
		return lane{Rows: f.Rows, Bar: f.Bar, Pad: f.Pad}, true
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
		out[profiles.Default] = normLane(lane{Rows: f.Rows, Bar: bar, Pad: f.Pad})
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
	lib, row := wireDefaults(lib, newIDs(lib))
	if len(lib) > MaxKeys {
		return Config{}, fmt.Errorf("要往「我的按键」里补几个出厂键，但已经到上限 %d 了 —— 先删几个再来", MaxKeys)
	}
	return resolveConfig(Config{Rows: 1, Lib: lib, Bar: [][]string{row}}, true)
}

// wireDefaults 把出厂那份接起来：缺的定义发个 ID 补进 lib、「方向」那个弹出组的格子从
// 「哪个键」换成 ID、再按 DefaultBar 排出条。
//
// 两个入口共用（DefaultConfig 从零、factory 在现有 lib 上补）—— 各写一遍的话「出厂长什么
// 样」就有两个版本，而「文件坏了」和「恢复默认」走的偏偏是不同那一个。
func wireDefaults(lib []Key, mint func() string) ([]Key, []string) {
	have := make(map[string]string, len(lib)+len(Defaults()))
	for _, k := range lib {
		if k.ID != "" {
			have[sigOf(k)] = k.ID
		}
	}
	// 先补成员定义（组的格子要引用它们），再补组本身
	for _, d := range Defaults() {
		if _, ok := have[sigOf(d)]; ok {
			continue
		}
		d.ID = mint()
		lib = append(lib, d)
		have[sigOf(d)] = d.ID
	}
	gk := arrowGroupKey()
	if _, ok := have[sigOf(gk)]; !ok {
		cells := make([]string, defaultArrows.Cols*MaxGroupRows)
		for i, c := range defaultArrows.Cells {
			if i >= len(cells) || (c.Label == "" && c.Send == "") {
				continue // 空格子
			}
			cells[i] = have[sigOf(c)] // 找不到就留空，别硬塞一个引用
		}
		gk.ID = mint()
		gk.Group = &Group{Cols: defaultArrows.Cols, Cells: cells}
		lib = append(lib, gk)
		have[sigOf(gk)] = gk.ID
	}
	bar := make([]string, 0, len(DefaultBar()))
	for _, d := range DefaultBar() {
		if id, ok := have[sigOf(d)]; ok {
			bar = append(bar, id)
		}
	}
	return lib, bar
}

// sigOf 「同一个键」的判定：名字 + 干什么。和前端 SoftkeysPanel 里的 sig 是同一套。
func sigOf(k Key) string {
	kind := "send:" + strings.TrimSpace(firstNonEmpty(k.Spec, k.Send))
	if k.Sticky != "" {
		kind = "sticky:" + k.Sticky
	} else if k.Act != "" {
		kind = "act:" + k.Act
	} else if k.Group != nil {
		// **不带列数**：用户把自己那个「方向」组改成 4 列之后，「恢复默认」不该因为
		// 认不出来又补一个同名的进去
		kind = "group"
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
	secs[profile] = lane{Rows: out.Rows, Bar: out.Bar, Pad: out.Pad}
	live := map[string]bool{}
	for _, k := range out.Lib {
		live[k.ID] = true
	}
	prune(secs, live)

	raw := make([]stored, len(out.Lib))
	for i, k := range out.Lib {
		// Wide 是给老版本看的镜像（见 Key.Wide），所以按 Span 现算，不抄 k.Wide
		raw[i] = stored{ID: k.ID, Label: k.Label, Span: k.Span, Wide: k.Span >= 2,
			Icon: k.Icon, Confirm: k.Confirm, Sticky: k.Sticky, Act: k.Act, Group: k.Group}
		if k.Sticky == "" && k.Act == "" {
			raw[i].Send = strings.TrimSpace(firstNonEmpty(k.Spec, k.Send))
		}
	}
	nf := file{Keys: raw, Profiles: secs}
	// 默认那一套镜像到顶层老字段（降级用，见 file 的注释）
	if d, ok := secs[profiles.Default]; ok {
		nf.Rows, nf.Bar, nf.Pad = d.Rows, d.Bar, d.Pad
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

// prune 把引用了已经不存在的定义的地方清干净（所有 profile 一起）。
//
// **两处**：条上（Bar）和固定块的格子（Pad.Cells）。漏掉固定块那一处的表现是那块网格上
// 留一个空洞，而且下次存盘会因为「引用了不存在的按键」直接失败 —— 而用户什么都没改。
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
		pad := ln.Pad
		if pad != nil {
			cells := make([]string, len(pad.Cells))
			any := false
			for i, k := range pad.Cells {
				if live[k] {
					cells[i] = k
					any = true
				}
			}
			// 清空了就把整块去掉（和 resolvePad 一个判据：空块 = 没有块）
			if any {
				pad = &Pad{Cols: pad.Cols, Cells: cells, Side: pad.Side}
			} else {
				pad = nil
			}
		}
		secs[id] = lane{Rows: ln.Rows, Bar: bar, Pad: pad}
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
		f.Rows, f.Bar, f.Pad = d.Rows, d.Bar, d.Pad
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

	// 弹出组里那些引用：和 Bar / Pad 同一套规矩（读盘丢掉认不出的，存盘报错）。
	// **组里不能再放组** —— 一层就够，嵌套只会变成「点开还要再点开」。
	isGroup := make(map[string]bool, len(lib))
	for _, k := range lib {
		if k.Group != nil {
			isGroup[k.ID] = true
		}
	}
	for i := range lib {
		g := lib[i].Group
		if g == nil {
			continue
		}
		cells, err := resolveCells(g.Cells, g.Cols*MaxGroupRows, seen, isGroup, dropUnknown,
			fmt.Sprintf("「%s」这个弹出组", lib[i].Label))
		if err != nil {
			return Config{}, err
		}
		lib[i].Group = &Group{Cols: g.Cols, Cells: cells}
	}

	pad, err := resolvePad(c.Pad, seen, dropUnknown)
	if err != nil {
		return Config{}, err
	}
	return Config{Rows: rows, Lib: lib, Bar: bar[:rows], Pad: pad}, nil
}

// resolveCells 收拾一片网格里的引用（固定块和弹出组共用）。
//
// 长度**一律补到 n**（不够补空、多了截掉）：格子是按位置排的，少一个就整段错位。
// noGroup 里那些是「组键」—— 传进来时表示这片网格里不许放它们（组里不能再放组）。
func resolveCells(in []string, n int, known, noGroup map[string]bool, dropUnknown bool, what string) ([]string, error) {
	cells := make([]string, n)
	for i := range cells {
		if i >= len(in) {
			break
		}
		id := strings.TrimSpace(in[i])
		if id == "" {
			continue
		}
		if !known[id] {
			if dropUnknown {
				continue
			}
			return nil, fmt.Errorf("%s第 %d 格引用了不存在的按键（%s）", what, i+1, id)
		}
		if noGroup != nil && noGroup[id] {
			if dropUnknown {
				continue
			}
			return nil, fmt.Errorf("%s第 %d 格放的又是一个弹出组 —— 组里不能再放组", what, i+1)
		}
		cells[i] = id
	}
	return cells, nil
}

// resolvePad 收拾固定块：列数、格子长度、格子里的引用。
//
// 长度**一律补到 Cols*MaxRows**（不够补空、多了截掉），而不是按当前 rows 算 —— 定义里
// 留着两行的格子，切回一行只是显示上把第二行接到后面（见 Pad 的注释），切回两行原样还在。
// 按 rows 截的话「切一行再切回两行」会把第二行的键悄悄吃掉。
//
// 引用和 Bar 同一套规矩：读盘丢掉认不出的（定义是全局的，别的设备删过），存盘报错
// （编辑器该自己清干净，静默丢就变成「保存完少了个键」）。
func resolvePad(p *Pad, known map[string]bool, dropUnknown bool) (*Pad, error) {
	if p == nil {
		return nil, nil
	}
	if p.Cols < 1 || p.Cols > MaxPadCols {
		return nil, fmt.Errorf("固定块只能是 1 到 %d 列", MaxPadCols)
	}
	side := p.Side
	if side != "left" {
		side = "right" // 空 / 认不出的一律靠右（拇指那一侧最常用）
	}
	// 固定块里**可以**放组键（那正好是「一格换一片浮窗」最省地方的用法），所以 noGroup 传 nil
	cells, err := resolveCells(p.Cells, p.Cols*MaxRows, known, nil, dropUnknown, "固定块")
	if err != nil {
		return nil, err
	}
	// 一个键都没有的固定块等于没有：别在条上留一块看不见的空地
	empty := true
	for _, c := range cells {
		if c != "" {
			empty = false
			break
		}
	}
	if empty {
		return nil, nil
	}
	return &Pad{Cols: p.Cols, Cells: cells, Side: side}, nil
}
