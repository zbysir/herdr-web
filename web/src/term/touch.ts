// 触屏手势：滑动 = 滚轮上报，单击 = 发给程序但不弹键盘，双击 = 切键盘，
// 长按 = 抓住（按下左键不放），之后滑动就是拖 —— herdr 的「拖 pane 边框改大小」靠这条。
//
// 为什么要自己写：xterm.js 只把 wheel 事件翻译成鼠标上报，完全不管 touch。像 herdr
// 这样「占着备用屏幕（本地没 scrollback 可滚）+ 开了鼠标上报」的程序，手机上手指划了
// 等于没划 —— 两头都不响应。而点击和长按又都会落到隐藏 textarea 上，浏览器顺手就把
// 键盘顶出来；在 TUI 里十次里九次只是想点个 pane，不是想打字。
import type { Terminal } from '@xterm/xterm'

interface Hooks {
  send: (d: string) => void
  toggleKeyboard: () => void
}

interface TouchState {
  x: number; y: number; x0: number; y0: number
  acc: number; at: number; owned: boolean
  /** 长按计时器：到点了就「抓住」 */
  hold: ReturnType<typeof setTimeout> | undefined
  /** 抓住之后左键一直按着，这里记上次上报的格子 */
  drag: { col: number; row: number } | null
}

// 长按多久算抓住。再短会和「想滑一下」打架，再长手指容易先动
const HOLD_MS = 380

/**
 * 是不是画框线的字符（U+2500–U+259F：制表符 + 方块元素）。
 *
 * herdr 的 pane 边框就是这批字符。用它做**吸附**：手指按下的位置差一格也算抓到边框，
 * 一格宽的竖线光靠手指精度是抓不住的。只吸附、不改变「按下」这个动作本身，所以猜错
 * 了也就是普通的一次拖动，不会造成别的后果。
 */
const isBorderGlyph = (ch: string) => {
  const c = ch.codePointAt(0) ?? 0
  return c >= 0x2500 && c <= 0x259f
}

