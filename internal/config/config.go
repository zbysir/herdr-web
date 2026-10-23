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
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/viper"

	"github.com/zbysir/herdr-web/internal/relay"
)

type Config struct {
	Host string
	// Port 是**主口**（默认 7788）：只服务本地网络。对端不是私网 / 本机 / 链路本地 /
	// CGNAT 的连接一个字节都不服务（见 server.PrivateListener），**没有开关能让开**。
	//
	// 「默认那个口只是本地的」必须是代码强制的，不能是习惯：在这台机器上开发的人
	// （尤其是 agent）看到的是 `127.0.0.1:7788`，不会知道机器上有没有一条隧道正把这个口
	// 转到公网上 —— 于是「反正只有本机能连」这个前提下做的每个决定（本机免配对、
	// 不配 TLS）都变成一个公网上的洞。从公网进来的唯一一条路是中继（RelayURL）。
	//
	// 以前还有一个「公网口」（PUBLIC_PORT，给 frp / 反代指）和一个「主口就是公网口」的
	// 声明（EXPOSED），2026-10-23 一起下线：那两条路上 TLS 常常在前面那层（VPS）解密，
	// VPS 被拿下就是整个终端（docs/dev/RELAY.md §1）。中继是 TLS 在本机终止的替代。
	Port int
	// LanPort：局域网直连口（自签 TLS，听 0.0.0.0）。0 = 不开。
	//
	// 为什么要单独一个口：**https 页面对 http 目标的 fetch 一律算 active mixed content
	// 被浏览器拦死**（`mode:'no-cors'` 也拦），所以这个口必须是 TLS；而拿真证书的那档
	// 主口证书只对域名有效，Let's Encrypt 不给私网 IP 签，照 IP 直连必然 SAN 不匹配。只有「主口本身就是自签 TLS 且听着局域网」那档不用多开一个，
	// 见 LanDirectPort。
	LanPort int

	// RelayURL / RelayHost：连云端中继（`herdr-relay`）。主机主动连出去，手机打开
	// https://<RelayHost>/，中继按 SNI 把整条 TLS 原样转过来（docs/dev/RELAY.md）。
	// 两个要么都给要么都不给；身份私钥在 Dir/relay 下，由 `herdr-web relay join` 生成。
	//
	// 隧道里冒出来的连接是**第五个口**，规则和公网口完全一样（server.PublicListener）。
	// 不接到主口上：主口那道「只服务本地网络」看对端地址，而隧道连接的对端地址是中继说的。
	RelayURL  string
	RelayHost string
	Dir       string // ~/.herdr-web：配置和文件（softkeys.json / tls / uploads）
	// DataDir 是**内部数据**：设备凭据、passkey 公钥。用户不该手改，所以和配置分开放。
	// （为什么不上 SQLite：这点数据量换 +5MB 二进制和「cat 不了」不划算，而它唯一
	//   能解决的跨进程写，用锁文件就够了 —— 见 internal/runlock。）
	DataDir  string
	Shell    string
	Socket   string // herdr 的 unix socket
	PollMS   int
	SettleMS int
	// NoticeMS：前端多久问一次「有没有新提示」（右上角弹窗 + 面板图标上的红点）。
	// 0 = 关掉整套提示（前端不再轮询）。
	NoticeMS int
	Loopback bool

	// DebugInput：把写进 PTY 的每一批字节 hex 打到日志（HERDR_WEB_DEBUG_INPUT=1）。
	// 排「某个键到底发出去了什么」只能靠它，猜是猜不出来的。
	DebugInput bool

	// UpdateCheck：每天去 GitHub Releases 问一次有没有新版本（默认开）。
	// 关掉它（HERDR_WEB_UPDATE_CHECK=0）之后进程不会有任何出站请求 —— 这是
	// 「内网机器不该主动连外网」那类环境的硬要求，所以必须留个开关。
	// 关掉只是不**自动**查；手动 `herdr-web update --check` 照样能查。
	UpdateCheck bool

	// Files：文件浏览开关（默认开）。关掉之后 /api/files/* 和 /_f/ 全部 404。
	//
	// FileRoots：配了就是**真白名单**（jail），只能看这几棵树；空 = 不设边界。
	// 默认不设边界不是偷懒 —— 能打开这个页面的人已经有一个登录 shell，白名单挡不住
	// 他，只会天天挡路（agent 往 /tmp、/var/folders/… 写图是常态，而这个功能要解决的
	// 恰恰就是「图不在当前 workspace 下」）。详见 internal/files 的包注释。
	Files     bool
	FileRoots []string

	// Git：看 diff 那个面板（默认开）。关掉之后 /api/git/* 全部 404，顶栏那个按钮也不画。
	//
	// **它还压在 Files 底下**：仓库根目录要过文件浏览那道检查（见 internal/gitdiff 的
	// 包注释）—— 一份 diff 就是文件内容，`HERDR_WEB_FILES=0` 却还能看，那个开关就是假的。
	Git bool

	// Chat：chat 模式那个面板（默认开）。关掉之后 /api/chat/* 全部 404，顶栏那个按钮也不画。
	//
	// **它不压在 Files 底下**（和 Git 那条不一样）：转录在 `~/.claude` / `~/.codex` 下，
	// 而 `FileRoots` 一配就是个 jail、那两个目录几乎肯定不在里面 —— 借它的话「配了
	// FILE_ROOTS 的部署上 chat 永远打不开」。换来的义务是 internal/transcript 自己把
	// 路径空间钉死（只认两个根 + session id 过正则 + 必须 .jsonl），见那边的包注释。
	Chat bool

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

	// TrustLoopback：从 127.0.0.1 连上来的免配对。**默认关**。
	//
	// 默认关是被 frp 逼出来的：穿透进来的请求源地址就是 127.0.0.1，开着的话公网上
	// 任何人都直接算「本机」。frp 那条路已经下线，但机器上照样可能有别的穿透（这台开发机
	// 上就有），所以还是默认关。中继那条路上它一律不生效（auth.FromPublicPort）。
	TrustLoopback bool
	// PublicURL 是**用户实际访问的**地址。连中继时默认就是 https://<RelayHost>，一般不用给。
	PublicURL string

	// Retired 是配置里还留着、但已经下线的那几个变量（启动时打一行警告，不拒绝启动 ——
	// 它们被忽略之后行为只会更严，不会更松）。TLS=proxy 例外，那个直接拒绝启动。
	Retired []string

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
}

