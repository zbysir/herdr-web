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
	"sync"
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
	// OnReload 换上新证书之后回调一次（打日志用）。
	OnReload func(*x509.Certificate)

	// 热重载：证书在磁盘上被换掉（ACME 续期、IP 变了重签）之后，跑着的进程要能捡起来。
	// 不然表现是「续期脚本明明成功了，某天早上所有设备一起报证书过期，重启一下就好」——
	// 而这个进程重启的代价比普通 web 服务高得多：每个连着的终端都会断、PTY 被杀。
	certFile, keyFile string
	mu                sync.RWMutex
	cur               *tls.Certificate
	mtimes            [2]time.Time
	checked           time.Time
}

// TLSConfig 给 tls.NewListener 用。走 GetCertificate 回调而不是静态的 Certificates，
// 这样换证书不用重启。
func (r *Result) TLSConfig() *tls.Config {
	r.cur = &r.Cert
	r.mtimes = r.stat()
	r.checked = time.Now()
	return &tls.Config{GetCertificate: r.get, MinVersion: tls.VersionTLS12}
}

func (r *Result) get(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	r.reloadIfChanged()
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cur, nil
}

func (r *Result) stat() [2]time.Time {
	var out [2]time.Time
	for i, p := range [2]string{r.certFile, r.keyFile} {
		if st, err := os.Stat(p); err == nil {
			out[i] = st.ModTime()
		}
	}
	return out
}

// reloadIfChanged 每 10 秒最多看一次 —— 每次握手都 stat 两个文件太浪费。
func (r *Result) reloadIfChanged() {
	if r.certFile == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if time.Since(r.checked) < 10*time.Second {
		return
	}
	r.checked = time.Now()
	now := r.stat()
	if now == r.mtimes {
		return
	}
	// **只有整对都能对上才替换**：续期工具写 cert 和 key 是两次、不原子的，
	// 中间那一瞬读到的是「新证书 + 旧私钥」。X509KeyPair 会校验它们配不配对，
	// 不配对就保留旧的、下一拍再试 —— 宁可服务一张过期证书（用户能点继续），
	// 也不能握手直接失败。
	c, err := tls.LoadX509KeyPair(r.certFile, r.keyFile)
	if err != nil {
		return
	}
	r.cur = &c
	r.mtimes = now
	if r.OnReload != nil {
		go r.OnReload(c.Leaf)
	}
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
		return &Result{
			Cert: c, LeafFP: fingerprint(c.Certificate[0]), DNSNames: leaf.DNSNames,
			certFile: certFile, keyFile: keyFile,
		}, nil
	}

	d := filepath.Join(dir, "tls")
	if err := os.MkdirAll(d, 0o700); err != nil {
		return nil, err
	}
	caCert, caKey, caDER, err := loadOrMakeCA(d)
	if err != nil {
		return nil, err
	}
	leafPEM, keyPEM, leafDER, err := loadOrMakeLeaf(d, caCert, caDER, caKey, ips, names)
	if err != nil {
		return nil, err
	}
	cert, err := tls.X509KeyPair(leafPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	return &Result{
		Cert: cert, LeafFP: fingerprint(leafDER), CAFP: fingerprint(caDER),
		CAPath: filepath.Join(d, "ca.pem"), SelfSigned: true, DNSNames: names,
		certFile: filepath.Join(d, "cert.pem"), keyFile: filepath.Join(d, "key.pem"),
	}, nil
}

// Resign 在局域网 IP 变了（换 Wi-Fi、插网线、DHCP 换了段）之后把自签证书重签一遍。
//
// 它只往磁盘写 —— **跑着的监听不用换，也不用重启**：Result 会在十秒内热重载
// （见 reloadIfChanged）。IP 没变就什么都不做（covers 为真时只读一遍文件）。
//
// 为什么这件事必须有人定期做：SAN 不匹配在手机上报的是 `ERR_CERT_COMMON_NAME_INVALID`，
// 那个错**没有「继续访问」的口子**，而局域网直连这条路本来就是靠「点一次继续」建立
// 信任的。启动时签一次不够，跑着的时候换个网就废了。
func Resign(dir string, ips []net.IP, names []string) error {
	d := filepath.Join(dir, "tls")
	caCert, caKey, caDER, err := loadOrMakeCA(d)
	if err != nil {
		return err
	}
	_, _, _, err = loadOrMakeLeaf(d, caCert, caDER, caKey, ips, names)
	return err
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
	if err := writePair(certPath, keyPath, der, der, key); err != nil {
		return nil, nil, nil, err
	}
	c, err := x509.ParseCertificate(der)
	return c, key, der, err
}

func loadOrMakeLeaf(d string, ca *x509.Certificate, caDER []byte, caKey *ecdsa.PrivateKey, ips []net.IP, names []string) (certPEM, keyPEM, der []byte, err error) {
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
	// 写 fullchain（叶子 + CA）而不是只写叶子：热重载那边只会重新读这个文件，
	// 要是链上的 CA 只活在内存里，重载之后就丢了。
	if err := writePair(certPath, keyPath, append(der, caDER...), der, key); err != nil {
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

// writePair：chain 是要写进证书文件的（可能是 叶子+CA 两块），der 只用来算指纹。
func writePair(certPath, keyPath string, chain, der []byte, key *ecdsa.PrivateKey) error {
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	// 用复数版：ParseCertificate（单数）后面还有数据会直接报 trailing data，
	// 而我们传进来的就是「叶子 DER + CA DER」拼在一起的。
	var buf []byte
	if certs, err := x509.ParseCertificates(chain); err == nil {
		for _, c := range certs {
			buf = append(buf, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})...)
		}
	}
	if len(buf) == 0 {
		buf = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	}
	if err := os.WriteFile(certPath, buf, 0o644); err != nil {
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
