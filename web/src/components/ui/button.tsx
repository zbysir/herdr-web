import * as React from 'react'
import { Slot } from '@radix-ui/react-slot'
import { cva, type VariantProps } from 'class-variance-authority'
import { cn } from '@/lib/utils'

const buttonVariants = cva(
  'inline-flex items-center justify-center gap-1.5 whitespace-nowrap rounded-[7px] font-[inherit] transition-colors ' +
    'disabled:opacity-45 disabled:pointer-events-none active:translate-y-px cursor-pointer',
  {
    variants: {
      variant: {
        default: 'border border-line bg-fg/6 text-fg hover:bg-fg/12',
        primary: 'bg-accent text-white border border-transparent hover:brightness-110',
        ghost: 'text-fg hover:bg-fg/10',
        danger: 'border border-line bg-fg/6 text-bad hover:bg-fg/12',
        /** 软键条上的大按键：手指点得中。手机竖屏上窄一号（见 size.key 那条注释） */
        key: 'border border-line bg-fg/8 text-fg font-mono min-w-11 max-phone:min-w-9 active:bg-accent active:border-transparent active:text-white',
      },
      size: {
        default: 'px-[11px] py-[5px] text-[13px]',
        tiny: 'px-[9px] py-[3px] text-xs',
        icon: 'px-2 py-[5px] min-w-8 text-[13px]',
        /**
         * 软键条的键。手机竖屏（< 440px）上字号和内边距各小一号：那一档软键条是
         * **一行横滑**的，键矮一点等于终端多一行，而横向省下来的那点宽度直接换成
         * 「一屏里能看见几个键」。35px 高 → 28px，仍在能点准的范围里。
         */
        key: 'px-3 py-2.5 text-[13px] leading-none max-phone:px-2 max-phone:py-[7px] max-phone:text-[11.5px]',
      },
      on: { true: '', false: '' },
    },
    compoundVariants: [
      { on: true, variant: 'default', class: 'bg-accent border-transparent text-white hover:brightness-110' },
      { on: true, variant: 'key', class: 'bg-accent border-transparent text-white' },
      { on: true, variant: 'icon' as never, class: '' },
    ],
    defaultVariants: { variant: 'default', size: 'default', on: false },
  },
)

export interface ButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement>,
    VariantProps<typeof buttonVariants> {
  asChild?: boolean
}

export const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant, size, on, asChild, ...props }, ref) => {
    const Comp = asChild ? Slot : 'button'
    return <Comp ref={ref} className={cn(buttonVariants({ variant, size, on }), className)} {...props} />
  },
)
Button.displayName = 'Button'
export { buttonVariants }
