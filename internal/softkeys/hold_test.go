package softkeys

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// holdNames 前端「按住不放会连发」的那几个键（见 web/src/hooks/useHold.ts）。
//
// 只有「多发几次无非是多走几步，退得回来」的才在里面：方向 + 翻页。回车 / ctrl+c /
// 退格不在（多发一次就是多跑一条命令、或者吃掉一整行，而终端里没有撤销）。
var holdNames = []string{"up", "down", "left", "right", "pgup", "pgdn"}

// TestHoldKeysMatchJS 前端是按**字节**认「这个键能不能长按连发」的（`send` 里那一串），
// 而那串字节是这儿的 named 表算出来的。两份对不上时是**完全静默**的：长按不再连发，
// 一个字都不报，看着就像「这个功能没做」。
//
// 按字节认而不是加一个 `repeat` 配置项，是有意的：`↑` 是不是方向键这件事的答案已经写在
// send 里了，再加一个开关就是同一件事的第二份来源 —— 而且用户自己配的 `↑` 也该有这一档，
// 不该取决于他有没有记得勾那个框。代价就是这个测试。
func TestHoldKeysMatchJS(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "web", "src", "hooks", "useHold.ts"))
	if err != nil {
		t.Skipf("读不到前端那份（源码包里可能没有 web/）: %v", err)
	}
	src := string(b)
	i := strings.Index(src, "const REPEATABLE = new Set([")
	if i < 0 {
		t.Fatal("前端那份里找不到 REPEATABLE（改名了？两边就该一起改）")
	}
	rest := src[i:]
	j := strings.Index(rest, "])")
	if j < 0 {
		t.Fatal("REPEATABLE 那一段没收口")
	}
	var got []string
	for _, m := range regexp.MustCompile(`'([^']*)'`).FindAllStringSubmatch(rest[:j], -1) {
		got = append(got, unescapeJS(m[1]))
	}

	var want []string
	for _, n := range holdNames {
		s, ok := named[n]
		if !ok {
			t.Fatalf("named 表里没有 %q", n)
		}
		want = append(want, s)
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("长按连发那份和前端对不上（表现是长按静默失效）\n go: %q\n js: %q", want, got)
	}
}

// unescapeJS 只认这份清单里用得上的 \xNN。
func unescapeJS(s string) string {
	re := regexp.MustCompile(`\\x([0-9a-fA-F]{2})`)
	return re.ReplaceAllStringFunc(s, func(m string) string {
		n, err := strconv.ParseUint(m[2:], 16, 8)
		if err != nil {
			return m
		}
		return fmt.Sprintf("%c", rune(n))
	})
}
