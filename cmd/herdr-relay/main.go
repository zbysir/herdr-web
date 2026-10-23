// herdr-relay：云端中继。跑在一台有公网 IP 的 VPS 上，主机（herdr-web）主动连进来，
// 手机打开 https://<主机名>/ 时按 SNI 把整条 TLS 原样转给那台主机。
//
// 它**不终止主机的 TLS、不存任何能登录的东西**，为什么见 docs/dev/RELAY.md。
//
// 配置和 herdr-web 一样只走环境变量（前缀 HERDR_RELAY_）：
//
//	HERDR_RELAY_DOMAIN   中继自己的域名（控制面），relay.example.com        必填
//	HERDR_RELAY_ZONE     主机名都在它下面，h.example.com                   必填
//	HERDR_RELAY_LISTEN   监听地址，默认 :443
//	HERDR_RELAY_DIR      数据目录，默认 ~/.herdr-relay
//	HERDR_RELAY_TLS_CERT / HERDR_RELAY_TLS_KEY   控制面的证书；不给就自己用 TLS-ALPN-01 签
//	HERDR_RELAY_ACME_EMAIL                       自己签时的 ACME 邮箱（可空）
//
// 前面已经有一层占着 443 的反代（Traefik / nginx）时，用下面两项：
//
//	HERDR_RELAY_CONTROL_LISTEN   控制面单独听一个**明文**口，由前面那层终止 TLS 再转进来
//	                             （那层和中继在同一台 VPS 上，信任边界没变）。设了它就不自己签证书
//	HERDR_RELAY_PROXY_PROTOCOL   =1：SNI 那个口上的连接开头必须带 PROXY 头（前面那层透传时加的），
//	                             不然中继看到的手机地址全是反代自己的，按 IP 的限速整层失效
package main

import (
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pires/go-proxyproto"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"

	"github.com/zbysir/herdr-web/internal/relay"
	"github.com/zbysir/herdr-web/internal/version"
)

type config struct {
	Domain, Zone, Listen, Dir string
	Cert, Key, Email          string
	Control                   string
	ProxyProtocol             bool
}

func load() config {
	v := viper.New()
	v.SetEnvPrefix("HERDR_RELAY")
	v.AutomaticEnv()
	home, _ := os.UserHomeDir()
	v.SetDefault("listen", ":443")
	v.SetDefault("dir", filepath.Join(home, ".herdr-relay"))
	c := config{
		Domain: strings.ToLower(strings.TrimSpace(v.GetString("domain"))),
		Zone:   strings.Trim(strings.ToLower(strings.TrimSpace(v.GetString("zone"))), "."),
		Listen: v.GetString("listen"),
		Dir:    v.GetString("dir"),
		Cert:   v.GetString("tls_cert"),
		Key:    v.GetString("tls_key"),
		Email:  v.GetString("acme_email"),

		Control:       v.GetString("control_listen"),
		ProxyProtocol: v.GetBool("proxy_protocol"),
	}
	return c
}

// 子命令只用到数据目录，起服务要的更多，在 serve 里核。
func (c config) store() relay.Store { return relay.Store{Dir: c.Dir} }

func main() {
	root := &cobra.Command{
		Use:           "herdr-relay",
		Short:         "herdr-web 的云端中继：只转发密文，TLS 在主机上终止",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version.Version,
		RunE:          func(*cobra.Command, []string) error { return serve() },
	}
	root.AddCommand(inviteCmd(), hostsCmd(), removeCmd())
	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "  ✗ %v\n", err)
		os.Exit(1)
	}
}

