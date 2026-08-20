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
      'size-4 shrink-0 rounded-[4px] border border-line bg-bg cursor-pointer',
      'data-[state=checked]:bg-accent data-[state=checked]:border-transparent data-[state=checked]:text-white',
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
