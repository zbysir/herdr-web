package gitdiff

import (
	"strconv"
	"strings"
)

/* --------------------------------------------------- git 那几种机器可读格式 */

// splitZ 把 `-z` 的输出切成记录。**最后一条是空的**（每条记录都以 NUL 结尾），
// 不去掉的话每份清单末尾都会多出一条空路径。
func splitZ(b []byte) []string {
	s := string(b)
	s = strings.TrimSuffix(s, "\x00")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\x00")
}

// parseNameStatus 解析 `--name-status -z`。
//
// 一条改动是「状态字母 + 路径」两条记录；**改名 / 复制是三条**（`R100`、旧路径、
// 新路径）。少认这一条的表现是从改名那一项开始，后面所有文件的名字都错位一格。
func parseNameStatus(b []byte) []Change {
	rec := splitZ(b)
	out := []Change{}
	for i := 0; i < len(rec); {
		st := rec[i]
		i++
		if st == "" {
			continue
		}
		kind := kindOf(st[0])
		if (st[0] == 'R' || st[0] == 'C') && i+1 < len(rec) {
			out = append(out, Change{Kind: kind, Old: rec[i], Path: rec[i+1]})
			i += 2
			continue
		}
		if i >= len(rec) {
			break
		}
		out = append(out, Change{Kind: kind, Path: rec[i]})
		i++
	}
	return out
}

func kindOf(c byte) string {
	switch c {
	case 'A':
		return KindAdd
	case 'D':
		return KindDelete
	case 'R':
		return KindRename
	case 'C':
		return KindCopy
	case 'T':
		return KindType
	case 'U':
		return KindConflict
	default:
		return KindModify
	}
}

type numstat struct {
	add, del int
	binary   bool
}

// parseNumstat 解析 `--numstat -z`。
//
// 一条是 `加\t删\t路径`（一整条记录）。**改名那条的路径是空的**，紧跟着两条记录才是
// 旧路径和新路径 —— 实测出来的（`0\t0\t\0sub/b.txt\0sub/c.txt\0`）。二进制文件的
// 加删两栏是 `-`。
//
// 返回的 map 里改名用 `旧\x00新` 当键，和 merge 那边对上。
func parseNumstat(b []byte) map[string]numstat {
	rec := splitZ(b)
	out := map[string]numstat{}
	for i := 0; i < len(rec); i++ {
		f := strings.SplitN(rec[i], "\t", 3)
		if len(f) != 3 {
			continue
		}
		n := numstat{binary: f[0] == "-"}
		n.add, _ = strconv.Atoi(f[0])
		n.del, _ = strconv.Atoi(f[1])
		key := f[2]
		if key == "" && i+2 < len(rec) {
			key = rec[i+1] + "\x00" + rec[i+2]
			i += 2
		}
		out[key] = n
	}
	return out
}

