package softkeys

// 本文件由 lib/softkeys.js 的 DEFAULTS / PRESETS 生成 —— 51 条按键谱手抄进 Go
// 太容易抄错。testdata/js-snapshot.json 里存着当时的快照，softkeys_test.go 会
// 逐条比对，保证没抄漏抄错。
//
// 快照只锁「迁移那一刻」那 6 组；后面新加的组直接往下写，不进快照。

// Defaults 出厂那些**定义**（「我的按键」里有哪些）—— 最早写死在 index.html 里的那一排。
//
// **它不等于「条上放哪几个」**（那是 DefaultBar）：方向键那四个还是定义，但在条上只占
// 一格 —— 它们是「方向」那个弹出组的成员。
func Defaults() []Key {
	return []Key{
		// 出厂就挑好图标的那几个：`⌨` / `↵` 这类字形在很多字体里缺（显示成方框）或者很难看。
		// **Label 一个字都没动** —— sigOf 认它（「恢复默认」的去重）、快照测试也比它。
		{Label: "⌨", Icon: "keyboard", Act: "kbd"},
		{Label: "⌃B 前缀", Wide: true, Send: "ctrl+b"},
		{Label: "Ctrl", Sticky: "ctrl"},
		{Label: "Alt", Sticky: "alt"},
		{Label: "Esc", Send: "esc"},
		{Label: "Tab", Send: "tab"},
		{Label: "↑", Send: "up"},
		{Label: "↓", Send: "down"},
		{Label: "←", Send: "left"},
		{Label: "→", Send: "right"},
		{Label: "PgUp", Send: "pgup"},
		{Label: "PgDn", Send: "pgdn"},
		{Label: "⌃C", Send: "ctrl+c"},
		{Label: "↵", Icon: "enter", Send: "enter"},
	}
}

// defaultArrows 出厂那个「方向」弹出组：条上只占一格，点开是
//
//	·  ↑  ·
//	←  ↓  →
//
// 出厂就是弹出组而不是四个键摊在条上：摊开要 3×2 六格，393px 的手机竖屏上那是半条屏幕。
//
// Cells 里写的是**按键本身**（靠 sigOf 去 lib 里找对应那条），不是 ID —— ID 是建配置时才发的。
// 空的 Key 就是空格子（方向键盘上方那两个空位靠它占出来）。
var defaultArrows = struct {
	Label string
	Cols  int
	Cells []Key
}{
	Label: "方向",
	Cols:  3,
	Cells: []Key{
		{}, {Label: "↑", Send: "up"}, {},
		{Label: "←", Send: "left"}, {Label: "↓", Send: "down"}, {Label: "→", Send: "right"},
	},
}

// arrowGroupKey 「方向」那个组键本身（不带格子 —— 格子由 wireDefaults 填 ID）。
// sigOf 认的是「名字 + 是个组」，所以拿它去 lib 里查「有没有」是够的。
func arrowGroupKey() Key {
	return Key{Label: defaultArrows.Label, Icon: "dpad", Group: &Group{Cols: defaultArrows.Cols}}
}

// isDefaultArrow 这个键是「方向」组的成员吗（出厂那四个方向键）。
func isDefaultArrow(k Key) bool {
	switch k.Send {
	case "up", "down", "left", "right":
		return k.Sticky == "" && k.Act == "" && k.Group == nil
	}
	return false
}

// DefaultBar 出厂**条上**放哪几个，顺序就是条上的顺序。
//
// 和 Defaults() 的差别只有一处：方向键那四个不各占一格，换成「方向」那一个组键，
// 摆在它们原来的位置上。
func DefaultBar() []Key {
	out := make([]Key, 0, len(Defaults()))
	put := false
	for _, k := range Defaults() {
		if isDefaultArrow(k) {
			if !put {
				out = append(out, arrowGroupKey())
				put = true
			}
			continue
		}
		out = append(out, k)
	}
	return out
}

