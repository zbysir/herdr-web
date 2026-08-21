// 触屏手势：滑动 = 滚轮上报，单击 = 发给程序但不弹键盘，双击 = 切键盘，
// 长按 = 抓住（按下左键不放），之后滑动就是拖。
// **手指落在 pane 分界线附近时不用长按**，直接就是拖 —— 那条线是 herdr 的边框，
// 在上面滑动本来也没别的含义，抓住就拉正是桌面上鼠标的做法。
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
  /**
   * 按下的时刻，取的是**事件自带的时间戳**（`e.timeStamp`）而不是处理函数里的
   * `Date.now()`。
   *
   * 差别很要紧：终端正忙着重绘的时候（herdr 刷屏、连上时那一大坨输出）JS 定时器
   * 和事件派发都会被推后，实测一次 60ms 的点击在处理函数里量出来是 994ms —— 于是
   * 被「超过 500ms 算长按，什么都不做」那条挡掉，表现就是「输出一多点哪儿都没反应」。
   * 事件时间戳是事件**产生**时打的，不受处理延迟影响。
   */
  at: number
  acc: number; owned: boolean
  /** 长按计时器：到点了就「抓住」 */
  hold: ReturnType<typeof setTimeout> | undefined
  /** 抓住之后左键一直按着，这里记上次上报的格子 */
  drag: { col: number; row: number } | null
}

// 长按多久算抓住。再短会和「想滑一下」打架，再长手指容易先动
const HOLD_MS = 380
/**
 * 长按期间允许手指晃多少 px。
 *
 * 原来是 8px，太苛刻 —— 平板上按住不动的手指本来就会飘十几个 px，一飘长按就被
 * 撤了，表现就是「长按根本没反应」。
 */
const HOLD_SLOP = 16
/**
 * 吸附半径按**像素**算，不是按格数。
 *
 * 这是「手机上拖不动 pane 边框」的真正原因：原来只允许差一格，而平板上 211 列宽的
 * 屏幕一格才 ~6px，手指落点偏十几个 px 是常事，press 就落进 pane 里去了（agent 收到
 * 一次拖动，屏幕上什么也没发生）。24px 差不多是一根手指的落点误差。
 */
const SNAP_PX = 24

