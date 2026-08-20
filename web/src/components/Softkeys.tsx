import { useEffect, useRef, useState } from 'react'
import { Settings } from 'lucide-react'
import { Button } from './ui/button'
import type { SoftKey } from '@/lib/api'
import { cn } from '@/lib/utils'

const H_KEY = 'softkeysH'
const INSET_KEY = 'softkeysInset'
/** 横向再窄也要留下这么多，不然缩成一条缝就抓不回来了 */
const MIN_BAR = 200
/**
 * 把手离**屏幕**边至少这么远。
 *
 * 安卓的手势导航把屏幕左右各约 24dp 划给了「侧滑返回 / 前进」，那一条是系统先吃，
 * 网页连 touchstart 都收不到 —— 把手正好贴在那儿就等于拖不动（实测就是这样）。
 * 所以把手离屏幕边不足 28px 时往里让，让到 28px 为止；软键条自己已经缩进来的话
 * 就不用让了（它的边本来就离屏幕边够远）。
 */
const EDGE_SAFE = 28
/** 没拖过的时候：自动高度，最多两排 —— 一眼能看出「会换行」，又不吃掉半个屏幕 */
const DEF_MAX = 96
const MIN_H = 38
/**
 * 二次确认的键「举起来」之后多久自动放下（ms）。
 * 太短来不及看第二眼，太长就会忘了自己举过 —— 回头随手一点反而正好点实。
 */
const CONFIRM_MS = 3000

/** 上限半屏。再高就没终端可看了，而软键条本来是终端的配角 */
const capH = () => Math.round((window.visualViewport?.height ?? window.innerHeight) / 2)

/** null = 还没拖过（自动高度）；数字 = 用户拖出来的高度 */
const readH = (): number | null => {
  const v = Number(localStorage.getItem(H_KEY))
  return v > 0 ? v : null
}

/** 左右两边各让出多少（px）。0/0 = 通屏，和以前一样 */
interface Inset { l: number; r: number }

const readInset = (): Inset => {
  try {
    const v = JSON.parse(localStorage.getItem(INSET_KEY) || 'null') as Inset | null
    if (v && typeof v.l === 'number' && typeof v.r === 'number') {
      return { l: Math.max(0, v.l), r: Math.max(0, v.r) }
    }
  } catch { /* 存坏了就当通屏 */ }
  return { l: 0, r: 0 }
}

const fitInset = (i: Inset): Inset => {
  const room = Math.max(0, window.innerWidth - MIN_BAR)
  const l = Math.min(i.l, room)
  return { l, r: Math.min(i.r, room - l) }
}

/**
 * 软键条。按键由 /api/softkeys 下发（存服务端，手机 / 平板 / 电脑共用一份），
 * 点最右边的 ⚙ 在网页上改。
 *
 * 手机没有 Ctrl 键，herdr 的 ctrl+b 前缀全靠这条。
 *
 * 按键**换行**排，不再挤成一条横向滚动的长龙 —— 平板上横滚找键太费劲。上边缘那三个
 * 把手是**两轴**的：上下拖改高度（最高半屏，放不下的部分上下滚），左右拖改横向 ——
 * 左边那个动左边界、右边那个动右边界、中间那个整条平移（宽度不变）。
 *
 * 拖过之后是**定高**（拖多高就是多高，哪怕键没排满），没拖过是自动高度封顶两排 ——
 * 一开始不留空白，而一旦用户明确要求「更高」，就别自作聪明缩回去。双击那条边复位。
 *
 * 左右两条边也能拖，**横向改宽度**：平板上输入法（还有它那一圈工具条）常常压住半边
 * 屏幕，把软键条横向缩到剩下的空地上，比整条通屏挤在键盘底下有用。两边各存一个
 * 留白（0/0 就是通屏），双击边复位。
 *
 * 打了 `confirm` 的键要点**两下**：第一下只是举起来（变红），第二下才真发出去。
 * 键挨得这么近，关 pane / 关标签这种误触一下就没了，而 herdr 那边没有撤销。
 */
