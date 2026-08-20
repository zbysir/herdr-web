// Package softkeys 管软键条的配置：存在 ~/.herdr-web/softkeys.json，在网页上编辑。
//
// 存服务端而不是浏览器 localStorage，是为了手机 / 平板 / 电脑共用一份 —— 和 token
// 落盘同一个道理，改一次到处生效。
//
// 每个按键三种形态之一：
//
//	{send: "<按键谱>"}  按一下发一串字节
//	{sticky: "ctrl"}    粘滞修饰键（点亮之后下一个字母组合成 ctrl+x）
//	{act: "kbd"}        显示 / 收起系统键盘
//
// 另外每个按键可以打上 {confirm: true}：第一下只是「举起来」，第二下才真发出去。
// 给关 pane / 关标签 / 断开这种误触代价很大的键用。
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

const MaxKeys = 40

// Key 是一个按键。Spec/Send 只在 send 形态下有值。
type Key struct {
	Label   string `json:"label"`
	Wide    bool   `json:"wide,omitempty"`
	Confirm bool   `json:"confirm,omitempty"` // 要点两下才发（防误触）
	Send    string `json:"send,omitempty"`    // 解析出来的字节（下发给前端）
	Spec    string `json:"spec,omitempty"`    // 用户写的按键谱（回显到编辑器）
	Sticky  string `json:"sticky,omitempty"`  // ctrl | alt
	Act     string `json:"act,omitempty"`     // kbd
}

// stored 是落盘的形状：只存用户写的东西，不存解析结果。
type stored struct {
	Label   string `json:"label"`
	Wide    bool   `json:"wide,omitempty"`
	Confirm bool   `json:"confirm,omitempty"`
	Send    string `json:"send,omitempty"`
	Sticky  string `json:"sticky,omitempty"`
	Act     string `json:"act,omitempty"`
}

type file struct {
	Keys []stored `json:"keys"`
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
	out := Key{Label: label, Wide: k.Wide, Confirm: k.Confirm}
	switch {
	case k.Sticky != "":
		if k.Sticky != "ctrl" && k.Sticky != "alt" {
			return Key{}, fmt.Errorf("%s 的 sticky 只能是 ctrl 或 alt", at)
		}
		out.Sticky = k.Sticky
	case k.Act != "":
		if k.Act != "kbd" {
			return Key{}, fmt.Errorf("%s 的 act 目前只支持 kbd", at)
		}
		out.Act = "kbd"
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

// Load 读配置；文件不存在或存坏了都退回出厂配置。
func (s *Store) Load() []Key {
	b, err := os.ReadFile(s.path())
	if err != nil {
		out, _ := Resolve(Defaults())
		return out
	}
	var f file
	if err := json.Unmarshal(b, &f); err != nil || f.Keys == nil {
		out, _ := Resolve(Defaults())
		return out
	}
	keys := make([]Key, len(f.Keys))
	for i, k := range f.Keys {
		keys[i] = Key{Label: k.Label, Wide: k.Wide, Confirm: k.Confirm, Send: k.Send, Sticky: k.Sticky, Act: k.Act}
	}
	out, err := Resolve(keys)
	if err != nil {
		out, _ = Resolve(Defaults())
	}
	return out
}

// Save 先全部校验通过再落盘。
func (s *Store) Save(keys []Key) ([]Key, error) {
	if len(keys) > MaxKeys {
		return nil, fmt.Errorf("最多 %d 个按键", MaxKeys)
	}
	out, err := Resolve(keys)
	if err != nil {
		return nil, err
	}
	raw := make([]stored, len(keys))
	for i, k := range keys {
		raw[i] = stored{Label: strings.TrimSpace(k.Label), Wide: k.Wide, Confirm: k.Confirm, Sticky: k.Sticky, Act: k.Act}
		if k.Sticky == "" && k.Act == "" {
			raw[i].Send = strings.TrimSpace(firstNonEmpty(k.Send, k.Spec))
		}
	}
	b, err := json.MarshalIndent(file{Keys: raw}, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(s.path(), append(b, '\n'), 0o600); err != nil {
		return nil, err
	}
	return out, nil
}
