package acme

import (
	"os"
	"strings"
)

// 云凭据的环境变量也带 HERDR_WEB_ 前缀，进 lego 之前再脱掉。
//
// 为什么要这一层：lego 的每个 provider 都直接读固定名字（`CLOUDFLARE_DNS_API_TOKEN`
// 这种），而本项目别的配置一律 `HERDR_WEB_*`。两套命名混着用有一个**静默**后果：
// `service install` 抄进 plist / unit 的是「所有 HERDR_WEB_* + 一张短白名单」，光秃秃的
// 云凭据两头都不占 —— 你在 `.zshrc` 里 export 得再对，装出来的服务照样签不出证书，
// 而且要等到第一次签发（或者三个月后第一次续期）才炸。
//
// 所以对外统一成 `HERDR_WEB_<厂商变量>`。老写法（光秃秃的名字）仍然能用 —— lego 自己会读，
// 我们什么都不做；两个都给的话**带前缀的赢**，显式配的那个应该盖过环境里捡来的。
const envPrefix = "HERDR_WEB_"

// dnsNamespaces 是每家 provider 的环境变量前缀（对应 lego 里的 envNamespace）。
//
// 记前缀而不是列全名：除了凭据，每家还有一串可选项（`_TTL` / `_PROPAGATION_TIMEOUT` /
// `_FILE` 后缀那种从文件读的写法）。列全名一定会漏，而漏掉的表现是「我明明配了这个变量
// 却没生效」—— 比报错难查得多。
//
// 加一家 provider 是三处：这张表 + envHint + newDNS 的 case。少了这张表的表现是
// 「带前缀的凭据被当成没配」。
var dnsNamespaces = map[string][]string{
	"cloudflare":   {"CLOUDFLARE_", "CF_"}, // CF_ 是 lego 认的别名
	"alidns":       {"ALICLOUD_"},
	"tencentcloud": {"TENCENTCLOUD_"},
	"route53":      {"AWS_"},
	"digitalocean": {"DO_"},
	"huaweicloud":  {"HUAWEICLOUD_"},
}

// exportEnv 把 HERDR_WEB_<厂商变量> 脱掉前缀写回进程环境，好让 lego 读到。
//
// 只翻译**当前这一家**的：`AWS_` 这种命名空间很宽（AWS SDK 自己也认一堆），跟着别家
// 一起导出去只会换来「我用的是 cloudflare，为什么 AWS 的东西也生效了」这种查不清的事。
//
// 导出来的名字不会漏进网页里的终端：PTY 的子进程环境按前缀清掉了这一批
// （`internal/server/pty.go` 的 dropEnv，`HERDR_` 和各家命名空间都在里面）。
func exportEnv(name string) {
	ns := dnsNamespaces[name]
	if len(ns) == 0 {
		return
	}
	for _, kv := range os.Environ() {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasPrefix(k, envPrefix) {
			continue
		}
		bare := strings.TrimPrefix(k, envPrefix)
		for _, p := range ns {
			if strings.HasPrefix(bare, p) {
				_ = os.Setenv(bare, v)
				break
			}
		}
	}
}

// SecretEnv 判一个环境变量名是不是云凭据。给「别把它明文打到终端上」用
// （`service install` 会把抄进 unit 的每一项都打出来，而那段输出往往就落在
// 一个跑着 agent 的 pane 里）。
//
// 判据是「在某家的命名空间里」+「名字里有 TOKEN / SECRET / KEY / PASSWORD」。
// 不整个命名空间一起盖是因为 `AWS_REGION` / `*_TTL` 这些恰恰是装的时候要看一眼的。
func SecretEnv(key string) bool {
	bare := strings.TrimPrefix(key, envPrefix)
	var known bool
	for _, ns := range dnsNamespaces {
		for _, p := range ns {
			if strings.HasPrefix(bare, p) {
				known = true
			}
		}
	}
	if !known {
		return false
	}
	for _, s := range []string{"TOKEN", "SECRET", "KEY", "PASSWORD"} {
		if strings.Contains(bare, s) {
			return true
		}
	}
	return false
}
