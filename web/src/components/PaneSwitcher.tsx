import { useMemo, useState } from 'react'
import { RefreshCw, Search } from 'lucide-react'
import type { Pane } from '@/lib/api'
import { Panel } from './ui/panel'
import { Button } from './ui/button'
import { Input } from './ui/input'
import { usePhone } from '@/hooks/usePhone'
import { cn } from '@/lib/utils'

/**
 * 面板一览：**手机上换 pane 的那条路**。
 *
 * 为什么需要它：软键条发的是按键，而按键只能表达**相对**导航（下一个 tab、往右一格），
 * 「让 w5:p3 全屏」这句话说不出来。于是手机上换个 pane 得盲敲一串 —— 而中间每一步的
 * 屏幕，正好都是「未放大的多 pane 布局」那个在手机上读不了的状态。herdr 的 socket 这层
 * 是**按 pane_id 寻址**的（`pane.zoom` 带 pane_id 就能跨 workspace + tab 一次切过去），
 * 所以这里点一行就到。
 *
 * 它是**索引，不是第二个界面**：点完之后看的还是同一个 herdr 终端（网页接的是整个 TUI，
 * herdr 那边一切焦点，画面自己就跟过来了），键盘那套操作一个字都没变。所以不存在
 * 「手机一套习惯、电脑另一套」—— 这条是刻意的取舍，别把它做成能增删改 pane 的管理器，
 * 那就有了第二个真相源。
 *
 * 「点了就全屏」默认开：手机上多 pane 平铺根本读不了，去了不放大等于没去。平板横屏
 * （或者你就想看那个 tab 的分屏）时关掉它，那时候点一行是「切焦点 + 退出全屏」。
 */
const LS_ZOOM = 'panesZoom'
const LS_ONLY_AGENT = 'panesOnlyAgent'

/** 长路径显示成 ~/…：一行里 cwd 是最认得出 pane 的东西，但绝对路径太占宽度 */
const shortCwd = (p: string) => p.replace(/^\/(?:Users|home)\/[^/]+/, '~')

const DOT: Record<string, string> = {
  working: 'bg-brand',
  blocked: 'bg-bad',
  idle: 'bg-muted',
  done: 'bg-muted',
}

