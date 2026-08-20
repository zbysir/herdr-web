package uploads

import (
	"bytes"
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
	if _, err := s.Save(bytes.Repeat([]byte{0x89}, MaxBytes+1)); err == nil || !strings.Contains(err.Error(), "太大") {
		t.Errorf("超限应当被拒，实际: %v", err)
	}
}
