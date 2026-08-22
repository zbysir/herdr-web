package files

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func write(t *testing.T, p string, b []byte) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func open(roots ...string) *Browser { return &Browser{Enabled: true, Roots: roots} }

/* ------------------------------------------------------------------ jail */

// 这一条是整个包里最容易写错的地方：纯前缀比较会让 /home/user 放行 /home/user2。
func TestCheckPrefixIsNotEnough(t *testing.T) {
	base := t.TempDir()
	ok := filepath.Join(base, "user")
	bad := filepath.Join(base, "user2")
	if err := os.MkdirAll(ok, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(bad, "secret.txt"), []byte("nope"))

	b := open(ok)
	if err := b.Check(ok); err != nil {
		t.Fatalf("root 自己应该放行：%v", err)
	}
	if err := b.Check(filepath.Join(ok, "a")); err == nil {
		// 不存在的路径 EvalSymlinks 会失败 → 拒。这是刻意的，见 Check 的注释。
		t.Log("不存在的路径被拒了，符合预期")
	}
	if err := b.Check(filepath.Join(bad, "secret.txt")); err == nil {
		t.Fatal("/…/user2 被 /…/user 这个 root 放行了 —— 前缀比较少了分隔符")
	}
}

// symlink 能从 jail 里指出去。检查必须在 EvalSymlinks 之后做。
func TestCheckFollowsSymlinkOutOfJail(t *testing.T) {
	base := t.TempDir()
	jail := filepath.Join(base, "jail")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(jail, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(outside, "secret.txt"), []byte("nope"))

	link := filepath.Join(jail, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("这个环境建不了 symlink：%v", err)
	}
	b := open(jail)
	if err := b.Check(filepath.Join(link, "secret.txt")); err == nil {
		t.Fatal("顺着 symlink 走出 jail 了 —— 前缀检查没在 EvalSymlinks 之后做")
	}
}

func TestCheckNoRootsAllowsEverything(t *testing.T) {
	b := open()
	if err := b.Check("/etc"); err != nil {
		t.Fatalf("不配 Roots 就不该有边界：%v", err)
	}
	if err := b.Check("relative/path"); err == nil {
		t.Fatal("相对路径应该被拒")
	}
	off := &Browser{Enabled: false}
	if err := off.Check("/etc"); err == nil {
		t.Fatal("关掉之后一切都该拒")
	}
}

/* --------------------------------------------------------------- Resolve */

