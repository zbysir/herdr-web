import * as React from 'react'
import { cn } from '@/lib/utils'

export const Textarea = React.forwardRef<HTMLTextAreaElement, React.TextareaHTMLAttributes<HTMLTextAreaElement>>(
  ({ className, ...props }, ref) => (
    <textarea
      ref={ref}
      className={cn(
        'w-full resize-y rounded-[7px] border border-line bg-bg px-2.5 py-2 text-sm/relaxed text-fg font-mono',
        'placeholder:text-muted focus:outline focus:outline-accent focus:border-accent',
        className,
      )}
      {...props}
    />
  ),
)
Textarea.displayName = 'Textarea'
