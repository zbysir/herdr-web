package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/zbysir/herdr-web/internal/acme"
	"github.com/zbysir/herdr-web/internal/config"
	"github.com/zbysir/herdr-web/internal/relay"
)

// relayCmd：`herdr-web relay join` —— 拿中继那边出的邀请码，把这台机器注册上去。
//
// 只做注册这一下。之后「一直连着」是服务本身的事（HERDR_WEB_RELAY_URL / _HOST 两个环境变量），
// 不是这个命令常驻：配置只走环境变量是这个项目的规矩，而且 service install 抄进 plist 的
// 也只有环境变量。
func relayCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "relay",
		Short: "云端中继（herdr-relay）：主机主动连出去，手机只认一个云端地址",
	}
	cmd.AddCommand(&cobra.Command{
		Use:     "join <中继地址> <主机名> <邀请码>",
		Short:   "用邀请码把这台机器注册到中继上（邀请码在 VPS 上用 herdr-relay invite 出）",
		Example: "  herdr-web relay join https://relay.example.com mac.h.example.com <邀请码>",
		Args:    cobra.ExactArgs(3),
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			host, err := relay.CleanHost(args[1])
			if err != nil {
				return err
			}
			url := strings.TrimRight(args[0], "/")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := relay.Join(ctx, relay.HostConfig{URL: url, Host: host, Dir: cfg.RelayDir()}, args[2]); err != nil {
				return err
			}
			printRelayNext(cfg, url, host)
			return nil
		},
	})
	return cmd
}

func printRelayNext(cfg *config.Config, url, host string) {
	zone := host
	if i := strings.IndexByte(host, '.'); i > 0 {
		zone = host[i+1:]
	}
	acct := acme.AccountURI(cfg.DataDir)
	if acct == "" {
		acct = "<这台机器的 ACME 账户；签过一次证书后再跑一次 join 就会印出来>"
	}
	fmt.Printf(`
  ✓ 注册好了：%s → %s

  1. 往 herdr-web 的环境变量里加两行（装成服务的话改 plist / unit 再重启）：

       HERDR_WEB_RELAY_URL=%s
       HERDR_WEB_RELAY_HOST=%s

  2. 还要一张 %s 的真证书 —— 中继不碰证书，TLS 在这台机器上终止：

       HERDR_WEB_ACME_DNS=cloudflare HERDR_WEB_CLOUDFLARE_DNS_API_TOKEN=…

  3. DNS（一次性，以后加机器不用再动）：

       *.%s        A    <中继 VPS 的 IP>
       %s          CAA  0 issue "letsencrypt.org; accounturi=%s; validationmethods=dns-01"
       %s          CAA  0 issuewild ";"

     CAA 那两条挡的是「中继占着这个 IP 去自己签一张你的证书」（docs/dev/RELAY.md §1.1）。
     **挂在 %s 上，别挂在 %s 上**：一个名字上只要有任何记录，通配 A 记录就不再替它生效，
     机器当场连不上。另一台机器的 accounturi 照样再加一条 issue。

`, host, url, url, host, host, zone, zone, acct, zone, zone, host)
}
