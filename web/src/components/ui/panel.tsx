import type * as React from 'react'
import { X } from 'lucide-react'
import { cn } from '@/lib/utils'
import { Button } from './button'

/** 顶栏下面浮出来的面板。窄屏时铺满宽度。 */
export function Panel({
  title, onClose, children, className,
}: { title: string; onClose: () => void; children: React.ReactNode; className?: string }) {
  return (
    <aside
      data-testid="panel"
      className={cn(
        'absolute top-11 right-2.5 z-10 flex max-h-[calc(100%-60px)] w-[460px] flex-col',
        'rounded-card border border-line bg-bar shadow-[0_12px_34px_rgba(0,0,0,.35)]',
        'max-md:inset-x-2 max-md:top-[100px] max-md:w-auto',
        className,
      )}
    >
      <div className="flex shrink-0 items-center justify-between border-b border-line py-2 pl-3 pr-2">
        <strong className="font-semibold">{title}</strong>
        <Button variant="ghost" size="icon" onClick={onClose} aria-label="关闭">
          <X className="size-4" />
        </Button>
      </div>
      <div className="overflow-auto overscroll-contain px-3 pb-3 pt-1">{children}</div>
    </aside>
  )
}
