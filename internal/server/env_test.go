package server

import (
	"strings"
	"testing"
)

// PTY 里跑的是登录 shell，而 agent 就在那里面跑 —— 它 `echo $XXX` 就能看到环境变量。
//
// 云厂商的 DNS 凭据是从 .env 进到 herdr-web 环境里的（ACME 要用），如果不清掉，
// **每个终端里的 agent 都能读走你的云账号密钥**。一次 prompt injection 就够了。
//
// 反过来：清掉不会影响你自己 —— PTY 起的是 `-l` 登录 shell，会重新 source 你的
// profile，你在 rc 里 export 的那些照样在。
func TestChildEnvDropsCredentials(t *testing.T) {
	secrets := []string{
		"CLOUDFLARE_DNS_API_TOKEN",
		"CLOUDFLARE_API_KEY",
		"ALICLOUD_ACCESS_KEY",
		"ALICLOUD_SECRET_KEY",
		"TENCENTCLOUD_SECRET_ID",
		"TENCENTCLOUD_SECRET_KEY",
		"DNSPOD_API_KEY",
		"AWS_ACCESS_KEY_ID",
		"AWS_SECRET_ACCESS_KEY",
		"AWS_SESSION_TOKEN",
		"DO_AUTH_TOKEN",
		"HUAWEICLOUD_ACCESS_KEY_ID",
		"HUAWEICLOUD_SECRET_ACCESS_KEY",
		"HERDR_WEB_TOKEN", // 旧的引导 token
		// 带前缀的写法（现在文档里就是这个，见 internal/acme/env.go）。
		// 这几条靠 `HERDR_` 那条规则一起被清掉，列出来是为了以后有人收窄那条规则时
		// 立刻炸掉 —— 不然凭据会悄悄回到 agent 手边。
		"HERDR_WEB_CLOUDFLARE_DNS_API_TOKEN",
		"HERDR_WEB_ALICLOUD_SECRET_KEY",
		"HERDR_WEB_AWS_SECRET_ACCESS_KEY",
		"HERDR_WEB_DO_AUTH_TOKEN",
	}
	for _, k := range secrets {
		t.Setenv(k, "SENTINEL-"+k)
	}
	// 对照组：正常的东西不能被误伤
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("LANG", "en_US.UTF-8")

	env := childEnv()
	joined := strings.Join(env, "\n")

	for _, k := range secrets {
		if strings.Contains(joined, "SENTINEL-"+k) {
			t.Errorf("%s 漏进了 PTY 的子进程环境 —— 那里面的 agent 能直接读走", k)
		}
	}
	if !strings.Contains(joined, "LANG=") {
		t.Error("LANG 被误伤了")
	}
	var hasPath bool
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			hasPath = true
		}
	}
	if !hasPath {
		t.Error("PATH 被误伤了 —— 那 shell 基本没法用")
	}
}
