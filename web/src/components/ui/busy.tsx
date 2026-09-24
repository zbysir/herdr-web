import type { ReactNode } from 'react'
import { LoaderCircle } from 'lucide-react'

/**
 * 键 / 按钮上的「正在连接终端」：原来那张脸**留着占位但隐身**，转圈叠在正中。
 *
 * 不换成别的字、不换尺寸 —— 键一变宽，手指底下的键当场挪位置（同「举起来只换颜色」那条）。
 */
export function BusyFace({ busy, children }: { busy?: boolean; children: ReactNode }) {
  if (!busy) return <>{children}</>
  return (
    <>
      <span className="invisible inline-flex items-center">{children}</span>
      <span className="absolute inset-0 flex items-center justify-center" aria-label="正在连接终端">
        <LoaderCircle className="size-4 animate-spin" />
      </span>
    </>
  )
}
