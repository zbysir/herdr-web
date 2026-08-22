import { useEffect, useRef, useState } from 'react'
import { X } from 'lucide-react'
import { api, type TopbarResponse } from '@/lib/api'
import { useChipDrag, type ChipAt } from '@/lib/chipdrag'
import { TOPBAR_BY_ID, TOPBAR_ITEMS, type TopbarId } from './topbarItems'
import { Button } from './ui/button'
import { cn } from '@/lib/utils'

/**
 * 顶栏编辑器：**上面是顶栏，下面是没上栏的按钮，按住拖上去**。
 *
 * 和软键条那一页是同一个形状（库在下、栏在上、拖进去），手势也是同一份
 * （`lib/chipdrag`）—— 两套排布界面各写一遍拖动，是「同一个动作两种手感」的来路。
 *
 * 差别在数据：顶栏就是**一串 id**，没有「定义」那一层（按钮长什么样是内置的，见
 * `topbarItems.tsx`），所以库里那些是「还没上栏的」，拖上去就从库里消失 —— 顶栏上同一个
 * 按钮放两次没有意义（软键条那边 Esc 两行各放一个是有意义的，这里没有对应的场景，
 * 服务端也直接拒了重复）。
 *
 * ⚙ 设置**拖不下来**：那是唯一一条改回这份配置的路，而配置是跟着人走的（存服务端），
 * 在手机上删掉，电脑上也就没了。服务端存盘时也会把它补回去，两头都拦一道。
 */
type Zone = 'bar' | 'lib'
type At = ChipAt<Zone>

