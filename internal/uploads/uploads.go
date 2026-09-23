// Package uploads 收图片和视频：手机把文件存到跑 herdr 的那台机器上，然后**把绝对路径
// 当文本投给 agent**。
//
// 为什么是这条路：herdr 的 socket API 里没有任何文件概念，能投的只有文本。而
// claude 和 codex 都能直接读磁盘上的图片文件（实测：给一张 320×200 左红右蓝中间
// 绿带的 PNG，两边都描述对了，codex 还会打一行 "Viewed Image"）。所以「上传」＝
// 落盘 + 在提示词里带上路径。
//
// **视频不一样，要心里有数**：两家 agent 的读文件工具都不直接「看」视频 —— 它们拿到的
// 是一个路径，要看内容得自己跑 ffmpeg 抽帧 / 抽音轨（跑 herdr 的那台机器上得装着）。
// 所以视频这条路的价值是「把手机上录的东西送到那台机器上」，不是「agent 当场看懂」。
// 用户点名要的，理由成立：录屏复现一个 bug 比截十张图说得清楚。
package uploads

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/zbysir/herdr-web/internal/files"
)

// MaxBytes 图片的上限。前端已经把长边缩到 2400 了，正常一张不过几 MB。
const MaxBytes = 25 << 20

// MaxVideoBytes 视频的上限。手机录一分钟 1080p 就是一两百 MB，4K 更多 ——
// 按图片那个 25 MB 卡的话等于不收视频。再大就该走别的路（AirDrop、网盘）了：
// 这条是穿过隧道从手机上行的，512 MB 在蜂窝网络上已经是好几分钟。
const MaxVideoBytes = 512 << 20

// headLen 认类型要看的头几个字节。ftyp 盒子里的兼容品牌表要多看一点（见 files.VideoType）。
const headLen = 64

type Result struct {
	Path  string `json:"path"`
	Name  string `json:"name"`
	Bytes int    `json:"bytes"`
	Kind  string `json:"kind"` // 落盘的扩展名：png / jpg / mp4 / mov …
	// Media 是「图」还是「视频」。前端拿它挑附件上的记号，别让它去猜扩展名
	Media string `json:"media"`
	Dir   string `json:"dir"`
}

type Store struct{ Dir string } // ~/.herdr-web

func (s *Store) dir() string { return filepath.Join(s.Dir, "uploads") }

// 按魔数认**图片**，不信客户端给的 content-type 和文件名 —— 那两个都是随便填的。
// 只收 agent 真读得懂的那几种。视频在 files.VideoType（和看文件那条路共用一份）。
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

// Save 一整份字节（测试和小文件用）。
func (s *Store) Save(buf []byte) (*Result, error) { return s.SaveReader(bytes.NewReader(buf)) }

// SaveReader **边读边落盘**。
//
// 原来是 `io.ReadAll` 整份读进内存再写 —— 图片无所谓，视频几百 MB，几个手机同时传就是
// 几个 G 的内存，而这台机器上正跑着 agent。所以现在先读头几个字节认类型、按类型定上限，
// 剩下的直接 `io.Copy` 进一个 `.part` 临时文件，读完再改名：
//
//   - **上限按类型定**（图 25 MB / 视频 512 MB），所以得先认出类型才知道能收多少 ——
//     这也是为什么「太大」现在排在「不认识」后面报；
//   - **先写 `.part` 再改名**：传到一半断网（手机上常事）时，uploads 目录里不会留下一个
//     看着正常、其实只有半截的 `.mp4`，而那个路径说不定已经被人投给 agent 了。
func (s *Store) SaveReader(r io.Reader) (*Result, error) {
	head := make([]byte, headLen)
	n, err := io.ReadFull(r, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, err
	}
	head = head[:n]
	if n == 0 {
		return nil, fmt.Errorf("空文件")
	}
	kind, media := sniff(head), "image"
	limit := MaxBytes
	if kind == "" {
		if _, ext := files.VideoType(head); ext != "" {
			kind, media, limit = ext, "video", MaxVideoBytes
		}
	}
	if kind == "" {
		if isHEIC(head) {
			return nil, fmt.Errorf("HEIC 图 agent 读不了，而这台浏览器也没能把它转成 PNG。到相册里导出成 JPEG 再传。")
		}
		return nil, fmt.Errorf("不认识这个文件类型，只收图片（png / jpg / gif / webp）和视频（mp4 / mov / webm / mkv / 3gp）")
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
	part := full + ".part"
	f, err := os.OpenFile(part, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	fail := func(e error) (*Result, error) {
		f.Close()
		os.Remove(part)
		return nil, e
	}
	if _, err := f.Write(head); err != nil {
		return fail(err)
	}
	// 多读一个字节：读满 limit+1 就说明超了（LimitReader 自己不会报错）
	rest, err := io.Copy(f, io.LimitReader(r, int64(limit-n)+1))
	if err != nil {
		return fail(fmt.Errorf("传到一半断了：%w", err))
	}
	total := int64(n) + rest
	if total > int64(limit) {
		what := "图片"
		if media == "video" {
			what = "视频"
		}
		return fail(fmt.Errorf("%s太大（超过上限 %d MB）", what, limit>>20))
	}
	if err := f.Close(); err != nil {
		os.Remove(part)
		return nil, err
	}
	if err := os.Rename(part, full); err != nil {
		os.Remove(part)
		return nil, err
	}
	return &Result{Path: full, Name: name, Bytes: int(total), Kind: kind, Media: media, Dir: dir}, nil
}
