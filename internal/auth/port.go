package auth

import (
	"context"
	"net/http"
)

// 「这个请求是从哪个口进来的」——认证的几个豁免全都压在这上面，所以单独一个文件说清楚。
//
// 判据只能是**落在哪个监听上**，绝不能看源 IP 或 Host：穿透进来的请求源地址就是
// 127.0.0.1（frpc 从本机连过来），Host 又是客户端自己说的。这也是 server.FromLan
// 那段注释里同一条理由 —— 看错一处，等于那道门不存在。

type portKey struct{}

// MarkPublicPort 盖一个「从公网口（HERDR_WEB_PUBLIC_PORT）进来的」的章。
// 只有 server.PublicListener 会调它。
func MarkPublicPort(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), portKey{}, true))
}

// FromPublicPort 这个请求是不是落在公网口上。
//
// 公网口上一律不认「因为你在本机所以放你进来」这类豁免（见 Store.trustLoopback、
// Store.legacyOK）：那些豁免的前提是「源地址是 127.0.0.1 就说明人在机器前」，而这个口
// 存在的意义恰恰是「公网能碰到它」。
func FromPublicPort(r *http.Request) bool {
	v, _ := r.Context().Value(portKey{}).(bool)
	return v
}
