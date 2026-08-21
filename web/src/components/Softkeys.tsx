import { useEffect, useRef, useState } from 'react'
import { Button } from './ui/button'
import type { SoftKey } from '@/lib/api'
import { usePhone } from '@/hooks/usePhone'
import { cn } from '@/lib/utils'

/**
 * 二次确认的键「举起来」之后多久自动放下（ms）。
 * 太短来不及看第二眼，太长就会忘了自己举过 —— 回头随手一点反而正好点实。
 */
const CONFIRM_MS = 3000

/**
 * 软键条：**只出键本身**，外壳（边框 / 宽度 / 高度 / 把手）归底部面板管（见 Dock）。
 *
 * 按键最多分**两行**（`rows` 是设置，每个键的 `row` 说它在第几行），**每行各自横向滚动**
 * —— 手机上一行只放得下四五个键，而「最常用那几个」和「次常用那几个」各占一行、各滑各的，
 * 比十几个键排成一条长龙好找：手指知道自己在哪一行，滑动也不会把另一行带跑。
 *
 * `off` 的键只在键库里（编辑器下面那片），这儿直接跳过。
 *
 * 按键由 /api/softkeys 下发（存服务端，手机 / 平板 / 电脑共用一份），在设置 →「软键条」
 * 页改。以前这一条右下角还挂着一个直通那一页的 ⚙，去掉了 —— 顶栏已经有一个设置入口，
 * 而这个位置每换一次朝向都跟键抢地方。
 *
 * 手机没有 Ctrl 键，herdr 的 ctrl+b 前缀全靠这条。
 *
 * 打了 `confirm` 的键要点**两下**：第一下只是举起来（变红），第二下才真发出去。
 * 键挨得这么近，关 pane / 关标签这种误触一下就没了，而 herdr 那边没有撤销。
 */
export function Softkeys({
  keys, rows, sticky, kbdUp, onSend, onSticky, onKeyboard, onImage,
}: {
  keys: SoftKey[]
  /** 软键条几行（服务端存的设置，1 或 2） */
  rows: 1 | 2
  sticky: { ctrl: boolean; alt: boolean }
  kbdUp: boolean
  onSend: (bytes: string) => void
  onSticky: (which: 'ctrl' | 'alt') => void
  onKeyboard: () => void
  /** act:img 的键：弹相机 / 相册（路径去哪儿由 App 决定） */
  onImage: () => void
}) {
  const phone = usePhone()

  // 举着的那个键（下标）。同一个键再点一下才真发，点别的键 / 等超时都算放下。
  const [armed, setArmed] = useState<number | null>(null)
  const armTimer = useRef<number | undefined>(undefined)
  const disarm = () => {
    clearTimeout(armTimer.current)
    setArmed(null)
  }
  useEffect(() => () => clearTimeout(armTimer.current), [])

  // 按键改了（在编辑器里存了一版）就别接着举着上一版的下标 —— 那个位置现在
  // 可能已经是别的键了，接着点就点错了东西。
  useEffect(disarm, [keys])

  // 分行时**带着原下标**：armed 记的是 keys 里的位置，跨行也得对得上
  const lanes = ([1, 2] as const)
    .slice(0, rows)
    .map((r) => keys.map((k, i) => [k, i] as const).filter(([k]) => !k.off && (k.row ?? 1) === r))

  return (
    <>
      {lanes.map((row, ri) => row.length > 0 && (
        <div
          key={ri}
          data-testid={`softkeys-row-${ri + 1}`}
          className={cn(
            'flex min-w-0 gap-1.5 overscroll-contain [scrollbar-width:none] [&::-webkit-scrollbar]:hidden',
            // 手机：一行不换行、自己横滑（滚动条不出，会盖住键）
            // 宽屏：照旧换行排，放不下的部分由外面那层上下滚
            phone ? 'shrink-0 flex-nowrap overflow-x-auto' : 'flex-wrap content-start',
          )}
        >
          {row.map(([k, i]) => {
            const on = k.sticky ? sticky[k.sticky] : k.act === 'kbd' ? kbdUp : false
            const up = armed === i   // 举起来了，等第二下
            return (
              <Button
                key={i}
                data-testid={up ? 'softkey-armed' : undefined}
                variant="key"
                size="key"
                on={on}
                // 举起来只换颜色，**不换文字**：改字会让按键变宽，手指底下的键
                // 当场挪位置，第二下就点到隔壁去了。
                className={cn(
                  k.wide && 'min-w-[78px]',
                  up && 'border-transparent bg-bad text-white',
                )}
                title={up ? '再点一次才真的发出去' : (k.spec || k.sticky || k.act || '') + (k.confirm ? '（要点两下）' : '')}
                // 这一个不能顺手 focus 终端，否则没法收起键盘
                onMouseDown={(e) => e.preventDefault()}
                onClick={() => {
                  if (k.confirm && !up) {
                    clearTimeout(armTimer.current)
                    setArmed(i)
                    armTimer.current = window.setTimeout(() => setArmed(null), CONFIRM_MS)
                    return
                  }
                  disarm()   // 点别的键 = 把举着的那个放下，但这一下照样算数
                  if (k.act === 'kbd') onKeyboard()
                  else if (k.act === 'img') onImage()
                  else if (k.sticky) onSticky(k.sticky)
                  else if (k.send) onSend(k.send)
                }}
              >
                {k.label}
              </Button>
            )
          })}
        </div>
      ))}
    </>
  )
}
