package gitdiff

import (
	"strings"
	"testing"
)

func TestParseBasic(t *testing.T) {
	patch := `diff --git a/x.go b/x.go
index 111..222 100644
--- a/x.go
+++ b/x.go
@@ -10,6 +10,7 @@ func hello() {
 	a := 1
-	b := 2
+	b := 3
+	c := 4
 	return
`
	fs := Parse([]byte(patch), 100)
	if len(fs) != 1 {
		t.Fatalf("文件数 = %d", len(fs))
	}
	f := fs[0]
	if f.Path != "x.go" || f.Kind != KindModify || f.Add != 2 || f.Del != 1 {
		t.Fatalf("%+v", f)
	}
	h := f.Hunks[0]
	if h.Head != "func hello() {" {
		t.Errorf("段头 = %q（`@@ … @@` 后面那截是函数名，手机上滚下去之后全靠它认位置）", h.Head)
	}
	if h.OldStart != 10 || h.OldLines != 6 || h.NewStart != 10 || h.NewLines != 7 {
		t.Errorf("%+v", h)
	}
	// 行号是自己数出来的：@@ 只给起点
	want := []struct {
		t    string
		o, n int
	}{
		{LineCtx, 10, 10}, {LineDel, 11, 0}, {LineAdd, 0, 11}, {LineAdd, 0, 12}, {LineCtx, 12, 13},
	}
	if len(h.Lines) != len(want) {
		t.Fatalf("行数 = %d，想要 %d", len(h.Lines), len(want))
	}
	for i, w := range want {
		got := h.Lines[i]
		if got.T != w.t || got.Old != w.o || got.New != w.n {
			t.Errorf("第 %d 行 = %+v，想要 %v/%d/%d", i, got, w.t, w.o, w.n)
		}
	}
}

func TestParseAddDeleteRenameMode(t *testing.T) {
	cases := []struct {
		name, patch, kind, path, old, mode string
		binary                             bool
	}{
		{
			name: "新增", kind: KindAdd, path: "n.txt",
			patch: "diff --git a/n.txt b/n.txt\nnew file mode 100644\n--- /dev/null\n+++ b/n.txt\n@@ -0,0 +1 @@\n+hi\n",
		},
		{
			name: "删除", kind: KindDelete, path: "d.txt", old: "",
			patch: "diff --git a/d.txt b/d.txt\ndeleted file mode 100644\n--- a/d.txt\n+++ /dev/null\n@@ -1 +0,0 @@\n-bye\n",
		},
		{
			// 100% 相似的改名**没有** ---/+++ 两行，只能靠 rename from/to
			name: "纯改名", kind: KindRename, path: "b.txt", old: "a.txt",
			patch: "diff --git a/a.txt b/b.txt\nsimilarity index 100%\nrename from a.txt\nrename to b.txt\n",
		},
		{
			name: "只改权限", kind: KindModify, path: "s.sh", mode: "100644 → 100755",
			patch: "diff --git a/s.sh b/s.sh\nold mode 100644\nnew mode 100755\n",
		},
		{
			name: "二进制", kind: KindModify, path: "p.png", binary: true,
			patch: "diff --git a/p.png b/p.png\nindex 1..2 100644\nBinary files a/p.png and b/p.png differ\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := Parse([]byte(c.patch), 100)
			if len(fs) != 1 {
				t.Fatalf("文件数 = %d", len(fs))
			}
			f := fs[0]
			if f.Kind != c.kind || f.Path != c.path || f.Old != c.old || f.Binary != c.binary || f.Mode != c.mode {
				t.Errorf("%+v", f)
			}
		})
	}
}

// 路径里有空格时 `diff --git a/x y b/x y` 那一行是**有歧义的**，所以文件名优先从
// ---/+++ 两行取（那两行一行一个，没有歧义）。
func TestParsePathWithSpace(t *testing.T) {
	patch := "diff --git a/my file.txt b/my file.txt\n--- a/my file.txt\n+++ b/my file.txt\n@@ -1 +1 @@\n-a\n+b\n"
	fs := Parse([]byte(patch), 100)
	if len(fs) != 1 || fs[0].Path != "my file.txt" || fs[0].Old != "" {
		t.Fatalf("%+v", fs)
	}
}

// 带引号 / 控制字符的路径 git 会 C 风格转义（`core.quotepath=false` 只挡住非 ASCII 那档）。
func TestParseQuotedPath(t *testing.T) {
	patch := "diff --git \"a/we\\\"ird.txt\" \"b/we\\\"ird.txt\"\n--- \"a/we\\\"ird.txt\"\n+++ \"b/we\\\"ird.txt\"\n@@ -1 +1 @@\n-a\n+b\n"
	fs := Parse([]byte(patch), 100)
	if len(fs) != 1 || fs[0].Path != `we"ird.txt` {
		t.Fatalf("%+v", fs)
	}
}

