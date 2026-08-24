// Command herdr-web 是浏览器里的 herdr 终端：一个二进制，起一个 web server，
// 前端产物嵌在里面。
//
// 子命令（都不需要密码，认证靠 ~/.herdr-web/ctl.sock 的文件权限）：
//
//	herdr-web pair            出一个一次性配对码 + 二维码
//	herdr-web devices         列出已配对设备
//	herdr-web revoke <id|all> 踢掉某台 / 全部
//	herdr-web unlock          解开「失败太多」的全局熔断
//
// 命令行用 cobra，配置全部走环境变量（viper 在 internal/config 里收口）。
// 这里**只有一个 --web 标志**，开发时指前端目录用；其余一律 HERDR_WEB_*，
// 免得同一个设置有两个入口，还得规定谁盖谁。
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/zbysir/herdr-web/internal/acme"
	"github.com/zbysir/herdr-web/internal/admin"
	"github.com/zbysir/herdr-web/internal/agentwatch"
	"github.com/zbysir/herdr-web/internal/auth"
	"github.com/zbysir/herdr-web/internal/config"
	"github.com/zbysir/herdr-web/internal/ctl"
	"github.com/zbysir/herdr-web/internal/lan"
	"github.com/zbysir/herdr-web/internal/qr"
	"github.com/zbysir/herdr-web/internal/runlock"
	"github.com/zbysir/herdr-web/internal/selfupdate"
	"github.com/zbysir/herdr-web/internal/server"
	"github.com/zbysir/herdr-web/internal/tlsgen"
	"github.com/zbysir/herdr-web/internal/version"
	"github.com/zbysir/herdr-web/internal/webui"
)

func main() {
	// 没有 ldflags 注入时（go install pkg@v1.2.3）从 build info 里兜一个版本号
	version.FromBuildInfo()
	if err := rootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "  ✗ "+err.Error())
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	var webDir string
	root := &cobra.Command{
		Use:   "herdr-web",
		Short: "浏览器里的 herdr 终端（一个二进制，前端嵌在里面）",
		Long: "浏览器里的 herdr 终端：起一个 web server，手机 / 平板扫码就能进。\n\n" +
			"配置全部走环境变量（HERDR_WEB_*，见 README 的环境变量表）。",
		Args: cobra.NoArgs,
		// 运行期出错时别再刷一屏用法；错误统一由 main 按 "  ✗ …" 打，
		// 免得同一条错误被 cobra 和我们各打一遍
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          func(*cobra.Command, []string) error { return serve(webDir) },
		Version:       version.Version,
	}
	root.Flags().StringVarP(&webDir, "web", "w", "", "从这个目录伺候前端（开发用；留空则用嵌进二进制的那份）")
	root.AddCommand(pairCmd(), devicesCmd(), revokeCmd(), unlockCmd(),
		versionCmd(), updateCmd(), serviceCmd())
	return root
}

