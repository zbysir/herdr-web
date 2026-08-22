package server

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zbysir/herdr-web/internal/files"
)

// 文件浏览的 HTTP 层。语义和取舍在 internal/files 的包注释里，这儿只讲两件事：
// 路由怎么分、以及**吐内容那条路为什么单独开一个不带 cookie 的口**。

func userHome() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
}

// resolveQ 把请求里的 path（可能是相对路径 / 带 ~）解析成绝对路径。
//
// base 是相对路径的基准，前端在**终端里点到一个相对路径**时传的是那个 pane 的 cwd
// （`/api/herdr/panes` 里就有）。没有 base 就不认相对路径 —— 猜一个基准出来，错的
// 时候会安静地打开另一个同名文件，而屏幕上看不出任何异常。
func resolveQ(r *http.Request) (string, error) {
	return files.Resolve(r.URL.Query().Get("path"), strings.TrimSpace(r.URL.Query().Get("base")))
}

func (s *Server) apiFiles(w http.ResponseWriter, r *http.Request, seg []string) {
	if s.Files == nil || !s.Files.Enabled {
		fail(w, http.StatusNotFound, errf("文件浏览被关掉了（HERDR_WEB_FILES=0）"))
		return
	}
	if len(seg) < 2 {
		fail(w, http.StatusNotFound, errf("没有这个接口"))
		return
	}
	q := r.URL.Query()

	switch {
	// 起点书签。**pane 的 cwd 不在这儿** —— 前端手上已经有 /api/herdr/panes 的结果
	// （面板一览用的同一份），在那边合进来就行，没必要为这个再打一次 herdr socket。
	case seg[1] == "roots" && r.Method == http.MethodGet:
		writeJSON(w, 200, map[string]any{
			"roots":  s.Files.Starts(),
			"jailed": s.Files.Jailed(),
			"limits": map[string]int{"entries": files.MaxEntries, "text": files.MaxText},
		})

	case seg[1] == "list" && r.Method == http.MethodGet:
		p, err := resolveQ(r)
		if err != nil {
			fail(w, 400, err)
			return
		}
		out, err := s.Files.List(p, q.Get("sort"), q.Get("all") == "1")
		respondFile(w, out, err)

	// 认一个路径是什么 + 顺手给一张票。合成一个口是有理由的：终端里点一下路径之后，
	// 「这是图还是文本」和「拿什么 URL 去渲染」是同一个动作要的两样东西，分两次问
	// 只是在本机 socket 上白跑一趟往返。
	case seg[1] == "stat" && r.Method == http.MethodGet:
		p, err := resolveQ(r)
		if err != nil {
			fail(w, 400, err)
			return
		}
		info, err := s.Files.Peek(p)
		if err != nil {
			respondFile(w, info, err)
			return
		}
		out := map[string]any{"info": info}
		if !info.Dir && info.Kind != files.KindSpecial {
			out["url"] = s.rawURL(info.Path)
			out["expires"] = time.Now().Add(files.TokenTTL).UnixMilli()
		}
		writeJSON(w, 200, out)

	case seg[1] == "text" && r.Method == http.MethodGet:
		p, err := resolveQ(r)
		if err != nil {
			fail(w, 400, err)
			return
		}
		out, err := s.Files.ReadText(p)
		respondFile(w, out, err)

	// 单独换一张票。stat 已经给过一张，这个口是给「开着看了十几分钟、票过期了」
	// 那种情况续一张，不用重新走一遍 stat。
	case seg[1] == "link" && r.Method == http.MethodPost:
		var b struct{ Path, Base string }
		if err := readJSON(r, &b); err != nil {
			fail(w, 400, err)
			return
		}
		p, err := files.Resolve(b.Path, strings.TrimSpace(b.Base))
		if err != nil {
			fail(w, 400, err)
			return
		}
		if err := s.Files.Check(p); err != nil {
			fail(w, 403, err)
			return
		}
		writeJSON(w, 200, map[string]any{
			"url": s.rawURL(p), "path": p,
			"expires": time.Now().Add(files.TokenTTL).UnixMilli(),
		})

	default:
		fail(w, http.StatusNotFound, errf("没有这个接口"))
	}
}

// respondFile 把 files 那边的错误翻成合适的状态码。
//
// 分开三档是为了前端能区分对待：403 是「这个部署不让看」（配了 FILE_ROOTS），
// 404 是「没这个文件」（终端里那行路径可能是折断的、或者文件已经被删了），
// 其余 400。都糊成 400 的话，「路径抄错了」和「被 jail 挡了」在界面上长得一样。
func respondFile(w http.ResponseWriter, out any, err error) {
	if err == nil {
		writeJSON(w, 200, out)
		return
	}
	switch {
	case errors.Is(err, files.ErrDisabled):
		fail(w, http.StatusNotFound, err)
	case errors.Is(err, os.ErrNotExist):
		fail(w, http.StatusNotFound, err)
	case errors.Is(err, os.ErrPermission):
		fail(w, http.StatusForbidden, err)
	case strings.Contains(err.Error(), "不在允许的目录里"):
		fail(w, http.StatusForbidden, err)
	default:
		fail(w, http.StatusBadRequest, err)
	}
}

// rawURL 出一条 /_f/ 链接。末尾挂上文件名只是为了好用：新标签的标题、以及
// 「另存为」默认的文件名都取自 URL 最后一段。**票在前面那一段**，文件名部分
// 不参与验签，改它没有任何作用。
func (s *Server) rawURL(p string) string {
	if s.Sign == nil {
		return ""
	}
	tok := s.Sign.Sign(p, time.Now())
	return "/_f/" + tok + "/" + url.PathEscape(filepath.Base(p))
}

