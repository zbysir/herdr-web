package softkeys

// 本文件由 lib/softkeys.js 的 DEFAULTS / PRESETS 生成 —— 51 条按键谱手抄进 Go
// 太容易抄错。testdata/js-snapshot.json 里存着当时的快照，softkeys_test.go 会
// 逐条比对，保证没抄漏抄错。
//
// 快照只锁「迁移那一刻」那 6 组；后面新加的组直接往下写，不进快照。

// Defaults 出厂配置：就是最早写死在 index.html 里的那一排。
func Defaults() []Key {
	return []Key{
		{Label: "⌨", Act: "kbd"},
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
		{Label: "↵", Send: "enter"},
	}
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
	}
}
