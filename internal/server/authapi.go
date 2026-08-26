package server

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/zbysir/herdr-web/internal/auth"
	"github.com/zbysir/herdr-web/internal/config"
)

// handleRoot 在静态资源前面截两种「URL 里带着秘密」的进入方式，换成 cookie 之后
// **把秘密从地址栏里抹掉**（302 到干净的 URL）。
//
// 这是「一台设备配一次」的入口：之后书签就是 https://host:port/，里面没有任何秘密，
// 所以浏览器历史、书签云同步、截图都不再是泄露渠道。
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		q := r.URL.Query()
		if code := q.Get("pair"); code != "" {
			s.enter(w, r, code, "")
			return
		}
		// 局域网直连的交接令牌。**只在从直连那个监听进来的请求上认** —— 判据是请求落在
		// 哪个监听上，不是 Host（Host 是客户端说的，公网那条路伪造一个内网 IP 就绕过去了）。
		// 走别的路进来的话当它不存在，照常渲染页面：那多半是链接被复制到了别处。
		if tok := q.Get("handoff"); tok != "" && FromLan(r) {
			s.enterLan(w, r, tok)
			return
		}
		if tok := q.Get("token"); tok != "" {
			s.enter(w, r, "", tok)
			return
		}
	}
	s.handleStatic(w, r)
}

// enterLan 用交接令牌在**直连这一侧**换一份凭据（见 auth.MintHandoff）。
//
// 已经有凭据就什么都不做，只把 URL 洗干净 —— 令牌自己过期。所以「每切一次都带一枚」
// 不会堆出一串设备。
//
// 不进 gateCheck：那一层管的是**猜得到的**凭据（配对码、旧 token）的限速，而交接令牌是
// 256 位随机的。把它算进失败计数反而会给出一条新的骚扰路径（拿废令牌反复打，把真人的
// 配对锁死）。
func (s *Server) enterLan(w http.ResponseWriter, r *http.Request, tok string) {
	if id := s.Auth.Authenticate(r); id != nil && id.Kind == "device" {
		s.clean(w, r)
		return
	}
	_, token, err := s.Auth.RedeemHandoff(tok, r.UserAgent(), s.Auth.ClientIP(r))
	if err != nil {
		s.clean(w, r, "e", "handoff")
		return
	}
	s.Auth.IssueCookie(w, token)
	s.clean(w, r)
}

func (s *Server) enter(w http.ResponseWriter, r *http.Request, code, legacy string) {
	// 已经有设备凭据了就什么都不做，只把 URL 洗干净 —— 否则旧书签每打开一次就多一台设备
	if id := s.Auth.Authenticate(r); id != nil && id.Kind == "device" {
		s.clean(w, r)
		return
	}
	if !s.gateCheck(w, r) {
		return
	}
	ua, ip := r.UserAgent(), s.Auth.ClientIP(r)

	if code != "" {
		_, token, err := s.Auth.Redeem(code, ua, ip)
		if err != nil {
			s.Gate.Fail(ip)
			s.clean(w, r, "e", "code")
			return
		}
		s.Gate.Reset(ip)
		s.Auth.IssueCookie(w, token)
		s.clean(w, r)
		return
	}

	// 旧 `?token=`：只够换一次设备凭据。换完就该把 ~/.herdr-web/token 删掉。
	if id := s.Auth.Authenticate(r); id == nil || id.Kind != "legacy" {
		s.Gate.Fail(ip)
		s.clean(w, r, "e", "token")
		return
	}
	s.Gate.Reset(ip)
	_, token := s.Auth.NewDevice(ua, ip, "旧 token 迁移")
	s.Auth.IssueCookie(w, token)
	s.clean(w, r)
}

// clean 跳到「去掉 pair/token、其余查询参数原样保留」的 URL。
// 保留是有意的：?poll= / ?push= 那两个调试参数还得能用。
//
// 路径也要保留：`/{session}` 是「开这个 herdr session」，扫码进来的链接是
// `https://host/work?pair=CODE`，洗成 `/` 的话人配完对就落在默认 session 上了。
func (s *Server) clean(w http.ResponseWriter, r *http.Request, kv ...string) {
	q := r.URL.Query()
	q.Del("pair")
	q.Del("token")
	q.Del("handoff")
	for i := 0; i+1 < len(kv); i += 2 {
		q.Set(kv[i], kv[i+1])
	}
	u := url.URL{Path: sessionPath(r.URL.Path), RawQuery: q.Encode()}
	w.Header().Set("cache-control", "no-store")
	http.Redirect(w, r, u.String(), http.StatusFound)
}

// sessionPath 把请求路径收成「/」或者「/{合法的 session 名}」。
//
// **不能把 r.URL.Path 原样回填进 Location**：`GET //evil.com` 这种请求的路径是
// `//evil.com`，回填出来是个协议相对的 URL，浏览器会当外站跳过去 —— 一个开放重定向。
// 走白名单就没有这类问题。
func sessionPath(p string) string {
	// 只收「一段」：`//evil.com` 去掉两边的斜杠之后是个看着合法的名字（`evil.com`），
	// 所以判断要在**斜杠还在**的时候做。
	seg := strings.TrimRight(p, "/")
	if strings.HasPrefix(seg, "/") && config.ValidSessionName(seg[1:]) {
		return seg
	}
	return "/"
}