// retired 是下线了的环境变量（2026-10-23）。看到就警告一句、照样忽略。
//
// 为什么不直接拒绝启动：这些变量被忽略之后行为只会更严（公网口不开、不信 XFF、
// 不认旧 token、没 TLS 照样拒绝启动），而 npm 上的用户升级之后 launchd 拉不起来的话，
// 他人在外面，连「去看一眼日志」的路都没有。
var retired = map[string]string{
	"HERDR_WEB_PUBLIC_PORT":  "公网口下线了，从公网进来改走云端中继（HERDR_WEB_RELAY_*，见 docs/dev/RELAY.md）",
	"HERDR_WEB_EXPOSED":      "「主口就是公网口」的声明下线了，主口永远只服务本地网络；公网走中继",
	"HERDR_WEB_INSECURE":     "裸奔开关下线了：非本机监听又没有 TLS 一律拒绝启动",
	"HERDR_WEB_TRUST_PROXY":  "不再信任 X-Forwarded-For：中继那条路上真实 IP 由隧道带过来，用不着它",
	"HERDR_WEB_LEGACY_TOKEN": "旧 ?token= 兼容下线了，~/.herdr-web/token 可以删掉",
	"HERDR_WEB_TOKEN":        "旧 ?token= 兼容下线了",
}