func serve() error {
	c := load()
	if c.Domain == "" || c.Zone == "" {
		return errors.New("要给 HERDR_RELAY_DOMAIN（中继自己的域名）和 HERDR_RELAY_ZONE（主机名都在它下面）")
	}
	if (c.Cert == "") != (c.Key == "") {
		return errors.New("HERDR_RELAY_TLS_CERT 和 HERDR_RELAY_TLS_KEY 要么都给，要么都不给")
	}

	var conf *tls.Config
	if c.Control != "" {
		// 控制面由前面那层终止 TLS，这里不要证书
	} else if c.Cert != "" {
		pair, err := tls.LoadX509KeyPair(c.Cert, c.Key)
		if err != nil {
			return fmt.Errorf("读控制面证书失败: %w", err)
		}
		conf = &tls.Config{Certificates: []tls.Certificate{pair}}
	} else {
		// TLS-ALPN-01：只要 443，不用 80。白名单只有中继自己的名字 —— 主机名那些握手根本到不了
		// 这里（按 SNI 分走了），但多一道总没错：autocert 对名单外的名字一律不签
		m := &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(c.Domain),
			Cache:      autocert.DirCache(filepath.Join(c.Dir, "autocert")),
			Email:      c.Email,
		}
		conf = m.TLSConfig()
	}
	if conf != nil {
		// 去掉 h2：控制面是 http.Serve 在一个自己包的 TLS 监听上跑的，那条路**不会**自动开 HTTP/2。
		// 留着 h2 的话浏览器会协商成 h2、而服务端讲的是 HTTP/1.1，打开首页就是一个协议错误
		conf.NextProtos = []string{"http/1.1", acme.ALPNProto}
	}

	ln, err := net.Listen("tcp", c.Listen)
	if err != nil {
		return fmt.Errorf("监听 %s 失败: %w", c.Listen, err)
	}
	if c.ProxyProtocol {
		// 默认策略就是 REQUIRE：没带 PROXY 头的连接一律拒。不能放宽成「有就读、没有就算了」——
		// 那样谁直连这个口都能自己写一个头、冒充任意来源地址
		ln = &proxyproto.Listener{Listener: ln, ReadHeaderTimeout: 10 * time.Second}
	}
	r := &relay.Relay{Domain: c.Domain, Zone: c.Zone, TLS: conf, Store: c.store()}
	if c.Control != "" {
		cl, err := net.Listen("tcp", c.Control)
		if err != nil {
			return fmt.Errorf("控制面监听 %s 失败: %w", c.Control, err)
		}
		srv := &http.Server{Handler: r.ControlHandler(), ReadHeaderTimeout: 10 * time.Second}
		go func() {
			if err := srv.Serve(cl); err != nil {
				log.Printf("控制面挂了: %v", err)
			}
		}()
	}
	hs, err := c.store().Hosts()
	if err != nil {
		return fmt.Errorf("读主机表失败（%s）: %w", c.Dir, err)
	}
	log.Printf("herdr-relay %s 在 %s 上跑起来了：控制面 https://%s/，主机名 *.%s，已注册 %d 台",
		version.Version, c.Listen, c.Domain, c.Zone, len(hs))
	return r.Serve(ln)
}

func inviteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "invite <主机名>",
		Short: "给一台主机出一个一次性邀请码（" + relay.InviteTTL.String() + " 内有效）",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c := load()
			host, err := relay.CleanHost(args[0])
			if err != nil {
				return err
			}
			if c.Zone != "" && host != c.Zone && !strings.HasSuffix(host, "."+c.Zone) {
				return fmt.Errorf("%s 不在 HERDR_RELAY_ZONE（%s）下面", host, c.Zone)
			}
			if host == c.Domain {
				return fmt.Errorf("%s 是中继自己的名字，不能注册成主机", host)
			}
			code, err := c.store().NewInvite(host)
			if err != nil {
				return err
			}
			domain := c.Domain
			if domain == "" {
				domain = "<HERDR_RELAY_DOMAIN>"
			}
			fmt.Printf("\n  邀请码（%s 内有效，用一次就废）：%s\n\n", relay.InviteTTL, code)
			fmt.Printf("  在那台主机上敲：\n\n    herdr-web relay join https://%s %s %s\n\n", domain, host, code)
			return nil
		},
	}
}

func hostsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "hosts",
		Short: "列出注册过的主机",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			c := load()
			hs, err := c.store().Hosts()
			if err != nil {
				return err
			}
			if len(hs) == 0 {
				fmt.Println("  还没有主机注册过。")
				return nil
			}
			for _, h := range hs {
				fmt.Printf("  %-40s 注册于 %s\n", h.Name, h.Created.Local().Format(time.DateTime))
			}
			return nil
		},
	}
}

func removeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <主机名>",
		Short: "删掉一台主机（跑着的中继会在 30 秒内掐掉它的隧道）",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c := load()
			host, err := relay.CleanHost(args[0])
			if err != nil {
				return err
			}
			ok, err := c.store().Remove(host)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("没有 %s 这台主机", host)
			}
			fmt.Printf("  删掉了 %s。\n", host)
			return nil
		},
	}
}
