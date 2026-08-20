import { useCallback, useEffect, useRef, useState } from 'react'

/**
 * 发件箱的「浮动」状态：位置 + 大小，拖着走。
 *
 * 为什么需要：平板上输入法一弹出来就盖住底部，而发件箱正好钉在底部。理论上
 * `interactive-widget=resizes-content` + visualViewport 那套（见 useViewportHeight）
 * 会把布局缩到键盘上面，但**不是每个浏览器都认** —— 实测国产浏览器里页面高度纹丝不动，
 * 框就被键盘埋掉一半。所以给用户一个不依赖浏览器行为的出口：把面板拖到看得见的地方。
 *
 * box === null 表示还停靠在底部（默认），拖一下才变成浮动。
 */
export interface Box { x: number; y: number; w: number; h: number }

const KEY = 'composeBox'
const MIN_W = 240
const MIN_H = 110
/** 至少露这么多在可见区域里 —— 否则一把甩出屏幕就再也抓不回来了 */
const KEEP = 60

function read(): Box | null {
  try {
    const v = JSON.parse(localStorage.getItem(KEY) || 'null') as Box | null
    return v && typeof v.x === 'number' && typeof v.w === 'number' ? v : null
  } catch {
    return null // 存坏了就当没存
  }
}

/** 真正能看见的那块。键盘弹出时 visualViewport 才是可见区域，innerHeight 不是。 */
function view() {
  const vv = window.visualViewport
  return {
    w: Math.round(vv?.width ?? window.innerWidth),
    h: Math.round(vv?.height ?? window.innerHeight),
    top: Math.round(vv?.offsetTop ?? 0),
  }
}

function clamp(b: Box): Box {
  const v = view()
  const w = Math.max(MIN_W, Math.min(Math.round(b.w), v.w))
  const h = Math.max(MIN_H, Math.min(Math.round(b.h), v.h))
  return {
    w,
    h,
    x: Math.round(Math.max(KEEP - w, Math.min(b.x, v.w - KEEP))),
    y: Math.round(Math.max(v.top, Math.min(b.y, v.top + v.h - KEEP))),
  }
}

export function useFloatBox<T extends HTMLElement>() {
  const ref = useRef<T | null>(null)
  const [box, setBox] = useState<Box | null>(read)
  const boxRef = useRef(box)
  boxRef.current = box

  const put = useCallback((b: Box | null) => {
    setBox(b)
    boxRef.current = b
    if (b) localStorage.setItem(KEY, JSON.stringify(b))
    else localStorage.removeItem(KEY)
  }, [])

  const dock = useCallback(() => put(null), [put])

  // 换屏 / 转屏 / 键盘进出之后重新收边：面板可能正落在已经看不见的地方
  useEffect(() => {
    const fix = () => {
      const b = boxRef.current
      if (!b) return
      const c = clamp(b)
      if (c.x !== b.x || c.y !== b.y || c.w !== b.w || c.h !== b.h) put(c)
    }
    fix()
    window.visualViewport?.addEventListener('resize', fix)
    addEventListener('orientationchange', fix)
    addEventListener('resize', fix)
    return () => {
      window.visualViewport?.removeEventListener('resize', fix)
      removeEventListener('orientationchange', fix)
      removeEventListener('resize', fix)
    }
  }, [put])

  /**
   * 抓着把手拖。mode：move 挪位置、se 右下角改宽高、e/w 只改宽度（右边 / 左边）。
   *
   * 左右两条边都能拖，是因为平板上手在哪边就想从哪边拽 —— 只留右下角一个点，
   * 换只手就得把面板整个挪一遍。
   *
   * 用 pointer 事件 + setPointerCapture：一套代码同时管鼠标和手指，而且手指滑出把手
   * 之外也不会丢事件（touch 事件那边得自己算 identifier，没必要）。
   * 停靠状态下第一次拖，用当前实际位置当起点，视觉上不会跳。
   */
  const drag = useCallback((e: React.PointerEvent, mode: 'move' | 'se' | 'e' | 'w') => {
    const el = ref.current
    if (!el) return
    const r = el.getBoundingClientRect()
    // 停靠状态下是一条通屏的横幅，直接飘起来的话大半截会被拖出屏幕外。
    // 第一次撕下来时收成一块正常宽度的面板（之后由用户自己拖大小）。
    const w = boxRef.current ? r.width : Math.min(r.width, 620)
    const from = { px: e.clientX, py: e.clientY, x: r.left, y: r.top, w, h: r.height }
    const target = e.currentTarget as HTMLElement
    // 抓住这个指针：手指/鼠标滑出把手之外也照样收得到事件。拿不到就算了，
    // 别因为一个 NotFoundError 把整次拖动废掉。
    try { target.setPointerCapture(e.pointerId) } catch { /* 没这个指针就不捕获 */ }
    e.preventDefault()
    e.stopPropagation()

    const onMove = (ev: PointerEvent) => {
      const dx = ev.clientX - from.px
      const dy = ev.clientY - from.py
      let next: Box
      if (mode === 'move') next = { x: from.x + dx, y: from.y + dy, w: from.w, h: from.h }
      else if (mode === 'se') next = { x: from.x, y: from.y, w: from.w + dx, h: from.h + dy }
      else if (mode === 'e') next = { x: from.x, y: from.y, w: from.w + dx, h: from.h }
      else {
        // 拖左边：右边缘钉住不动，所以宽度变了 x 得跟着补
        const w = Math.max(MIN_W, from.w - dx)
        next = { x: from.x + (from.w - w), y: from.y, w, h: from.h }
      }
      put(clamp(next))
    }
    const stop = () => {
      target.removeEventListener('pointermove', onMove)
      target.removeEventListener('pointerup', stop)
      target.removeEventListener('pointercancel', stop)
    }
    target.addEventListener('pointermove', onMove)
    target.addEventListener('pointerup', stop)
    target.addEventListener('pointercancel', stop)
  }, [put])

  return { ref, box, floating: !!box, drag, dock }
}
