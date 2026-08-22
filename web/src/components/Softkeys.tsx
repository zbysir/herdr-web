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
 * 按键最多分**两行**（几行是设置），**每行各自横向滚动** —— 手机上一行只放得下四五个键，
 * 而「最常用那几个」和「次常用那几个」各占一行、各滑各的，比十几个键排成一条长龙好找：
 * 手指知道自己在哪一行，滑动也不会把另一行带跑。
 *
 * 收到的是**已经解析好的每行**（`bar`）：同一个键可以在两行里各出现一次，所以这里认
 * 「第几行第几个」，不认「keys 里的第几个」。
 *
 * 按键由 /api/softkeys 下发（存服务端；**定义所有设备共用一份，几行 / 哪些键在条上按
 * 「排布」分**，见 internal/profiles），在设置 →「软键条」页改。以前这一条右下角还挂着一个直通那一页的 ⚙，去掉了 —— 顶栏已经有一个设置入口，
 * 而这个位置每换一次朝向都跟键抢地方。
 *
 * 手机没有 Ctrl 键，herdr 的 ctrl+b 前缀全靠这条。
 *
 * 打了 `confirm` 的键要点**两下**：第一下只是举起来（变红），第二下才真发出去。
 * 键挨得这么近，关 pane / 关标签这种误触一下就没了，而 herdr 那边没有撤销。
 */
export function Softkeys({
  bar, sticky, kbdUp, notice, onSend, onSticky, onKeyboard, onImage, onPanes, onFiles, onClip, onPaste,
}: {
  /** 每行的按键（已按 id 解析好）。一到两行 */
  bar: SoftKey[][]
  sticky: { ctrl: boolean; alt: boolean }
  kbdUp: boolean
  onSend: (bytes: string) => void
  onSticky: (which: 'ctrl' | 'alt') => void
  onKeyboard: () => void
  /** act:img 的键：弹相机 / 相册（路径去哪儿由 App 决定） */
  onImage: () => void
  /** act:panes 的键：开「面板一览」。手机上键盘一弹起来顶栏就收掉了，那时候只有这条路 */
  onPanes: () => void
  /**
   * 还有几条提示没看过（`act:panes` 那个键右上角挂个数字角标）。0 = 不挂。
   *
   * 顶栏那个 ▦ 上已经有一个了，这儿还要一个是因为**手机上键盘一弹起来顶栏整条就收掉**
   * （见 App 里的 barHidden）——而那正是你在跟 agent 说话、最该知道「另一个在等你」的时候。
   */
  notice?: number
  /** act:files 的键：开文件浏览（看 agent 生成的图）。同样是键盘弹起时唯一的入口 */
  onFiles: () => void
  /**
   * act:clip：把**跑 herdr 那台机器**的剪贴板取到手机剪贴板（herdr 的复制落在那儿）。
   * act:paste：把手机剪贴板粘进终端。
   *
   * 这两个只能是「用户点的键」——浏览器只在用户手势里给读写剪贴板，定时器或者事件里
   * 偷偷做一律被拒（而且是静默的）。
   */
  onClip: () => void
  onPaste: () => void
}) {
  const phone = usePhone()

  // 举着的那个键（"行:位置"）。同一个键再点一下才真发，点别的键 / 等超时都算放下。
  // 用坐标而不是下标：同一个定义可能在两行各有一个，举起来的必须是**手指点的那个**。
  const [armed, setArmed] = useState<string | null>(null)
  const armTimer = useRef<number | undefined>(undefined)
  const disarm = () => {
    clearTimeout(armTimer.current)
    setArmed(null)
  }
  useEffect(() => () => clearTimeout(armTimer.current), [])

  // 按键改了（在编辑器里存了一版）就别接着举着上一版的位置 —— 那个位置现在
  // 可能已经是别的键了，接着点就点错了东西。
  useEffect(disarm, [bar])

  return (
    <>
      {bar.map((row, ri) => row.length > 0 && (
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
          {row.map((k, i) => {
            const at = `${ri}:${i}`
            const on = k.sticky ? sticky[k.sticky] : k.act === 'kbd' ? kbdUp : false
            const up = armed === at   // 举起来了，等第二下
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
                  'relative',
                  k.wide && 'min-w-[78px]',
                  up && 'border-bad bg-bad text-white hover:border-bad hover:bg-bad',
                )}
                title={up ? '再点一次才真的发出去' : (k.spec || k.sticky || k.act || '') + (k.confirm ? '（要点两下）' : '')}
                // 这一个不能顺手 focus 终端，否则没法收起键盘
                onMouseDown={(e) => e.preventDefault()}
                onClick={() => {
                  if (k.confirm && !up) {
                    clearTimeout(armTimer.current)
                    setArmed(at)
                    armTimer.current = window.setTimeout(() => setArmed(null), CONFIRM_MS)
                    return
                  }
                  disarm()   // 点别的键 = 把举着的那个放下，但这一下照样算数
                  if (k.act === 'kbd') onKeyboard()
                  else if (k.act === 'img') onImage()
                  else if (k.act === 'panes') onPanes()
                  else if (k.act === 'files') onFiles()
                  else if (k.act === 'clip') onClip()
                  else if (k.act === 'paste') onPaste()
                  else if (k.sticky) onSticky(k.sticky)
                  else if (k.send) onSend(k.send)
                }}
              >
                {k.label}
                {/* ring 用面板底色，让角标看着像贴在键上的徽标而不是浮在半空 */}
                {!!notice && k.act === 'panes' && (
                  <span className="absolute -top-1 -right-1 grid h-4 min-w-4 place-items-center rounded-full bg-bad px-1
                                   font-mono text-[10px]/none font-medium text-white ring-2 ring-bar tabular-nums">
                    {notice > 9 ? '9+' : notice}
                  </span>
                )}
              </Button>
            )
          })}
        </div>
      ))}
    </>
  )
}
