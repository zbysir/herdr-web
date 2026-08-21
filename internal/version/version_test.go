package version

import "testing"

// 只认真 tag。收下伪版本的后果是本地构建一直提示「有新版本」，而那个现象很难联想到这里。
func TestReleaseTag(t *testing.T) {
	yes := []string{"v1.0.0", "v0.1.2", "v1.2.3-rc1", "v10.20.30"}
	no := []string{
		"", "(devel)", "dev", "1.0.0", "v1.0", "v1.0.0.0",
		"v0.0.0-20260821095702-ca3cc05636d8",       // go build 出来的伪版本
		"v0.0.0-20260821095702-ca3cc05636d8+dirty", // 带脏标记
		"v2.0.0+incompatible",                      // 没上 go module 的老库
		"vX.Y.Z",
	}
	for _, v := range yes {
		if !releaseTag(v) {
			t.Errorf("releaseTag(%q) = false, 应当是真 tag", v)
		}
	}
	for _, v := range no {
		if releaseTag(v) {
			t.Errorf("releaseTag(%q) = true, 不该收", v)
		}
	}
}

func TestDev(t *testing.T) {
	old := Version
	defer func() { Version = old }()
	for _, v := range []string{"dev", ""} {
		Version = v
		if !Dev() {
			t.Errorf("Version=%q 应当算本地构建", v)
		}
	}
	Version = "v1.0.0"
	if Dev() {
		t.Error("v1.0.0 不该算本地构建")
	}
	if Semver() != "1.0.0" {
		t.Errorf("Semver() = %q", Semver())
	}
}
