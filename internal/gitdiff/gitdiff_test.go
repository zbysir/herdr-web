package gitdiff

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zbysir/herdr-web/internal/files"
)

// 这一组是**拿真 git 跑的**。理由和 internal/composer 那边的真机抓屏一样：这个包干的
// 全部事情就是「git 到底吐出来什么」，而那几种 `-z` 格式（改名占三条记录、二进制的
// 加删两栏是 `-`、未跟踪目录折成一条）照着文档写和照着输出写不是一回事 —— 前者错了
// 是静默的（列表错位一格）。

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// repo 造一个有各种花样的仓库：改了的、暂存的、改名的、二进制、未跟踪的文件和目录、
// 名字里带空格的、中文名的。
func repo(t *testing.T) (*Runner, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("这台机器上没有 git")
	}
	dir := t.TempDir()
	// macOS 的 TempDir 是 /var/folders/…，而 /var 是指向 /private/var 的软链 ——
	// git 回的是解开之后的路径，直接比字符串会对不上。
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	git(t, dir, "init", "-q", "-b", "main", ".")
	write(t, dir, "a.txt", "a\nb\nc\n")
	write(t, dir, "sub/b.txt", "1\n2\n")
	write(t, dir, "空 格.txt", "x\n")
	write(t, dir, "中文.txt", "旧\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", "first")

	write(t, dir, "a.txt", "a\nB\nc\nd\n")        // 改一行 + 加一行
	write(t, dir, "中文.txt", "新\n")                // 中文名 + 中文内容
	git(t, dir, "mv", "sub/b.txt", "sub/c.txt")   // 改名
	write(t, dir, "untracked.txt", "new\nnew2\n") // 未跟踪
	write(t, dir, "newdir/q.txt", "q\n")          // 未跟踪的整个目录
	write(t, dir, "staged.txt", "s\n")
	git(t, dir, "add", "staged.txt") // 只在暂存区
	if err := os.WriteFile(filepath.Join(dir, "bin.dat"), []byte{0, 1, 2, 'x'}, 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "bin.dat")

	r := New(&files.Browser{Enabled: true})
	if !r.Enabled() {
		t.Skip("git 不可用")
	}
	return r, dir
}

func find(cs []Change, path string) *Change {
	for i := range cs {
		if cs[i].Path == path {
			return &cs[i]
		}
	}
	return nil
}

func TestStatusAll(t *testing.T) {
	r, dir := repo(t)
	st, err := r.Status(context.Background(), dir, ModeAll)
	if err != nil {
		t.Fatal(err)
	}
	if st.Repo.Root != dir {
		t.Errorf("root = %q，想要 %q", st.Repo.Root, dir)
	}
	if st.Repo.Branch != "main" {
		t.Errorf("branch = %q", st.Repo.Branch)
	}
	a := find(st.Changes, "a.txt")
	if a == nil || a.Kind != KindModify || a.Add != 2 || a.Del != 1 {
		t.Errorf("a.txt = %+v，想要 modify +2 -1", a)
	}
	if a != nil && (!a.Unstaged || a.Staged) {
		t.Errorf("a.txt 只改了工作区，角标应该只有「未暂存」：%+v", a)
	}
	// 改名：**必须认出来**（不认的话清单上是「删一个 + 加一个」，点进去两条都是整个文件）
	if c := find(st.Changes, "sub/c.txt"); c == nil || c.Kind != KindRename || c.Old != "sub/b.txt" {
		t.Errorf("改名没认出来：%+v", c)
	}
	// 二进制：numstat 的两栏是 `-`
	if c := find(st.Changes, "bin.dat"); c == nil || !c.Binary {
		t.Errorf("bin.dat 该是二进制：%+v", c)
	}
	if c := find(st.Changes, "staged.txt"); c == nil || !c.Staged || c.Unstaged {
		t.Errorf("staged.txt 该只有「已暂存」角标：%+v", c)
	}
	// 未跟踪的文件要数出行数（自己读的，不 fork git）
	if c := find(st.Changes, "untracked.txt"); c == nil || c.Kind != KindUntrack || c.Add != 2 {
		t.Errorf("未跟踪文件：%+v，想要 untracked +2", c)
	}
	// 未跟踪的**目录**：git 折成一条，末尾带斜杠
	if c := find(st.Changes, "newdir/"); c == nil || !c.Dir {
		t.Errorf("未跟踪目录没折出来：%+v", c)
	}
	// 中文名 + 空格名：不能是八进制转义，也不能被空格切断
	if c := find(st.Changes, "中文.txt"); c == nil {
		t.Errorf("中文路径丢了（core.quotepath 没关？）：%v", paths(st.Changes))
	}
}

