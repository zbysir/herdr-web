import { cn } from '@/lib/utils'

export function Toast({ msg }: { msg: string | null }) {
  if (!msg) return null
  return (
    <div
      className={cn(
        'pointer-events-none absolute bottom-[18px] left-1/2 z-20 -translate-x-1/2 rounded-full',
        'border border-line bg-bar px-4 py-[7px] shadow-[0_8px_24px_rgba(0,0,0,.3)]',
      )}
    >
      {msg}
    </div>
  )
}
