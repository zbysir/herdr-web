// Package gitdiff 把 `git diff` 变成手机上读得下去的东西：先一份改动清单，点进去是
// 一份**能折行、按词高亮**的补丁。
//
// # 为什么值得单独做一层，而不是「在终端里跑 git diff」
//
// 用户报的原话是「手机上终端的 git diff 太难看了」。拆开看是三件事，每一件在终端里
// 都没法解决：
//
//  1. **不能折行。** 终端里一行就是一行，手机竖屏只有 40 来列，而 diff 每行前面还要
//     先占掉一格 +/-；长行要么被切掉、要么整屏横滚。这一层把长行折下来。
//  2. **看不出改了哪个词。** 终端里一整行红、一整行绿，一个变量名改了大小写得拿眼睛扫。
//     这儿把配得上对的那些行按**字符**求公共前后缀，只把中间那截标出来（见 parse.go）。
//  3. **翻页只能靠 pager。** less 在触屏上是最难用的东西之一（翻页要点软键条的方向键）。
//
// # 跑 git 的那几条硬规矩（每一条都**静默**出错）
//
//  1. **`-c color.ui=false`。** 用户 `~/.gitconfig` 里写了 `color.ui = always` 的话，
//     每行前面都多一串 ANSI 转义 —— `+` / `-` 前缀于是认不出来，整份补丁被解析成一片
//     上下文：屏幕上是「有内容，但一个改动都没有」。
//  2. **`-c core.quotepath=false`。** 不给的话中文路径会变成 `\344\270\255` 这种八进制，
//     文件列表里一片乱码（`-z` 只管记录分隔符，不管这个转义）。
//  3. **`--no-ext-diff`。** 仓库自己的 config 里能挂外部 diff 程序（`diff.external`），
//     那时候输出压根不是 unified diff，解析出来是空的。
//  4. **`--no-optional-locks`。** `git status` 默认会顺手刷新并**写** index，也就是要去抢
//     `.git/index.lock` —— 而这个面板的对面正有一个 agent 在同一个仓库里跑 git。我们只是
//     看一眼，不该让别人的命令因此失败（而那个失败落在**对方**头上，这边一点症状都没有）。
//  5. **退出码 1 不是失败。** `git diff --no-index`（未跟踪文件走这条）有差异时退出 1。
//     当成失败的表现是「新建的文件永远打不开」。
//  6. **输出要限量，而且要把进程杀掉。** agent 生成一个几百 MB 的文件是常事，`git diff`
//     会老老实实整个吐出来。读到上限就 cancel —— 不 cancel 的话 git 一直阻塞在写管道上
//     （我们已经不读了），那条连接和那个进程一起挂着。
//  7. **`--no-index` 那条路必须自己夹住路径。** 它那两个参数是**任意路径**、不受仓库约束,
//     不夹的话前端传一个 `../../../.ssh/id_rsa` 就把任意文件读出来了。所以未跟踪文件的
//     路径先 Clean 再核「还在仓库根下面」，然后照旧过一遍 files.Check。
//  8. **`GIT_DIR` / `GIT_WORK_TREE` 要从环境里清掉。** 它们会把 `-C` 指定的仓库整个顶掉
//     （herdr-web 可能是从某个设了这些变量的 shell / hook 里起来的），表现是「不管点哪个
//     仓库，看到的都是同一份 diff」。
//
// # 鉴权只有一处
//
// 仓库根目录要过 `files.Browser.Check`：`HERDR_WEB_FILES=0` 时这条路一起关掉（本来就是
// 同一件事 —— 把这台机器上的文件内容摊到浏览器里），配了 `HERDR_WEB_FILE_ROOTS` 时那个
// jail 也照旧生效。不另起一套边界的理由见 internal/files 的包注释。
package gitdiff

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zbysir/herdr-web/internal/files"
)

// 三档「跟谁比」。默认是 ModeAll —— agent 多半根本不 `git add`，人想看的就是
// 「相对上次提交，这个仓库现在长什么样」。
const (
	ModeAll    = "all"    // 工作区 vs HEAD（含未跟踪的新文件）
	ModeStaged = "staged" // 已暂存 vs HEAD
	ModeHead   = "head"   // 最近一次提交自己那一份改动
)

