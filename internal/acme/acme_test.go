package acme

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 服务商名字写错时的错误必须能自己解释清楚 —— 这是最容易踩的一脚，
// 而且编译进来的是哪几家只有二进制自己知道。
func TestUnknownProviderErrorListsSupported(t *testing.T) {
	_, err := newDNS("aliyun") // 常见的写错：真名是 alidns
	if err == nil {
		t.Fatal("不认识的服务商必须报错")
	}
	for _, want := range []string{"alidns", "cloudflare", "tencentcloud", "route53"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("错误信息里该列出 %s：%v", want, err)
		}
	}
}

func TestProvidersAllHaveEnvHint(t *testing.T) {
	for _, p := range Providers() {
		if EnvHint(p) == "" {
			t.Errorf("%s 没写要哪些环境变量 —— 用户会不知道该配什么", p)
		}
		// 每一家都要真的能构造（凭据不全会报错，但不能是「不认识」）
		if _, err := newDNS(p); err != nil && strings.Contains(err.Error(), "不认识") {
			t.Errorf("%s 在 Providers() 里但 newDNS 没接：%v", p, err)
		}
	}
}

func TestEnsureNeedsDomains(t *testing.T) {
	c := Config{Dir: t.TempDir(), DNS: "cloudflare"}
	if _, _, _, err := c.Ensure(); err == nil {
		t.Error("没有域名该直接报错，别跑到 ACME 那边去")
	}
}

// 已有证书够用就不要去动 ACME（正式环境一周只给同一组域名签 5 张，
// 每次启动都重签会把自己锁在外面一周）。
func TestEnsureReusesValidCert(t *testing.T) {
	dir := t.TempDir()
	c := Config{Dir: dir, Domains: []string{"herdr.example.com"}, DNS: "cloudflare"}
	certFile, keyFile := c.Files()
	writeTestCert(t, certFile, keyFile, "herdr.example.com", 60*24*time.Hour)

	got, _, renewed, err := c.Ensure()
	if err != nil {
		t.Fatalf("有现成证书就该直接用，却报错：%v", err)
	}
	if renewed {
		t.Error("还剩 60 天，不该重签")
	}
	if got != certFile {
		t.Errorf("路径不对：%s", got)
	}
}

func TestValidForDetectsExpiryAndWrongDomain(t *testing.T) {
	dir := t.TempDir()
	c := Config{Dir: dir, Domains: []string{"a.example.com"}}
	certFile, keyFile := c.Files()

	// 快到期的：算「不够用」由 Ensure 判断，这里只看剩余时间
	writeTestCert(t, certFile, keyFile, "a.example.com", 10*24*time.Hour)
	if d, ok := c.ValidFor(); !ok || d > RenewBefore {
		t.Errorf("该读出一个小于续期阈值的剩余时间，得到 %v ok=%v", d, ok)
	}

	// 换了 HOSTNAME：证书没过期但域名对不上，也必须算「不能用」
	writeTestCert(t, certFile, keyFile, "other.example.com", 60*24*time.Hour)
	if _, ok := c.ValidFor(); ok {
		t.Error("域名对不上的证书必须算不能用，否则换了域名之后一直用旧证书")
	}
}

func TestSafeName(t *testing.T) {
	if got := safeName([]string{"B.example.com", "a.example.com"}); got != "a.example.com_b.example.com" {
		t.Errorf("要排序 + 小写，得到 %q", got)
	}
	if got := safeName([]string{"*.example.com"}); strings.ContainsAny(got, "*/") {
		t.Errorf("通配符要换掉，不然当不了文件名：%q", got)
	}
}

func writeTestCert(t *testing.T, certFile, keyFile, dnsName string, valid time.Duration) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(certFile), 0o700); err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: dnsName},
		DNSNames:     []string{dnsName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(valid),
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
	kb, _ := x509.MarshalECPrivateKey(key)
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600); err != nil {
		t.Fatal(err)
	}
}