// serve 起服务。这里的错误一律 return 而不是 log.Fatal —— Fatal 会跳过 defer，
// 攒着的 LastSeen 就落不了盘了。
func serve(webDir string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("读配置失败: %w", err)
	}

	var web fs.FS
	if webDir != "" {
		web = os.DirFS(webDir)
		if _, err := fs.Stat(web, "index.html"); err != nil {
			return fmt.Errorf("--web %s 里没有 index.html", webDir)
		}
	} else {
		web = webui.FS()
	}

	// 这个口后面是一个登录 shell。**能从公网碰到又没有 TLS** 的组合直接拒绝启动 ——
	// 以前这里只打一行警告，警告没人看。
	if (cfg.PublicReachable() || !cfg.Loopback) && !cfg.BrowserHTTPS() && !cfg.Insecure {
		why := "监听的是 " + cfg.Host + "，不是本机地址"
		if cfg.Exposed {
			why = "声明了 HERDR_WEB_EXPOSED=1（这个口能从公网碰到）"
		}
		if cfg.PublicPort > 0 {
			why = fmt.Sprintf("开了公网口 HERDR_WEB_PUBLIC_PORT=%d", cfg.PublicPort)
		}
		return fmt.Errorf("拒绝启动：%s，但没有配 TLS。\n\n"+
			"  这个口后面是一个登录 shell，明文传输等于把它挂在网上（抓包就能拿到 cookie 和整个终端画面）。\n"+
			"  三条路挑一条：\n"+
			"    1. 有自己的域名（最省事，浏览器零警告）：用 DNS-01 签一张真证书，然后\n"+
			"       HERDR_WEB_TLS_CERT=…/fullchain.pem HERDR_WEB_TLS_KEY=…/privkey.pem\n"+
			"    2. 自签：HERDR_WEB_TLS=auto（每台设备第一次要点一下「继续访问」，或者装一次 CA）\n"+
			"    3. 前置已经终止了 TLS（frp 的 https 模式 / nginx）：HERDR_WEB_TLS=proxy\n\n"+
			"  真要裸奔：HERDR_WEB_INSECURE=1\n", why)
	}

	store, err := auth.New(auth.Config{
		Dir:           cfg.DataDir,
		LegacyFile:    cfg.LegacyDataFile("devices.json"),
		TTL:           time.Duration(cfg.DeviceTTLDays) * 24 * time.Hour,
		Secure:        cfg.BrowserHTTPS(),
		LegacyToken:   cfg.LegacyToken,
		Token:         cfg.Token,
		TrustLoopback: cfg.TrustLoopback,
		TrustProxy:    cfg.TrustProxy,
	})
	if err != nil {
		return fmt.Errorf("读设备列表失败: %w", err)
	}
	// 拿住数据目录的锁：命令行那边靠它判断「服务在跑」，不然它会退回直接改文件
	unlock, ok, err := runlock.Acquire(cfg.DataDir)
	if err != nil {
		log.Printf("锁文件建不起来（命令行的并发保护会失效）: %v", err)
	} else if !ok {
		log.Printf("\n  ⚠️  %s 已经有另一个实例在用了。两个服务写同一份设备凭据会丢数据 ——\n"+
			"     要跑第二个实例请另给一个 HERDR_WEB_DIR。\n", cfg.DataDir)
	} else {
		defer unlock()
	}

	alert := func(msg string) { log.Printf("\n  ⚠️  %s\n", msg) }
	gate := auth.NewGate()
	gate.Alert = alert

	// 「本机永不封」这个豁免只有在**拿到的 IP 确实是客户端的**时候才成立：
	//   - 信任前置（XFF 给的是真实 IP）→ loopback 只会出现在真本机，豁免安全；
	//   - 有公网入口但没有可信前置 → 可能是 frp 这类穿透，所有请求的源 IP 都是
	//     127.0.0.1 且不可信，留着豁免等于整层限速空转；
	//   - 纯本地部署 → 本机 / 局域网，照常豁免。
	gate.ExemptLoopback = cfg.TrustProxy || !cfg.PublicReachable()

	// ACME 要在 tlsgen 之前：签完把路径交给它，后面就当成「用户指定的证书」走。
	var acmeMgr *acme.Manager
	if cfg.ACMEDNS != "" {
		acmeMgr = acme.NewManager(acme.Config{
			Dir: cfg.DataDir, Domains: cfg.Hostnames, Email: cfg.ACMEEmail,
			DNS: cfg.ACMEDNS, Staging: cfg.ACMEStaging,
		})
		if a := acmeMgr.Ensure(false); a.Err != "" {
			return fmt.Errorf("ACME 签证书失败: %s", a.Err)
		} else if a.Renewed {
			log.Printf("已签下 %s 的证书", strings.Join(cfg.Hostnames, " "))
		}
		certFile, keyFile := acmeMgr.Config().Files()
		cfg.TLSCert, cfg.TLSKey, cfg.TLSMode = certFile, keyFile, "files"

		// 半天看一次要不要续。续完不用重启 —— tlsgen 那边会在十秒内热重载。
		go func() {
			for range time.Tick(12 * time.Hour) {
				if a := acmeMgr.Ensure(false); a.Err != "" {
					alert("证书续期失败（先看 DNS 凭据还在不在）：" + a.Err)
				} else if a.Renewed {
					log.Printf("证书已续期，热重载会在十秒内生效")
				}
			}
		}()
	}

	var cert *tlsgen.Result
	if cfg.ServesTLS() {
		if cert, err = tlsgen.Load(cfg.Dir, cfg.TLSCert, cfg.TLSKey, certIPs(cfg), cfg.Hostnames); err != nil {
			return fmt.Errorf("准备证书失败: %w", err)
		}
	}

	// 局域网直连口（HERDR_WEB_LAN_PORT）。**必须是 TLS** —— 见 config.Config.LanPort
	// 那段注释：https 页面嗅探不到 http 目标，明文的口等于这条路不存在。
	// 主口本来就是自签的话同一张就够（那张证书的 SAN 里已经有所有局域网 IP）。
	var lanCert *tlsgen.Result
	if cfg.LanNeedsListener() {
		if cert != nil && cert.SelfSigned {
			lanCert = cert
		} else if lanCert, err = tlsgen.Load(cfg.Dir, "", "", certIPs(cfg), cfg.Hostnames); err != nil {
			return fmt.Errorf("局域网直连口的自签证书准备失败: %w", err)
		}
	}
	// IP 会变（换 Wi-Fi、插网线），而 SAN 不匹配那个错在手机上没有「继续访问」的口子。
	// 半分钟看一次，变了就重签写盘，跑着的监听自己热重载 —— 见 tlsgen.Resign。
	if cfg.LanDirectPort() > 0 && (lanCert != nil && lanCert.SelfSigned || cert != nil && cert.SelfSigned) {
		go func() {
			for range time.Tick(30 * time.Second) {
				if err := tlsgen.Resign(cfg.Dir, certIPs(cfg), cfg.Hostnames); err != nil {
					log.Printf("局域网 IP 变了但重签证书失败: %v", err)
				}
			}
		}()
	}

	// passkey：RPID 必须是域名，裸 IP 访问的部署这里会拿到空字符串，
	// 于是 Passkeys.Available() 为假，相关的口和按钮都不出现。
	passkeys, err := auth.NewPasskeys(auth.PasskeyConfig{
		Dir: cfg.DataDir, LegacyFile: cfg.LegacyDataFile("passkeys.json"),
		RPID: cfg.PasskeyRPID(), Origins: cfg.PasskeyOrigins(), Display: "herdr-web",
	})
	if err != nil {
		log.Fatalf("passkey 初始化失败: %v", err)
	}

	// 盯着两个凭据文件：被外部改过就吼一声。它们怕写不怕读 —— 往里加一行就等于
	// 给自己发一份能进来的凭据，而这台机器上的 agent 是以你的身份跑的，写得动。
	go func() {
		for range time.Tick(30 * time.Second) {
			store.CheckTampered(alert)
			passkeys.CheckTampered(alert)
		}
	}()

	// 后台盯新版本。放在这里而不是更早：前面那些检查可能直接 return，没必要为一个
	// 已经要退出的进程去发请求。
	updates := startUpdateWatch(cfg)

	// 盯 agent 状态变化，给「面板一览」的时间列打时间戳。herdr 的 API 里没有任何
	// 时间戳，所以只能这一侧自己记（细节见 internal/agentwatch 的包注释）。
	// herdr 没在跑也无所谓：它自己按 5 秒重试，只在日志里说一次。
	agents := agentwatch.New(cfg.Socket, filepath.Join(cfg.Dir, "agent-seen.json"))
	agents.Start(context.Background())

	names := append([]string{}, cfg.Hostnames...)
	if cert != nil {
		names = append(names, cert.DNSNames...)
	}
	srv := server.New(cfg, web, store, gate, server.Options{
		BrowserHTTPS: cfg.BrowserHTTPS(), Hostnames: names,
		Passkeys:    passkeys,
		ReauthAfter: time.Duration(cfg.ReauthHours) * time.Hour,
		RPID:        cfg.PasskeyRPID(),
		Version:     version.Version,
		Updates:     updates,
		Agents:      agents,
		LanPort:     cfg.LanDirectPort(),
	})

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("监听 %s 失败: %w", addr, err)
	}
	var mainTLS *tls.Config
	if cert != nil {
		cert.OnReload = func(leaf *x509.Certificate) {
			if leaf != nil {
				log.Printf("换上了新证书（到期 %s）", leaf.NotAfter.Format("2006-01-02"))
			}
		}
		mainTLS = cert.TLSConfig()
		ln = tls.NewListener(ln, mainTLS)
	}

	if l, err := ctl.Listen(cfg.Dir, ctlHandler(cfg, store, gate)); err != nil {
		log.Printf("命令行通道起不来（子命令会用不了）: %v", err)
	} else if l != nil {
		defer l.Close()
	}

	// 局域网直连口：和主口同一个 handler，只是自己带 TLS 且听 0.0.0.0。
	// 起不来不影响主服务 —— 那时候只是没有加速，网页嗅探失败会安静地留在公网那条路上。
	if lanCert != nil {
		lanAddr := fmt.Sprintf("0.0.0.0:%d", cfg.LanPort)
		if lanLn, err := net.Listen("tcp", lanAddr); err != nil {
			log.Printf("局域网直连口 %s 起不来（不影响主服务）: %v", lanAddr, err)
		} else {
			defer lanLn.Close()
			// 和主口是同一张自签证书时**复用同一份 tls.Config**：在一个 Result 上
			// 调两次 TLSConfig() 会不上锁地改它的内部状态，和另一个监听的握手抢。
			conf := mainTLS
			if lanCert != cert {
				conf = lanCert.TLSConfig()
			}
			// 摘转发头 + 盖「从直连口进来」的章（交接令牌只在这种请求上兑得动）——
			// 见 server.LanListener
			h := server.LanListener(srv.Handler())
			go func() {
				if err := http.Serve(tls.NewListener(lanLn, conf), h); err != nil {
					log.Printf("局域网直连口挂了: %v", err)
				}
			}()
		}
	}

	// 公网口：隧道 / 端口转发 / 反代该指的那个口。和主口同一个 handler，区别在认证上
	// 按「公网」对待（本机免配对之类的豁免一律不生效，见 server.PublicListener）。
	//
	// 听 0.0.0.0 是必须的：隧道从本机连过来只需要 127.0.0.1，但路由器端口转发那条路
	// 要的是通配地址，两者只能取宽的那个。
	//
	// **起不来就拒绝启动**（局域网口和管理口是软失败）：这个口是远程访问的唯一入口，
	// 软失败的表现是「服务好像起来了，就是外面连不上」，而人在外面，看不到日志。
	if cfg.PublicPort > 0 {
		pubAddr := fmt.Sprintf("0.0.0.0:%d", cfg.PublicPort)
		pubLn, err := net.Listen("tcp", pubAddr)
		if err != nil {
			return fmt.Errorf("公网口 %s 起不来: %w", pubAddr, err)
		}
		defer pubLn.Close()
		if mainTLS != nil {
			// 和主口同一张证书就复用同一份 tls.Config：在一个 Result 上调两次
			// TLSConfig() 会不上锁地改它的内部状态，和另一个监听的握手抢。
			pubLn = tls.NewListener(pubLn, mainTLS)
		}
		h := server.PublicListener(srv.Handler())
		go func() {
			if err := http.Serve(pubLn, h); err != nil {
				log.Printf("公网口挂了: %v", err)
			}
		}()
	}

	// 管理口：只绑 127.0.0.1。为什么单独一个口而不是在主服务上加个认证页面，
	// 见 internal/admin 的包注释（一句话：不能靠源 IP 判断「本机」，而且管理页
	// 不能依赖它自己要管的那个证书）。
	adminAddr := fmt.Sprintf("127.0.0.1:%d", cfg.Port+1)
	adminLn, err := net.Listen("tcp", adminAddr)
	if err != nil {
		log.Printf("管理口 %s 起不来（不影响主服务）: %v", adminAddr, err)
	} else {
		defer adminLn.Close()
		certFile := cfg.TLSCert
		if cert != nil && cert.SelfSigned {
			certFile = filepath.Join(cfg.Dir, "tls", "cert.pem")
		}
		h := admin.Handler(admin.Deps{
			Cfg: cfg, Store: store, Passkeys: passkeys, Gate: gate, ACME: acmeMgr,
			CertFile: certFile, SelfSigned: cert != nil && cert.SelfSigned,
			PairURL: func(code string) string { return pairURL(cfg, code) },
			Version: version.Version, Updates: updates,
		})
		go func() {
			if err := http.Serve(adminLn, h); err != nil {
				log.Printf("管理口挂了: %v", err)
			}
		}()
	}

	banner(cfg, store, passkeys, cert, web == nil, adminAddr, updates)
	defer store.Flush() // 把攒着的 LastSeen 落盘
	// 主口默认**只服务本地网络**（见 server.PrivateListener）。声明了 EXPOSED=1 才让开
	// 那道门 —— 那是「主口就是公网口」的老写法，新配置该用 HERDR_WEB_PUBLIC_PORT。
	mainHandler := srv.Handler()
	if cfg.MainIsPrivate() {
		mainHandler = server.PrivateListener(mainHandler)
	}
	return http.Serve(ln, mainHandler)
}

