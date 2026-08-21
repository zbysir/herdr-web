// Package selfupdate 回答两件事：**有没有新版本**，以及**怎么换上去**。
//
// 查版本走 GitHub Releases 的 latest 接口，不需要 token（匿名 60 次/小时，够了）。
// 结果缓存到 ~/.herdr-web/update.json，进程重启也不会重新问一遍 —— 一天一次的节流
// 必须落盘，否则「起服务的时候查一次」在频繁重启的机器上就是每次都查。
//
// 为什么不做「自动装上」：这个进程后面挂着别人正在用的终端会话，换二进制之后要重启
// 才生效，而重启会把所有 PTY 掐掉。什么时候承受这个代价只能由人来定，所以这里只
// 提醒，动手是 `herdr-web update`。
package selfupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Repo 是发布仓库，也是 `herdr-web update` 下载 archive 的地方。
const Repo = "zbysir/herdr-web"

// NPMPackage 是 npm 上的包名，装法是 npm 的人要看这个提示。
const NPMPackage = "@bysir/herdr-web"

// Release 是一次查询的结果。
type Release struct {
	// Version 不带前导 v，比较和拼 archive 文件名都用它。
	Version   string    `json:"version"`
	Tag       string    `json:"tag"`
	URL       string    `json:"url"`
	Notes     string    `json:"notes,omitempty"`
	Published time.Time `json:"published,omitempty"`
}

// State 是落盘的缓存。Latest 空表示查过但没查到（比如仓库还没发过 release）。
type State struct {
	CheckedAt time.Time `json:"checkedAt"`
	Latest    string    `json:"latest,omitempty"`
	Tag       string    `json:"tag,omitempty"`
	URL       string    `json:"url,omitempty"`
	// Err 是上次查询失败的原因。留着是为了在管理页里说清「为什么没有版本信息」——
	// 空白比「查不到」更让人怀疑是不是自己看错了。
	Err string `json:"err,omitempty"`
}

// Checker 管一个数据目录上的查更新。并发安全。
type Checker struct {
	Dir      string        // 缓存放哪（一般是 cfg.Dir）
	Current  string        // 当前版本，不带 v；"dev" / "" 表示本地构建
	Interval time.Duration // 两次自动查询的最短间隔，0 用默认 24h
	// APIBase 只给测试用。空的话走 https://api.github.com。
	APIBase string
	// Client 空的话用一个带 10s 超时的。查更新绝不能拖住启动。
	Client *http.Client

	mu     sync.Mutex
	state  State
	loaded bool
}

func (c *Checker) interval() time.Duration {
	if c.Interval > 0 {
		return c.Interval
	}
	return 24 * time.Hour
}

func (c *Checker) client() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	return &http.Client{Timeout: 10 * time.Second}
}

func (c *Checker) file() string { return filepath.Join(c.Dir, "update.json") }

// State 返回缓存（第一次调用会读盘）。
func (c *Checker) State() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.load()
	return c.state
}

// load 必须在持锁时调用。
func (c *Checker) load() {
	if c.loaded {
		return
	}
	c.loaded = true
	b, err := os.ReadFile(c.file())
	if err != nil {
		return
	}
	_ = json.Unmarshal(b, &c.state) // 缓存坏了就当没有，不是错误
}

func (c *Checker) save(s State) {
	c.mu.Lock()
	c.state, c.loaded = s, true
	c.mu.Unlock()
	b, err := json.Marshal(s)
	if err != nil {
		return
	}
	if err := os.MkdirAll(c.Dir, 0o700); err != nil {
		return
	}
	// 写不进去就算了：查更新的缓存丢了最多是多查一次，不值得让它挡住任何事
	_ = os.WriteFile(c.file(), b, 0o600)
}

// Available 用缓存回答「有没有新版本」，不发请求。启动横幅和管理页都用它。
func (c *Checker) Available() (State, bool) {
	s := c.State()
	return s, Newer(c.Current, s.Latest)
}

