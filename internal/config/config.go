// Package config 集中所有环境变量和路径，顺带管落盘的 token。
//
// 配置**只从环境变量来**，用 viper 收口（见 newViper）。不读配置文件是故意的：
// 这个口后面是一个登录 shell，「当前生效的配置是什么」必须一眼看得见 —— 环境变量
// 在 ps / systemd unit / launchd plist 里都是明摆着的，再多一个「某个目录下可能
// 还有个 yaml」，出事时先得花半天确认到底哪份生效。
package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/viper"
)

type Config struct {
	Host string
	Port int
	Dir  string // ~/.herdr-web：配置和文件（softkeys.json / tls / uploads）
	// DataDir 是**内部数据**：设备凭据、passkey 公钥。用户不该手改，所以和配置分开放。
	// （为什么不上 SQLite：这点数据量换 +5MB 二进制和「cat 不了」不划算，而它唯一
	//   能解决的跨进程写，用锁文件就够了 —— 见 internal/runlock。）
	DataDir  string
	Shell    string
	Socket   string // herdr 的 unix socket
	PollMS   int
	PushMS   int
	SettleMS int
	Loopback bool

	// DebugInput：把写进 PTY 的每一批字节 hex 打到日志（HERDR_WEB_DEBUG_INPUT=1）。
	// 排「某个键到底发出去了什么」只能靠它，猜是猜不出来的。
	DebugInput bool

	// 连上就自动往 PTY 里敲的那一行（后面自带回车）。默认 `herdr` —— 这个项目本来
	// 就是「浏览器里的 herdr」，开页面十有八九是要进 herdr，少敲一次是一次。
	// 显式设成空串就不敲（`HERDR_WEB_ONCONNECT=`）。
	OnConnect string
	// 等 shell 吐出第一批东西之后再多等多久才敲（ms），见 server/pty.go。
	OnConnectMS int

	// TLS：指了 CERT/KEY 就用那对文件（自己有域名、DNS-01 拿的真证书走这条，最省事）。
	// 否则看 TLSMode："auto" 自签、"off" 明文、"proxy" 表示**前置已经终止了 TLS**
	// （frp 的 https 模式、nginx 反代），本进程收 http 但浏览器看到的是 https。
	TLSCert string
	TLSKey  string
	TLSMode string

	// ACME：自己去签证书，走 DNS-01。ACMEDNS 是服务商名字（空 = 不签）。
	//
	// 为什么必须 DNS-01：HTTP-01 只认 80、TLS-ALPN-01 只认 443，穿透出来的端口经常
	// 都不是这两个。DNS-01 不需要任何入站连接，所以 NAT 后面、甚至把域名指到内网地址
	// 都能签 —— 于是纯局域网部署也能有受信任证书（passkey 就靠它）。
	ACMEDNS     string
	ACMEEmail   string
	ACMEStaging bool
	// Insecure=true 才允许「暴露出去 + 没有 TLS」这种裸奔配置。
	Insecure bool

	// Exposed：**声明这个口能从公网碰到**（frp / 端口转发 / 隧道）。
	//
	// 为什么必须手动声明：走 frp 的时候 herdr-web 往往只监听 127.0.0.1（frpc 从本机连
	// 过来），于是「监听地址是不是 loopback」这个判据完全失效 —— 看起来最安全的配置
	// 实际上暴露在整个互联网上。没有任何办法自动测出来，只能让人自己说。
	//
	// 声明之后：强制要求 TLS、关掉本机免配对、限速封锁默认打开。
	Exposed bool

	// TrustProxy：信任 X-Forwarded-For。**只有确实有个自己的可信前置时才能开** ——
	// 否则攻击者自己发一个 XFF 头就能换一个「源 IP」，把按 IP 的限速和封锁绕干净。
	TrustProxy bool

	// TrustLoopback：从 127.0.0.1 连上来的免配对。**默认关**。
	//
	// 默认关是被 frp 逼出来的：穿透进来的请求源地址就是 127.0.0.1，开着的话公网上
	// 任何人都直接算「本机」，等于把 shell 挂在外网上。纯本机玩的人可以手动打开。
	TrustLoopback bool
	// PublicURL 是**用户实际访问的**地址（比如 https://herdr.example.com:17788）。
	// 走 frp 这类穿透时，公网端口和本地监听端口经常不是一个，本进程自己猜不出来 ——
	// 猜错了横幅上的二维码就是废的。
	PublicURL string

	// 允许的 Host 头里的**域名**（IP 一律放行，理由见 server/guard.go）。
	// 不在名单里的域名直接 421 —— 这是 DNS rebinding 的唯一防线。
	Hostnames []string

	// 设备凭据多久不活跃就失效（每次用都续期）。0 = 永不过期。
	DeviceTTLDays int

	// RPID 是 passkey 绑定的域名。留空就按「HOSTNAME 的第一个 → 本机时用 localhost」推。
	// **裸 IP 不是合法 RPID**（规范里只有 localhost 是特例），所以用 IP 访问的部署
	// 天生用不了 passkey，见 DEPLOY.md 的 B 档。
	RPID string
	// 注册过 passkey 之后，一份会话凭据在「上次生物验证」之后还能用多久。
	// 0 = 不要求重验（passkey 只当登录/换设备的入口）。
	ReauthHours int

	// 旧 token 的兼容档位："on" | "loopback" | "off"。
	// 见 SECURITY.md 的「迁移路径」：这把 token 是明文落盘的长期秘密，只留着换一次
	// 设备凭据，换完就该删。
	LegacyToken string
	Token       string // 旧 token 的明文；没有这个文件就是空
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// newViper 把配置项和环境变量对齐：SetEnvPrefix + AutomaticEnv 之后，配置项
// `poll_ms` 就是环境变量 `HERDR_WEB_POLL_MS`，名字不用两边各写一遍。
func newViper() *viper.Viper {
	v := viper.New()
	v.SetEnvPrefix("HERDR_WEB")
	v.AutomaticEnv()
	// 显式设成空串**算数**：`HERDR_WEB_ONCONNECT=` 就是「连上什么都不敲」。
	// viper 默认把空串当没设、退回默认值，那样有默认值的开关永远关不掉。
	v.AllowEmptyEnv(true)

	v.SetDefault("host", "127.0.0.1")
	v.SetDefault("dir", filepath.Join(home(), ".herdr-web"))
	v.SetDefault("legacy_token", "on")
	v.SetDefault("onconnect", "herdr") // 连上就自动敲这一行，见 Config.OnConnect

	// 这两项的兜底值不在 HERDR_WEB_* 里：shell 跟 $SHELL 走，socket 跟 herdr 自己的
	// $HERDR_SOCKET_PATH 走。BindEnv 按给的顺序找，前一个没有才看后一个。
	_ = v.BindEnv("shell", "HERDR_WEB_SHELL", "SHELL")
	v.SetDefault("shell", "/bin/zsh")
	// 别指望 HERDR_SOCKET_PATH 一定在：PTY 那边会把 HERDR_* 清掉（防嵌套启动），
	// 而这个进程自己也可能根本不是从 herdr pane 里起的。
	_ = v.BindEnv("socket", "HERDR_WEB_SOCKET", "HERDR_SOCKET_PATH")
	v.SetDefault("socket", filepath.Join(home(), ".config", "herdr", "herdr.sock"))
	return v
}

// intOf 读整数配置。**写了个解析不了的值就当没设**（退回 def），而不是让 viper
// 给出 0 —— 把 `DEVICE_TTL_DAYS=9O`（字母 O）静默变成「永不过期」这种事查起来要命。
// min 是地板，各调用点注释里写了为什么是那个数。
func intOf(v *viper.Viper, key string, def, min int) int {
	n := def
	if s := strings.TrimSpace(v.GetString(key)); s != "" {
		if parsed, err := strconv.Atoi(s); err == nil {
			n = parsed
		}
	}
	if n < min {
		return min
	}
	return n
}

func home() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}

