package uploads

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func png() []byte {
	return append([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}, bytes.Repeat([]byte{0}, 20)...)
}
func jpg() []byte { return append([]byte{0xff, 0xd8, 0xff, 0xe0}, bytes.Repeat([]byte{0}, 20)...) }
func gif() []byte { return append([]byte("GIF89a"), bytes.Repeat([]byte{0}, 20)...) }
func webp() []byte {
	return append(append([]byte("RIFF"), []byte{0, 0, 0, 0}...), append([]byte("WEBP"), bytes.Repeat([]byte{0}, 12)...)...)
}
func heic() []byte {
	b := make([]byte, 24)
	copy(b[4:8], "ftyp")
	copy(b[8:12], "heic")
	return b
}

// 按魔数认类型，不信 content-type 和文件名 —— 那两个都是随便填的
func TestSniffAndSave(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	for _, c := range []struct {
		name, want string
		body       []byte
	}{
		{"png", "png", png()}, {"jpg", "jpg", jpg()}, {"gif", "gif", gif()}, {"webp", "webp", webp()},
	} {
		r, err := s.Save(c.body)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if r.Kind != c.want {
			t.Errorf("%s 认成了 %s", c.name, r.Kind)
		}
		if !strings.HasSuffix(r.Path, "."+c.want) {
			t.Errorf("%s 文件名后缀不对: %s", c.name, r.Path)
		}
		st, err := os.Stat(r.Path)
		if err != nil {
			t.Fatalf("%s 没落盘: %v", c.name, err)
		}
		if st.Mode().Perm() != 0o600 {
			t.Errorf("%s 权限应当是 0600，实际 %o", c.name, st.Mode().Perm())
		}
		if filepath.Dir(r.Path) != filepath.Join(s.Dir, "uploads") {
			t.Errorf("%s 落错目录: %s", c.name, r.Path)
		}
	}
}

func TestRejects(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	if _, err := s.Save(nil); err == nil {
		t.Error("空文件应当被拒")
	}
	// 文本冒充 png：content-type 骗不过魔数
	if _, err := s.Save([]byte("I am definitely not a png, trust me")); err == nil {
		t.Error("非图片应当被拒")
	}
	// HEIC 要给一句能看懂的错，而不是「不认识的类型」
	_, err := s.Save(heic())
	if err == nil || !strings.Contains(err.Error(), "HEIC") {
		t.Errorf("HEIC 应当给专门的提示，实际: %v", err)
	}
	// 超限：上限按类型定，所以得是一张**认得出的**图才会走到「太大」那一步
	big := append(png(), bytes.Repeat([]byte{0}, MaxBytes)...)
	if _, err := s.Save(big); err == nil || !strings.Contains(err.Error(), "太大") {
		t.Errorf("超限应当被拒，实际: %v", err)
	}
	// 被拒的那份不能在目录里留下半截（`.part` 也不行）
	if left, _ := filepath.Glob(filepath.Join(s.Dir, "uploads", "*")); len(left) != 0 {
		t.Errorf("被拒之后目录里不该留东西，实际: %v", left)
	}
}

// mp4 家族的 ftyp 头：size(4) + "ftyp" + 主品牌 + 次版本(4) + 兼容品牌…
func ftyp(major string, compat ...string) []byte {
	b := []byte{0, 0, 0, 0}
	b = append(b, "ftyp"...)
	b = append(b, major...)
	b = append(b, 0, 0, 0, 0)
	for _, c := range compat {
		b = append(b, c...)
	}
	n := len(b)
	b[0], b[1], b[2], b[3] = byte(n>>24), byte(n>>16), byte(n>>8), byte(n)
	return append(b, bytes.Repeat([]byte{0}, 40)...)
}

// 视频按魔数认；**HEIC / AVIF 用的是同一个 ftyp 容器**，不能被认成视频
func TestVideo(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	webm := append([]byte{0x1a, 0x45, 0xdf, 0xa3, 0x9f, 0x42, 0x82, 0x84}, append([]byte("webm"), bytes.Repeat([]byte{0}, 40)...)...)
	for _, c := range []struct {
		name, want string
		body       []byte
	}{
		{"android mp4", "mp4", ftyp("isom", "isom", "iso2", "mp41")},
		{"iPhone mov", "mov", ftyp("qt  ", "qt  ")},
		{"主品牌怪、兼容表里有 mp42", "mp4", ftyp("xyzw", "mp42")},
		{"3gp", "3gp", ftyp("3gp4", "isom")},
		{"webm", "webm", webm},
	} {
		r, err := s.Save(c.body)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if r.Kind != c.want || r.Media != "video" {
			t.Errorf("%s 认成了 %s/%s", c.name, r.Kind, r.Media)
		}
	}
	for _, heif := range []string{"heic", "mif1", "avif"} {
		if _, err := s.Save(ftyp(heif, "mif1", "heic")); err == nil {
			t.Errorf("%s 是照片，不该被当成视频收下", heif)
		}
	}
	// 视频的上限比图片大得多：图片上限那么大的一段视频照样收
	big := append(ftyp("isom", "mp42"), bytes.Repeat([]byte{0}, MaxBytes+1)...)
	if _, err := s.Save(big); err != nil {
		t.Errorf("比图片上限大的视频应当收下，实际: %v", err)
	}
	// 传完的不留 .part
	if left, _ := filepath.Glob(filepath.Join(s.Dir, "uploads", "*.part")); len(left) != 0 {
		t.Errorf("传完不该留 .part：%v", left)
	}
}

// 传到一半断了（手机上常事）：报错，而且**不留下一个看着正常的半截文件**
func TestBrokenStream(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	r := io.MultiReader(bytes.NewReader(ftyp("isom", "mp42")), errReader{})
	if _, err := s.SaveReader(r); err == nil {
		t.Fatal("断流应当报错")
	}
	if left, _ := filepath.Glob(filepath.Join(s.Dir, "uploads", "*")); len(left) != 0 {
		t.Errorf("断流之后目录里不该留东西，实际: %v", left)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
