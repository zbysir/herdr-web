// Package version 是「这个二进制是哪一版」的唯一出处。
//
// 值由 goreleaser 在链接时用 -ldflags -X 注入（见 .goreleaser.yaml）。自己
// `go build` 出来的二进制没人注入，就是 "dev" —— 这个默认值是刻意的：查更新那边
// 见到 dev 会直接跳过，免得开发机上天天弹「有新版本」。
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
)

// 这三个由 -ldflags -X 注入，别在代码里赋值。
var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

// Dev 表示这是一个没被注入版本号的本地构建。查更新、提示升级都要看这个。
func Dev() bool { return Version == "dev" || Version == "" }

// Semver 返回不带前导 v 的版本号，比较版本号时用它。
func Semver() string { return strings.TrimPrefix(Version, "v") }

// String 是给人看的一行，`herdr-web version` 和启动横幅共用。
// releaseTag 判断 build info 里那个版本号是不是一个真的发布 tag。
// 排掉 "(devel)"、带 +incompatible / +dirty 的、以及伪版本（中间有 14 位时间戳）。
func releaseTag(v string) bool {
	if !strings.HasPrefix(v, "v") || strings.Contains(v, "+") {
		return false
	}
	main, pre, _ := strings.Cut(strings.TrimPrefix(v, "v"), "-")
	seg := strings.Split(main, ".")
	if len(seg) != 3 {
		return false
	}
	for _, s := range seg {
		if s == "" || strings.Trim(s, "0123456789") != "" {
			return false
		}
	}
	// 伪版本的 pre-release 段形如 20260821095702-ca3cc05636d8
	for _, part := range strings.Split(pre, "-") {
		if len(part) == 14 && strings.Trim(part, "0123456789") == "" {
			return false
		}
	}
	return true
}

func String() string {
	s := "herdr-web " + Version
	if Commit != "" {
		c := Commit
		if len(c) > 7 {
			c = c[:7]
		}
		s += " (" + c
		if Date != "" {
			s += " " + Date
		}
		s += ")"
	}
	return s + fmt.Sprintf("  %s/%s  %s", runtime.GOOS, runtime.GOARCH, runtime.Version())
}

// FromBuildInfo 在没有 ldflags 的情况下兜一把：`go install pkg@v1.2.3` 装出来的
// 二进制，Go 会把模块版本写进 build info。
//
// **只认真正的 tag 版本**。Go 1.24 起，在 git 仓库里 `go build` 出来的二进制，
// build info 里是个伪版本（v0.0.0-20260821095702-ca3cc05636d8+dirty）。收下它的话
// Dev() 就永远是 false，本地构建会开始查更新、并且因为 0.0.0 < 任何发布版而一直
// 提示「有新版本」—— 那是个不容易联想到这里的骚扰。
func FromBuildInfo() {
	if !Dev() {
		return
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok || !releaseTag(bi.Main.Version) {
		return
	}
	Version = bi.Main.Version
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			Commit = s.Value
		case "vcs.time":
			Date = s.Value
		}
	}
}
