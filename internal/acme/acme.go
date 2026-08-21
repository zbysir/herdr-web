// Package acme 自己去签证书，用 **DNS-01**。
//
// 为什么必须是 DNS-01：HTTP-01 只认 80 端口、TLS-ALPN-01 只认 443，而穿透出来的端口经常
// 都不是这两个（frp 的 remotePort 随便挑）。DNS-01 **不需要任何入站连接** —— 只要能改一条
// TXT 记录就行，所以：
//
//   - 家宽在 NAT 后面也能签；
//   - **把域名的 A 记录指到内网地址（192.168.x.x）一样能签**，于是纯局域网部署也能拿到
//     浏览器默认信任的证书 → 没有警告页，而且 passkey 能用（它要求标识是域名）。
//
// 只 import 用到的那几个 provider，不 import 聚合的 providers/dns —— 那个包会把一百多家
// DNS 的 SDK 全带进来，二进制会涨一个数量级。实测每家的边际成本只有 0.5–1.8 MB。
package acme

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/challenge"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/registration"

	"github.com/go-acme/lego/v4/providers/dns/alidns"
	"github.com/go-acme/lego/v4/providers/dns/cloudflare"
	"github.com/go-acme/lego/v4/providers/dns/digitalocean"
	"github.com/go-acme/lego/v4/providers/dns/huaweicloud"
	"github.com/go-acme/lego/v4/providers/dns/route53"
	"github.com/go-acme/lego/v4/providers/dns/tencentcloud"
)

// RenewBefore：剩这么多就去续。Let's Encrypt 签 90 天，30 天的余量够重试很多次。
const RenewBefore = 30 * 24 * time.Hour

type Config struct {
	// Dir 放证书、账号私钥和注册信息。
	Dir     string
	Domains []string
	// Email 是 ACME 账号邮箱。可以空着（LE 允许），但那样到期提醒也收不到。
	Email string
	// DNS 是 provider 名字，见 Providers()。凭据**只从环境变量读**，不落盘 ——
	// 落盘的话它和 TOTP 密钥是同一类问题：这台机器上的 agent 读得到。
	DNS string
	// Staging 打开就用 LE 的测试环境。**调试时一定要开**：正式环境有速率限制
	//（同一组域名一周 5 张证书），试几次就把自己锁在外面了，而且锁的是一周。
	Staging bool
}

