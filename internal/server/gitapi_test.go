package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zbysir/herdr-web/internal/config"
	"github.com/zbysir/herdr-web/internal/files"
	"github.com/zbysir/herdr-web/internal/gitdiff"
)

// 这几条只盯 HTTP 那一层：路由、参数、以及**关掉之后真的关上了**。
// git 到底吐出来什么在 internal/gitdiff 那边用真仓库验。

func gitServer(t *testing.T, on bool) (*Server, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("这台机器上没有 git")
	}
	dir := t.TempDir()
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-q", "-b", "main", ".")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-qm", "first")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\nB\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := &Server{Cfg: &config.Config{Files: true, Git: on}, Files: &files.Browser{Enabled: true}}
	if on {
		s.Git = gitdiff.New(s.Files)
	}
	return s, dir
}

func getGit(t *testing.T, s *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("GET", path, nil)
	w := httptest.NewRecorder()
	seg := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api"), "/"), "/")
	s.apiGit(w, r, seg)
	return w
}

func TestGitStatusAndDiff(t *testing.T) {
	s, dir := gitServer(t, true)

	w := getGit(t, s, "/api/git/status?dir="+dir)
	if w.Code != 200 {
		t.Fatalf("HTTP %d %s", w.Code, w.Body.String())
	}
	var st gitdiff.Status
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.Repo.Root != dir || len(st.Changes) != 1 || st.Changes[0].Path != "a.txt" {
		t.Fatalf("%+v", st)
	}

	w = getGit(t, s, "/api/git/diff?dir="+dir+"&path=a.txt")
	if w.Code != 200 {
		t.Fatalf("HTTP %d %s", w.Code, w.Body.String())
	}
	var p gitdiff.Patch
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Files) != 1 || p.Files[0].Add != 1 || p.Files[0].Del != 1 {
		t.Fatalf("%+v", p.Files)
	}
}

// 不是仓库 / 没说目录：错误码要分得开（前端据此决定是「换一个目录」还是「这条路没开」）。
func TestGitBadRequests(t *testing.T) {
	s, _ := gitServer(t, true)
	if w := getGit(t, s, "/api/git/status"); w.Code != 400 {
		t.Errorf("没带 dir：HTTP %d", w.Code)
	}
	if w := getGit(t, s, "/api/git/status?dir="+t.TempDir()); w.Code == 200 {
		t.Errorf("不是 git 仓库居然给了 200：%s", w.Body.String())
	}
	if w := getGit(t, s, "/api/git/nope?dir=/tmp"); w.Code != 404 {
		t.Errorf("不存在的接口：HTTP %d", w.Code)
	}
}

// HERDR_WEB_GIT=0 时三个口一律 404 —— 前端也据此不画那个按钮。
func TestGitOff(t *testing.T) {
	s, dir := gitServer(t, false)
	for _, p := range []string{"/api/git/status?dir=" + dir, "/api/git/diff?dir=" + dir + "&path=a.txt"} {
		if w := getGit(t, s, p); w.Code != http.StatusNotFound {
			t.Errorf("%s：HTTP %d（关掉了还答）", p, w.Code)
		}
	}
}

// 文件浏览关掉（HERDR_WEB_FILES=0）时 diff 也要跟着关：一份 diff 就是文件内容。
func TestGitFollowsFilesSwitch(t *testing.T) {
	s, dir := gitServer(t, true)
	s.Files.Enabled = false
	if w := getGit(t, s, "/api/git/status?dir="+dir); w.Code != http.StatusNotFound {
		t.Errorf("HERDR_WEB_FILES=0 时还能看 diff：HTTP %d %s", w.Code, w.Body.String())
	}
}

func TestGitRepos(t *testing.T) {
	s, dir := gitServer(t, true)
	q := "dir=" + url.QueryEscape(dir) + "&dir=" + url.QueryEscape(t.TempDir())
	r := httptest.NewRequest("GET", "/api/git/repos?"+q, nil)
	w := httptest.NewRecorder()
	s.apiGit(w, r, []string{"git", "repos"})
	if w.Code != 200 {
		t.Fatalf("HTTP %d %s", w.Code, w.Body.String())
	}
	var out struct{ Repos []gitdiff.Repo }
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	// 不是仓库的那个**不报错**，只是不出现
	if len(out.Repos) != 1 || out.Repos[0].Root != dir {
		t.Fatalf("%+v", out.Repos)
	}
}
