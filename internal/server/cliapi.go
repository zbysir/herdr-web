package server

import (
	"net/http"
	"time"

	"github.com/zbysir/herdr-web/internal/auth"
)

// apiCLI 是命令行登录（`herdr-web connect`）那几个口：在浏览器里用 passkey 批准一台
// 命令行设备（第一次登录，或者到期重验）。流程和为什么第一次登录也能走这条，见
// internal/auth/cliapprove.go 的包注释那段。
//
//	start    发起方（命令行）。不要求认证；带着 Bearer 来 = 给那台设备重验
//	poll     发起方拿轮询令牌来问
//	peek     批准方（浏览器，已认证）：输了显示码，先看一眼是谁
//	approve  批准方：要求**刚过了一把 passkey**（step-up，403 reason=stepup 让前端去刷）
//	deny     批准方：拒绝
func (s *Server) apiCLI(w http.ResponseWriter, r *http.Request, seg []string) {
	if len(seg) < 3 || r.Method != http.MethodPost {
		fail(w, 404, errf("没有这个接口"))
		return
	}
	switch seg[2] {
	case "start":
		// 一把 passkey 都没有就没有东西可批：命令行只能用配对码（那种情况下也没有 24 小时重验）
		if s.Passkeys == nil || s.Passkeys.Count() == 0 {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": "那边还没注册 passkey，只能用配对码", "reason": "nopasskey",
			})
			return
		}
		device := ""
		if r.Header.Get("Authorization") != "" {
			id := s.Auth.Authenticate(r)
			if id == nil || id.Device == nil || id.Ambient {
				fail(w, http.StatusUnauthorized, errf("这份凭据不认了（被撤销或过期了）"))
				return
			}
			device = id.Device.ID
		}
		req, poll, err := s.Auth.StartCLI(r.UserAgent(), s.Auth.ClientIP(r), auth.FromThisMachine(r), device)
		if err != nil {
			fail(w, http.StatusTooManyRequests, err)
			return
		}
		writeJSON(w, 200, map[string]any{
			"id": req.ID, "poll": poll, "code": req.Code,
			"expires": req.Expires, "url": s.cliURL(r),
		})

	case "poll":
		var b struct{ ID, Poll string }
		if err := readJSON(r, &b); err != nil {
			fail(w, 400, err)
			return
		}
		res, err := s.Auth.PollCLI(b.ID, b.Poll)
		if err != nil {
			fail(w, http.StatusGone, err)
			return
		}
		out := map[string]any{"state": res.State}
		if res.Device != nil {
			out["label"], out["deviceId"] = res.Device.Label, res.Device.ID
		}
		if res.Token != "" {
			out["token"] = res.Token
		}
		writeJSON(w, 200, out)

	case "peek", "approve", "deny":
		if s.requireAuth(w, r) == nil {
			return
		}
		var b struct{ Code string }
		if err := readJSON(r, &b); err != nil {
			fail(w, 400, err)
			return
		}
		// 显示码只有 40 位：批准方虽然已认证，猜码也照样进限速（码错了算一次失败）
		if !s.gateCheck(w, r) {
			return
		}
		ip := s.Auth.ClientIP(r)
		var req auth.CLIRequest
		var err error
		switch seg[2] {
		case "peek":
			req, err = s.Auth.PeekCLI(b.Code)
		case "deny":
			err = s.Auth.DenyCLI(b.Code)
		case "approve":
			// 先确认码是真的再要 passkey：码输错了就别让人白刷一次 Face ID
			if req, err = s.Auth.PeekCLI(b.Code); err == nil {
				if s.cliStepUp(w, r) {
					return
				}
				req, err = s.Auth.ApproveCLI(b.Code)
			}
		}
		if err != nil {
			if err == auth.ErrCLIUnknown {
				s.Gate.Fail(ip)
			}
			fail(w, http.StatusNotFound, err)
			return
		}
		s.Gate.Reset(ip)
		writeJSON(w, 200, req)

	default:
		fail(w, 404, errf("没有这个接口"))
	}
}

// cliStepUp：批准必须是**刚过了一把 passkey** 的人 —— 光有一个登录着的浏览器不够
// （那正是「手机被拿去一次」的情形）。挡住了返回 true，响应已经写出去。
//
// 和注册 passkey 那道门是同一个判据（StepUpNeeded），但**不走 s.stepUp**：那个的前提是
// 「一把都没有时不设门」，而这里一把都没有时 start 就已经拒了，走到这儿一定有。
func (s *Server) cliStepUp(w http.ResponseWriter, r *http.Request) bool {
	id := s.Auth.Authenticate(r)
	if id != nil && id.Kind == "device" && time.Since(id.PasskeyAt) <= stepUpWindow {
		return false
	}
	writeJSON(w, http.StatusForbidden, map[string]any{
		"error":  "批准之前先用 passkey 验一次",
		"reason": "stepup",
	})
	return true
}

// cliURL 是命令行那边该告诉人「去哪儿批准」的地址。配了 PUBLIC_URL 就用它（发起方可能是
// 从局域网口连的，但人拿着的手机多半在外面），否则就用发起方自己连的那个 origin。
func (s *Server) cliURL(r *http.Request) string {
	base := s.Cfg.PublicURL // config 里已经去掉了结尾的 /
	if base == "" {
		scheme := "http"
		if s.TLS {
			scheme = "https"
		}
		base = scheme + "://" + r.Host
	}
	return base + "/?cli"
}
