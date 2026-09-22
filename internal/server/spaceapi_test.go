package server

import (
	"bufio"
	"encoding/json"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/zbysir/herdr-web/internal/config"
	"github.com/zbysir/herdr-web/internal/herdr"
	"github.com/zbysir/herdr-web/internal/outbox"
)

// 这一份假 herdr 答的是**工作空间那一层**（列 / 切 / 新开），并且把收到的调用记下来。
//
// 和 chatapi_test 里那个分开写：那个的 default 分支是「不认识就报错」，而这一层要答
// workspace.list / tab.list / agent.list —— 混进去会让两边的用例互相拌蒜。
type spaceCalls struct {
	mu   sync.Mutex
	list []spaceCall
}

type spaceCall struct {
	Method string
	Params map[string]any
}

func (c *spaceCalls) add(m string, p map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.list = append(c.list, spaceCall{Method: m, Params: p})
}

func (c *spaceCalls) of(method string) []spaceCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []spaceCall
	for _, x := range c.list {
		if x.Method == method {
			out = append(out, x)
		}
	}
	return out
}

func fakeSpaceHerdr(t *testing.T, calls *spaceCalls) string {
	t.Helper()
	// /tmp 而不是 t.TempDir()：unix socket 路径有 104 字节上限（同 fakeHerdrKeys）
	dir, err := os.MkdirTemp("/tmp", "spaceapi")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "h.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })

	panes := []map[string]any{
		{"pane_id": "w1:p1", "workspace_id": "w1", "tab_id": "w1:t1", "focused": true, "agent": "claude", "agent_status": "blocked", "cwd": "/a"},
		{"pane_id": "w2:p1", "workspace_id": "w2", "tab_id": "w2:t1", "agent": "codex", "agent_status": "idle", "cwd": "/b"},
	}
	workspaces := []map[string]any{
		{"workspace_id": "w1", "number": 1, "label": "甲", "focused": true, "pane_count": 1, "tab_count": 1, "active_tab_id": "w1:t1", "agent_status": "blocked"},
		{"workspace_id": "w2", "number": 2, "label": "", "pane_count": 1, "tab_count": 1, "active_tab_id": "w2:t1", "agent_status": "idle"},
	}
	tabs := []map[string]any{
		{"tab_id": "w1:t1", "workspace_id": "w1", "number": 1, "label": "一", "focused": true, "pane_count": 1},
		{"tab_id": "w2:t1", "workspace_id": "w2", "number": 1, "label": "", "pane_count": 1},
	}

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				line, err := bufio.NewReader(c).ReadBytes('\n')
				if err != nil {
					return
				}
				var req struct {
					ID     string         `json:"id"`
					Method string         `json:"method"`
					Params map[string]any `json:"params"`
				}
				json.Unmarshal(line, &req)
				calls.add(req.Method, req.Params)
				var result any
				switch req.Method {
				case "pane.list":
					result = map[string]any{"panes": panes}
				case "workspace.list":
					result = map[string]any{"workspaces": workspaces}
				case "tab.list":
					result = map[string]any{"tabs": tabs}
				case "agent.list":
					result = map[string]any{"agents": []map[string]any{}}
				case "workspace.focus", "tab.focus":
					result = map[string]any{"type": "ok"}
				case "workspace.create":
					result = map[string]any{"workspace": map[string]any{"workspace_id": "w9"}}
				case "tab.create":
					result = map[string]any{"tab": map[string]any{"tab_id": "w1:t9"}}
				default:
					json.NewEncoder(c).Encode(map[string]any{"id": req.ID, "error": map[string]any{"message": "unexpected " + req.Method}})
					return
				}
				json.NewEncoder(c).Encode(map[string]any{"id": req.ID, "result": result})
			}(conn)
		}
	}()
	return sock
}

func spaceServer(t *testing.T) (*Server, *spaceCalls) {
	t.Helper()
	calls := &spaceCalls{}
	sock := fakeSpaceHerdr(t, calls)
	s := &Server{Cfg: &config.Config{}, sess: map[string]*live{}}
	s.def = &live{socket: sock, outbox: &outbox.Outbox{C: herdr.New(sock)}}
	return s, calls
}

func hitHerdr(t *testing.T, s *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r = httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		r.Header.Set("content-type", "application/json")
	}
	w := httptest.NewRecorder()
	seg := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api"), "/"), "/")
	s.apiHerdr(w, r, seg)
	return w
}

