// Package uploads 收图片：手机把图存到跑 herdr 的那台机器上，然后**把绝对路径
// 当文本投给 agent**。
//
// 为什么是这条路：herdr 的 socket API 里没有任何图片概念，能投的只有文本。而
// claude 和 codex 都能直接读磁盘上的图片文件（实测：给一张 320×200 左红右蓝中间
// 绿带的 PNG，两边都描述对了，codex 还会打一行 "Viewed Image"）。所以「上传」＝
// 落盘 + 在提示词里带上路径。
package uploads

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const MaxBytes = 25 << 20

type Result struct {
	Path  string `json:"path"`
	Name  string `json:"name"`
	Bytes int    `json:"bytes"`
	Kind  string `json:"kind"`
	Dir   string `json:"dir"`
}

type Store struct{ Dir string } // ~/.herdr-web

func (s *Store) dir() string { return filepath.Join(s.Dir, "uploads") }

// 按魔数认类型，不信客户端给的 content-type 和文件名 —— 那两个都是随便填的。
// 只收 agent 真读得懂的那几种。
func sniff(b []byte) string {
	switch {
	case len(b) > 8 && bytes.Equal(b[:8], []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}):
		return "png"
	case len(b) > 3 && b[0] == 0xff && b[1] == 0xd8 && b[2] == 0xff:
		return "jpg"
	case len(b) > 6 && (bytes.Equal(b[:6], []byte("GIF87a")) || bytes.Equal(b[:6], []byte("GIF89a"))):
		return "gif"
	case len(b) > 12 && bytes.Equal(b[:4], []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WEBP")):
		return "webp"
	}
	return ""
}

// iPhone 直接给的 HEIC，agent 读不了；前端会先用 canvas 转成 PNG/JPEG，
// 转不了才会原样传上来，这里给一句能看懂的错。
func isHEIC(b []byte) bool {
	if len(b) < 12 || !bytes.Equal(b[4:8], []byte("ftyp")) {
		return false
	}
	switch string(b[8:12]) {
	case "heic", "heix", "heif", "mif1", "msf1":
		return true
	}
	return false
}

func (s *Store) Save(buf []byte) (*Result, error) {
	if len(buf) == 0 {
		return nil, fmt.Errorf("空文件")
	}
	if len(buf) > MaxBytes {
		return nil, fmt.Errorf("图片太大（%.1f MB，上限 25 MB）", float64(len(buf))/(1<<20))
	}
	kind := sniff(buf)
	if kind == "" {
		if isHEIC(buf) {
			return nil, fmt.Errorf("HEIC 图 agent 读不了，而这台浏览器也没能把它转成 PNG。到相册里导出成 JPEG 再传。")
		}
		return nil, fmt.Errorf("不认识这个文件类型，只收 png / jpg / gif / webp")
	}

	dir := s.dir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	rnd := make([]byte, 3)
	if _, err := rand.Read(rnd); err != nil {
		return nil, err
	}
	name := fmt.Sprintf("%s-%s.%s", time.Now().Format("20060102-150405"), hex.EncodeToString(rnd), kind)
	full := filepath.Join(dir, name)
	if err := os.WriteFile(full, buf, 0o600); err != nil {
		return nil, err
	}
	return &Result{Path: full, Name: name, Bytes: len(buf), Kind: kind, Dir: dir}, nil
}
