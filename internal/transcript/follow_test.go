package transcript

import (
	"os"
	"path/filepath"
	"testing"
)

// 前台 `/resume` 一个后台会话：pane 报的那份只剩几行 + continued-in，而 continued-in 指的
// 那个 id 没有文件（是前台自己被挂起的 job），真对话在「running in the background (job)」
// 那个后台会话里。按真机那份（claude 2.1.309）的形状造的。
func TestFollowResumedBackground(t *testing.T) {
	s := store(t)
	dir := filepath.Join(s.ClaudeRoot, "-Users-x-portai")
	os.MkdirAll(dir, 0o700)
	front := "8305fe03-3ab4-4443-bdbe-19573df33555"
	bg := "2219da0d-2010-43ce-980d-b8d28ef862f2"
	os.WriteFile(filepath.Join(dir, front+".jsonl"), []byte(
		`{"type":"system","subtype":"local_command","content":"<local-command-stdout>Opening \"TOB\", running in the background (2219da0d)</local-command-stdout>","cwd":"/Users/x/portai"}`+"\n"+
			`{"type":"continued-in","sessionId":"`+front+`","continuedInSessionId":"c0885d2a-5fbc-48d5-8f1c-4aed5365b6bd"}`+"\n"), 0o600)
	os.WriteFile(filepath.Join(dir, bg+".jsonl"), []byte(`{"type":"user","cwd":"/Users/x/portai"}`+"\n"), 0o600)
	got, err := s.Find(Ref{Agent: "claude", Kind: "id", Value: front, CWD: "/Users/x/portai"})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got.Path) != bg+".jsonl" || got.Sig != bg {
		t.Fatalf("该跟到后台会话：%s sig=%s", got.Path, got.Sig)
	}
}

// continued-in 指的 id 有文件时直接过去；没有 continued-in 的照旧。
func TestFollowContinuedIn(t *testing.T) {
	s := store(t)
	dir := filepath.Join(s.ClaudeRoot, "-p")
	os.MkdirAll(dir, 0o700)
	a, b := "aaaaaaaa-0000-0000-0000-000000000000", "bbbbbbbb-0000-0000-0000-000000000000"
	os.WriteFile(filepath.Join(dir, a+".jsonl"), []byte(`{"type":"continued-in","continuedInSessionId":"`+b+`"}`+"\n"), 0o600)
	os.WriteFile(filepath.Join(dir, b+".jsonl"), []byte(`{"type":"user"}`+"\n"), 0o600)
	got, err := s.Find(Ref{Agent: "claude", Kind: "id", Value: a})
	if err != nil || got.Sig != b {
		t.Fatalf("没跟过去：%v %+v", err, got)
	}
	got, _ = s.Find(Ref{Agent: "claude", Kind: "id", Value: b})
	if got.Sig != b {
		t.Fatalf("没搬走的不该动：%+v", got)
	}
}