/* ------------------------------------------------------------------ 子命令 */

// 每个子命令自己 config.Load()：它们只用到 cfg.Dir 这类路径，和服务进程互不影响，
// 也就不需要一个全局 cfg 变量在那儿等着被谁提前写坏。
func pairCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pair",
		Short: "出一个一次性配对码 + 二维码",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			res, err := ctl.Call(cfg.Dir, ctl.Request{Cmd: "pair"})
			if err != nil {
				if err == ctl.ErrNoServer {
					// 配对码只活在服务进程的内存里（故意的），所以离线出不了码
					return fmt.Errorf("服务没在跑。先起 herdr-web，启动横幅里就有一个配对码")
				}
				return err
			}
			fmt.Println()
			fmt.Printf("  配对码  %s      %s 内有效，用一次就废\n", res.Code, time.Until(res.Expires).Round(time.Second))
			if res.URL != "" {
				fmt.Println("  " + res.URL)
				for _, l := range qr.Render(res.URL) {
					fmt.Println("  " + l)
				}
			}
			fmt.Println()
			return nil
		},
	}
}

func devicesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "devices",
		Short: "列出已配对设备（标签 / 最后活跃 / 最后 IP / 到期）",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			devs, err := devices(cfg)
			if err != nil {
				return err
			}
			if len(devs) == 0 {
				fmt.Println("  还没有配对过的设备。起服务之后扫横幅里的二维码，或者 herdr-web pair。")
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
			fmt.Fprintln(w, "  ID\t标签\t最后活跃\t最后 IP\t到期")
			for _, d := range devs {
				exp := "永不"
				if !d.Expires.IsZero() {
					exp = d.Expires.Format("2006-01-02")
				}
				fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\n", d.ID, d.Label, ago(d.LastSeen), d.LastIP, exp)
			}
			return w.Flush()
		},
	}
}

func revokeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "revoke <id|all>",
		Short: "踢掉某台设备（all = 全部），下一个请求立刻 401",
		// 自己校验而不是 cobra.ExactArgs(1)：那个报的是「accepts 1 arg(s)」，
		// 而这里想说清楚「ID 去哪儿看、前四位就够」
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				return fmt.Errorf("要给一个设备 ID（herdr-web devices 里看，前四位就够），或者 all")
			}
			return nil
		},
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			arg := args[0]
			if res, err := ctl.Call(cfg.Dir, ctl.Request{Cmd: "revoke", Arg: arg}); err == nil {
				fmt.Println("  ✓ " + res.Msg)
				return nil
			} else if err != ctl.ErrNoServer {
				return err
			}
			// 要退回「直接改文件」之前，先确认真的没有服务在跑。
			// 「socket 连不上」不等于「服务没在跑」—— 两个进程写同一份文件会静默丢数据。
			if err := requireNoServer(cfg); err != nil {
				return err
			}
			st, err := offlineStore(cfg)
			if err != nil {
				return err
			}
			if arg == "all" || arg == "--all" {
				fmt.Printf("  ✓ 撤销了 %d 台设备\n", st.RevokeAll())
				return nil
			}
			label, ok := st.Revoke(arg)
			if !ok {
				return fmt.Errorf("没有这台设备：%s", arg)
			}
			fmt.Println("  ✓ 撤销了 " + label)
			return nil
		},
	}
}

func unlockCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unlock",
		Short: "解开「失败太多」的全局熔断",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if _, err := ctl.Call(cfg.Dir, ctl.Request{Cmd: "unlock"}); err != nil {
				if err == ctl.ErrNoServer {
					return fmt.Errorf("服务没在跑；熔断状态只在内存里，重启就清了")
				}
				return err
			}
			fmt.Println("  ✓ 已解开，可以配新设备了")
			return nil
		},
	}
}

// devices 优先问正在跑的服务（它内存里的 LastSeen 比文件新），不行就读文件。
func devices(cfg *config.Config) ([]auth.Device, error) {
	if res, err := ctl.Call(cfg.Dir, ctl.Request{Cmd: "devices"}); err == nil {
		return res.Devices, nil
	} else if err != ctl.ErrNoServer {
		return nil, err
	}
	if runlock.InUse(cfg.DataDir) {
		// 服务在跑但通道连不上：读文件会给出一个过期的答案（服务内存里那份才是准的）
		return nil, fmt.Errorf("服务在跑，但命令行通道（%s）连不上，列出来的会是过期数据。\n"+
			"  先看看服务启动日志里 ctl.sock 那一行报了什么", ctl.Path(cfg.Dir))
	}
	st, err := offlineStore(cfg)
	if err != nil {
		return nil, err
	}
	return st.Devices(), nil
}