// Check 去问一次，并写缓存。force=false 时距上次查询不到 Interval 就直接返回缓存。
func (c *Checker) Check(ctx context.Context, force bool) (State, error) {
	if !force {
		if s := c.State(); !s.CheckedAt.IsZero() && time.Since(s.CheckedAt) < c.interval() {
			return s, nil
		}
	}
	rel, err := Latest(ctx, c.client(), c.APIBase)
	now := time.Now()
	if err != nil {
		// 失败也要写 CheckedAt：不然网络不通的机器每次启动都去撞一次超时
		s := c.State()
		s.CheckedAt, s.Err = now, err.Error()
		c.save(s)
		return s, err
	}
	s := State{CheckedAt: now, Latest: rel.Version, Tag: rel.Tag, URL: rel.URL}
	c.save(s)
	return s, nil
}

// Watch 后台盯着新版本：起来先查一次（有节流），之后每 Interval 查一次。
// 发现比当前新就调 notify —— 只在**版本号变化时**调一次，别每天提醒同一个版本。
//
// 调用方应当在 goroutine 里跑它。ctx 取消就返回。
func (c *Checker) Watch(ctx context.Context, notify func(State)) {
	notified := ""
	// 已经缓存着一个新版本的话，横幅那边已经打过了，这里记下来别重复提
	if s, ok := c.Available(); ok {
		notified = s.Latest
	}
	t := time.NewTicker(c.interval())
	defer t.Stop()
	for {
		if s, err := c.Check(ctx, false); err == nil && Newer(c.Current, s.Latest) && s.Latest != notified {
			notified = s.Latest
			if notify != nil {
				notify(s)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Latest 问 GitHub 要 latest release。apiBase 空则用 api.github.com。
func Latest(ctx context.Context, hc *http.Client, apiBase string) (Release, error) {
	if apiBase == "" {
		apiBase = "https://api.github.com"
	}
	url := strings.TrimRight(apiBase, "/") + "/repos/" + Repo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("accept", "application/vnd.github+json")
	req.Header.Set("user-agent", "herdr-web")
	resp, err := hc.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		// 404 有两种：仓库不存在，或者存在但没发过 release。GitHub 不区分，
		// 所以话得说全 —— 只说「没发过 release」会让写错仓库名的人查半天。
		return Release{}, fmt.Errorf("查不到 %s 的 release（仓库不存在，或者还没发过版）", Repo)
	default:
		return Release{}, fmt.Errorf("GitHub 返回 %s", resp.Status)
	}
	var raw struct {
		TagName     string    `json:"tag_name"`
		HTMLURL     string    `json:"html_url"`
		Body        string    `json:"body"`
		Draft       bool      `json:"draft"`
		Prerelease  bool      `json:"prerelease"`
		PublishedAt time.Time `json:"published_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return Release{}, err
	}
	if raw.TagName == "" {
		return Release{}, fmt.Errorf("GitHub 没给 tag_name")
	}
	return Release{
		Version:   strings.TrimPrefix(raw.TagName, "v"),
		Tag:       raw.TagName,
		URL:       raw.HTMLURL,
		Notes:     raw.Body,
		Published: raw.PublishedAt,
	}, nil
}

// Newer 判断 latest 是否比 cur 新。
//
// 只认 x.y.z[-pre] 这一种形状，够用而且不用引第三方 semver 库。两条要点：
//   - cur 是 "dev" / 空（本地构建）时**一律返回 false**：开发机上不该弹升级提示。
//   - 带 pre-release 后缀的比同号正式版**旧**（1.0.0-rc1 < 1.0.0），这是 semver 的规矩，
//     搞反的话发了 rc 会把所有正式版用户都提示成「有新版本」。
func Newer(cur, latest string) bool {
	if latest == "" || cur == "" || cur == "dev" {
		return false
	}
	cn, cp := parse(cur)
	ln, lp := parse(latest)
	for i := 0; i < 3; i++ {
		if ln[i] != cn[i] {
			return ln[i] > cn[i]
		}
	}
	// 主版本号一样：只有「当前是 pre-release、对面不是」才算新
	return cp != "" && lp == ""
}

// parse 把 1.2.3-rc1 拆成 [1,2,3] 和 "rc1"。解析不了的段当 0。
func parse(v string) ([3]int, string) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	pre := ""
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v, pre = v[:i], v[i+1:]
	}
	var out [3]int
	for i, seg := range strings.SplitN(v, ".", 3) {
		if i > 2 {
			break
		}
		n, err := strconv.Atoi(strings.TrimSpace(seg))
		if err == nil {
			out[i] = n
		}
	}
	return out, pre
}
