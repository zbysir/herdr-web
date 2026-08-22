// Package files 文件浏览：把跑 herdr 那台机器上的文件摊到浏览器里 —— 主要是为了
// **看 agent 刚生成的那张图**。
//
// # 为什么默认不设 jail
//
// 能打开这个页面的人已经有一个登录 shell（`/pty`），在里面 `cat` 任何文件都行。
// 所以白名单挡不住这个人，只会天天挡路 —— agent 往 `/tmp`、`/var/folders/…`、
// `~/Downloads` 写文件是常态，而这些目录和「当前 workspace」没有任何关系，这个功能
// 要解决的恰恰就是那种情况。Roots 为空 = 不设边界；配了 `HERDR_WEB_FILE_ROOTS`
// 才变成**真**白名单（给暴露到公网 / 多人共用的部署留的口子）。
//
// # 真正要防的是这四件事（都和路径范围无关）
//
//  1. **绝不以 text/html 吐内容。** 同源的 HTML 就是一个能调 `/api/herdr/say` 的
//     跳板：agent 写一个 html、你点开，等于它拿到了你的 herdr。所以这里按**魔数**
//     认类型，只有认出来的图允许 inline，其余一律 attachment + octet-stream。
//
//     **SVG 是个特例，值得单独说。** 它没有魔数（就是 XML），而且**能跑脚本** ——
//     所以它安不安全，全看它以什么身份被渲染：
//     - 查看器里走 `<img src=…>`：规范规定的 secure static mode，脚本一律不跑、
//     外部资源一律不加载。这条**不依赖任何响应头**。
//     - 「在新标签打开」是顶层文档：靠 `/_f/` 上那条 CSP `sandbox`（没有
//     allow-scripts 就是不给执行，而且源是 opaque 的，跑了也碰不到我们的 API），
//     外加一条只给 SVG 的 `default-src 'none'` 把外链也堵掉。
//     两条路各自独立成立，所以才敢认它。见 server/filesapi.go 的 svgCSP。
//
//  2. **只读常规文件**（IsRegular）。少这一条，点进 `/dev/zero` 就是一条无限流，
//     点 `/dev/rdisk0` 更糟。目录里照样把它们列出来（列表要说实话），只是打不开。
//
//  3. **目录列表要限条数**。一个二十万文件的目录能把手机浏览器卡死。
//
//  4. **配了 Roots 时，前缀检查必须在 EvalSymlinks 之后**，而且比的是
//     `root + 分隔符` —— 少任何一条都是**静默放行**：symlink 能从 jail 里指出去，
//     纯前缀比较会让 `/home/user` 放行 `/home/user2`。
package files

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

// MaxEntries 一个目录最多回多少条。被截掉的条数会在响应里说清楚 —— 静默截断会让
// 「这个目录里没有那张图」变成一句假话。
const MaxEntries = 2000

// MaxText 文本预览最多读多少。超了就截断并标出来；这是个预览，不是编辑器。
const MaxText = 512 << 10

// sniffLen 认类型读多少字节。512 够所有魔数，也够判断「这是不是文本」。
const sniffLen = 512

var ErrDisabled = errors.New("文件浏览被关掉了（HERDR_WEB_FILES=0）")

// Browser 是一次部署的文件浏览策略。
type Browser struct {
	Enabled bool
	// Roots 空 = 不设边界（默认）；非空 = 真白名单，只能看这几棵树。
	Roots []string
	// Home / Tmp / Uploads 只用来生成「起点」列表，不参与鉴权。
	Home    string
	Tmp     string
	Uploads string
}

// Jailed 说这个部署到底有没有边界。前端拿它决定要不要显示「只能看这几个目录」。
func (b *Browser) Jailed() bool { return len(b.Roots) > 0 }

// pathErr 把 os 那边的英文错误换成一句能看懂的中文，**同时保住哨兵**
// （HTTP 层靠 errors.Is(err, fs.ErrNotExist) 分 404 / 403，见 server/filesapi.go）。
//
// 为什么值得多这一层：这个功能最常见的失败就是「终端里那行路径被折行断开 / 被 TUI
// 用 … 截断了」，而用户看到的应该是**解析之后的绝对路径**加一句人话，
// 不是 `stat /a/b: no such file or directory`。
type pathErr struct {
	msg string
	err error
}

