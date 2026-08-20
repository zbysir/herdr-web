import * as React from 'react'
import { cn } from '@/lib/utils'

export const Input = React.forwardRef<HTMLInputElement, React.InputHTMLAttributes<HTMLInputElement>>(
  ({ className, ...props }, ref) => (
    <input
      ref={ref}
      className={cn(
        'rounded-[7px] border border-line bg-bg px-[9px] py-1.5 text-xs text-fg font-mono',
        'placeholder:text-muted focus:outline focus:outline-accent focus:border-accent',
        className,
      )}
      {...props}
    />
  ),
)
Input.displayName = 'Input'
