import { useEffect } from 'react'
import { X } from 'lucide-react'
import type { Notice } from '@/lib/api'
import { ago } from './PaneSwitcher'
import { MAX_CARDS } from '@/hooks/useNotices'
import { cn } from '@/lib/utils'

/**
 * 右上角的提示：某个 agent **等你回答**了，或者**跑完了**。
 *
 * 为什么要这么一块东西：这个页面里 herdr 只有一个 pane 是看得见的（手机上更是只有
 * 全屏那一个），而正在跑的 agent 常常有十几个。以前要发现「那个在等我」只能自己去开
 * 面板一览翻 —— 于是 agent 停在一个 y/n 上等半小时是常事。
 *
 * **卡上不贴那段话**（服务端照旧抽，见下）——它只回答「谁、什么事」：某个 agent 在等你 /
 * 跑完了，点一下过去看。
 *
 * 这一条改过一次，理由是用下来的三点（用户报的），三点是叠在一起的：
 *
 *   - **读屏抽出来的话经常不准**：那是从 TUI 画面上扒的（`internal/agentwatch/extract.go`），
 *     选择框、进度条、被折断的行都可能混进来，而一段**看着像内容、其实是错的**摘要比没有
 *     摘要糟 —— 你会照着它做判断；
 *   - **卡上那点地方本来就放不全**（右上角一张卡、最多五行，长问题一律截断）；
 *   - **反正每次都要点进去看**。既然如此，那五行的代价就是白付的：它压在终端右上角，
 *     盖住的正是 agent 刚写的那几行。
 *
 * 所以卡片退回它真正不可替代的那件事：**你不知道另外十几个 pane 里发生了什么**，
 * 而这一张告诉你「那边有事了」。内容去它自己那儿读。
 *
 * （`Notice.text` 服务端照旧发、也照旧要抽 —— 去重靠它认「投了又按 Esc」那一档，
 * 见 internal/agentwatch/notice.go 的 lastText。别因为界面不画就把那一层拆了。）
 *
 * 点一下卡片 = 跳到那个 pane（顺带全屏，和面板一览点一行一样）。
 *
 * **「等你回答」那种不自动消失**：它是真的停在那儿等你，自己飘走了就又回到「不知道谁在等」
 * 的状态。跑完了那种十几秒后自己收掉 —— 那只是通知，错过也不影响什么。
 */

/**
 * 「跑完了」那种挂多久自己收掉（ms）。**在设置里可调**（0 = 一直挂着）。
 *
 * 「等你回答」那种不受这个数管，永远挂着：它是真的停在那儿等你，自己飘走了就又回到
 * 「不知道谁在等」的状态 —— 而这正是这个功能要解决的那件事。
 */
export const AUTO_MS_DEFAULT = 12_000

/**
 * 状态怎么说。颜色和面板一览那一列点是同一套：**红 = 等你，绿 = 跑完了**。
 * 别在这儿另发明一套配色 —— 同一件事在两个地方不同色，比没有颜色还糟。
 */
const CHIP: Record<string, { text: string; chip: string; card: string }> = {
  blocked: {
    text: '等你回答',
    chip: 'border-bad/50 bg-bad/15 text-bad',
    card: 'border-bad/40',
  },
  done: {
    text: '跑完了',
    chip: 'border-brand/40 bg-brand/12 text-brand',
    card: 'border-line',
  },
}
const chipOf = (status: string) => CHIP[status] ?? CHIP.done