func (e *pathErr) Error() string { return e.msg }
func (e *pathErr) Unwrap() error { return e.err }

func nice(p string, err error) error {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return &pathErr{msg: "找不到 " + p, err: err}
	case errors.Is(err, fs.ErrPermission):
		return &pathErr{msg: "没权限读 " + p, err: err}
	}
	return err
}

/* ------------------------------------------------------------------ 解析 */

// Resolve 把一段用户 / 终端给的路径变成绝对路径。
//
// base 是相对路径的解析基准 —— 终端里点到 `./out/chart.png` 时传的是**那个 pane 的
// cwd**（herdr 的 pane.list 里就有）。没有 base 就不认相对路径：猜一个基准出来，
// 错的时候会安静地打开另一个同名文件。
func Resolve(p, base string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", errors.New("路径是空的")
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("展不开 ~：%w", err)
		}
		p = filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	if !filepath.IsAbs(p) {
		if base == "" {
			return "", fmt.Errorf("%q 是相对路径，但不知道该相对哪儿（要带上那个 pane 的 cwd）", p)
		}
		if !filepath.IsAbs(base) {
			return "", fmt.Errorf("基准目录 %q 不是绝对路径", base)
		}
		p = filepath.Join(base, p)
	}
	return filepath.Clean(p), nil
}

// Check 是唯一的鉴权点：所有摸磁盘的口都得先过它。
//
// Roots 为空就直接放行（见包注释）。配了 Roots 时**先 EvalSymlinks 再比前缀**，
// 而且比 `root + 分隔符`：
//
//   - 不解 symlink：jail 里放一个指向 / 的软链就出去了；
//   - 不带分隔符：root=/home/user 会把 /home/user2 也放进来。
//
// 解不开（路径不存在 / 没权限）时**直接拒**，不退回未解析的路径 —— 那等于把上面
// 两条防线在「恰好解不开」的时候全关掉。
func (b *Browser) Check(p string) error {
	if !b.Enabled {
		return ErrDisabled
	}
	if !filepath.IsAbs(p) {
		return fmt.Errorf("%q 不是绝对路径", p)
	}
	if len(b.Roots) == 0 {
		return nil
	}
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return nice(p, err)
	}
	for _, root := range b.Roots {
		rr, err := filepath.EvalSymlinks(root)
		if err != nil {
			continue // 配了个不存在的 root，跳过（启动时已经警告过）
		}
		if real == rr || strings.HasPrefix(real, rr+string(os.PathSeparator)) {
			return nil
		}
	}
	return fmt.Errorf("%s 不在允许的目录里（HERDR_WEB_FILE_ROOTS 限定了 %s）",
		p, strings.Join(b.Roots, "、"))
}

/* ------------------------------------------------------------------ 类型 */

// Kind 是「这个东西能怎么看」，不是文件格式。
const (
	KindDir     = "dir"
	KindImage   = "image"   // 魔数认出来的 png/jpg/gif/webp，能 inline 渲染
	KindText    = "text"    // 采样看着像 UTF-8 文本，走 /api/files/text 预览
	KindBinary  = "binary"  // 只能下载
	KindSpecial = "special" // 设备 / socket / fifo —— 列出来但打不开
)

// imageMIME 按**魔数**认图。不信扩展名也不信 content-type：这两个都是随手能改的，
// 而 inline 渲染一个「其实不是图」的东西正是要防的那件事。
//
// SVG 不在这儿 —— 它没有魔数，见 isSVG。
func imageMIME(b []byte) string {
	switch {
	case len(b) > 8 && bytes.Equal(b[:8], []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}):
		return "image/png"
	case len(b) > 3 && b[0] == 0xff && b[1] == 0xd8 && b[2] == 0xff:
		return "image/jpeg"
	case len(b) > 6 && (bytes.Equal(b[:6], []byte("GIF87a")) || bytes.Equal(b[:6], []byte("GIF89a"))):
		return "image/gif"
	case len(b) > 12 && bytes.Equal(b[:4], []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WEBP")):
		return "image/webp"
	}
	return ""
}

// SVGMIME 单独拎出来：HTTP 那层要认它来决定发哪条 CSP（见 server/filesapi.go）。
const SVGMIME = "image/svg+xml"