func TestResolve(t *testing.T) {
	home, _ := os.UserHomeDir()
	cases := []struct {
		p, base, want string
		bad           bool
	}{
		{p: "/a/b/../c", want: "/a/c"},
		{p: "out/chart.png", base: "/work/proj", want: "/work/proj/out/chart.png"},
		{p: "./chart.png", base: "/work", want: "/work/chart.png"},
		{p: "  /a/b  ", want: "/a/b"},
		{p: "~/x", want: filepath.Join(home, "x")},
		{p: "out/chart.png", bad: true},        // 相对路径但没有基准 —— 不许猜
		{p: "x", base: "rel/ative", bad: true}, // 基准本身不是绝对路径
		{p: "", bad: true},
	}
	for _, c := range cases {
		got, err := Resolve(c.p, c.base)
		if c.bad {
			if err == nil {
				t.Errorf("Resolve(%q, %q) = %q，应该报错", c.p, c.base, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("Resolve(%q, %q) 出错：%v", c.p, c.base, err)
		} else if got != c.want {
			t.Errorf("Resolve(%q, %q) = %q，想要 %q", c.p, c.base, got, c.want)
		}
	}
}

/* ------------------------------------------------------------------ 认类型 */

var pngHead = []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 13}

func TestPeekKinds(t *testing.T) {
	dir := t.TempDir()
	b := open()

	img := write(t, filepath.Join(dir, "a.png"), pngHead)
	// **改后缀骗不过去**：认的是魔数
	imgLying := write(t, filepath.Join(dir, "b.txt"), pngHead)
	txt := write(t, filepath.Join(dir, "c.md"), []byte("# 标题\n正文"))
	// SVG 认成图（查看器走 <img>，那条路规范上就不跑脚本；顶层打开靠 svgCSP）
	svg := write(t, filepath.Join(dir, "d.svg"), []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`))
	bin := write(t, filepath.Join(dir, "e.bin"), []byte{0x00, 0x01, 0x02, 0xff})

	for _, c := range []struct{ p, kind string }{
		{img, KindImage}, {imgLying, KindImage}, {txt, KindText}, {svg, KindImage}, {bin, KindBinary},
	} {
		info, err := b.Peek(c.p)
		if err != nil {
			t.Fatalf("Peek(%s)：%v", c.p, err)
		}
		if info.Kind != c.kind {
			t.Errorf("Peek(%s).Kind = %q，想要 %q", filepath.Base(c.p), info.Kind, c.kind)
		}
	}
	if info, _ := b.Peek(svg); info.Mime != SVGMIME {
		t.Errorf("svg 的 mime = %q，HTTP 那层要靠它挑 CSP", info.Mime)
	}
	if info, _ := b.Peek(img); info.Mime != "image/png" {
		t.Errorf("png 的 mime = %q", info.Mime)
	}
}

// 认错的代价不对称：svg 认成文本只是看到源码，**html 认成 svg 就是把一个能跑脚本的
// 文档 inline 出去了**。所以这条只往「宁可少认」那边错。
func TestIsSVGDoesNotSwallowHTML(t *testing.T) {
	yes := []string{
		`<svg xmlns="http://www.w3.org/2000/svg"/>`,
		"\n\t  <svg width=\"10\"></svg>",
		`<?xml version="1.0"?><svg></svg>`,
		"\ufeff<svg></svg>", // 带 BOM
		`<!DOCTYPE svg PUBLIC "-//W3C//DTD SVG 1.1//EN" ""><svg></svg>`,
	}
	no := []string{
		`<html><body><svg><script>alert(1)</script></svg></body></html>`, // 关键的一条
		`<!DOCTYPE html><html><svg/></html>`,
		`说明：这个文件里提到了 <svg> 但它是一份文本`, // 第一个非空白字符不是 <
		`{"kind":"<svg>"}`,
		``,
		`<note>没有 svg</note>`,
	}
	for _, v := range yes {
		if !isSVG([]byte(v)) {
			t.Errorf("应该认成 SVG：%q", v)
		}
	}
	for _, v := range no {
		if isSVG([]byte(v)) {
			t.Errorf("**不该**认成 SVG：%q", v)
		}
	}
}

// 采样是从文件头截的，最后那个中文字符会被切成半个 —— 不回退的话整个文件被判成二进制。
// 纯 ASCII 文件永远不会触发这个，所以很容易漏。
func TestPeekTextTruncatedRune(t *testing.T) {
	dir := t.TempDir()
	// 造一个「第 512 字节正好落在一个三字节汉字中间」的文件
	body := strings.Repeat("a", sniffLen-2) + "中" + strings.Repeat("b", 100)
	p := write(t, filepath.Join(dir, "cn.txt"), []byte(body))
	info, err := open().Peek(p)
	if err != nil {
		t.Fatal(err)
	}
	if info.Kind != KindText {
		t.Fatalf("尾部被切断的中文文件判成了 %q —— looksText 没回退不完整的 rune", info.Kind)
	}
}

// /dev/zero 是一条无限流；fifo 会把请求永远挂住。这两个都只能列出来，不能读。
func TestOpenRefusesNonRegular(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("建不了 fifo：%v", err)
	}
	b := open()
	info, err := b.Peek(fifo)
	if err != nil {
		t.Fatal(err)
	}
	if info.Kind != KindSpecial {
		t.Fatalf("fifo 的 Kind = %q，应该是 special（而且 Peek 绝不能去读它）", info.Kind)
	}
	if _, _, err := b.Open(fifo); err == nil {
		t.Fatal("Open 放行了一个 fifo —— 这个响应会永远挂着")
	}
	if _, _, err := b.Open(dir); err == nil {
		t.Fatal("Open 放行了一个目录")
	}
}

/* ------------------------------------------------------------------ 列目录 */

func TestList(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "sub", "x"), []byte("x"))
	write(t, filepath.Join(dir, ".hidden"), []byte("h"))
	old := write(t, filepath.Join(dir, "aaa-old.png"), pngHead)
	neu := write(t, filepath.Join(dir, "zzz-new.png"), pngHead)
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	_ = neu

	b := open()
	l, err := b.List(dir, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if l.Hidden != 1 {
		t.Errorf("点开头的应该被过滤，Hidden = %d", l.Hidden)
	}
	if len(l.Entries) != 3 {
		t.Fatalf("想要 3 条（sub + 两张图），拿到 %d", len(l.Entries))
	}
	// 目录永远在最前面
	if !l.Entries[0].Dir || l.Entries[0].Name != "sub" {
		t.Errorf("目录没排在最前面：%+v", l.Entries[0])
	}
	// 默认按 mtime 倒序：新的那张在前，哪怕名字排在后面。这个功能存在的理由就是
	// 「找 agent 刚生成的那个文件」。
	if l.Entries[1].Name != "zzz-new.png" {
		t.Errorf("默认排序应该是最近改动优先，第二条是 %q", l.Entries[1].Name)
	}
	if l.Entries[1].Kind != KindImage {
		t.Errorf("列表里 .png 应该标成 image，拿到 %q", l.Entries[1].Kind)
	}

	byName, err := b.List(dir, "name", false)
	if err != nil {
		t.Fatal(err)
	}
	if byName.Entries[1].Name != "aaa-old.png" {
		t.Errorf("sort=name 时第二条应该是 aaa-old.png，拿到 %q", byName.Entries[1].Name)
	}

	all, err := b.List(dir, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Entries) != 4 {
		t.Errorf("all=1 应该带上 .hidden，拿到 %d 条", len(all.Entries))
	}
}

// jail 的根目录不该给出一个点了就报错的「上一级」。
func TestListParentStopsAtJail(t *testing.T) {
	base := t.TempDir()
	jail := filepath.Join(base, "jail")
	sub := filepath.Join(jail, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	b := open(jail)
	l, err := b.List(jail, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if l.Parent != "" {
		t.Errorf("jail 根的 Parent 应该是空的，拿到 %q", l.Parent)
	}
	l2, err := b.List(sub, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if l2.Parent != jail {
		t.Errorf("jail 里面的子目录该能往上走，Parent = %q", l2.Parent)
	}
}

func TestReadText(t *testing.T) {
	dir := t.TempDir()
	b := open()
	p := write(t, filepath.Join(dir, "a.md"), []byte("你好\nworld"))
	tx, err := b.ReadText(p)
	if err != nil {
		t.Fatal(err)
	}
	if tx.Text != "你好\nworld" || tx.Truncated {
		t.Errorf("读出来的是 %q（truncated=%v）", tx.Text, tx.Truncated)
	}

	big := write(t, filepath.Join(dir, "big.log"), []byte(strings.Repeat("x", MaxText+100)))
	tx2, err := b.ReadText(big)
	if err != nil {
		t.Fatal(err)
	}
	if !tx2.Truncated || len(tx2.Text) != MaxText {
		t.Errorf("大文件该被截断：truncated=%v len=%d", tx2.Truncated, len(tx2.Text))
	}
	if tx2.Bytes != int64(MaxText+100) {
		t.Errorf("Bytes 该是文件真实大小，拿到 %d", tx2.Bytes)
	}

	img := write(t, filepath.Join(dir, "a.png"), pngHead)
	if _, err := b.ReadText(img); err == nil {
		t.Fatal("图片不该走文本预览")
	}
}

/* ------------------------------------------------------------------ 签名 */

func TestSign(t *testing.T) {
	s, err := NewSigner()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0)
	tok := s.Sign("/tmp/a b/中文.png", now)
	if strings.Contains(tok, "/") {
		t.Fatalf("票里不能有斜杠（URL 上后面还要挂文件名）：%q", tok)
	}
	got, err := s.Verify(tok, now)
	if err != nil {
		t.Fatal(err)
	}
	if got != "/tmp/a b/中文.png" {
		t.Errorf("验出来的路径是 %q", got)
	}
	if _, err := s.Verify(tok, now.Add(TokenTTL+time.Second)); err == nil {
		t.Fatal("过期的票应该被拒")
	}
	// 换掉 payload（把过期时间改远）：签名覆盖了过期字段，所以必须挂
	enc, sig, _ := strings.Cut(tok, ".")
	forged := b64.EncodeToString([]byte("99999999999:/etc/passwd")) + "." + sig
	if _, err := s.Verify(forged, now); err == nil {
		t.Fatal("换了 payload 的票被放行了 —— 签名没覆盖过期时间或路径")
	}
	if _, err := s.Verify(enc+".AAAA", now); err == nil {
		t.Fatal("坏签名被放行了")
	}
	// 另一个进程（另一把密钥）签的票不能通用
	s2, _ := NewSigner()
	if _, err := s2.Verify(tok, now); err == nil {
		t.Fatal("换了密钥还能验过")
	}
}

// 错误消息要是人话，而且**哨兵得留着** —— HTTP 层靠 errors.Is 分 404 / 403。
func TestErrorsAreReadableAndKeepSentinel(t *testing.T) {
	b := open()
	_, err := b.Peek("/definitely/not/here.png")
	if err == nil {
		t.Fatal("不存在的路径应该报错")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("哨兵丢了，HTTP 层分不出 404：%v", err)
	}
	if !strings.HasPrefix(err.Error(), "找不到 /definitely/not/here.png") {
		t.Errorf("消息应该以「找不到 + 解析后的绝对路径」开头，拿到 %q", err.Error())
	}
	if strings.Contains(err.Error(), "no such file") {
		t.Errorf("别把 os 的英文原文糊在后面：%q", err.Error())
	}
}
