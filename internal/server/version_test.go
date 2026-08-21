package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zbysir/herdr-web/internal/selfupdate"
)

// /api/state 里 version 那一段的形状是前端依赖的（web/src/lib/api.ts 的 State.version）：
// **只有真的有新版本时才带 latest / outdated / how**。前端靠「有没有这几个字段」决定
// 要不要显示提示，所以「已是最新时也带 outdated:false」和这里不是一回事 —— 改形状要
// 两边一起改。
func TestVersionInfo(t *testing.T) {
	dir := t.TempDir()
	write := func(latest string) *selfupdate.Checker {
		if latest != "" {
			body := `{"checkedAt":"2026-08-21T10:00:00Z","latest":"` + latest + `","tag":"v` + latest + `"}`
			if err := os.WriteFile(filepath.Join(dir, "update.json"), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return &selfupdate.Checker{Dir: dir, Current: "1.0.0"}
	}

	// 没接 checker：只有 current，不能 panic
	s := &Server{Version: "v1.0.0"}
	got := s.versionInfo()
	if got["current"] != "v1.0.0" {
		t.Errorf("current = %v", got["current"])
	}
	if _, ok := got["outdated"]; ok {
		t.Error("没有 checker 时不该有 outdated")
	}

	// 有新版本
	s = &Server{Version: "v1.0.0", Updates: write("9.9.9")}
	got = s.versionInfo()
	if got["outdated"] != true || got["latest"] != "9.9.9" {
		t.Errorf("应当报有新版本: %v", got)
	}
	if got["how"] == "" || got["how"] == nil {
		t.Error("必须给一条能照着敲的命令")
	}

	// 已是最新：不带 latest / outdated，前端就什么都不显示
	s = &Server{Version: "v9.9.9", Updates: write("9.9.9")}
	got = s.versionInfo()
	if _, ok := got["outdated"]; ok {
		t.Errorf("已是最新时不该带 outdated: %v", got)
	}

	// 本地构建（dev）：Newer 一律 false，不骚扰
	s = &Server{Version: "dev", Updates: write("9.9.9")}
	if _, ok := s.versionInfo()["outdated"]; ok {
		t.Error("dev 构建不该提示升级")
	}
}
