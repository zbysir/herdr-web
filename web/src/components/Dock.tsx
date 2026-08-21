import { useEffect, useRef, useState, type ReactNode } from 'react'
import { clearOriented, readOriented, useOrient, writeOriented } from '@/lib/oriented'
import { usePhone } from '@/hooks/usePhone'
import { cn } from '@/lib/utils'

/** 键那一区的高度。语义没变，沿用软键条时代的键名，已经调好的高度不会因为这次合并丢掉 */
const H_KEY = 'softkeysH'
/**
 * 左右留白。**换了键名**：以前那份（`softkeysInset`）只缩键那一条，现在缩的是整块底部
 * 面板（发件箱跟着一起窄），语义不一样了 —— 把旧值直接套过来的话，升级后一开页面就会
 * 发现发件箱莫名其妙只剩半屏宽。
 */
const INSET_KEY = 'dockInset'
/** 横向再窄也要留下这么多，不然缩成一条缝就抓不回来了 */
const MIN_BAR = 240
/**
 * 把手离**屏幕**边至少这么远。
 *
 * 安卓的手势导航把屏幕左右各一条划给了「侧滑返回 / 前进」，那一条是系统先吃，网页连
 * touchstart 都收不到 —— 把手正好贴在那儿就等于拖不动（实测就是这样）。所以把手离
 * 屏幕边不足这么多就往里让；面板自己已经缩进来的话不用让（边本来就离屏幕边够远）。
 *
 * 14px 是真机上试出来的：按系统标称的 24dp 让，让出来的那一条太宽、看着像错位，
 * 而实际有效的侧滑判定比标称窄。要是哪天侧边把手又拖不动了，先怀疑这个数。
 */
const EDGE_SAFE = 14
/**
 * 侧边把手有多宽，**同时也是面板内容的左右内边距** —— 两个数必须一样。
 *
 * 把手是贯穿整块面板的一条竖带（发件箱的 textarea 也在它下面），一旦压到内容上，
 * 「点一下 textarea 左边想放光标」就变成了「拖着改面板宽度」，而且 touch-none 之后
 * 连选字都选不了。软键条时代把手压在按钮上还能忍（按钮点哪儿都一样），到了输入框
 * 就不行了。
 */
const EDGE_W = 16
/** 键没拖过的时候：自动高度，最多两排 —— 一眼能看出「会换行」，又不吃掉半个屏幕 */
const DEF_MAX = 96
const MIN_H = 38

/** 键那一区上限半屏。再高就没终端可看了，而软键条本来是终端的配角 */
const capH = () => Math.round((window.visualViewport?.height ?? window.innerHeight) / 2)

/** null = 还没拖过（自动高度）；数字 = 用户拖出来的高度。横竖屏各存一份 */
const readH = () => readOriented<number>(H_KEY, (v) => typeof v === 'number' && v > 0)

/** 左右两边各让出多少（px）。0/0 = 通屏 */
interface Inset { l: number; r: number }

const readInset = (): Inset => {
  const v = readOriented<Inset>(INSET_KEY, (x) => {
    const i = x as Inset | null
    return !!i && typeof i.l === 'number' && typeof i.r === 'number'
  })
  return v ? { l: Math.max(0, v.l), r: Math.max(0, v.r) } : { l: 0, r: 0 }
}

const fitInset = (i: Inset): Inset => {
  const room = Math.max(0, window.innerWidth - MIN_BAR)
  const l = Math.min(i.l, room)
  return { l, r: Math.min(i.r, room - l) }
}

