// Package admin 是**只绑 127.0.0.1** 的管理口。
//
// 为什么单独一个监听口，而不是在主服务上加个「需要认证」的页面：
//
//  1. 应该先问「这东西为什么要在公网上」。管理页只会在机器前用（装的时候、续期坏了的时候），
//     挂在公网上买不到任何方便，却把风险面赔进去。认证是个会失效的控制，「碰不到」是个性质。
//  2. **不能靠判断源 IP 来实现「只有本机」** —— 走 frp 这类穿透进来的请求源 IP 也是
//     127.0.0.1，Host 头也能用 curl 伪造。只有「隧道压根没转发这个端口」是伪造不了的。
//  3. **管理页不能依赖它自己要管的那个东西。** 挂在主 TLS 口上的话，证书一坏就打不开那个
//     用来修证书的页面 —— 死锁。绑 loopback 跑 http 天然绕开（loopback 本来是 secure context）。
//
// 不要求认证：能连上 loopback 的东西已经有你的 shell 了，和 ctl.sock 同一个道理。
// **但仍然要防浏览器** —— 见 guard()。
package admin

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/zbysir/herdr-web/internal/acme"
	"github.com/zbysir/herdr-web/internal/auth"
	"github.com/zbysir/herdr-web/internal/config"
	"github.com/zbysir/herdr-web/internal/selfupdate"
)

type Deps struct {
	Cfg      *config.Config
	Store    *auth.Store
	Passkeys *auth.Passkeys
	Gate     *auth.Gate
	// ACME 可能是 nil（没配自动签发）。
	ACME *acme.Manager
	// CertFile 是现在实际在用的证书（自签 / 指定 / ACME 都可能），空表示没有 TLS。
	CertFile   string
	SelfSigned bool
	PairURL    func(code string) string
	// Version 是当前跑的版本号，Updates 是查更新的缓存（可能是 nil）。
	// 管理页要回答「该不该升级」，这两个都得有。
	Version string
	Updates *selfupdate.Checker
}

// Header 是写接口要求的自定义头。跨站请求设不了它（会触发 preflight，而我们不答
// preflight），所以它是这个不认证的口上防「恶意网页替你点按钮」的那一道。
const Header = "X-Herdr-Admin"

func Handler(d Deps) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/state", d.state)
	mux.HandleFunc("/api/cert/renew", d.renew)
	mux.HandleFunc("/api/pair", d.pair)
	mux.HandleFunc("/api/unlock", d.unlock)
	mux.HandleFunc("/api/devices", d.devices)
	mux.HandleFunc("/api/devices/", d.devices)
	mux.HandleFunc("/api/passkeys/", d.delPasskey)
	mux.HandleFunc("/", page)
	return guard(mux)
}