// Presets 编辑器「常用」下拉。按键谱抄的是 `herdr --default-config` 的 [keys]
// 默认值，改过 keybinding 的人得自己手输 —— 下拉只是省事，不是全部。
//
// 关 pane / 关标签 / 关工作区 / 断开 / /clear 预先打了 Confirm：软键条上的键挨得近，
// 平板上误触一下就没了，而这几个都是不可撤销的。不想要两下的自己在编辑器里取消勾选。
//
// 注意 prefix+shift+x 这类要写成 "ctrl+b X"（大写字母就是 shift），
// prefix+minus 写成 "ctrl+b -"。
func Presets() []PresetGroup {
	return []PresetGroup{
		{Group: "前缀 / 通用", Items: []Key{
			{Label: "⌃B 前缀", Wide: true, Send: "ctrl+b"},
			{Label: "帮助", Send: "ctrl+b ?"},
			{Label: "设置", Send: "ctrl+b s"},
			{Label: "侧边栏", Send: "ctrl+b b"},
			{Label: "跳转", Send: "ctrl+b g"},
			{Label: "断开", Send: "ctrl+b q", Confirm: true},
		}},
		{Group: "标签", Items: []Key{
			{Label: "新标签", Send: "ctrl+b c"},
			{Label: "下个标签", Send: "ctrl+b n"},
			{Label: "上个标签", Send: "ctrl+b p"},
			{Label: "关标签", Send: "ctrl+b X", Confirm: true},
			{Label: "标签 1", Send: "ctrl+b 1"},
			{Label: "标签 2", Send: "ctrl+b 2"},
			{Label: "标签 3", Send: "ctrl+b 3"},
		}},
		{Group: "Pane", Items: []Key{
			{Label: "竖分屏", Send: "ctrl+b v"},
			{Label: "横分屏", Send: "ctrl+b -"},
			{Label: "关 pane", Send: "ctrl+b x", Confirm: true},
			{Label: "放大", Send: "ctrl+b z"},
			{Label: "下个 pane", Send: "ctrl+b tab"},
			{Label: "pane ←", Send: "ctrl+b h"},
			{Label: "pane ↓", Send: "ctrl+b j"},
			{Label: "pane ↑", Send: "ctrl+b k"},
			{Label: "pane →", Send: "ctrl+b l"},
			{Label: "调大小", Send: "ctrl+b r"},
			{Label: "改名", Send: "ctrl+b P"},
		}},
		{Group: "工作区", Items: []Key{
			{Label: "工作区", Send: "ctrl+b w"},
			{Label: "新工作区", Send: "ctrl+b N"},
			{Label: "改名", Send: "ctrl+b W"},
			{Label: "关工作区", Send: "ctrl+b D", Confirm: true},
		}},
		{Group: "终端按键", Items: []Key{
			{Label: "Esc", Send: "esc"},
			{Label: "Tab", Send: "tab"},
			{Label: "↵", Send: "enter"},
			{Label: "↑", Send: "up"},
			{Label: "↓", Send: "down"},
			{Label: "←", Send: "left"},
			{Label: "→", Send: "right"},
			{Label: "PgUp", Send: "pgup"},
			{Label: "PgDn", Send: "pgdn"},
			{Label: "Home", Send: "home"},
			{Label: "End", Send: "end"},
			{Label: "⌃C", Send: "ctrl+c"},
			{Label: "⌃D", Send: "ctrl+d"},
			{Label: "⌃L 清屏", Send: "ctrl+l"},
			{Label: "⌃U 清行", Send: "ctrl+u"},
			{Label: "⌃R 搜索", Send: "ctrl+r"},
			{Label: "⌃Z", Send: "ctrl+z"},
			{Label: "Shift+Tab", Send: "shift+tab"},
		}},
		{Group: "特殊 / 文本", Items: []Key{
			{Label: "⌨ 键盘", Act: "kbd"},
			{Label: "Ctrl", Sticky: "ctrl"},
			{Label: "Alt", Sticky: "alt"},
			{Label: "敲 herdr", Wide: true, Send: "\"herdr\" enter"},
			{Label: "git status", Wide: true, Send: "\"git status\" enter"},
		}},
		// Claude Code 的斜杠命令。这些不是按键，是往输入框里打字，所以走 text:。
		// 都带 enter 是为了一下点完 —— 命令补全菜单里已经是唯一匹配，回车直接执行；
		// 万一你的版本要按两下，自己在后面再加一个 enter。
		//
		// 只挑了平板上真会想一键点的：清上下文、看用量、换模型这种。全量命令太多，
		// 打字更快的那些没必要占软键条。
		{Group: "Claude 命令", Items: []Key{
			{Label: "/new", Send: "text:/new enter"},
			{Label: "/clear", Send: "text:/clear enter", Confirm: true},
			{Label: "/compact", Wide: true, Send: "text:/compact enter"},
			{Label: "/usage", Send: "text:/usage enter"},
			{Label: "/context", Wide: true, Send: "text:/context enter"},
			{Label: "/model", Send: "text:/model enter"},
			{Label: "/resume", Send: "text:/resume enter"},
			{Label: "/cost", Send: "text:/cost enter"},
		}},
		// 这几个不发字节，是网页端自己处理的动作 —— 传图要弹相机 / 相册，键盘要动隐藏
		// textarea 的焦点，终端那边都无从代劳。「面板一览」也一样：按键只能表达「下一个
		// tab」这种相对导航，说不出「让 w5:p3 全屏」，那条路只有 socket 走得通。
		//
		// 「面板一览」放在软键条上是有讲究的：手机上键盘一弹起来顶栏整段就收掉了（那时候
		// 那个入口点不到），而软键条正好在拇指底下。
		{Group: "网页端动作", Items: []Key{
			{Label: "🖼 传图", Wide: true, Act: "img"},
			{Label: "⌨ 键盘", Act: "kbd"},
			{Label: "▦ 面板", Wide: true, Act: "panes"},
			{Label: "📁 文件", Wide: true, Act: "files"},
			// 剪贴板两条**必须是两个键**：手机浏览器只在用户手势里给读 / 写剪贴板，
			// 所以「取」和「粘」各要用户自己点一下，没法合成一个「同步」。
			// 「取」拿的是跑 herdr 那台机器的剪贴板 —— herdr 的复制落在那儿（实测），
			// 不取过来手机上哪儿都粘不出来。
			{Label: "📋 取", Wide: true, Act: "clip"},
			{Label: "📥 粘", Wide: true, Act: "paste"},
		}},
	}
}
