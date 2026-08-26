package server

import (
	"log"
	"net/http"
	"sort"
	"strings"

	"github.com/zbysir/herdr-web/internal/auth"
	"github.com/zbysir/herdr-web/internal/profiles"
)

// apiProfiles 是「这台设备用哪一套排布」那一层（见 internal/profiles 的包注释）。
//
// 路由：
//
//	POST   /api/profiles/hello       报到：记住这台设备，没绑过就按 kind 挑一套绑上
//	GET    /api/profiles             名册 + 这台设备用哪一套 + 各设备绑在哪（只读）
//	POST   /api/profiles             新建（可以从某一套复制排布过来）
//	PUT    /api/profiles/{id}        改名
//	DELETE /api/profiles/{id}        删掉（快捷键条 / 顶栏里那一段一起清）
//	POST   /api/profiles/bind        把某台设备绑到某一套（可以绑别人那台）
//	PUT    /api/profiles/{id}/prefs  合并几个开关（只动传进来的那几个键）
//
// **为什么报到是 POST 而不是让 GET 顺手绑**：绑定是写操作。藏在 GET 里的写会让
// 「curl 看一眼」变成「curl 改了配置」，而这个口前端每次开页面都要打一次。
func (s *Server) apiProfiles(w http.ResponseWriter, r *http.Request, seg []string) {
	sub := ""
	if len(seg) > 1 {
		sub = seg[1]
	}
	switch {
	case sub == "hello" && r.Method == http.MethodPost:
		s.profilesHello(w, r)
	case sub == "bind" && r.Method == http.MethodPost:
		s.profilesBind(w, r)
	case sub == "" && r.Method == http.MethodGet:
		c := s.Profiles.Load()
		s.profilesOut(w, r, c, s.profileOf(r))
	case sub == "" && r.Method == http.MethodPost:
		s.profilesCreate(w, r)
	case sub != "" && len(seg) == 3 && seg[2] == "prefs" && r.Method == http.MethodPut:
		s.profilesPrefs(w, r, sub)
	case sub != "" && len(seg) == 2 && r.Method == http.MethodPut:
		s.profilesRename(w, r, sub)
	case sub != "" && len(seg) == 2 && r.Method == http.MethodDelete:
		s.profilesDelete(w, r, sub)
	default:
		fail(w, http.StatusNotFound, errf("没有这个接口"))
	}
}

// profilesOut 一份响应把前端要的都给全：名册、这台设备用哪一套、那一套的开关、
// 各设备绑在哪。前端拿一次就能把整块设置画出来，不用连着发三个请求。
func (s *Server) profilesOut(w http.ResponseWriter, r *http.Request, c profiles.Config, cur string) {
	writeJSON(w, 200, s.profilesBody(r, c, cur))
}

func (s *Server) profilesBody(r *http.Request, c profiles.Config, cur string) map[string]any {
	me := installID(r)
	type inst struct {
		ID       string `json:"id"`
		Label    string `json:"label,omitempty"`
		Profile  string `json:"profile"`
		LastSeen string `json:"lastSeen,omitempty"`
		Me       bool   `json:"me,omitempty"`
	}
	list := make([]inst, 0, len(c.Installs))
	for id, in := range c.Installs {
		it := inst{ID: id, Label: in.Label, Profile: in.Profile, Me: id == me}
		if !in.LastSeen.IsZero() {
			it.LastSeen = in.LastSeen.UTC().Format("2006-01-02T15:04:05Z")
		}
		list = append(list, it)
	}
	// 最近来过的排前面：设置面板里那一列是给「把手机那台改回去」用的，最近用的最可能是它
	sort.Slice(list, func(i, j int) bool { return list[i].LastSeen > list[j].LastSeen })

	p, _ := c.Get(cur)
	return map[string]any{
		"profiles": c.Profiles,
		"current":  cur,
		"prefs":    p.Prefs,
		"installs": list,
		"max":      profiles.MaxProfiles,
		"maxName":  profiles.MaxName,
	}
}

func (s *Server) profilesHello(w http.ResponseWriter, r *http.Request) {
	var body struct{ Kind string }
	if err := readJSON(r, &body); err != nil {
		fail(w, 400, err)
		return
	}
	// Label 从 User-Agent 猜，不收前端给的：这一列只是为了在设置面板里认出是哪台，
	// 而设备面板那一列用的就是这个函数 —— 同一台设备在两处显示成两个名字最让人犯疑。
	c, cur := s.Profiles.Hello(installID(r), body.Kind, auth.LabelFromUA(r.UserAgent()))
	s.profilesOut(w, r, c, cur)
}

func (s *Server) profilesBind(w http.ResponseWriter, r *http.Request) {
	var body struct{ Install, Profile string }
	if err := readJSON(r, &body); err != nil {
		fail(w, 400, err)
		return
	}
	// 不给 install 就是「这台」。给了就是在改**别人**那台 —— 手机上排布调坏了的时候，
	// 那台设备自己反而是最难操作的一台，所以这条路必须留着。
	target := strings.TrimSpace(body.Install)
	if target == "" {
		target = installID(r)
	}
	c, err := s.Profiles.Bind(target, strings.TrimSpace(body.Profile))
	if err != nil {
		fail(w, 400, err)
		return
	}
	s.profilesOut(w, r, c, c.Installs[installID(r)].Profile)
}

