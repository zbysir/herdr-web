// Package topbar 管顶栏上那排图标按钮**放哪几个、什么顺序**：存在
// ~/.herdr-web/topbar.json，在网页上拖着编辑。
//
// 存服务端而不是浏览器 localStorage，和软键条同一个道理（见 internal/softkeys 的包注释）：
// 存一次就跟着人走，不用一台一台调。**但放哪几个是按 profile 分的**（见
// internal/profiles）：手机竖屏那一行放不下平板上那八个图标，所以 Load / Save 都带一个
// profile ID，一套排布一段。
//
// **为什么不塞进 softkeys.json**：那边一个 PUT 收一整份配置（rows + lib + bar 一起进出，
// 因为分几次存中间必然有个自相矛盾的状态）。顶栏混进去之后，两个编辑器就都在 PUT「一整份」
// —— 谁先谁后决定另一半是不是被清掉，而这种偏更新语义正是静默丢配置最常见的来路。两份文件
// 各自一个 PUT，谁也盖不掉谁。
//
// 存的是**一串 id**，不是按钮的定义：按钮长什么样（图标、名字、点了干什么）是前端的事，
// 服务端只负责「这个 id 认不认、顺序是什么、能不能删」。所以这里有一份白名单 ——
// 前端拿到不认识的 id 只能画一个点了没反应的按钮，那种东西不该能存进来。
// 白名单和前端那份目录必须对得上，有测试盯着（TestActionsMatchJS）。
package topbar

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/zbysir/herdr-web/internal/profiles"
)

// MaxItems 顶栏最多放几个。放不下会横滑（和手机上的软键条一样），所以这个数不是屏幕
// 定的，只是给「一次误操作塞进几千个」一个头。
const MaxItems = 24

// Actions 是全部可选按钮的 id + **编辑器里「库」的排列顺序**。
//
// 顺序不是随便排的：面板 / 文件 / 发件箱 / 软键条是四个常驻入口，接着是几个「点一下就发生
// 一件事」的动作，最后是外观和设置。库里按这个顺序摆，找起来和顶栏上的习惯一致。
var Actions = []string{
	"panes", "files", "compose", "keys",
	"kbd", "img", "clip", "paste",
	"font-", "font+", "theme", "full",
	"settings",
}

// Pinned 不能删的：**设置是唯一一条改回这份配置的路**，删掉就把自己锁在外面了
// （而且配置是跟着人走的，一台手机上删掉，电脑上也没了）。
var Pinned = []string{"settings"}

// Defaults 出厂顺序 —— 和这个功能做出来之前顶栏上写死的那一排一样，升级不改样子。
//
// 字号 ±、明暗在**手机竖屏上原来是 CSS 藏掉的**（七个图标在 393px 上排不下）。现在不藏了：
// 顶栏改成一行横滑（放不下就滑，见 App 里那排的 overflow-x-auto），而「放哪几个」本来就
// 该由人定 —— 藏起来的按钮最难解释，用户只会觉得「我明明拖上去了」。
func Defaults() []string {
	return []string{"panes", "files", "compose", "keys", "font-", "font+", "theme", "full", "settings"}
}

// Config 是**一套**顶栏配置。
type Config struct {
	Items []string `json:"items"`
}

// DefaultConfig 出厂配置。
func DefaultConfig() Config { return Config{Items: Defaults()} }

// file 是落盘的形状。
//
// Items 是**默认那一套**，同时也是「老形状」：分 profile 之前整份文件就是这一个字段。
// 新版本把每一套写进 Profiles，默认那一套照旧往 Items 也写一份 —— 降级回老版本时它只
// 认得 Items，不镜像的话降级看到的是「顶栏恢复出厂」（和 softkeys.json 同一个处理）。
type file struct {
	Items    []string          `json:"items"`
	Profiles map[string]Config `json:"profiles,omitempty"`
}

type Store struct {
	Dir string
	// 写路径是「读整份 → 改一段 → 写回」，别的 profile 那几段要原样保住；两台设备各改
	// 自己那一套是常事。见 internal/softkeys 里同一把锁的理由。
	mu sync.Mutex
}

func (s *Store) path() string { return filepath.Join(s.Dir, "topbar.json") }

func known(id string) bool {
	for _, a := range Actions {
		if a == id {
			return true
		}
	}
	return false
}

func errf(f string, a ...any) error { return fmt.Errorf(f, a...) }

