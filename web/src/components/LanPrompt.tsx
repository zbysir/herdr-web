import { Button } from './ui/button'
import type { LanDirect } from '@/hooks/useLanDirect'

/**
 * 局域网直连的那条小横幅。两种话要说，见 hooks/useLanDirect.ts 的注释：
 *
 * - `ask`：探通了，问一次要不要切（同意之后就再也不问，静默切）
 * - `moved`：走过这条路，现在探不通，而且地址变了 —— 得让人去新地址上再点一次
 *   「继续访问」，否则这条路就永久静默失效，而且找不到原因
 *
 * 摆在底部：和 CopyPrompt / PastePrompt 同一个位置，拇指能到，也不会压住终端顶上
 * 那几行正在输出的东西。
 */
export function LanPrompt({
  state, onAccept, onDecline, onDismiss,
}: {
  state: LanDirect
  onAccept: () => void
  onDecline: () => void
  onDismiss: () => void
}) {
  if (state.kind === 'idle') return null
  const host = state.origin.replace(/^https?:\/\//, '')
  return (
    <div
      className="absolute bottom-4 left-1/2 z-30 w-[min(92%,460px)] -translate-x-1/2 rounded-lg border border-line
                 bg-bar/95 p-3 shadow-[0_16px_40px_-12px_rgba(0,0,0,.7)] backdrop-blur-md"
    >
      <div className="flex items-center gap-2">
        <p className="min-w-0 flex-1 text-xs/relaxed text-muted">
          {state.kind === 'ask' ? (
            <>
              这台设备就在 <span className="text-fg">{host}</span> 那个局域网里。
              切过去就不绕公网了，按键的往返会快一截。
            </>
          ) : (
            <>
              局域网地址变成了 <span className="text-fg">{host}</span>。
              开一次 <span className="text-fg">https://{host}/</span> 并点「继续访问」，之后就会自动走它。
            </>
          )}
        </p>
        {state.kind === 'ask' ? (
          <>
            <Button variant="primary" onClick={onAccept}>切过去</Button>
            {/* 「不用」是记住的 —— 问过一次就别再烦人（清 localStorage 能反悔） */}
            <Button variant="ghost" onClick={onDecline}>不用</Button>
          </>
        ) : (
          <Button variant="ghost" size="icon" title="知道了" onClick={onDismiss}>✕</Button>
        )}
      </div>
    </div>
  )
}
