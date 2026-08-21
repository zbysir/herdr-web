import { useEffect, useRef, useState } from 'react'
import { writeClipboard } from '@/lib/clipboard'
import { Button } from './ui/button'

/**
 * 「点一下复制」。
 *
 * 手机浏览器只在**用户手势**里允许写剪贴板，而终端里触发复制的两条路都不是点击
 * （herdr 的 COPY 模式按 `y` 走 OSC 52、「选中即复制」走选区变化），所以那两条在手机上
 * 必然写不进去。以前是静默失败：选区好好的、什么提示都没有，剪贴板里还是上一次的东西
 * （见 lib/clipboard.ts 的包注释）。
 *
 * 这个条子就是把那一下手势补回来 —— 按钮上的点击本身就是手势，这时候浏览器才给写。
 * 再不行（局域网 http 上连 `navigator.clipboard` 都没有、`execCommand` 也被拒）就把文本
 * 摊在一个框里、**替你全选好**，长按选「拷贝」是最后一条路。
 *
 * 摆在底部而不是中间：手要点它，而屏幕下半边是拇指能到的地方。
 */
export function CopyPrompt({
  text, onCopied, onClose,
}: {
  text: string
  onCopied: () => void
  onClose: () => void
}) {
  const [manual, setManual] = useState(false)
  const box = useRef<HTMLTextAreaElement>(null)

  // 摊开那一步就把内容选好：长按之后菜单里直接是「拷贝」，不用先自己划一遍
  useEffect(() => {
    if (!manual) return
    const el = box.current
    if (!el) return
    el.focus()
    el.setSelectionRange(0, el.value.length)
  }, [manual])

  const tryCopy = async () => {
    if (await writeClipboard(text)) onCopied()
    else setManual(true)
  }

  const chars = [...text].length
  return (
    <div
      className="absolute bottom-4 left-1/2 z-30 w-[min(92%,460px)] -translate-x-1/2 rounded-lg border border-line
                 bg-bar/95 p-3 shadow-[0_16px_40px_-12px_rgba(0,0,0,.7)] backdrop-blur-md"
    >
      <div className="flex items-center gap-2">
        <p className="min-w-0 flex-1 text-xs/relaxed text-muted">
          {manual
            ? '这个浏览器不给写剪贴板。长按下面框里的字 → 选「拷贝」。'
            // 不能说「选了多少字」：OSC 52 那条路（herdr 的 COPY 模式）压根没有选区，
            // 是程序把文本推过来的
            : `${chars} 个字等着复制 —— 浏览器要求这一下由点击发起。`}
        </p>
        {!manual && (
          <Button variant="primary" onClick={() => void tryCopy()}>复制</Button>
        )}
        <Button variant="ghost" size="icon" title="关掉" onClick={onClose}>✕</Button>
      </div>

      {manual && (
        // 字号 16px 是给 iOS 的：小于 16 它会顺手把整个页面缩放一下
        <textarea
          ref={box}
          readOnly
          value={text}
          className="mt-2 h-24 w-full resize-none rounded-md border border-line bg-ctl px-2 py-1.5
                     font-mono text-[16px]/snug text-fg outline-none"
        />
      )}
    </div>
  )
}