// parseStatus 解析 `status --porcelain=v2 --branch -z`：分支抬头 + 每个文件的
// 暂存 / 未暂存两个标记 + 未跟踪的条目。
//
// 记录形状（man git-status 的 v2 一节）：
//
//	# branch.head main            分支名；摘着头时是字面量 `(detached)`
//	# branch.ab +1 -2             领先 / 落后
//	1 XY sub mH mI mW hH hI path
//	2 XY sub mH mI mW hH hI Xnn path\0origPath      改名：**两条记录**
//	u XY sub m1 m2 m3 mW h1 h2 h3 path              冲突中
//	? path                                          未跟踪
//
// 路径里可能有空格，所以每种都用 SplitN 数着字段切，不能 Fields。
func parseStatus(b []byte, st *Status) {
	rec := splitZ(b)
	for i := 0; i < len(rec); i++ {
		r := rec[i]
		switch {
		case strings.HasPrefix(r, "# branch.oid "):
			oid := strings.TrimPrefix(r, "# branch.oid ")
			if oid == "(initial)" {
				st.Repo.Unborn = true
			} else if len(oid) >= 7 {
				st.Repo.Head = oid[:7]
			}
		case strings.HasPrefix(r, "# branch.head "):
			h := strings.TrimPrefix(r, "# branch.head ")
			if h == "(detached)" {
				st.Repo.Detached = true
			} else {
				st.Repo.Branch = h
			}
		case strings.HasPrefix(r, "# branch.upstream "):
			st.Repo.Upstream = strings.TrimPrefix(r, "# branch.upstream ")
		case strings.HasPrefix(r, "# branch.ab "):
			for _, f := range strings.Fields(strings.TrimPrefix(r, "# branch.ab ")) {
				n, err := strconv.Atoi(f[1:])
				if err != nil {
					continue
				}
				if f[0] == '+' {
					st.Repo.Ahead = n
				} else {
					st.Repo.Behind = n
				}
			}
		case strings.HasPrefix(r, "1 "):
			if f := strings.SplitN(r[2:], " ", 8); len(f) == 8 {
				st.Changes = append(st.Changes, flagged(f[0], f[7]))
			}
		case strings.HasPrefix(r, "2 "):
			if f := strings.SplitN(r[2:], " ", 9); len(f) == 9 {
				st.Changes = append(st.Changes, flagged(f[0], f[8]))
			}
			i++ // 下一条是改名前的路径
		case strings.HasPrefix(r, "u "):
			if f := strings.SplitN(r[2:], " ", 11); len(f) == 11 {
				c := flagged(f[0], f[10])
				c.Kind = KindConflict
				st.Changes = append(st.Changes, c)
			}
		case strings.HasPrefix(r, "? "):
			p := r[2:]
			st.Changes = append(st.Changes, Change{
				Path: p, Kind: KindUntrack, Unstaged: true,
				// 一整个新目录 git 折成一条（末尾带斜杠），里面有什么它不说
				Dir: strings.HasSuffix(p, "/"),
			})
		}
	}
}

// flagged 从 XY 两个字母出「在暂存区里 / 还没暂存」。`.` = 这一侧没变化。
func flagged(xy, path string) Change {
	c := Change{Path: path, Kind: KindModify}
	if len(xy) == 2 {
		c.Staged = xy[0] != '.'
		c.Unstaged = xy[1] != '.'
	}
	return c
}

/* ------------------------------------------------------------ unified diff */

// Seg 是一行里的一截。Eq 为真 = 两边一样的部分，假 = 真正变了的那截。
type Seg struct {
	Eq bool   `json:"eq,omitempty"`
	S  string `json:"s"`
}

// 一行的类型。用 diff 自己那几个字符，前端直接照着画。
const (
	LineCtx  = " "
	LineAdd  = "+"
	LineDel  = "-"
	LineNote = "\\" // `\ No newline at end of file`
)

// Line 是补丁里的一行。
//
// **Segs 和 S 是二选一**：按词高亮出来的行只发 Segs（三截：一样的前缀 / 变了的中间 /
// 一样的后缀），不再重复发一份整行文本。前端取文本时是 `segs ? segs.join : s`。
//
// 为什么不发「第几个字符到第几个字符」：那个偏移在 Go 里是字节 / rune，在 JS 里是
// UTF-16 码元 —— 一个 emoji 就能让两边错位，而错位的表现是高亮框歪在半个字上，
// 没人会想到是编码问题。发字符串没有这个坑。
type Line struct {
	T    string `json:"t"`
	Old  int    `json:"o,omitempty"` // 旧文件里的行号（新增行没有）
	New  int    `json:"n,omitempty"` // 新文件里的行号（删除行没有）
	S    string `json:"s,omitempty"`
	Segs []Seg  `json:"segs,omitempty"`
}

// Text 取这一行的完整文本（Segs 拼回来）。
func (l Line) Text() string {
	if l.Segs == nil {
		return l.S
	}
	var b strings.Builder
	for _, s := range l.Segs {
		b.WriteString(s.S)
	}
	return b.String()
}

// Hunk 是一段。
type Hunk struct {
	Head     string `json:"head,omitempty"` // `@@ … @@` 后面那截（多半是函数名）
	OldStart int    `json:"os"`
	OldLines int    `json:"ol"`
	NewStart int    `json:"ns"`
	NewLines int    `json:"nl"`
	Lines    []Line `json:"lines"`

	// 行号是自己数出来的（`@@` 只给起点），这两个是数到哪儿了。不导出 = 不进 JSON。
	oldCur, newCur int
}