// profilesCreate 新建一套。CopyFrom 给了就把那一套的快捷键条 / 顶栏**复制**过来。
//
// 复制而不是继承：继承要给每一项做「跟随 / 覆盖」两态，那点复杂度这十来个开关撑不起，
// 而「从平板那套复制过来再删几个键」比从零拖快得多 —— 这也是这个功能最常用的动作。
func (s *Server) profilesCreate(w http.ResponseWriter, r *http.Request) {
	var body struct{ Name, Kind, CopyFrom string }
	if err := readJSON(r, &body); err != nil {
		fail(w, 400, err)
		return
	}
	c, p, err := s.Profiles.Create(body.Name, body.Kind)
	if err != nil {
		fail(w, 400, err)
		return
	}
	if from := strings.TrimSpace(body.CopyFrom); from != "" {
		// 复制失败不回滚：那一套已经建好了，读出来就是「退到默认那一套」（见 softkeys 的
		// pick）—— 比「建了一半又没了」好交代。出错原样报出来，人能再点一次。
		if err := s.Softkeys.Copy(from, p.ID); err != nil {
			fail(w, 400, err)
			return
		}
		if err := s.Topbar.Copy(from, p.ID); err != nil {
			fail(w, 400, err)
			return
		}
		if err := s.Profiles.CopyPrefs(from, p.ID); err != nil {
			fail(w, 400, err)
			return
		}
		// 名册被 CopyPrefs 改过了，重新读一份再回给前端（不然响应里那一套没有开关）
		c = s.Profiles.Load()
	}
	out := s.profilesBody(r, c, s.profileOf(r))
	// 把新建出来的 ID 也给出去：前端接着要「把这台设备绑过去」，靠名字反查是多一处会错的地方
	out["created"] = p.ID
	writeJSON(w, 200, out)
}

func (s *Server) profilesRename(w http.ResponseWriter, r *http.Request, id string) {
	var body struct{ Name string }
	if err := readJSON(r, &body); err != nil {
		fail(w, 400, err)
		return
	}
	c, err := s.Profiles.Rename(id, body.Name)
	if err != nil {
		fail(w, 400, err)
		return
	}
	s.profilesOut(w, r, c, s.profileOf(r))
}

// profilesDelete 删一套：名册先删（绑在上面的设备落回默认），然后把快捷键条 / 顶栏里
// 那一段清掉。
//
// 顺序是刻意的：名册是唯一的真相源，先把它改对。那两份里剩下的孤儿段只是占几行 JSON，
// 而反过来（先清数据、名册没删成）就是「点进去一片出厂配置」。
func (s *Server) profilesDelete(w http.ResponseWriter, r *http.Request, id string) {
	c, err := s.Profiles.Delete(id)
	if err != nil {
		fail(w, 400, err)
		return
	}
	// 那两份清不掉只记一句：名册已经改对了，孤儿段不影响任何人（下次同名 ID 不会再发出来）
	if err := s.Softkeys.Drop(id); err != nil {
		log.Printf("删 %s 的快捷键条那一段: %v", id, err)
	}
	if err := s.Topbar.Drop(id); err != nil {
		log.Printf("删 %s 的顶栏那一段: %v", id, err)
	}
	s.profilesOut(w, r, c, s.profileOf(r))
}

func (s *Server) profilesPrefs(w http.ResponseWriter, r *http.Request, id string) {
	var body struct {
		Prefs map[string]string `json:"prefs"`
	}
	if err := readJSON(r, &body); err != nil {
		fail(w, 400, err)
		return
	}
	p, err := s.Profiles.SetPrefs(id, body.Prefs)
	if err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"prefs": p.Prefs})
}

// installID 是这个浏览器自己生成的标识，挂在每个请求的 query 上（和 session 一样，
// 见前端 lib/api.ts 的 url()）。**不是 auth 的设备 ID**：本机直连压根没有那个，
// 而那正是桌面上最常见的情形（见 internal/profiles 的包注释）。
func installID(r *http.Request) string {
	v := strings.TrimSpace(r.URL.Query().Get("install"))
	if !profiles.ValidInstall(v) {
		return ""
	}
	return v
}

// profileOf 这个请求说的是哪一套排布：显式指定优先，否则按这台设备的绑定算。
//
// 显式指定是给编辑器用的：GET 的时候算出是哪一套，存的时候原样带回来 —— 中间要是有人
// 在别的设备上把这台的绑定改了，也不会静默存到另一套上去（存错地方完全看不出来）。
func (s *Server) profileOf(r *http.Request) string {
	if p := strings.TrimSpace(r.URL.Query().Get("profile")); p != "" {
		return p
	}
	return s.Profiles.Resolve(installID(r))
}