/**
 * 底部面板：发件箱 + 软键条装在**同一块**里。
 *
 * 以前这是两块各管各的：发件箱能抓着 ⠿ 从底部「撕」下来变成浮动面板（自己一套位置 /
 * 大小），软键条自己一套左右留白和高度。两块叠在一起时是两条边框、两种宽度、两套把手 ——
 * 平板上看着就是错位的两层，而且想「把底下这一坨挪开输入法」得分别调两次。
 *
 * 现在只有一块，一套边框一套宽度：**左右两条侧边**横向改宽度（输入法连着它那圈工具条
 * 常常压住半边屏幕，把整块底部面板缩到剩下的空地上），**键那一区上边缘的三个把手**是
 * 两轴的 —— 上下改键区高度（最多半屏，放不下的部分上下滚），左右和侧边一个意思（左边
 * 那个动左边界、右边那个动右边界、中间那个整条平移，宽度不变）。任意把手双击复位。
 *
 * 面板里的东西按**面板自己的宽度**折行（`@container` + `@max-*`），不是按视口宽度：
 * 缩成半屏之后视口还是那么宽，按视口算的话发件箱那排控件会挤成一团。
 *
 * 宽度和高度都**横竖屏各存一份**（见 lib/oriented）：横屏能排下的行数、能让出的宽度
 * 和竖屏完全不是一回事，共用一份的话每转一次屏就得重调。
 *
 * **手机竖屏（< PHONE_MAX）整套把手都不出**，面板通屏、软键条**一行横滑**：那么窄的
 * 屏上把手是负收益 —— 三条把手加起来占掉 24px 的高（约两行终端），而左右能让出的空地
 * 本来就没有；键换行排更是每多一排就吃掉一排终端。宽度一过线（转横屏、平板、桌面）把手
 * 和存着的那份尺寸自己就回来了，两边互不影响。
 */
