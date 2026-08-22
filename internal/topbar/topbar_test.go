package topbar

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestLoadFallsBackToDefaults(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	// 文件不在
	if got := s.Load().Items; strings.Join(got, ",") != strings.Join(Defaults(), ",") {
		t.Fatalf("没有文件时该给出厂配置，给的是 %v", got)
	}
	// 文件存坏了
	if err := os.WriteFile(filepath.Join(s.Dir, "topbar.json"), []byte("{ not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := s.Load().Items; strings.Join(got, ",") != strings.Join(Defaults(), ",") {
		t.Fatalf("坏文件时该给出厂配置，给的是 %v", got)
	}
}

func TestLoadDropsUnknownAndKeepsRest(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	// 「从新版本降级回来」的样子：里面有这个版本不认识的 id
	body := `{"items":["panes","teleport","settings","panes"]}`
	if err := os.WriteFile(filepath.Join(s.Dir, "topbar.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got := s.Load().Items
	if strings.Join(got, ",") != "panes,settings" {
		t.Fatalf("不认识的 id 该丢掉、重复的该去掉，剩下的照用；拿到 %v", got)
	}
}

func TestLoadAllUnknownGivesDefaults(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	body := `{"items":["teleport","warp"]}`
	if err := os.WriteFile(filepath.Join(s.Dir, "topbar.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := s.Load().Items; strings.Join(got, ",") != strings.Join(Defaults(), ",") {
		t.Fatalf("一个都认不出时该给出厂配置，给的是 %v", got)
	}
}

func TestLoadKeepsEmptyBarButPinsSettings(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	// 空栏：合法（用户可以把顶栏清到只剩设置），但 ⚙ 得补回来
	if err := os.WriteFile(filepath.Join(s.Dir, "topbar.json"), []byte(`{"items":["full"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := s.Load().Items; strings.Join(got, ",") != "full,settings" {
		t.Fatalf("设置该被补回来，拿到 %v", got)
	}
}

func TestSaveValidates(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	if _, err := s.Save(Config{Items: []string{"panes", "teleport"}}); err == nil {
		t.Fatal("不认识的 id 该报错")
	}
	if _, err := s.Save(Config{Items: []string{"panes", "panes"}}); err == nil {
		t.Fatal("重复该报错")
	}
	long := make([]string, 0, MaxItems+1)
	for i := 0; i <= MaxItems; i++ {
		long = append(long, "panes") // 长度先超了，重复的检查在后面
	}
	if _, err := s.Save(Config{Items: long}); err == nil {
		t.Fatal("超过上限该报错")
	}
}

func TestSaveRoundTripAndPins(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	out, err := s.Save(Config{Items: []string{"compose", "panes"}})
	if err != nil {
		t.Fatal(err)
	}
	// 存的时候没给设置，落盘时补在末尾 —— 不然就把自己锁在配置外面了
	if strings.Join(out.Items, ",") != "compose,panes,settings" {
		t.Fatalf("返回的顺序不对：%v", out.Items)
	}
	if got := s.Load().Items; strings.Join(got, ",") != "compose,panes,settings" {
		t.Fatalf("读回来的顺序不对：%v", got)
	}
	// 文件是给人看 / 偶尔手改的，落盘就该是那一串 id
	b, err := os.ReadFile(filepath.Join(s.Dir, "topbar.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f Config
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	if strings.Join(f.Items, ",") != "compose,panes,settings" {
		t.Fatalf("落盘的内容不对：%s", b)
	}
}

func TestPinnedAreKnown(t *testing.T) {
	for _, p := range Pinned {
		if !known(p) {
			t.Fatalf("Pinned 里的 %q 不在 Actions 里", p)
		}
	}
	for _, d := range Defaults() {
		if !known(d) {
			t.Fatalf("出厂配置里的 %q 不在 Actions 里", d)
		}
	}
}

// TestActionsMatchJS 白名单要和前端那份按钮目录**一字不差**（顺序也一样）。
//
// 为什么值得一个测试：这两份是同一件事的两半 —— 前端那份带图标和点了干什么，服务端这份
// 决定「能不能存进来」。只在一边加一个按钮的后果很难查：编辑器里拖得上去，一存就报
// 「不认识的按钮」（或者反过来，存得下去但顶栏上画不出来）。
//
// 直接读 .tsx 抠 id，不用快照文件：快照是第三份，一样会忘了改。
func TestActionsMatchJS(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "web", "src", "components", "topbarItems.tsx"))
	if err != nil {
		t.Skipf("读不到前端那份目录（源码包里可能没有 web/）: %v", err)
	}
	ids := regexp.MustCompile(`\{ id: '([a-z+-]+)'`).FindAllStringSubmatch(string(b), -1)
	got := make([]string, 0, len(ids))
	for _, m := range ids {
		got = append(got, m[1])
	}
	if strings.Join(got, ",") != strings.Join(Actions, ",") {
		t.Fatalf("白名单和前端目录不一致\n go: %v\n js: %v", Actions, got)
	}
}