// isSVG：SVG 没有魔数（它就是 XML），只能看开头像不像。
//
// 判据故意保守：BOM / 空白之后第一个字符必须是 `<`，采样里出现 `<svg`，而且
// **不能出现 `<html`** —— 「一份 HTML 里顺手嵌了个 svg」必须走附件那条路，那才是真
// 危险的东西。认错的代价不对称：把 svg 认成文本只是看到源码，把 html 认成 svg 就是
// 把一个能跑脚本的文档 inline 出去了。
//
// 采样只有 sniffLen 字节，所以开头挂着一长串注释 / DOCTYPE 的 svg 会认不出来，退回
// 文本预览。这个代价可以接受：宁可少认，不要多认。
func isSVG(b []byte) bool {
	t := bytes.TrimLeft(bytes.TrimPrefix(b, []byte{0xef, 0xbb, 0xbf}), " \t\r\n")
	if len(t) == 0 || t[0] != '<' {
		return false
	}
	low := bytes.ToLower(t)
	return !bytes.Contains(low, []byte("<html")) && bytes.Contains(low, []byte("<svg"))
}

// looksText：这一段采样像不像 UTF-8 文本。判据是「没有 NUL 且是合法 UTF-8」。
//
// truncated 说采样是被 sniffLen 截出来的（不是整个文件）。那时候**最后那个字符很
// 可能被切成半个**，所以最多回退 3 字节（UTF-8 一个字符最长 4 字节）再验 —— 不回退
// 的话，每个尾部恰好落在多字节字符中间的中文文件都会被判成二进制。这个坑很容易漏：
// 纯 ASCII 文件永远不会触发它。
func looksText(b []byte, truncated bool) bool {
	if bytes.IndexByte(b, 0) >= 0 {
		return false
	}
	if truncated {
		for i := 0; i < 3 && len(b) > 0 && !utf8.Valid(b); i++ {
			b = b[:len(b)-1]
		}
	}
	return utf8.Valid(b)
}

// extKind 按扩展名粗判，**只用在目录列表上**（两千个文件不可能一个个去读魔数）。
// 真正打开时会重新按内容认（see Peek），所以这里判错了最多是图标画错。
var textExt = map[string]bool{
	".txt": true, ".md": true, ".markdown": true, ".log": true, ".json": true, ".jsonl": true,
	".yaml": true, ".yml": true, ".toml": true, ".ini": true, ".conf": true, ".cfg": true,
	".csv": true, ".tsv": true, ".xml": true, ".html": true, ".htm": true, ".css": true,
	".js": true, ".jsx": true, ".ts": true, ".tsx": true, ".mjs": true, ".cjs": true,
	".go": true, ".mod": true, ".sum": true, ".rs": true, ".py": true, ".rb": true,
	".java": true, ".kt": true, ".swift": true, ".c": true, ".h": true, ".cc": true,
	".cpp": true, ".hpp": true, ".m": true, ".mm": true, ".cs": true, ".php": true,
	".sh": true, ".bash": true, ".zsh": true, ".fish": true, ".sql": true, ".proto": true,
	".gitignore": true, ".env": true, ".patch": true, ".diff": true, ".lock": true,
}

var imageExt = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".svg": true}

func extKind(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	switch {
	case imageExt[ext]:
		return KindImage
	case textExt[ext]:
		return KindText
	// 没有扩展名的常见是 README / Makefile / Dockerfile 这类纯文本
	case ext == "" && name != "":
		return KindText
	}
	return KindBinary
}

/* ------------------------------------------------------------------ 列目录 */

type Entry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Dir   bool   `json:"dir"`
	Size  int64  `json:"size"`
	Mtime int64  `json:"mtime"` // unix 毫秒
	Kind  string `json:"kind"`
	Link  bool   `json:"link,omitempty"` // 是 symlink（点进去会跳到别处，值得标一下）
}

type Listing struct {
	Path string `json:"path"`
	// Parent 空 = 没有上一级可去（已经到 / 了，或者上一级在 jail 外面）
	Parent  string  `json:"parent"`
	Entries []Entry `json:"entries"`
	// Truncated 被砍掉多少条。不为 0 时前端必须说出来，不然「这儿没有那张图」是句假话
	Truncated int `json:"truncated"`
	// Hidden 有多少条点开头的被过滤了（用户可以选择显示）
	Hidden int `json:"hidden"`
}