// requireNoServer：只有确认没有服务占着这个数据目录，才允许直接改文件。
func requireNoServer(cfg *config.Config) error {
	if !runlock.InUse(cfg.DataDir) {
		return nil
	}
	return fmt.Errorf("服务正在跑（%s 被占着），但命令行通道 %s 连不上，"+
		"所以不能直接改文件 —— 两边同时写会静默丢数据。\n"+
		"  先看服务启动日志里 ctl.sock 那一行；或者停掉服务再执行这条命令",
		cfg.DataDir, ctl.Path(cfg.Dir))
}

func offlineStore(cfg *config.Config) (*auth.Store, error) {
	return auth.New(auth.Config{
		Dir: cfg.DataDir, LegacyFile: cfg.LegacyDataFile("devices.json"),
		TTL: time.Duration(cfg.DeviceTTLDays) * 24 * time.Hour,
	})
}

func ctlHandler(cfg *config.Config, store *auth.Store, gate *auth.Gate) func(ctl.Request) ctl.Response {
	return func(req ctl.Request) ctl.Response {
		switch req.Cmd {
		case "pair":
			code, exp := store.MintCode()
			return ctl.Response{Code: code, Expires: exp, URL: pairURL(cfg, code)}
		case "devices":
			return ctl.Response{Devices: store.Devices()}
		case "revoke":
			if req.Arg == "all" || req.Arg == "--all" {
				n := store.RevokeAll()
				return ctl.Response{N: n, Msg: fmt.Sprintf("撤销了 %d 台设备", n)}
			}
			label, ok := store.Revoke(req.Arg)
			if !ok {
				return ctl.Response{Err: "没有这台设备：" + req.Arg}
			}
			return ctl.Response{N: 1, Msg: "撤销了 " + label}
		case "unlock":
			gate.Unlock()
			return ctl.Response{Msg: "ok"}
		}
		return ctl.Response{Err: "不认识的命令 " + req.Cmd}
	}
}

func ago(t time.Time) string {
	if t.IsZero() {
		return "从没"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "刚刚"
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟前", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小时前", int(d.Hours()))
	default:
		return fmt.Sprintf("%d 天前", int(d.Hours()/24))
	}
}

/* ------------------------------------------------------------------ 启动横幅 */

func scheme(cfg *config.Config) string {
	if cfg.BrowserHTTPS() {
		return "https"
	}
	return "http"
}

// base 是「用户在地址栏里看到的」前缀。走 frp 时公网端口和本地端口经常不一样，
// 所以 HERDR_WEB_PUBLIC_URL 一给就以它为准。
func base(cfg *config.Config, host string) string {
	if cfg.PublicURL != "" {
		return cfg.PublicURL
	}
	return fmt.Sprintf("%s://%s:%d", scheme(cfg), host, cfg.Port)
}

func pairURL(cfg *config.Config, code string) string {
	host := "127.0.0.1"
	if len(cfg.Hostnames) > 0 {
		host = cfg.Hostnames[0]
	} else if nics := lan.Addresses(); len(nics) > 0 && !cfg.Loopback {
		host = nics[0].Address
	}
	return base(cfg, host) + "/?pair=" + code
}

