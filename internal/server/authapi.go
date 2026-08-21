package server

import (
	"net/http"
	"net/url"

	"git.huglight.cn/bysir/herdr-web/internal/auth"
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
		if tok := q.Get("token"); tok != "" {
			s.enter(w, r, "", tok)
			return
		}
	}
	s.handleStatic(w, r)
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
func (s *Server) clean(w http.ResponseWriter, r *http.Request, kv ...string) {
	q := r.URL.Query()
	q.Del("pair")
	q.Del("token")
	for i := 0; i+1 < len(kv); i += 2 {
		q.Set(kv[i], kv[i+1])
	}
	u := url.URL{Path: "/", RawQuery: q.Encode()}
	w.Header().Set("cache-control", "no-store")
	http.Redirect(w, r, u.String(), http.StatusFound)
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
			// 配对页据此决定要不要显示「用 passkey 登录」
			"passkeys":         s.Passkeys.Count(),
			"passkeyAvailable": s.Passkeys.Available(),
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