export function TopbarPanel({
  onSaved, toast, profile,
}: {
  /** 存好之后把新顺序交回去，顶栏立刻跟着变（不用刷新页面） */
  onSaved: (items: TopbarId[]) => void
  toast: (m: string) => void
  /** 改**哪一套**（见 internal/profiles 和 SoftkeysPanel 里同一个 prop 的注释） */
  profile: { id: string; name: string }
}) {
  const [items, setItems] = useState<TopbarId[]>([])
  const [pinned, setPinned] = useState<string[]>(['settings'])
  const [max, setMax] = useState(24)
  const [err, setErr] = useState('')
  const [dirty, setDirty] = useState(false)

  const take = (r: TopbarResponse) => {
    setItems(r.items.filter((id): id is TopbarId => TOPBAR_BY_ID.has(id as TopbarId)))
    setPinned(r.pinned)
    setMax(r.max || 24)
    setDirty(false)
  }

  useEffect(() => {
    void (async () => {
      try {
        take(await api.get<TopbarResponse>(`/topbar?profile=${encodeURIComponent(profile.id)}`))
      } catch (e) {
        setErr((e as Error).message)
      }
    })()
    // 换了一套就整份重读（上面那一行就在同一块面板里，开着也能换）
  }, [profile.id])

  /** 库 = 还没上栏的，按内置目录的顺序（不是用户排的顺序，库里要好找） */
  const lib = TOPBAR_ITEMS.filter((it) => !items.includes(it.id))

  const at = (zone: Zone, i: number): TopbarId | undefined =>
    zone === 'bar' ? items[i] : lib[i]?.id

  /* ------------------------------------------------------------ 拖动 */

  const zoneEl = useRef<Partial<Record<Zone, HTMLDivElement | null>>>({})

  const drop = (from: At, to: At) => {
    const id = at(from.zone, from.i)
    if (!id) return
    if (from.zone === to.zone && from.zone === 'lib') return // 库里的顺序是内置的，排不动

    // 库 → 栏：上栏
    if (from.zone === 'lib' && to.zone === 'bar') {
      if (items.length >= max) { toast(`顶栏最多放 ${max} 个`); return }
      setItems((prev) => [...prev.slice(0, to.i), id, ...prev.slice(to.i)])
      setDirty(true)
      return
    }
    // 栏 → 栏：排序
    if (from.zone === 'bar' && to.zone === 'bar') {
      setItems((prev) => {
        const next = [...prev]
        next.splice(from.i, 1)
        next.splice(from.i < to.i ? to.i - 1 : to.i, 0, id)
        return next
      })
      setDirty(true)
      return
    }
    // 栏 → 库：下栏
    remove(from.i)
  }

  const remove = (i: number) => {
    const id = items[i]
    if (!id) return
    if (pinned.includes(id)) {
      toast(`「${TOPBAR_BY_ID.get(id)?.label}」留着 —— 删了就没路回来改这份配置了`)
      return
    }
    setItems((prev) => prev.filter((_, n) => n !== i))
    setDirty(true)
  }

  const { drag, over, onChipDown } = useChipDrag<Zone>({
    zones: () => ['bar', 'lib'],
    elOf: (z) => zoneEl.current[z],
    onDrop: drop,
  })

  /** 键盘也要能排：← → 栏里挪，↑ 上栏 / ↓ 下栏，Delete 拿下来 */
  const onChipKey = (e: React.KeyboardEvent, from: At) => {
    if (e.key === 'ArrowLeft' || e.key === 'ArrowRight') {
      if (from.zone !== 'bar') return
      e.preventDefault()
      drop(from, { zone: 'bar', i: from.i + (e.key === 'ArrowLeft' ? -1 : 2) })
    } else if (e.key === 'ArrowUp' && from.zone === 'lib') {
      e.preventDefault()
      drop(from, { zone: 'bar', i: items.length })
    } else if ((e.key === 'ArrowDown' || e.key === 'Delete' || e.key === 'Backspace') && from.zone === 'bar') {
      e.preventDefault()
      remove(from.i)
    } else if (e.key === 'Enter' || e.key === ' ') {
      // 点一下 / 回车 = 上栏（拖不动的时候的那条路：鼠标点、键盘按都走这儿）
      if (from.zone === 'lib') {
        e.preventDefault()
        drop(from, { zone: 'bar', i: items.length })
      }
    }
  }

  /* ------------------------------------------------------------ 存 */

  const save = async () => {
    setErr('')
    try {
      const r = await api.put<TopbarResponse>(`/topbar?profile=${encodeURIComponent(profile.id)}`, { items })
      take(r)
      onSaved(r.items.filter((id): id is TopbarId => TOPBAR_BY_ID.has(id as TopbarId)))
      toast(`「${profile.name}」的顶栏已保存`)
    } catch (e) {
      setErr((e as Error).message)
    }
  }

  const reset = async () => {
    setErr('')
    try {
      const r = await api.del<TopbarResponse>(`/topbar?profile=${encodeURIComponent(profile.id)}`)
      take(r)
      onSaved(r.items.filter((id): id is TopbarId => TOPBAR_BY_ID.has(id as TopbarId)))
      toast('已恢复默认')
    } catch (e) {
      setErr((e as Error).message)
    }
  }

  /* ------------------------------------------------------------ 渲染 */

  const chip = (zone: Zone, i: number, id: TopbarId) => {
    const it = TOPBAR_BY_ID.get(id)!
    const fixed = zone === 'bar' && pinned.includes(id)
    return (
      <div key={`${zone}-${id}`} className="flex items-center">
        {over?.zone === zone && over.i === i && <span className="mr-1 h-7 w-0.5 shrink-0 rounded-full bg-brand" />}
        <span
          data-chip
          data-testid={`topbar-chip-${zone}-${id}`}
          role="button"
          tabIndex={0}
          title={zone === 'bar'
            ? `${it.hint} —— 按住拖动排序${fixed ? '（这个删不掉）' : '，✕ 从顶栏拿下来'}`
            : `${it.hint} —— 按住拖到上面，或者点一下就上栏`}
          className={cn(
            'flex shrink-0 items-center gap-1.5 rounded-md border border-line bg-ctl px-2 py-1.5',
            'text-xs text-fg cursor-grab select-none active:cursor-grabbing',
            'transition-[background-color,border-color] duration-100 hover:border-line-hi hover:bg-ctl-hi',
            fixed && 'border-brand/40 bg-brand/10 text-brand',
          )}
          onPointerDown={(e) => onChipDown(e, { zone, i })}
          onKeyDown={(e) => onChipKey(e, { zone, i })}
          // 库里的点一下就上栏（拖是给排序用的；「加一个」不该非得学会拖）
          onClick={() => { if (zone === 'lib') drop({ zone, i }, { zone: 'bar', i: items.length }) }}
        >
          {it.icon}
          <span className="whitespace-nowrap">{it.label}</span>
          {zone === 'bar' && !fixed && (
            <X
              className="size-3 shrink-0 text-muted hover:text-bad"
              // 这一下不能起拖，也不能冒到方块上去
              onPointerDown={(ev) => { ev.stopPropagation(); ev.preventDefault() }}
              onClick={(ev) => { ev.stopPropagation(); remove(i) }}
            />
          )}
        </span>
      </div>
    )
  }

  const box = (zone: Zone, label: string, hint: string, ids: TopbarId[]) => (
    <div className="flex items-start gap-2">
      <span className="w-11 shrink-0 pt-2.5 text-xs text-faint">{label}</span>
      <div
        ref={(el) => { zoneEl.current[zone] = el }}
        data-testid={`topbar-zone-${zone}`}
        className={cn(
          // 虚线框 + 比面板暗一档的底：一眼看出「这是个筐，东西能拖进来」
          'flex min-h-[44px] flex-1 flex-wrap content-start items-start gap-1.5 rounded-lg p-2',
          'border border-dashed border-line-hi/70 bg-bg/40 transition-colors duration-100',
          over?.zone === zone && 'border-brand/60 bg-brand/8',
        )}
      >
        {ids.map((id, i) => chip(zone, i, id))}
        {over?.zone === zone && over.i >= ids.length && <span className="h-7 w-0.5 rounded-full bg-brand" />}
        {ids.length === 0 && over?.zone !== zone && <span className="px-1 py-1.5 text-xs text-faint">{hint}</span>}
      </div>
    </div>
  )

  return (
    <div className="flex flex-col gap-2.5">
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-[13px] font-medium">顶栏</span>
        {/* 在改哪一套 —— 和软键条那页同一个位置、同一个样子 */}
        <span className="rounded border border-line bg-ctl px-1.5 py-0.5 text-xs text-muted">{profile.name}</span>
        <span className="text-xs text-faint">{items.length} / {max}</span>
        <div className="ml-auto flex items-center gap-2">
          <Button size="tiny" variant="primary" onClick={() => void save()} disabled={!dirty}>
            {dirty ? '保存' : '已保存'}
          </Button>
          <Button size="tiny" onClick={() => void reset()}>恢复默认</Button>
        </div>
      </div>

      {box('bar', '顶栏', '空的 —— 从下面拖一个上来', items)}
      {box('lib', '没上栏', '都在顶栏上了', lib.map((it) => it.id))}

      {err && <p className="text-xs text-bad">{err}</p>}

      <p className="text-xs/relaxed text-muted">
        按住一个方块拖：从下面拖上去就是加，从上面拖下来（或者点 ✕）就是去掉，栏里拖是排序。
        库里的方块<strong className="font-medium text-fg">点一下</strong>也能直接加到末尾。手机上按住
        0.25 秒才算拿起 —— 这一页要能上下滚，不按住就没法区分「滚页面」和「拖这个」。
        <br />
        顶栏放不下会<strong className="font-medium text-fg">自己横滑</strong>，不换行、也不会藏起来
        （原来字号 ± / 明暗在手机竖屏是 CSS 藏掉的）。
        这一份是<strong className="font-medium text-fg">「{profile.name}」这一套</strong>的，
        存在服务端（<code className="rounded border border-line bg-ctl px-1 py-px font-mono text-[11px]">~/.herdr-web/topbar.json</code>）——
        平板放八个图标、手机竖屏放三个，各存一份互不影响；换一套在上面那一行。
      </p>

      {/* 跟着手指走的残影。fixed + pointer-events-none：它自己不能挡住命中判定 */}
      {drag && (() => {
        const id = at(drag.from.zone, drag.from.i)
        const it = id && TOPBAR_BY_ID.get(id)
        return it ? (
          <span
            className="pointer-events-none fixed z-50 flex -translate-x-1/2 -translate-y-1/2 items-center gap-1.5
                       rounded-md border border-brand-line bg-brand-bg px-2 py-1.5 text-xs text-brand-fg
                       shadow-[0_10px_24px_-8px_rgba(0,0,0,.7)]"
            style={{ left: drag.x, top: drag.y }}
          >
            {it.icon}
            {it.label}
          </span>
        ) : null
      })()}
    </div>
  )
}