// 限量。三个数各有各的理由：
//
//   - MaxPatch：一次补丁最多读多少字节。超了就在最后一个完整行上切断并说出来。
//   - MaxLines：默认最多给多少行（前端一行一个 DOM 节点，手机上几千行就开始卡）。
//     前端可以要更多，上限 MaxLinesCap。
//   - MaxFiles：清单最多给多少条。
const (
	MaxPatch    = 4 << 20
	MaxLines    = 2000
	MaxLinesCap = 20000
	MaxFiles    = 1000
	// MaxStatus 清单那几条命令的输出上限。十万个文件的 `git status` 也就几 MB。
	MaxStatus = 8 << 20
	// MaxUntracked 未跟踪文件数多少行：直接自己读文件数换行符（不 fork git），
	// 超过这个大小就不数了 —— 只是列表上一个 `+N`，不值得为它读一个大文件。
	MaxUntracked = 2 << 20
)

// DefaultTimeout 一条 git 命令最多跑多久。给得比较松是因为大仓库上 `git status`
// 冷启动真的要好几秒（要 stat 一遍工作区）。
const DefaultTimeout = 20 * time.Second

// Runner 是「在这台机器上跑 git」。
type Runner struct {
	// Exe git 可执行文件。空 = 这台机器上没有 git，整条路关掉（见 Enabled）。
	Exe string
	// Files 是**唯一的鉴权点**：仓库根目录要过它（见包注释）。
	Files   *files.Browser
	Timeout time.Duration

	// 顶栏那个角标要按秒问（见 DirtyStat），所以给它一个很短的缓存：开着两个浏览器、
	// 或者一台设备切来切去时，别把同一个 `git status` 在同一秒里跑好几遍。
	mu    sync.Mutex
	dirty map[string]dirtyAt
}

type dirtyAt struct {
	at time.Time
	d  Dirty
}

// DirtyTTL 角标那份结果留多久。短得只够合并「同一下的重复问」——
// 再长就会出现「已经提交了，红点还挂着」。
const DirtyTTL = 2 * time.Second

// New 查一次 PATH。查不到就返回一个 Enabled() 为假的 Runner —— 前端据此不画那个按钮
// （和文件浏览一样：入口点开是一片报错，比没有入口更糟）。
func New(f *files.Browser) *Runner {
	exe, err := exec.LookPath("git")
	if err != nil {
		exe = ""
	}
	return &Runner{Exe: exe, Files: f}
}

// Enabled 这个部署能不能看 diff。
func (r *Runner) Enabled() bool {
	return r != nil && r.Exe != "" && r.Files != nil && r.Files.Enabled
}

var ErrNoGit = errors.New("这台机器上没有 git（或者文件浏览被关掉了）")

/* ------------------------------------------------------------------ 形状 */

// Repo 是一个仓库的抬头。
type Repo struct {
	Root     string `json:"root"`
	Branch   string `json:"branch,omitempty"` // 摘着头（detached）时是空
	Detached bool   `json:"detached,omitempty"`
	Head     string `json:"head,omitempty"` // 短 hash
	Upstream string `json:"upstream,omitempty"`
	Ahead    int    `json:"ahead,omitempty"`
	Behind   int    `json:"behind,omitempty"`
	// Unborn 还没有任何提交（`git init` 完还没 commit）。这时候「跟 HEAD 比」不成立，
	// 前端据此别去问「上次提交」那一档。
	Unborn bool `json:"unborn,omitempty"`
}

// 一条改动是什么。和 git 的状态字母对得上，但翻成了不用查手册的词。
const (
	KindAdd      = "add"
	KindModify   = "modify"
	KindDelete   = "delete"
	KindRename   = "rename"
	KindCopy     = "copy"
	KindType     = "type" // 文件变成了软链 / 目录之类
	KindConflict = "conflict"
	KindUntrack  = "untracked"
)