func certIPs(cfg *config.Config) []net.IP {
	ips := []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}
	for _, n := range lan.Addresses() {
		if ip := net.ParseIP(n.Address); ip != nil {
			ips = append(ips, ip)
		}
	}
	return ips
}

func banner(cfg *config.Config, store *auth.Store, passkeys *auth.Passkeys, cert *tlsgen.Result, noWeb bool, adminAddr string, updates *selfupdate.Checker) {
	fmt.Println()
	fmt.Println("  herdr-web " + version.Version + " 已启动")
	fmt.Println("  " + base(cfg, "127.0.0.1") + "/")

	var nics []lan.Addr
	if !cfg.Loopback {
		nics = lan.Addresses()
	}
	for i, n := range nics {
		tag := ""
		if i == 0 {
			tag = "  ← 手机用这个"
		}
		fmt.Printf("  %s/   %s%s\n", base(cfg, n.Address), n.Name, tag)
	}
	// 局域网直连口。**必须说「点一次继续访问」**：那张证书是自签的，不点过一次的话
	// 网页那边的嗅探会因为证书不认而失败 —— 而失败是静默的（安静走公网），人根本
	// 不会知道这条路存在过。见 internal/server/lanapi.go。
	if origins := lan.Origins(cfg.LanDirectPort()); len(origins) > 0 {
		fmt.Println()
		fmt.Println("  局域网直连（不绕公网，按键往返快一截）：")
		for _, o := range origins {
			fmt.Println("    " + o + "/")
		}
		fmt.Println("    ↑ 每台设备**先手动开一次**上面任一个、点「继续访问」（自签证书）。")
		fmt.Println("      之后从公网那个地址进来，网页会自己探到它并切过去。")
		fmt.Println("      建议在路由器上给这台机器绑个固定内网 IP —— 地址一变那一下信任就得重来。")
	}

	fmt.Println()
	if cfg.PublicPort > 0 {
		fmt.Printf("  公网口：%d ← 隧道 / 端口转发 / 反代指这个口。\n", cfg.PublicPort)
		fmt.Printf("    主口 %d 只服务本地网络（公网连过来会被拒），它上面的本机免配对在公网口不生效。\n", cfg.Port)
	} else if cfg.Exposed {
		fmt.Printf("  主口 %d 被声明成公网口了（HERDR_WEB_EXPOSED=1，老写法）。\n", cfg.Port)
		fmt.Printf("    建议改成 HERDR_WEB_PUBLIC_PORT=<另一个端口> 并让隧道指过去：\n")
		fmt.Printf("    那样漏配的后果是「外面连不上」，而不是「本地那些宽松默认全暴露在公网」。\n")
	} else {
		fmt.Printf("  主口 %d 只服务本地网络（公网要另开 HERDR_WEB_PUBLIC_PORT）。\n", cfg.Port)
	}
	fmt.Printf("  管理页（只本机能开）：http://%s/\n", adminAddr)
	fmt.Printf("  shell：%s   数据目录：%s\n", cfg.Shell, cfg.Dir)
	fmt.Printf("  herdr socket：%s\n", cfg.Socket)

	devs := store.Devices()
	fmt.Printf("  已配对设备：%d 台", len(devs))
	if len(devs) > 0 {
		fmt.Print("（herdr-web devices 看列表，revoke 踢人）")
	}
	fmt.Println()

	if passkeys.Available() {
		fmt.Printf("  passkey：%d 把（域名 %s）", passkeys.Count(), cfg.PasskeyRPID())
		if passkeys.Count() == 0 {
			fmt.Print(" —— 在网页的设备面板里加一把，之后换设备就不用回机器前了")
		} else if cfg.ReauthHours > 0 {
			fmt.Printf("，每 %d 小时要重验一次", cfg.ReauthHours)
		}
		fmt.Println()
	} else {
		fmt.Println("  passkey：用不了（得用域名访问，裸 IP 不能当 WebAuthn 的标识）")
	}

	if cert != nil {
		if cert.SelfSigned {
			fmt.Println("  证书：自签")
			fmt.Println("    CA 指纹  SHA-256 " + short(cert.CAFP))
			fmt.Println("    想彻底去掉浏览器警告：把 " + cert.CAPath + " 装到设备上并信任")
		} else if cfg.ACMEDNS != "" {
			left := "?"
			if d, ok := (acme.Config{Dir: cfg.DataDir, Domains: cfg.Hostnames}).ValidFor(); ok {
				left = fmt.Sprintf("%.0f 天后到期", d.Hours()/24)
			}
			env := "正式"
			if cfg.ACMEStaging {
				env = "**测试环境（浏览器不认这张证书）**"
			}
			fmt.Printf("  证书：ACME 自动签发（DNS-01 / %s / %s），%s\n", cfg.ACMEDNS, env, left)
		} else {
			fmt.Println("  证书：用的是 " + cfg.TLSCert)
		}
	} else if cfg.TLSMode == "proxy" {
		fmt.Println("  TLS：由前置终止（本进程收 http，cookie 仍按 https 下发）")
	}

	if noWeb {
		fmt.Println("  ⚠️  没有前端产物：先 npm --prefix web run build，或者用 --web web 指向开发目录")
	}

	// 一次性配对码：新设备扫它。已经配过的设备不需要，它们带着 cookie 直接进。
	code, exp := store.MintCode()
	u := pairURL(cfg, code)
	fmt.Println()
	fmt.Printf("  配对新设备（%s 内有效，用一次就废）：%s\n", time.Until(exp).Round(time.Minute), code)
	fmt.Println("  " + u)
	if lines := qr.Render(u); len(lines) > 0 {
		fmt.Println()
		for _, l := range lines {
			fmt.Println("  " + l)
		}
	}

	fmt.Println()
	if cfg.Exposed {
		fmt.Println("  ⚠️  声明了 HERDR_WEB_EXPOSED=1：这个口能从公网碰到。")
		fmt.Println("     已关闭「本机免配对」（穿透进来的请求源地址也是 127.0.0.1），限速和封锁生效。")
	} else if !cfg.Loopback {
		fmt.Println("  ⚠️  正在监听 " + cfg.Host + "：局域网里的人能碰到这个口。")
	}
	if cfg.PublicPort > 0 && cfg.PublicURL == "" {
		fmt.Println("  ⚠️  开了公网口但没给 HERDR_WEB_PUBLIC_URL：上面那个配对链接和二维码指的是本机地址，")
		fmt.Println("     从公网扫它进不来（公网端口和本地端口通常不是一个，本进程猜不出来）。")
	}
	if cfg.TrustLoopback {
		fmt.Println("  ⚠️  本机免配对是开着的（HERDR_WEB_TRUST_LOOPBACK=1）。")
		if cfg.PublicPort > 0 {
			fmt.Printf("     它只在主口 %d 上生效，公网口 %d 不认这个豁免。\n", cfg.Port, cfg.PublicPort)
		} else {
			fmt.Println("     主口只服务本地网络，所以这个豁免够不到公网 —— 但别把隧道指到主口上。")
		}
	}
	if cfg.Token != "" && cfg.LegacyToken == "on" {
		fmt.Println("  ⚠️  " + cfg.TokenFile() + " 还在，旧链接仍然能进来换凭据。")
		fmt.Println("     设备都迁完就删掉它（或者 HERDR_WEB_LEGACY_TOKEN=loopback 只留本机可用）。")
	}
	if !cfg.BrowserHTTPS() && !cfg.Loopback {
		fmt.Println("  ⚠️  明文 http：抓包就能拿到 cookie 和整个终端画面。")
	}
	// 有新版本就在这儿说一句。用缓存，不在启动路径上发请求 —— 网络慢的时候
	// 那会变成「启动卡住十秒」，而这条提示不值那个代价。
	if st, ok := updates.Available(); ok {
		fmt.Println("  ⬆️  " + updateNotice(st))
	}
	fmt.Println()
}

// short 把指纹截短：横幅上要的是「能核对」，不是「能抄」。
func short(fp string) string {
	parts := strings.Split(fp, ":")
	if len(parts) <= 8 {
		return fp
	}
	return strings.Join(parts[:4], ":") + " … " + strings.Join(parts[len(parts)-4:], ":")
}