// guard：绑 loopback 挡住了网络上的别人，但**挡不住浏览器** —— 你在这台机器上打开的任何
// 网页都能 fetch http://127.0.0.1:PORT/。所以：
//
//   - Host 必须是 loopback 字面量。挡 DNS rebinding：攻击者把自己的域名解析到 127.0.0.1，
//     浏览器眼里就同源了，那时候 Host 是他的域名。
//   - 写接口要自定义头（见 Header）。
//
// loopback-only ≠ 浏览器安全，这条容易漏。
func guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !loopbackHost(r.Host) {
			http.Error(w, "管理口只认 127.0.0.1 / localhost 这样的地址（挡 DNS rebinding）", http.StatusMisdirectedRequest)
			return
		}
		if r.Method != http.MethodGet && r.Header.Get(Header) == "" {
			http.Error(w, "缺 "+Header+" 头：这个口不认证，所以写操作靠自定义头挡跨站", http.StatusForbidden)
			return
		}
		if o := r.Header.Get("Origin"); o != "" {
			if u, err := url.Parse(o); err != nil || !strings.EqualFold(u.Host, r.Host) {
				http.Error(w, "跨站请求被拒", http.StatusForbidden)
				return
			}
		}
		w.Header().Set("cache-control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func loopbackHost(h string) bool {
	if h == "" {
		return false
	}
	host := h
	if x, _, err := net.SplitHostPort(h); err == nil {
		host = x
	}
	host = strings.ToLower(strings.Trim(host, "[]"))
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

/* ------------------------------------------------------------------ 接口 */

type certInfo struct {
	Have     bool      `json:"have"`
	File     string    `json:"file,omitempty"`
	Subject  string    `json:"subject,omitempty"`
	Issuer   string    `json:"issuer,omitempty"`
	Domains  []string  `json:"domains,omitempty"`
	NotAfter time.Time `json:"notAfter,omitempty"`
	DaysLeft int       `json:"daysLeft"`
	// SelfSigned = 自签（浏览器会警告）；Staging = ACME 测试环境（浏览器也不认）
	SelfSigned bool   `json:"selfSigned"`
	Staging    bool   `json:"staging"`
	Err        string `json:"err,omitempty"`
}

func (d Deps) state(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{
		"version":  d.versionInfo(),
		"cert":     d.certInfo(),
		"devices":  d.Store.Devices(),
		"passkeys": d.Passkeys.List(),
		"locked":   d.Gate.Locked(),
		"cfg": map[string]any{
			"hostnames":   d.Cfg.Hostnames,
			"publicURL":   d.Cfg.PublicURL,
			"tlsMode":     d.Cfg.TLSMode,
			"exposed":     d.Cfg.Exposed,
			"acmeDNS":     d.Cfg.ACMEDNS,
			"acmeStaging": d.Cfg.ACMEStaging,
			"rpid":        d.Cfg.PasskeyRPID(),
			"reauthHours": d.Cfg.ReauthHours,
			"ttlDays":     d.Cfg.DeviceTTLDays,
			"dataDir":     d.Cfg.DataDir,
		},
		"providers": providerHelp(),
	}
	if d.ACME != nil {
		out["lastAttempt"] = d.ACME.Last()
	}
	writeJSON(w, 200, out)
}

// versionInfo 回答「当前什么版本、有没有新的、怎么升」。
//
// 只读缓存，不在这个请求里去问 GitHub：管理页是修东西的地方，它必须在断网时也能开。
func (d Deps) versionInfo() map[string]any {
	out := map[string]any{"current": d.Version}
	if d.Updates == nil {
		return out
	}
	st := d.Updates.State()
	out["latest"] = st.Latest
	out["url"] = st.URL
	out["outdated"] = selfupdate.Newer(strings.TrimPrefix(d.Version, "v"), st.Latest)
	if !st.CheckedAt.IsZero() {
		out["checkedAt"] = st.CheckedAt
	}
	if st.Err != "" {
		out["err"] = st.Err
	}
	// 升级命令取决于当初怎么装的，前端自己猜不出来
	if inst, err := selfupdate.Detect(); err == nil {
		if c := inst.Command(); c != "" {
			out["how"] = c
		} else {
			out["how"] = "herdr-web update"
		}
	}
	return out
}

func (d Deps) certInfo() certInfo {
	ci := certInfo{SelfSigned: d.SelfSigned}
	if d.ACME != nil {
		ci.Staging = d.ACME.Config().Staging
	}
	if d.CertFile == "" {
		return ci
	}
	ci.File = d.CertFile
	b, err := os.ReadFile(d.CertFile)
	if err != nil {
		ci.Err = err.Error()
		return ci
	}
	blk, _ := pem.Decode(b)
	if blk == nil {
		ci.Err = "证书文件解不开"
		return ci
	}
	c, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		ci.Err = err.Error()
		return ci
	}
	ci.Have = true
	ci.Subject = c.Subject.CommonName
	ci.Issuer = c.Issuer.CommonName
	ci.Domains = c.DNSNames
	ci.NotAfter = c.NotAfter
	ci.DaysLeft = int(time.Until(c.NotAfter).Hours() / 24)
	return ci
}

func (d Deps) renew(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不对", 405)
		return
	}
	if d.ACME == nil {
		writeJSON(w, 400, map[string]any{"err": "没有配 HERDR_WEB_ACME_DNS —— 证书不是这个进程签的，没法在这儿续"})
		return
	}
	var body struct{ Staging bool }
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body)
	// 这一步会去敲 ACME 和 DNS，慢（要等 DNS 传播），几十秒是正常的
	writeJSON(w, 200, d.ACME.EnsureWith(body.Staging))
}

func (d Deps) pair(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不对", 405)
		return
	}
	code, exp := d.Store.MintCode()
	out := map[string]any{"code": code, "expires": exp}
	if d.PairURL != nil {
		out["url"] = d.PairURL(code)
	}
	writeJSON(w, 200, out)
}

