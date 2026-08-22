package server

import (
	"net/http"
	"time"

	"github.com/zbysir/herdr-web/internal/auth"
)

// passkey 的四个口。
//
// 注册要求已认证（在网页上注册 passkey 是安全的 —— 私钥出不去设备的安全芯片，不构成一份
// 能被人拿走的凭据。这和「网页上出配对码」是两回事：那个会造出一份独立的、不随创造者一起
// 被撤销的 bearer 凭据）。
//
// 登录不要求认证，这正是它的价值：**换新设备时不用回机器前**。同步 passkey 的话，
// 一次注册就覆盖你所有设备。
// passkeyOK 这个请求所在的 origin 上能不能做 passkey。
//
// **必须按请求算，不能只看部署配了 RPID 没有**：开了局域网直连之后同一个部署同时有域名
// origin（公网那条路）和裸 IP origin（直连那条），而 WebAuthn 只认域名。判据的细节和
// 为什么和证书无关，见 auth.UsableOn。
func (s *Server) passkeyOK(r *http.Request) bool {
	return s.Passkeys.Available() && auth.UsableOn(s.RPID, r.Host)
}

// 裸 IP 上直接把这几个口拒掉，而不是让 WebAuthn 库在后面报一个看不懂的 origin 错。
func (s *Server) passkeyGate(w http.ResponseWriter, r *http.Request) bool {
	if s.passkeyOK(r) {
		return true
	}
	fail(w, http.StatusConflict, errf("这个地址上用不了 passkey：WebAuthn 的标识只能是域名，裸 IP 不行。用域名那条路访问"))
	return false
}

func (s *Server) apiPasskey(w http.ResponseWriter, r *http.Request, seg []string) {
	// seg = ["auth", "passkey", ...] 或 ["auth", "passkeys", ...]
	if seg[1] == "passkeys" {
		s.apiPasskeyList(w, r, seg)
		return
	}
	if len(seg) < 4 {
		fail(w, 404, errf("没有这个接口"))
		return
	}
	action, step := seg[2], seg[3]

	// 注册和登录两套 ceremony 在裸 IP 上都不可能成功，早拒早说清楚
	if !s.passkeyGate(w, r) {
		return
	}

	switch {
	case action == "register" && step == "begin" && r.Method == http.MethodPost:
		if s.requireAuth(w, r) == nil {
			return
		}
		opts, ceremony, err := s.Passkeys.BeginRegister()
		if err != nil {
			fail(w, 400, err)
			return
		}
		writeJSON(w, 200, map[string]any{"options": opts, "ceremony": ceremony})

	case action == "register" && step == "finish" && r.Method == http.MethodPost:
		id := s.requireAuth(w, r)
		if id == nil {
			return
		}
		k, err := s.Passkeys.FinishRegister(r.URL.Query().Get("c"), r.UserAgent(), r)
		if err != nil {
			fail(w, 400, err)
			return
		}
		// 刚做过生物验证，顺手给当前设备续上 —— 否则注册完这一刻就会被要求重验
		if id.Device != nil {
			s.Auth.MarkVerified(id.Device.ID)
		}
		writeJSON(w, 200, map[string]any{"ok": true, "id": k.ID, "label": k.Label})

	case action == "login" && step == "begin" && r.Method == http.MethodPost:
		if !s.gateCheck(w, r) {
			return
		}
		opts, ceremony, err := s.Passkeys.BeginLogin()
		if err != nil {
			fail(w, 400, err)
			return
		}
		writeJSON(w, 200, map[string]any{"options": opts, "ceremony": ceremony})

	case action == "login" && step == "finish" && r.Method == http.MethodPost:
		if !s.gateCheck(w, r) {
			return
		}
		ip := s.Auth.ClientIP(r)
		k, err := s.Passkeys.FinishLogin(r.URL.Query().Get("c"), r)
		if err != nil {
			s.Gate.Fail(ip)
			fail(w, http.StatusUnauthorized, err)
			return
		}
		s.Gate.Reset(ip)

		// 已经是配过对的设备：只是刷新「上次验证」时间，不新建设备
		if id := s.Auth.Authenticate(r); id != nil && id.Device != nil {
			s.Auth.MarkVerified(id.Device.ID)
			s.refresh(w, r)
			writeJSON(w, 200, map[string]any{"ok": true, "label": id.Device.Label, "passkey": k.Label})
			return
		}
		// 新设备：passkey 直接换一份设备凭据，不用回机器前拿配对码
		dev, token := s.Auth.NewDevice(r.UserAgent(), ip, "passkey")
		s.Auth.IssueCookie(w, token)
		writeJSON(w, 200, map[string]any{"ok": true, "label": dev.Label, "passkey": k.Label})

	default:
		fail(w, 404, errf("没有这个接口"))
	}
}

func (s *Server) apiPasskeyList(w http.ResponseWriter, r *http.Request, seg []string) {
	if s.requireAuth(w, r) == nil {
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, 200, map[string]any{
			"passkeys":  s.Passkeys.List(),
			"available": s.passkeyOK(r),
			"rpid":      s.RPID,
		})
	case http.MethodDelete:
		if len(seg) < 3 || seg[2] == "" {
			fail(w, 400, errf("要给一个 id"))
			return
		}
		label, ok := s.Passkeys.Delete(seg[2])
		if !ok {
			fail(w, 404, errf("没有这把 passkey"))
			return
		}
		writeJSON(w, 200, map[string]any{"deleted": 1, "label": label})
	default:
		fail(w, 405, errf("方法不对"))
	}
}

// reauthNeeded 把判断委托给 auth.ReauthNeeded（那边好写测试），这里只喂参数。
func (s *Server) reauthNeeded(id *auth.Ident) bool {
	return auth.ReauthNeeded(id, s.ReauthAfter, s.Passkeys.Count(), time.Now())
}
