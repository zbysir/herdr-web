// 原生 <select>：手机上会调起系统选择器（比自绘列表好用得多），
// 而这个项目的下拉全是「选一个 pane / 选一个预设」这类朴素场景。
import * as React from 'react'
import { cn } from '@/lib/utils'

export const Select = React.forwardRef<HTMLSelectElement, React.SelectHTMLAttributes<HTMLSelectElement>>(
  ({ className, ...props }, ref) => (
    <select
      ref={ref}
      className={cn(
        'rounded-[7px] border border-line bg-bg px-[9px] py-1.5 text-xs text-fg font-mono cursor-pointer',
        'focus:outline focus:outline-accent focus:border-accent',
        className,
      )}
      {...props}
    />
  ),
)
Select.displayName = 'Select'