// Change 是清单里的一行。
type Change struct {
	Path string `json:"path"`          // 仓库相对路径
	Old  string `json:"old,omitempty"` // 改名前的路径（只有 rename / copy 有）
	Kind string `json:"kind"`
	// Staged / Unstaged 只有 ModeAll 有意义：同一个文件可能一半在暂存区、一半还没。
	// 清单上那两个小角标就是它俩 —— 不显示的话「我明明 add 过了」没有任何线索。
	Staged   bool `json:"staged,omitempty"`
	Unstaged bool `json:"unstaged,omitempty"`
	Add      int  `json:"add"`
	Del      int  `json:"del"`
	Binary   bool `json:"binary,omitempty"`
	// Dir 是**未跟踪的目录**：`git status` 会把一整个新目录折成一条（`newdir/`），
	// 里面有多少东西它不说。这种点开不是 diff，是转到文件面板去翻。
	Dir bool `json:"dir,omitempty"`
}

// Commit 是「上次提交」那一档的抬头。
type Commit struct {
	Hash    string `json:"hash"`
	Short   string `json:"short"`
	Author  string `json:"author"`
	Date    string `json:"date"`
	Subject string `json:"subject"`
	Merge   bool   `json:"merge,omitempty"`
}

// Status 是一次清单请求的全部回答。
type Status struct {
	Repo    Repo     `json:"repo"`
	Mode    string   `json:"mode"`
	Commit  *Commit  `json:"commit,omitempty"`
	Changes []Change `json:"changes"`
	// Truncated 被砍掉多少条。**不为 0 时前端必须说出来** —— 不说的话「就改了这几个文件」
	// 是句假话（和文件浏览那边同一条规矩）。
	Truncated int `json:"truncated,omitempty"`
}

/* ------------------------------------------------------------------ 跑 git */

// spec 是一次 git 调用。写成结构体是因为「允许退出码 1」和「输出上限」这两件事
// 每次都不一样，堆成参数列表读不出谁是谁。
type spec struct {
	dir string
	// args 是**子命令及其之后**的部分；`-C` / `-c` 那些公共开关由 argv 统一加。
	args []string
	// limit 输出上限（字节）。读满就把 git 杀掉，见包注释第 6 条。
	limit int
	// diff 表示「退出码 1 = 有差异」，不是失败（`--no-index` 会这样）。
	diff bool
}

// argv 拼出完整命令行。公共开关都在这儿，每条命令都带 —— 见包注释里那几条硬规矩。
func (r *Runner) argv(s spec) []string {
	out := []string{
		"--no-pager",
		"--no-optional-locks",
		"-c", "color.ui=false",
		"-c", "core.quotepath=false",
		"-C", s.dir,
	}
	return append(out, s.args...)
}

// env 是给 git 的环境。
//
// 清掉所有 `GIT_*` 是关键的一条（见包注释第 8 条）：`GIT_DIR` / `GIT_WORK_TREE` /
// `GIT_INDEX_FILE` 会把 `-C` 指的仓库整个顶掉，而症状是「每个仓库看到的 diff 都一样」。
// 顺带把交互也关死：这几条命令不该有任何机会去问密码（那会把请求挂到超时）。
func env() []string {
	src := os.Environ()
	out := make([]string, 0, len(src)+3)
	for _, kv := range src {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(strings.ToUpper(k), "GIT_") {
			continue
		}
		out = append(out, kv)
	}
	return append(out,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0", // 和 --no-optional-locks 同一件事，两头都写上
		"GIT_PAGER=cat",
	)
}

// run 跑一条 git，返回输出和「是不是读满上限被截断了」。
//
// 用 StdoutPipe 自己读而不是 cmd.Output()：要能在读满上限的那一刻**把 git 杀掉**。
// 交给 os/exec 拷贝的话，我们这边停止读之后 git 会一直阻塞在写管道上，而 Wait 又在等
// 它退出 —— 正好互相等死。
func (r *Runner) run(ctx context.Context, s spec) ([]byte, bool, error) {
	if r.Exe == "" {
		return nil, false, ErrNoGit
	}
	to := r.Timeout
	if to <= 0 {
		to = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, to)
	defer cancel()

	cmd := exec.CommandContext(ctx, r.Exe, r.argv(s)...)
	cmd.Env = env()
	// 杀掉之后再等 1 秒收尸：git 可能起了子进程（比如 textconv 过滤器）。
	cmd.WaitDelay = time.Second
	var errb bytes.Buffer
	cmd.Stderr = &limitWriter{w: &errb, n: 8 << 10}
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, false, err
	}
	if err := cmd.Start(); err != nil {
		return nil, false, err
	}
	limit := s.limit
	if limit <= 0 {
		limit = MaxPatch
	}
	// 多读一个字节：读到 limit+1 才说明真的超了（正好 limit 字节不算截断）。
	out, err := io.ReadAll(io.LimitReader(pipe, int64(limit)+1))
	over := len(out) > limit
	if over {
		out = out[:limit]
		cancel() // 见上面那段：不杀掉就是互相等死
	}
	werr := cmd.Wait()
	if err != nil && !over {
		return nil, false, err
	}
	if over {
		return out, true, nil
	}
	if werr != nil {
		if s.diff && exitCode(werr) == 1 {
			return out, false, nil // `--no-index`：1 = 有差异，不是失败
		}
		if ctx.Err() == context.DeadlineExceeded {
			return nil, false, fmt.Errorf("git 跑了 %s 还没回来（仓库太大？）", to)
		}
		return nil, false, gitErr(werr, errb.String())
	}
	return out, false, nil
}

