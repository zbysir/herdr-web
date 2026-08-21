package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Method 是「这个二进制当初是怎么装上来的」。换版本的动作完全取决于它 ——
// npm 装的去动 node_modules 里的文件，下次 npm 自己就把它盖回去了，白忙一场。
type Method int

const (
	// MethodArchive：从 GitHub release 的 tar.gz 解出来的（install.sh 或手动）。
	// 只有这一种能由我们自己原地换。
	MethodArchive   Method = iota
	MethodNPM              // 路径里有 node_modules
	MethodBrew             // 路径里有 /Cellar/ 或 linuxbrew
	MethodGoInstall        // 在 $GOPATH/bin 或 ~/go/bin 里
)

// Install 把「当前二进制是怎么来的」和它的绝对路径一起给出来。
type Install struct {
	Method Method
	Path   string // 解过 symlink 的真实路径
}

// Detect 判断装法。判据只有可执行文件的路径 —— 没有别的可靠信号，而路径这个
// 判据的误判后果也只是「给了条不对的升级命令」，不会动错文件。
func Detect() (Install, error) {
	exe, err := os.Executable()
	if err != nil {
		return Install{}, err
	}
	// 必须解 symlink：npm 全局装出来的 herdr-web 是 …/bin/herdr-web → node_modules/… 的
	// 链接，不解开就会把它当成 archive 装法，然后去覆盖那个 symlink。
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	p := filepath.ToSlash(exe)
	switch {
	case strings.Contains(p, "/node_modules/"):
		return Install{MethodNPM, exe}, nil
	case strings.Contains(p, "/Cellar/") || strings.Contains(p, "/linuxbrew/"):
		return Install{MethodBrew, exe}, nil
	case strings.HasSuffix(filepath.Dir(p), "/go/bin") || strings.Contains(p, "/pkg/mod/"):
		return Install{MethodGoInstall, exe}, nil
	}
	return Install{MethodArchive, exe}, nil
}

// Command 返回该用哪条命令升级。MethodArchive 返回空串 —— 那种情况由本进程自己换。
func (i Install) Command() string {
	switch i.Method {
	case MethodNPM:
		return "npm install -g " + NPMPackage + "@latest"
	case MethodBrew:
		return "brew upgrade herdr-web"
	case MethodGoInstall:
		return "go install github.com/" + Repo + "/cmd/herdr-web@latest"
	}
	return ""
}

// AssetName 是某个版本在 release 里的 archive 文件名，必须和 .goreleaser.yaml 的
// name_template 对得上（改一边就要改另一边）。
func AssetName(v, goos, goarch string) string {
	return fmt.Sprintf("herdr-web_%s_%s_%s.tar.gz", strings.TrimPrefix(v, "v"), goos, goarch)
}

// Apply 把 rel 这一版下载下来，校验 sha256，然后原地换掉 exe。
//
// 三个关键点：
//   - **先校验再落地**：checksums.txt 是同一个 release 的产物，对不上就整个放弃。
//   - **同目录里先写临时文件再 rename**：rename 是原子的，中途断网 / 断电不会留下一个
//     半截的二进制把命令行整个搞坏。跨目录 rename 会 EXDEV，所以临时文件必须同目录。
//   - **不删旧的**：unix 上 rename 覆盖一个正在运行的可执行文件是允许的（老 inode
//     还被进程持着），所以当前进程能安全跑到自己退出为止。
//
// 返回换上去的文件路径。progress 可以是 nil。
func Apply(ctx context.Context, hc *http.Client, exe string, rel Release, progress func(string)) (string, error) {
	if hc == nil {
		hc = &http.Client{Timeout: 5 * time.Minute}
	}
	name := AssetName(rel.Version, runtime.GOOS, runtime.GOARCH)
	base := "https://github.com/" + Repo + "/releases/download/" + rel.Tag + "/"
	if err := applyFrom(ctx, hc, base, exe, name, progress); err != nil {
		return "", err
	}
	return exe, nil
}

// applyFrom 是 Apply 去掉「地址怎么拼」之后的部分，单独拆出来是为了能测 —— 校验和
// 原子替换这两段错了都是静默的（装上一个坏二进制 / 把好的覆盖没了）。
func applyFrom(ctx context.Context, hc *http.Client, base, exe, name string, progress func(string)) error {
	say := func(s string) {
		if progress != nil {
			progress(s)
		}
	}
	say("下载 " + name)
	archive, err := get(ctx, hc, base+name)
	if err != nil {
		return fmt.Errorf("下载 %s 失败: %w", name, err)
	}

	say("校验 sha256")
	sums, err := get(ctx, hc, base+"checksums.txt")
	if err != nil {
		return fmt.Errorf("下载 checksums.txt 失败（没有它就无法确认下载没被动过）: %w", err)
	}
	want, ok := lookupSum(string(sums), name)
	if !ok {
		return fmt.Errorf("checksums.txt 里没有 %s", name)
	}
	got := sha256.Sum256(archive)
	if hex.EncodeToString(got[:]) != want {
		return fmt.Errorf("sha256 不对：算出 %s，checksums.txt 写的是 %s —— 别装", hex.EncodeToString(got[:]), want)
	}

	say("解包")
	bin, err := extract(archive, "herdr-web")
	if err != nil {
		return err
	}
	if len(bin) < 1<<20 {
		return fmt.Errorf("解出来的 herdr-web 只有 %d 字节，不像是个完整二进制", len(bin))
	}

	say("换上 " + exe)
	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".herdr-web-new-*")
	if err != nil {
		// 最常见就是这里：装在 /usr/local/bin 这种要 root 的地方
		return fmt.Errorf("在 %s 里建不了临时文件（要不要 sudo？）: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // rename 成功之后这个就不存在了，Remove 会失败，无所谓
	if _, err := tmp.Write(bin); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmpName, exe); err != nil {
		return fmt.Errorf("换 %s 失败（要不要 sudo？）: %w", exe, err)
	}
	return nil
}

func get(ctx context.Context, hc *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("user-agent", "herdr-web")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %s", resp.Status)
	}
	// 100MB 上限：正常 archive 六七兆，这个数只是防「响应其实是别的东西」时把内存吃光
	return io.ReadAll(io.LimitReader(resp.Body, 100<<20))
}

// lookupSum 从 checksums.txt（`<sha256>  <文件名>` 每行一条）里找某个文件的哈希。
func lookupSum(txt, name string) (string, bool) {
	for _, line := range strings.Split(txt, "\n") {
		f := strings.Fields(strings.TrimSpace(line))
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			return strings.ToLower(f[0]), true
		}
	}
	return "", false
}

// extract 从 tar.gz 里取出叫 want 的那个文件。
func extract(archive []byte, want string) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("不是 gzip: %w", err)
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("archive 里没有 %s", want)
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag != tar.TypeReg || filepath.Base(h.Name) != want {
			continue
		}
		return io.ReadAll(io.LimitReader(tr, 200<<20))
	}
}