export function Dock({
  keys, onLayout, children,
}: {
  /** 软键条（`<Softkeys>`，只出键本身）。不显示就传 null */
  keys?: ReactNode
  /** 面板尺寸变了要重排终端（面板占的地方是从终端那儿借的） */
  onLayout: () => void
  /** 发件箱 */
  children?: ReactNode
}) {
  const phone = usePhone()
  const orient = useOrient()
  const [h, setH] = useState(readH)
  const [inset, setInset] = useState(readInset)
  const keysBox = useRef<HTMLDivElement>(null)
  useEffect(() => { onLayout() }, [h, inset, phone, onLayout])

  // 转屏 / 换窗口大小之后：**重新读**当前朝向那一份（不是把手上这份挪一挪），再收边。
  // 横屏调的高度和留白跟竖屏无关，各读各的。
  useEffect(() => {
    const fix = () => {
      setInset(fitInset(readInset()))
      setH(readH())
    }
    fix()
    addEventListener('resize', fix)
    addEventListener('orientationchange', fix)
    return () => {
      removeEventListener('resize', fix)
      removeEventListener('orientationchange', fix)
    }
  }, [orient])

  const putH = (v: number) => {
    setH(v)
    writeOriented(H_KEY, v)
  }

  const putInset = (v: Inset) => {
    setInset(v)
    writeOriented(INSET_KEY, v)
  }

  const resetAll = () => {
    setH(null)
    clearOriented(H_KEY)
    setInset({ l: 0, r: 0 })
    clearOriented(INSET_KEY)
  }

  /**
   * 拖把手。axes 说这个把手管哪几个方向、zone 说横向那一下算谁的：
   * l 动左边界、r 动右边界、m 整条平移（宽度不变）。
   *
   * 每个方向都有 3px 的死区：只想横着拖的时候不该顺手把键区高度锁成定高
   * （反之亦然）—— 手指很难走出一条直线。
   */
  const startEdge = (
    e: React.PointerEvent,
    zone: 'l' | 'm' | 'r',
    axes: 'x' | 'xy',
  ) => {
    // 从键区**当前实际高度**起步，手感才连续
    const h0 = keysBox.current?.getBoundingClientRect().height ?? 0
    const from = inset
    const x0 = e.clientX
    const y0 = e.clientY
    const target = e.currentTarget as HTMLElement
    try { target.setPointerCapture(e.pointerId) } catch { /* 没这个指针就不捕获 */ }
    e.preventDefault()
    const move = (ev: PointerEvent) => {
      const dx = ev.clientX - x0
      const dy = ev.clientY - y0
      if (axes === 'xy' && h0 && Math.abs(dy) > 3) {
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

  // 把手离屏幕边不够 EDGE_SAFE 就往里让（安卓侧滑区），面板内容跟着让出同样的内边距，
  // 免得控件钻到把手底下去
  const offL = Math.max(0, EDGE_SAFE - inset.l)
  const offR = Math.max(0, EDGE_SAFE - inset.r)

  return (
    <div
      data-testid="dock"
      className={cn(
        'relative flex shrink-0 flex-col border-t border-line bg-bar',
        // 缩过之后看着像一块，而不是断了一截的横条
        !phone && (inset.l || inset.r) && 'rounded-t-[10px] border-x',
      )}
      style={{
        marginLeft: phone ? 0 : inset.l,
        marginRight: phone ? 0 : inset.r,
        paddingBottom: `calc(${phone ? 4 : 7}px + env(safe-area-inset-bottom))`,
      }}
    >
      {/* 左右两条边：横向改宽度（把整块面板从输入法压住的那半边挪开）。
          位置不写死在边上 —— 离屏幕边不足 EDGE_SAFE 就往里让，躲开安卓侧滑区 */}
      {!phone && (['l', 'r'] as const).map((side) => (
        <span
          key={side}
          data-testid={`dock-side-${side}`}
          className="absolute inset-y-0 z-1 flex w-4 cursor-ew-resize touch-none items-center justify-center select-none"
          style={side === 'l' ? { left: offL } : { right: offR }}
          title="左右拖：面板这一边收到哪儿（横向改宽度）。双击复位"
          onPointerDown={(e) => startEdge(e, side, 'x')}
          onDoubleClick={resetAll}
        >
          <span className="h-8 w-1 rounded-full bg-fg/25" />
        </span>
      ))}

      {/* @container：里面的东西按**这块面板**的宽度折行，不是按视口。
          手机档没有侧边把手，内边距也就不用给它让位（10px 是纯粹的呼吸位） */}
      <div
        className="@container flex min-h-0 flex-col"
        style={phone
          ? { paddingLeft: 10, paddingRight: 10 }
          : { paddingLeft: EDGE_W + offL, paddingRight: EDGE_W + offR }}
      >
        {children}

        {keys && (
          <>
            {/* 键区上边缘三个把手，都是**两轴**的：上下改键区高度，左右按抓的位置改横向。
                离屏幕边留够 EDGE_SAFE（px-8 + 外面那层内边距），不然安卓侧滑先把手势吃掉 */}
            {!phone && (
              <div
                data-testid="dock-grip"
                className="flex shrink-0 items-center justify-between px-8 pt-1 select-none"
              >
                {(['l', 'm', 'r'] as const).map((zone) => (
                  <span
                    key={zone}
                    data-testid={`dock-grip-${zone}`}
                    className="group flex h-5 cursor-move touch-none items-center px-3"
                    title={
                      zone === 'm'
                        ? '拖我：上下改软键条高度（最多半屏），左右整条平移。双击复位'
                        : `拖我：上下改软键条高度（最多半屏），左右改${zone === 'l' ? '左' : '右'}边界。双击复位`
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
            )}

            {/* 键区。**横向**怎么排是软键条自己的事（它分两行，各自横滑，见 Softkeys）；
                这一层只管**纵向**那点：宽屏上给一个能拖的定高 + 上下滚，手机上跟着内容走
                （那一档没有把手，也不该有第三行） */}
            <div
              ref={keysBox}
              data-testid="softkeys"
              className={cn(
                'flex min-w-0 flex-col gap-1.5 select-none',
                phone ? 'shrink-0 pt-1.5' : 'overflow-y-auto overscroll-contain',
              )}
              style={phone ? undefined : (h ? { height: h } : { maxHeight: DEF_MAX })}
            >
              {keys}
            </div>
          </>
        )}
      </div>
    </div>
  )
}