func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// gitErr 把 git 自己那句话原样带出去。
//
// **值得专门这么做**：这条路上最常见的几种失败（`detected dubious ownership`、
// `not a git repository`、`bad revision`）git 说得比我们清楚得多，而且它那句里
// 往往直接带着修法。糊成「git 执行失败」的话，用户能查的只剩下猜。
func gitErr(err error, stderr string) error {
	msg := strings.TrimSpace(stderr)
	if msg == "" {
		return fmt.Errorf("git 执行失败：%w", err)
	}
	if len(msg) > 2000 {
		msg = msg[:2000] + "…"
	}
	return errors.New(msg)
}

// limitWriter 收 stderr 用：只留前 n 字节，后面的丢掉（不报错 —— stderr 满了不该
// 让整条命令失败）。
type limitWriter struct {
	w io.Writer
	n int
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if l.n > 0 {
		if len(p) > l.n {
			p = p[:l.n]
		}
		l.n -= len(p)
		_, _ = l.w.Write(p)
	}
	return len(p), nil
}

/* ------------------------------------------------------------------ 仓库 */

// Repos 认一批目录里哪些是 git 仓库（前端拿各个 pane 的 cwd 来问）。
//
// 按仓库根去重：好几个 pane 开在同一个仓库的不同子目录里是常态，而人心里那个东西是
// 「仓库」，不是「目录」。**认不出来的不报错**，直接不出现在候选里 —— 这是个探测，
// 不是一次操作。
//
// **只回仓库根，不回分支。** 一次探测就是一个目录一次 fork，而实测有开着 48 个 pane、
// 34 个不同 cwd 的机器 —— 每个根再问一次分支就是又几十次 fork，而那个分支名前端在选择器里
// 压根没画（抬头上那个是选定之后 status 一起回来的）。
func (r *Runner) Repos(ctx context.Context, dirs []string) []Repo {
	if !r.Enabled() {
		return nil
	}
	seen := map[string]bool{}
	out := []Repo{}
	for _, d := range dirs {
		if d = strings.TrimSpace(d); d == "" {
			continue
		}
		abs, err := files.Resolve(d, "")
		if err != nil {
			continue
		}
		repo, err := r.open(ctx, abs)
		if err != nil || seen[repo.Root] {
			continue
		}
		seen[repo.Root] = true
		out = append(out, repo)
	}
	return out
}

// open 从任意目录找到仓库根，并**过一遍鉴权**。
//
// **只问 `--show-toplevel`，分支另说。** 顺手加个 `--abbrev-ref HEAD` 看着能省一次
// fork，但那在**还没有第一次提交**的仓库上整条命令都失败（`ambiguous argument 'HEAD'`）——
// 于是 `git init` 完还没 commit 的目录会变成「这不是 git 仓库」。分支名反正 `git status`
// 那一条里就有（`# branch.head`），Repos 那边才需要单独问一次。
func (r *Runner) open(ctx context.Context, dir string) (Repo, error) {
	if !r.Enabled() {
		return Repo{}, ErrNoGit
	}
	out, _, err := r.run(ctx, spec{
		dir:   dir,
		args:  []string{"rev-parse", "--show-toplevel"},
		limit: 64 << 10,
	})
	if err != nil {
		return Repo{}, err
	}
	root := strings.TrimSpace(string(out))
	if root == "" {
		return Repo{}, fmt.Errorf("%s 不在一个 git 仓库里", dir)
	}
	repo := Repo{Root: filepath.Clean(root)}
	// 鉴权在这儿，且只有这一处：根目录过了，里面所有文件就都在范围内（jail 是按前缀算的）。
	if err := r.Files.Check(repo.Root); err != nil {
		return Repo{}, err
	}
	return repo, nil
}

