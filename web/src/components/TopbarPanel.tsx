import { useEffect, useRef, useState, type ReactNode } from 'react'
import { X } from 'lucide-react'
import { api, libMap, TOPBAR_KEY, topbarKeyRef, type SoftKey, type SoftkeysResponse, type TopbarResponse } from '@/lib/api'
import { useChipDrag, type ChipAt } from '@/lib/chipdrag'
import { CAP_BY_ID, TOPBAR_ITEMS, type CapId } from '@/capabilities'
import { Button } from './ui/button'
import { SaveButton } from './ui/savebutton'
import { cn } from '@/lib/utils'

/**
 * 顶栏编辑器：**上面是顶栏，下面是没上栏的，按住拖上去**。
 *
 * 和快捷键条那一页是同一个形状（库在下、栏在上、拖进去），手势也是同一份
 * （`lib/chipdrag`）—— 两套排布界面各写一遍拖动，是「同一个动作两种手感」的来路。
 *
 * 下面的库有**两个**：
 *
 *   - 「内置按钮」：这一版有哪些功能按钮，唯一一份清单在 `topbarItems.tsx`（服务端有一份
 *     一样的白名单，有测试盯着）。拖上去就从库里消失 —— 顶栏上同一个按钮放两次没有意义
 *     （快捷键条那边 Esc 两行各放一个是有意义的，这里没有对应的场景，服务端也直接拒重复）。
 *   - 「我的按键」：快捷键条那份定义（`internal/softkeys` 的 Lib）。顶栏上存成
 *     `key:<定义ID>`，**定义还是那一份** —— 改一处按键谱，快捷键条和顶栏一起变。于是
 *     「顶栏上能不能加个 ctrl+b z」不用每次都动一遍白名单。
 *
 * ⚙ 设置**拖不下来**：那是唯一一条改回这份配置的路，而配置是跟着人走的（存服务端），
 * 在手机上删掉，电脑上也就没了。服务端存盘时也会把它补回去，两头都拦一道。
 */
type Zone = 'bar' | 'lib' | 'keys'
type At = ChipAt<Zone>

/** 库那两个筐里的顺序都是固定的（内置目录顺序 / 定义顺序），排不动 —— 只有顶栏能排 */
const isLib = (z: Zone) => z !== 'bar'

/**
 * 一个 item 在编辑器里长什么样。`gone` = 引用指到空处了（定义被删掉了）。
 */
interface Face { label: string; hint: string; icon?: ReactNode; mono?: boolean; gone?: boolean }