// File 是补丁里的一个文件。
type File struct {
	Path   string `json:"path"`
	Old    string `json:"old,omitempty"`
	Kind   string `json:"kind"`
	Binary bool   `json:"binary,omitempty"`
	Mode   string `json:"mode,omitempty"` // 权限位变了，比如 "100644 → 100755"
	Add    int    `json:"add"`
	Del    int    `json:"del"`
	Hunks  []Hunk `json:"hunks"`
	// Cut 还有多少行没给（撞上行数上限）。前端必须显示 —— 不说的话「就改了这么多」
	// 是句假话。
	Cut int `json:"cut,omitempty"`
}

// Parse 把一份 unified diff 解析成结构。limit 是**总行数**上限（所有文件加起来）。
//
// 文件名不从 `diff --git a/x b/y` 那一行拆：路径里有空格时那一行是**有歧义的**
// （`a/my file b/my file` 从哪儿断开？）。`--- a/…` / `+++ b/…` 各占一行，
// 反而没有歧义 —— git 只在路径含引号 / 反斜杠 / 控制字符时才加引号包起来。
// 只有「纯改名」「纯权限变化」这种没有 ---/+++ 的补丁才退回去拆那一行。
func Parse(b []byte, limit int) []File {
	var out []File
	var f *File
	var h *Hunk
	total := 0
	// flush 把上一个文件收尾（按词高亮是**逐段**做的，收尾时最后一段也要做）
	closeHunk := func() {
		if f != nil && h != nil {
			markWords(h.Lines)
			f.Hunks = append(f.Hunks, *h)
		}
		h = nil
	}
	closeFile := func() {
		closeHunk()
		if f != nil {
			out = append(out, *f)
		}
		f = nil
	}

	// 去掉末尾那个换行再切：补丁的最后一行也是以 `\n` 收尾的，直接 Split 会多出一个
	// 空元素 —— 而空行在这儿是「上下文里的空行」，于是每份补丁末尾都凭空多一行。
	for _, raw := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		line := strings.TrimSuffix(raw, "\r")
		switch {
		case strings.HasPrefix(line, "diff --git "):
			closeFile()
			a, bb := splitHeader(strings.TrimPrefix(line, "diff --git "))
			f = &File{Kind: KindModify, Path: bb, Old: "", Hunks: []Hunk{}}
			if a != bb {
				f.Old = a
			}
			continue
		case f == nil:
			continue // 头一个 `diff --git` 之前的东西（`git show` 的提交信息之类）
		}

		if h == nil {
			// 还在文件头里
			switch {
			case strings.HasPrefix(line, "new file mode"):
				f.Kind = KindAdd
				continue
			case strings.HasPrefix(line, "deleted file mode"):
				f.Kind = KindDelete
				continue
			case strings.HasPrefix(line, "rename from "):
				f.Kind = KindRename
				f.Old = unquote(strings.TrimPrefix(line, "rename from "))
				continue
			case strings.HasPrefix(line, "rename to "):
				f.Kind = KindRename
				f.Path = unquote(strings.TrimPrefix(line, "rename to "))
				continue
			case strings.HasPrefix(line, "copy from "):
				f.Kind = KindCopy
				f.Old = unquote(strings.TrimPrefix(line, "copy from "))
				continue
			case strings.HasPrefix(line, "copy to "):
				f.Kind = KindCopy
				f.Path = unquote(strings.TrimPrefix(line, "copy to "))
				continue
			case strings.HasPrefix(line, "old mode "):
				f.Mode = strings.TrimSpace(strings.TrimPrefix(line, "old mode "))
				continue
			case strings.HasPrefix(line, "new mode "):
				f.Mode += " → " + strings.TrimSpace(strings.TrimPrefix(line, "new mode "))
				continue
			case strings.HasPrefix(line, "Binary files ") || strings.HasPrefix(line, "GIT binary patch"):
				f.Binary = true
				continue
			case strings.HasPrefix(line, "--- "):
				if p := side(line[4:]); p != "" {
					f.Old = p
				} else {
					f.Kind = KindAdd // --- /dev/null
				}
				continue
			case strings.HasPrefix(line, "+++ "):
				if p := side(line[4:]); p != "" {
					f.Path = p
				} else {
					f.Kind = KindDelete // +++ /dev/null
				}
				continue
			}
		}

		if strings.HasPrefix(line, "@@") {
			closeHunk()
			nh, ok := parseHunkHead(line)
			if !ok {
				continue
			}
			h = &nh
			continue
		}
		if h == nil {
			continue
		}
		if total >= limit {
			f.Cut++
			continue
		}
		l, ok := hunkLine(line, h)
		if !ok {
			continue
		}
		total++
		switch l.T {
		case LineAdd:
			f.Add++
		case LineDel:
			f.Del++
		}
		h.Lines = append(h.Lines, l)
	}
	closeFile()
	// 改名前后同名（`--- a/x` / `+++ b/x`）时把 Old 抹掉，前端就不用自己判「这算改名吗」
	for i := range out {
		if out[i].Old == out[i].Path {
			out[i].Old = ""
		}
	}
	return out
}