// Load 读某一套顶栏。**读得宽松**：文件不在、存坏了、里面有不认识的 id，都不该让顶栏整条
// 消失（顶栏没了就等于没有「设置」那个入口，连改回来的路都没有）。
//
//   - 文件不在 / 解析失败 → 出厂配置
//   - 这一套没排过 → 退到默认那一套，再没有就出厂配置
//   - 不认识的 id（从新版本降级回来时会有）→ 丢掉那一个，剩下的照用
//   - 认不出任何一个 → 出厂配置（空栏只剩一个 ⚙，看着像坏了）
func (s *Store) Load(profile string) Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.read()
	if !ok {
		return DefaultConfig()
	}
	items, has := pick(f, profile)
	if !has {
		return DefaultConfig()
	}
	out := clean(items)
	if len(out) == 0 {
		return DefaultConfig()
	}
	return Config{Items: withPinned(out)}
}

func (s *Store) read() (file, bool) {
	b, err := os.ReadFile(s.path())
	if err != nil {
		return file{}, false
	}
	var f file
	if err := json.Unmarshal(b, &f); err != nil {
		return file{}, false
	}
	if f.Items == nil && len(f.Profiles) == 0 {
		return file{}, false
	}
	return f, true
}

// pick 挑这一套。找不到就退到默认那一套（老文件里就是顶层的 Items）。
func pick(f file, profile string) ([]string, bool) {
	if c, ok := f.Profiles[profile]; ok {
		return c.Items, true
	}
	if c, ok := f.Profiles[profiles.Default]; ok {
		return c.Items, true
	}
	if f.Items != nil {
		return f.Items, true
	}
	return nil, false
}

// sections 摊成「每套一段」，并保证默认那一套一定在里面（老文件的顶层 Items 在这儿收敛）。
// 理由和 softkeys 的 sections 一样：先建第二套会把还留在顶层的默认那一套挤掉。
func sections(f file) map[string]Config {
	out := map[string]Config{}
	for id, c := range f.Profiles {
		out[id] = c
	}
	if _, ok := out[profiles.Default]; !ok && f.Items != nil {
		out[profiles.Default] = Config{Items: f.Items}
	}
	return out
}

// Save 存之前**严格校验**：这一份是编辑器发过来的，不认识的 id / 重复项都说明前端有 bug，
// 静默修掉只会让 bug 留在那儿（Load 那边宽松是另一回事 —— 那儿面对的是旧文件和降级）。
func (s *Store) Save(profile string, c Config) (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.write(profile, c)
}

func (s *Store) write(profile string, c Config) (Config, error) {
	if len(c.Items) > MaxItems {
		return Config{}, errf("顶栏最多放 %d 个", MaxItems)
	}
	seen := map[string]bool{}
	for _, id := range c.Items {
		if !known(id) {
			return Config{}, errf("不认识的按钮：%q", id)
		}
		if seen[id] {
			return Config{}, errf("「%s」放了两次，顶栏上一个按钮只能有一个", id)
		}
		seen[id] = true
	}
	out := Config{Items: withPinned(append([]string{}, c.Items...))}
	f, _ := s.read()
	secs := sections(f)
	secs[profile] = out
	if err := s.flush(secs); err != nil {
		return Config{}, err
	}
	return out, nil
}

// Copy 把一套复制成另一套（新建 profile 时用）。
func (s *Store) Copy(from, to string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.read()
	src := Defaults()
	if ok {
		if items, has := pick(f, from); has {
			if cl := clean(items); len(cl) > 0 {
				src = withPinned(cl)
			}
		}
	}
	_, err := s.write(to, Config{Items: src})
	return err
}

// Drop 删掉一套那一段（profile 删掉之后清场）。默认那一套删不掉。
func (s *Store) Drop(profile string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if profile == profiles.Default {
		return errf("默认那一套的顶栏删不掉")
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
	return s.flush(secs)
}

// flush 落盘：每套一段 + 默认那一套镜像到顶层（降级用，见 file 的注释）。
func (s *Store) flush(secs map[string]Config) error {
	f := file{Profiles: secs}
	if d, ok := secs[profiles.Default]; ok {
		f.Items = d.Items
	} else {
		f.Items = []string{} // 顶层不留 nil：nil 会被 read 当成「这份文件不能用」
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(s.path(), append(b, '\n'), 0o600)
}

// clean 去掉不认识的 id 和重复的，保留原顺序。
func clean(items []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(items))
	for _, it := range items {
		id := strings.TrimSpace(it)
		if !known(id) || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// withPinned 把必须留着的补到末尾（Pinned 里那几个，见上面）。
func withPinned(items []string) []string {
	has := map[string]bool{}
	for _, it := range items {
		has[it] = true
	}
	for _, p := range Pinned {
		if !has[p] {
			items = append(items, p)
		}
	}
	return items
}
