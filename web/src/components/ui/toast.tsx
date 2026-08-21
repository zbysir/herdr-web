import { cn } from '@/lib/utils'

export function Toast({ msg }: { msg: string | null }) {
  if (!msg) return null
  return (
    <div
      className={cn(
        'pointer-events-none absolute bottom-5 left-1/2 z-20 max-w-[min(90%,420px)] -translate-x-1/2',
        // 卡片而不是胶囊：提示经常是一整句话，胶囊在长文本下会拉成一条很怪的赛道
        'rounded-lg border border-line bg-bar/95 px-3.5 py-2 text-center backdrop-blur-md',
        'shadow-[0_16px_40px_-12px_rgba(0,0,0,.7)]',
      )}
    >
      {msg}
    </div>
  )
}
