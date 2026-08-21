package agentwatch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zbysir/herdr-web/internal/herdr"
)

func pane(id, term, status string) herdr.Pane {
	return herdr.Pane{PaneID: id, TerminalID: term, Agent: "claude", AgentStatus: status}
}

// 补发的事件不是变化。这条是踩出来的：订阅一建立 herdr 会补发旧事件，把它当变化的话
// 一次重订阅就能把所有 pane 的时间刷成「刚刚」—— 那是编时间。
func TestObserveSkipsSameStatus(t *testing.T) {
	w := New("/nonexistent.sock", "")
	w.observe(pane("p1", "term1", "idle"))
	if got := w.At("p1"); got != 0 {
		t.Fatalf("第一次见到的 pane 不该有时间（它上次变化是本进程起来之前的事），got %d", got)
	}

	w.observe(pane("p1", "term1", "idle")) // 补发：状态没变
	if got := w.At("p1"); got != 0 {
		t.Errorf("状态没变不该记时间，got %d", got)
	}

	w.observe(pane("p1", "term1", "working")) // 真的变了
	first := w.At("p1")
	if first == 0 {
		t.Fatal("状态变了应当记下时间")
	}

	w.observe(pane("p1", "term1", "working")) // 又补发一次同样的
	if w.At("p1") != first {
		t.Error("同一个状态再来一次不该刷新时间")
	}
}

// 两轮订阅之间变的状态，对底时要补上时间（那中间只隔几百毫秒，按刚刚算）。
func TestObserveStampsWhenStatusMoved(t *testing.T) {
	w := New("/nonexistent.sock", "")
	w.observe(pane("p1", "term1", "idle"))
	w.observe(pane("p1", "term1", "blocked"))
	if w.At("p1") == 0 {
		t.Error("对底时发现状态和上次不一样，应当记下时间")
	}
}

// 没在盯的时候 At 给 0，调用方（outbox）拿它当「不知道」。
func TestAtUnknown(t *testing.T) {
	w := New("/nonexistent.sock", "")
	if w.At("没这个 pane") != 0 {
		t.Error("没记过的 pane 应当给 0")
	}
	if w.Live() {
		t.Error("还没订上就不该说自己 live")
	}
}

// 落盘是按 **terminal_id** 存的：herdr-web 自己重启（升级、改配置）是常事，一重启
// 时间列全空看着就像坏了。而 pane_id 会被重新分配，拿它当 key 会张冠李戴。
func TestPersistsByTerminalID(t *testing.T) {
	file := filepath.Join(t.TempDir(), "seen.json")

	w := New("/nonexistent.sock", file)
	w.observe(pane("w1:pA", "term_A", "idle"))
	w.observe(pane("w1:pA", "term_A", "working"))
	want := w.At("w1:pA")
	if want == 0 {
		t.Fatal("状态变了应当记下时间")
	}
	w.save()

	// 「重启一次」：同一个终端在 herdr 里换了个位置编号，时间要还认得
	w2 := New("/nonexistent.sock", file)
	w2.observe(pane("w9:pZ", "term_A", "working"))
	w2.observe(pane("w9:pQ", "term_NEW", "idle"))
	if got := w2.At("w9:pZ"); got != want {
		t.Errorf("同一个终端换了 pane_id，时间应当还在：want %d got %d", want, got)
	}
	if got := w2.At("w9:pQ"); got != 0 {
		t.Errorf("没见过的终端不该有时间，got %d", got)
	}

	// 存盘只留还在的终端：herdr 重启之后终端 id 全是新的，旧记录留着只会让文件长胖
	w2.observe(pane("w9:pQ", "term_NEW", "blocked"))
	w2.save()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var snap snapshot
	if err := json.Unmarshal(b, &snap); err != nil {
		t.Fatal(err)
	}
	if _, ok := snap.At["term_A"]; !ok {
		t.Error("还在的终端应当留在存盘里")
	}
	if len(snap.At) != 2 {
		t.Errorf("存盘里应当只有这会儿还在的两个终端，got %v", snap.At)
	}
}

// 状态**不**落盘：重启之后拿旧状态去比，会把「停机期间变的」记成「刚刚变的」。
func TestStatusNotPersisted(t *testing.T) {
	file := filepath.Join(t.TempDir(), "seen.json")
	w := New("/nonexistent.sock", file)
	w.observe(pane("w1:pA", "term_A", "idle"))
	w.observe(pane("w1:pA", "term_A", "working"))
	want := w.At("w1:pA")
	w.save()

	w2 := New("/nonexistent.sock", file)
	if w2.At("w1:pA") != 0 {
		t.Fatal("还没对底（term 映射还是空的）就不该拿到时间")
	}
	// 停机期间这个终端变成 blocked 了。存盘里没有状态，所以对底时看不出「变过」——
	// 时间必须**原样**留着，不能被刷成现在。刷了就是把停机期间的变化说成刚刚发生的。
	time.Sleep(2 * time.Millisecond)
	w2.observe(pane("w1:pA", "term_A", "blocked"))
	if got := w2.At("w1:pA"); got != want {
		t.Errorf("时间被改写了：want %d got %d（差 %dms）", want, got, got-want)
	}
}
