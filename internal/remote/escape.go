package remote

// escaper 是 ssh 那套「行首 `~.` 断开」：网络僵住时（锁屏、换网）按什么都没反应，
// 本地又在 raw 模式里，Ctrl+C 也只是一个发给对面的字节 —— 没有这条就只能关终端窗口。
//
//	行首 ~.   断开退出
//	行首 ~~   发一个 ~
//	行首 ~x   原样发 ~x（所以行首敲 `~/foo` 照常，只是 ~ 晚一个键才发出去）
//
// 「行首」= 刚发过回车（\r 或 \n），或者这条连接上还什么都没敲过。
type escaper struct {
	bol     bool // 在行首
	pending bool // 行首收到了一个 ~，等下一个键
}

func newEscaper() *escaper { return &escaper{bol: true} }

// feed 过滤一批输入，返回要发出去的字节，以及「用户要断开」。
func (e *escaper) feed(in []byte) (out []byte, quit bool) {
	out = make([]byte, 0, len(in)+1)
	for _, c := range in {
		if e.pending {
			e.pending = false
			switch c {
			case '.':
				return out, true
			case '~':
				out = append(out, '~')
				e.bol = false
				continue
			default:
				out = append(out, '~')
			}
		} else if e.bol && c == '~' {
			e.pending = true
			continue
		}
		out = append(out, c)
		e.bol = c == '\r' || c == '\n'
	}
	return out, false
}