/** 是不是画框线的字符（U+2500–U+259F：制表符 + 方块元素） */
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

  /** 这一列 / 这一行里框线字符占了多少格 */
  const runLen = (fixed: number, vertical: boolean) => {
    const n = vertical ? term.rows : term.cols
    let hit = 0
    for (let i = 1; i <= n; i++) {
      if (isBorderGlyph(vertical ? glyphAt(fixed, i) : glyphAt(i, fixed))) hit++
    }
    return hit
  }

  /**
   * 手指附近的贯穿线，长按抓取时用来**吸附**（手指偏十几个 px 也能抓到一格宽的竖线）。
   *
   * 只认**贯穿的长线**（占了 70% 以上、至少 6 格）：agent 自己画的短横线（消息之间的
   * 分隔、「2 new messages」那种）都被挡在外面。挡不住的是 agent 的**外框竖边** ——
   * 它和 herdr 的 pane 边框一样贯穿整屏，从字符层面分不出来；不过既然只是「按下点
   * 挪最多 24px」，猜错了也就是这一次拖动落在框边上，不会有别的后果。
   *
   * 代价：2×2 布局里那条横向分界只占半屏宽，吸附不到，得按准一点。
   */
  const nearestDivider = (col: number, row: number, box: NonNullable<ReturnType<typeof cellBox>>) => {
    const long = (n: number, total: number) => n >= Math.max(6, Math.round(total * 0.7))
    const dc = Math.max(1, Math.round(SNAP_PX / box.w))
    const dr = Math.max(1, Math.round(SNAP_PX / box.h))
    for (let d = 0; d <= Math.max(dc, dr); d++) {
      for (const sign of d === 0 ? [0] : [-1, 1]) {
        const c = col + d * sign
        if (d <= dc && c >= 1 && c <= term.cols && long(runLen(c, true), term.rows)) {
          return { col: c, row }
        }
      }
      for (const sign of d === 0 ? [0] : [-1, 1]) {
        const r = row + d * sign
        if (d <= dr && r >= 1 && r <= term.rows && long(runLen(r, false), term.cols)) {
          return { col, row: r }
        }
      }
    }
    return null
  }

  // SGR 鼠标（1006）编码：0 = 左键按下，32 = 按着左键移动，末尾 m = 松开。
  // herdr 常驻开着 1002/1003/1006，所以这里直接按 SGR 发。
  /** 按下左键不放。手指附近有贯穿线就吸到线上（一格宽的竖线按不准） */
  const grab = () => {
    const box = cellBox()
    if (!touch || !box) return
    const under = cellAt(touch.x, touch.y, box)
    const cell = nearestDivider(under.col, under.row, box) ?? under
    touch.drag = cell
    hooks.send(`\x1b[<0;${cell.col};${cell.row}M`)
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
      acc: 0, at: e.timeStamp, owned: mouseReporting(),
      hold: undefined, drag: null,
    }
    // 单指手势整个由这一层接管，**不分有没有鼠标上报**都吃掉默认行为。
    //
    // 不吃的话浏览器会补发兼容鼠标事件，xterm 当成「按下 + 拖选」—— 手指一滑变成选中
    // 文字、终端一动不动（鼠标上报关着的时候必现：还没 attach herdr，或者 pane 里跑着
    // 不收鼠标的程序）。触屏上想滚屏远比想选字常见，所以这里选滚屏。
    //
    // 两个代价，都是有意的：触屏不再能拖选（要复制用桌面鼠标，或者 herdr 自己的 COPY
    // 模式 —— 前缀进去之后 hjkl 选、y 复制），点一下也不再由浏览器顺手聚焦隐藏
    // textarea，改成自己在 touchend 里 focus（见 onEnd）。
    if (e.cancelable) e.preventDefault()
    if (!touch.owned) return // 普通 shell 里拖一下没有任何意义
    // 抓取**只认长按**。曾经试过「落在框线附近就立刻抓」，翻车了：agent 自己画的框
    // （Claude Code 每个 pane 一个圆角框）竖边同样贯穿整屏，判据分不出来，于是在框边
    // 上一划就变成往 agent 里拖鼠标 —— 手指想滚屏，屏幕上却在选文字。
    // 滑动必须永远是滑动，拖动是要「先按住」才换挡的那种动作。
    touch.hold = setTimeout(grab, HOLD_MS)
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
    // 手指走远了就不算长按了（不撤的话滑到一半会突然被抓住）。
    // 阈值给到 16px：按住不动的手指本来就会飘，太苛刻等于长按永远不成立。
    if (Math.abs(t.clientY - touch.y0) > HOLD_SLOP || Math.abs(t.clientX - touch.x0) > HOLD_SLOP) {
      clearTimeout(touch.hold)
    }

    touch.acc += t.clientY - touch.y
    touch.y = t.clientY

    if (e.cancelable) e.preventDefault() // 页面橡皮筋 / 原生选区都别来
    // 之前手忙脚乱选中的那块（比如用鼠标选过又来滑）先清掉，不然它一直亮着
    if (term.hasSelection()) term.clearSelection()

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

  const onEnd = (e?: TouchEvent) => {
    const tc = touch
    touch = null
    if (!tc) return
    clearTimeout(tc.hold)
    // 拖动结束必须发松开：不发的话 herdr 那边左键一直是按着的
    if (tc.drag) { release(tc); return }
    if (Math.abs(tc.y - tc.y0) > 8 || Math.abs(tc.x - tc.x0) > 8) return // 是滑动，已经处理过了

    // 同样用事件时间戳（和 tc.at 一个时间轴），不是处理函数里的 Date.now()
    const now = e?.timeStamp ?? performance.now()
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
