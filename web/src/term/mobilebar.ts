import type { Terminal } from '@xterm/xterm'

/**
 * herdr 移动端顶栏上那个 `switch` 按钮在屏幕的哪一块。
 *
 * 为什么要认出它：窄终端下（herdr 自己的 `ui.mobile_width_threshold`，默认 64 列）herdr
 * 换成单列布局，顶上两行是它的状态栏，**右上角贴着屏幕右缘**挂一个 `switch` 按钮，点开的是
 * **herdr 自己**那张切换面板（spaces / tabs / menu 一条竖列）。设置里开着「点 switch 开
 * 面板一览」时，这一下点击就不发给 herdr，改开我们自己那张（见 `components/PaneSwitcher`）。
 *
 * 认法是**从那个词顺着背景色把色块摊开**，不按坐标写死 —— 按钮多宽、从第几列开始都跟着终端
 * 宽度和布局走。50 列实测：色块 41–50 列 × 1–2 行（第 41 列那根 `│` 也是按钮的底色），
 * 字在第 2 行 43–48 列；34 列时色块是 26–34 列。
 *
 * **热区必须是整块，不能只认那六个字**：herdr 自己的热区就是整个色块 —— 实测在字**上面**
 * 那一行（色块的第一行、那儿一个字都没有）点一下，它的面板照样开。只认字的话有一半面积点
 * 下去还是 herdr 的面板，同一个按钮就有了两种行为。
 *
 * 两道防误伤：
 * - 那格的背景**不能是默认底色**：pane 里正好打出 `switch` 这个词（说明、代码、日志）到处
 *   都是，那不是按钮。
 * - 色块**不能从第 1 列开始**：herdr 自己那张面板开着时，标题栏左边也写着 `switch`，而标题
 *   那一整行是同一个底色（50 列实测 1–40 列一个色，右上角另有一个 `close` 块）—— 从第 1 列
 *   起的一律不认，不然「关掉 herdr 的面板」那一下会被我们接走。
 */

/** 状态栏是两行（实测）。多留一行余量，只用来限定搜索范围 —— 认不认还是看色块 */
const BAR_ROWS = 3
const LABEL = 'switch'

/** 这一格的背景色。默认底色返回空串 = 「这儿没有色块」 */
function bgAt(term: Terminal, col: number, row: number) {
  const buf = term.buffer.active
  const cell = buf.getLine(buf.viewportY + row - 1)?.getCell(col - 1)
  if (!cell || cell.isBgDefault()) return ''
  return `${cell.getBgColorMode()}:${cell.getBgColor()}`
}

/**
 * 这一行从 col 起是不是 `switch` 这个词。
 *
 * 一格一格比，不用 `translateToString` 再 `indexOf`：CJK 宽字符占两格，字符串下标和列号
 * 会错开（tab 名是中文时前面就差了几列），而我们要的正是列号。
 */
function labelAt(term: Terminal, col: number, row: number) {
  const line = term.buffer.active.getLine(term.buffer.active.viewportY + row - 1)
  if (!line) return false
  for (let k = 0; k < LABEL.length; k++) {
    if (line.getCell(col - 1 + k)?.getChars() !== LABEL[k]) return false
  }
  return true
}

/** 那个按钮的色块，列 / 行都是 1 起、闭区间；顶栏上没有这个按钮就 null */
export function findSwitchButton(term: Terminal) {
  for (let row = 1; row <= Math.min(BAR_ROWS, term.rows); row++) {
    for (let col = 1; col + LABEL.length - 1 <= term.cols; col++) {
      if (!labelAt(term, col, row)) continue
      const bg = bgAt(term, col, row)
      if (!bg) continue
      let c0 = col
      let c1 = col + LABEL.length - 1
      while (c0 > 1 && bgAt(term, c0 - 1, row) === bg) c0--
      while (c1 < term.cols && bgAt(term, c1 + 1, row) === bg) c1++
      if (c0 === 1) continue // 整行同底 = herdr 自己那张面板的标题栏，不是按钮
      let r0 = row
      let r1 = row
      while (r0 > 1 && bgAt(term, col, r0 - 1) === bg) r0--
      while (r1 < Math.min(BAR_ROWS + 1, term.rows) && bgAt(term, col, r1 + 1) === bg) r1++
      return { c0, c1, r0, r1 }
    }
  }
  return null
}

/** 这一下点击落在那个按钮上了吗 */
export function hitsSwitchButton(term: Terminal, col: number, row: number) {
  // 先按行早退：点击绝大多数落在下面的 pane 里，那时候一格都不用扫
  if (row > BAR_ROWS + 1) return false
  const b = findSwitchButton(term)
  return !!b && col >= b.c0 && col <= b.c1 && row >= b.r0 && row <= b.r1
}
