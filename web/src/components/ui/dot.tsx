import { cn } from '@/lib/utils'

/**
 * 「有还没看的」那个红点：贴在按钮右上角的一小点（**不报数**，为什么见 App 的 dotEl）。
 *
 * 一份，两处用（顶栏的图标按钮、快捷键条上的键）—— 原来两边各写一份同样的 class 串，
 * 而下面这条「不能探出按钮」的约束只要有一处忘了就会现形。
 *
 * # 它必须**整个待在按钮框里**
 *
 * 原来是 `-top-0.5 -right-0.5`（往外探 2px）+ 2px 的 ring，也就是比按钮高出 4px。
 * 而顶栏那排和手机上的快捷键条**都是横滑容器**（`overflow-x-auto`）—— CSS 里只要有一个轴
 * 是 auto，另一个轴的 `visible` 就会被算成 auto，于是**竖着也裁**。表现就是用户报的
 * 「红点上面一点点被裁掉了，看起来不圆」：一个上缘平掉的圆。
 *
 * 所以位置改成往里收 2px（ring 的外缘正好和按钮的 padding 盒对齐，一点都不探出去）。
 * 这样它在任何容器里都不会被裁 —— 而「往外探一点」那种做法，等于要求以后每一个放它的
 * 地方都记得留出 4px 的溢出空间。
 *
 * ring 用的是所在条的底色（顶栏和面板都是 `bar`），让这一点看着像贴在图标上的，
 * 而不是浮在半空的一块红。
 */
/**
 * 两种颜色，意思不一样，别混：
 *
 *   - `bad`（红，默认）= **有 agent 在等你**（提示那条路）。红是「要你动手」。
 *   - `brand`（绿）= **有你还没看过的改动**（顶栏「改动」那个按钮）。它不是坏事，
 *     红色会让人以为哪儿出问题了 —— 而这条路上真出问题时是根本不画角标（安静地没有）。
 */
export function NoticeDot({ testId, tone = 'bad' }: { testId?: string; tone?: 'bad' | 'brand' }) {
  return (
    <span
      data-testid={testId}
      className={cn('absolute right-0.5 top-0.5 size-2 rounded-full ring-2 ring-bar',
        tone === 'brand' ? 'bg-brand' : 'bg-bad')}
    />
  )
}