// Providers 是编译进来的 DNS 服务商，以及各自要的环境变量。
//
// 加一家是两行：这张表加一行，newDNS 里加一个 case。
func Providers() []string {
	out := make([]string, 0, len(envHint))
	for k := range envHint {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

var envHint = map[string]string{
	"cloudflare":   "CLOUDFLARE_DNS_API_TOKEN（Zone:DNS:Edit 权限）",
	"alidns":       "ALICLOUD_ACCESS_KEY + ALICLOUD_SECRET_KEY",
	"tencentcloud": "TENCENTCLOUD_SECRET_ID + TENCENTCLOUD_SECRET_KEY",
	"route53":      "AWS_ACCESS_KEY_ID + AWS_SECRET_ACCESS_KEY + AWS_REGION",
	"digitalocean": "DO_AUTH_TOKEN",
	"huaweicloud":  "HUAWEICLOUD_ACCESS_KEY_ID + HUAWEICLOUD_SECRET_ACCESS_KEY + HUAWEICLOUD_REGION",
}

// EnvHint 给错误信息用：告诉用户这家要哪几个环境变量。
func EnvHint(name string) string { return envHint[name] }

func newDNS(name string) (challenge.Provider, error) {
	switch name {
	case "cloudflare":
		return cloudflare.NewDNSProvider()
	case "alidns":
		return alidns.NewDNSProvider()
	case "tencentcloud":
		return tencentcloud.NewDNSProvider()
	case "route53":
		return route53.NewDNSProvider()
	case "digitalocean":
		return digitalocean.NewDNSProvider()
	case "huaweicloud":
		return huaweicloud.NewDNSProvider()
	}
	return nil, fmt.Errorf("不认识的 DNS 服务商 %q，编译进来的有：%s",
		name, strings.Join(Providers(), " / "))
}

/* ------------------------------------------------------------------ 对外 */

// Files 是证书落盘的位置。
//
// **测试环境和正式环境分开存。** 否则「用 staging 试一次」会把线上那张真证书覆盖成
// 浏览器不认的那张 —— 一个调试动作把生产打挂。分开之后还顺带解决了另一个坑：
// 从 staging 切回正式时不会把 staging 那张当成「现有的还够用」。
func (c Config) Files() (certFile, keyFile string) {
	base := filepath.Join(c.Dir, "acme", safeName(c.Domains))
	if c.Staging {
		base += ".staging"
	}
	return base + ".crt", base + ".key"
}

// Ensure 保证磁盘上有一张够用的证书：没有就签，快到期就续，都不需要就原样返回。
// 返回的 renewed 表示这次真的动过（调用方可以据此打日志）。
func (c Config) Ensure() (certFile, keyFile string, renewed bool, err error) {
	certFile, keyFile = c.Files()
	if len(c.Domains) == 0 {
		return "", "", false, fmt.Errorf("没有域名，签不了证书（设 HERDR_WEB_HOSTNAME）")
	}
	if left, ok := validFor(certFile, c.Domains); ok && left > RenewBefore {
		return certFile, keyFile, false, nil
	}
	if err := c.obtain(certFile, keyFile); err != nil {
		return "", "", false, err
	}
	return certFile, keyFile, true, nil
}

// ValidFor 现有证书还能用多久。ok=false 表示没有、读不出来、或者域名对不上。
func (c Config) ValidFor() (time.Duration, bool) {
	certFile, _ := c.Files()
	return validFor(certFile, c.Domains)
}

func (c Config) obtain(certFile, keyFile string) error {
	dns, err := newDNS(c.DNS)
	if err != nil {
		if h := EnvHint(c.DNS); h != "" {
			// lego 自己的措辞会把两组备选凭据一起列出来，容易看花。补一句我们推荐的那组。
			return fmt.Errorf("%w\n  %s 要的是：%s", err, c.DNS, h)
		}
		return err
	}
	acct, err := c.account()
	if err != nil {
		return err
	}

	lc := lego.NewConfig(acct)
	lc.Certificate.KeyType = certcrypto.EC256
	if c.Staging {
		lc.CADirURL = lego.LEDirectoryStaging
	}
	client, err := lego.NewClient(lc)
	if err != nil {
		return err
	}
	// lego 默认会等 DNS 传播（自己查一遍 TXT 记录），别关 —— 关了只会换来「验证失败」。
	if err := client.Challenge.SetDNS01Provider(dns); err != nil {
		return err
	}

	if acct.Reg == nil {
		reg, err := client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
		if err != nil {
			return fmt.Errorf("注册 ACME 账号失败: %w", err)
		}
		acct.Reg = reg
		if err := acct.save(); err != nil {
			return err
		}
	}

	res, err := client.Certificate.Obtain(certificate.ObtainRequest{
		Domains: c.Domains, Bundle: true,
	})
	if err != nil {
		return fmt.Errorf("签证书失败（DNS 服务商 %s，要的环境变量：%s）: %w",
			c.DNS, EnvHint(c.DNS), err)
	}

	if err := os.MkdirAll(filepath.Dir(certFile), 0o700); err != nil {
		return err
	}
	// 先写临时文件再 rename：热重载那边是按 mtime 触发的，写一半被读到会拿到
	// 「新证书 + 旧私钥」这种对不上的组合。
	if err := writeAtomic(certFile, res.Certificate, 0o644); err != nil {
		return err
	}
	return writeAtomic(keyFile, res.PrivateKey, 0o600)
}

/* ------------------------------------------------------------------ 账号 */

// account 实现 lego 的 registration.User。
//
// 账号私钥是这个 ACME 账号的身份：丢了只能重新注册一个（不影响已经签出来的证书，
// 但续期得用新账号）。所以它要落盘，0600。
type account struct {
	Email string                 `json:"email"`
	Reg   *registration.Resource `json:"reg"`

	key     crypto.PrivateKey
	regPath string
}

func (a *account) GetEmail() string                        { return a.Email }
func (a *account) GetRegistration() *registration.Resource { return a.Reg }
func (a *account) GetPrivateKey() crypto.PrivateKey        { return a.key }

func (a *account) save() error {
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(a.regPath, b, 0o600)
}

func (c Config) account() (*account, error) {
	dir := filepath.Join(c.Dir, "acme")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	keyPath := filepath.Join(dir, "account.key")

	var key *ecdsa.PrivateKey
	if b, err := os.ReadFile(keyPath); err == nil {
		if blk, _ := pem.Decode(b); blk != nil {
			key, _ = x509.ParseECPrivateKey(blk.Bytes)
		}
	}
	if key == nil {
		var err error
		if key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader); err != nil {
			return nil, err
		}
		der, err := x509.MarshalECPrivateKey(key)
		if err != nil {
			return nil, err
		}
		if err := writeAtomic(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), 0o600); err != nil {
			return nil, err
		}
	}

	a := &account{Email: c.Email, key: key, regPath: filepath.Join(dir, "account.json")}
	if b, err := os.ReadFile(a.regPath); err == nil {
		_ = json.Unmarshal(b, a) // 读不出来就当没注册过，下面会重新注册
	}
	return a, nil
}

/* ------------------------------------------------------------------ 小工具 */

// validFor 读证书看还剩多久，顺便确认域名覆盖得上 —— 换了 HOSTNAME 之后旧证书
// 虽然没过期但已经不对了，那种也要重签。
func validFor(certFile string, domains []string) (time.Duration, bool) {
	b, err := os.ReadFile(certFile)
	if err != nil {
		return 0, false
	}
	blk, _ := pem.Decode(b)
	if blk == nil {
		return 0, false
	}
	cert, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		return 0, false
	}
	for _, d := range domains {
		if cert.VerifyHostname(d) != nil {
			return 0, false
		}
	}
	return time.Until(cert.NotAfter), true
}

func writeAtomic(path string, b []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// safeName 把域名列表变成一个能当文件名的东西。
//
// **先转小写再排序**：反过来的话大小写不同会算出不同的文件名（'B' < 'a'），
// 于是同一组域名换个写法就白重签一张证书 —— 而正式环境一周只给 5 张。
func safeName(domains []string) string {
	d := make([]string, 0, len(domains))
	for _, x := range domains {
		d = append(d, strings.ToLower(x))
	}
	sort.Strings(d)
	s := strings.ReplaceAll(strings.Join(d, "_"), "*", "wildcard")
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			return r
		}
		return '-'
	}, s)
}
