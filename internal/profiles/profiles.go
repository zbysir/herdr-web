// Package profiles 管「这台设备用哪一套排布」。
//
// 存 ~/.herdr-web/profiles.json：一个名册（有哪几套、叫什么）+ 每个浏览器绑在哪一套上。
//
// 为什么要这一层：快捷键条和顶栏存服务端是对的（手机 / 平板 / 电脑共用一份，改一次到处
// 生效，见 internal/softkeys 的包注释），但**排布**这件事恰恰是每类设备各要一份 ——
// 平板上二十个键排两行，手机竖屏上放得下五个。原来只有一份，等于平板和手机互相拆台。
//
// 分层是「这个键是什么」和「它排在哪」那条老缝再往上接一层：
//
//	全局      快捷键条的「我的按键」（键的定义）—— 改一个按键谱，所有 profile 一起变
//	profile   快捷键条的 rows/bar、顶栏的 items、几个小开关（见 Prefs）
//	设备本地  通知开关（浏览器权限绑着这一台）、面板几何、未读游标 —— 压根不进服务端
//
// 全局那一层是刻意的：每个 profile 各存一份定义的话，会长出「手机上那个 ⌃B 还是老按键
// 谱」这种最难查的毛病 —— 改完在平板上验过了，手机上没变。
//
// **绑定的键是浏览器自己生成的 installId，不是 auth 里的设备 ID**：本机直连
// （loopback / legacy 身份）压根没有设备 ID，而那正是桌面上最常见的情形。代价是清掉
// localStorage 就丢了绑定 —— 那时候按 kind 重新猜一次，人在设置里再点一下就好。
//
// **不按屏幕宽度自动切**：分屏、转屏、外接显示器都会让宽度跳变，而 profile 里装的正是
// 快捷键条这种一跳就手忙脚乱的东西。kind 只在「这个 install 第一次来、还没绑」那一下用来
// 挑个默认值，挑完就落盘，之后再也不猜。横屏 / 竖屏那条轴**不并进来**（profile 数量会
// 翻倍，而朝向当场就判得出来，不需要人选）—— 那一层在前端 lib/oriented.ts。
package profiles

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// Default 出厂那一套的 ID。**删不掉**：所有找不到归属的设备都落到它身上，
	// 而「一台设备绑着一个不存在的 profile」是静默出错的最好来路。
	Default = "default"
	// DefaultName 出厂那一套的名字。
	DefaultName = "默认"
	// MaxProfiles 名册上限。八套比「手机 / 平板 / 电脑 + 几个特例」多得多，这个数只是
	// 给「一次误操作建出几千套」一个头。
	MaxProfiles = 8
	// MaxName 名字最多几个字。要在设置面板那个下拉里一行显示得完。
	MaxName = 16
	// maxPrefLen 一个开关的值最多多长。这些值是 "1" / "0" / "12000" 这种。
	maxPrefLen = 16
	// MaxInstalls 记多少台设备。超了就把最久没来的那些忘掉 —— 这张表只是为了在设置
	// 面板里显示得懂人话（「iPhone · Safari 用的是手机那套」），不是账本。
	MaxInstalls = 32
)

// installRe：installId 是前端生成的随机串，只当 map 的键用。挡住的是「拿它去拼路径」
// 这类将来可能出现的用法，以及一眼看得出的垃圾。
var installRe = regexp.MustCompile(`^[A-Za-z0-9_-]{6,64}$`)

// Kinds 设备类别。只在「第一次来还没绑」那一下用来挑默认 profile，见包注释。
var Kinds = map[string]bool{"phone": true, "tablet": true, "desktop": true}

