import * as React from 'react'
import { cn } from '@/lib/utils'

/* 输入框比面板**暗**一档（bg 是画布色）：凹进去的那一档在暗色里就是「这儿能填字」。
   焦点不用浏览器默认的实心 outline，改成绿边 + 一圈很淡的绿光 —— 实心 outline 会
   压在边框上，看着像描歪了。 */
export const Input = React.forwardRef<HTMLInputElement, React.InputHTMLAttributes<HTMLInputElement>>(
  ({ className, ...props }, ref) => (
    <input
      ref={ref}
      className={cn(
        'rounded-md border border-line bg-bg px-2.5 py-1.5 text-xs text-fg font-mono',
        'outline-none transition-[border-color,box-shadow] duration-100',
        'placeholder:text-faint hover:border-line-hi focus:border-brand/70 focus:ring-2 focus:ring-brand/15',
        className,
      )}
      {...props}
    />
  ),
)
Input.displayName = 'Input'
