import { useEffect, useRef, useState } from 'react'
import { Button } from './ui/button'

/**
 * 「长按这儿粘贴」——手机剪贴板读不到时的那条路。
 *
 * 浏览器只在用户手势里给读剪贴板，而且 Chrome 还会额外弹一次确认；不是安全上下文
 * （局域网 http）的话压根没有 `readText`。这几种情况下唯一还通的是**浏览器自己的
 * 长按菜单** —— 那是系统菜单，不受这套权限管。所以这里给一个空框：长按 → 粘贴 →
 * 「发送」，文本按括号粘贴送进终端。
 *
 * 框是空的、而且自动聚焦：手机上少一步点击就少一次「点不中」。
 */
export function PastePrompt({
  onSend, onClose,
}: {
  onSend: (text: string) => void
  onClose: () => void
}) {
  const [text, setText] = useState('')
  const box = useRef<HTMLTextAreaElement>(null)

  useEffect(() => box.current?.focus(), [])

  return (
    <div
      className="absolute bottom-4 left-1/2 z-30 w-[min(92%,460px)] -translate-x-1/2 rounded-lg border border-line
                 bg-bar/95 p-3 shadow-[0_16px_40px_-12px_rgba(0,0,0,.7)] backdrop-blur-md"
    >
      <div className="flex items-center gap-2">
        <p className="min-w-0 flex-1 text-xs/relaxed text-muted">
          读不到手机剪贴板。<strong className="font-medium text-fg">长按</strong>下面的框 →
          「粘贴」，然后按发送。
        </p>
        <Button
          variant="primary"
          disabled={!text}
          onClick={() => { onSend(text); onClose() }}
        >
          发送
        </Button>
        <Button variant="ghost" size="icon" title="关掉" onClick={onClose}>✕</Button>
      </div>
      {/* 16px 字号是给 iOS 的：小于 16 它会顺手把整个页面缩放一下 */}
      <textarea
        ref={box}
        value={text}
        onChange={(e) => setText(e.target.value)}
        placeholder="长按 → 粘贴"
        className="mt-2 h-24 w-full resize-none rounded-md border border-line bg-ctl px-2 py-1.5
                   font-mono text-[16px]/snug text-fg outline-none placeholder:text-faint"
      />
    </div>
  )
}