func TestParseMultipleFiles(t *testing.T) {
	patch := "diff --git a/a b/a\n--- a/a\n+++ b/a\n@@ -1 +1 @@\n-1\n+2\n" +
		"diff --git a/b b/b\n--- a/b\n+++ b/b\n@@ -1 +1 @@\n-3\n+4\n"
	fs := Parse([]byte(patch), 100)
	if len(fs) != 2 || fs[0].Path != "a" || fs[1].Path != "b" {
		t.Fatalf("%+v", fs)
	}
}

// 撞上行数上限时**必须把剩下多少行报出来** —— 不报的话「就改了这么多」是句假话。
func TestParseLimit(t *testing.T) {
	var b strings.Builder
	b.WriteString("diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1,10 +1,10 @@\n")
	for i := 0; i < 10; i++ {
		b.WriteString("+line\n")
	}
	fs := Parse([]byte(b.String()), 4)
	if len(fs[0].Hunks[0].Lines) != 4 {
		t.Errorf("给了 %d 行，上限是 4", len(fs[0].Hunks[0].Lines))
	}
	if fs[0].Cut != 6 {
		t.Errorf("没给的行数 = %d，想要 6", fs[0].Cut)
	}
}

/* ------------------------------------------------------------------ 词高亮 */

func segs(l Line) string {
	var b strings.Builder
	for _, s := range l.Segs {
		if s.Eq {
			b.WriteString(s.S)
		} else {
			b.WriteString("«" + s.S + "»")
		}
	}
	return b.String()
}

func TestWordHighlight(t *testing.T) {
	cases := []struct {
		name, del, add, wantDel, wantAdd string
	}{
		{
			name: "改一个词", del: "\tconst timeout = 30", add: "\tconst timeout = 60",
			wantDel: "\tconst timeout = «3»0", wantAdd: "\tconst timeout = «6»0",
		},
		{
			name: "中文改中间", del: "这里是旧的说法，后面一样", add: "这里是新的讲法，后面一样",
			// 「法」也是公共后缀的一部分（前后缀是按**字符**求的，不是按词）
			wantDel: "这里是«旧的说»法，后面一样", wantAdd: "这里是«新的讲»法，后面一样",
		},
		{
			// emoji 是两个 UTF-16 码元 —— 发字符串而不是偏移，正是为了这种
			name: "emoji", del: "status: 🙂 ok", add: "status: 🎉 ok",
			wantDel: "status: «🙂» ok", wantAdd: "status: «🎉» ok",
		},
		{
			name: "只加后缀", del: "foo", add: "foobar",
			wantDel: "foo", wantAdd: "foo«bar»",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ls := []Line{{T: LineDel, S: c.del}, {T: LineAdd, S: c.add}}
			markWords(ls)
			if got := segs(ls[0]); got != c.wantDel {
				t.Errorf("删除行 = %q，想要 %q", got, c.wantDel)
			}
			if got := segs(ls[1]); got != c.wantAdd {
				t.Errorf("新增行 = %q，想要 %q", got, c.wantAdd)
			}
			// Segs 和 S 是二选一：都发就是同一份文本发两遍
			if ls[0].Segs != nil && ls[0].S != "" {
				t.Error("Segs 出来了 S 还留着")
			}
		})
	}
}

// 标错了比不标更糟（满屏高亮 = 没有高亮），所以这几种一律不标。
func TestWordHighlightRestraint(t *testing.T) {
	cases := []struct{ name, del, add string }{
		{"毫不相干的两行", "package main", "写点别的东西吧"},
		{"只有缩进一样", "\t\tfoo(1, 2, 3)", "\t\tbar(9)"},
		{"整行都变了", "aaaa", "bbbb"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ls := []Line{{T: LineDel, S: c.del}, {T: LineAdd, S: c.add}}
			markWords(ls)
			if ls[0].Segs != nil || ls[1].Segs != nil {
				t.Errorf("不该标：%q / %q", segs(ls[0]), segs(ls[1]))
			}
		})
	}
}

