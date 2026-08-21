package tlsgen

import (
	"crypto/tls"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func loadSelf(t *testing.T, dir string, names ...string) *Result {
	t.Helper()
	r, err := Load(dir, "", "", []net.IP{net.IPv4(127, 0, 0, 1)}, names)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return r
}

// 自签要写 fullchain（叶子 + CA）：热重载只重新读 cert.pem，
// 要是 CA 只活在内存里，重载之后链就断了。
func TestSelfSignedWritesFullChain(t *testing.T) {
	dir := t.TempDir()
	r := loadSelf(t, dir, "herdr.example.com")
	if len(r.Cert.Certificate) < 2 {
		t.Errorf("证书链该有叶子 + CA 两张，得到 %d", len(r.Cert.Certificate))
	}
	// 重新从文件读也要有两张
	c, err := tls.LoadX509KeyPair(filepath.Join(dir, "tls", "cert.pem"), filepath.Join(dir, "tls", "key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Certificate) < 2 {
		t.Errorf("从文件读回来也该是两张，得到 %d", len(c.Certificate))
	}
}

// 证书在磁盘上被换掉（ACME 续期）之后，跑着的进程要能捡起来 —— 不然表现是
// 「续期成功了但某天早上所有设备一起报过期，重启一下就好」。
func TestHotReload(t *testing.T) {
	dir := t.TempDir()
	r := loadSelf(t, dir, "a.example.com")
	cfg := r.TLSConfig()

	first, err := cfg.GetCertificate(&tls.ClientHelloInfo{ServerName: "a.example.com"})
	if err != nil {
		t.Fatal(err)
	}

	// 换一张（换个域名，好区分）
	other := t.TempDir()
	r2 := loadSelf(t, other, "b.example.com")
	_ = r2
	copyFile(t, filepath.Join(other, "tls", "cert.pem"), filepath.Join(dir, "tls", "cert.pem"))
	copyFile(t, filepath.Join(other, "tls", "key.pem"), filepath.Join(dir, "tls", "key.pem"))

	r.checked = time.Now().Add(-time.Hour) // 跳过十秒节流
	second, err := cfg.GetCertificate(&tls.ClientHelloInfo{ServerName: "b.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if string(second.Certificate[0]) == string(first.Certificate[0]) {
		t.Error("文件换了之后该用上新证书")
	}
}

// 续期工具写 cert 和 key 是两次、不原子的。中间那一瞬读到的是「新证书 + 旧私钥」，
// 这种对不上的组合必须**保留旧证书**，不能替换、更不能变成握手失败。
func TestHotReloadKeepsOldOnMismatch(t *testing.T) {
	dir := t.TempDir()
	r := loadSelf(t, dir, "a.example.com")
	cfg := r.TLSConfig()
	before, _ := cfg.GetCertificate(&tls.ClientHelloInfo{})

	other := t.TempDir()
	loadSelf(t, other, "b.example.com")
	// 只换证书、不换私钥 —— 模拟写到一半
	copyFile(t, filepath.Join(other, "tls", "cert.pem"), filepath.Join(dir, "tls", "cert.pem"))

	r.checked = time.Now().Add(-time.Hour)
	after, err := cfg.GetCertificate(&tls.ClientHelloInfo{})
	if err != nil {
		t.Fatalf("对不上的时候不能报错，要继续用旧的：%v", err)
	}
	if string(after.Certificate[0]) != string(before.Certificate[0]) {
		t.Error("证书和私钥对不上时换上去了 —— 那样握手会直接失败")
	}
}

// IP 变了要重签（SAN 对不上时浏览器报的错在手机上没有「继续访问」的口子），
// 但设备信任的是 CA，所以 CA 不能跟着换。
func TestLeafResignedButCAStable(t *testing.T) {
	dir := t.TempDir()
	first := loadSelf(t, dir, "a.example.com")

	r2, err := Load(dir, "", "", []net.IP{net.IPv4(192, 168, 1, 5)}, []string{"a.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if r2.LeafFP == first.LeafFP {
		t.Error("IP 变了该重签叶子")
	}
	if r2.CAFP != first.CAFP {
		t.Error("CA 不能变 —— 设备信任的是它，换了就得每台设备重新点一次「继续访问」")
	}
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	b, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, b, 0o600); err != nil {
		t.Fatal(err)
	}
}