/* ------------------------------------------------------------------ 清单 */

// Status 出一份改动清单。
func (r *Runner) Status(ctx context.Context, dir, mode string) (*Status, error) {
	repo, err := r.open(ctx, dir)
	if err != nil {
		return nil, err
	}
	mode = normMode(mode)
	st := &Status{Repo: repo, Mode: mode, Changes: []Change{}}

	// 未跟踪的只有「工作区」那一档要扫。别的档给 -uno：大仓库上扫一遍未跟踪文件是
	// `git status` 最贵的一段，而那一档根本用不上。
	untracked := mode == ModeAll
	if err := r.readStatus(ctx, st, untracked); err != nil {
		return nil, err
	}

	base := "HEAD"
	if st.Repo.Unborn {
		// 还没有任何提交：跟 HEAD 比不成立。index 那一档 git 自己会拿空树比，
		// 所以这儿把 base 去掉，`git diff --cached` 照样工作。
		base = ""
		if mode == ModeHead {
			return st, nil // 没有「上次提交」可看
		}
	}

	var nameArgs, numArgs []string
	switch mode {
	case ModeStaged:
		nameArgs = diffArgs("--name-status", "--cached", base)
		numArgs = diffArgs("--numstat", "--cached", base)
	case ModeHead:
		nameArgs = showArgs("--name-status")
		numArgs = showArgs("--numstat")
		if st.Commit, err = r.commit(ctx, repo.Root); err != nil {
			return nil, err
		}
	default: // ModeAll
		if base == "" {
			// 空仓库：工作区那一档退化成「index 里有什么」+ 未跟踪
			nameArgs = diffArgs("--name-status", "--cached", "")
			numArgs = diffArgs("--numstat", "--cached", "")
		} else {
			nameArgs = diffArgs("--name-status", "", base)
			numArgs = diffArgs("--numstat", "", base)
		}
	}

	nameOut, _, err := r.run(ctx, spec{dir: repo.Root, args: nameArgs, limit: MaxStatus})
	if err != nil {
		return nil, err
	}
	numOut, _, err := r.run(ctx, spec{dir: repo.Root, args: numArgs, limit: MaxStatus})
	if err != nil {
		return nil, err
	}
	merge(st, parseNameStatus(nameOut), parseNumstat(numOut), mode)

	if untracked {
		r.fillUntracked(st.Repo.Root, st.Changes)
	}
	if len(st.Changes) > MaxFiles {
		st.Truncated = len(st.Changes) - MaxFiles
		st.Changes = st.Changes[:MaxFiles]
	}
	return st, nil
}

func normMode(m string) string {
	switch m {
	case ModeStaged, ModeHead:
		return m
	default:
		return ModeAll
	}
}

// diffArgs 一条 `git diff`。`-M` 认改名（不认的话一次改名在清单上是「删一个 + 加一个」，
// 而那两条点进去都是整个文件，读的人得自己对）。
func diffArgs(format, cached, base string) []string {
	a := []string{"diff", "--no-ext-diff", "-M", "-z", format}
	if cached != "" {
		a = append(a, cached)
	}
	if base != "" {
		a = append(a, base)
	}
	return a
}

// showArgs 「上次提交」那一档。
//
// `-m --first-parent` 是给 merge 提交准备的：不给的话 `git show` 对一个 merge
// **什么都不输出**（合并提交默认不展开），屏幕上就是「这次提交没改任何文件」。
func showArgs(format string) []string {
	return []string{"show", "--no-ext-diff", "-M", "-m", "--first-parent", "--format=", "-z", format, "HEAD"}
}