export function PaneSwitcher({
  panes, onClose, onGoto, onReload,
}: {
  panes: Pane[]
  onClose: () => void
  /** 跳过去（zoom=true 顺带全屏）。面板自己不等结果，交给上层 toast */
  onGoto: (id: string, zoom: boolean) => void
  onReload: () => void
}) {
  const phone = usePhone()
  const [q, setQ] = useState('')
  const [zoom, setZoom] = useState(() => localStorage.getItem(LS_ZOOM) !== '0')
  const [onlyAgent, setOnlyAgent] = useState(() => localStorage.getItem(LS_ONLY_AGENT) === '1')

  const groups = useMemo(() => {
    const kw = q.trim().toLowerCase()
    const hit = (p: Pane) =>
      (!onlyAgent || !!p.agent) &&
      (!kw || `${p.agent} ${p.workspace} ${p.tab} ${p.title} ${p.cwd} ${p.id}`.toLowerCase().includes(kw))
    // 按 workspace 分组，顺序沿用 pane.list（herdr 自己的顺序，用户对它有肌肉记忆）
    const out: { ws: string; items: Pane[] }[] = []
    for (const p of panes) {
      if (!hit(p)) continue
      const g = out.find((x) => x.ws === p.workspace)
      if (g) g.items.push(p)
      else out.push({ ws: p.workspace, items: [p] })
    }
    return out
  }, [panes, q, onlyAgent])

  const total = groups.reduce((n, g) => n + g.items.length, 0)

  const flip = (key: string, v: boolean, set: (v: boolean) => void) => {
    set(v)
    localStorage.setItem(key, v ? '1' : '0')
  }

  return (
    <Panel
      title="面板一览"
      onClose={onClose}
      // 一览是要扫的，比设置面板高一档、宽一档；手机上几乎铺满（48 个 pane 得能滚起来）
      className="w-[520px] max-h-[calc(100%-24px)] max-md:inset-x-2 max-md:top-3 max-md:w-auto"
    >
      {/* 这一排粘在顶上：滚到第三个 workspace 还得能改筛选（-top-2/-mt-2/pt-2 那三个一套，
          和设置面板的分页条同理 —— 外层滚动容器有 pt-2，只写 top-0 会漏一条缝） */}
      <div className="sticky -top-2 z-1 -mx-4 -mt-2 mb-2 flex flex-wrap items-center gap-1.5 border-b border-line bg-bar px-4 pt-2 pb-2">
        <div className="relative min-w-0 flex-1">
          <Search className="pointer-events-none absolute top-1/2 left-2 size-3 -translate-y-1/2 text-faint" />
          <Input
            data-testid="panes-filter"
            className="h-7 w-full pl-7"
            placeholder="筛 tab / 标题 / 路径"
            value={q}
            // 手机上不自动聚焦：一开面板就顶出键盘，而这儿多半是用手指扫、不是打字
            autoFocus={!phone}
            onChange={(e) => setQ(e.target.value)}
          />
        </div>
        <Button
          size="tiny"
          on={onlyAgent}
          title="只看跑着 agent 的 pane（claude / codex）"
          onClick={() => flip(LS_ONLY_AGENT, !onlyAgent, setOnlyAgent)}
        >
          Agent
        </Button>
        <Button
          size="tiny"
          on={zoom}
          data-testid="panes-zoom"
          title="点一行就把那个 pane 放大铺满（手机上多 pane 平铺读不了，去了不放大等于没去）"
          onClick={() => flip(LS_ZOOM, !zoom, setZoom)}
        >
          全屏
        </Button>
        <Button size="tiny" title="刷新列表" onClick={onReload}>
          <RefreshCw className="size-3" />
        </Button>
      </div>

      {total === 0 && (
        <p className="px-1 py-6 text-center text-xs text-faint">
          {panes.length === 0 ? '拿不到 pane 列表 —— herdr server 在跑吗？' : '没有匹配的 pane'}
        </p>
      )}

      {groups.map((g) => (
        <section key={g.ws} className="mb-1.5">
          <h4 className="px-1 pt-1.5 pb-1 text-[11px] font-medium tracking-wide text-faint">{g.ws}</h4>
          <div className="flex flex-col gap-0.5">
            {g.items.map((p) => (
              <button
                key={p.id}
                type="button"
                data-testid="panes-row"
                // min-h-11：一行至少 44px，手指点得中（这一条整块都是热区，不是只有文字）
                className={cn(
                  'flex min-h-11 w-full cursor-pointer items-center gap-2.5 rounded-md border border-transparent',
                  'px-2 py-1.5 text-left outline-none transition-colors duration-100',
                  'hover:border-line hover:bg-ctl focus-visible:ring-2 focus-visible:ring-brand/35',
                  p.focused && 'border-brand/40 bg-brand/10',
                )}
                onClick={() => onGoto(p.id, zoom)}
              >
                <span
                  title={p.agent ? `${p.agent} · ${p.status}` : 'shell'}
                  className={cn('size-1.5 shrink-0 rounded-full', DOT[p.status] ?? 'bg-line-hi')}
                />
                <span className="min-w-0 flex-1">
                  <span className="flex items-center gap-1.5">
                    <span className="truncate text-[13px]">{p.tab}</span>
                    {p.agent && (
                      <span className="shrink-0 rounded border border-line bg-ctl px-1 py-px font-mono text-[10px] text-muted">
                        {p.agent}
                      </span>
                    )}
                    {p.focused && (
                      <span className="shrink-0 rounded border border-brand/40 bg-brand/12 px-1 py-px text-[10px] text-brand">
                        当前
                      </span>
                    )}
                  </span>
                  {/* agent pane 的 terminal_title 是它自己写的会话标题（「图片识别」这种），
                      比路径认得出得多；shell pane 的标题只是 user@host:path，那还是给路径 */}
                  <span className="mt-px block truncate font-mono text-[11px] text-faint">
                    {(p.agent && p.title) || shortCwd(p.cwd) || p.id}
                  </span>
                </span>
                {/* pane id 在手机上**也要出**：一个 tab 里有两个 pane 时（herdr 里分屏），
                    两行的 tab 标签和 cwd 一模一样，id 是唯一分得开的东西（实拍见过） */}
                <span className="shrink-0 font-mono text-[10px] text-faint">{p.id}</span>
              </button>
            ))}
          </div>
        </section>
      ))}
    </Panel>
  )
}