export function TopbarPanel({
  onSaved, toast, profile,
}: {
  /** 存好之后把新顺序交回去，顶栏立刻跟着变（不用刷新页面） */
  onSaved: (items: string[]) => void
  toast: (m: string) => void
  /** 改**哪一套**（见 internal/profiles 和 SoftkeysPanel 里同一个 prop 的注释） */
  profile: { id: string; name: string }
}) {
  const [items, setItems] = useState<string[]>([])
  /** 「我的按键」的定义（全局的，不分套）—— 顶栏上那些 `key:` 引用靠它落地 */
  const [keys, setKeys] = useState<SoftKey[]>([])
  const [pinned, setPinned] = useState<string[]>(['settings'])
  /**
   * **服务端说哪些能放顶栏**（`capability.TopbarIDs()`）。
   *
   * 以前这儿不看它，库直接铺前端那份目录 —— 只要两边有一点不一致，用户就能把一个存不进去的
   * 按钮拖上去，一保存报「不认识的按钮」。「编辑器里拖得上去、一存报错」是这个项目最不想有的
   * 那种交互，所以现在**库 = 服务端那份 ∩ 前端认得画的**。
   *
   * 空 = 服务端没说（旧后端），那就退回前端那份目录，别把库变成空的。
   */
  const [actions, setActions] = useState<string[]>([])
  const [max, setMax] = useState(24)
  const [err, setErr] = useState('')
  const [dirty, setDirty] = useState(false)

  const byKey = libMap(keys)

  /**
   * 只按**形状**过一遍，不核「这个定义还在不在」。
   *
   * 核的话就得等 /softkeys 那一趟也回来才敢渲染，而两趟是并发的 —— 谁先回来决定要不要
   * 把人家的配置抹掉，这种时序 bug 的表现是「偶尔保存完少一个键」。指到空处的引用照旧
   * 留在 items 里，画成一块红的「已删掉」让人自己拿下来（见 face）。
   */
  const take = (r: TopbarResponse) => {
    setItems(r.items.filter((id) => CAP_BY_ID.has(id as CapId) || !!topbarKeyRef(id)))
    setPinned(r.pinned)
    setActions(r.actions ?? [])
    setMax(r.max || 24)
    setDirty(false)
  }

  useEffect(() => {
    void (async () => {
      try {
        // 两趟一起发、一起落地：分开 set 的话会先闪一排「已删掉」再变回来
        const [tb, sk] = await Promise.all([
          api.get<TopbarResponse>(`/topbar?profile=${encodeURIComponent(profile.id)}`),
          // 定义是全局的，不用带 ?profile=
          api.get<SoftkeysResponse>('/softkeys'),
        ])
        setKeys(sk.lib)
        take(tb)
      } catch (e) {
        setErr((e as Error).message)
      }
    })()
    // 换了一套就整份重读（上面那一行就在同一块面板里，开着也能换）
  }, [profile.id])

  /** 内置库 = 服务端认的 ∩ 前端画得出的 ∩ 还没上栏的，按内置目录的顺序（库里要好找） */
  const lib = TOPBAR_ITEMS.filter(
    (it) => (actions.length === 0 || actions.includes(it.id)) && !items.includes(it.id),
  )
  /** 按键库 = 还没上栏的定义，按「我的按键」里的顺序 */
  const keyLib = keys.filter((k) => !!k.id && !items.includes(TOPBAR_KEY + k.id))

  const at = (zone: Zone, i: number): string | undefined => {
    if (zone === 'bar') return items[i]
    if (zone === 'lib') return lib[i]?.id
    const id = keyLib[i]?.id
    return id ? TOPBAR_KEY + id : undefined
  }

  const face = (item: string): Face | null => {
    const ref = topbarKeyRef(item)
    if (ref) {
      const k = byKey.get(ref)
      if (!k) return { label: ref, hint: '这个按键已经不在「我的按键」里了 —— 点 ✕ 拿下来', mono: true, gone: true }
      return {
        label: k.label,
        hint: `我的按键：${k.spec || k.sticky || k.act || ''}${k.confirm ? '（要点两下）' : ''}`,
        mono: true,
      }
    }
    const it = CAP_BY_ID.get(item as CapId)
    return it ? { label: it.label, hint: it.hint, icon: it.icon } : null
  }

  /* ------------------------------------------------------------ 拖动 */

  const zoneEl = useRef<Partial<Record<Zone, HTMLDivElement | null>>>({})

  const drop = (from: At, to: At) => {
    const id = at(from.zone, from.i)
    if (!id) return
    if (isLib(from.zone) && isLib(to.zone)) return // 库里的顺序是固定的，排不动

    // 库 → 栏：上栏
    if (isLib(from.zone)) {
      if (items.length >= max) { toast(`顶栏最多放 ${max} 个`); return }
      setItems((prev) => [...prev.slice(0, to.i), id, ...prev.slice(to.i)])
      setDirty(true)
      return
    }
    // 栏 → 栏：排序
    if (to.zone === 'bar') {
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
      toast(`「${CAP_BY_ID.get(id as CapId)?.label}」留着 —— 删了就没路回来改这份配置了`)
      return
    }
    setItems((prev) => prev.filter((_, n) => n !== i))
    setDirty(true)
  }

  const { drag, over, onChipDown } = useChipDrag<Zone>({
    zones: () => ['bar', 'lib', 'keys'],
    elOf: (z) => zoneEl.current[z],
    onDrop: drop,
  })

  /** 键盘也要能排：← → 栏里挪，↑ 上栏 / ↓ 下栏，Delete 拿下来 */
  const onChipKey = (e: React.KeyboardEvent, from: At) => {
    if (e.key === 'ArrowLeft' || e.key === 'ArrowRight') {
      if (from.zone !== 'bar') return
      e.preventDefault()
      drop(from, { zone: 'bar', i: from.i + (e.key === 'ArrowLeft' ? -1 : 2) })
    } else if (e.key === 'ArrowUp' && isLib(from.zone)) {
      e.preventDefault()
      drop(from, { zone: 'bar', i: items.length })
    } else if ((e.key === 'ArrowDown' || e.key === 'Delete' || e.key === 'Backspace') && from.zone === 'bar') {
      e.preventDefault()
      remove(from.i)
    } else if (e.key === 'Enter' || e.key === ' ') {
      // 点一下 / 回车 = 上栏（拖不动的时候的那条路：鼠标点、键盘按都走这儿）
      if (isLib(from.zone)) {
        e.preventDefault()
        drop(from, { zone: 'bar', i: items.length })
      }
    }
  }

  /* ------------------------------------------------------------ 存 */

  // 成没成要回给按钮（它自己举 ✔ / ✕，见 ui/savebutton）。**成了不再吐 toast** ——
  // 那条提示在整屏正下方，而按钮在面板顶上，用户报的就是「点了跟没点一样」
  const save = async () => {
    setErr('')
    try {
      const r = await api.put<TopbarResponse>(`/topbar?profile=${encodeURIComponent(profile.id)}`, { items })
      take(r)
      onSaved(r.items)
      return true
    } catch (e) {
      setErr((e as Error).message)
      return false
    }
  }

  const reset = async () => {
    setErr('')
    try {
      const r = await api.del<TopbarResponse>(`/topbar?profile=${encodeURIComponent(profile.id)}`)
      take(r)
      onSaved(r.items)
      toast('已恢复默认')
    } catch (e) {
      setErr((e as Error).message)
    }
  }

  /* ------------------------------------------------------------ 渲染 */

  const chip = (zone: Zone, i: number, item: string) => {
    const f = face(item)
    if (!f) return null
    const fixed = zone === 'bar' && pinned.includes(item)
    return (
      <div key={`${zone}-${item}`} className="flex items-center">
        {over?.zone === zone && over.i === i && <span className="mr-1 h-7 w-0.5 shrink-0 rounded-full bg-brand" />}
        <span
          data-chip
          data-testid={`topbar-chip-${zone}-${item}`}
          role="button"
          tabIndex={0}
          title={zone === 'bar'
            ? `${f.hint} —— 按住拖动排序${fixed ? '（这个删不掉）' : '，✕ 从顶栏拿下来'}`
            : `${f.hint} —— 按住拖到上面，或者点一下就上栏`}
          className={cn(
            'flex shrink-0 items-center gap-1.5 rounded-md border border-line bg-ctl px-2 py-1.5',
            'text-xs text-fg cursor-grab select-none active:cursor-grabbing',
            'transition-[background-color,border-color] duration-100 hover:border-line-hi hover:bg-ctl-hi',
            fixed && 'border-brand/40 bg-brand/10 text-brand',
            // 「我的按键」用 mono：和内置按钮（有图标）在同一个筐里也分得开
            f.mono && 'font-mono',
            f.gone && 'border-bad/45 bg-bad/10 text-bad',
          )}
          onPointerDown={(e) => onChipDown(e, { zone, i })}
          onKeyDown={(e) => onChipKey(e, { zone, i })}
          // 库里的点一下就上栏（拖是给排序用的；「加一个」不该非得学会拖）
          onClick={() => { if (isLib(zone)) drop({ zone, i }, { zone: 'bar', i: items.length }) }}
        >
          {f.icon}
          <span className="whitespace-nowrap">{f.label}</span>
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

  const box = (zone: Zone, label: string, hint: string, list: string[]) => (
    <div className="flex items-start gap-2">
      <span className="w-16 shrink-0 pt-2.5 text-xs text-faint">{label}</span>
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
        {list.map((it, i) => chip(zone, i, it))}
        {over?.zone === zone && over.i >= list.length && <span className="h-7 w-0.5 rounded-full bg-brand" />}
        {list.length === 0 && over?.zone !== zone && <span className="px-1 py-1.5 text-xs text-faint">{hint}</span>}
      </div>
    </div>
  )

  return (
    <div className="flex flex-col gap-2.5">
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-[13px] font-medium">顶栏</span>
        {/* 在改哪一套 —— 和快捷键条那页同一个位置、同一个样子 */}
        <span className="rounded border border-line bg-ctl px-1.5 py-0.5 text-xs text-muted">{profile.name}</span>
        <span className="text-xs text-faint">{items.length} / {max}</span>
        <div className="ml-auto flex items-center gap-2">
          <SaveButton size="tiny" onSave={save} disabled={!dirty}>
            {dirty ? '保存' : '已保存'}
          </SaveButton>
          <Button size="tiny" onClick={() => void reset()}>恢复默认</Button>
        </div>
      </div>

      {box('bar', '顶栏', '空的 —— 从下面拖一个上来', items)}
      {box('lib', '内置按钮', '都在顶栏上了', lib.map((it) => it.id))}
      {box('keys', '我的按键', '「我的按键」里的都在顶栏上了 —— 去「快捷键条」那页加', keyLib.map((k) => TOPBAR_KEY + k.id))}

      {err && <p className="text-xs text-bad">{err}</p>}

      {/* 就地说明：**只写「怎么用」**，不写「为什么这么设计」—— 理由归 docs/dev/MOBILE.md。
          混进来的后果是用户面前这一块变成一堵墙（快捷键条那一页犯过，用户报的） */}
      <div className="text-xs/relaxed text-muted
                      [&_code]:rounded [&_code]:border [&_code]:border-line [&_code]:bg-ctl
                      [&_code]:px-1 [&_code]:py-px [&_code]:font-mono [&_code]:text-[11px] [&_code]:text-fg
                      [&_strong]:font-medium [&_strong]:text-fg">
        <ul className="ml-3.5 list-disc space-y-0.5">
          <li>下面的方块<strong>点一下</strong>就加到顶栏末尾，<strong>按住拖</strong>能放到指定位置。</li>
          <li>顶栏里拖是排序；拖下来（或点 ✕）就是去掉。⚙ 设置去不掉。</li>
          <li><strong>「我的按键」</strong>那一筐是快捷键条上那份定义：拖上来就多一个键。
            改一处按键谱（在「快捷键条」那页）两边一起变，在那儿删掉一个，顶栏上也跟着没了 ——
            所以 <code>ctrl+b z</code> 这种自己配一个拖上来就行。</li>
          <li>顶栏放不下会<strong>自己横滑</strong>，不换行、也不会藏起来。</li>
        </ul>
        <p className="mt-2">
          这一份是「{profile.name}」这一套自己的 —— 平板放八个图标、手机竖屏放三个，各存一份互不影响；
          换一套在上面那一行。
        </p>
      </div>

      {/* 跟着手指走的残影。fixed + pointer-events-none：它自己不能挡住命中判定 */}
      {drag && (() => {
        const id = at(drag.from.zone, drag.from.i)
        const f = id ? face(id) : null
        return f ? (
          <span
            className={cn(
              'pointer-events-none fixed z-50 flex -translate-x-1/2 -translate-y-1/2 items-center gap-1.5',
              'rounded-md border border-brand-line bg-brand-bg px-2 py-1.5 text-xs text-brand-fg',
              'shadow-[0_10px_24px_-8px_rgba(0,0,0,.7)]',
              f.mono && 'font-mono',
            )}
            style={{ left: drag.x, top: drag.y }}
          >
            {f.icon}
            {f.label}
          </span>
        ) : null
      })()}
    </div>
  )
}
