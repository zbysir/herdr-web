package auth

import (
	"context"
	"net/http"
)

// 「这个请求是从哪个口进来的」——认证的几个豁免全都压在这上面，所以单独一个文件说清楚。
//
// 判据只能是**落在哪个监听上**，绝不能看源 IP 或 Host：穿透进来的请求源地址可以是
// 127.0.0.1，中继隧道里的源地址是中继说的，Host 又是客户端自己说的。看错一处，
// 等于那道门不存在。
//
// 「公网」现在只有一条路：云端中继的隧道（docs/dev/RELAY.md）。名字还叫 PublicPort 是历史：
// 以前有一个单独的公网口（HERDR_WEB_PUBLIC_PORT），2026-10-23 下线。

type portKey struct{}

// MarkPublicPort 盖一个「从公网（中继隧道）进来的」的章。只有 server.PublicListener 会调它。
func MarkPublicPort(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), portKey{}, true))
}

// FromPublicPort 这个请求是不是落在公网口上。
//
// 公网那条路上一律不认「因为你在本机所以放你进来」这类豁免（见 Store.trustLoopback）：
// 那些豁免的前提是「源地址是 127.0.0.1 就说明人在机器前」，而这条路存在的意义恰恰是
// 「公网能碰到它」。
func FromPublicPort(r *http.Request) bool {
	v, _ := r.Context().Value(portKey{}).(bool)
	return v
}

// FromThisMachine 这个请求是不是**这台机器自己**发的（不是穿透进来的）。只拿来给人看的
// 警告用（命令行登录的批准页：「这个请求是从跑 herdr-web 的这台机器发起的」），不拿来放行。
// 判据和 trustLoopback 那条同一套：不在公网口上、源地址是 loopback、没带代理头。
func FromThisMachine(r *http.Request) bool {
	return !FromPublicPort(r) && remoteIsLoopback(r) && !behindProxy(r)
}
