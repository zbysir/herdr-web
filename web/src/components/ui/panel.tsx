import type * as React from 'react'
import { X } from 'lucide-react'
import { cn } from '@/lib/utils'
import { Button } from './button'

/**
 * 顶栏下面浮出来的面板。窄屏时铺满宽度。
 *
 * **`title` 是可选的**：不给就不画那条标题栏，关闭按钮由里面自己安排（`onClose` 照样传，
 * Esc 那条兜底和外面的开合都还认它）。手机上那一整行（标题 + ×）就是 44px 高的一片空白 ——
 * 面板一览要的是尽量多几行 pane，而「这是什么面板」看内容就知道了，所以它把 × 并进了
 * 筛选那一排。内容本身需要一句抬头的面板（设置、文件浏览）照旧给 title。
 */
export function Panel({
  title, onClose, children, className,
}: { title?: string; onClose: () => void; children: React.ReactNode; className?: string }) {
  return (
    <aside
      data-testid="panel"
      className={cn(
        // top 只留一点点缝：这几块面板都是顶栏上的按钮呼出来的，**贴着顶栏**才看得出
        // 「是那个按钮开的」；原来 44px（窄屏 100px）那一截空白既不显示什么，又白吃掉面板
        // 的高度 —— 而软键条编辑器和设置这两页恰恰是最缺高度的。留 6px 是为了两条边框
        // 之间还看得出一道缝，不然面板顶边和顶栏的底边糊成一条。
        'absolute top-1.5 right-2.5 z-10 flex max-h-[calc(100%-24px)] w-[460px] flex-col',
        // 浮层的阴影要「大而淡」：短而黑的阴影在深色底上看不出层次，只会糊一圈脏边
        // overflow-hidden 是给「没有标题栏」那种准备的：里面第一行常常是个粘在顶上的
        // 工具条（自带 bg-bar），不裁的话它会画到圆角外面去，四个角看着就方了。
        // 里面真正滚的是下面那个 div，这一层裁掉不影响 sticky。
        'overflow-hidden rounded-card border border-line bg-bar shadow-[0_24px_60px_-16px_rgba(0,0,0,.75)]',
        'max-md:inset-x-2 max-md:w-auto',
        className,
      )}
    >
      {title && (
        <div className="flex shrink-0 items-center justify-between border-b border-line py-2.5 pl-4 pr-2.5">
          <strong className="text-sm font-medium tracking-tight">{title}</strong>
          <Button variant="ghost" size="icon" onClick={onClose} aria-label="关闭">
            <X className="size-4" />
          </Button>
        </div>
      )}
      <div className="overflow-auto overscroll-contain px-4 pt-2 pb-4">{children}</div>
    </aside>
  )
}
