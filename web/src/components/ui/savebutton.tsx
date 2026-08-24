import { useEffect, useRef, useState } from 'react'
import { Check, X } from 'lucide-react'
import { Button, type ButtonProps } from './button'
import { cn } from '@/lib/utils'

/** ✔ / ✕ 举多久（毫秒）。成了看一眼就够，没成的那条要留得住看清 */
const DONE_MS = 1600
const FAIL_MS = 2600

/**
 * 「保存」这类按钮：**成没成由按钮自己说**，不靠右下角那条 toast。
 *
 * 为什么不靠 toast（原来就是那样）：toast 挂在终端那一层、底部居中，而保存按钮在面板
 * **顶上**那一行 —— 桌面上面板靠右、提示在整屏正下方；手机上面板几乎铺满，提示压在最底
 * 那条缝里。人的眼睛在刚点的那个按钮上，反馈却在半屏之外，于是「点了跟没点一样」
 * （用户报的）。反馈要出现在**手指刚离开的地方**。
 *
 * 没成也要举一下（✕）：只给成功状态的话，失败又退回一个一模一样的「保存」——「点了没反应」
 * 那个毛病原样回来了。**原因**照旧留在面板里那行红字上（服务端会指出是第几个按键、哪里
 * 不认），按钮只负责「这一下到底算不算」。
 *
 * 所以约定：`onSave` **成了返回 true、没成返回 false**，别把异常抛到这儿 —— 抛过来
 * 按钮也只能显示「没成」，而那行原因就丢了。
 */
export interface SaveButtonProps extends Omit<ButtonProps, 'onClick'> {
  onSave: () => Promise<boolean>
}

export function SaveButton({
  onSave, children, className, disabled, variant = 'primary', ...rest
}: SaveButtonProps) {
  const [phase, setPhase] = useState<'idle' | 'busy' | 'done' | 'fail'>('idle')
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  // 面板可以在请求飞着的时候被关掉（Esc、点 ×），计时器得跟着走
  useEffect(() => () => clearTimeout(timer.current), [])

  const run = async () => {
    if (phase === 'busy') return
    clearTimeout(timer.current)
    setPhase('busy')
    const ok = await onSave()
    setPhase(ok ? 'done' : 'fail')
    timer.current = setTimeout(() => setPhase('idle'), ok ? DONE_MS : FAIL_MS)
  }

  const shown = phase === 'done' || phase === 'fail'
  return (
    <Button
      {...rest}
      variant={phase === 'fail' ? 'destructive' : variant}
      // 举着 ✔ / ✕ 的那一下也不让点（连点两下的第二下毫无意义），但**不能跟着变灰**：
      // disabled 自带 opacity-45，那正好把要人看见的这一下压暗了
      disabled={disabled || phase !== 'idle'}
      className={cn(shown && 'disabled:opacity-100', className)}
      onClick={() => void run()}
    >
      {phase === 'done' && <Check className="size-3.5" strokeWidth={2.5} />}
      {phase === 'fail' && <X className="size-3.5" strokeWidth={2.5} />}
      {phase === 'done' ? '已保存' : phase === 'fail' ? '没存上' : phase === 'busy' ? '存着…' : children}
    </Button>
  )
}
