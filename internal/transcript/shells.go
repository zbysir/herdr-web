package transcript

import (
	"bytes"
	"os"
)

// 「还有几个后台任务在跑」—— claude 自己那条状态行最后那截（`1 shell still running`）。
//
// # 判据：转录里两头都记着，不用读屏
//
// 一开始以为这个拿不到（它看着像 claude 的进程内状态），结论错了。转录里是有的：
//
//	启动   那次 Bash 的结果里带 `"backgroundTaskId": "b3jdz1im8"`
//	完成   后面会出现一行带 `<task-id>b3jdz1im8</task-id>` 的 task-notification
//	       （`type` 是 `queue-operation` / `attachment`，同一个 id 会出现好几次）
//
// 所以 **「还在跑」= 报过 id 但还没出现过对应的 task-notification**。在你说的那个会话上验过：
// 启动 1 个、通知 0 个 → 还在跑 1 个，和人看到的一致；另一个会话启动 1 个、通知 1 个 → 0。
//
// # 为什么必须扫整份，以及怎么让它不贵
//
// 后台任务可能是**很多轮之前**启动的（实测那个是一小时前起的），所以「这一轮」那个回扫窗口
// （见 turn.go）覆盖不到它 —— 只扫窗口的后果是**漏报**，而漏报看着就像这个功能没做。
//
// 全扫一遍不解析 JSON、只找两个子串，实测 8–12MB 的转录要 9–16ms。3 秒一拍不算贵，但它会
// 随转录长大，所以这儿是**增量**的：记住扫到哪个字节了，下一拍只扫新长出来的那段。
// 通知永远在启动之后，所以正着扫一趟就够，不用回头。
//
// # 一个已知的偏差
//
// claude 进程重启时在跑的后台任务会跟着死，而转录里永远等不到那条通知 —— 那时这儿会一直
// 报「还在跑」。实际影响很小：重启会换一个新 session（hook 会把新 id 报给 herdr），我们跟着
// 读的就是新那份转录，老的那个压根不在视野里。真被外面 kill 掉那种才会留下这个偏差。

// shellState 一份转录扫到哪儿了、见过哪些 id。
type shellState struct {
	at int64
	// fi 上次扫的那个文件本身（`os.SameFile` 比的是 dev+ino）。
	//
	// 作废的判据是两条：**换成了另一个文件**（新 inode —— 原子替换那种）或者**变短了**
	// （截断过）。只看后者不够：换上一份长度相近的新文件时旧账还留着，于是报出一个已经不
	// 存在的任务。
	//
	// 唯一盖不住的是「同一个 inode 上原地截断重写成别的内容」（`os.WriteFile` 就是这样）——
	// 那个便宜地检测不出来，而转录不会这样变：同一条会话纯 append，换会话就换一个路径。
	fi      os.FileInfo
	started map[string]bool
	done    map[string]bool
}

var (
	markStart = []byte(`"backgroundTaskId"`)
	// 完成那个标记有**两种写法**，都要认。
	//
	// 真机上的转录里是字面的 `<task-id>`，但那一行是别人的 JSON 编码器写的 —— Go 自己的
	// `json.Marshal` 默认就会把 `<` `>` 转义成 `<`（HTML 转义），别的实现同理。
	// 只认一种的后果是**静默的**：完成通知认不出来，那个任务就永远挂在「还在跑」上。
	// 多一遍子串查找的代价可以忽略，所以两种都认。
	markDone = []byte(`<task-id>`)
	// 注意这儿是**双引号**：要的是字面的反斜杠 + u003c 这六个字符，不是那个字符本身。
	markDoneEsc = []byte("\\u003ctask-id\\u003e")
)

// overlap 两块之间留多少字节，防标记正好被切开。
// 两个标记加上 id 都远短于这个数（`"backgroundTaskId": "xxxxxxxxx"` 也就三十几字节）。
const overlap = 128