// Prefs 是跟着 profile 走的开关的**白名单** —— 设置面板「终端」那一页**整页**都在里面。
//
// 键名就是前端 localStorage 里的键名，一字不差：前端那侧是「服务端为准 + localStorage
// 镜像」，同名才能让原来那些读镜像的地方（有几处在终端回调里直接读 localStorage，见
// App.tsx 里的 kbdFull）一行都不用改。两边一致有测试盯着（TestPrefsMatchJS）。
//
// 通知开关（noticeOS）也在里面，尽管权限是每台设备各自的：界面上那个勾是「想要」和
// 「给了权限」两件事**与**起来画的（见 SettingsPanel），而真弹之前还要再问一次权限
// （lib/notify.ts 的 showNotify 第一行），所以同步过去不会出现「显示着开着、一条都不弹」。
//
// **不在里面的**是这些：kbdFullErr（上次全屏为什么失败，一条本机诊断）、面板的尺寸位置
// （还要按横竖屏各存一份，见前端 lib/oriented.ts）、提示的未读游标、发件箱瞄准哪个 pane，
// 以及发件箱 / 快捷键条显不显示 —— 最后这几个是随手开关的视图状态，一次会话里点十几次，
// 每点一次写一趟服务端不值当。
var Prefs = []string{
	"fontSize", "scheme",
	"kitty", "meta", "copyOnSelect", "sync2026", "switchPanel",
	"kbdFull",
	"noticeDot", "noticeCard", "noticeOS", "noticeOSFg", "noticeCardMs",
	"keyStyle", "popupClear", "holdRate",
	"diffWrap",
	"composeEnter", "composeLive",
}

// Profile 是一套排布的身份 + 它自己那几个小开关。
// 排布数据本身在各自的文件里（softkeys.json / topbar.json，按这个 ID 分段）。
type Profile struct {
	ID    string            `json:"id"`
	Name  string            `json:"name"`
	Kind  string            `json:"kind,omitempty"`  // 建它的时候那台设备是什么，见包注释
	Prefs map[string]string `json:"prefs,omitempty"` // 键在 Prefs 白名单里
}

// Install 是一个浏览器（一台设备上的一个浏览器）。Label 是从 User-Agent 猜的，
// 只求「在列表里能认出是哪台」—— 和设备面板那份是同一个函数（auth.LabelFromUA）。
type Install struct {
	Label    string    `json:"label,omitempty"`
	Kind     string    `json:"kind,omitempty"`
	Profile  string    `json:"profile"`
	LastSeen time.Time `json:"lastSeen"`
}

// Config 是一整份名册。
type Config struct {
	Profiles []Profile          `json:"profiles"`
	Installs map[string]Install `json:"installs,omitempty"`
}

// DefaultConfig 出厂：只有一套「默认」。
//
// **不预先造出「手机 / 平板 / 电脑」三套**：没人调过的三套空排布是负担不是方便 ——
// 打开设置看见三个名字，还得一个个点进去看哪个是自己在用的那个。要第二套的时候
// 「新建（复制这一套）」比从零拖快得多。
func DefaultConfig() Config {
	return Config{Profiles: []Profile{{ID: Default, Name: DefaultName}}}
}

// Get 按 ID 找一套。
func (c Config) Get(id string) (Profile, bool) {
	for _, p := range c.Profiles {
		if p.ID == id {
			return p, true
		}
	}
	return Profile{}, false
}

// Has 有没有这一套。
func (c Config) Has(id string) bool { _, ok := c.Get(id); return ok }

// resolve 这个 install 该用哪一套：绑了就用绑的（那一套还在的话），没绑就按 kind 猜
// 一个，猜不着落到默认。**只在 Hello 里落盘一次**，别的地方只读。
func (c Config) resolve(install, kind string) string {
	if in, ok := c.Installs[install]; ok && in.Profile != "" && c.Has(in.Profile) {
		return in.Profile
	}
	if Kinds[kind] {
		for _, p := range c.Profiles {
			if p.Kind == kind {
				return p.ID
			}
		}
	}
	return Default
}

type Store struct {
	Dir string
	// 每个写操作都是「读整份 → 改一处 → 写回」，而两台设备同时在设置面板里点是常事
	// （改名 + 另一台绑过来）。没有这把锁就是「谁后写谁把对方那一下吃掉」。
	mu sync.Mutex
}

