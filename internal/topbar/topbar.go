// Package topbar 管顶栏上那排图标按钮**放哪几个、什么顺序**：存在
// ~/.herdr-web/topbar.json，在网页上拖着编辑。
//
// 存服务端而不是浏览器 localStorage，和软键条同一个道理（见 internal/softkeys 的包注释）：
// 手机 / 平板 / 电脑共用一份，调一次到处生效。
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

// Config 是一整份顶栏配置。
type Config struct {
	Items []string `json:"items"`
}

// DefaultConfig 出厂配置。
func DefaultConfig() Config { return Config{Items: Defaults()} }

type Store struct{ Dir string }

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

// Load 读配置。**读得宽松**：文件不在、存坏了、里面有不认识的 id，都不该让顶栏整条消失。
//
//   - 文件不在 / 解析失败 → 出厂配置
//   - 不认识的 id（从新版本降级回来时会有）→ 丢掉那一个，剩下的照用
//   - 认不出任何一个 → 出厂配置（空栏只剩一个 ⚙，看着像坏了）
func (s *Store) Load() Config {
	b, err := os.ReadFile(s.path())
	if err != nil {
		return DefaultConfig()
	}
	var f Config
	if err := json.Unmarshal(b, &f); err != nil || f.Items == nil {
		return DefaultConfig()
	}
	out := clean(f.Items)
	if len(out) == 0 {
		return DefaultConfig()
	}
	return Config{Items: withPinned(out)}
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

// Save 存之前**严格校验**：这一份是编辑器发过来的，不认识的 id / 重复项都说明前端有 bug，
// 静默修掉只会让 bug 留在那儿（Load 那边宽松是另一回事 —— 那儿面对的是旧文件和降级）。
func (s *Store) Save(c Config) (Config, error) {
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
	b, err := json.MarshalIndent(out, "", "  ")
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
