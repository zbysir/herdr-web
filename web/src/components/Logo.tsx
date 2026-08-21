import { cn } from '@/lib/utils'

/**
 * 图标。herdr 的羊关在一个浏览器窗口里 —— 产品就是这一句「浏览器里的 herdr」。
 * 图形本体在 web/public/logo.svg，由 assets/make-logo.py 生成（羊的剪影是从 herdr
 * 的 assets/logo.svg 复用的描图路径，一千八百多字符，不适合内联进 tsx）。
 *
 * 用 <img> 而不是内联 svg：省掉那条巨长的路径进 bundle，而且换构图时只改生成脚本、
 * 不用碰组件。暗色那版在亮色主题下也不违和 —— 它本来就是一枚「应用图标」，
 * 深色小方块摆在浅色卡片上是常规做法。
 */
export function Logo({ size = 44, className }: { size?: number; className?: string }) {
  return (
    <img
      src="/logo.svg"
      alt=""
      width={size}
      height={size}
      draggable={false}
      // 加一圈描边：图标里「页面」那块底色（#191919）和面板底色几乎一样，不描边的话
      // 方块的边界看不见，只剩一条浮在半空的窗口顶栏。圆角跟着尺寸走 —— 和 logo.svg
      // 里的 rx=14/64 是同一个比例，描边才不会和图形的圆角错开。
      style={{ borderRadius: (size * 14) / 64 }}
      className={cn('ring-1 ring-line', className)}
    />
  )
}
