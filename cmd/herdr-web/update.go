package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/zbysir/herdr-web/internal/config"
	"github.com/zbysir/herdr-web/internal/runlock"
	"github.com/zbysir/herdr-web/internal/selfupdate"
	"github.com/zbysir/herdr-web/internal/service"
	"github.com/zbysir/herdr-web/internal/version"
)

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "打印版本号",
		Args:  cobra.NoArgs,
		Run: func(*cobra.Command, []string) {
			fmt.Println(version.String())
			if inst, err := selfupdate.Detect(); err == nil {
				fmt.Printf("  装在  %s（%s）\n", inst.Path, methodName(inst.Method))
			}
		},
	}
}

func methodName(m selfupdate.Method) string {
	switch m {
	case selfupdate.MethodNPM:
		return "npm"
	case selfupdate.MethodBrew:
		return "homebrew"
	case selfupdate.MethodGoInstall:
		return "go install"
	}
	return "release archive"
}

func updateCmd() *cobra.Command {
	var checkOnly, force, restart bool
	c := &cobra.Command{
		Use:   "update",
		Short: "查有没有新版本，有就装上",
		Long: "查 GitHub Releases，有新版本就装上。\n\n" +
			"怎么装取决于当初怎么装的：npm / homebrew / go install 装的会调对应的包管理器，\n" +
			"从 release archive 装的由本程序自己下载、校验 sha256、原地换掉二进制。\n\n" +
			"**换完要重启才生效**，而重启会掐掉所有正在用的终端会话 —— 所以这一步不自动做，\n" +
			"想让它顺手重启加 --restart。",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUpdate(cmd.Context(), checkOnly, force, restart)
		},
	}
	c.Flags().BoolVar(&checkOnly, "check", false, "只查，不装")
	c.Flags().BoolVar(&force, "force", false, "不比版本号，硬装最新版（本地 dev 构建想换成正式版时用）")
	c.Flags().BoolVar(&restart, "restart", false, "装完顺手重启服务（会断开所有终端会话）")
	return c
}

func runUpdate(ctx context.Context, checkOnly, force, restart bool) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("读配置失败: %w", err)
	}
	ck := &selfupdate.Checker{Dir: cfg.Dir, Current: version.Semver()}

	fmt.Printf("  当前  %s\n", version.Version)
	// force=true：手敲这条命令就是想现在知道，不该被一天一次的节流挡住
	st, err := ck.Check(ctx, true)
	if err != nil {
		return fmt.Errorf("查更新失败: %w", err)
	}
	if st.Latest == "" {
		fmt.Println("  查不到已发布的版本")
		return nil
	}
	fmt.Printf("  最新  %s   %s\n", st.Latest, st.URL)

	newer := selfupdate.Newer(version.Semver(), st.Latest)
	switch {
	case newer:
		fmt.Println()
	case version.Dev():
		fmt.Println("\n  这是本地构建（没有版本号），没法跟发布版比。")
		if !force {
			fmt.Println("  要换成发布版：herdr-web update --force")
			return nil
		}
	case force:
		fmt.Println("\n  已经是最新，但 --force 了，继续。")
	default:
		fmt.Println("\n  已经是最新。")
		return nil
	}
	if checkOnly {
		return nil
	}

	inst, err := selfupdate.Detect()
	if err != nil {
		return err
	}
	// 包管理器装的：让包管理器去换。自己去动 node_modules / Cellar 里的文件，
	// 下次那个包管理器一升级就把我们的改动盖回去了。
	if cmdline := inst.Command(); cmdline != "" {
		fmt.Printf("  这是用 %s 装的，交给它来换：\n    %s\n\n", methodName(inst.Method), cmdline)
		parts := strings.Fields(cmdline)
		c := exec.CommandContext(ctx, parts[0], parts[1:]...)
		c.Stdout, c.Stderr, c.Stdin = os.Stdout, os.Stderr, os.Stdin
		if err := c.Run(); err != nil {
			return fmt.Errorf("%s 失败: %w\n\n  权限不够的话前面加 sudo 再跑一次那条命令。", parts[0], err)
		}
	} else {
		rel := selfupdate.Release{Version: st.Latest, Tag: st.Tag, URL: st.URL}
		if rel.Tag == "" {
			rel.Tag = "v" + st.Latest
		}
		path, err := selfupdate.Apply(ctx, nil, inst.Path, rel, func(s string) {
			fmt.Println("  " + s)
		})
		if err != nil {
			return err
		}
		fmt.Printf("  ✓ 换好了：%s\n", path)
	}

	// 重启这件事必须说清楚：换了文件不等于换了正在跑的那个进程
	svc := service.New(inst.Path, cfg.Dir, nil)
	running := runlock.InUse(cfg.DataDir)
	fmt.Println()
	if restart {
		if !svc.Supported() {
			return fmt.Errorf("这台机器没法自动重启服务：%s", svc.Why)
		}
		if err := svc.Restart(); err != nil {
			return fmt.Errorf("重启失败: %w", err)
		}
		fmt.Println("  ✓ 服务已重启，新版本生效了")
		return nil
	}
	if st, err := svc.Status(); err == nil && st.Installed {
		fmt.Println("  装成服务了，重启才生效：herdr-web service restart")
		fmt.Println("  （会掐掉所有正在用的终端会话，挑个时候）")
	} else if running {
		fmt.Println("  有一个 herdr-web 在跑，重启它才生效。")
	}
	return nil
}

// updateNotice 是启动横幅和运行期日志共用的那句提示。返回空串表示没有新版本。
func updateNotice(st selfupdate.State) string {
	if !selfupdate.Newer(version.Semver(), st.Latest) {
		return ""
	}
	how := "herdr-web update"
	if inst, err := selfupdate.Detect(); err == nil {
		if c := inst.Command(); c != "" {
			how = c
		}
	}
	return fmt.Sprintf("有新版本 %s（当前 %s）。升级：%s", st.Latest, version.Version, how)
}

// startUpdateWatch 在后台盯新版本。查更新绝不能影响启动，所以整个过程都在 goroutine 里，
// 出错只写日志。返回的 Checker 给管理页读缓存用。
func startUpdateWatch(cfg *config.Config) *selfupdate.Checker {
	ck := &selfupdate.Checker{Dir: cfg.Dir, Current: version.Semver()}
	// 本地构建不查：开发机上每天弹一条「有新版本」纯属噪音
	if !cfg.UpdateCheck || version.Dev() {
		return ck
	}
	go func() {
		// 等一会儿再查：启动那几秒该留给「把服务跑起来」，别和 ACME 抢网络
		select {
		case <-time.After(20 * time.Second):
		}
		ck.Watch(context.Background(), func(st selfupdate.State) {
			fmt.Printf("\n  ⬆️  %s\n\n", updateNotice(st))
		})
	}()
	return ck
}

// osArch 给管理页显示，顺带说明为什么 Windows 上没有包。
func osArch() string { return runtime.GOOS + "/" + runtime.GOARCH }
