import { useEffect, useRef, useState } from 'react'
import { Plus, Trash2, X } from 'lucide-react'
import { api, type PresetGroup, type SoftKey, type SoftkeysResponse, type SoftkeysSaved } from '@/lib/api'
import { Button } from './ui/button'
import { Input } from './ui/input'
import { Checkbox } from './ui/checkbox'
import { Panel } from './ui/panel'
import { cn } from '@/lib/utils'

/** 键待的地方：软键条第一行 / 第二行 / 键库（`off`，只在库里不上条） */
type Zone = 1 | 2 | 'lib'
type Groups = Record<Zone, SoftKey[]>
/** 拖动的来源。'preset' 是从预设里拖出来的新键（预设本身不会被拿走） */
type From = { zone: Zone; i: number } | 'preset'

/**
 * 触屏上按住这么久才算「把键拿起来」。
 *
 * 为什么要按住：这一页要能上下滚（键库 + 八组预设），而键本身就是拖动的把手 —— 手指落在
 * 键上往下划，到底是滚页面还是拖这个键，只能靠「有没有按住」区分。给键写死
 * `touch-action: none` 的话页面就滚不动了（键铺满了整页），而 `pan-y` 又会把「往下拖到
 * 第二行」吃成滚动。
 *
 * 250ms 是取舍：再短一点，滚页面时容易误拿；再长就等得难受。
 */
const HOLD_MS = 250
/** 按住期间手指飘出这么多就当是在滚页面，撤销这次拿起 */
const HOLD_SLOP = 10
/** 鼠标不用按住：走这么多像素就算拖（鼠标没有「滚 vs 拖」的歧义） */
const MOVE_SLOP = 6

/** 「按键」栏怎么显示：sticky/act 用 `sticky:ctrl` 这种写法，其余就是按键谱。 */
const kindOf = (k: SoftKey) => (k.sticky ? `sticky:${k.sticky}` : k.act ? `act:${k.act}` : (k.spec ?? k.send ?? ''))

/** 把「按键」栏的文本解回一条 SoftKey。名字 / 宽 / 两下这些旁边勾的东西原样留着。 */
function parseKind(spec: string, k: SoftKey): SoftKey {
  const keep = { label: k.label, wide: k.wide, confirm: k.confirm }
  const m = spec.match(/^(sticky|act):(.+)$/)
  if (m) {
    return m[1] === 'sticky'
      ? { ...keep, sticky: m[2].trim() as 'ctrl' | 'alt' }
      : { ...keep, act: m[2].trim() as 'kbd' | 'img' }
  }
  return { ...keep, send: spec }
}

/** 服务端那份扁平的 keys ↔ 三个筐。send 回填成用户写的按键谱，编辑器里要回显原文 */
const split = (keys: SoftKey[]): Groups => {
  const one = (k: SoftKey) => ({ ...k, send: k.spec ?? k.send })
  return {
    1: keys.filter((k) => !k.off && (k.row ?? 1) === 1).map(one),
    2: keys.filter((k) => !k.off && k.row === 2).map(one),
    lib: keys.filter((k) => k.off).map(one),
  }
}

const flatten = (g: Groups): SoftKey[] => [
  ...g[1].map((k) => ({ ...k, row: 1 as const, off: false })),
  ...g[2].map((k) => ({ ...k, row: 2 as const, off: false })),
  ...g.lib.map((k) => ({ ...k, off: true })),
]

