package server

import "testing"

// revOf 的两条性质。第二条是这一处**唯一**的静默失败方式：内容变了而指纹没变，
// 表现是前端一直收到「没变」—— 面板从此永久冻住，而且一个字都不报。
func TestRevOf(t *testing.T) {
	body := func() map[string]any {
		return map[string]any{
			"panes": []map[string]any{
				{"id": "w1:p1", "agent": "claude", "status": "idle", "seq": 7},
				{"id": "w1:p2", "agent": "", "status": "unknown"},
			},
			"watching": true, "session": "", "socket": "/tmp/h.sock",
		}
	}

	// ① 同样的内容一定同样的指纹。map 的 key 在 encoding/json 里是排过序的，所以这条成立；
	//    真要是不成立，表现是每拍都发全量（也就是没有这一档时的行为），不会出错。
	a, b := revOf(body()), revOf(body())
	if a != b || a == "" {
		t.Fatalf("同样的内容指纹不一样：%q vs %q", a, b)
	}

	// ② 任何一处变了指纹就得变。这几处覆盖了「状态变了」「换了焦点」「盯没盯着」
	//    「多了一个 pane」四类真实变化。
	for _, tc := range []struct {
		name string
		mut  func(m map[string]any)
	}{
		{"状态变了", func(m map[string]any) {
			m["panes"].([]map[string]any)[0]["status"] = "working"
		}},
		{"计数往前走了", func(m map[string]any) {
			m["panes"].([]map[string]any)[0]["seq"] = 8
		}},
		{"换了焦点", func(m map[string]any) {
			m["panes"].([]map[string]any)[1]["focused"] = true
		}},
		{"订阅断了", func(m map[string]any) { m["watching"] = false }},
		{"多了一个 pane", func(m map[string]any) {
			m["panes"] = append(m["panes"].([]map[string]any), map[string]any{"id": "w1:p3"})
		}},
	} {
		m := body()
		tc.mut(m)
		if got := revOf(m); got == a {
			t.Errorf("%s：指纹没变（%q）—— 前端会一直以为没变", tc.name, got)
		}
	}
}