// apiAuth 是唯一一组**不要求已认证**的接口 —— 配对页自己得能加载和提交。
func (s *Server) apiAuth(w http.ResponseWriter, r *http.Request, seg []string) {
	if len(seg) < 2 {
		fail(w, 404, errf("没有这个接口"))
		return
	}
	if seg[1] == "passkey" || seg[1] == "passkeys" {
		s.apiPasskey(w, r, seg)
		return
	}

	switch {
	case seg[1] == "whoami" && r.Method == http.MethodGet:
		id := s.Auth.Authenticate(r)
		out := map[string]any{
			"authed": id != nil, "tls": s.TLS,
			"ttlDays": s.Cfg.DeviceTTLDays,
			// 有没有旧 token 文件：配对页要据此提示「你也可以用旧链接进来」
			"legacy": s.Cfg.Token != "" && s.Cfg.LegacyToken != "off",
			// 配对页据此决定要不要显示「用 passkey 登录」。**按当前 origin 算** ——
			// 裸 IP 上那个按钮按下去只会抛 SecurityError，见 Server.passkeyOK
			"passkeys":         s.Passkeys.Count(),
			"passkeyAvailable": s.passkeyOK(r),
			// 用不了的时候「该换哪个地址」：空 = 说不出确切地址（见 passkeyURL）
			"passkeyURL": s.passkeyURL(),
		}
		if id != nil {
			out["kind"], out["label"] = id.Kind, id.Label
			if id.Device != nil {
				out["deviceId"] = id.Device.ID
				if !id.Device.Expires.IsZero() {
					out["expires"] = id.Device.Expires
				}
				// 每次开页面顺手把 cookie 的 Max-Age 续上（浏览器那边最长只认 400 天）
				s.refresh(w, r)
			}
		}
		writeJSON(w, 200, out)

	// 手输配对码（扫不了码的设备，或者二维码过期了）
	case seg[1] == "pair" && r.Method == http.MethodPost:
		var b struct{ Code string }
		if err := readJSON(r, &b); err != nil {
			fail(w, 400, err)
			return
		}
		if !s.gateCheck(w, r) {
			return
		}
		ip := s.Auth.ClientIP(r)
		dev, token, err := s.Auth.Redeem(b.Code, r.UserAgent(), ip)
		if err != nil {
			s.Gate.Fail(ip)
			fail(w, http.StatusUnauthorized, err)
			return
		}
		s.Gate.Reset(ip)
		s.Auth.IssueCookie(w, token)
		writeJSON(w, 200, map[string]any{"ok": true, "label": dev.Label})

	case seg[1] == "logout" && r.Method == http.MethodPost:
		id := s.Auth.Authenticate(r)
		if id != nil && id.Device != nil {
			s.Auth.Revoke(id.Device.ID) // 退出就是撤销这台设备，不留半截凭据
		}
		s.Auth.ClearCookie(w)
		writeJSON(w, 200, map[string]any{"ok": true})

	case seg[1] == "devices" && r.Method == http.MethodGet:
		id := s.requireAuth(w, r)
		if id == nil {
			return
		}
		writeJSON(w, 200, map[string]any{"devices": s.Auth.Devices(), "me": deviceID(id)})

	case seg[1] == "devices" && r.Method == http.MethodDelete:
		id := s.requireAuth(w, r)
		if id == nil {
			return
		}
		if len(seg) < 3 || seg[2] == "" {
			n := s.Auth.RevokeAll()
			s.Auth.ClearCookie(w) // 包括自己
			writeJSON(w, 200, map[string]any{"revoked": n})
			return
		}
		label, ok := s.Auth.Revoke(seg[2])
		if !ok {
			fail(w, 404, errf("没有这台设备"))
			return
		}
		if deviceID(id) == seg[2] {
			s.Auth.ClearCookie(w)
		}
		writeJSON(w, 200, map[string]any{"revoked": 1, "label": label})

	default:
		fail(w, 404, errf("没有这个接口"))
	}
}

func deviceID(id *auth.Ident) string {
	if id == nil || id.Device == nil {
		return ""
	}
	return id.Device.ID
}

// refresh 重新下发一次 cookie。cookie 值本身在请求里，直接原样回写。
func (s *Server) refresh(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(auth.CookieName); err == nil && c.Value != "" {
		s.Auth.IssueCookie(w, c.Value)
	}
}

// requireAuth 给「必须已认证」的接口用；顺手做跨站检查。
func (s *Server) requireAuth(w http.ResponseWriter, r *http.Request) *auth.Ident {
	id := s.Auth.Authenticate(r)
	if id == nil {
		fail(w, http.StatusUnauthorized, errf("这台设备还没配对"))
		return nil
	}
	if id.Ambient && (!csrfHeaderOK(r) || !s.originOK(r)) {
		fail(w, http.StatusForbidden, errf("跨站请求被拒"))
		return nil
	}
	if s.reauthNeeded(id) {
		// need 这个字段让前端能区分「没配对」和「配过但要重新验一次」——
		// 两者的 UI 完全不同：一个要配对码，一个只要点一下 Face ID
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"error": "太久没验证了，用 passkey 再验一次", "need": "passkey",
		})
		return nil
	}
	return id
}