export function attachTouch(host: HTMLElement, term: Terminal, hooks: Hooks): () => void {
  const screenEl = () => host.querySelector('.xterm-screen') as HTMLElement | null

  const cellBox = () => {
    const el = screenEl()
    if (!el || !term.rows || !term.cols) return null
    const r = el.getBoundingClientRect()
    return { r, w: r.width / term.cols, h: r.height / term.rows }
  }

  // 鼠标上报开着的时候，滚动得发给程序；否则滚本地 scrollback
  const mouseReporting = () => term.modes.mouseTrackingMode !== 'none'

  const cellAt = (x: number, y: number, box: NonNullable<ReturnType<typeof cellBox>>) => ({
    col: Math.min(term.cols, Math.max(1, Math.floor((x - box.r.left) / box.w) + 1)),
    row: Math.min(term.rows, Math.max(1, Math.floor((y - box.r.top) / box.h) + 1)),
  })

  let touch: TouchState | null = null
  let lastTapAt = 0

  const glyphAt = (col: number, row: number) => {
    const buf = term.buffer.active
    return buf.getLine(buf.viewportY + row - 1)?.getCell(col - 1)?.getChars() ?? ''
  }

  /** 手指按下的位置附近有边框就吸过去（先看左右，再看上下） */
  const snapToBorder = (col: number, row: number) => {
    for (const [dc, dr] of [[0, 0], [-1, 0], [1, 0], [0, -1], [0, 1]] as const) {
      const c = col + dc
      const r = row + dr
      if (c < 1 || c > term.cols || r < 1 || r > term.rows) continue
      if (isBorderGlyph(glyphAt(c, r))) return { col: c, row: r }
    }
    return { col, row }
  }

  // SGR 鼠标（1006）编码：0 = 左键按下，32 = 按着左键移动，末尾 m = 松开。
  // herdr 常驻开着 1002/1003/1006，所以这里直接按 SGR 发。
  const grab = () => {
    const box = cellBox()
    if (!touch || !box) return
    const under = cellAt(touch.x, touch.y, box)
    const at = snapToBorder(under.col, under.row)
    touch.drag = at
    hooks.send(`\x1b[<0;${at.col};${at.row}M`)
    navigator.vibrate?.(12) // 有振动的机器上给一下「抓住了」的反馈
  }

  const release = (st: TouchState) => {
    if (!st.drag) return
    hooks.send(`\x1b[<0;${st.drag.col};${st.drag.row}m`)
    st.drag = null
  }

  const onStart = (e: TouchEvent) => {
    if (e.touches.length !== 1) {
      // 第二根手指落下：拖动到此结束，别把左键留在按下的状态
      if (touch) { clearTimeout(touch.hold); release(touch) }
      touch = null
      return
    }
    const t = e.touches[0]
    touch = {
      x: t.clientX, y: t.clientY, x0: t.clientX, y0: t.clientY,
      acc: 0, at: Date.now(), owned: mouseReporting(),
      hold: undefined, drag: null,
    }
    // 有程序在收鼠标上报（herdr 这种）时独占手势：不让浏览器聚焦隐藏的 textarea
    // （点一下就弹键盘的根源）、不让长按弹出选择气泡、也不补发兼容鼠标事件。
    if (touch.owned && e.cancelable) e.preventDefault()
    // 只在有人收鼠标上报时才做长按抓取：普通 shell 里拖一下没有任何意义
    if (touch.owned) touch.hold = setTimeout(grab, HOLD_MS)
  }

  const onMove = (e: TouchEvent) => {
    if (!touch || e.touches.length !== 1) return
    const box = cellBox()
    if (!box) return
    const t = e.touches[0]

    // 抓住之后：这一路都是拖动，不再滚屏
    if (touch.drag) {
      if (e.cancelable) e.preventDefault()
      touch.x = t.clientX
      touch.y = t.clientY
      const at = cellAt(t.clientX, t.clientY, box)
      if (at.col !== touch.drag.col || at.row !== touch.drag.row) {
        touch.drag = at
        hooks.send(`\x1b[<32;${at.col};${at.row}M`)
      }
      return
    }
    // 手指动了就不算长按了（不撤的话滑到一半会突然被抓住）
    if (Math.abs(t.clientY - touch.y0) > 8 || Math.abs(t.clientX - touch.x0) > 8) {
      clearTimeout(touch.hold)
    }

    touch.acc += t.clientY - touch.y
    touch.y = t.clientY

    // 没独占的时候（普通 shell），越过阈值再吃掉默认行为，避免页面橡皮筋
    if (!touch.owned && Math.abs(touch.y - touch.y0) > 6 && e.cancelable) e.preventDefault()

    const steps = Math.trunc(touch.acc / box.h)
    if (!steps) return
    touch.acc -= steps * box.h

    const dir = steps > 0 ? -1 : 1 // 手指下滑 = 看更早的内容 = 往上滚
    const n = Math.min(Math.abs(steps), 8)
    if (mouseReporting()) {
      const { col, row } = cellAt(touch.x, t.clientY, box)
      const btn = dir < 0 ? 64 : 65 // SGR：64 上滚，65 下滚
      for (let i = 0; i < n; i++) hooks.send(`\x1b[<${btn};${col};${row}M`)
    } else {
      term.scrollLines(dir * n)
    }
  }

  const onEnd = () => {
    const tc = touch
    touch = null
    if (!tc) return
    clearTimeout(tc.hold)
    // 拖动结束必须发松开：不发的话 herdr 那边左键一直是按着的
    if (tc.drag) { release(tc); return }
    if (Math.abs(tc.y - tc.y0) > 8 || Math.abs(tc.x - tc.x0) > 8) return // 是滑动，已经处理过了

    const now = Date.now()
    const isDouble = now - lastTapAt < 320
    lastTapAt = now

    if (isDouble) return hooks.toggleKeyboard() // 双击 = 显示 / 收起键盘
    // 长按什么都不做（重点是别弹键盘）。有鼠标上报的时候长按已经在上面被「抓住」接走了，
    // 走到这儿只剩普通 shell，以及抓取还没到点就松手的情况。
    if (now - tc.at > 500) return

    if (tc.owned) {
      const box = cellBox()
      if (!box) return
      const { col, row } = cellAt(tc.x, tc.y, box)
      hooks.send(`\x1b[<0;${col};${row}M`)      // 单击照样发给程序，只是不抢焦点
      hooks.send(`\x1b[<0;${col};${row}m`)
    } else {
      term.focus()                              // 普通 shell 里点一下就是想打字
    }
  }

  host.addEventListener('touchstart', onStart, { passive: false })
  host.addEventListener('touchmove', onMove, { passive: false })
  host.addEventListener('touchend', onEnd, { passive: true })
  host.addEventListener('touchcancel', onEnd, { passive: true })
  return () => {
    host.removeEventListener('touchstart', onStart)
    host.removeEventListener('touchmove', onMove)
    host.removeEventListener('touchend', onEnd)
    host.removeEventListener('touchcancel', onEnd)
  }
}
