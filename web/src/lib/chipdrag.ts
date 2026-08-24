import { useEffect, useState } from 'react'
import type { PointerEvent as ReactPointerEvent } from 'react'

/**
 * 「小方块拖来拖去」那套手势 —— 软键条编辑器和顶栏编辑器共用。
 *
 * 两边的排布界面是同一个形状（**库在下、栏在上、拖进去**），差别只在筐有几个、落下来之后
 * 怎么改数据。所以这里只管手势和「落在哪一格」，`onDrop` / `onTap` 留给各自。
 *
 * 拖动是手写的 Pointer Events，不用 HTML5 drag-and-drop —— 后者在触屏上根本不触发，
 * 而平板 / 手机是这个项目的主设备。
 */

/** 第几个筐里的第几个位置（i 可以等于长度 = 插到末尾） */
export type ChipAt<Z> = { zone: Z; i: number }

/**
 * 触屏上按住这么久才算「把方块拿起来」。
 *
 * 为什么要按住：编辑页要能上下滚，而方块本身就是拖动的把手 —— 手指落在方块上往下划，
 * 到底是滚页面还是拖它，只能靠「有没有按住」区分。给方块写死 `touch-action: none` 的话
 * 页面就滚不动了（方块铺满了整页），而 `pan-y` 又会把「往下拖到另一个筐」吃成滚动。
 *
 * 250ms 是取舍：再短一点，滚页面时容易误拿；再长就等得难受。
 */
const HOLD_MS = 250
/** 按住期间手指飘出这么多就当是在滚页面，撤销这次拿起 */
const HOLD_SLOP = 10
/** 鼠标不用按住：走这么多像素就算拖（鼠标没有「滚 vs 拖」的歧义） */
const MOVE_SLOP = 6

export interface ChipDrag<Z> {
  /** 正在拖的那一个（渲染跟着手指的那个影子用）；没在拖就是 null */
  drag: { from: ChipAt<Z>; x: number; y: number } | null
  /** 这会儿会落在哪（画插入位那条线用） */
  over: ChipAt<Z> | null
  /** 绑在每个方块上的 onPointerDown */
  onChipDown: (e: ReactPointerEvent, from: ChipAt<Z>) => void
}