func paths(cs []Change) []string {
	out := []string{}
	for _, c := range cs {
		out = append(out, c.Path)
	}
	return out
}

func TestStatusStagedAndHead(t *testing.T) {
	r, dir := repo(t)
	st, err := r.Status(context.Background(), dir, ModeStaged)
	if err != nil {
		t.Fatal(err)
	}
	if find(st.Changes, "staged.txt") == nil {
		t.Errorf("已暂存那一档里没有 staged.txt：%v", paths(st.Changes))
	}
	// 只改了工作区的不该出现在「已暂存」里
	if find(st.Changes, "a.txt") != nil {
		t.Errorf("a.txt 没暂存过，不该出现在已暂存那一档：%v", paths(st.Changes))
	}
	// 未跟踪的一律不进这一档
	if find(st.Changes, "untracked.txt") != nil {
		t.Error("未跟踪的进了已暂存那一档")
	}

	st, err = r.Status(context.Background(), dir, ModeHead)
	if err != nil {
		t.Fatal(err)
	}
	if st.Commit == nil || st.Commit.Subject != "first" {
		t.Fatalf("提交抬头不对：%+v", st.Commit)
	}
	if c := find(st.Changes, "a.txt"); c == nil || c.Kind != KindAdd || c.Add != 3 {
		t.Errorf("上次提交里的 a.txt：%+v，想要 add +3", c)
	}
}

