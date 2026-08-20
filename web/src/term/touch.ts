// 触屏手势：滑动 = 滚轮上报，单击 = 发给程序但不弹键盘，双击 = 切键盘，长按 = 什么都不做。
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

  const onStart = (e: TouchEvent) => {
    if (e.touches.length !== 1) { touch = null; return }
    const t = e.touches[0]
    touch = {
      x: t.clientX, y: t.clientY, x0: t.clientX, y0: t.clientY,
      acc: 0, at: Date.now(), owned: mouseReporting(),
    }
    // 有程序在收鼠标上报（herdr 这种）时独占手势：不让浏览器聚焦隐藏的 textarea
    // （点一下就弹键盘的根源）、不让长按弹出选择气泡、也不补发兼容鼠标事件。
    if (touch.owned && e.cancelable) e.preventDefault()
  }

  const onMove = (e: TouchEvent) => {
    if (!touch || e.touches.length !== 1) return
    const box = cellBox()
    if (!box) return
    const t = e.touches[0]
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
    if (Math.abs(tc.y - tc.y0) > 8 || Math.abs(tc.x - tc.x0) > 8) return // 是滑动，已经处理过了

    const now = Date.now()
    const isDouble = now - lastTapAt < 320
    lastTapAt = now

    if (isDouble) return hooks.toggleKeyboard() // 双击 = 显示 / 收起键盘
    if (now - tc.at > 500) return               // 长按 = 什么都不做（重点是别弹键盘）

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
  return () => {
    host.removeEventListener('touchstart', onStart)
    host.removeEventListener('touchmove', onMove)
    host.removeEventListener('touchend', onEnd)
  }
}