/**
 * 软键条编辑器：**上面是软键条本身（一 / 两栏），下面是键库**，按住键拖上去排布。
 *
 * 之前是一长串竖排的表单行（每个键一行：把手 / 名字 / 按键谱 / 两个勾 / 删除）。信息全，
 * 但看不出「排出来是什么样」—— 而这一页要调的恰恰就是排布：哪个键在第几行、第几个。
 * 十四行表单在手机上还要滚三屏。
 *
 * 现在两件事分开了：
 *
 *   - **上面的栏 = 排布**，所见即所得（键长什么样、多宽、换不换行）。栏里只能拖着排序、
 *     或者点 ✕ 把键**拿下来**（回到键库，不是销毁 —— 排布是随手改的东西，改错了不该
 *     连键的定义一起赔进去）。
 *   - **下面的键库 = 定义**：新增、改名字 / 按键谱 / 宽 / 两下、彻底删掉，都在这儿。
 *     库里的键不上条，随时拖上去。再下面是内置预设，拖上去或者点一下就进库。
 *
 * 拖动用 Pointer Events 手写，不用 HTML5 drag-and-drop —— 后者在触屏上根本不触发，
 * 而平板 / 手机是这个项目的主设备。
 */
export function SoftkeysPanel({
  onClose, onSaved, toast, embedded,
}: {
  onClose?: () => void
  onSaved: (keys: SoftKey[], rows: 1 | 2) => void
  toast: (m: string) => void
  embedded?: boolean
}) {
  const [g, setG] = useState<Groups>({ 1: [], 2: [], lib: [] })
  const [rows, setRows] = useState<1 | 2>(1)
  const [presets, setPresets] = useState<PresetGroup[]>([])
  const [max, setMax] = useState(40)
  const [err, setErr] = useState('')
  /** 选中的那个**库里**的键（下标），下面那条编辑它 */
  const [sel, setSel] = useState<number | null>(null)

  useEffect(() => {
    void (async () => {
      try {
        const r = await api.get<SoftkeysResponse>('/softkeys')
        setG(split(r.keys))
        setRows(r.rows || 1)
        setPresets(r.presets)
        setMax(r.max || 40)
      } catch (e) {
        setErr((e as Error).message)
      }
    })()
  }, [])

  const total = g[1].length + g[2].length + g.lib.length
  const zones = (): Zone[] => (rows === 2 ? [1, 2, 'lib'] : [1, 'lib'])

  /* ------------------------------------------------------------ 拖动 */

  const zoneEl = useRef<Partial<Record<Zone, HTMLDivElement | null>>>({})
  const [drag, setDrag] = useState<{ key: SoftKey; from: From; x: number; y: number } | null>(null)
  const [over, setOver] = useState<{ zone: Zone; i: number } | null>(null)

  /**
   * 指针落在哪个筐的第几个位置。
   *
   * 筐里是**换行**排的（和真软键条一样），所以先按 y 找到同一视觉行上的那几个键，再在这
   * 一行里比 x 的中点。只比 x 的话，两行键叠在一起时插入点会跳到上一行去。
   */
  const hit = (x: number, y: number): { zone: Zone; i: number } | null => {
    for (const zone of zones()) {
      const el = zoneEl.current[zone]
      if (!el) continue
      const r = el.getBoundingClientRect()
      if (y < r.top - 8 || y > r.bottom + 8) continue
      const rects = ([...el.querySelectorAll('[data-chip]')] as HTMLElement[]).map((c) => c.getBoundingClientRect())
      if (!rects.length) return { zone, i: 0 }
      const line = rects.filter((c) => y >= c.top - 2 && y <= c.bottom + 2)
      if (!line.length) return { zone, i: y < rects[0].top ? 0 : rects.length }
      for (let n = 0; n < rects.length; n++) {
        const c = rects[n]
        if (y < c.top - 2 || y > c.bottom + 2) continue
        if (x < c.left + c.width / 2) return { zone, i: n }
      }
      return { zone, i: rects.lastIndexOf(line[line.length - 1]) + 1 }
    }
    return null
  }

  /** 把一个键放到目标位置。from 是 'preset' 就是新加一个（库和栏都能直接落） */
  const move = (key: SoftKey, from: From, to: { zone: Zone; i: number }) => {
    setG((prev) => {
      const next: Groups = { 1: [...prev[1]], 2: [...prev[2]], lib: [...prev.lib] }
      if (from === 'preset') {
        if (total >= max) return prev
        next[to.zone].splice(to.i, 0, { ...key })
        return next
      }
      const [k] = next[from.zone].splice(from.i, 1)
      // 同一个筐里往后挪：抽掉自己之后，目标下标要减一
      const i = from.zone === to.zone && from.i < to.i ? to.i - 1 : to.i
      next[to.zone].splice(i, 0, k)
      return next
    })
    // 选中跟着走：改完一个键往往就想接着改它
    setSel(to.zone === 'lib' ? to.i : null)
  }

  /**
   * 按下一个键。触屏按住 HOLD_MS 才算拿起、鼠标走 MOVE_SLOP 就算拖；
   * 没拿起就松手 = 点一下（库里的选中它，预设里的加进库）。
   */
  const onChipDown = (e: React.PointerEvent, key: SoftKey, from: From) => {
    const target = e.currentTarget as HTMLElement
    const touch = e.pointerType !== 'mouse'
    const x0 = e.clientX
    const y0 = e.clientY
    let picked = false
    let hold: number | undefined
    let to: { zone: Zone; i: number } | null = null
    try { target.setPointerCapture(e.pointerId) } catch { /* 没这个指针就不捕获 */ }

    // 拿起来之后不让页面跟着滚。手指在按住期间没动过，浏览器还没开始滚，这时候
    // preventDefault 拦得住（等它滚起来就只剩 pointercancel 了）。
    const noScroll = (ev: TouchEvent) => ev.preventDefault()

    const pick = () => {
      picked = true
      setDrag({ key, from, x: x0, y: y0 })
      document.addEventListener('touchmove', noScroll, { passive: false })
    }
    if (touch) hold = window.setTimeout(pick, HOLD_MS)

    const onMove = (ev: PointerEvent) => {
      const dx = ev.clientX - x0
      const dy = ev.clientY - y0
      if (!picked) {
        if (touch) {
          if (Math.abs(dx) > HOLD_SLOP || Math.abs(dy) > HOLD_SLOP) stop(false)  // 在滚页面，放手
          return
        }
        if (Math.abs(dx) < MOVE_SLOP && Math.abs(dy) < MOVE_SLOP) return
        pick()
      }
      setDrag((d) => (d ? { ...d, x: ev.clientX, y: ev.clientY } : d))
      to = hit(ev.clientX, ev.clientY)
      setOver(to)
    }

    const stop = (finish: boolean) => {
      clearTimeout(hold)
      document.removeEventListener('touchmove', noScroll)
      target.removeEventListener('pointermove', onMove)
      target.removeEventListener('pointerup', up)
      target.removeEventListener('pointercancel', cancel)
      setDrag(null)
      setOver(null)
      if (!finish) return
      if (picked) {
        // 落在筐外面 = 什么都不做（放回原处）。误删太贵，删只走库里那个「删掉」
        if (to) move(key, from, to)
        else if (from === 'preset') toast('拖到上面的栏里或者键库里才算加进来')
        return
      }
      // 没拿起来就松手 = 点一下
      if (from === 'preset') {
        if (total >= max) { toast(`最多 ${max} 个按键`); return }
        setG((prev) => ({ ...prev, lib: [...prev.lib, { ...key }] }))
        setSel(g.lib.length)
        toast('加进键库了，拖到上面的栏里就能用')
      } else if (from.zone === 'lib') {
        setSel(from.i)
      }
    }
    const up = () => stop(true)
    const cancel = () => stop(false)

    target.addEventListener('pointermove', onMove)
    target.addEventListener('pointerup', up)
    target.addEventListener('pointercancel', cancel)
  }

  /** 键盘也要能排：← → 在本筐里挪，↑ ↓ 换筐（栏 ↔ 库），Delete 拿下来 / 删掉 */
  const onChipKey = (e: React.KeyboardEvent, at: { zone: Zone; i: number }) => {
    const k = g[at.zone][at.i]
    if (!k) return
    const order = zones()
    const n = order.indexOf(at.zone)
    if (e.key === 'ArrowLeft' || e.key === 'ArrowRight') {
      e.preventDefault()
      move(k, at, { zone: at.zone, i: at.i + (e.key === 'ArrowLeft' ? -1 : 2) })
    } else if (e.key === 'ArrowUp' || e.key === 'ArrowDown') {
      e.preventDefault()
      const z = order[Math.min(Math.max(n + (e.key === 'ArrowUp' ? -1 : 1), 0), order.length - 1)]
      if (z !== at.zone) move(k, at, { zone: z, i: g[z].length })
    } else if (e.key === 'Delete' || e.key === 'Backspace') {
      e.preventDefault()
      if (at.zone === 'lib') del(at.i)
      else move(k, at, { zone: 'lib', i: g.lib.length })
    }
  }

  const patchSel = (f: (k: SoftKey) => SoftKey) => {
    if (sel === null) return
    setG((prev) => ({ ...prev, lib: prev.lib.map((k, j) => (j === sel ? f(k) : k)) }))
  }

  const del = (i: number) => {
    setG((prev) => ({ ...prev, lib: prev.lib.filter((_, j) => j !== i) }))
    setSel(null)
  }

  /** 切一行 / 两行。切回一行时把第二行的键**并到第一行末尾** —— 服务端也是这么算的，
      而「存着但不显示」是最烦人的一种状态。 */
  const setLaneCount = (n: 1 | 2) => {
    setRows(n)
    if (n === 1) setG((prev) => ({ ...prev, 1: [...prev[1], ...prev[2]], 2: [] }))
  }

  /* ---------------------------------------------------------------- 存 / 复位 */

  const save = async () => {
    setErr('')
    try {
      const r = await api.put<SoftkeysSaved>('/softkeys', { rows, keys: flatten(g) })
      onSaved(r.keys, r.rows)
      setG(split(r.keys))
      setRows(r.rows)
      setSel(null)
      toast('软键条已保存')
    } catch (e) {
      setErr((e as Error).message)   // 服务端会指出是第几个按键、哪里不认
    }
  }

  const reset = async () => {
    setErr('')
    try {
      const r = await api.del<SoftkeysSaved>('/softkeys')
      onSaved(r.keys, r.rows)
      setG(split(r.keys))
      setRows(r.rows)
      setSel(null)
      toast('已恢复默认')
    } catch (e) {
      setErr((e as Error).message)
    }
  }

  /* ------------------------------------------------------------------- 渲染 */

  const chipCls = (k: SoftKey, on: boolean) =>
    cn(
      'flex shrink-0 items-center gap-1 rounded-[7px] border border-line bg-fg/8 px-2.5 py-1.5',
      'font-mono text-[12.5px] text-fg cursor-grab select-none active:cursor-grabbing',
      k.wide && 'min-w-[70px]',
      on && 'border-accent bg-accent/25',
    )

  const mark = (zone: Zone, i: number) =>
    over?.zone === zone && over.i === i ? <span className="mr-1 h-6 w-0.5 shrink-0 rounded bg-accent" /> : null

  /** 一个筐：栏（可排序 + ✕ 拿下来）或者键库（点一下改它） */
  const box = (zone: Zone, label: string, hint: string) => (
    <div className="flex items-start gap-1.5">
      <span className="w-11 shrink-0 pt-2 text-[11.5px] text-muted">{label}</span>
      <div
        ref={(el) => { zoneEl.current[zone] = el }}
        data-testid={`keys-zone-${zone}`}
        className={cn(
          'flex min-h-[42px] flex-1 flex-wrap content-start items-start gap-1.5 rounded-[8px] border border-dashed border-line p-1.5',
          over?.zone === zone && 'border-accent bg-accent/8',
        )}
      >
        {g[zone].map((k, i) => (
          <div key={i} className="flex items-center">
            {mark(zone, i)}
            <span
              data-chip
              role="button"
              tabIndex={0}
              className={chipCls(k, zone === 'lib' && sel === i)}
              title={
                zone === 'lib'
                  ? `${kindOf(k)} —— 点一下改它，按住拖到上面的栏里`
                  : `${kindOf(k)}${k.confirm ? '（要点两下）' : ''} —— 按住拖动排序，✕ 拿下来放回键库`
              }
              onPointerDown={(e) => onChipDown(e, k, { zone, i })}
              onKeyDown={(e) => onChipKey(e, { zone, i })}
            >
              {k.label || kindOf(k) || '（空）'}
              {zone !== 'lib' && (
                <X
                  className="size-3 shrink-0 text-muted hover:text-bad"
                  // 这一下不能起拖，也不能冒到键上去
                  onPointerDown={(e) => { e.stopPropagation(); e.preventDefault() }}
                  onClick={(e) => { e.stopPropagation(); move(k, { zone, i }, { zone: 'lib', i: g.lib.length }) }}
                />
              )}
            </span>
          </div>
        ))}
        {over?.zone === zone && over.i >= g[zone].length && <span className="h-6 w-0.5 rounded bg-accent" />}
        {g[zone].length === 0 && over?.zone !== zone && (
          <span className="px-1 py-1.5 text-[11.5px] text-muted">{hint}</span>
        )}
      </div>
    </div>
  )

  const selKey = sel === null ? undefined : g.lib[sel]

  const body = (
    <>
      {/* 行数 + 存盘。行数放在最前面：它决定下面画一栏还是两栏 */}
      <div className="mb-2 flex flex-wrap items-center gap-1.5">
        <span className="text-[11.5px] text-muted">软键条</span>
        <Button size="tiny" on={rows === 1} onClick={() => setLaneCount(1)} title="只要一行（键多了横滑）">一行</Button>
        <Button size="tiny" on={rows === 2} onClick={() => setLaneCount(2)} title="要两行，每行各自横滑">两行</Button>
        <span className="ml-auto text-[11.5px] text-muted">{total}/{max}</span>
        <Button size="tiny" variant="primary" onClick={save}>保存</Button>
        <Button size="tiny" variant="danger" onClick={reset}>恢复默认</Button>
      </div>

      <div className="flex flex-col gap-1.5">
        {box(1, rows === 2 ? '第一行' : '按键', '把下面键库里的键拖上来')}
        {rows === 2 && box(2, '第二行', '拖上来的键排在第二行')}
      </div>

      {/* 键库：定义在这儿改 —— 新增 / 改 / 删 */}
      <div className="mt-3 border-t border-line pt-2">
        <div className="mb-1.5 flex flex-wrap items-center gap-1.5">
          <span className="text-[11.5px] text-muted">
            键库（不上条的键待在这儿）：点一下改它，<strong>按住拖到上面</strong>就上条
          </span>
          <Button
            size="tiny"
            className="ml-auto shrink-0"
            title="加一个空的，自己填按键谱"
            onClick={() => {
              if (total >= max) { toast(`最多 ${max} 个按键`); return }
              setG((prev) => ({ ...prev, lib: [...prev.lib, { label: '', send: '' }] }))
              setSel(g.lib.length)
            }}
          >
            <Plus className="size-3" />新增
          </Button>
        </div>

        {box('lib', '我的键', '空的。从下面预设里挑，或者点「新增」自己写一个')}

        {/* 选中的那个库里的键 */}
        {selKey && (
          <div className="mt-1.5 flex flex-wrap items-center gap-1.5 rounded-[8px] bg-fg/5 p-1.5">
            <Input
              className="w-[5.5em] shrink-0"
              value={selKey.label}
              maxLength={12}
              placeholder="名字"
              onChange={(e) => patchSel((x) => ({ ...x, label: e.target.value }))}
            />
            <Input
              className="min-w-0 flex-1"
              value={kindOf(selKey)}
              placeholder="ctrl+b c"
              onChange={(e) => patchSel((x) => parseKind(e.target.value, x))}
            />
            <label className="flex shrink-0 items-center gap-1 text-[11px] text-muted" title="占宽一点">
              <Checkbox checked={!!selKey.wide} onCheckedChange={(v) => patchSel((x) => ({ ...x, wide: !!v }))} />
              宽
            </label>
            <label className="flex shrink-0 items-center gap-1 text-[11px] text-muted" title="点两下才真发出去：第一下只是举起来（变红），3 秒不点就放下">
              <Checkbox checked={!!selKey.confirm} onCheckedChange={(v) => patchSel((x) => ({ ...x, confirm: !!v }))} />
              两下
            </label>
            <Button size="tiny" variant="danger" className="shrink-0" title="彻底删掉这个键" onClick={() => del(sel!)}>
              <Trash2 className="size-3" />删掉
            </Button>
          </div>
        )}
      </div>

      {/* 内置预设：只读的来源，拖上去 / 点一下就进键库 */}
      <div className="mt-3 border-t border-line pt-2">
        <p className="mb-1.5 text-[11.5px] text-muted">
          预设：<strong>按住拖</strong>到上面的栏里（或键库里），或者点一下先进键库
        </p>
        <div className="flex flex-col gap-1.5">
          {presets.map((grp) => (
            <div key={grp.group} className="flex items-start gap-1.5">
              <span className="w-11 shrink-0 pt-1.5 text-[11.5px] text-muted">{grp.group}</span>
              <div className="flex flex-1 flex-wrap gap-1.5">
                {grp.items.map((it, n) => (
                  <span
                    key={n}
                    role="button"
                    tabIndex={0}
                    className={chipCls(it, false)}
                    title={`${kindOf(it)}${it.confirm ? '（两下）' : ''} —— 按住拖到上面，或者点一下进键库`}
                    onPointerDown={(e) => onChipDown(e, it, 'preset')}
                  >
                    {it.label}
                  </span>
                ))}
              </div>
            </div>
          ))}
        </div>
      </div>

      {err && <p className="mt-1.5 text-[11.5px] text-bad">{err}</p>}

      <p className="mt-2 text-[11.5px]/relaxed text-muted">
        <strong>两行各自横向滚动</strong>：手机上一行放得下四五个键，常用的放第一行、次常用的
        放第二行，比十几个键排成一条长龙好找。切回一行时第二行的键会并到第一行末尾。<br />
        栏里只管排布：<strong>拖着排序</strong>、<strong>✕ 拿下来</strong>（回键库，不是删）。
        改名字 / 按键谱、彻底删掉都在键库里。<br />
        「按键」一栏写按键谱，空格分隔可以连发多下 —— <code>ctrl+b c</code> 就是 herdr 的前缀加 c，一下点出来。<br />
        支持：<code>ctrl+x</code> <code>alt+x</code> <code>shift+tab</code>、具名键{' '}
        <code>esc tab enter space bs del ins up down left right home end pgup pgdn f1-f12</code>、
        原样文本两种写法都行：<code>"herdr" enter</code> 和 <code>text:/new enter</code>
        （<code>text:</code> 后面带空格的要加引号，<code>text:"git status"</code>）。<br />
        <code>Ctrl</code> / <code>Alt</code> 这种粘滞修饰键写 <code>sticky:ctrl</code> / <code>sticky:alt</code>，
        呼出键盘写 <code>act:kbd</code>、传图写 <code>act:img</code>（这两个是网页端自己处理，不发字节）。<br />
        勾上<strong>「两下」</strong>就是要点两次才真发出去：第一下只是举起来（键变红），
        3 秒不点就自己放下 —— 关 pane / 关标签这种误触没法撤销的键值得勾上。
      </p>

      {/* 跟着手指走的残影。fixed + pointer-events-none：它自己不能挡住命中判定 */}
      {drag && (
        <span
          className="pointer-events-none fixed z-50 -translate-x-1/2 -translate-y-1/2 rounded-[7px] border border-accent bg-accent/90 px-2.5 py-1.5 font-mono text-[12.5px] text-white shadow-lg"
          style={{ left: drag.x, top: drag.y }}
        >
          {drag.key.label || kindOf(drag.key)}
        </span>
      )}
    </>
  )

  return embedded ? body : <Panel title="软键条" onClose={onClose ?? (() => {})}>{body}</Panel>
}