export function Notices({
  items, hidden, autoMs = AUTO_MS_DEFAULT, onGoto, onDismiss, onMore,
}: {
  /** 老的在前，新的在后（和 useNotices 给的顺序一致） */
  items: Notice[]
  /** 有面板开着时先让开：那些面板就在同一个角上 */
  hidden?: boolean
  /** 「跑完了」那种挂多久（ms）；0 = 一直挂着。设置里调 */
  autoMs?: number
  /** 跳过去（seq 是这条提示 —— 跳完把它收掉，已经去看了，留着只挡视线） */
  onGoto: (paneID: string, seq: number) => void
  onDismiss: (seq: number) => void
  /** 「还有 N 条」点开：交给面板一览（那儿是看全部变化的地方） */
  onMore: () => void
}) {
  if (hidden || !items.length) return null
  /*
   * 排序和面板一览那份「优先级」是同一条规矩：**等你回答的在最上面**，然后才按新旧。
   *
   * 不按纯时间排，是因为两种卡的寿命不一样：「跑完了」十几秒就自己走了，而「等你回答」
   * 一直挂着 —— 按时间排的话，一条刚来的「跑完了」会把那个真的在等你的顶下去，
   * 而它恰恰是唯一需要你动手的那张。
   */
  const show = [...items]
    .sort((a, b) => Number(b.status === 'blocked') - Number(a.status === 'blocked') || b.seq - a.seq)
    .slice(0, MAX_CARDS)
  const more = items.length - show.length

  return (
    <div
      data-testid="notices"
      // pointer-events-none：这一摞浮在终端上面，没卡片的地方要能点到终端
      className="pointer-events-none absolute top-2.5 right-2.5 z-20 flex w-[min(360px,calc(100%-20px))] flex-col gap-1.5"
    >
      {show.map((n) => (
        <Card key={n.seq} n={n} autoMs={autoMs} onGoto={onGoto} onDismiss={onDismiss} />
      ))}
      {more > 0 && (
        <button
          type="button"
          className="pointer-events-auto self-end rounded-md border border-line bg-bar/95 px-2 py-1 text-[11px] text-muted backdrop-blur-md hover:text-fg"
          onClick={onMore}
        >
          还有 {more} 条 · 去面板一览
        </button>
      )}
    </div>
  )
}

function Card({
  n, autoMs, onGoto, onDismiss,
}: {
  n: Notice
  autoMs: number
  onGoto: (paneID: string, seq: number) => void
  onDismiss: (seq: number) => void
}) {
  const c = chipOf(n.status)

  useEffect(() => {
    if (n.status === 'blocked' || autoMs <= 0) return // 等你回答的 / 设成「一直挂着」的不收
    const t = setTimeout(() => onDismiss(n.seq), autoMs)
    return () => clearTimeout(t)
  }, [n.seq, n.status, autoMs, onDismiss])

  return (
    <div
      data-testid="notice"
      className={cn(
        'pointer-events-auto relative rounded-card border bg-bar/95 backdrop-blur-md',
        'shadow-[0_16px_40px_-12px_rgba(0,0,0,.7)] animate-[notice-in_.18s_ease-out]',
        c.card,
      )}
    >
      <button
        type="button"
        className="block w-full cursor-pointer px-3 py-2 pr-8 text-left outline-none focus-visible:ring-2 focus-visible:ring-brand/35 rounded-card"
        title={`跳到 ${n.pane}（顺带全屏）`}
        onClick={() => onGoto(n.pane, n.seq)}
      >
        <span className="flex items-center gap-1.5">
          <span className={cn('shrink-0 rounded border px-1 py-px text-[10px]', c.chip)}>{c.text}</span>
          {n.agent && (
            <span className="shrink-0 rounded border border-line bg-ctl px-1 py-px font-mono text-[10px] text-muted">
              {n.agent}
            </span>
          )}
          <span className="truncate text-[12px] text-muted">{n.title || n.pane}</span>
          <span className="ml-auto shrink-0 font-mono text-[10px] text-faint">{ago(n.at)}</span>
        </span>

        {/* 这儿原来贴那段读屏抽出来的话，去掉了（理由见文件头：常常不准、放不全、反正都要
            点进去）。**服务端那一层别跟着拆** —— 去重要用它 */}
        <span className="mt-1 block truncate font-mono text-[10px] text-faint">{n.pane} · 点击跳转</span>
      </button>

      <button
        type="button"
        aria-label="收起这条"
        title="收起这条（不算看过，面板图标上的红点不灭）"
        className="absolute top-1.5 right-1.5 cursor-pointer rounded p-1 text-faint hover:bg-ctl hover:text-fg"
        onClick={() => onDismiss(n.seq)}
      >
        <X className="size-3.5" />
      </button>
    </div>
  )
}