func (d Deps) unlock(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不对", 405)
		return
	}
	d.Gate.Unlock()
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (d Deps) devices(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/devices")
	id = strings.Trim(id, "/")
	switch {
	case r.Method == http.MethodGet:
		writeJSON(w, 200, map[string]any{"devices": d.Store.Devices()})
	case r.Method == http.MethodDelete && id == "":
		writeJSON(w, 200, map[string]any{"revoked": d.Store.RevokeAll()})
	case r.Method == http.MethodDelete:
		label, ok := d.Store.Revoke(id)
		if !ok {
			writeJSON(w, 404, map[string]any{"err": "没有这台设备"})
			return
		}
		writeJSON(w, 200, map[string]any{"revoked": 1, "label": label})
	default:
		http.Error(w, "方法不对", 405)
	}
}

func (d Deps) delPasskey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "方法不对", 405)
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/passkeys"), "/")
	label, ok := d.Passkeys.Delete(id)
	if !ok {
		writeJSON(w, 404, map[string]any{"err": "没有这把 passkey"})
		return
	}
	writeJSON(w, 200, map[string]any{"deleted": 1, "label": label})
}

/* ------------------------------------------------------- DNS 服务商配置助手 */

type provider struct {
	Name    string   `json:"name"`
	Label   string   `json:"label"`
	Vars    []string `json:"vars"`
	Console string   `json:"console"`
	Perm    string   `json:"perm"`
}

// providerHelp 是「不想手填 env」里真正烦的那部分：不知道该填什么、去哪儿建、给多大权限。
// 凭据本身仍然由用户放进 .env —— 我们不接管保管（那样就没法用 Keychain 之类了）。
//
// Vars 里写的是**带 HERDR_WEB_ 前缀**的名字（见 internal/acme/env.go）：页面上那段 .env
// 片段是直接抄走用的，写成光秃秃的名字就等于教人配一份 `service install` 抄不进去的东西。
func providerHelp() []provider {
	meta := map[string]provider{
		"cloudflare": {
			Label: "Cloudflare", Vars: []string{"HERDR_WEB_CLOUDFLARE_DNS_API_TOKEN"},
			Console: "https://dash.cloudflare.com/profile/api-tokens",
			Perm:    "用 Edit zone DNS 模板，Zone Resources 收窄到你那个域名。别用 Global API Key",
		},
		"alidns": {
			Label: "阿里云 DNS", Vars: []string{"HERDR_WEB_ALICLOUD_ACCESS_KEY", "HERDR_WEB_ALICLOUD_SECRET_KEY"},
			Console: "https://ram.console.aliyun.com/users",
			Perm:    "建 RAM 子用户 + AliyunDNSFullAccess。别用主账号 AccessKey",
		},
		"tencentcloud": {
			Label: "腾讯云 / DNSPod", Vars: []string{"HERDR_WEB_TENCENTCLOUD_SECRET_ID", "HERDR_WEB_TENCENTCLOUD_SECRET_KEY"},
			Console: "https://console.cloud.tencent.com/cam/user",
			Perm:    "建子用户 + QcloudDNSPodFullAccess",
		},
		"route53": {
			Label: "AWS Route 53", Vars: []string{"HERDR_WEB_AWS_ACCESS_KEY_ID", "HERDR_WEB_AWS_SECRET_ACCESS_KEY", "HERDR_WEB_AWS_REGION"},
			Console: "https://console.aws.amazon.com/iam/home#/users",
			Perm:    "最小策略见 DNS.md。REGION 必填（写 us-east-1 就行）",
		},
		"digitalocean": {
			Label: "DigitalOcean", Vars: []string{"HERDR_WEB_DO_AUTH_TOKEN"},
			Console: "https://cloud.digitalocean.com/account/api/tokens",
			Perm:    "细粒度 token，只勾 domain 的 read + write",
		},
		"huaweicloud": {
			Label:   "华为云 DNS",
			Vars:    []string{"HERDR_WEB_HUAWEICLOUD_ACCESS_KEY_ID", "HERDR_WEB_HUAWEICLOUD_SECRET_ACCESS_KEY", "HERDR_WEB_HUAWEICLOUD_REGION"},
			Console: "https://console.huaweicloud.com/iam/",
			Perm:    "IAM 用户 + DNS FullAccess。REGION 必填，如 cn-north-4",
		},
	}
	out := make([]provider, 0, len(meta))
	for _, name := range acme.Providers() {
		p := meta[name]
		p.Name = name
		if p.Label == "" {
			p.Label = name
		}
		if len(p.Vars) == 0 {
			p.Vars = []string{acme.EnvHint(name)}
		}
		out = append(out, p)
	}
	return out
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