// DefaultSocket 是 herdr socket 的路径：HERDR_WEB_SOCKET → HERDR_SOCKET_PATH → 兜底。
func DefaultSocket() string { return newViper().GetString("socket") }

func Load() (*Config, error) {
	v := newViper()
	c := &Config{
		Host:       v.GetString("host"),
		Port:       intOf(v, "port", 7788, 1),
		Dir:        v.GetString("dir"),
		Shell:      v.GetString("shell"),
		Socket:     v.GetString("socket"),
		DebugInput: v.GetBool("debug_input"),
		// 500ms 是实测挑的：切 pane 到 textarea 更新的中位延迟约 500ms，
		// 再往下调收益递减（地板是一次 sync 的 ~150-300ms）。
		PollMS: intOf(v, "poll_ms", 500, 200),
		PushMS: intOf(v, "push_ms", 700, 100),
		// 两次 pane.read 之间等多久。**不能是 0**：实测调成 0 时整个清空循环
		// 会读到同一帧陈旧内容，6 轮全跑完仍然清不空（27ms 就返回了）。
		SettleMS: intOf(v, "settle_ms", 120, 0),

		OnConnect:   v.GetString("onconnect"),
		OnConnectMS: intOf(v, "onconnect_ms", 250, 0),

		TLSCert:     v.GetString("tls_cert"),
		TLSKey:      v.GetString("tls_key"),
		TLSMode:     strings.ToLower(v.GetString("tls")),
		ACMEDNS:     strings.ToLower(strings.TrimSpace(v.GetString("acme_dns"))),
		ACMEEmail:   v.GetString("acme_email"),
		ACMEStaging: v.GetBool("acme_staging"),
		// 这几个开关 1 / true / yes 都认（viper 的 GetBool），比只认 "1" 宽容
		Insecure:      v.GetBool("insecure"),
		Exposed:       v.GetBool("exposed"),
		TrustProxy:    v.GetBool("trust_proxy"),
		TrustLoopback: v.GetBool("trust_loopback"),
		// 90 天滑动过期；设 0 就是永不过期（撤销改成纯手动）。目标是「一台设备配一次」，
		// 所以这个值宁可长不要短 —— 过期一次就等于逼用户回到机器前重配一次。
		DeviceTTLDays: intOf(v, "device_ttl_days", 90, 0),
		LegacyToken:   v.GetString("legacy_token"),
		RPID:          strings.ToLower(strings.TrimSpace(v.GetString("rpid"))),
		// 24 小时：一天过一次 Face ID 是能接受的摩擦，而它把「cookie 被偷」的
		// 可用窗口从三个月压到一天。调大就是拿这个窗口换省事。
		ReauthHours: intOf(v, "reauth_hours", 24, 0),
		PublicURL:   strings.TrimRight(v.GetString("public_url"), "/"),
	}
	for _, h := range strings.Split(v.GetString("hostname"), ",") {
		if h = strings.TrimSpace(h); h != "" {
			c.Hostnames = append(c.Hostnames, strings.ToLower(h))
		}
	}
	if (c.TLSCert == "") != (c.TLSKey == "") {
		return nil, errors.New("HERDR_WEB_TLS_CERT 和 HERDR_WEB_TLS_KEY 要么都给，要么都不给")
	}
	c.DataDir = filepath.Join(c.Dir, "data")
	c.Loopback = c.Host == "127.0.0.1" || c.Host == "localhost" || c.Host == "::1"

	// PublicURL 里的主机名自动算进白名单：既然你就是从那儿访问的，没必要再配一遍
	if c.PublicURL != "" {
		if u, err := url.Parse(c.PublicURL); err == nil && u.Hostname() != "" {
			if net.ParseIP(u.Hostname()) == nil {
				c.Hostnames = append(c.Hostnames, strings.ToLower(u.Hostname()))
			}
		}
	}
	c.Hostnames = dedupe(c.Hostnames) // 两个变量指同一个域名时别往证书里塞两遍
	if c.TLSMode == "" {
		// 没说的话：暴露出去或者听着局域网就自签，纯本机就明文（loopback 上 http
		// 本来就是 secure context，没必要给自己找证书麻烦）
		if c.Exposed || !c.Loopback {
			c.TLSMode = "auto"
		} else {
			c.TLSMode = "off"
		}
	}
	if c.TLSCert != "" {
		c.TLSMode = "files"
	}
	// 配了 ACME 就是「本进程自己终止 TLS」。真正的证书路径要等签完才知道，所以先占一个
	// 档位名，serve() 那边签完再换成 files —— 这样启动早期那个「暴露了没 TLS 就拒绝启动」
	// 的检查才不会误判。
	if c.ACMEDNS != "" {
		c.TLSMode = "acme"
		if len(c.Hostnames) == 0 {
			return nil, errors.New("配了 HERDR_WEB_ACME_DNS 就得给 HERDR_WEB_HOSTNAME —— 证书总得签给某个域名")
		}
	}
	if c.Exposed {
		c.TrustLoopback = false // 声明暴露之后这个豁免只会是个洞
	}

	if c.LegacyToken != "off" {
		c.Token = legacyToken(v, c.Dir)
	}
	return c, nil
}

