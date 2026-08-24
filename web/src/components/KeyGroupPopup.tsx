import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import type { SoftKey } from '@/lib/api'
import { cn } from '@/lib/utils'

/**
 * 弹出组的浮窗：一个键在条上**只占一格**，点开在它旁边浮出那几个键。
 *
 * **`position: fixed`，盖在终端上，条一点都不重排。** 这不只是观感：内联展开会改 dock 的
 * 高度，而那会一路触发终端重算行列 + SIGWINCH + 冻帧那一整套（见 CLAUDE.md 「改尺寸会闪
 * 一下全黑」）—— 点一下方向键闪一次屏，没人受得了。浮层则完全不参与布局。
 *
 * 摆在哪：锚点在屏幕**上半**就朝下弹（顶栏上那些），下半就朝上弹（软键条）。横向以锚点为中心，
 * 撞到屏幕边就往里收。位置在挂载后量一次自己的尺寸再定 —— 列数和键宽都是配置，算不出来。
 *
 * 视口一变（呼输入法、转屏）或者条被横滑，**重新定位，不关掉**。原来写的是「resize 就关」，
 * 那是错的：Android 上点一下就可能把输入法顶回来（见 MOBILE.md 那条 GestureTap），视口跟着
 * 一变，浮窗刚开就自己关了 —— 这个功能在那台机器上等于不存在。
 *
 * 什么时候关：再点一次那个键、或者**点到浮窗外面**。点外面那一下**照旧透传**（不吞）——
 * 吞掉要在 touchstart 上 preventDefault，而那条路在这个项目里是一串真机 bug 的来源
 * （见 CLAUDE.md 触屏那两条）。点开着的时候按里面的键**不关**：方向键要连着点。
 *
 * 判「点到外面了」只认 `pointerdown`：`click` 在触屏上会因为浮层被卸掉而丢（浮层在
 * pointerup 里消失，touch 事件的 target 钉在已脱离文档的元素上，**不冒泡到 document**）。
 */
export function KeyGroupPopup({
  cols, members, anchor, onClose, renderKey,
}: {
  cols: number
  /** 按行读的成员，`null` = 空格子（方向键盘上方那两个空位靠它占出来） */
  members: (SoftKey | null)[]
  /** 那个组键的 DOM 元素，浮窗贴着它摆 */
  anchor: HTMLElement | null
  onClose: () => void
  /** 画一个成员键。**用调用方那一份**（发字节 / 粘滞 / act / 两次确认全在那儿），别写第二份 */
  renderKey: (k: SoftKey, at: string) => ReactNode
}) {
  const box = useRef<HTMLDivElement>(null)
  const [pos, setPos] = useState<{ left: number; top: number } | null>(null)
  /** 撞一下就重新量位置（视口变了 / 条被滑了） */
  const [tick, setTick] = useState(0)

  // 位置：挂载后量自己一次再定（列数和键宽都是配置，算不出来）
  useLayoutEffect(() => {
    const el = box.current
    if (!el || !anchor) return
    const a = anchor.getBoundingClientRect()
    const b = el.getBoundingClientRect()
    const GAP = 6
    const EDGE = 8 // 离屏幕边至少留这么多
    const below = a.top < innerHeight / 2 // 锚点在上半屏 → 朝下弹
    const top = below ? a.bottom + GAP : a.top - b.height - GAP
    const left = Math.min(Math.max(a.left + a.width / 2 - b.width / 2, EDGE), innerWidth - b.width - EDGE)
    setPos({ left: Math.round(left), top: Math.round(top) })
  }, [anchor, cols, members, tick])

  // 点到外面就关。捕获段 + pointerdown，理由见上面
  useEffect(() => {
    const off = (e: PointerEvent) => {
      const t = e.target as HTMLElement | null
      if (t?.closest('[data-keygroup]') || (anchor && t && anchor.contains(t))) return
      onClose()
    }
    document.addEventListener('pointerdown', off, true)
    // 视口变了 / 条被横滑了就**重新定位**（不关，理由见上面）。scroll 用捕获段：
    // 条那两行是各自横滑的，滚动事件不冒泡
    const move = () => setTick((n) => n + 1)
    addEventListener('resize', move)
    visualViewport?.addEventListener('resize', move)
    document.addEventListener('scroll', move, true)
    return () => {
      document.removeEventListener('pointerdown', off, true)
      removeEventListener('resize', move)
      visualViewport?.removeEventListener('resize', move)
      document.removeEventListener('scroll', move, true)
    }
  }, [anchor, onClose])

  // 尾部整行是空的就不画：3×2 的方向键盘不该拖着一条空的第三行
  const rows: (SoftKey | null)[][] = []
  for (let i = 0; i < members.length; i += cols) rows.push(members.slice(i, i + cols))
  while (rows.length && rows[rows.length - 1].every((k) => !k)) rows.pop()
  if (!rows.length) return null

  return (
    <div
      ref={box}
      data-keygroup
      data-testid="key-group"
      className={cn(
        'fixed z-40 grid gap-1.5 rounded-card border border-line bg-bar p-1.5',
        'shadow-[0_10px_30px_-8px_rgba(0,0,0,.75)]',
        // 量出位置之前先别显示：否则会看到它从左上角跳到锚点上
        pos ? 'visible' : 'invisible',
      )}
      style={{
        // `minmax(--sk-w, auto)`：至少一个可点宽，装不下就让那一列自己撑开 ——
        // 写死 `var(--sk-w)` 的话 `/clear` 这种长键的字会漏到格子外面（键是 nowrap 的）
        gridTemplateColumns: `repeat(${cols}, minmax(var(--sk-w), auto))`,
        left: pos?.left ?? 0,
        top: pos?.top ?? 0,
      }}
      // 这一下不能把焦点从终端上摘走，也不能让浏览器顺手弹输入法
      onMouseDown={(e) => e.preventDefault()}
    >
      {rows.flatMap((row, r) => row.map((k, c) => (
        k ? renderKey(k, `g:${r}:${c}`) : <span key={`g:${r}:${c}`} aria-hidden />
      )))}
    </div>
  )
}
