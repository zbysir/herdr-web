import { useEffect, useRef } from 'react'
import type { SoftKey } from '@/lib/api'
import { holdRate } from '@/lib/prefs'

/**
 * 按住不放就**连发**（方向键那种）。
 *
 * 手机上没有物理键盘，「往上翻十行」只能点十下 —— 而这正是方向键在终端里最常见的用法
 * （翻历史命令、在 agent 的选择列表里挑一项、`ctrl+b` 之后调 pane 大小）。物理键盘天生
 * 有这一档（按住 → 停一下 → 连发），触屏上得自己做。
 *
 * 三条是有意这么定的：
 *
 *  ① **一下点（tap）照旧走 `click`，不改成 `pointerdown` 发。** 条那一行是横滑的，
 *     手指多半就是落在某个键上再往旁边划 —— 在按下那一刻就把字节发出去的话，每划一下
 *     就白发一个方向键。所以按下只是**起表**，到 `HOLD_DELAY_MS` 还没松手 / 没变成滑动，
 *     才认定是「按住」。
 *  ② **连发过就把随后那一下 `click` 吞掉**（`swallow`）：松手时浏览器照旧补一个 click，
 *     不吞的话「按住走了 8 格」最后会多走一格。
 *  ③ **有保险丝**（`HOLD_MAX_TICKS`）。丢掉 `pointerup` 在这个项目里是真事：touch 的事件
 *     target 在按下那一刻就钉死了，那个元素要是中途从文档里被拿掉（弹出组关了、编辑器里
 *     存了一版），松手那一下**不冒泡到 document**，谁都收不到（见 CLAUDE.md 触屏那两条）。
 *     没有上限的话就是「一个键卡住了，方向键一直在发」。
 *
 * **速度是个设置**（设置 →「终端」页，`holdRate`，16 / 8 / 4 / 2 / 1 次每秒）：16 下/秒 是
 * 物理键盘那一档，触屏上常常一按就冲过头（想在选择列表里下移两项，结果走到底）。它在
 * **pointerdown 里现读镜像**，所以改完下一次按住就是新速度 —— 不用把这个值一路传进快捷
 * 键条和顶栏（同 keyStyle / popupClear 那条，见 lib/prefs.ts）。
 */

/** 按住多久才开始连发。太短会把「点一下」变成连发，太长手指会以为没反应 */
export const HOLD_DELAY_MS = 400
/**
 * 一次按住最多发这么多下就自己停（保险丝，见上面第③条）。
 *
 * 数**下数**不是时长：速度是个设置，按时长卡的话「1 次/秒」那一档 20 秒就只剩 20 下，
 * 而那正是慢档存在的用法（按住慢慢走）。400 下在最快那档也要按住 25 秒，正常按住够不着。
 */
const HOLD_MAX_TICKS = 400
/**
 * 还在等「这一下算不算按住」的那 400ms 里，手指挪开这么多像素就当成「在滑条」，不算按住。
 * 连发跑起来之后不再管位移，见 bind 里 onPointerMove 那条注释。
 */
const SLOP_PX = 12

/**
 * 哪些键**可以**连发：只有「多发几次无非是多走几步，退得回来」的那几个。
 *
 * 按**解析出来的字节**认，不是新加一个配置项：`↑` 是不是方向键这件事，答案就写在
 * `send` 里（服务端 named 表：`up` → `\x1b[A`），再加一个「这个键能不能连发」的开关
 * 就是同一件事的第二份来源 —— 而用户自己配的 `↑` 也该有这一档，不该取决于他有没有
 * 记得勾那个框。
 *
 * 有意**不**连发的：`enter`（多发一次就是多跑一条命令）、`ctrl+c`、退格 / 删除
 * （手指压久一点就吃掉一整行，而终端里没有撤销）、组合键（`ctrl+b` 之类前缀，
 * 连发出去的是一串谁也不认识的东西）。这条清单要加东西的话，先问「多发十次要不要紧」。
 */
const REPEATABLE = new Set([
  '\x1b[A', '\x1b[B', '\x1b[C', '\x1b[D', // ↑ ↓ → ←
  '\x1b[5~', '\x1b[6~',                   // PgUp / PgDn
])