export function useChipDrag<Z extends string | number>(opts: {
  /** 有哪几个筐，**按屏幕上从上到下的顺序**（键盘换筐也按这个顺序） */
  zones: () => Z[]
  /** 每个筐的容器元素。里面的方块要带 `data-chip` 属性，命中判定靠它 */
  elOf: (z: Z) => HTMLElement | null | undefined
  /** 落一次拖动。from / to 都是「筐 + 位置」，怎么改数据由调用方决定 */
  onDrop: (from: ChipAt<Z>, to: ChipAt<Z>) => void
  /**
   * 这个筐是**定位格**吗（网格，落哪一格就是哪一格），还是默认的**插入序列**
   * （落在两个方块之间）。
   *
   * 软键条的固定块是定长网格：格子按位置排，「插到第 3 个前面」没有意义 —— 要的是
   * 「就放进第 3 格」。命中判据也就不一样：序列比中点（在左半边 = 插它前面），
   * 格子比**离哪个格子的中心最近**（比「指针在不在框里」宽容 —— 格与格之间那 6px 缝里
   * 松手不该白拖一次）。
   *
   * 定位格的筐要给**每一格**都挂 `data-chip`，空格也挂：不然空格压根不是落点，
   * 而「往空格里放一个」正是这种筐最主要的用法。
   */
  slots?: (z: Z) => boolean
  /** 没拿起来就松手 = 点一下（软键条那边是「选中这个定义」） */
  onTap?: (at: ChipAt<Z>) => void
}): ChipDrag<Z> {
  const [drag, setDrag] = useState<ChipDrag<Z>['drag']>(null)
  const [over, setOver] = useState<ChipAt<Z> | null>(null)

  /**
   * 长按方块时**别让浏览器弹它自己的菜单**。
   *
   * 真机上（平板的 Edge / Chrome）长按一个方块，弹出来的是浏览器的页面菜单（返回 / 重新加载 /
   * 下载 / 共享…），而且它一弹，浏览器就把这次触摸 `pointercancel` 掉 —— 于是「按住 250ms
   * 拿起来」永远走不到，表现就是「在平板上长按拖不动，只会呼出菜单」（用户报的）。
   *
   * 挂在 document 的捕获段，**只拦落在方块上的那一下**：面板里别处的文字还得能长按复制
   * （设置页上那些路径、环境变量名就是拿来抄的）。判据用 `[data-chip]` —— 命中判定本来就
   * 靠它，两个编辑器的方块都带。
   */
  useEffect(() => {
    const onMenu = (e: MouseEvent) => {
      if ((e.target as HTMLElement | null)?.closest?.('[data-chip]')) e.preventDefault()
    }
    document.addEventListener('contextmenu', onMenu, true)
    return () => document.removeEventListener('contextmenu', onMenu, true)
  }, [])

  /**
   * 指针落在哪个筐的第几个位置。
   *
   * 筐里是**换行**排的（和真的栏一样），所以先按 y 找到同一视觉行上的那几个方块，再在这
   * 一行里比 x 的中点。只比 x 的话，两行方块叠在一起时插入点会跳到上一行去。
   */
  const hit = (x: number, y: number): ChipAt<Z> | null => {
    for (const zone of opts.zones()) {
      const el = opts.elOf(zone)
      if (!el) continue
      const r = el.getBoundingClientRect()
      if (y < r.top - 8 || y > r.bottom + 8) continue
      const rects = ([...el.querySelectorAll('[data-chip]')] as HTMLElement[]).map((c) => c.getBoundingClientRect())
      if (!rects.length) return { zone, i: 0 }
      // 定位格：离哪个格子中心最近就是哪一格（见 slots）
      if (opts.slots?.(zone)) {
        let best = 0
        let bd = Infinity
        rects.forEach((c, n) => {
          const dx = x - (c.left + c.width / 2)
          const dy = y - (c.top + c.height / 2)
          const d = dx * dx + dy * dy
          if (d < bd) { bd = d; best = n }
        })
        return { zone, i: best }
      }
      const line = rects.filter((c) => y >= c.top - 2 && y <= c.bottom + 2)
      if (!line.length) return { zone, i: y < rects[0].top ? 0 : rects.length }
      for (let n = 0; n < rects.length; n++) {
        const c = rects[n]
        if (y < c.top - 2 || y > c.bottom + 2) continue
        if (x < c.left + c.width / 2) return { zone, i: n }
      }
      return { zone, i: rects.lastIndexOf(line[line.length - 1]) + 1 }
    }
    return null
  }

  /**
   * 按下一个方块。触屏按住 HOLD_MS 才算拿起、鼠标走 MOVE_SLOP 就算拖；
   * 没拿起就松手 = 点一下。
   */
  const onChipDown = (e: ReactPointerEvent, from: ChipAt<Z>) => {
    const target = e.currentTarget as HTMLElement
    const touch = e.pointerType !== 'mouse'
    const x0 = e.clientX
    const y0 = e.clientY
    let picked = false
    let hold: number | undefined
    let to: ChipAt<Z> | null = null
    try { target.setPointerCapture(e.pointerId) } catch { /* 没这个指针就不捕获 */ }

    // 拿起来之后不让页面跟着滚。手指在按住期间没动过，浏览器还没开始滚，这时候
    // preventDefault 拦得住（等它滚起来就只剩 pointercancel 了）。
    const noScroll = (ev: TouchEvent) => ev.preventDefault()

    const pick = () => {
      picked = true
      setDrag({ from, x: x0, y: y0 })
      document.addEventListener('touchmove', noScroll, { passive: false })
    }
    if (touch) hold = window.setTimeout(pick, HOLD_MS)

    const onMove = (ev: PointerEvent) => {
      const dx = ev.clientX - x0
      const dy = ev.clientY - y0
      if (!picked) {
        if (touch) {
          if (Math.abs(dx) > HOLD_SLOP || Math.abs(dy) > HOLD_SLOP) stop(false) // 在滚页面，放手
          return
        }
        if (Math.abs(dx) < MOVE_SLOP && Math.abs(dy) < MOVE_SLOP) return
        pick()
      }
      setDrag((d) => (d ? { ...d, x: ev.clientX, y: ev.clientY } : d))
      to = hit(ev.clientX, ev.clientY)
      setOver(to)
    }

    const stop = (finish: boolean) => {
      clearTimeout(hold)
      document.removeEventListener('touchmove', noScroll)
      target.removeEventListener('pointermove', onMove)
      target.removeEventListener('pointerup', up)
      target.removeEventListener('pointercancel', cancel)
      setDrag(null)
      setOver(null)
      if (!finish) return
      // 落在筐外面 = 什么都不做（放回原处）。误删太贵，删只走专门那个入口
      if (picked) {
        if (to) opts.onDrop(from, to)
        return
      }
      opts.onTap?.(from) // 没拿起来就松手 = 点一下
    }
    const up = () => stop(true)
    const cancel = () => stop(false)

    target.addEventListener('pointermove', onMove)
    target.addEventListener('pointerup', up)
    target.addEventListener('pointercancel', cancel)
  }

  return { drag, over, onChipDown }
}
