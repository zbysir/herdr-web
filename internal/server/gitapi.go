package server

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/zbysir/herdr-web/internal/files"
	"github.com/zbysir/herdr-web/internal/gitdiff"
)

// 看 diff 那条路的 HTTP 层。语义、跑 git 的那几条硬规矩、以及「鉴权只有一处」都在
// internal/gitdiff 的包注释里，这儿只讲路由和参数。
//
// **三个口都是只读的。** 这一层不给 add / commit / checkout 留任何入口 —— 会改仓库的
// 事在终端里做（那儿有完整的 git，还看得见输出）。一个能从公网点到的「一键 checkout」
// 按钮，值不了它带来的那些问题。

func (s *Server) apiGit(w http.ResponseWriter, r *http.Request, seg []string) {
	if s.Git == nil || !s.Git.Enabled() {
		fail(w, http.StatusNotFound, errf("这台机器上没有 git，或者文件浏览被关掉了（HERDR_WEB_FILES=0 / HERDR_WEB_GIT=0）"))
		return
	}
	if len(seg) < 2 {
		fail(w, http.StatusNotFound, errf("没有这个接口"))
		return
	}
	q := r.URL.Query()

	switch {
	// 这批目录里哪些是 git 仓库（前端拿各个 pane 的 cwd 来问）。**认不出来的不报错**，
	// 直接不出现在候选里 —— 这是探测，不是操作。
	//
	// 多个目录用重复的 `dir=` 传，而不是 POST 一个数组：这三个口全是只读的，读的东西就该
	// 用 GET。顺带还省掉一堆麻烦 —— POST 要过跨站那道检查（Origin 必须等于 Host），而开发时
	// vite 那个代理会把 Host 改掉、Origin 不改，于是这一个口在 `make dev` 下永远是
	// 「跨站请求被拒」，而生产上好好的。
	case seg[1] == "repos" && r.Method == http.MethodGet:
		dirs := q["dir"]
		if len(dirs) > 32 {
			dirs = dirs[:32]
		}
		writeJSON(w, 200, map[string]any{"repos": s.Git.Repos(r.Context(), dirs)})

	// 顶栏那个角标：几个文件改了 + 一个「变没变」的指纹。**按秒问的就是它**，
	// 所以服务端那边带 2 秒缓存（见 gitdiff.DirtyStat）。
	case seg[1] == "dirty" && r.Method == http.MethodGet:
		dir, err := gitDir(q.Get("dir"))
		if err != nil {
			fail(w, 400, err)
			return
		}
		d, err := s.Git.DirtyStat(r.Context(), dir)
		respondFile(w, d, err)

	case seg[1] == "status" && r.Method == http.MethodGet:
		dir, err := gitDir(q.Get("dir"))
		if err != nil {
			fail(w, 400, err)
			return
		}
		st, err := s.Git.Status(r.Context(), dir, q.Get("mode"))
		respondFile(w, st, err) // 403 / 404 / 400 的分法和文件浏览那边同一套

	case seg[1] == "diff" && r.Method == http.MethodGet:
		dir, err := gitDir(q.Get("dir"))
		if err != nil {
			fail(w, 400, err)
			return
		}
		p, err := s.Git.Diff(r.Context(), gitdiff.Req{
			Dir:       dir,
			Mode:      q.Get("mode"),
			Path:      q.Get("path"),
			Old:       q.Get("old"),
			Untracked: q.Get("untracked") == "1",
			Context:   atoiDefault(q.Get("context"), 0),
			Limit:     atoiDefault(q.Get("limit"), 0),
		})
		respondFile(w, p, err)

	default:
		fail(w, http.StatusNotFound, errf("没有这个接口"))
	}
}

// gitDir 把 `dir` 参数解析成绝对路径。**不猜基准**（和文件浏览同一条规矩）：
// 相对路径在这儿没有意义，前端传的一律是某个 pane 的 cwd 或者仓库根。
func gitDir(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", errf("没说在哪个目录里看（要带上 dir=）")
	}
	return files.Resolve(s, "")
}

func atoiDefault(s string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return n
	}
	return def
}
