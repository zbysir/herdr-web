package acme

import (
	"os"
	"testing"
)

// 前缀这条路是**为了 service install 才存在的**：抄进 plist / unit 的只有
// HERDR_WEB_*，光秃秃的云凭据抄不进去，而那个失败要等到第一次签发才现形。
// 所以「带前缀的能被 lego 读到」这条必须有测试盯着。
func TestExportEnvStripsPrefix(t *testing.T) {
	// 先用 t.Setenv 占个位：exportEnv 走的是 os.Setenv，测试结束不会自己还原，
	// 占位之后由 t.Setenv 的 cleanup 负责收拾，免得脏到同包别的测试。
	t.Setenv("CLOUDFLARE_DNS_API_TOKEN", "")
	t.Setenv("HERDR_WEB_CLOUDFLARE_DNS_API_TOKEN", "tok-prefixed")

	if _, err := newDNS("cloudflare"); err != nil {
		t.Fatalf("带前缀的 token 该够用了，却没构造出来：%v", err)
	}
	if got := os.Getenv("CLOUDFLARE_DNS_API_TOKEN"); got != "tok-prefixed" {
		t.Errorf("前缀没脱掉写回去：%q", got)
	}
}

// 两个都给的时候带前缀的赢：显式配的那个应该盖过环境里捡来的。
func TestExportEnvPrefixWins(t *testing.T) {
	t.Setenv("CLOUDFLARE_DNS_API_TOKEN", "tok-bare")
	t.Setenv("HERDR_WEB_CLOUDFLARE_DNS_API_TOKEN", "tok-prefixed")

	if _, err := newDNS("cloudflare"); err != nil {
		t.Fatalf("newDNS: %v", err)
	}
	if got := os.Getenv("CLOUDFLARE_DNS_API_TOKEN"); got != "tok-prefixed" {
		t.Errorf("带前缀的该赢，得到 %q", got)
	}
}

// 老写法不能坏：只给光秃秃的名字时我们什么都不做，lego 自己读得到。
func TestExportEnvKeepsBareOnly(t *testing.T) {
	t.Setenv("CLOUDFLARE_DNS_API_TOKEN", "tok-bare")
	os.Unsetenv("HERDR_WEB_CLOUDFLARE_DNS_API_TOKEN")

	if _, err := newDNS("cloudflare"); err != nil {
		t.Fatalf("老写法必须还能用：%v", err)
	}
	if got := os.Getenv("CLOUDFLARE_DNS_API_TOKEN"); got != "tok-bare" {
		t.Errorf("没配带前缀的时候不该动它：%q", got)
	}
}

// 凭据之外的可选项（TTL / 超时 / _FILE 从文件读那种）也得跟着过去 ——
// 按前缀翻译而不是列全名就是为了这个，漏掉的表现是「我明明配了却没生效」。
func TestExportEnvCarriesOptionalVars(t *testing.T) {
	t.Setenv("CLOUDFLARE_DNS_API_TOKEN", "")
	t.Setenv("CLOUDFLARE_PROPAGATION_TIMEOUT", "")
	t.Setenv("CF_DNS_API_TOKEN", "")
	t.Setenv("HERDR_WEB_CLOUDFLARE_DNS_API_TOKEN", "tok")
	t.Setenv("HERDR_WEB_CLOUDFLARE_PROPAGATION_TIMEOUT", "300")
	t.Setenv("HERDR_WEB_CF_DNS_API_TOKEN", "tok-alias")

	if _, err := newDNS("cloudflare"); err != nil {
		t.Fatalf("newDNS: %v", err)
	}
	if got := os.Getenv("CLOUDFLARE_PROPAGATION_TIMEOUT"); got != "300" {
		t.Errorf("可选项没带过去：%q", got)
	}
	if got := os.Getenv("CF_DNS_API_TOKEN"); got != "tok-alias" {
		t.Errorf("CF_ 这个别名命名空间也要认：%q", got)
	}
}

// 只翻译当前这一家。AWS_ 命名空间很宽（SDK 自己也认一堆），跟着别家一起导出去
// 会换来「我用的是 cloudflare，为什么 AWS 的凭据也生效了」这种查不清的事。
func TestExportEnvOnlyThatProvider(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("CLOUDFLARE_DNS_API_TOKEN", "")
	t.Setenv("HERDR_WEB_AWS_ACCESS_KEY_ID", "aws-key")
	t.Setenv("HERDR_WEB_CLOUDFLARE_DNS_API_TOKEN", "tok")

	if _, err := newDNS("cloudflare"); err != nil {
		t.Fatalf("newDNS: %v", err)
	}
	if got := os.Getenv("AWS_ACCESS_KEY_ID"); got != "" {
		t.Errorf("用 cloudflare 不该把 AWS 的凭据也导出去：%q", got)
	}
}

// 每家都要有命名空间，否则「带前缀的凭据被当成没配」——
// 而错误信息里写的恰恰是带前缀的名字，于是照着提示配也不生效。
func TestEveryProviderHasNamespace(t *testing.T) {
	for _, p := range Providers() {
		if len(dnsNamespaces[p]) == 0 {
			t.Errorf("%s 没在 dnsNamespaces 里，带前缀的凭据会被忽略", p)
		}
	}
}

// service install 会把抄进 unit 的每一项都打到终端上，而那段输出往往就落在一个
// 跑着 agent 的 pane 里。凭据要打成星号，非凭据（region / TTL）得照原样显示。
func TestSecretEnv(t *testing.T) {
	secret := []string{
		"HERDR_WEB_CLOUDFLARE_DNS_API_TOKEN", "CLOUDFLARE_DNS_API_TOKEN",
		"HERDR_WEB_CF_DNS_API_TOKEN", "HERDR_WEB_ALICLOUD_SECRET_KEY",
		"HERDR_WEB_ALICLOUD_ACCESS_KEY", "HERDR_WEB_TENCENTCLOUD_SECRET_ID",
		"HERDR_WEB_AWS_SECRET_ACCESS_KEY", "HERDR_WEB_AWS_ACCESS_KEY_ID",
		"HERDR_WEB_DO_AUTH_TOKEN", "HERDR_WEB_HUAWEICLOUD_SECRET_ACCESS_KEY",
	}
	for _, k := range secret {
		if !SecretEnv(k) {
			t.Errorf("%s 是凭据，不该明文打出来", k)
		}
	}
	plain := []string{
		"HERDR_WEB_AWS_REGION", "HERDR_WEB_HUAWEICLOUD_REGION",
		"HERDR_WEB_CLOUDFLARE_TTL", "HERDR_WEB_PORT", "PATH",
		"HERDR_WEB_TLS_KEY", // 这是证书路径，装的时候正要看一眼
	}
	for _, k := range plain {
		if SecretEnv(k) {
			t.Errorf("%s 不是凭据，盖掉只会让 install 那段输出没用", k)
		}
	}
}