func TestDiffText(t *testing.T) {
	r, dir := repo(t)
	p, err := r.Diff(context.Background(), Req{Dir: dir, Mode: ModeAll, Path: "a.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Files) != 1 {
		t.Fatalf("想要一个文件，拿到 %d", len(p.Files))
	}
	f := p.Files[0]
	if f.Path != "a.txt" || f.Add != 2 || f.Del != 1 {
		t.Errorf("%+v", f)
	}
	if len(f.Hunks) != 1 {
		t.Fatalf("段数 = %d", len(f.Hunks))
	}
	h := f.Hunks[0]
	// 行号是自己数出来的，钉住：b 在旧文件第 2 行、B 在新文件第 2 行
	var del, add *Line
	for i := range h.Lines {
		switch h.Lines[i].T {
		case LineDel:
			del = &h.Lines[i]
		case LineAdd:
			if add == nil {
				add = &h.Lines[i]
			}
		}
	}
	if del == nil || del.Old != 2 || del.Text() != "b" {
		t.Errorf("删除行：%+v", del)
	}
	if add == nil || add.New != 2 || add.Text() != "B" {
		t.Errorf("新增行：%+v", add)
	}
}

func TestDiffRenameKeepsBothPaths(t *testing.T) {
	r, dir := repo(t)
	p, err := r.Diff(context.Background(), Req{Dir: dir, Mode: ModeAll, Path: "sub/c.txt", Old: "sub/b.txt"})
	if err != nil {
		t.Fatal(err)
	}
	f := p.Files[0]
	if f.Kind != KindRename || f.Old != "sub/b.txt" || f.Path != "sub/c.txt" {
		t.Errorf("改名那份补丁：%+v", f)
	}
}

// 未跟踪文件走 `--no-index`，而它**有差异时退出码是 1** —— 当成失败的话表现是
// 「新建的文件永远打不开」。
func TestDiffUntracked(t *testing.T) {
	r, dir := repo(t)
	p, err := r.Diff(context.Background(), Req{Dir: dir, Path: "untracked.txt", Untracked: true})
	if err != nil {
		t.Fatal(err)
	}
	f := p.Files[0]
	if f.Add != 2 || f.Del != 0 {
		t.Errorf("%+v", f)
	}
	if f.Path != "untracked.txt" {
		t.Errorf("路径该是仓库相对的：%q", f.Path)
	}
}

// 这条是**安全**的：`--no-index` 的参数是任意路径，不夹住的话前端传 `../..` 就能
// 把仓库外面的文件读出来。
func TestDiffUntrackedEscape(t *testing.T) {
	r, dir := repo(t)
	outside := filepath.Join(filepath.Dir(dir), "secret.txt")
	if err := os.WriteFile(outside, []byte("s3cret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"../secret.txt", "sub/../../secret.txt", outside} {
		if _, err := r.Diff(context.Background(), Req{Dir: dir, Path: bad, Untracked: true}); err == nil {
			t.Errorf("%q 居然给读出来了", bad)
		}
	}
}

func TestReposDedupe(t *testing.T) {
	r, dir := repo(t)
	got := r.Repos(context.Background(), []string{dir, filepath.Join(dir, "sub"), "/", os.TempDir()})
	if len(got) != 1 || got[0].Root != dir {
		t.Fatalf("同一个仓库的两个子目录该合成一条：%+v", got)
	}

}

// 文件浏览关掉 / 配了 jail 时，这条路要跟着关 —— 同一件事（把这台机器上的文件内容
// 摊到浏览器里），不该有两套边界。
func TestJail(t *testing.T) {
	r, dir := repo(t)
	r.Files = &files.Browser{Enabled: false}
	if _, err := r.Status(context.Background(), dir, ModeAll); err == nil {
		t.Error("HERDR_WEB_FILES=0 时还能看 diff")
	}
	r.Files = &files.Browser{Enabled: true, Roots: []string{t.TempDir()}}
	if _, err := r.Status(context.Background(), dir, ModeAll); err == nil {
		t.Error("仓库不在 FILE_ROOTS 里，还是给看了")
	}
}

// 空仓库（`git init` 完还没提交）：`git diff HEAD` 是直接报错的，得退回「跟空树比」。
func TestUnborn(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("没有 git")
	}
	dir := t.TempDir()
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	git(t, dir, "init", "-q", "-b", "main", ".")
	write(t, dir, "new.txt", "hello\n")
	git(t, dir, "add", "new.txt")
	r := New(&files.Browser{Enabled: true})
	st, err := r.Status(context.Background(), dir, ModeAll)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Repo.Unborn {
		t.Error("还没有提交，Unborn 该是真")
	}
	if c := find(st.Changes, "new.txt"); c == nil || c.Kind != KindAdd {
		t.Errorf("%+v / %v", c, paths(st.Changes))
	}
	if _, err := r.Diff(context.Background(), Req{Dir: dir, Mode: ModeAll, Path: "new.txt"}); err != nil {
		t.Errorf("空仓库里看一个文件的 diff：%v", err)
	}
}

// 角标那份数据：文件数 + 指纹。**指纹要能认出「同一个文件又多改了几行」** ——
// `status --porcelain=v2` 里那两个 blob 哈希是 HEAD 和 index 的，光看它的话
// 工作区里再写十行是一个字都不变的（所以还要一条 shortstat，见 DirtyStat）。
func TestDirtyStat(t *testing.T) {
	r, dir := repo(t)
	ctx := context.Background()
	a, err := r.DirtyStat(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if a.Root != dir || a.Files == 0 {
		t.Fatalf("%+v", a)
	}

	// 缓存：2 秒内再问一次是同一份（开着几个标签页时不该把 git 跑好几遍）
	b, _ := r.DirtyStat(ctx, dir)
	if b.Sig != a.Sig {
		t.Errorf("同一秒内两次问出了两个指纹：%q / %q", a.Sig, b.Sig)
	}

	// 往一个**已经改过**的文件里再写几行 → 指纹必须变
	r.dirty = nil // 把缓存清掉，模拟「过了 2 秒」
	write(t, dir, "a.txt", "a\nB\nc\nd\ne\nf\ng\n")
	c, err := r.DirtyStat(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.Sig == a.Sig {
		t.Error("同一个文件多改了几行，指纹却没变 —— 角标就永远不会再亮")
	}

	// 多一个新文件 → 文件数和指纹都要变
	r.dirty = nil
	write(t, dir, "brand-new.txt", "x\n")
	d, err := r.DirtyStat(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if d.Files != c.Files+1 || d.Sig == c.Sig {
		t.Errorf("新建一个文件之后：%+v（之前 %+v）", d, c)
	}
}

// 干净的仓库：0 个文件。角标据此不画（「有改动吗」这一位得是真的）
func TestDirtyClean(t *testing.T) {
	r, dir := repo(t)
	git(t, dir, "stash", "-u")
	d, err := r.DirtyStat(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if d.Files != 0 {
		t.Errorf("stash 之后还报 %d 个文件改了", d.Files)
	}
}