// List 列一个目录。sortBy 是 "mtime"（默认）或 "name"。
//
// 默认按改动时间倒序而不是名字：这个功能存在的理由就是「找 agent 刚生成的那个文件」，
// 按名字排的话它埋在几百行里。目录永远排在最前面并按名字排 —— 目录是导航，不是内容。
//
// 排序在**截断之前**做，所以 sort 切换出来的是不同的一批，不是同一批换个顺序。
func (b *Browser) List(p, sortBy string, all bool) (*Listing, error) {
	if err := b.Check(p); err != nil {
		return nil, err
	}
	st, err := os.Stat(p)
	if err != nil {
		return nil, nice(p, err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("%s 不是目录", p)
	}
	des, err := os.ReadDir(p)
	if err != nil {
		return nil, nice(p, err)
	}

	out := &Listing{Path: p}
	dirs := make([]Entry, 0, 16)
	fils := make([]Entry, 0, len(des))
	for _, de := range des {
		name := de.Name()
		if !all && strings.HasPrefix(name, ".") {
			out.Hidden++
			continue
		}
		full := filepath.Join(p, name)
		e := Entry{Name: name, Path: full, Link: de.Type()&fs.ModeSymlink != 0}
		// symlink 要 Stat（跟随）才知道指向的是不是目录；其余用 DirEntry 自带的类型，
		// 省掉每个文件一次 syscall。
		info, err := de.Info()
		if e.Link {
			if si, serr := os.Stat(full); serr == nil {
				info, err = si, nil
			}
		}
		if err != nil {
			continue // 列的这一刻文件没了，跳过就是了
		}
		e.Size, e.Mtime = info.Size(), info.ModTime().UnixMilli()
		switch {
		case info.IsDir():
			e.Dir, e.Kind = true, KindDir
			dirs = append(dirs, e)
			continue
		case !info.Mode().IsRegular():
			e.Kind = KindSpecial // 设备 / socket / fifo：列出来，但 Open 会拒
		default:
			e.Kind = extKind(name)
		}
		fils = append(fils, e)
	}

	sort.Slice(dirs, func(i, j int) bool { return lessName(dirs[i].Name, dirs[j].Name) })
	if sortBy == "name" {
		sort.Slice(fils, func(i, j int) bool { return lessName(fils[i].Name, fils[j].Name) })
	} else {
		sort.Slice(fils, func(i, j int) bool {
			if fils[i].Mtime != fils[j].Mtime {
				return fils[i].Mtime > fils[j].Mtime
			}
			return lessName(fils[i].Name, fils[j].Name)
		})
	}

	out.Entries = append(dirs, fils...)
	if len(out.Entries) > MaxEntries {
		out.Truncated = len(out.Entries) - MaxEntries
		out.Entries = out.Entries[:MaxEntries]
	}

	// 上一级：到根了就没有，jail 挡住了也没有 —— 给一个点了报错的 ".." 不如不给
	if parent := filepath.Dir(p); parent != p && b.Check(parent) == nil {
		out.Parent = parent
	}
	return out, nil
}

// lessName 忽略大小写排，同名再按原样定序（否则 README 和 readme 的先后每次都变）。
func lessName(a, c string) bool {
	la, lc := strings.ToLower(a), strings.ToLower(c)
	if la != lc {
		return la < lc
	}
	return a < c
}

/* ------------------------------------------------------------------ 认一个文件 */

type Info struct {
	Path   string `json:"path"`
	Name   string `json:"name"`
	Dir    bool   `json:"dir"`
	Size   int64  `json:"size"`
	Mtime  int64  `json:"mtime"`
	Kind   string `json:"kind"`
	Mime   string `json:"mime,omitempty"` // 只有 KindImage 有
	Parent string `json:"parent,omitempty"`
}

// Peek 认一个路径是什么。**按内容认，不按扩展名**（扩展名只在目录列表里用）。
func (b *Browser) Peek(p string) (*Info, error) {
	if err := b.Check(p); err != nil {
		return nil, err
	}
	st, err := os.Stat(p)
	if err != nil {
		return nil, nice(p, err)
	}
	out := &Info{
		Path: p, Name: filepath.Base(p),
		Size: st.Size(), Mtime: st.ModTime().UnixMilli(),
	}
	if parent := filepath.Dir(p); parent != p && b.Check(parent) == nil {
		out.Parent = parent
	}
	switch {
	case st.IsDir():
		out.Dir, out.Kind = true, KindDir
		return out, nil
	case !st.Mode().IsRegular():
		// /dev/zero 这种：绝不能读它来认类型，那是一条无限流
		out.Kind = KindSpecial
		return out, nil
	}

	f, err := os.Open(p)
	if err != nil {
		return nil, nice(p, err)
	}
	defer f.Close()
	head := make([]byte, sniffLen)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, err
	}
	head = head[:n]

	if mime := imageMIME(head); mime != "" {
		out.Kind, out.Mime = KindImage, mime
	} else if isSVG(head) {
		out.Kind, out.Mime = KindImage, SVGMIME
	} else if looksText(head, n == sniffLen) {
		out.Kind = KindText
	} else {
		out.Kind = KindBinary
	}
	return out, nil
}

