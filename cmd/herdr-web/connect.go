package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/zbysir/herdr-web/internal/remote"
)

func connectCmd() *cobra.Command {
	var logout bool
	c := &cobra.Command{
		Use:   "connect <地址>",
		Short: "在这个终端里连另一台机器上的 herdr-web（第一次要配对码）",
		Long: "在本地终端里用另一台机器上的 herdr-web，不用开浏览器。\n\n" +
			"  herdr-web connect https://herdr.example.com        默认 session\n" +
			"  herdr-web connect https://herdr.example.com/work   herdr --session work\n\n" +
			"第一次连要一个配对码（在那台机器上跑 `herdr-web pair`）。配出来的是一台普通设备，\n" +
			"网页的设备页里看得见、撤销得掉；凭据存在 ~/.herdr-web/remotes.json（0600）。\n" +
			"网络僵住时在行首按 `~.` 断开（同 ssh）。",
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			t, err := remote.Parse(args[0])
			if err != nil {
				return err
			}
			if logout {
				if err := remote.Logout(t); err != nil {
					return err
				}
				fmt.Printf("  ✓ 已撤销这台设备，本地凭据也删了\n")
				return nil
			}
			code, err := remote.Run(t)
			if err != nil {
				return err
			}
			os.Exit(code)
			return nil
		},
	}
	c.Flags().BoolVar(&logout, "logout", false, "撤销这台设备在那边的凭据并删掉本地那份")
	return c
}
