// Package webui 把前端产物嵌进二进制。
//
// dist/ 由 `make build` 从 web/dist 拷过来（Vite 的输出）。仓库里只留一个占位，
// 这样没跑过 npm build 也能 go build —— 那种情况下启动会提示前端产物缺失。
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var embedded embed.FS

// FS 返回前端产物；没真正构建过则返回 nil。
func FS() fs.FS {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		return nil
	}
	if _, err := sub.Open("index.html"); err != nil {
		return nil
	}
	return sub
}
