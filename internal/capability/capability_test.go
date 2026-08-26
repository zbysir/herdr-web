package capability

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestMatchJS 这张表要和前端那份**一字不差**（顺序也一样），而且 `key` 标记也要对上。
//
// 为什么值得一个测试：这两份是同一件事的两半 —— 前端那份带图标和名字，这份决定「能不能存
// 进来」。只在一边加一件事的后果都很难查，而且**每一种都是静默的**：
//
//   - 只在前端加 → 编辑器里拖得上去，一存报「不认识的按钮」；
//   - 只在这儿加 → 存得下去，但顶栏上画不出来；
//   - `key` 标记只在这儿打上 → 前端那个 `KeyAct` 类型里没有它，编辑器根本给不出这个 act；
//   - `key` 标记只在前端打上 → 存盘时被 IsKeyAct 拒掉，用户看到「act 只能是这几个」而他
//     明明是从界面上选的。
//
// 最后那两条以前**没有任何测试**（act 白名单两边各写一份联合类型 / map），这次一起纳进来。
//
// 直接读 .tsx 抠，不用快照文件：快照是第三份，一样会忘了改。
func TestMatchJS(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "web", "src", "capabilities.tsx"))
	if err != nil {
		t.Skipf("读不到前端那份清单（源码包里可能没有 web/）: %v", err)
	}
	// 一条一行、id 排最前面（那个文件的注释里写了这条约束）
	re := regexp.MustCompile(`(?m)^\s*\{ id: '([a-z+-]+)'(.*)$`)
	var got []string
	for _, m := range re.FindAllStringSubmatch(string(b), -1) {
		id, rest := m[1], m[2]
		if strings.Contains(rest, "key: true") {
			id += "+key"
		}
		got = append(got, id)
	}
	var want []string
	for _, c := range All {
		id := c.ID
		if c.Key {
			id += "+key"
		}
		want = append(want, id)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("清单和前端那份不一致（`+key` = 能当快捷键条的 act）\n go: %v\n js: %v", want, got)
	}
}

// TestPanelAndPinnedAreSane 几条自洽性：删不掉的必须能放顶栏，act 也必须是顶栏 id 的子集。
//
// 后一条是前端那边 `topbarAct[k.act]` 能直接查的前提（同一个 id 就是同一件事）——
// 破了的表现是快捷键条上那个键点下去什么都不发生，而且不报错。
func TestPanelAndPinnedAreSane(t *testing.T) {
	for _, c := range All {
		if c.Pinned && !c.Topbar {
			t.Errorf("%q 删不掉却又不能放顶栏", c.ID)
		}
		if c.Key && !c.Topbar {
			t.Errorf("%q 能当 act 却不能放顶栏 —— 前端拿 act 去查顶栏那张动作表会查不到", c.ID)
		}
	}
	if len(TopbarPinned()) == 0 {
		t.Error("一个删不掉的都没有：设置被删掉之后就没路回来改配置了")
	}
	seen := map[string]bool{}
	for _, c := range All {
		if seen[c.ID] {
			t.Errorf("id 重了：%q", c.ID)
		}
		seen[c.ID] = true
	}
}
