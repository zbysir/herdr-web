import { useEffect, useMemo, useRef, useState, type MouseEvent, type PointerEvent } from 'react'
import { Search, X } from 'lucide-react'
import type { Pane } from '@/lib/api'
import { Panel } from './ui/panel'
import { Button } from './ui/button'
import { Input } from './ui/input'
import { cn } from '@/lib/utils'

/**
 * 面板一览：**手机上换 pane 的那条路**。
 *
 * 为什么需要它：快捷键条发的是按键，而按键只能表达**相对**导航（下一个 tab、往右一格），
 * 「让 w5:p3 全屏」这句话说不出来。于是手机上换个 pane 得盲敲一串 —— 而中间每一步的
 * 屏幕，正好都是「未放大的多 pane 布局」那个在手机上读不了的状态。herdr 的 socket 这层
 * 是**按 pane_id 寻址**的（`pane.zoom` 带 pane_id 就能跨 workspace + tab 一次切过去），
 * 所以这里点一行就到。
 *
 * 它是**索引，不是第二个界面**：点完之后看的还是同一个 herdr 终端（网页接的是整个 TUI，
 * herdr 那边一切焦点，画面自己就跟过来了），键盘那套操作一个字都没变。所以不存在
 * 「手机一套习惯、电脑另一套」—— 这条是刻意的取舍，别把它做成能增删改 pane 的管理器：
 * 那是**会改状态**的事，留在终端里做（判据见 docs/dev/TUI-VS-GUI.md 第 2 节）。
 *
 * 「点了就全屏」默认开：手机上多 pane 平铺根本读不了，去了不放大等于没去。平板横屏
 * （或者你就想看那个 tab 的分屏）时关掉它，那时候点一行是「切焦点 + 退出放大」。
 */
const LS_ZOOM = 'panesZoom'
const LS_ONLY_AGENT = 'panesOnlyAgent'
const LS_SORT = 'panesSort'

/**
 * 「点了就全屏」这会儿开着没有。右上角那张提示卡点一下也是跳 pane，走的得是**同一个**
 * 开关 —— 两处各存各的话，同一个动作在两个入口下行为不一样，而用户只会记得自己关过一次。
 */
export const paneZoomPref = () => localStorage.getItem(LS_ZOOM) !== '0'

type Sort = 'priority' | 'group'

const SORTS: { id: Sort; label: string; hint: string }[] = [
  { id: 'priority', label: '优先级', hint: '要你看的在前（等你 > 完成 > 在跑 > 闲着），同档按最近动过' },
  { id: 'group', label: '分组', hint: '按 workspace 分组，组里是 tab / pane 的原顺序 —— 和你在 herdr 里看到的一样' },
]

/**
 * 优先级分档：**等你 > 完成 > 在跑 > 闲着 > 非 agent**，同一档里按最近动过排。
 *
 * 前两档是需求方定的（需要人看的 > 完成的）。「在跑」单独一档也是他定的 —— 一开始
 * 我把「在跑」和「闲着」合成一档，理由是两个都不需要你、谁在前面没有客观答案；实际用起来
 * 不对：黄点那个（正在跑）会被十几个闲着的埋掉，而列表里最想一眼看到的就是它。
 *
 * 非 agent pane（shell）永远最后 —— 那儿没有状态可言。
 */
const BUCKET: Record<string, number> = { blocked: 0, done: 1, working: 2, idle: 3 }
const bucketOf = (p: Pane) => (p.agent ? (BUCKET[p.status] ?? 4) : 5)

/** 只给最该被看见的两个状态加字：其余靠那个点的颜色，别把每行都塞满标签 */
const STATUS_CHIP: Record<string, { text: string; cls: string }> = {
  blocked: { text: '等你', cls: 'border-bad/50 bg-bad/15 text-bad' },
  done: { text: '完成', cls: 'border-brand/40 bg-brand/12 text-brand' },
}

/**
 * 状态点的颜色：**红 = 等你，绿 = 跑完了，黄 = 在跑**，闲着是灰点。
 *
 * 「在跑」用黄不用绿 —— 和 herdr 自己 agents 栏里那个黄点一致。绿留给「跑完了」（这是
 * 通用约定，一眼就知道是好事）。只有闲着没有颜色：一列点里要是全是彩的，就没有重点了。
 */
