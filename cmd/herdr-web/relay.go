package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/zbysir/dialhome/relay"

	"github.com/zbysir/herdr-web/internal/acme"
	"github.com/zbysir/herdr-web/internal/config"
)

// relayCmd：`herdr-web relay join` —— 拿中继那边出的邀请码，把这台机器注册上去，然后直接装成
// 常驻服务（带上 HERDR_WEB_RELAY_URL / _HOST 两项）。
//
// 原来只做注册、再让人自己带着那两项重跑一次 service install。两步之间没有任何需要人决定的
// 事，拆开只多一个抄错变量的机会。配置照旧只走环境变量：join 就是替你在 service install
// 抄的那份环境里加上这两项，不另存任何东西。
//
// **先核配置、再用邀请码**：连中继要求 TLS 在本进程终止（得配 ACME 或证书），缺了这项
// 服务起不来。邀请码一次性、30 分钟过期，用掉了才发现配置不对就得回 VPS 再出一个。
func relayCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "relay",
		Short: "云端中继（herdr-relay）：主机主动连出去，手机只认一个云端地址",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "join <中继地址> <主机名> <邀请码>",
		Short: "用邀请码把这台机器注册到中继上，并装成常驻服务（邀请码在 VPS 上用 herdr-relay invite 出）",
		Long: "注册到中继，然后像 service install 一样把当前 shell 的配置（加上中继那两项）写进\n" +
			"launchd / systemd 并起来。所以证书相关的配置要在同一条命令里带上：\n\n" +
			"  HERDR_WEB_ACME_DNS=cloudflare HERDR_WEB_CLOUDFLARE_DNS_API_TOKEN=… \\\n" +
			"    herdr-web relay join https://relay.example.com mac.h.example.com <邀请码>",
		Example: "  HERDR_WEB_ACME_DNS=cloudflare HERDR_WEB_CLOUDFLARE_DNS_API_TOKEN=… herdr-web relay join https://relay.example.com mac.h.example.com <邀请码>",
		Args:    cobra.ExactArgs(3),
		RunE: func(_ *cobra.Command, args []string) error {
			host, err := relay.CleanHost(args[1])
			if err != nil {
				return err
			}
			url := strings.TrimRight(args[0], "/")
			// 按「连上中继之后」的样子核一遍配置，缺证书配置在这儿就停，邀请码还没用掉
			os.Setenv("HERDR_WEB_RELAY_URL", url)
			os.Setenv("HERDR_WEB_RELAY_HOST", host)
			cfg, err := config.Load()
			if err == nil && cfg.ACMEDNS == "" && cfg.TLSCert == "" {
				// config 那道只要求「本进程终止 TLS」，自签也算过；但从中继进来的手机不认自签，
				// App 更是直接拒，所以这里要的是一张真证书
				err = fmt.Errorf("从公网连要一张 %s 的真证书：带上 HERDR_WEB_ACME_DNS=<DNS 服务商> 和它的 token"+
					"（比如 HERDR_WEB_ACME_DNS=cloudflare HERDR_WEB_CLOUDFLARE_DNS_API_TOKEN=…），"+
					"或者 HERDR_WEB_TLS_CERT / HERDR_WEB_TLS_KEY", host)
			}
			if err != nil {
				return fmt.Errorf("%w\n\n  还没注册，邀请码没用掉：把配置补上再敲一次", err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := relay.Join(ctx, relay.HostConfig{URL: url, Host: host, Dir: cfg.RelayDir()}, args[2]); err != nil {
				return err
			}
			fmt.Printf("\n  ✓ 注册好了：%s → %s\n", host, url)

			extra := map[string]string{"HERDR_WEB_RELAY_URL": url, "HERDR_WEB_RELAY_HOST": host}
			if m, _, err := mgr(nil); err != nil || !m.Supported() {
				// 装不了服务的平台：退回原来的说法，让人自己把这两项加进启动环境
				fmt.Printf("\n  这台机器装不了常驻服务，启动 herdr-web 时带上这两项：\n\n"+
					"       HERDR_WEB_RELAY_URL=%s\n       HERDR_WEB_RELAY_HOST=%s\n", url, host)
			} else if err := installService(extra, true); err != nil {
				return fmt.Errorf("注册好了，但装服务失败（带上上面两项重跑 herdr-web service install）：%w", err)
			}
			printRelayDNS(cfg, host)
			return nil
		},
	})
	return cmd
}

func printRelayDNS(cfg *config.Config, host string) {
	zone := host
	if i := strings.IndexByte(host, '.'); i > 0 {
		zone = host[i+1:]
	}
	acct := acme.AccountURI(cfg.DataDir)
	if acct == "" {
		acct = "<这台机器的 ACME 账户；服务签完第一张证书后再跑一次 join 就会印出来>"
	}
	fmt.Printf(`
  最后去 DNS 上加几条（一次性）：

       %s     A    <中继 VPS 的 IP>
       %s          CAA  0 issue "letsencrypt.org; accounturi=%s; validationmethods=dns-01"
       %s          CAA  0 issuewild ";"

     A 记录：手机连的是这个名字，中继按名字转给这台机器。以后要接好几台机器，可以换成
     一条 *.%s 通配记录，加机器就不用再动 DNS。
     CAA 那两条挡的是「中继占着这个 IP 去自己签一张你的证书」（docs/dev/RELAY.md §1.1）。
     **挂在 %s 上，别挂在 %s 上**：用通配 A 记录时，一个名字上只要有任何记录，通配就不再
     替它生效，机器当场连不上。另一台机器的 accounturi 照样再加一条 issue。

`, host, zone, acct, zone, zone, zone, host)
}
