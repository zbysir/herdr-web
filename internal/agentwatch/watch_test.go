package agentwatch

import "testing"

// 补发的事件不是变化。这条是踩出来的：订阅一建立 herdr 会补发旧事件，把它当变化的话
// 一次重订阅就能把所有 pane 的时间刷成「刚刚」—— 那是编时间。
func TestRecordSkipsSameStatus(t *testing.T) {
	w := New("/nonexistent.sock")
	w.seed(map[string]string{"p1": "idle"})
	if got := w.At("p1"); got != 0 {
		t.Fatalf("第一次见到的 pane 不该有时间（它上次变化是本进程起来之前的事），got %d", got)
	}

	w.record("p1", "idle") // 补发：状态没变
	if got := w.At("p1"); got != 0 {
		t.Errorf("状态没变不该记时间，got %d", got)
	}

	w.record("p1", "working") // 真的变了
	first := w.At("p1")
	if first == 0 {
		t.Fatal("状态变了应当记下时间")
	}

	w.record("p1", "working") // 又补发一次同样的
	if w.At("p1") != first {
		t.Error("同一个状态再来一次不该刷新时间")
	}
}

// 两轮订阅之间变的状态，对底时要补上时间（那中间只隔几百毫秒，按刚刚算）。
func TestSeedStampsWhenStatusMoved(t *testing.T) {
	w := New("/nonexistent.sock")
	w.seed(map[string]string{"p1": "idle"})
	w.seed(map[string]string{"p1": "blocked"})
	if w.At("p1") == 0 {
		t.Error("对底时发现状态和上次不一样，应当记下时间")
	}
}

// 没在盯的时候 At 给 0，调用方（outbox）拿它当「不知道」。
func TestAtUnknown(t *testing.T) {
	w := New("/nonexistent.sock")
	if w.At("没这个 pane") != 0 {
		t.Error("没记过的 pane 应当给 0")
	}
	if w.Live() {
		t.Error("还没订上就不该说自己 live")
	}
}
