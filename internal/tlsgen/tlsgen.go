// Package tlsgen 管服务端证书：要么用用户指定的那一对，要么自己签。
//
// 自签这条路是**本地 CA + 短期叶子证书**两层，不是直接签一张自签叶子。多这一层是为了
// 「一台设备配一次」这个目标：设备信任的是 CA，叶子到期或者机器换了 IP 时重签一张新的
// 叶子，手机上什么都不用重做。要是只有一张自签叶子，每次重签都得再点一遍「继续访问」
// 或者重装一次描述文件。
//
// 叶子故意只签 397 天：Apple 对「预装根 CA 签出来的」服务端证书有 398 天上限，虽然
// 用户手动信任的根不在这个限制里，但没必要贴着规则的边走。CA 自己签 10 年。
package tlsgen

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	caValid   = 10 * 365 * 24 * time.Hour
	leafValid = 397 * 24 * time.Hour
	// 离到期还有这么久就提前重签，别等用户某天早上打不开
	renewBefore = 30 * 24 * time.Hour
)

type Result struct {
	Cert tls.Certificate
	// LeafFP 是浏览器警告页上显示的那个指纹；CAFP 是装描述文件时要核对的那个。
	LeafFP, CAFP string
	CAPath       string
	SelfSigned   bool
	DNSNames     []string // 证书里的域名，给 Host 白名单当输入
}

// Load 优先用 certFile/keyFile；两个都空就在 dir/tls 下自签（够用就复用，不够就重签）。
func Load(dir, certFile, keyFile string, ips []net.IP, names []string) (*Result, error) {
	if certFile != "" {
		c, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("读证书 %s: %w", certFile, err)
		}
		leaf, err := x509.ParseCertificate(c.Certificate[0])
		if err != nil {
			return nil, err
		}
		c.Leaf = leaf
		return &Result{Cert: c, LeafFP: fingerprint(c.Certificate[0]), DNSNames: leaf.DNSNames}, nil
	}

	d := filepath.Join(dir, "tls")
	if err := os.MkdirAll(d, 0o700); err != nil {
		return nil, err
	}
	caCert, caKey, caDER, err := loadOrMakeCA(d)
	if err != nil {
		return nil, err
	}
	leafPEM, keyPEM, leafDER, err := loadOrMakeLeaf(d, caCert, caKey, ips, names)
	if err != nil {
		return nil, err
	}
	cert, err := tls.X509KeyPair(leafPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	// 把 CA 也带上：有些客户端拿到完整链才不报「证书不完整」，而且不影响「根本来就不被信任」
	// 这件事 —— 该点的「继续访问」还是要点，除非装了 CA。
	cert.Certificate = append(cert.Certificate, caDER)
	return &Result{
		Cert: cert, LeafFP: fingerprint(leafDER), CAFP: fingerprint(caDER),
		CAPath: filepath.Join(d, "ca.pem"), SelfSigned: true, DNSNames: names,
	}, nil
}

func loadOrMakeCA(d string) (*x509.Certificate, *ecdsa.PrivateKey, []byte, error) {
	certPath, keyPath := filepath.Join(d, "ca.pem"), filepath.Join(d, "ca-key.pem")
	if der, key, err := readPair(certPath, keyPath); err == nil {
		c, err := x509.ParseCertificate(der)
		// CA 快到期就整个重来（连带叶子）—— 十年一次的事，不值得做平滑轮换
		if err == nil && time.Until(c.NotAfter) > renewBefore {
			return c, key, der, nil
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, err
	}
	host, _ := os.Hostname()
	tpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: "herdr-web local CA (" + host + ")"},
		NotBefore:             time.Now().Add(-time.Hour), // 容一点时钟偏差
		NotAfter:              time.Now().Add(caValid),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := writePair(certPath, keyPath, der, key); err != nil {
		return nil, nil, nil, err
	}
	c, err := x509.ParseCertificate(der)
	return c, key, der, err
}

func loadOrMakeLeaf(d string, ca *x509.Certificate, caKey *ecdsa.PrivateKey, ips []net.IP, names []string) (certPEM, keyPEM, der []byte, err error) {
	certPath, keyPath := filepath.Join(d, "cert.pem"), filepath.Join(d, "key.pem")
	if oldDER, _, e := readPair(certPath, keyPath); e == nil {
		if c, e := x509.ParseCertificate(oldDER); e == nil && covers(c, ca, ips, names) {
			cp, _ := os.ReadFile(certPath)
			kp, _ := os.ReadFile(keyPath)
			return cp, kp, oldDER, nil
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, err
	}
	cn := "herdr-web"
	if len(names) > 0 {
		cn = names[0]
	} else if len(ips) > 0 {
		cn = ips[0].String()
	}
	tpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(leafValid),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  ips,
		DNSNames:     names,
	}
	der, err = x509.CreateCertificate(rand.Reader, tpl, ca, &key.PublicKey, caKey)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := writePair(certPath, keyPath, der, key); err != nil {
		return nil, nil, nil, err
	}
	cp, _ := os.ReadFile(certPath)
	kp, _ := os.ReadFile(keyPath)
	return cp, kp, der, nil
}

// covers：手里这张叶子还能不能用。IP 变了（换网、插网线）就得重签，
// 否则浏览器报的是 SAN 不匹配，而那个错在手机上没有「继续访问」的口子。
func covers(c, ca *x509.Certificate, ips []net.IP, names []string) bool {
	if time.Until(c.NotAfter) < renewBefore {
		return false
	}
	if c.CheckSignatureFrom(ca) != nil {
		return false
	}
	for _, ip := range ips {
		found := false
		for _, have := range c.IPAddresses {
			if have.Equal(ip) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	for _, n := range names {
		found := false
		for _, have := range c.DNSNames {
			if strings.EqualFold(have, n) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func readPair(certPath, keyPath string) ([]byte, *ecdsa.PrivateKey, error) {
	cb, err := os.ReadFile(certPath)
	if err != nil {
		return nil, nil, err
	}
	kb, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, nil, err
	}
	cblk, _ := pem.Decode(cb)
	kblk, _ := pem.Decode(kb)
	if cblk == nil || kblk == nil {
		return nil, nil, fmt.Errorf("PEM 解不开")
	}
	key, err := x509.ParseECPrivateKey(kblk.Bytes)
	if err != nil {
		return nil, nil, err
	}
	return cblk.Bytes, key, nil
}

func writePair(certPath, keyPath string, der []byte, key *ecdsa.PrivateKey) error {
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return err
	}
	return os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600)
}

func serial() *big.Int {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return big.NewInt(time.Now().UnixNano())
	}
	return n
}

// fingerprint 按浏览器显示的格式来（SHA-256，两位一组冒号分隔），方便肉眼核对。
func fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	var b strings.Builder
	for i, c := range sum {
		if i > 0 {
			b.WriteByte(':')
		}
		fmt.Fprintf(&b, "%02X", c)
	}
	return b.String()
}