// hunkLine 认段里的一行，顺带记行号。
func hunkLine(line string, h *Hunk) (Line, bool) {
	if line == "" {
		// 空行：git 对「上下文里的空行」发的就是一个空格，但补丁被别的工具处理过之后
		// 那个空格常常被吃掉。当成空的上下文行 —— 丢掉的话前后会粘成一行。
		l := Line{T: LineCtx, Old: h.oldNext(), New: h.newNext()}
		return l, true
	}
	body := line[1:]
	switch line[0] {
	case ' ':
		return Line{T: LineCtx, Old: h.oldNext(), New: h.newNext(), S: body}, true
	case '+':
		return Line{T: LineAdd, New: h.newNext(), S: body}, true
	case '-':
		return Line{T: LineDel, Old: h.oldNext(), S: body}, true
	case '\\':
		return Line{T: LineNote, S: strings.TrimSpace(body)}, true
	}
	return Line{}, false
}

// oldNext / newNext 发下一个行号。**行号是自己数出来的**，因为 `@@` 只给起点。
func (h *Hunk) oldNext() int { h.oldCur++; return h.OldStart + h.oldCur - 1 }
func (h *Hunk) newNext() int { h.newCur++; return h.NewStart + h.newCur - 1 }

// parseHunkHead 解析 `@@ -1,3 +1,4 @@ func x()`。
func parseHunkHead(line string) (Hunk, bool) {
	rest, ok := strings.CutPrefix(line, "@@ ")
	if !ok {
		return Hunk{}, false
	}
	body, head, ok := strings.Cut(rest, " @@")
	if !ok {
		return Hunk{}, false
	}
	oldSpec, newSpec, ok := strings.Cut(body, " ")
	if !ok {
		return Hunk{}, false
	}
	os, ol := span(strings.TrimPrefix(oldSpec, "-"))
	ns, nl := span(strings.TrimPrefix(newSpec, "+"))
	return Hunk{
		Head: strings.TrimSpace(head), Lines: []Line{},
		OldStart: os, OldLines: ol, NewStart: ns, NewLines: nl,
	}, true
}

// span 解析 `12,5`（省略逗号那一半时行数是 1）。
func span(s string) (start, n int) {
	a, b, ok := strings.Cut(s, ",")
	start, _ = strconv.Atoi(a)
	n = 1
	if ok {
		n, _ = strconv.Atoi(b)
	}
	return
}

// side 从 `a/path` / `b/path` / `/dev/null` 里取路径。/dev/null 回空串。
func side(s string) string {
	s = strings.TrimSpace(s)
	if s == "/dev/null" {
		return ""
	}
	s = unquote(s)
	if len(s) > 2 && (s[0] == 'a' || s[0] == 'b' || s[0] == 'i' || s[0] == 'w' || s[0] == 'c' || s[0] == 'o') && s[1] == '/' {
		return s[2:]
	}
	return s
}

