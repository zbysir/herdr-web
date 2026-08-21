package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// Newer 是「要不要提示升级」的唯一判据，搞反了的表现是所有人都被误提示或者谁都收不到。
func TestNewer(t *testing.T) {
	cases := []struct {
		cur, latest string
		want        bool
	}{
		{"0.1.0", "0.2.0", true},
		{"0.2.0", "0.1.0", false},
		{"0.2.0", "0.2.0", false},
		{"0.9.9", "1.0.0", true},
		{"1.0.0", "1.0.1", true},
		{"1.2.3", "1.10.0", true}, // 数值比较，不是字符串（"10" < "2" 会错）
		{"v1.0.0", "v1.0.1", true},
		// 本地构建一律不提示，否则开发机上天天弹
		{"dev", "9.9.9", false},
		{"", "9.9.9", false},
		// 查不到版本（没发过 release）时不提示
		{"1.0.0", "", false},
		// pre-release 比同号正式版旧
		{"1.0.0-rc1", "1.0.0", true},
		{"1.0.0", "1.0.0-rc1", false},
		{"1.0.0-rc1", "1.0.0-rc2", false}, // 同号 pre 之间不比，避免瞎猜规则
	}
	for _, c := range cases {
		if got := Newer(c.cur, c.latest); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.cur, c.latest, got, c.want)
		}
	}
}

// checksums.txt 的格式（两个空格分隔，有时文件名带 *）解错就等于跳过校验。
func TestLookupSum(t *testing.T) {
	txt := "abc123  herdr-web_1.0.0_darwin_arm64.tar.gz\n" +
		"def456 *herdr-web_1.0.0_linux_amd64.tar.gz\n"
	if s, ok := lookupSum(txt, "herdr-web_1.0.0_darwin_arm64.tar.gz"); !ok || s != "abc123" {
		t.Errorf("darwin/arm64: %q %v", s, ok)
	}
	if s, ok := lookupSum(txt, "herdr-web_1.0.0_linux_amd64.tar.gz"); !ok || s != "def456" {
		t.Errorf("带 * 的文件名也要认: %q %v", s, ok)
	}
	if _, ok := lookupSum(txt, "herdr-web_1.0.0_windows_amd64.zip"); ok {
		t.Error("没有的条目不该报有")
	}
}

// AssetName 必须和 .goreleaser.yaml 的 name_template 一致。改了这里就要改那里，
// 对不上的表现是 update 下载 404。
func TestAssetName(t *testing.T) {
	if got := AssetName("v1.2.3", "darwin", "arm64"); got != "herdr-web_1.2.3_darwin_arm64.tar.gz" {
		t.Errorf("AssetName = %q", got)
	}
}

// 缓存：查过就写盘，没到间隔不再发请求。这条错了会变成每次启动都撞一次网络。
func TestCheckThrottle(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v2.0.0","html_url":"https://example.com/r"}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	c := &Checker{Dir: dir, Current: "1.0.0", APIBase: srv.URL, Interval: time.Hour}
	if _, err := c.Check(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Fatalf("第一次应当发请求，hits=%d", hits)
	}
	if _, err := c.Check(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Errorf("没到间隔不该再发请求，hits=%d", hits)
	}
	if _, err := c.Check(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if hits != 2 {
		t.Errorf("force 要绕过节流，hits=%d", hits)
	}

	// 换一个 Checker（模拟进程重启）：应当从盘上读到缓存，不再发请求
	c2 := &Checker{Dir: dir, Current: "1.0.0", APIBase: srv.URL, Interval: time.Hour}
	s, ok := c2.Available()
	if !ok || s.Latest != "2.0.0" {
		t.Errorf("重启后应当从 update.json 读到 2.0.0，得到 %+v ok=%v", s, ok)
	}
	if hits != 2 {
		t.Errorf("Available 不该发请求，hits=%d", hits)
	}
}

// 查失败也要记 CheckedAt，否则网络不通的机器每次启动都要等一次超时。
func TestCheckFailureStillThrottles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := &Checker{Dir: t.TempDir(), Current: "1.0.0", APIBase: srv.URL, Interval: time.Hour}
	if _, err := c.Check(context.Background(), false); err == nil {
		t.Fatal("应当报错")
	}
	s := c.State()
	if s.CheckedAt.IsZero() {
		t.Error("失败也要记 CheckedAt")
	}
	if s.Err == "" {
		t.Error("失败原因要留下，管理页要显示")
	}
	if _, ok := c.Available(); ok {
		t.Error("查失败不该报「有新版本」")
	}
}

// 仓库还没发过 release 时 GitHub 给 404。这是正常状态，不能当成崩溃条件。
func TestLatestNoRelease(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()
	if _, err := Latest(context.Background(), srv.Client(), srv.URL); err == nil {
		t.Error("404 应当是个错误（但不该 panic）")
	}
}

// 全链路：把一个假 release 摆好，跑 Apply，确认换上去的是新二进制、且 sha 不对时拒绝。
func TestApply(t *testing.T) {
	newBin := bytes.Repeat([]byte("N"), 2<<20) // 过 1MB 的 sanity 检查
	archive := tarGz(t, "herdr-web", newBin)
	sum := sha256.Sum256(archive)
	name := AssetName("1.0.0", runtime.GOOS, runtime.GOARCH)

	var badSum bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch filepath.Base(r.URL.Path) {
		case "checksums.txt":
			s := hex.EncodeToString(sum[:])
			if badSum {
				s = hex.EncodeToString(bytes.Repeat([]byte{0}, 32))
			}
			_, _ = w.Write([]byte(s + "  " + name + "\n"))
		case name:
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	// Apply 的下载地址是硬编码 github.com 的，所以这里直接测它的两个可测部分：
	// 校验逻辑和原子替换。下载那段用 httptest 覆盖不了，改由 applyFrom 承接。
	exe := filepath.Join(t.TempDir(), "herdr-web")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := applyFrom(context.Background(), srv.Client(), srv.URL+"/", exe, name, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, newBin) {
		t.Errorf("换上去的不是新二进制（%d 字节）", len(got))
	}
	if fi, _ := os.Stat(exe); fi.Mode().Perm()&0o111 == 0 {
		t.Error("换上去之后没有可执行位")
	}

	// sha 不对：必须拒绝，而且**不能动**原文件
	badSum = true
	if err := os.WriteFile(exe, []byte("keep-me"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := applyFrom(context.Background(), srv.Client(), srv.URL+"/", exe, name, nil); err == nil {
		t.Fatal("sha 不对时必须报错")
	}
	if b, _ := os.ReadFile(exe); string(b) != "keep-me" {
		t.Error("校验失败时原文件被动了")
	}
	// 临时文件不能留在那儿
	ents, _ := os.ReadDir(filepath.Dir(exe))
	for _, e := range ents {
		if e.Name() != "herdr-web" {
			t.Errorf("留下了残留文件: %s", e.Name())
		}
	}
}

func tarGz(t *testing.T, name string, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
