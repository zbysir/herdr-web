package softkeys

// 软键上能挑的**内置图标**：这份是白名单，前端那份画得出来的在
// `web/src/keyicons.tsx`，两边**一字不差、顺序也一样**，有测试盯着（TestIconsMatchJS）。
//
// 为什么要有图标这一档：`Key.Label` 是自由文本，于是「键盘」这种键只能靠字形（`⌨`）——
// 而那些符号字形在很多字体里压根缺（缺了就显示成一个方框），有的字体里画得很难看，而且
// 大小 / 基线和旁边的字母都对不齐。图标是 SVG，三个问题一起没了。
//
// Label 仍然是**名字**（编辑器里认它、组键靠它、title 里显示它）；Icon 只决定**条上画什么**。
// 两者不是二选一：挑了图标，名字还在，只是条上不画字。
//
// 加一个图标 = 这儿一行 + 那边一行。别在这儿放太多：编辑器里是一格一个的选择器，
// 铺满三屏就没人找得到了 —— 常用的那三十几个就够，剩下的用文字标签。
var KeyIcons = []string{
	// 修饰键
	"ctrl", "alt", "shift", "cmd",
	// 编辑键
	"esc", "enter", "tab", "space", "bs", "del",
	// 方向 / 翻页
	"up", "down", "left", "right", "dpad", "pgup", "pgdn",
	// 常用动作
	"keyboard", "terminal", "close", "stop", "check", "trash",
	"copy", "paste", "search", "refresh", "undo", "redo",
	"plus", "minus", "zoom-in", "zoom-out", "max", "min",
	"split", "menu",
	// 界面里那几件事 —— 软键 `act:` 那一档最常用的就是这几个，得有对应的图标可挑
	"panes", "files", "image", "compose", "settings", "theme", "exit",
}

// IconOK 这个图标 id 认不认。空串 = 不挑图标（画文字标签），也算认。
func IconOK(id string) bool {
	if id == "" {
		return true
	}
	for _, s := range KeyIcons {
		if s == id {
			return true
		}
	}
	return false
}

// iconOf 读路径用：认不出的图标当没挑（画文字标签）。
//
// 和 spanOf 一个道理 —— 从新版本降级回来的文件里会有这个版本不认识的 id，
// 整份退回出厂太贵，而 Label 一直在，退化成「画文字」是完全可用的。
func iconOf(id string) string {
	if IconOK(id) {
		return id
	}
	return ""
}

// IconAt 图标摆在哪儿（只在挑了图标时有意义）：
//
//	""/"only"  只画图标，不画名字（默认，也是原来唯一的行为）
//	"pre"      图标当**前缀**，后面接名字 —— `[⌃ B]`
//	"post"     名字在前，图标当**后缀** —— `[新建 +]`
//
// 为什么要这一档：`^B 前缀` 这种键，名字里那个 `B` 是**有意义的**（换成别的字母就是另一个
// 键），而 `^` 那个字形恰恰是丑的那半。只能「图标或文字」二选一的话，这类键只能忍字形。
var iconAts = map[string]bool{"": true, "only": true, "pre": true, "post": true}

// IconAtOK 认不认这个摆法。
func IconAtOK(s string) bool { return iconAts[s] }

// iconAtOf 读路径用：认不出的当默认（只画图标）。和 iconOf / spanOf 一个道理。
func iconAtOf(s string) string {
	if IconAtOK(s) {
		return s
	}
	return ""
}