// PasskeyRPID 推出 passkey 用的 RPID，空字符串表示这个部署用不了 passkey。
//
// 只有域名能当 RPID。`localhost` 是规范里的特例（而且 http://localhost 也算 secure
// context），所以纯本机开发能用 passkey，但换成 127.0.0.1 就不行 —— 那是个 IP。
func (c *Config) PasskeyRPID() string {
	if c.RPID != "" {
		return c.RPID
	}
	if len(c.Hostnames) > 0 {
		return c.Hostnames[0]
	}
	if c.Loopback {
		return "localhost"
	}
	return ""
}

// PasskeyOrigins 是允许的 Origin 列表，必须和浏览器实际发的那个**完全一致**
// （scheme、主机、端口都算），所以把几种可能都列上。
func (c *Config) PasskeyOrigins() []string {
	rpid := c.PasskeyRPID()
	if rpid == "" {
		return nil
	}
	var out []string
	add := func(o string) {
		for _, e := range out {
			if e == o {
				return
			}
		}
		out = append(out, o)
	}
	if c.PublicURL != "" {
		add(c.PublicURL)
	}
	if rpid == "localhost" {
		add(fmt.Sprintf("http://localhost:%d", c.Port))
		add(fmt.Sprintf("https://localhost:%d", c.Port))
	} else {
		add("https://" + rpid)
		add(fmt.Sprintf("https://%s:%d", rpid, c.Port))
	}
	return out
}