/** 这个键按住不放该不该连发。举一下才发的（confirm）、组键、粘滞、act 一律不 */
export function canHold(k: SoftKey): boolean {
  if (k.confirm || k.members || k.sticky || k.act) return false
  return !!k.send && REPEATABLE.has(k.send)
}

/**
 * 给按钮挂「按住连发」。快捷键条和顶栏**共用这一份**（同一个定义能同时出现在两个界面上，
 * 见 internal/topbar 的 `key:` 引用）—— 两处各写一遍计时器就是「同一个键两种手感」的来路，
 * 和 useArm 同一个道理。
 */
export function useHold() {
  const delay = useRef<number | undefined>(undefined)
  const tick = useRef<number | undefined>(undefined)
  /** 刚刚连发过：随后那一下 click 要吞掉 */
  const fired = useRef(false)
  /** 按下那一点，用来判「这是在滑条，不是在按住」 */
  const from = useRef<{ x: number; y: number } | null>(null)

  const stop = () => {
    clearTimeout(delay.current)
    clearInterval(tick.current)
    delay.current = tick.current = undefined
    from.current = null
  }

  // 卸载时收干净：连发的定时器不属于任何 React 状态，漏一个就是「键没了字还在发」
  useEffect(() => stop, [])

  /**
   * 摊在按钮上的那几个 handler。`run` 是**只发这一下字节**那一半：它一秒会被调十几次，
   * 幂等的副作用（顺手 focus 一下终端）无所谓，弹提示 / 记一笔那种一次性的别放进来。
   *
   * **不能连发的键也要挂**（`run` 传 null）：按下那一下顺手把「刚连发过」的标记清掉。
   * 只给能连发的键挂的话，会出这么一档：按住 ← 连发完，手指挪到键外面松开（没有 click），
   * 接着点 `↵` —— 那个陈年标记正好把这一下吞掉，表现是「回车点了没反应」。
   */
  const bind = (run: (() => void) | null) => ({
    onPointerDown: (e: React.PointerEvent) => {
      stop()
      fired.current = false
      if (!run) return
      from.current = { x: e.clientX, y: e.clientY }
      delay.current = window.setTimeout(() => {
        delay.current = undefined                  // 「还在等」结束了，见 onPointerMove
        fired.current = true
        run()                                      // 认定「按住」的第一下
        // 速度**在这儿现读**（同步读镜像，见 lib/prefs.ts）：设置里改完，下一次按住就是
        // 新速度，不用把它一路传进快捷键条和顶栏
        let n = 1
        tick.current = window.setInterval(() => {
          if (++n > HOLD_MAX_TICKS) return stop()  // 保险丝：多半是松手那一下丢了
          run()
        }, Math.round(1000 / holdRate()))
      }, HOLD_DELAY_MS)
    },
    // 变成滑动时浏览器发的是 pointercancel（条那一行是横滑的，这条最常走）
    onPointerCancel: stop,
    onPointerUp: stop,
    // 鼠标按着挪出键外 = 放手。触屏上 touch 是隐式捕获的，这一条只在松手时才走，无害
    onPointerLeave: stop,
    onPointerMove: (e: React.PointerEvent) => {
      // **只在「还在等这一下算不算按住」的那 400ms 里量**（`delay` 还挂着）：条不滚动时
      // （键少、放得下）划一下发不出 pointercancel，那就只能自己量位移。
      // 连发已经跑起来之后不再管位移 —— 按住三秒手指本来就会飘，那时候停掉的表现是
      // 「按着按着自己不动了」；真要滑条的话浏览器会发 pointercancel。
      const f = from.current
      if (!f || !delay.current) return
      if (Math.hypot(e.clientX - f.x, e.clientY - f.y) > SLOP_PX) stop()
    },
  })

  /** 这一下 click 要不要吞掉（刚连发过就吞，见上面第②条）。读一次就清 */
  const swallow = () => {
    const f = fired.current
    fired.current = false
    return f
  }

  return { bind, swallow }
}
