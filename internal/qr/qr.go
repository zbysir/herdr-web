// Package qr 在终端里画二维码，手机扫一下就不用手打 token 了。
package qr

import qrcode "github.com/skip2/go-qrcode"

// 半块字符：一个字符表示上下两个模块，这样二维码宽高比才正常（终端字符是竖长的）。
const (
	blank = ' '
	upper = '▀'
	lower = '▄'
	full  = '█'
)

// Render 返回若干行，直接逐行 println 即可。失败返回 nil。
func Render(text string) []string {
	q, err := qrcode.New(text, qrcode.Low)
	if err != nil {
		return nil
	}
	// Bitmap 里 true = 黑模块。终端一般是亮底，所以「黑」用实心块画。
	bm := q.Bitmap()
	h := len(bm)
	if h == 0 {
		return nil
	}
	out := make([]string, 0, h/2+1)
	for y := 0; y < h; y += 2 {
		row := make([]rune, 0, len(bm[y]))
		for x := range bm[y] {
			top := bm[y][x]
			bot := y+1 < h && bm[y+1][x]
			switch {
			case top && bot:
				row = append(row, full)
			case top:
				row = append(row, upper)
			case bot:
				row = append(row, lower)
			default:
				row = append(row, blank)
			}
		}
		out = append(out, string(row))
	}
	return out
}
