import type { StickyMode } from '@/term/session'

/**
 * 粘滞修饰键（Ctrl / Alt）三档的界面那一半。定义在 `term/session.ts`（StickyMode），
 * 这儿只管「长什么样、title 写什么」—— 顶栏和快捷键条**两处共用**（顶栏上也能放
 * 「我的按键」里的 Ctrl，见 internal/topbar 的 KeyPrefix）。
 *
 * 各写一份的话，同一个键在两处的样子会慢慢分家，而这是**状态**不是装饰：分不出
 * 「一次性」和「锁住」，人就会在锁着的时候接着打字，把一整行敲成控制字符。
 */

/**
 * 「锁住」那一档的记号：键底下一条小横杠（像输入法 Shift 锁定那条）。
 *
 * **不改文字、不改尺寸**：`on` 那档已经是饱和填充（「按下去了必须一眼看见」，见 CLAUDE.md
 * 配色那节），锁住要在它之上再分一层，而键上加字会让按钮变宽 —— 手指底下的键当场挪位置，
 * 第二下就点到隔壁去了（举起来那一下不换文字是同一条理由）。
 *
 * 用 `bg-current`：亮着的时候字是深色，横杠跟着字走，不用再挑一次颜色。
 */
export const LOCK_CLS =
  'after:absolute after:inset-x-1.5 after:bottom-1 after:h-0.5 after:rounded-full after:bg-current after:opacity-55'

/** 三档各自的 title。说清「这会儿是什么」和「再点一下会怎样」——这类键没有别的地方讲得了 */
export const STICKY_HINT: Record<StickyMode, string> = {
  off: '点一下：下一个键带上它；点两下：锁住（能连按）',
  once: '只对下一个键生效 —— 再点一下锁住（Ctrl+C 要连按好几次就用这档）',
  lock: '锁住了，一直带着它 —— 再点一下关掉',
}