func (s *Store) path() string { return filepath.Join(s.Dir, "profiles.json") }

func errf(f string, a ...any) error { return fmt.Errorf(f, a...) }

// Load 读名册。**读得宽松**：文件不在、存坏了、里面有说不通的东西，都不该让设置面板
// 整块打不开 —— 那时候连改回来的路都没有了。
//
//   - 文件不在 / 解析失败 → 出厂（只有「默认」一套）
//   - 名字空的 / 重复 ID / 认不出的 kind → 修掉那一处，别的照用
//   - 没有 default 那一套 → 补一个（所有兜底都落在它身上）
func (s *Store) Load() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

func (s *Store) load() Config {
	b, err := os.ReadFile(s.path())
	if err != nil {
		return DefaultConfig()
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return DefaultConfig()
	}
	return clean(c)
}

// clean 把一份读进来的名册收拾干净（宽松那一侧的规则都在这儿）。
func clean(c Config) Config {
	seen := map[string]bool{}
	out := make([]Profile, 0, len(c.Profiles))
	for _, p := range c.Profiles {
		p.ID = strings.TrimSpace(p.ID)
		p.Name = trimName(p.Name)
		if p.ID == "" || seen[p.ID] || len(out) >= MaxProfiles {
			continue
		}
		if p.Name == "" {
			p.Name = p.ID // 名字丢了也得能在下拉里点到它
		}
		if !Kinds[p.Kind] {
			p.Kind = ""
		}
		p.Prefs = cleanPrefs(p.Prefs)
		seen[p.ID] = true
		out = append(out, p)
	}
	if !seen[Default] {
		// 兜底那一套必须在，而且放在最前面（下拉里第一个就是「默认」）
		out = append([]Profile{{ID: Default, Name: DefaultName}}, out...)
		if len(out) > MaxProfiles {
			out = out[:MaxProfiles]
		}
	}
	res := Config{Profiles: out}
	if len(c.Installs) > 0 {
		res.Installs = map[string]Install{}
		for id, in := range c.Installs {
			if !installRe.MatchString(id) {
				continue
			}
			if !Kinds[in.Kind] {
				in.Kind = ""
			}
			// 指向已经删掉的那一套 = 落到默认（不清空，清空就分不出「没绑过」了）
			if in.Profile == "" || !seenHas(out, in.Profile) {
				in.Profile = Default
			}
			in.Label = trimLabel(in.Label)
			res.Installs[id] = in
		}
	}
	return res
}

func seenHas(ps []Profile, id string) bool {
	for _, p := range ps {
		if p.ID == id {
			return true
		}
	}
	return false
}

// trimName 名字收一刀：去掉控制字符（快捷键条那边吃过一次，标签里带 \n 会把布局撑坏）、
// 掐到 MaxName 个字。
func trimName(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' || unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	if utf8.RuneCountInString(s) > MaxName {
		rs := []rune(s)
		s = string(rs[:MaxName])
	}
	return strings.TrimSpace(s)
}

func trimLabel(s string) string {
	s = trimName(s)
	if utf8.RuneCountInString(s) > 32 {
		return string([]rune(s)[:32])
	}
	return s
}