// cleanRoots 解析 HERDR_WEB_FILE_ROOTS（逗号分隔）。
//
// 展开 ~、转成绝对路径、Clean、去重。**扔掉转不成绝对路径的那些** —— 一个相对路径
// 当 jail 根是没有意义的（相对谁？），而留着它会让前缀检查在某个意想不到的地方通过。
// 这里不检查目录存不存在：配置解析不该碰磁盘，不存在的根在 files.Check 里自然不匹配。
func cleanRoots(raw string) []string {
	var out []string
	for _, r := range strings.Split(raw, ",") {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		if r == "~" || strings.HasPrefix(r, "~/") {
			r = filepath.Join(home(), strings.TrimPrefix(r, "~"))
		}
		if !filepath.IsAbs(r) {
			continue
		}
		out = append(out, filepath.Clean(r))
	}
	return dedupe(out)
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
	v.SetDefault("update_check", true)
	v.SetDefault("files", true) // 文件浏览默认开，见 Config.Files
	v.SetDefault("git", true)   // 看 diff 默认开，见 Config.Git
	v.SetDefault("chat", true)  // chat 模式默认开，见 Config.Chat

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

// noticeMS 收一下提示的轮询间隔：0（含负数）= 关掉，否则不低于 1 秒。
//
// 地板放在这儿而不是 intOf 的 min 里，是因为 0 有**特殊含义**（关掉），min=1000 会把
// 「我想关掉」悄悄变成「一秒一次」。而 1 秒的地板本身没什么代价可省 —— 提示天生比状态
// 慢 2.5 秒（agentwatch 那边的防抖），前端问得再勤也快不过那一段。
func noticeMS(n int) int {
	if n <= 0 {
		return 0
	}
	if n < 1000 {
		return 1000
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
		Host:        v.GetString("host"),
		Port:        intOf(v, "port", 7788, 1),
		LanPort:     intOf(v, "lan_port", 0, 0),
		Dir:         v.GetString("dir"),
		Shell:       v.GetString("shell"),
		Socket:      v.GetString("socket"),
		DebugInput:  v.GetBool("debug_input"),
		UpdateCheck: v.GetBool("update_check"),
		Files:       v.GetBool("files"),
		Git:         v.GetBool("git"),
		Chat:        v.GetBool("chat"),
		// 发件箱那一拍只问「投给谁」（不读屏，见 outbox.Where），500ms 是当初按「切 pane 到
		// 框里跟着变」实测挑的；自动拉回去掉之后这一拍便宜了，数没动。
		PollMS: intOf(v, "poll_ms", 500, 200),
		// 两次 pane.read 之间等多久。**不能是 0**：实测调成 0 时整个清空循环
		// 会读到同一帧陈旧内容，6 轮全跑完仍然清不空（27ms 就返回了）。
		SettleMS: intOf(v, "settle_ms", 120, 0),
		// 4 秒一拍够了：提示本来就比状态晚 2.5 秒（防抖），而这一拍只是一个
		// 「有没有新的」的 GET（不打 herdr socket，读的是内存里那个环）。
		NoticeMS: noticeMS(intOf(v, "notice_ms", 4000, 0)),

		OnConnect:   v.GetString("onconnect"),
		OnConnectMS: intOf(v, "onconnect_ms", 250, 0),

		TLSCert:     v.GetString("tls_cert"),
		TLSKey:      v.GetString("tls_key"),
		TLSMode:     strings.ToLower(v.GetString("tls")),
		ACMEDNS:     strings.ToLower(strings.TrimSpace(v.GetString("acme_dns"))),
		ACMEEmail:   v.GetString("acme_email"),
		ACMEStaging: v.GetBool("acme_staging"),
		// 这几个开关 1 / true / yes 都认（viper 的 GetBool），比只认 "1" 宽容
		TrustLoopback: v.GetBool("trust_loopback"),
		// 90 天滑动过期；设 0 就是永不过期（撤销改成纯手动）。目标是「一台设备配一次」，
		// 所以这个值宁可长不要短 —— 过期一次就等于逼用户回到机器前重配一次。
		DeviceTTLDays: intOf(v, "device_ttl_days", 90, 0),
		RPID:          strings.ToLower(strings.TrimSpace(v.GetString("rpid"))),
		// 24 小时：一天过一次 Face ID 是能接受的摩擦，而它把「cookie 被偷」的
		// 可用窗口从三个月压到一天。调大就是拿这个窗口换省事。
		ReauthHours: intOf(v, "reauth_hours", 24, 0),
		PublicURL:   strings.TrimRight(v.GetString("public_url"), "/"),
		RelayURL:    strings.TrimRight(strings.TrimSpace(v.GetString("relay_url")), "/"),
	}
	for _, h := range strings.Split(v.GetString("hostname"), ",") {
		if h = strings.TrimSpace(h); h != "" {
			c.Hostnames = append(c.Hostnames, strings.ToLower(h))
		}
	}
	c.FileRoots = cleanRoots(v.GetString("file_roots"))

	for name, why := range retired {
		if _, ok := os.LookupEnv(name); ok {
			c.Retired = append(c.Retired, name+"："+why)
		}
	}
	sort.Strings(c.Retired)
	if _, err := os.Stat(filepath.Join(c.Dir, "token")); err == nil {
		c.Retired = append(c.Retired, filepath.Join(c.Dir, "token")+" 还在：旧 token 已经不认了，这个明文文件留着只是多一个能被读走的秘密，删掉")
	}

	if rh := strings.TrimSpace(v.GetString("relay_host")); rh != "" || c.RelayURL != "" {
		if rh == "" || c.RelayURL == "" {
			return nil, errors.New("HERDR_WEB_RELAY_URL 和 HERDR_WEB_RELAY_HOST 要么都给，要么都不给")
		}
		h, err := relay.CleanHost(rh)
		if err != nil {
			return nil, fmt.Errorf("HERDR_WEB_RELAY_HOST: %w", err)
		}
		c.RelayHost = h
		// 进 Host 白名单 / 证书的域名 / passkey 的 Origin。追加在后面而不是插到最前：
		// PasskeyRPID 默认取第一个，已经配着 HOSTNAME 的人换了 RPID 会让注册过的 passkey 全部失效
		c.Hostnames = append(c.Hostnames, h)
		if c.PublicURL == "" {
			c.PublicURL = "https://" + h // 横幅上的二维码就该是这个地址
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
		// 没说的话：连中继、或者听着局域网就自签，纯本机就明文
		// （loopback 上 http 本来就是 secure context，没必要给自己找证书麻烦）
		if !c.Loopback || c.RelayURL != "" {
			c.TLSMode = "auto"
		} else {
			c.TLSMode = "off"
		}
	}
	switch c.TLSMode {
	case "auto", "off":
	case "proxy":
		// 「前面那层终止 TLS」意味着那一层（多半是一台 VPS）看得到整个终端的明文，
		// 还能改页面里的 JS。2026-10-23 下线，换成 TLS 在本机终止的中继。不能静默退回别的
		// 档位：前面那层还在往这个口发明文，换成哪一档都是「看着在跑、一个都打不开」。
		return nil, errors.New("HERDR_WEB_TLS=proxy 下线了：TLS 必须在本进程终止（前面那层解密 = 它被拿下就是整个终端）。" +
			"从公网进来改走云端中继（HERDR_WEB_RELAY_*，见 docs/dev/RELAY.md），本机证书用 HERDR_WEB_ACME_DNS 或 HERDR_WEB_TLS_CERT/KEY")
	default:
		return nil, fmt.Errorf("HERDR_WEB_TLS=%q 不认识（auto / off）", c.TLSMode)
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
	// 中继只转 TLS 密文、靠 ClientHello 里的 SNI 认路，所以 TLS **必须在本进程终止**：
	// 明文在这条路上是「隧道连上了、手机一个都打不开」
	if c.RelayURL != "" && !c.ServesTLS() {
		return nil, fmt.Errorf("连中继要求 TLS 在本进程终止（现在是 HERDR_WEB_TLS=%s）："+
			"配 HERDR_WEB_ACME_DNS 签一张 %s 的真证书，或者给 HERDR_WEB_TLS_CERT/KEY", c.TLSMode, c.RelayHost)
	}
	// 撞口的表现是「第二个监听起不来」，而那条日志夹在启动横幅里很容易看漏，
	// 所以在这儿直接拒绝启动。Port+1 是管理口（见 serve）。
	if c.LanPort > 0 && (c.LanPort == c.Port || c.LanPort == c.Port+1) {
		return nil, fmt.Errorf("HERDR_WEB_LAN_PORT=%d 和主口 %d（或管理口 %d）撞了，换一个", c.LanPort, c.Port, c.Port+1)
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

// PublicReachable：这个部署有没有一条公网进来的路（用来决定限速的「本机永不封」豁免
// 该不该关、启动时要不要强制 TLS）。现在只剩中继这一条。
func (c *Config) PublicReachable() bool { return c.RelayURL != "" }

// RelayDir 放这台主机连中继用的身份私钥。
func (c *Config) RelayDir() string { return filepath.Join(c.Dir, "relay") }

// ServesTLS：本进程自己终止 TLS 吗。
func (c *Config) ServesTLS() bool {
	return c.TLSMode == "auto" || c.TLSMode == "files" || c.TLSMode == "acme"
}

// LanDirectPort 是网页要去嗅探的那个端口。0 = 这个部署没有局域网直连这条路。
//
// 显式配了 LanPort 就是它。没配的话，只有一种情况白送：主口自己就是**自签** TLS 且
// 听着局域网 —— 那张自签证书的 SAN 里本来就有所有局域网 IP（见 cmd 的 certIPs），
// 照 IP 直连就是有效的，不用多开一个口。
//
// 真证书（files / acme）那档**不算**：那张证书只对域名有效，照 IP 连必然 SAN 不匹配。
func (c *Config) LanDirectPort() int {
	if c.LanPort > 0 {
		return c.LanPort
	}
	if c.TLSMode == "auto" && !c.Loopback {
		return c.Port
	}
	return 0
}

// LanNeedsListener 局域网直连要不要另起一个监听（false = 直接用主口那个）。
func (c *Config) LanNeedsListener() bool { return c.LanPort > 0 }

// BrowserHTTPS：**浏览器眼里**是不是 https（cookie 的 Secure 属性跟着它）。
// proxy 档下线之后它和 ServesTLS 是一回事；留着这个名字，是因为调用点问的就是「浏览器眼里」。
func (c *Config) BrowserHTTPS() bool { return c.ServesTLS() }

// LegacyDataFile 是分层之前那个位置（~/.herdr-web/xxx.json）。
// auth 那边发现新位置没文件、旧位置有，就搬过去。
func (c *Config) LegacyDataFile(name string) string { return filepath.Join(c.Dir, name) }