// commit 取「上次提交」的抬头。用 `-s`（不出 diff）单独问一次：把它和 numstat 挤在
// 同一条命令里的话，两段输出之间的分隔要靠数换行，而 `-z` 之后那个边界很难说清。
func (r *Runner) commit(ctx context.Context, root string) (*Commit, error) {
	out, _, err := r.run(ctx, spec{
		dir:   root,
		args:  []string{"show", "-s", "--format=%H%x00%h%x00%an%x00%aI%x00%s%x00%P", "HEAD"},
		limit: 64 << 10,
	})
	if err != nil {
		return nil, err
	}
	f := strings.Split(strings.TrimRight(string(out), "\n"), "\x00")
	for len(f) < 6 {
		f = append(f, "")
	}
	return &Commit{
		Hash: f[0], Short: f[1], Author: f[2], Date: f[3], Subject: f[4],
		Merge: len(strings.Fields(f[5])) > 1,
	}, nil
}

// readStatus 跑 `git status --porcelain=v2 --branch -z`，填分支信息、暂存/未暂存
// 两个角标、以及未跟踪的那些条目。
func (r *Runner) readStatus(ctx context.Context, st *Status, untracked bool) error {
	u := "--untracked-files=no"
	if untracked {
		u = "--untracked-files=normal"
	}
	out, over, err := r.run(ctx, spec{
		dir:   st.Repo.Root,
		args:  []string{"status", "--porcelain=v2", "--branch", "-z", u},
		limit: MaxStatus,
	})
	if err != nil {
		return err
	}
	_ = over // 十万个文件也到不了 8MB；真到了下面的 MaxFiles 也会兜住
	parseStatus(out, st)
	return nil
}

// fillUntracked 给未跟踪的**文件**数一下有多少行。
//
// 自己读文件而不是 fork 一次 `git diff --no-index --numstat`：未跟踪文件常常是几十个
// （agent 刚生成的一批），一个一个 fork 太贵。数不出来（太大 / 二进制 / 读不了）就
// 留 0，前端那一行只画一个「新」。
func (r *Runner) fillUntracked(root string, cs []Change) {
	for i := range cs {
		c := &cs[i]
		if c.Kind != KindUntrack || c.Dir {
			continue
		}
		p, err := Join(root, c.Path)
		if err != nil {
			continue
		}
		n, bin := countLines(p)
		c.Add, c.Binary = n, bin
	}
}

func countLines(p string) (int, bool) {
	f, err := os.Open(p)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() || fi.Size() > MaxUntracked {
		return 0, false
	}
	buf := make([]byte, 32<<10)
	n, first := 0, true
	for {
		k, err := f.Read(buf)
		if k > 0 {
			if first {
				first = false
				if bytes.IndexByte(buf[:min(k, 512)], 0) >= 0 {
					return 0, true // 有 NUL 字节 = 二进制，行数没有意义
				}
			}
			n += bytes.Count(buf[:k], []byte{'\n'})
		}
		if err != nil {
			return n, false
		}
	}
}

// merge 把三份数据合成一份清单：
//
//	name-status  这个文件发生了什么（增 / 删 / 改 / 改名）—— **只有它说得准**
//	numstat      加了几行删了几行
//	status v2    暂存 / 未暂存两个角标 + 未跟踪的条目
//
// 为什么不从 status 的 XY 两个字母推 Kind：`AD`（暂存了新增、工作区又删了）这类组合
// 落到「跟 HEAD 比」上到底算什么，得一格一格地编，而 name-status 本来就是那个答案。
func merge(st *Status, named []Change, nums map[string]numstat, mode string) {
	// status v2 先扫出来的那些（未跟踪 + 角标）按路径索引
	flags := map[string]Change{}
	untrackedRows := []Change{}
	for _, c := range st.Changes {
		if c.Kind == KindUntrack {
			untrackedRows = append(untrackedRows, c)
			continue
		}
		flags[c.Path] = c
	}
	out := make([]Change, 0, len(named)+len(untrackedRows))
	for _, c := range named {
		// 「上次提交」那一档不带角标：那两个说的是**现在**工作区的状态，而这一档列的是
		// 一次已经过去的提交里的文件 —— 挂上去只会让人以为它们说的是同一件事。
		if f, ok := flags[c.Path]; ok && mode != ModeHead {
			c.Staged, c.Unstaged = f.Staged, f.Unstaged
		}
		key := c.Path
		if c.Old != "" {
			key = c.Old + "\x00" + c.Path // numstat 里改名是「旧 + 新」两段
		}
		if n, ok := nums[key]; ok {
			c.Add, c.Del, c.Binary = n.add, n.del, n.binary
		}
		out = append(out, c)
	}
	out = append(out, untrackedRows...)
	st.Changes = out
}