func cleanPrefs(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := map[string]string{}
	for _, k := range Prefs {
		v, ok := in[k]
		if !ok || len(v) > maxPrefLen {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (s *Store) save(c Config) (Config, error) {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return Config{}, err
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return Config{}, err
	}
	if err := os.WriteFile(s.path(), append(b, '\n'), 0o600); err != nil {
		return Config{}, err
	}
	return c, nil
}

/* ------------------------------------------------------------------ 操作 */

// Resolve 这个 install 用哪一套（只读，不落盘）。认不出就是默认那一套 ——
// 快捷键条 / 顶栏的 GET 每次都要过这儿，不该顺手写盘。
func (s *Store) Resolve(install string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load().resolve(install, "")
}

// Hello 是页面一打开报到那一下：记住这台设备（Label / LastSeen），没绑过就按 kind
// 挑一套绑上。返回收拾好的名册和**这台设备该用的那一套**。
//
// 为什么是 POST 而不是让 GET 顺手绑：绑定是写操作，藏在 GET 里的写会让「curl 看一眼」
// 变成「curl 改了配置」。install 不合法就什么都不写，直接给默认那一套 —— 这条路上
// 不该出现「因为存储坏了所以整页打不开」。
func (s *Store) Hello(install, kind, label string) (Config, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.load()
	if !installRe.MatchString(install) {
		return c, Default
	}
	if !Kinds[kind] {
		kind = ""
	}
	id := c.resolve(install, kind)
	if c.Installs == nil {
		c.Installs = map[string]Install{}
	}
	prev := c.Installs[install]
	if label == "" {
		label = prev.Label
	}
	c.Installs[install] = Install{Label: trimLabel(label), Kind: kind, Profile: id, LastSeen: time.Now()}
	forget(c.Installs, install)
	out, err := s.save(c)
	if err != nil {
		// 写不进盘不该让页面打不开：这一趟就当没记住（下次再报到），排布照样给
		return c, id
	}
	return out, id
}

// forget 表满了就把最久没来的忘掉，但**永远留着刚报到的那一台**。
func forget(m map[string]Install, keep string) {
	if len(m) <= MaxInstalls {
		return
	}
	ids := make([]string, 0, len(m))
	for id := range m {
		if id != keep {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return m[ids[i]].LastSeen.Before(m[ids[j]].LastSeen) })
	for _, id := range ids {
		if len(m) <= MaxInstalls {
			return
		}
		delete(m, id)
	}
}

// Create 建一套新的。名字重复直接拒 —— 下拉里两个「手机」是分不出来的。
func (s *Store) Create(name, kind string) (Config, Profile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.load()
	if len(c.Profiles) >= MaxProfiles {
		return c, Profile{}, errf("最多 %d 套排布", MaxProfiles)
	}
	name = trimName(name)
	if name == "" {
		return c, Profile{}, errf("给它起个名字")
	}
	for _, p := range c.Profiles {
		if p.Name == name {
			return c, Profile{}, errf("已经有一套叫「%s」了", name)
		}
	}
	if !Kinds[kind] {
		kind = ""
	}
	p := Profile{ID: newID(c.Profiles), Name: name, Kind: kind}
	c.Profiles = append(c.Profiles, p)
	out, err := s.save(c)
	if err != nil {
		return c, Profile{}, err
	}
	return out, p, nil
}

// newID 发 ID：default、p2、p3……跳过用掉的号。
// 用递增的小字符串而不是随机串，理由和快捷键条的 k1/k2 一样：这几份 JSON 是人会去看、
// 偶尔手改的，`"profiles": {"p2": …}` 一眼对得上是哪一套。
func newID(ps []Profile) string {
	used := map[string]bool{}
	for _, p := range ps {
		used[p.ID] = true
	}
	if !used[Default] {
		return Default
	}
	for n := 2; ; n++ {
		id := fmt.Sprintf("p%d", n)
		if !used[id] {
			return id
		}
	}
}

// CopyPrefs 把一套的开关复制给另一套（新建时用）。
//
// 排布复制了、开关不复制的话，「从平板复制一份」出来的那一套字号会是空的 —— 而人刚刚就是
// 照着平板那一套的样子建的它。from 没有开关就什么都不做。
func (s *Store) CopyPrefs(from, to string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.load()
	src, ok := c.Get(from)
	if !ok {
		return errf("没有这一套排布（%s）", from)
	}
	if len(src.Prefs) == 0 {
		return nil
	}
	for i, p := range c.Profiles {
		if p.ID != to {
			continue
		}
		cp := make(map[string]string, len(src.Prefs))
		for k, v := range src.Prefs {
			cp[k] = v
		}
		c.Profiles[i].Prefs = cp
		_, err := s.save(c)
		return err
	}
	return errf("没有这一套排布（%s）", to)
}

// Rename 改名。
func (s *Store) Rename(id, name string) (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.load()
	name = trimName(name)
	if name == "" {
		return c, errf("名字不能空着")
	}
	found := false
	for i, p := range c.Profiles {
		if p.ID == id {
			c.Profiles[i].Name = name
			found = true
			continue
		}
		if p.Name == name {
			return c, errf("已经有一套叫「%s」了", name)
		}
	}
	if !found {
		return c, errf("没有这一套排布（%s）", id)
	}
	return s.save(c)
}

// Delete 删一套。绑在它上面的设备落回默认 —— 绑着一个不存在的 profile 是静默出错的
// 最好来路（下次报到时拿到的排布是默认那套，而界面上还写着老名字）。
//
// 排布数据（softkeys.json / topbar.json 里那一段）不在这个包里，调用方删完这儿再去
// 清那两份（见 server 的 apiProfiles）。
func (s *Store) Delete(id string) (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.load()
	if id == Default {
		return c, errf("「%s」那一套删不掉：所有没绑过的设备都落在它身上", DefaultName)
	}
	if !c.Has(id) {
		return c, errf("没有这一套排布（%s）", id)
	}
	out := make([]Profile, 0, len(c.Profiles))
	for _, p := range c.Profiles {
		if p.ID != id {
			out = append(out, p)
		}
	}
	c.Profiles = out
	for k, in := range c.Installs {
		if in.Profile == id {
			in.Profile = Default
			c.Installs[k] = in
		}
	}
	return s.save(c)
}

// Bind 把某台设备（installId）绑到某一套上。
//
// 允许绑**别人**：在平板上就能把手机那台改回去 —— 手机上排布调坏了的时候，那台设备
// 自己反而是最难操作的一台。
func (s *Store) Bind(install, profile string) (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.load()
	if !installRe.MatchString(install) {
		return c, errf("设备标识不对")
	}
	if !c.Has(profile) {
		return c, errf("没有这一套排布（%s）", profile)
	}
	if c.Installs == nil {
		c.Installs = map[string]Install{}
	}
	in := c.Installs[install]
	in.Profile = profile
	if in.LastSeen.IsZero() {
		in.LastSeen = time.Now()
	}
	c.Installs[install] = in
	return s.save(c)
}

// SetPrefs 合并几个开关（**只动传进来的那几个键**，不是整份替换）。
//
// 合并而不是替换：同一套排布可能有两台设备在用，一台改字号、另一台改提示卡停留，整份
// 替换就是「谁后存谁把对方那一下清掉」。白名单外的键直接拒，别静默丢 —— 前端拼错了
// 键名的表现会是「点了没反应，也没报错」。
func (s *Store) SetPrefs(id string, kv map[string]string) (Profile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.load()
	idx := -1
	for i, p := range c.Profiles {
		if p.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return Profile{}, errf("没有这一套排布（%s）", id)
	}
	allowed := map[string]bool{}
	for _, k := range Prefs {
		allowed[k] = true
	}
	cur := c.Profiles[idx].Prefs
	if cur == nil {
		cur = map[string]string{}
	}
	for k, v := range kv {
		if !allowed[k] {
			return Profile{}, errf("不认识这个设置项：%q", k)
		}
		if len(v) > maxPrefLen {
			return Profile{}, errf("%s 的值太长了", k)
		}
		cur[k] = v
	}
	c.Profiles[idx].Prefs = cur
	if _, err := s.save(c); err != nil {
		return Profile{}, err
	}
	return c.Profiles[idx], nil
}

// ValidInstall 给上层用：这串 installId 收不收。
func ValidInstall(s string) bool { return installRe.MatchString(s) }