// 「删 2 加 3」是最常见的形状（改两行、顺手多加一行），前两条照旧要按位置配上 ——
// 第一版要求两边条数相等，正好把这种挡掉了。
func TestWordHighlightUnevenRuns(t *testing.T) {
	ls := []Line{
		{T: LineDel, S: "count := 1"}, {T: LineDel, S: "limit := 2"},
		{T: LineAdd, S: "count := 9"}, {T: LineAdd, S: "limit := 8"}, {T: LineAdd, S: "log.Print(count)"},
	}
	markWords(ls)
	if got := segs(ls[0]); got != "count := «1»" {
		t.Errorf("第 1 对没配上：%q", got)
	}
	if got := segs(ls[3]); got != "limit := «8»" {
		t.Errorf("第 2 对没配上：%q", got)
	}
	// 多出来那条没得配，原样留着
	if ls[4].Segs != nil || ls[4].S != "log.Print(count)" {
		t.Errorf("多出来的那条不该动：%+v", ls[4])
	}
}

/* ------------------------------------------------------- git 的机器可读格式 */

// 改名在 `--numstat -z` 里是**三条记录**（空路径 + 旧 + 新）—— 少认这一条的表现是
// 从改名那一项开始，后面所有文件的加删数字都错位。
func TestParseNumstatRename(t *testing.T) {
	in := []byte("2\t1\ta.txt\x00-\t-\tbin.dat\x000\t0\t\x00sub/b.txt\x00sub/c.txt\x00")
	got := parseNumstat(in)
	if n := got["a.txt"]; n.add != 2 || n.del != 1 {
		t.Errorf("a.txt = %+v", n)
	}
	if n := got["bin.dat"]; !n.binary {
		t.Errorf("bin.dat = %+v", n)
	}
	if n, ok := got["sub/b.txt\x00sub/c.txt"]; !ok || n.add != 0 {
		t.Errorf("改名那条没认出来：%+v", got)
	}
}

func TestParseNameStatusRename(t *testing.T) {
	in := []byte("M\x00a.txt\x00R100\x00old.txt\x00new.txt\x00A\x00z.txt\x00")
	got := parseNameStatus(in)
	if len(got) != 3 {
		t.Fatalf("%+v", got)
	}
	if got[1].Kind != KindRename || got[1].Old != "old.txt" || got[1].Path != "new.txt" {
		t.Errorf("改名 = %+v", got[1])
	}
	// 错位的话这一条会变成 "new.txt"
	if got[2].Path != "z.txt" || got[2].Kind != KindAdd {
		t.Errorf("改名后面那条错位了：%+v", got[2])
	}
}

func TestParseStatusV2(t *testing.T) {
	in := []byte(strings.Join([]string{
		"# branch.oid abcdef1234567890",
		"# branch.head main",
		"# branch.upstream github/main",
		"# branch.ab +2 -3",
		"1 .M N... 100644 100644 100644 aaa bbb a b.txt", // 路径里有空格
		"1 M. N... 100644 100644 100644 aaa bbb staged.txt",
		"2 R. N... 100644 100644 100644 aaa bbb R100 new.txt",
		"old.txt",
		"? untracked.txt",
		"? newdir/",
		"",
	}, "\x00"))
	st := &Status{}
	parseStatus(in, st)
	if st.Repo.Branch != "main" || st.Repo.Upstream != "github/main" || st.Repo.Ahead != 2 || st.Repo.Behind != 3 {
		t.Fatalf("%+v", st.Repo)
	}
	if st.Repo.Head != "abcdef1" {
		t.Errorf("短 hash = %q", st.Repo.Head)
	}
	got := map[string]Change{}
	for _, c := range st.Changes {
		got[c.Path] = c
	}
	if c := got["a b.txt"]; !c.Unstaged || c.Staged {
		t.Errorf("带空格的路径 / 角标：%+v；全部 = %v", c, paths(st.Changes))
	}
	if c := got["staged.txt"]; !c.Staged || c.Unstaged {
		t.Errorf("%+v", c)
	}
	// 改名那条占两条记录，多吃一条的话 old.txt 会变成一个幽灵条目
	if _, bad := got["old.txt"]; bad {
		t.Errorf("改名前的路径被当成了一条改动：%v", paths(st.Changes))
	}
	if c := got["newdir/"]; !c.Dir || c.Kind != KindUntrack {
		t.Errorf("未跟踪目录：%+v", c)
	}
}

func TestParseStatusDetachedAndUnborn(t *testing.T) {
	st := &Status{}
	parseStatus([]byte("# branch.oid (initial)\x00# branch.head main\x00"), st)
	if !st.Repo.Unborn {
		t.Error("`(initial)` = 还没有第一次提交")
	}
	st = &Status{}
	parseStatus([]byte("# branch.oid abc\x00# branch.head (detached)\x00"), st)
	if !st.Repo.Detached || st.Repo.Branch != "" {
		t.Errorf("%+v", st.Repo)
	}
}