const DOT: Record<string, string> = {
  working: 'bg-warn',
  blocked: 'bg-bad',
  done: 'bg-ok',
  idle: 'bg-muted',
}

/** 长路径显示成 ~/…：一行里 cwd 是最认得出 pane 的东西，但绝对路径太占宽度 */
const shortCwd = (p: string) => p.replace(/^\/(?:Users|home)\/[^/]+/, '~')

/**
 * 去掉标题前面那个状态字形。Claude Code 会在终端标题前挂一个转圈的符号（`✳ 图片识别`、
 * `◐ Herdr session URL 路由`），herdr 的 `terminal_title_stripped` 只剥掉了一部分
 * （实拍见过 ◐ 留在里面）。这一行左边已经有一个状态点了，再挂一个抖动的字形只是噪音。
 *
 * 只吃「符号 + 空白」这种开头，所以 `~/subhub`（符号后面没空格）不会被误伤。
 */
const cleanTitle = (t: string) => t.replace(/^[^\p{L}\p{N}\s]+\s+/u, '')

/**
 * 「上次动过」多久了。给的是 unix 毫秒，0 / 缺失就返回空字符串 —— **空着比编一个时间好**
 * （herdr 的 API 不带时间戳，herdr-web 起来之前发生的变化就是不知道，见 internal/agentwatch）。
 *
 * 用 `3m` `2h` `4d` 这种紧凑写法而不是「3 分钟前」：这一列在手机上只有几十像素，
 * 完整时间放在 title 里。
 */
export function ago(ms?: number, now = Date.now()) {
  if (!ms) return ''
  const s = Math.max(0, Math.round((now - ms) / 1000))
  if (s < 45) return '刚刚'
  const m = Math.round(s / 60)
  if (m < 60) return `${m}m`
  const h = Math.round(m / 60)
  if (h < 24) return `${h}h`
  return `${Math.round(h / 24)}d`
}

