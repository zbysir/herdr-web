import * as React from 'react'
import * as C from '@radix-ui/react-checkbox'
import { Check } from 'lucide-react'
import { cn } from '@/lib/utils'

export const Checkbox = React.forwardRef<
  React.ComponentRef<typeof C.Root>,
  React.ComponentPropsWithoutRef<typeof C.Root>
>(({ className, ...props }, ref) => (
  <C.Root
    ref={ref}
    className={cn(
      'size-4 shrink-0 rounded-[5px] border border-line-hi bg-bg cursor-pointer',
      'outline-none transition-[background-color,border-color] duration-100',
      'hover:border-brand/50 focus-visible:ring-2 focus-visible:ring-brand/35',
      // 勾上是唯一该用饱和绿的地方：一排复选框里要能扫一眼看出哪几个开着
      'data-[state=checked]:border-brand data-[state=checked]:bg-brand data-[state=checked]:text-bg',
      className,
    )}
    {...props}
  >
    <C.Indicator className="flex items-center justify-center">
      <Check className="size-3" strokeWidth={3} />
    </C.Indicator>
  </C.Root>
))
Checkbox.displayName = 'Checkbox'
