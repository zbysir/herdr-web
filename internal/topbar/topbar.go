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
//
// # 「我的按键」也能上顶栏
//
// items 里除了内置按钮的 id，还能放 `key:<定义ID>` —— 指向软键条那份「我的按键」
// （internal/softkeys 的 Lib）。于是「顶栏上能不能加个 ctrl+b z」不再是每次都要动一遍
// 白名单的事：**动作库只有一份，顶栏和软键条是它的两个界面**。
//
// 引用和内置 id 有两处不对称，都是有意的：
//
//   - **存盘严格、读盘不核**：Save 会拿 Keys 钩子核一遍「这个定义还在不在」，Load
//     **故意不核** —— 核的话一次 softkeys.json 读失败就能把人家配好的键从顶栏上抹掉，
//     而读失败是暂时的、配置不是。认不出的引用交给前端渲染时丢掉（和软键条那边
//     resolveBar 一个做法）。
//   - **删定义要顺带清引用**：那一步在软键条那个口存完之后调 PruneKeys（见
//     internal/server 的 apiSoftkeys）。不清的表现是顶栏上留一个画不出来的幽灵项，
//     占着 MaxItems 的名额却看不见，下次打开编辑器它又静悄悄消失。
package topbar

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/zbysir/herdr-web/internal/capability"
	"github.com/zbysir/herdr-web/internal/profiles"
)

// MaxItems 顶栏最多放几个。放不下会横滑（和手机上的软键条一样），所以这个数不是屏幕
// 定的，只是给「一次误操作塞进几千个」一个头。
const MaxItems = 24

// Actions 是全部可选按钮的 id + **编辑器里「库」的排列顺序**。
//
// 清单本身不在这儿：它和软键条的 act 白名单、前端那份按钮目录是同一件事的三个切面，
// 合在 internal/capability 里（为什么合、散着会怎么静默出错，见那个包的注释）。
var Actions = capability.TopbarIDs()

// Pinned 不能删的：**设置是唯一一条改回这份配置的路**，删掉就把自己锁在外面了
// （而且配置是跟着人走的，一台手机上删掉，电脑上也没了）。
var Pinned = capability.TopbarPinned()

// Defaults 出厂顺序 —— 和这个功能做出来之前顶栏上写死的那一排一样，升级不改样子。
//
// 字号 ±、明暗在**手机竖屏上原来是 CSS 藏掉的**（七个图标在 393px 上排不下）。现在不藏了：
// 顶栏改成一行横滑（放不下就滑，见 App 里那排的 overflow-x-auto），而「放哪几个」本来就
// 该由人定 —— 藏起来的按钮最难解释，用户只会觉得「我明明拖上去了」。
func Defaults() []string {
	return []string{"panes", "files", "compose", "keys", "font-", "font+", "theme", "full", "settings"}
}

// KeyPrefix 是「这一项是引用，不是内置按钮」的记号：`key:k3` 指向软键条那份「我的按键」
// 里 ID 为 k3 的那个定义（见 internal/softkeys）。
//
// 用前缀而不是另开一个平行数组：items 是一串字符串，而**顺序在这儿就是全部意义** ——
// 多一个数组就得处理「两边长度不一样」。冒号在内置 id 里不可能出现（那些是 [a-z+-]），
// 所以两种形态一眼分得开，也撞不上。
const KeyPrefix = "key:"

// keyID 是引用里那个定义 ID 的合法形状。软键条自己发的是 k1 / k2……，但 ID 是从客户端
// 原样收下来的（见 softkeys 里 newIDs 那段），所以这儿自己挡一道：长度有头、不带冒号和
// 空白 —— 免得一个畸形 ID 变成一个畸形 item，再顺着 JSON 一路传到前端。
var keyID = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// KeyRef 认「这一项是不是一个引用」，并给出被引用的定义 ID。
func KeyRef(item string) (string, bool) {
	id, ok := strings.CutPrefix(item, KeyPrefix)
	if !ok || !keyID.MatchString(id) {
		return "", false
	}
	return id, true
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

	// Keys 报「『我的按键』现在有哪些定义 ID」—— 存盘时核 `key:` 引用用的。
	//
	// 是个钩子而不是直接 import softkeys：那两份配置是**两个文件两个口**（见包注释），
	// 一边直接抓另一边的 Store 就等于把这条边界拆了。nil = 不核，引用只查形状 ——
	// 单测和降级路径走这条。
	Keys func() map[string]bool

	// 写路径是「读整份 → 改一段 → 写回」，别的 profile 那几段要原样保住；两台设备各改
	// 自己那一套是常事。见 internal/softkeys 里同一把锁的理由。
	mu sync.Mutex
}

func (s *Store) path() string { return filepath.Join(s.Dir, "topbar.json") }

func known(id string) bool { return capability.OnTopbar(id) }

// itemOK 「这一项能不能存进来」：内置 id 查白名单，`key:` 引用查形状 + 定义还在不在。
// keys 为 nil 时只查形状（见 Store.Keys）。
func itemOK(item string, keys map[string]bool) error {
	if known(item) {
		return nil
	}
	id, ok := KeyRef(item)
	if !ok {
		return errf("不认识的按钮：%q", item)
	}
	if keys != nil && !keys[id] {
		return errf("「我的按键」里已经没有 %q 了 —— 可能在别的设备上删掉了，重开一下设置", id)
	}
	return nil
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
	var keys map[string]bool
	if s.Keys != nil {
		keys = s.Keys()
	}
	seen := map[string]bool{}
	for _, id := range c.Items {
		if err := itemOK(id, keys); err != nil {
			return Config{}, err
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

// PruneKeys 把**所有套**里指向「已经不在了的定义」的引用清掉。软键条那个口存完之后调
// （见 internal/server 的 apiSoftkeys）—— 定义是全局的，一台设备上删一个键，别的套条上
// 和顶栏上的引用都得跟着走，和 softkeys.Save 里 prune 条上引用是同一件事。
//
// keep 是**留下来的**定义 ID。没有一处要改就不写盘（这个口每存一次软键条都会被调一遍）。
func (s *Store) PruneKeys(keep map[string]bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.read()
	if !ok {
		return nil
	}
	secs := sections(f)
	changed := false
	for id, c := range secs {
		out := make([]string, 0, len(c.Items))
		hit := false
		for _, it := range c.Items {
			if kid, ref := KeyRef(it); ref && !keep[kid] {
				hit = true
				continue
			}
			out = append(out, it)
		}
		if hit {
			secs[id] = Config{Items: withPinned(out)}
			changed = true
		}
	}
	if !changed {
		return nil
	}
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
//
// 形状对的 `key:` 引用一律**留着**，哪怕那个定义此刻不在了 —— 这儿是读路径，不核存在性
// （理由见包注释）。指到空处的引用由前端渲染时丢掉，被删掉的定义由 PruneKeys 清。
func clean(items []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(items))
	for _, it := range items {
		id := strings.TrimSpace(it)
		if seen[id] {
			continue
		}
		if _, ref := KeyRef(id); !ref && !known(id) {
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