export function PaneSwitcher({
  panes, watching, onClose, onGoto, onReload,
}: {
  panes: Pane[]
  /** 状态变化的订阅连着没有。没连着时时间列必然是空的，得说清不是坏了 */
  watching?: boolean
  onClose: () => void
  /** 跳过去（zoom=true 顺带全屏）。面板自己不等结果，交给上层 toast */
  onGoto: (id: string, zoom: boolean) => void
  /** 重新拉一次列表。面板开着的时候自己按拍调，界面上没有刷新按钮 —— 见下面那个 interval */
  onReload: () => void
}) {
  /**
   * 触屏上**不自动聚焦**筛选框。原来的判据是「手机竖屏」（`usePhone`，宽度 < 440px），
   * 平板上于是照样自动聚焦 —— 一开面板键盘就顶出来，而这个面板刚刚才把键盘收掉
   * （见 App 的 openPanes）：收一下又弹回来，点第一行还是只会先收键盘。
   * 判据本来就该是「有没有实体指针」而不是屏幕多宽：这儿多半是用手指扫，不是打字。
   */
  const coarse = matchMedia('(pointer: coarse)').matches
  const [q, setQ] = useState('')
  const [zoom, setZoom] = useState(paneZoomPref)
  const [onlyAgent, setOnlyAgent] = useState(() => localStorage.getItem(LS_ONLY_AGENT) === '1')
  const [sort, setSort] = useState<Sort>(() => {
    const v = localStorage.getItem(LS_SORT)
    return SORTS.some((s) => s.id === v) ? (v as Sort) : 'priority'
  })

  /**
   * 点一行**要一下就跳过去**，所以收工在 `pointerup` 上，不在 `click` 上。
   *
   * 为什么不能只靠 click：手机上键盘弹着的时候（在发件箱里口述、或者从 herdr 顶栏那个
   * switch 开进来 —— 触屏那层为了不让浏览器乱改焦点把 touchstart 的默认行为吃掉了，
   * 焦点还在输入框上），点这一行的**同一下**会先把键盘收掉：焦点一走 visualViewport 一变，
   * `--vvh` 跟着变、面板整个重排，手指底下那一行已经挪了位置 —— 浏览器于是把 click 派给
   * 别人或者干脆不派，表现就是「第一下只收键盘，第二下才跳」（真机上实拍到）。
   *
   * touch / 笔的 pointer 事件有**隐式捕获**：pointerdown 落在哪一行，pointerup 就还在
   * 那一行，中间布局怎么动都不影响。鼠标不走这条（`pointerType === 'mouse'` 直接放过），
   * 键盘 Enter 也不走 —— 那两条本来就是 click，而 click 在桌面上从不丢。
   */
  const down = useRef<{ x: number; y: number; id: string } | null>(null)
  /** pointerup 已经接走的那一刻。紧跟着可能还来一个 click，得吃掉，不然跳两次 */
  const took = useRef(0)
  const tap = (p: Pane) => ({
    onPointerDown: (e: PointerEvent) => {
      // **手指落下那一刻就把 pane id 记住**，抬起时用记下来的这个，不用闭包里的 p.id：
      // 列表是会在手底下变的（每 4 秒重拉一次），React 重渲染之后同一个 DOM 元素上挂的
      // 已经是另一个 pane 的回调了 —— 那就会「跳到你没点的那个 pane」。
      down.current = e.pointerType === 'mouse' ? null : { x: e.clientX, y: e.clientY, id: p.id }
    },
    // 滚列表时浏览器会给一个 pointercancel，那一下不算点
    onPointerCancel: () => { down.current = null },
    onPointerUp: (e: PointerEvent) => {
      const d = down.current
      down.current = null
      // 手指走了超过 10px 当成在滚列表（pointercancel 不是每次都来）
      if (!d || Math.abs(e.clientX - d.x) > 10 || Math.abs(e.clientY - d.y) > 10) return
      took.current = e.timeStamp
      onGoto(d.id, zoom)
    },
    onClick: (e: MouseEvent) => {
      if (e.timeStamp - took.current < 800) return // pointerup 刚接走过
      onGoto(p.id, zoom)
    },
  })

  /**
   * 面板开着的时候**自己重新拉列表**，所以界面上没有刷新按钮。
   *
   * 原来只在打开那一下拉一次：开着看的这段时间里，状态点、「几分钟前」、新起的 pane 全是
   * 打开那一刻的快照 —— 而这个面板正是用来看「谁在等我」的，停住的状态比没有更糟。手上
   * 那颗刷新按钮等于把这件事推给人，它占的还是筛选那一排最右边、最容易误按的位置。
   *
   * 4 秒一拍，顺手把「3m」那一列的 now 也推一下（最小刻度是分钟，跟着一起走没坏处）。
   * 标签页不可见时不打 —— 那时候没人看，而手机上后台请求最容易被系统掐着不放。
   * 这一拍是**静默**的：失败不弹 toast（herdr 停了会一直失败，每 4 秒一条没人受得了），
   * 列表空掉时底下那句「拿不到 pane 列表」已经把话说清楚了。
   */
  const [now, setNow] = useState(() => Date.now())
  // 回调放 ref 里，interval 只建一次。挂在依赖上试过，翻车了：外面每渲染一次
  // （发件箱那条 500ms 的心跳一直在推状态）这个 prop 就可能换个新函数，interval
  // 于是每半秒被拆了重建，4 秒那一拍永远等不到 —— 表现是「列表压根不自动刷新」。
  const reload = useRef(onReload)
  useEffect(() => { reload.current = onReload })
  useEffect(() => {
    const t = setInterval(() => {
      setNow(Date.now())
      if (!document.hidden) reload.current()
    }, 4000)
    return () => clearInterval(t)
  }, [])

  const rows = useMemo(() => {
    const kw = q.trim().toLowerCase()
    const hit = (p: Pane) =>
      (!onlyAgent || !!p.agent) &&
      (!kw || `${p.agent} ${p.status} ${p.workspace} ${p.tab} ${p.title} ${p.cwd} ${p.id}`.toLowerCase().includes(kw))
    const list = panes.map((p, i) => ({ p, i })).filter(({ p }) => hit(p))
    if (sort === 'group') return list
    // 同一档里认 seq（state_change_seq）：它是 herdr 里的全局递增计数，「谁最近动过」
    // 一直算得准；changed 只有 herdr-web 在盯的这段时间里才有，拿它排会把「起来之前
    // 就没动过」的 pane 一律沉到底，那不是事实。
    return [...list].sort((a, b) =>
      bucketOf(a.p) - bucketOf(b.p) ||
      (b.p.seq ?? 0) - (a.p.seq ?? 0) ||
      a.i - b.i,
    )
  }, [panes, q, onlyAgent, sort])

  /*
   * **顺序是实时的**：4 秒一拍重拉之后跟着重排，谁刚开工 / 刚等你就当场浮上来。
   *
   * 中间有一版是「面板开着就把顺序冻住」，为的是治「你看着第三行、手指落下去时那一行已经
   * 是别人了」。需求方看到的结果是排序像坏的（在跑的 pane 明明 1 分钟前动过，却排在几个
   * 闲了两小时的下面 —— 因为它是在面板打开之后才开工的），所以撤掉了：这个面板的价值就是
   * 「谁在等我」，停住的排名比抽走一行更糟。
   *
   * 「点错 pane」那条不靠冻结兜：行在 `pointerdown` 那一刻就把 pane id 记住了（见下面的
   * tap），抬手用记下的那个 —— 列表怎么动，跳的都是手指真正落下去那一行。
   */

  // 「分组」按 workspace 分组（和 herdr 里看到的一样）；优先级是全局排序，
  // 分组会把它切碎，所以那边是一条平铺的列表，workspace 挪进每行的副行。
  const groups = useMemo(() => {
    if (sort !== 'group') return [{ ws: '', items: rows.map((r) => r.p) }]
    const out: { ws: string; items: Pane[] }[] = []
    for (const { p } of rows) {
      const g = out.find((x) => x.ws === p.workspace)
      if (g) g.items.push(p)
      else out.push({ ws: p.workspace, items: [p] })
    }
    return out
  }, [rows, sort])

  const flip = (key: string, v: boolean, set: (v: boolean) => void) => {
    set(v)
    localStorage.setItem(key, v ? '1' : '0')
  }
  const cycleSort = () => {
    const next = SORTS[(SORTS.findIndex((s) => s.id === sort) + 1) % SORTS.length].id
    setSort(next)
    localStorage.setItem(LS_SORT, next)
  }
  const cur = SORTS.find((s) => s.id === sort)!

  return (
    <Panel
      onClose={onClose}
      // 一览是要扫的，比设置面板宽一档；手机上几乎铺满（几十个 pane 得能滚起来）。
      // 高度和「贴着顶栏」现在都是 Panel 的默认，这儿不再各说一遍
      className="w-[520px] max-md:inset-x-2 max-md:w-auto"
    >
      {/* 这一排粘在顶上：滚到第三个 workspace 还得能改筛选（-top-2/-mt-2/pt-2 那三个一套，
          和设置面板的分页条同理 —— 外层滚动容器有 pt-2，只写 top-0 会漏一条缝） */}
      <div className="sticky -top-2 z-1 -mx-4 -mt-2 mb-1.5 flex flex-wrap items-center gap-1.5 border-b border-line bg-bar px-4 pt-2 pb-1.5">
        <div className="relative min-w-0 flex-1">
          <Search className="pointer-events-none absolute top-1/2 left-2 size-3 -translate-y-1/2 text-faint" />
          <Input
            data-testid="panes-filter"
            className="h-7 w-full pl-7"
            placeholder="筛 tab / 标题 / 路径"
            value={q}
            // 触屏上不自动聚焦（见上面 coarse 那段）
            autoFocus={!coarse}
            onChange={(e) => setQ(e.target.value)}
          />
        </div>
        <Button
          size="tiny"
          data-testid="panes-sort"
          title={`排序：${cur.hint}（点一下换下一种）`}
          onClick={cycleSort}
        >
          {cur.label}
        </Button>
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
        {/* 关闭并进这一排（面板没有标题栏了）。摆在最右边、和别的键留一点距离：
            这是唯一一个「点了就没了」的按钮，别和筛选那几个挨成一片 */}
        <Button
          size="tiny"
          className="ml-0.5"
          aria-label="关闭"
          title="关闭（Esc 也行）"
          onClick={onClose}
        >
          <X className="size-3.5" />
        </Button>
      </div>

      {rows.length === 0 && (
        <p className="px-1 py-6 text-center text-xs text-faint">
          {panes.length === 0 ? '拿不到 pane 列表 —— herdr server 在跑吗？' : '没有匹配的 pane'}
        </p>
      )}

      {groups.map((g) => (
        <section key={g.ws} className="mb-1">
          {g.ws && <h4 className="px-1 pt-1 pb-0.5 text-[11px] font-medium tracking-wide text-faint">{g.ws}</h4>}
          <div className="flex flex-col gap-0.5">
            {g.items.map((p) => {
              const chip = p.agent ? STATUS_CHIP[p.status] : undefined
              const when = ago(p.changed, now)
              return (
                <button
                  key={p.id}
                  type="button"
                  data-testid="panes-row"
                  // min-h-9：一行至少 36px。原来是 44px（手指的常规下限），但这一行里本来就有
                  // 两行字（≈30px），44 是硬撑出来的空白 —— 手机上一屏能看的 pane 少两三个，
                  // 而这个面板的价值就是「一眼扫完」。整块都是热区（不是只有文字），36px 高、
                  // 铺满整宽的条子点不空。
                  className={cn(
                    'flex min-h-9 w-full cursor-pointer items-center gap-2 rounded-md border border-transparent',
                    'px-2 py-1 text-left leading-tight outline-none transition-colors duration-100',
                    'hover:border-line hover:bg-ctl focus-visible:ring-2 focus-visible:ring-brand/35',
                    p.focused && 'border-brand/40 bg-brand/10',
                  )}
                  {...tap(p)}
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
                      {chip && (
                        <span className={cn('shrink-0 rounded border px-1 py-px text-[10px]', chip.cls)}>
                          {chip.text}
                        </span>
                      )}
                      {p.focused && (
                        <span className="shrink-0 rounded border border-brand/40 bg-brand/12 px-1 py-px text-[10px] text-brand">
                          当前
                        </span>
                      )}
                      <span
                        className="ml-auto shrink-0 font-mono text-[10px] text-faint"
                        title={p.changed ? `上次状态变化：${new Date(p.changed).toLocaleString()}` : ''}
                      >
                        {when}
                      </span>
                    </span>
                    {/* agent pane 的 terminal_title 是它自己写的会话标题（「图片识别」这种），
                        比路径认得出得多；shell pane 的标题只是 user@host:path，那还是给路径。
                        平铺排序时没有 workspace 分组，所以 workspace 挪到这儿来 */}
                    <span className="mt-px flex items-center gap-1.5 font-mono text-[11px] text-faint">
                      <span className="truncate">
                        {sort !== 'group' && `${p.workspace} · `}
                        {(p.agent && cleanTitle(p.title)) || shortCwd(p.cwd) || p.id}
                      </span>
                      {/* pane id 在手机上**也要出**：一个 tab 里有两个 pane 时（herdr 里分屏），
                          两行的 tab 标签和 cwd 一模一样，id 是唯一分得开的东西（实拍见过） */}
                      <span className="ml-auto shrink-0 text-[10px]">{p.id}</span>
                    </span>
                  </span>
                </button>
              )
            })}
          </div>
        </section>
      ))}

      {/* 时间列空着的两种原因完全不同，别让用户以为坏了 */}
      {rows.length > 0 && (watching === false
        ? (
          <p className="px-1 pt-1 text-[11px] text-faint">
            没在盯 agent 状态变化（herdr server 没在跑？），所以没有时间。排序仍然按 herdr 的
            状态变化计数来，是准的。
          </p>
        )
        : rows.some(({ p }) => p.agent && !p.changed) && (
          <p className="px-1 pt-1 text-[11px] text-faint">
            没时间的那几个：herdr 的 API 不给时间戳，时间是 herdr-web 盯着状态变化自己记的 ——
            起来之后还没变过状态的就先空着，变一次就有了（记下来的会存着，重启不丢）。
          </p>
        ))}
    </Panel>
  )
}