// splitHeader 从 `a/x b/y` 里拆两个路径。**只在没有 ---/+++ 时用**（纯改名 / 纯权限
// 变化），因为路径带空格时这一行本身就是有歧义的：`a/my file b/my file` 里那个断点
// 只能靠「前半段以 a/ 开头、后半段以 b/ 开头」去猜。
func splitHeader(s string) (string, string) {
	if strings.HasPrefix(s, `"`) {
		// 带引号那半是转义过的，找结束引号（跳过 \" ）
		for i := 1; i < len(s); i++ {
			if s[i] == '\\' {
				i++
				continue
			}
			if s[i] == '"' {
				return side(unquote(s[:i+1])), side(strings.TrimSpace(s[i+1:]))
			}
		}
		return "", ""
	}
	for i := 0; i+2 < len(s); i++ {
		if s[i] == ' ' && (s[i+1] == 'b' || s[i+1] == 'w' || s[i+1] == 'o' || s[i+1] == 'c') && s[i+2] == '/' {
			a, b := side(s[:i]), side(s[i+1:])
			if a != "" && b != "" {
				return a, b
			}
		}
	}
	return side(s), side(s)
}

// unquote 展开 git 的 C 风格转义（路径里有引号 / 反斜杠 / 控制字符时才会出现；
// 中文那种非 ASCII 已经被 `core.quotepath=false` 挡住了）。
func unquote(s string) string {
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return s
	}
	if out, err := strconv.Unquote(s); err == nil {
		return out
	}
	return s[1 : len(s)-1]
}

/* ------------------------------------------------------------------ 词高亮 */

// markWords 把「配得上对」的删除行 / 新增行按字符求公共前后缀，只把中间那截标出来。
//
// 手机上这一档比什么都值：一整行红配一整行绿的时候，改了哪个字得拿眼睛一格一格扫。
//
// 配对是**按位置的**：一段里「连着几条删除 + 紧接着连着几条新增」，第 1 条配第 1 条、
// 第 2 条配第 2 条，多出来的那几条不配。
//
// 为什么不要求两边条数相等（第一版是那样写的，理由是「条数不等就是整块重写」）：那条
// 规则把最常见的情况挡掉了 —— 改一个 if 条件顺手多加一行日志，就是「删 2 加 3」，
// 而前两行明明是一一对应的。真正防「标错」的不是条数，是 pairWords 里那两道门槛。
//
// 代价说清楚：块中间**插进**一行的话，后面那些就整体错位一格。那时候错位的那几对
// 基本都过不了门槛（前后缀对不上），于是退化成不标 —— 而不是标出一堆假的对应关系。
func markWords(lines []Line) {
	i := 0
	for i < len(lines) {
		if lines[i].T != LineDel {
			i++
			continue
		}
		d := i
		for i < len(lines) && lines[i].T == LineDel {
			i++
		}
		a := i
		for i < len(lines) && lines[i].T == LineAdd {
			i++
		}
		for k := 0; k < min(a-d, i-a); k++ {
			pairWords(&lines[d+k], &lines[a+k])
		}
	}
}

func pairWords(del, add *Line) {
	x, y := []rune(del.S), []rune(add.S)
	if len(x) == 0 || len(y) == 0 {
		return
	}
	p := 0
	for p < len(x) && p < len(y) && x[p] == y[p] {
		p++
	}
	s := 0
	for s < len(x)-p && s < len(y)-p && x[len(x)-1-s] == y[len(y)-1-s] {
		s++
	}
	common := p + s
	// 两条规矩，都是为了「标错了比不标更糟」：
	//  ① 前后缀一个字符都不一样 = 这两行没关系，别配；
	//  ② 变了的那截超过任意一边的四分之三 = 整行都换了，标出来和不标没区别
	//     （那一行本来就整条是红/绿的），却会让两条毫不相干的行看着像有对应关系。
	if common == 0 ||
		4*(len(x)-common) > 3*len(x) ||
		4*(len(y)-common) > 3*len(y) {
		return
	}
	del.Segs, del.S = segsOf(x, p, s), ""
	add.Segs, add.S = segsOf(y, p, s), ""
}

// segsOf 切成「一样的前缀 / 变了的中间 / 一样的后缀」，空的那截不发。
func segsOf(r []rune, p, s int) []Seg {
	out := []Seg{}
	if p > 0 {
		out = append(out, Seg{Eq: true, S: string(r[:p])})
	}
	if mid := r[p : len(r)-s]; len(mid) > 0 {
		out = append(out, Seg{S: string(mid)})
	}
	if s > 0 {
		out = append(out, Seg{Eq: true, S: string(r[len(r)-s:])})
	}
	return out
}
