import * as React from 'react'
import { Slot } from '@radix-ui/react-slot'
import { cva, type VariantProps } from 'class-variance-authority'
import { cn } from '@/lib/utils'

/*
  按钮的分层（Supabase 那套）：
  - 常态是「抬高一层灰 + 一圈边框」，hover 只再抬一层，**不换色**；
  - 打开 / 选中（`on`）是「淡绿底 + 绿边 + 绿字」，不是把亮绿涂满 —— 顶栏上五六个
    图标同时可能是打开态，涂满的话整条栏全是色块，什么都不突出了；
  - 饱和填充只有 `primary`（一屏一个主操作）和粘滞修饰键（`on` + `key`，按下去了
    必须一眼看见）。
  - 焦点用一圈半透明绿的 ring，不用浏览器默认那道实心 outline（那道线会盖住边框，
    在深色底上像描歪了）。
*/
const buttonVariants = cva(
  'inline-flex items-center justify-center gap-1.5 whitespace-nowrap rounded-md font-[inherit] ' +
    'transition-[background-color,border-color,color,box-shadow] duration-100 cursor-pointer ' +
    'outline-none focus-visible:ring-2 focus-visible:ring-brand/35 ' +
    'disabled:opacity-45 disabled:pointer-events-none',
  {
    variants: {
      variant: {
        default: 'border border-line bg-ctl text-fg hover:border-line-hi hover:bg-ctl-hi',
        primary: 'border border-brand-line bg-brand-bg text-brand-fg hover:border-brand hover:bg-brand-line',
        ghost: 'text-muted hover:bg-ctl hover:text-fg',
        danger: 'border border-line bg-ctl text-bad hover:border-bad/45 hover:bg-bad/12',
        /** 二次确认举起来的那一下（「再点一次」）：红底红字，和常态的 danger 明显两回事 */
        destructive: 'border border-bad/55 bg-bad/18 text-bad hover:bg-bad/26',
        /** 软键条上的大按键：手指点得中。手机竖屏上窄一号（见 size.key 那条注释） */
        key:
          'border border-line bg-ctl text-fg font-mono min-w-11 max-phone:min-w-9 ' +
          'hover:border-line-hi hover:bg-ctl-hi ' +
          // 按下去那一瞬间给绿：触屏上没有 hover，这是唯一的「点到了」的反馈
          'active:translate-y-px active:border-brand/45 active:bg-brand/15 active:text-brand',
      },
      size: {
        default: 'px-3 py-1.5 text-[13px]',
        tiny: 'px-2.5 py-1.5 text-xs',
        icon: 'px-2 py-1.5 min-w-8 text-[13px]',
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
      { on: true, variant: 'default', class: 'border-brand/40 bg-brand/12 text-brand hover:border-brand/55 hover:bg-brand/18' },
      // 粘滞 Ctrl / Alt 和「键盘正弹着」：这是**状态**不是选项，要在一排键里跳出来
      { on: true, variant: 'key', class: 'border-brand bg-brand text-bg hover:border-brand hover:bg-brand' },
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