/* ------------------------------------------------------------------ 角标 */

// Dirty 是顶栏那个角标要的全部东西：几个文件改了 + 一个「和上次比变没变」的指纹。
type Dirty struct {
	Root  string `json:"root"`
	Files int    `json:"files"`
	// Sig 是这份改动的指纹。前端拿它和「上次看过的那个」比 —— 角标要回答的是
	// **「有你还没看过的改动吗」**，不是「有改动吗」：后者在一个正干活的仓库里永远为真，
	// 那个点就等于一直亮着，没有信息量（提示红点那边也是这么定的）。
	Sig string `json:"sig"`
}

// DirtyStat 出一份角标数据。**两次 git**（status + shortstat），带 2 秒缓存。
//
// 为什么要 shortstat：`status --porcelain=v2` 里那两个 blob 哈希是 HEAD 和 index 的，
// agent 往一个**已经改过**的文件里再写十行，那份输出一个字都不变 —— 只看它的话，
// 「又改了很多」这件事在角标上是看不见的。shortstat 那行（几个文件、加了几行、删了几行）
// 正好补上这一位。
func (r *Runner) DirtyStat(ctx context.Context, dir string) (*Dirty, error) {
	repo, err := r.open(ctx, dir)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	if e, ok := r.dirty[repo.Root]; ok && time.Since(e.at) < DirtyTTL {
		d := e.d
		r.mu.Unlock()
		return &d, nil
	}
	r.mu.Unlock()

	out, _, err := r.run(ctx, spec{
		dir:   repo.Root,
		args:  []string{"status", "--porcelain=v2", "--branch", "-z", "--untracked-files=normal"},
		limit: MaxStatus,
	})
	if err != nil {
		return nil, err
	}
	st := &Status{Repo: repo}
	parseStatus(out, st)

	var short []byte
	if !st.Repo.Unborn {
		// 出错就当没有（比如仓库正处在一个奇怪的状态）—— 角标不该因此整个失效
		short, _, _ = r.run(ctx, spec{
			dir: repo.Root, args: []string{"diff", "--no-ext-diff", "--shortstat", "HEAD"}, limit: 64 << 10,
		})
	}

	d := Dirty{Root: repo.Root, Files: len(st.Changes), Sig: sigOf(out, short)}
	r.mu.Lock()
	if r.dirty == nil {
		r.dirty = map[string]dirtyAt{}
	}
	r.dirty[repo.Root] = dirtyAt{at: time.Now(), d: d}
	r.mu.Unlock()
	return &d, nil
}

func sigOf(parts ...[]byte) string {
	h := fnv.New64a()
	for _, p := range parts {
		_, _ = h.Write(p)
		_, _ = h.Write([]byte{0})
	}
	return strconv.FormatUint(h.Sum64(), 36)
}

/* ------------------------------------------------------------------ 补丁 */

// Patch 是一个文件的 diff。
type Patch struct {
	Files []File `json:"files"`
	// Over 输出撞到了字节上限（`MaxPatch`）—— 后面还有，但不给了。
	Over  bool `json:"over,omitempty"`
	Limit int  `json:"limit"`
}

// Req 是「要哪一份 diff」。
type Req struct {
	Dir  string
	Mode string
	Path string
	// Old 改名前的路径。要一起传：只给新路径的话 `git diff -- <新>` 出来的是
	// 「整个文件都是新加的」，改名那件事看不出来。
	Old string
	// Untracked 走 `--no-index` 那条（整份当新增看）。
	Untracked bool
	// Context 上下文行数（`-U`）。0 就是 git 的默认 3。
	Context int
	// Limit 最多给多少行。0 = MaxLines。
	Limit int
}