/* ------------------------------------------------------------------ 读内容 */

type Text struct {
	Path      string `json:"path"`
	Text      string `json:"text"`
	Bytes     int64  `json:"bytes"`
	Truncated bool   `json:"truncated"`
}

// ReadText 读文本预览。
//
// 走 JSON 而不是「按 text/plain 吐出去」是刻意的：内容一旦从本站的源上以某个
// content-type 出去，就得赌浏览器不会拿它当别的东西执行。塞进 JSON 里、由前端画进
// `<pre>`，这条路上根本没有「浏览器解释这段内容」的环节。
func (b *Browser) ReadText(p string) (*Text, error) {
	info, err := b.Peek(p)
	if err != nil {
		return nil, err
	}
	if info.Dir {
		return nil, fmt.Errorf("%s 是目录", p)
	}
	if info.Kind != KindText {
		return nil, fmt.Errorf("%s 看着不是文本（%s），下载下来看", info.Name, info.Kind)
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf, err := io.ReadAll(io.LimitReader(f, MaxText+1))
	if err != nil {
		return nil, err
	}
	out := &Text{Path: p, Bytes: info.Size}
	if len(buf) > MaxText {
		buf, out.Truncated = buf[:MaxText], true
	}
	// 非法字节换成 U+FFFD：采样只看了头 512 字节，后面完全可能有坏字节，
	// 而那会让整个 JSON 响应变成一串问号（json 包自己也会替换，这里说清楚而已）
	out.Text = strings.ToValidUTF8(string(buf), "�")
	return out, nil
}

// Open 打开一个文件给 HTTP 直接吐。第二个返回值是 Info（调用方据此决定
// content-type 和 inline / attachment）。
//
// **只开常规文件**：目录、设备、socket、fifo 全拒。少这一条，`/dev/zero` 就是一条
// 永远不结束的响应，而 `/dev/rdisk0` 是在读整块磁盘。
func (b *Browser) Open(p string) (*os.File, *Info, error) {
	info, err := b.Peek(p)
	if err != nil {
		return nil, nil, err
	}
	if info.Dir {
		return nil, nil, fmt.Errorf("%s 是目录", p)
	}
	if info.Kind == KindSpecial {
		return nil, nil, fmt.Errorf("%s 不是常规文件（设备 / socket / 管道），不给读", p)
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, nil, err
	}
	return f, info, nil
}

/* ------------------------------------------------------------------ 起点 */

// Root 是浏览的**起点书签**，不是边界。没配 Roots 时从任何一个起点都能 `..` 走到 /。
type Root struct {
	Path  string `json:"path"`
	Label string `json:"label"`
}

// Starts 给出默认起点。pane 的 cwd 不在这儿 —— 前端手上已经有 /api/herdr/panes 的
// 结果了（面板一览用的同一份），在那边合进来，省一次 herdr 调用。
func (b *Browser) Starts() []Root {
	var out []Root
	seen := map[string]bool{}
	add := func(p, label string) {
		if p == "" {
			return
		}
		p = filepath.Clean(p)
		if seen[p] || b.Check(p) != nil {
			return
		}
		if st, err := os.Stat(p); err != nil || !st.IsDir() {
			return
		}
		seen[p] = true
		out = append(out, Root{Path: p, Label: label})
	}
	add(b.Uploads, "传上去的图")
	for _, r := range b.Roots {
		add(r, "允许的目录")
	}
	add(b.Home, "家目录")
	add(b.Tmp, "临时目录")
	return out
}
