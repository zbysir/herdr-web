// Package capability 是「这个版本能做哪几件事」的**唯一一份清单**。
//
// 以前这份清单散在三处，而它们其实是同一件事的三个切面：
//
//	topbar.Actions / topbar.Pinned   顶栏上能放哪几个、哪个删不掉
//	softkeys 的 acts                  快捷键条按键的 act 白名单（是上面那份的**子集**）
//	web/.../topbarItems.tsx           按钮长什么样 + 那个 TS 联合类型
//
// 散着的代价是加一件事要动四处 + 两个测试，而漏一处的表现都是**静默**的：顶栏上画一个点了
// 没反应的按钮，或者编辑器里拖得上去、一存报「不认识」。更要紧的是「act 是顶栏 id 的子集」
// 这条关系原来只写在注释里 —— 只在 softkeys 那边加一个 act，前端拿它去查顶栏那张动作表就是
// 查不到，键点下去什么都不发生，没有任何报错。
//
// 现在一件事一行，**能出现在哪些界面上是这一行里的字段**：
//
//	Topbar  能放到顶栏上（顺带：这个切片的顺序 = 顶栏编辑器里「库」的排列顺序）
//	Key     能当快捷键条按键的 act
//	Panel   点开是一块浮层（前端据此推出 panel 那个状态的类型）
//	Pinned  顶栏上删不掉
//
// 前端那一半（图标、名字、一句说明）在 `web/src/capabilities.tsx` —— 那些是
// React 节点，搬不到 Go 来。两边靠 id 对上，**顺序和 Key 标记必须一字不差**，有测试盯着
// （internal/capability 的 TestMatchJS）。
//
// 加一件事 = 这儿一行 + 那边一行。以后子进程插件（见 CLAUDE.md 的路线）就是往这张表里
// 运行时再塞几行，顶栏 / 快捷键条 / 两个编辑器全都白拿到。
package capability

// Cap 是一件能做的事。
type Cap struct {
	ID string
	// Topbar 能放到顶栏上。目前全部都能 —— 留这个字段是因为「只能放在快捷键条上」这种东西迟早会
	// 有（子进程插件里那些不该占顶栏那一行的），到时候不用再改一次结构。
	Topbar bool
	Key    bool // 能当快捷键条按键的 act
	Panel  bool // 点开是一块浮层（面板一览 / 文件 / 设置）
	Pinned bool // 顶栏上删不掉
}

// All 是全部能做的事 + **顶栏编辑器里「库」的排列顺序**。
//
// 顺序不是随便排的：面板 / 文件 / 发件箱 / 快捷键条是四个常驻入口，接着是几个「点一下就发生
// 一件事」的动作，最后是外观和设置。库里按这个顺序摆，找起来和顶栏上的习惯一致。
var All = []Cap{
	{ID: "panes", Topbar: true, Key: true, Panel: true},
	{ID: "files", Topbar: true, Key: true, Panel: true},
	{ID: "diff", Topbar: true, Key: true, Panel: true},
	{ID: "compose", Topbar: true},
	{ID: "keys", Topbar: true},
	{ID: "kbd", Topbar: true, Key: true},
	{ID: "img", Topbar: true, Key: true},
	{ID: "clip", Topbar: true, Key: true},
	{ID: "paste", Topbar: true, Key: true},
	{ID: "pull", Topbar: true, Key: true},
	{ID: "font-", Topbar: true},
	{ID: "font+", Topbar: true},
	{ID: "theme", Topbar: true},
	{ID: "full", Topbar: true},
	// 设置**删不掉**：它是唯一一条改回这份配置的路，而配置是跟着人走的（存服务端）——
	// 在手机上删掉，电脑上也就没了。
	{ID: "settings", Topbar: true, Panel: true, Pinned: true},
}

// TopbarIDs 顶栏上能放的那些 id，**按库的顺序**。
func TopbarIDs() []string {
	out := make([]string, 0, len(All))
	for _, c := range All {
		if c.Topbar {
			out = append(out, c.ID)
		}
	}
	return out
}

// TopbarPinned 顶栏上删不掉的那几个。
func TopbarPinned() []string {
	out := []string{}
	for _, c := range All {
		if c.Pinned {
			out = append(out, c.ID)
		}
	}
	return out
}

// KeyActs 能当快捷键条 act 的那些 id，按同一个顺序。
func KeyActs() []string {
	out := make([]string, 0, len(All))
	for _, c := range All {
		if c.Key {
			out = append(out, c.ID)
		}
	}
	return out
}

// IsKeyAct 这个 act 认不认。
func IsKeyAct(id string) bool {
	for _, c := range All {
		if c.ID == id {
			return c.Key
		}
	}
	return false
}

// OnTopbar 这个 id 能不能放顶栏。
func OnTopbar(id string) bool {
	for _, c := range All {
		if c.ID == id {
			return c.Topbar
		}
	}
	return false
}