// ServesTLS：本进程自己终止 TLS 吗。
func (c *Config) ServesTLS() bool {
	return c.TLSMode == "auto" || c.TLSMode == "files" || c.TLSMode == "acme"
}

// BrowserHTTPS：**浏览器眼里**是不是 https。cookie 的 Secure 属性要跟着这个走，
// 而不是跟着本进程有没有 TLS —— 前置终止 TLS 时本进程收的是 http，但浏览器是 https。
func (c *Config) BrowserHTTPS() bool { return c.ServesTLS() || c.TLSMode == "proxy" }

// LegacyDataFile 是分层之前那个位置（~/.herdr-web/xxx.json）。
// auth 那边发现新位置没文件、旧位置有，就搬过去。
func (c *Config) LegacyDataFile(name string) string { return filepath.Join(c.Dir, name) }

// TokenFile 是旧 token 的落盘位置。迁移完就该删掉它（见 SECURITY.md）。
func (c *Config) TokenFile() string { return filepath.Join(c.Dir, "token") }

// legacyToken 只**读**不生成。
//
// 以前这里会在首启时生成一把永不过期的明文 token 落盘，那把 token 同时充当引导凭据和
// 长期会话凭据。现在长期凭据是每台设备自己的（哈希落盘，见 internal/auth），这把明文
// token 只剩「让旧书签还能进来换一次 cookie」这一个用途，所以**新装不再生成**它 ——
// 磁盘上少一个能直接登录的明文，就少一条被同机 agent 读走的路。
func legacyToken(v *viper.Viper, dir string) string {
	if t := v.GetString("token"); t != "" {
		return t
	}
	if b, err := os.ReadFile(filepath.Join(dir, "token")); err == nil {
		return strings.TrimSpace(string(b))
	}
	return ""
}