// 工作空间那一层跟着 panes 那一拍一起给 —— **不另开一个口**，因为数据来自同一批调用，
// 而这是按秒轮询的一拍（多一个口就是在跑着 agent 的那台机器上多问一遍同样的东西）。
func TestPanesCarriesSpaces(t *testing.T) {
	s, calls := spaceServer(t)
	w := hitHerdr(t, s, "GET", "/api/herdr/panes", "")
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var got struct {
		Spaces []outbox.Space `json:"spaces"`
		Panes  []outbox.Target
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Spaces) != 2 {
		t.Fatalf("该有两个工作空间，拿到 %+v", got.Spaces)
	}
	if got.Spaces[0].Label != "甲" || !got.Spaces[0].Focused || got.Spaces[0].Status != "blocked" {
		t.Errorf("第一个工作空间不对：%+v", got.Spaces[0])
	}
	// 没标签的退回 wN（和 pane 那边的 orElse 一个规矩 —— 空着的话界面上是个没名字的格子）
	if got.Spaces[1].Label != "w2" {
		t.Errorf("没标签该退回 w2，拿到 %q", got.Spaces[1].Label)
	}
	if got.Spaces[0].Panes != 1 || got.Spaces[0].Tabs != 1 {
		t.Errorf("数量没带上：%+v", got.Spaces[0])
	}
	// 每样只问一次：这一拍是 1.5 秒一次，多问一遍就是在跑着 agent 的机器上白跑一趟
	for _, m := range []string{"pane.list", "workspace.list", "tab.list"} {
		if n := len(calls.of(m)); n != 1 {
			t.Errorf("%s 该只问一次，问了 %d 次", m, n)
		}
	}
}

// 切过去：**两个 id 显式分开**，不靠形状猜（herdr 里 `w5` 和 `w5:t3` 长得不一样，
// 但猜错就是切到别处，而屏幕上看着完全正常）。
func TestSpaceFocus(t *testing.T) {
	for _, tc := range []struct {
		why, body, want, wantID string
	}{
		{"切工作空间", `{"workspace":"w2"}`, "workspace.focus", "w2"},
		{"切 tab", `{"tab":"w2:t1"}`, "tab.focus", "w2:t1"},
		{"两个都给：tab 优先（更具体）", `{"workspace":"w1","tab":"w2:t1"}`, "tab.focus", "w2:t1"},
	} {
		s, calls := spaceServer(t)
		w := hitHerdr(t, s, "POST", "/api/herdr/space", tc.body)
		if w.Code != 200 {
			t.Errorf("%s：%d %s", tc.why, w.Code, w.Body.String())
			continue
		}
		got := calls.of(tc.want)
		if len(got) != 1 {
			t.Errorf("%s：该调一次 %s，实际 %+v", tc.why, tc.want, calls.list)
			continue
		}
		key := "workspace_id"
		if tc.want == "tab.focus" {
			key = "tab_id"
		}
		if got[0].Params[key] != tc.wantID {
			t.Errorf("%s：该切到 %q，实际 %+v", tc.why, tc.wantID, got[0].Params)
		}
	}
}

// 新开一个：**建完就过去**（focus:true）—— 从手机上新开就是为了去那儿干活。
func TestSpaceNew(t *testing.T) {
	for _, tc := range []struct{ why, body, want string }{
		{"新 tab（指定工作空间和 cwd）", `{"new":"tab","workspace":"w1","cwd":"/a"}`, "tab.create"},
		{"新工作空间", `{"new":"workspace","cwd":"/b","label":"乙"}`, "workspace.create"},
	} {
		s, calls := spaceServer(t)
		w := hitHerdr(t, s, "POST", "/api/herdr/space", tc.body)
		if w.Code != 200 {
			t.Errorf("%s：%d %s", tc.why, w.Code, w.Body.String())
			continue
		}
		got := calls.of(tc.want)
		if len(got) != 1 {
			t.Errorf("%s：该调一次 %s，实际 %+v", tc.why, tc.want, calls.list)
			continue
		}
		if got[0].Params["focus"] != true {
			t.Errorf("%s：新开的要 focus:true（建完就过去），实际 %+v", tc.why, got[0].Params)
		}
	}
}

// 认不出的动作一律拒 —— 这个口只发得出那四种形状，别让它变成「往 herdr 发任意方法」。
func TestSpaceRejectsUnknown(t *testing.T) {
	for _, body := range []string{`{}`, `{"new":"pane"}`, `{"new":"close"}`} {
		s, calls := spaceServer(t)
		if w := hitHerdr(t, s, "POST", "/api/herdr/space", body); w.Code == 200 {
			t.Errorf("%s 该被拒，却回了 200", body)
		}
		if len(calls.list) != 0 {
			t.Errorf("%s 被拒之前不该打 herdr：%+v", body, calls.list)
		}
	}
}