export function Softkeys({
  keys, sticky, kbdUp, onSend, onSticky, onKeyboard, onEdit, onLayout,
}: {
  keys: SoftKey[]
  sticky: { ctrl: boolean; alt: boolean }
  kbdUp: boolean
  onSend: (bytes: string) => void
  onSticky: (which: 'ctrl' | 'alt') => void
  onKeyboard: () => void
  onEdit: () => void
  /** 高度变了要重排终端（软键条占的地方是从终端那儿借的） */
  onLayout: () => void
}) {
  const [h, setH] = useState(readH)
  const [inset, setInset] = useState(readInset)
  const rows = useRef<HTMLDivElement>(null)
  useEffect(() => { onLayout() }, [h, inset, onLayout])

  // 举着的那个键（下标）。同一个键再点一下才真发，点别的键 / 等超时都算放下。
  const [armed, setArmed] = useState<number | null>(null)
  const armTimer = useRef<number | undefined>(undefined)
  const disarm = () => {
    clearTimeout(armTimer.current)
    setArmed(null)
  }
  useEffect(() => () => clearTimeout(armTimer.current), [])

  // 按键改了（在编辑器里存了一版）就别接着举着上一版的下标 —— 那个位置现在
  // 可能已经是别的键了，接着点就点错了东西。
  useEffect(disarm, [keys])

  // 转屏 / 换窗口大小之后可能横向留白已经超出屏幕了，收一下
  useEffect(() => {
    const fix = () => setInset((i) => {
      const f = fitInset(i)
      return f.l === i.l && f.r === i.r ? i : f
    })
    fix()
    addEventListener('resize', fix)
    addEventListener('orientationchange', fix)
    return () => {
      removeEventListener('resize', fix)
      removeEventListener('orientationchange', fix)
    }
  }, [])

  const resetH = () => {
    setH(null)
    localStorage.removeItem(H_KEY)
  }

  const resetInset = () => {
    setInset({ l: 0, r: 0 })
    localStorage.removeItem(INSET_KEY)
  }

  const putH = (v: number) => {
    setH(v)
    localStorage.setItem(H_KEY, String(v))
  }

  const putInset = (v: Inset) => {
    setInset(v)
    localStorage.setItem(INSET_KEY, JSON.stringify(v))
  }

  /**
   * 拖把手。axes 说这个把手管哪几个方向、zone 说横向那一下算谁的：
   * l 动左边界、r 动右边界、m 整条平移（宽度不变）。
   *
   * 每个方向都有 3px 的死区：只想横着拖的时候不该顺手把高度锁成定高
   * （反之亦然）—— 手指很难走出一条直线。
   */
  const startEdge = (
    e: React.PointerEvent,
    zone: 'l' | 'm' | 'r',
    axes: 'x' | 'xy',
  ) => {
    const el = rows.current
    if (!el) return
    const h0 = el.getBoundingClientRect().height // 从**当前实际高度**起步，手感才连续
    const from = inset
    const x0 = e.clientX
    const y0 = e.clientY
    const target = e.currentTarget as HTMLElement
    try { target.setPointerCapture(e.pointerId) } catch { /* 没这个指针就不捕获 */ }
    e.preventDefault()
    const move = (ev: PointerEvent) => {
      const dx = ev.clientX - x0
      const dy = ev.clientY - y0
      if (axes === 'xy' && Math.abs(dy) > 3) {
        putH(Math.round(Math.min(Math.max(h0 - dy, MIN_H), capH())))
      }
      if (Math.abs(dx) > 3) {
        if (zone === 'l') putInset(fitInset({ l: Math.max(0, Math.round(from.l + dx)), r: from.r }))
        else if (zone === 'r') putInset(fitInset({ l: from.l, r: Math.max(0, Math.round(from.r - dx)) }))
        else {
          // 整条平移：宽度不变，撞到屏幕边就停
          const shift = Math.round(Math.max(-from.l, Math.min(dx, from.r)))
          putInset({ l: from.l + shift, r: from.r - shift })
        }
      }
    }
    const stop = () => {
      target.removeEventListener('pointermove', move)
      target.removeEventListener('pointerup', stop)
      target.removeEventListener('pointercancel', stop)
    }
    target.addEventListener('pointermove', move)
    target.addEventListener('pointerup', stop)
    target.addEventListener('pointercancel', stop)
  }

  const resetAll = () => {
    resetH()
    resetInset()
  }

  // 把手离屏幕边不够 28px 就往里让（安卓侧滑区），键那一排跟着让出同样的内边距，
  // 免得键钻到把手底下去
  const offL = Math.max(0, EDGE_SAFE - inset.l)
  const offR = Math.max(0, EDGE_SAFE - inset.r)

  return (
    <nav
      data-testid="softkeys"
      className={cn(
        'relative flex shrink-0 flex-col gap-1 border-t border-line bg-bar px-4 pt-1 pb-[7px] select-none',
        (inset.l || inset.r) && 'rounded-t-[10px] border-x', // 缩过之后看着像一块，而不是断了一截的横条
      )}
      style={{
        marginLeft: inset.l,
        marginRight: inset.r,
        paddingBottom: 'calc(7px + env(safe-area-inset-bottom))',
      }}
    >
      {/* 左右两条边：横向改宽度（把软键条从输入法压住的那半边挪开）。
          位置不写死在边上 —— 离屏幕边不足 EDGE_SAFE 就往里让，躲开安卓侧滑区 */}
      {(['l', 'r'] as const).map((side) => (
        <span
          key={side}
          data-testid={`softkeys-side-${side}`}
          className="absolute inset-y-0 flex w-6 cursor-ew-resize touch-none items-center justify-center"
          style={side === 'l' ? { left: offL } : { right: offR }}
          title="左右拖：软键条这一边收到哪儿（横向改宽度）。双击复位"
          onPointerDown={(e) => startEdge(e, side, 'x')}
          onDoubleClick={resetAll}
        >
          <span className="h-8 w-1 rounded-full bg-fg/25" />
        </span>
      ))}
      {/* 上边缘三个把手，都是**两轴**的：上下改高度，左右按抓的位置改横向。
          离屏幕边留够 EDGE_SAFE（px-8 + 各自的内边距），不然安卓侧滑先把手势吃掉 */}
      <div data-testid="softkeys-grip" className="-mt-1 flex shrink-0 items-center justify-between px-8 pt-1">
        {(['l', 'm', 'r'] as const).map((zone) => (
          <span
            key={zone}
            data-testid={`softkeys-grip-${zone}`}
            className="group flex h-5 cursor-move touch-none items-center px-3"
            title={
              zone === 'm'
                ? '拖我：上下改高度（最多半屏），左右整条平移。双击复位'
                : `拖我：上下改高度（最多半屏），左右改${zone === 'l' ? '左' : '右'}边界。双击复位`
            }
            onPointerDown={(e) => startEdge(e, zone, 'xy')}
            onDoubleClick={resetAll}
          >
            <span
              className={cn(
                'h-1.5 rounded-full bg-fg/25 group-active:bg-accent',
                zone === 'm' ? 'w-16' : 'w-10',
              )}
            />
          </span>
        ))}
      </div>
      <div className="flex gap-1.5" style={{ paddingLeft: offL, paddingRight: offR }}>
        {/* 按键换行排，放不下就上下滚；⚙ 固定在右边不跟着滚走 */}
        <div
          ref={rows}
          className="flex min-w-0 flex-1 flex-wrap content-start gap-1.5 overflow-y-auto overscroll-contain [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
          style={h ? { height: h } : { maxHeight: DEF_MAX }}
        >
          {keys.map((k, i) => {
            const on = k.sticky ? sticky[k.sticky] : k.act === 'kbd' ? kbdUp : false
            const up = armed === i   // 举起来了，等第二下
            return (
              <Button
                key={i}
                data-testid={up ? 'softkey-armed' : undefined}
                variant="key"
                size="key"
                on={on}
                // 举起来只换颜色，**不换文字**：改字会让按键变宽，手指底下的键
                // 当场挪位置，第二下就点到隔壁去了。
                className={cn(
                  k.wide && 'min-w-[78px]',
                  up && 'border-transparent bg-bad text-white',
                )}
                title={up ? '再点一次才真的发出去' : (k.spec || k.sticky || k.act || '') + (k.confirm ? '（要点两下）' : '')}
                // 这一个不能顺手 focus 终端，否则没法收起键盘
                onMouseDown={(e) => e.preventDefault()}
                onClick={() => {
                  if (k.confirm && !up) {
                    clearTimeout(armTimer.current)
                    setArmed(i)
                    armTimer.current = window.setTimeout(() => setArmed(null), CONFIRM_MS)
                    return
                  }
                  disarm()   // 点别的键 = 把举着的那个放下，但这一下照样算数
                  if (k.act === 'kbd') onKeyboard()
                  else if (k.sticky) onSticky(k.sticky)
                  else if (k.send) onSend(k.send)
                }}
              >
                {k.label}
              </Button>
            )
          })}
        </div>
        <Button data-testid="softkeys-edit" variant="key" size="key" className="self-start" title="配置软键条" onClick={() => { disarm(); onEdit() }} onMouseDown={(e) => e.preventDefault()}>
          <Settings className="size-4" />
        </Button>
      </div>
    </nav>
  )
}
