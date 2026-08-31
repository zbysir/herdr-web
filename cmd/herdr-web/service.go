package main

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/zbysir/herdr-web/internal/acme"
	"github.com/zbysir/herdr-web/internal/config"
	"github.com/zbysir/herdr-web/internal/selfupdate"
	"github.com/zbysir/herdr-web/internal/service"
)

func serviceCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "service",
		Short: "装成常驻服务（开机自启）",
		Long: "把 herdr-web 装成 user 级常驻服务：macOS 用 launchd，Linux 用 systemd。\n\n" +
			"配置是**装的那一刻**从当前 shell 里抄进 plist / unit 的（所有 HERDR_WEB_* 加上\n" +
			"PATH / SHELL / LANG 这几个）。DNS 凭据也带 HERDR_WEB_ 前缀，所以一起抄进去。\n" +
			"所以先把环境配对，再 install。装完之后改配置也是同一条路 —— 没有「改一项」的\n" +
			"子命令，换个环境重新 install 就行（幂等，覆盖 + 重启）：\n\n" +
			"  HERDR_WEB_PUBLIC_PORT=9000 herdr-web service install   # 改一项，其余照抄当前 shell\n" +
			"  herdr-web service install --env-file .env              # 或者让一个文件当唯一出处\n\n" +
			"注意它是**整份重来**：这个 shell 里没有的变量，上次装进去的那份也会跟着没了。",
	}
	root.AddCommand(serviceInstallCmd(), serviceUninstallCmd(), serviceStatusCmd(),
		serviceRestartCmd(), serviceLogsCmd())
	return root
}

// mgr 建一个 Manager：二进制路径取当前可执行文件（解过 symlink），配置目录取 cfg.Dir。
func mgr(env map[string]string) (*service.Manager, *config.Config, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, fmt.Errorf("读配置失败: %w", err)
	}
	inst, err := selfupdate.Detect()
	if err != nil {
		return nil, nil, err
	}
	return service.New(inst.Path, cfg.Dir, env), cfg, nil
}

func serviceInstallCmd() *cobra.Command {
	var envFile string
	c := &cobra.Command{
		Use:   "install",
		Short: "装上并起来（已经装过就是覆盖 + 重启）",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			var extra map[string]string
			if envFile != "" {
				var err error
				if extra, err = service.ParseEnvFile(envFile); err != nil {
					return err
				}
			}
			env := service.EnvSnapshot(extra)
			m, cfg, err := mgr(env)
			if err != nil {
				return err
			}
			if !m.Supported() {
				return fmt.Errorf("%s", m.Why)
			}

			// 装之前把要写进去的配置全打出来。这一步是刻意的：这台机器上「服务到底
			// 在用哪套配置」以后只能靠 plist / unit 回答，装的时候看一眼最省事。
			fmt.Println()
			fmt.Printf("  二进制  %s\n", m.Exec)
			fmt.Printf("  方式    %s\n", m.Kind)
			fmt.Printf("  文件    %s\n", m.UnitPath())
			fmt.Printf("  日志    %s\n", m.LogPath())
			fmt.Println("  抄进去的环境变量：")
			keys := make([]string, 0, len(env))
			for k := range env {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				v := env[k]
				// PATH 太长，横幅上没意义，只说抄了
				if k == "PATH" {
					fmt.Printf("    %-24s（%d 个目录，照抄当前 shell）\n", k, 1+countColon(v))
					continue
				}
				// 云凭据和引导 token 不打明文：这段输出常常就落在一个跑着 agent 的
				// pane 里，`service install` 一敲等于把云账号密钥念给它听。
				// 但也不能只字不提 —— 「到底抄没抄进去」是这条路最容易错的地方，
				// 所以照样列出名字，只把值换成长度。
				if acme.SecretEnv(k) || k == "HERDR_WEB_TOKEN" {
					fmt.Printf("    %-24s ****（%d 字符，没打出来）\n", k, len(v))
					continue
				}
				fmt.Printf("    %-24s %s\n", k, v)
			}
			fmt.Println()

			if err := m.Install(func(s string) { fmt.Println("  " + s) }); err != nil {
				return err
			}
			fmt.Println()
			fmt.Printf("  ✓ 装好了。看状态：herdr-web service status\n")
			fmt.Printf("    跟日志：herdr-web service logs\n")
			fmt.Printf("    配对新设备：herdr-web pair\n")
			if m.Kind == service.Launchd {
				fmt.Println()
				fmt.Println("  注意：macOS 的 LaunchAgent 是**登录时**起，不是开机时起。")
				fmt.Println("  这台机器开了自动登录的话二者等价；没开的话得登录一次它才起来。")
			}
			_ = cfg
			return nil
		},
	}
	c.Flags().StringVar(&envFile, "env-file", "", "从这个文件读配置（.env 那种 KEY=VALUE），优先级高于当前环境")
	return c
}

func countColon(s string) int {
	n := 0
	for _, r := range s {
		if r == ':' {
			n++
		}
	}
	return n
}

func serviceUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "停掉并卸掉（不动数据和日志）",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			m, _, err := mgr(nil)
			if err != nil {
				return err
			}
			if err := m.Uninstall(func(s string) { fmt.Println("  " + s) }); err != nil {
				return err
			}
			fmt.Println("  设备凭据和日志都留着（在 ~/.herdr-web/），要清自己删。")
			return nil
		},
	}
}

func serviceStatusCmd() *cobra.Command {
	var verbose bool
	c := &cobra.Command{
		Use:   "status",
		Short: "装没装、跑没跑",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			m, _, err := mgr(nil)
			if err != nil {
				return err
			}
			st, err := m.Status()
			if err != nil {
				return err
			}
			fmt.Println()
			fmt.Printf("  方式    %s\n", m.Kind)
			fmt.Printf("  文件    %s", m.UnitPath())
			if !st.Installed {
				fmt.Print("   ← 不存在（没装）")
			}
			fmt.Println()
			if st.Running {
				fmt.Printf("  状态    在跑（PID %d）\n", st.PID)
			} else if st.Installed {
				fmt.Println("  状态    装了但没在跑 —— 起不来的原因看日志")
			} else {
				fmt.Println("  状态    没装（herdr-web service install）")
			}
			fmt.Printf("  日志    %s\n", m.LogPath())
			if verbose && st.Detail != "" {
				fmt.Println()
				fmt.Println(st.Detail)
			}
			fmt.Println()
			return nil
		},
	}
	c.Flags().BoolVarP(&verbose, "verbose", "v", false, "把 launchctl / systemctl 的原始输出也打出来")
	return c
}

func serviceRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "重启服务（换过二进制之后要这一步；会断开所有终端会话）",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			m, _, err := mgr(nil)
			if err != nil {
				return err
			}
			if err := m.Restart(); err != nil {
				return err
			}
			fmt.Println("  ✓ 重启了")
			return nil
		},
	}
}

func serviceLogsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logs",
		Short: "跟日志（tail -f）",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			m, _, err := mgr(nil)
			if err != nil {
				return err
			}
			argv := m.LogsCmd()
			bin, err := exec.LookPath(argv[0])
			if err != nil {
				return err
			}
			// 直接 exec 换掉自己：这样 Ctrl-C 打在 tail 上，行为和手敲 tail -f 一模一样，
			// 不用自己去处理信号转发和缓冲
			return syscall.Exec(bin, argv, os.Environ())
		},
	}
}
