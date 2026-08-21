import * as React from 'react'
import { cn } from '@/lib/utils'

export const Textarea = React.forwardRef<HTMLTextAreaElement, React.TextareaHTMLAttributes<HTMLTextAreaElement>>(
  ({ className, ...props }, ref) => (
    <textarea
      ref={ref}
      className={cn(
        'w-full resize-y rounded-md border border-line bg-bg px-2.5 py-2 text-sm/relaxed text-fg font-mono',
        'outline-none transition-[border-color,box-shadow] duration-100',
        'placeholder:text-faint hover:border-line-hi focus:border-brand/70 focus:ring-2 focus:ring-brand/15',
        className,
      )}
      {...props}
    />
  ),
)
Textarea.displayName = 'Textarea'