// Diff 出一个文件的补丁。
func (r *Runner) Diff(ctx context.Context, q Req) (*Patch, error) {
	repo, err := r.open(ctx, q.Dir)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(q.Path) == "" {
		return nil, errors.New("没说要看哪个文件")
	}
	limit := q.Limit
	if limit <= 0 {
		limit = MaxLines
	}
	limit = min(limit, MaxLinesCap)

	args, err := r.diffCmd(repo.Root, q)
	if err != nil {
		return nil, err
	}
	out, over, err := r.run(ctx, spec{dir: repo.Root, args: args, limit: MaxPatch, diff: true})
	if err != nil {
		return nil, err
	}
	if over {
		out = cutLine(out)
	}
	fs := Parse(out, limit)
	if q.Untracked {
		// `--no-index` 那条路里两边给的是**绝对路径**，于是补丁头里写的也是绝对路径
		// （`+++ b/private/var/folders/…`）。前端拿这个当文件名会摊出一条长得吓人的路径，
		// 而它本来就该显示成仓库里的相对路径 —— 这儿改回来。
		for i := range fs {
			fs[i].Path, fs[i].Old, fs[i].Kind = q.Path, "", KindAdd
		}
	}
	p := &Patch{Files: fs, Over: over, Limit: limit}
	// 一个文件都没解析出来，但 git 确实回了东西：多半是我们没认出来的头（比如
	// `GIT binary patch`）。给一条空壳，别让前端显示「没有改动」。
	if len(p.Files) == 0 {
		p.Files = []File{{Path: q.Path, Old: q.Old, Kind: kindGuess(q), Hunks: []Hunk{}}}
	}
	return p, nil
}

func kindGuess(q Req) string {
	if q.Untracked {
		return KindAdd
	}
	if q.Old != "" {
		return KindRename
	}
	return KindModify
}

// diffCmd 拼出「这一档要跑的 git diff」。
func (r *Runner) diffCmd(root string, q Req) ([]string, error) {
	u := "-U3"
	if q.Context > 0 {
		u = "-U" + strconv.Itoa(min(q.Context, 99))
	}
	if q.Untracked {
		// 未跟踪的文件不在 index 里，`git diff` 看不见它 —— 只能用 `--no-index`
		// 拿它和 /dev/null 比。**这条路的参数是任意路径**，所以先夹住（见包注释第 7 条）。
		abs, err := Join(root, q.Path)
		if err != nil {
			return nil, err
		}
		if err := r.Files.Check(abs); err != nil {
			return nil, err
		}
		return []string{"diff", "--no-ext-diff", "--no-index", u, "--", os.DevNull, abs}, nil
	}
	paths := []string{q.Path}
	if q.Old != "" {
		paths = []string{q.Old, q.Path} // 改名：两头都给，git 才认得出是同一个文件
	}
	switch normMode(q.Mode) {
	case ModeStaged:
		return append([]string{"diff", "--no-ext-diff", "-M", u, "--cached", "--"}, paths...), nil
	case ModeHead:
		return append([]string{"show", "--no-ext-diff", "-M", "-m", "--first-parent", "--format=", u, "HEAD", "--"}, paths...), nil
	default:
		a := []string{"diff", "--no-ext-diff", "-M", u}
		if !r.unborn(root) {
			a = append(a, "HEAD")
		}
		return append(append(a, "--"), paths...), nil
	}
}

// unborn 还没有第一次提交吗。`git diff HEAD` 在那时候是直接报错的。
func (r *Runner) unborn(root string) bool {
	out, _, err := r.run(context.Background(), spec{
		dir: root, args: []string{"rev-parse", "--verify", "-q", "HEAD"}, limit: 4 << 10, diff: true,
	})
	return err != nil || strings.TrimSpace(string(out)) == ""
}

// cutLine 截断之后把最后那半行去掉 —— 半行 diff 解析出来是一条假的改动。
func cutLine(b []byte) []byte {
	if i := bytes.LastIndexByte(b, '\n'); i >= 0 {
		return b[:i+1]
	}
	return nil
}

// Join 把仓库相对路径拼成绝对路径，并**夹在仓库里面**。
//
// 这是 `--no-index` 那条路的安全边界（见包注释第 7 条）：路径是前端传过来的，
// `../../..` 一路能走到任何地方，而那条命令不受仓库约束。
func Join(root, rel string) (string, error) {
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("%q 应该是仓库里的相对路径", rel)
	}
	p := filepath.Clean(filepath.Join(root, rel))
	if p != root && !strings.HasPrefix(p, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("%q 不在这个仓库里", rel)
	}
	return p, nil
}