/* ------------------------------------------------------------------ 吐内容 */

// handleFileRaw 是唯一真正吐文件字节的口，而且**不看 cookie**，只认票。
//
// 为什么必须这样：`/api/*` 上 cookie 认证的请求要求 `X-Herdr-Web` 头（CSRF 的第三道
// 防线，见 requireAuth），而 `<img src>`、「在新标签打开」、iOS「长按存到相册」全都
// 设不了头 —— 走 /api 的话它们一律 403。票是能力凭据：绑死一个路径、十几分钟就过期、
// 密钥只在内存里（重启即全废）。细节和代价写在 internal/files/sign.go。
//
// 这个口上四条硬规矩，任何一条破了都是一个能被 agent 写出来的文件利用的洞：
//
//  1. **content-type 只有两种**：认出来的图，和 application/octet-stream。
//     绝不会是 text/html —— 同源的 HTML 页面能调本站所有 /api（cookie 是 HttpOnly，
//     但它根本不需要读 cookie，浏览器会自动带上），等于 agent 写个 html 你点开就把
//     herdr 交出去了。
//  2. **只有图 inline**，其余一律 attachment。
//  3. **再压一条 CSP `sandbox`**，覆盖 guard 里那条。万一前两条哪天被改坏了，
//     sandbox 会让这个响应连自己的源都没有（同源 fetch 直接失效），脚本也不给执行。
//     SVG 再多收一档，见 svgCSP。
//  4. **只读常规文件**（在 files.Open 里挡）—— /dev/zero 是一条无限流。
//
// svgCSP 是只发给 SVG 的那条。
//
// SVG 是**能跑脚本**的图片，所以它安不安全全看以什么身份被渲染。两条路各自独立成立：
//
//   - 查看器里是 `<img src=…>`。规范规定的 secure static mode：脚本一律不跑、外部
//     资源一律不加载。**这条不看任何响应头**，就算这个 CSP 哪天被前置代理剥掉了也还在。
//   - 「在新标签打开」是顶层文档，那时候 SVG 里的 `<script>` / `onload=` 本来是会跑的。
//     `sandbox`（不带 allow-scripts）把执行掐掉，顺带把源变成 opaque —— 就算跑起来
//     也碰不到本站的 API。
//
// `default-src 'none'` 是再多的一档：堵掉「svg 里塞个外链图片 / 字体，你一打开它就
// 往外发一个请求」。style 要放行 —— 图表类 svg 基本都带 `style=` 属性和 `<style>`，
// 不放行就画出来一片黑。data: 的图和字体是自包含的，放行没有外发风险。
const svgCSP = "sandbox; default-src 'none'; style-src 'unsafe-inline'; img-src data:; font-src data:"

func (s *Server) handleFileRaw(w http.ResponseWriter, r *http.Request) {
	if s.Files == nil || !s.Files.Enabled || s.Sign == nil {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "只支持 GET", http.StatusMethodNotAllowed)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/_f/")
	tok, _, _ := strings.Cut(rest, "/") // 后面那段是给「另存为」看的文件名，不验签
	p, err := s.Sign.Verify(tok, time.Now())
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	f, info, err := s.Files.Open(p)
	if err != nil {
		code := http.StatusBadRequest
		if errors.Is(err, os.ErrNotExist) {
			code = http.StatusNotFound
		} else if errors.Is(err, os.ErrPermission) {
			code = http.StatusForbidden
		}
		http.Error(w, err.Error(), code)
		return
	}
	defer f.Close()

	h := w.Header()
	h.Set("content-security-policy", "sandbox")
	h.Set("x-content-type-options", "nosniff")
	// 这是别人磁盘上的文件，可能是任何东西 —— 别让它留在共用设备的磁盘缓存里。
	// 票本来就只活十几分钟，缓存也没多少意义。
	h.Set("cache-control", "no-store, private")
	if info.Kind == files.KindImage {
		h.Set("content-type", info.Mime)
		h.Set("content-disposition", disposition("inline", info.Name))
		if info.Mime == files.SVGMIME {
			h.Set("content-security-policy", svgCSP)
		}
	} else {
		h.Set("content-type", "application/octet-stream")
		h.Set("content-disposition", disposition("attachment", info.Name))
	}
	st, err := f.Stat()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// ServeContent 顺带把 Range 和 If-Modified-Since 处理了：手机上拖动一个大文件的
	// 下载进度条要靠它。名字传空串，免得它按扩展名再猜一次 content-type 把上面覆盖掉。
	http.ServeContent(w, r, "", st.ModTime(), f)
}

// disposition 拼 Content-Disposition。
//
// 文件名是**磁盘上来的**，可能含引号、换行、任何字节 —— 直接拼进响应头就是一个
// 头注入。所以 ASCII 那一份只留可打印字符并去掉引号和反斜杠，真正的名字走
// RFC 5987 的 `filename*`（浏览器优先用它）。
func disposition(kind, name string) string {
	var ascii strings.Builder
	for _, r := range name {
		if r >= 0x20 && r < 0x7f && r != '"' && r != '\\' {
			ascii.WriteRune(r)
		}
	}
	safe := ascii.String()
	if safe == "" {
		safe = "download"
	}
	return fmt.Sprintf("%s; filename=%q; filename*=UTF-8''%s", kind, safe, url.PathEscape(name))
}