// Shells 这个会话里还有几个后台任务在跑。算不出来给 0。
//
// codex 给 0：它的后台机制不是这一套（转录里没有这两个标记），而**编一个数比不显示更糟**。
func (s *Store) Shells(src Source) int {
	if s == nil || src.Agent != "claude" || src.Path == "" {
		return 0
	}
	f, err := os.Open(src.Path)
	if err != nil {
		return 0
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return 0
	}
	size := st.Size()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shells == nil {
		s.shells = map[string]*shellState{}
	}
	cur := s.shells[src.Path]
	// 不是同一个文件（换掉了）或者变短了（截断过）→ 手上那份记账作废，从头重扫。
	if cur == nil || size < cur.at || cur.fi == nil || !os.SameFile(cur.fi, st) {
		cur = &shellState{started: map[string]bool{}, done: map[string]bool{}}
		s.shells[src.Path] = cur
	}
	cur.fi = st
	if size > cur.at {
		from := cur.at - overlap
		if from < 0 {
			from = 0
		}
		if err := cur.eat(f, from, size); err == nil {
			cur.at = size
		}
	}

	n := 0
	for id := range cur.started {
		if !cur.done[id] {
			n++
		}
	}
	return n
}

// eat 扫 [from, to) 这一段，把见到的 id 记进去。
func (st *shellState) eat(f *os.File, from, to int64) error {
	if _, err := f.Seek(from, 0); err != nil {
		return err
	}
	buf := make([]byte, 1<<20)
	var tail []byte
	left := to - from
	for left > 0 {
		lim := int64(len(buf))
		if left < lim {
			lim = left
		}
		n, err := f.Read(buf[:lim])
		if n > 0 {
			left -= int64(n)
			chunk := append(tail, buf[:n]...)
			st.pick(chunk)
			if len(chunk) > overlap {
				tail = append(tail[:0:0], chunk[len(chunk)-overlap:]...)
			} else {
				tail = append(tail[:0:0], chunk...)
			}
		}
		if err != nil {
			break
		}
	}
	return nil
}

// pick 从一块字节里抠出两种 id。
//
// 故意**不解析 JSON**：这是为了便宜（一整份转录只做两遍子串查找）。代价是「标记出现在
// 别人的字符串里」也会被数进去 —— 比如这段注释本身要是进了转录，`"backgroundTaskId"`
// 就会被认成一次启动。所以 id 还要过一道形状检查（只认 `[a-z0-9]`，claude 那个 id 就是
// 这个形状），把复述文本里那种带引号嵌套、带空格的排掉。
func (st *shellState) pick(b []byte) {
	for i := 0; ; {
		j := bytes.Index(b[i:], markStart)
		if j < 0 {
			break
		}
		i += j + len(markStart)
		if id := quoted(b[i:]); id != "" {
			st.started[id] = true
		}
	}
	st.done2(b, markDone, '<')
	st.done2(b, markDoneEsc, '\\')
}

// done2 按一种标记写法扫一遍完成通知。sep 是 id 后面那个字符（字面是 `<`，转义形态是 `\`）。
func (st *shellState) done2(b, mark []byte, sep byte) {
	for i := 0; ; {
		j := bytes.Index(b[i:], mark)
		if j < 0 {
			return
		}
		i += j + len(mark)
		if id := until(b[i:], sep); id != "" {
			st.done[id] = true
		}
	}
}

// quoted 取 `: "xxx"` 里那个 xxx（跳过冒号和空白）。
func quoted(b []byte) string {
	k := 0
	for k < len(b) && (b[k] == ':' || b[k] == ' ' || b[k] == '\t') {
		k++
	}
	if k >= len(b) || b[k] != '"' {
		return ""
	}
	k++
	return until(b[k:], '"')
}

// until 取到分隔符之前那一截，并且**只认 id 该有的字符**。
func until(b []byte, sep byte) string {
	for k := 0; k < len(b) && k <= 64; k++ {
		if b[k] == sep {
			if k == 0 {
				return ""
			}
			return string(b[:k])
		}
		c := b[k]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return "" // 不是那个形状，当没看见（见 pick 的注释）
		}
	}
	return ""
}
