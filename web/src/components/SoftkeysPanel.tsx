import { useEffect, useRef, useState } from 'react'
import { Download, Plus, Trash2, X } from 'lucide-react'
import { api, type PresetGroup, type SoftKey, type SoftkeysConfig, type SoftkeysResponse } from '@/lib/api'
import { Button } from './ui/button'
import { Input } from './ui/input'
import { Checkbox } from './ui/checkbox'
import { Panel } from './ui/panel'
import { cn } from '@/lib/utils'

/** 拖放的三个筐：软键条第一行 / 第二行 / 「我的按键」 */
type Zone = 1 | 2 | 'lib'
type At = { zone: Zone; i: number }

/**
 * 触屏上按住这么久才算「把键拿起来」。
 *
 * 为什么要按住：这一页要能上下滚，而键本身就是拖动的把手 —— 手指落在键上往下划，到底是
 * 滚页面还是拖这个键，只能靠「有没有按住」区分。给键写死 `touch-action: none` 的话页面
 * 就滚不动了（键铺满了整页），而 `pan-y` 又会把「往下拖到第二行」吃成滚动。
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

/** 把「按键」栏的文本解回一条 SoftKey。id / 名字 / 宽 / 两下这些原样留着。 */
function parseKind(spec: string, k: SoftKey): SoftKey {
  const keep = { id: k.id, label: k.label, wide: k.wide, confirm: k.confirm }
  const m = spec.match(/^(sticky|act):(.+)$/)
  if (m) {
    return m[1] === 'sticky'
      ? { ...keep, sticky: m[2].trim() as 'ctrl' | 'alt' }
      : { ...keep, act: m[2].trim() as 'kbd' | 'img' }
  }
  return { ...keep, send: spec }
}

/** 同一个键的判定（「载入预设」去重用）：名字 + 干什么 */
const sig = (k: SoftKey) => `${k.label} ${kindOf(k)}`

/**
 * 发 id：接着现有最大的 k<n> 往下发。
 *
 * 前端必须**先**有 id 才能把新键放到条上（条上存的是引用），所以不能等服务端补。
 * 号段和服务端一致（k1、k2……），存下去看到的还是一串连号，不会混进随机串。
 */
function minter(lib: SoftKey[]) {
  let n = 0
  for (const k of lib) {
    const m = /^k(\d+)$/.exec(k.id ?? '')
    if (m) n = Math.max(n, Number(m[1]))
  }
  return () => `k${++n}`
}

/**
 * 软键条编辑器：**上面是软键条（一 / 两行），下面是「我的按键」**。
 *
 * 关键在于这两层是**引用**关系，不是搬家：条上放的是「我的按键」里某个键的 id。所以
 *
 *   - 拖上去是「选中它」，不是从库里拿走 —— 库里那个还在，同一个键**两行各放一个也行**
 *     （Esc 第一行来一个、第二行也来一个，完全合法）；
 *   - 改一处定义，条上所有引用一起变；
 *   - 从条上 ✕ 掉只是去掉一个引用，定义还在库里。
 *
 * 之前是一长串竖排的表单行（每个键一行：把手 / 名字 / 按键谱 / 两个勾 / 删除）。信息全，
 * 但看不出「排出来是什么样」—— 而这一页要调的恰恰就是排布。十四行表单在手机上还要滚三屏。
 *
 * 内置预设不再单独占一片：六十多个键铺出去比整页都长，而且看着像能编辑其实不能。改成给一个
 * 「载入预设」，一下全灌进「我的按键」，之后每个都能改名字 / 改按键谱 / 删。
 *
 * 拖动用 Pointer Events 手写，不用 HTML5 drag-and-drop —— 后者在触屏上根本不触发，
 * 而平板 / 手机是这个项目的主设备。
 */
export function SoftkeysPanel({
  onClose, onSaved, toast, embedded,
}: {
  onClose?: () => void
  onSaved: (lib: SoftKey[], bar: string[][]) => void
  toast: (m: string) => void
  embedded?: boolean
}) {
  const [lib, setLib] = useState<SoftKey[]>([])
  const [bar, setBar] = useState<string[][]>([[]])
  const [rows, setRows] = useState<1 | 2>(1)
  const [presets, setPresets] = useState<PresetGroup[]>([])
  const [max, setMax] = useState(120)
  const [maxBar, setMaxBar] = useState(40)
  const [err, setErr] = useState('')
  /** 选中的那个定义（id），下面那条编辑它 */
  const [selId, setSelId] = useState<string | null>(null)

  const take = (c: SoftkeysConfig) => {
    // send 回填成**用户写的按键谱**：服务端回来的 send 是解析好的字节（Tab 是 "\t"），
    // 编辑器要显示和回传的是 spec。不换的话存回去就是「按键谱是空的」（踩过）。
    setLib(c.lib.map((k) => ({ ...k, send: k.spec ?? k.send })))
    setRows(c.rows)
    // 行数是设置，栏的数量跟着它 —— 少的那行补个空数组，省得处处判 undefined
    setBar(Array.from({ length: c.rows }, (_, i) => c.bar[i] ?? []))
  }

  useEffect(() => {
    void (async () => {
      try {
        const r = await api.get<SoftkeysResponse>('/softkeys')
        take(r)
        setPresets(r.presets)
        setMax(r.max || 120)
        setMaxBar(r.maxBar || 40)
      } catch (e) {
        setErr((e as Error).message)
      }
    })()
  }, [])

  const byId = new Map(lib.map((k) => [k.id, k]))
  /** 某个筐里第 i 个是哪个键（认不出就当空，正常不会有） */
  const at = (zone: Zone, i: number): SoftKey | undefined =>
    zone === 'lib' ? lib[i] : byId.get(bar[zone - 1]?.[i] ?? '')

  /* ------------------------------------------------------------ 拖动 */

  const zoneEl = useRef<Partial<Record<Zone, HTMLDivElement | null>>>({})
  const [drag, setDrag] = useState<{ key: SoftKey; from: At; x: number; y: number } | null>(null)
  const [over, setOver] = useState<At | null>(null)

  const zones = (): Zone[] => (rows === 2 ? [1, 2, 'lib'] : [1, 'lib'])

  /**
   * 指针落在哪个筐的第几个位置。
   *
   * 筐里是**换行**排的（和真软键条一样），所以先按 y 找到同一视觉行上的那几个键，再在这
   * 一行里比 x 的中点。只比 x 的话，两行键叠在一起时插入点会跳到上一行去。
   */
  const hit = (x: number, y: number): At | null => {
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

  /** 落一次拖动 */
  const drop = (from: At, to: At) => {
    const key = at(from.zone, from.i)
    if (!key?.id) return
    const id = key.id

    // 从「我的按键」拖到条上 = 选中它（库里那个不动，所以能重复放）
    if (from.zone === 'lib' && to.zone !== 'lib') {
      const ti = to.zone - 1
      setBar((prev) => {
        if (prev[ti].length >= maxBar) { toast(`一行最多 ${maxBar} 个`); return prev }
        const next = prev.map((r) => [...r])
        next[ti].splice(to.i, 0, id)
        return next
      })
      return
    }
    // 条上 → 条上：挪引用（同行内排序 / 跨行搬）
    if (from.zone !== 'lib' && to.zone !== 'lib') {
      const fi = from.zone - 1
      const ti = to.zone - 1
      setBar((prev) => {
        if (fi !== ti && prev[ti].length >= maxBar) { toast(`一行最多 ${maxBar} 个`); return prev }
        const next = prev.map((r) => [...r])
        next[fi].splice(from.i, 1)
        next[ti].splice(fi === ti && from.i < to.i ? to.i - 1 : to.i, 0, id)
        return next
      })
      return
    }
    // 条上 → 「我的按键」：从条上拿下来（定义还在库里）
    if (from.zone !== 'lib' && to.zone === 'lib') {
      const fi = from.zone - 1
      setBar((prev) => prev.map((r, ri) => (ri === fi ? r.filter((_, j) => j !== from.i) : r)))
      setSelId(id)
      return
    }
    // 库里排序：库大了（载入预设六十多个）之后，常用的排前面找得快
    setLib((prev) => {
      const next = [...prev]
      const [k] = next.splice(from.i, 1)
      next.splice(from.i < to.i ? to.i - 1 : to.i, 0, k)
      return next
    })
  }

  /**
   * 按下一个键。触屏按住 HOLD_MS 才算拿起、鼠标走 MOVE_SLOP 就算拖；
   * 没拿起就松手 = 点一下（选中这个定义，下面那条改它）。
   */
  const onChipDown = (e: React.PointerEvent, from: At) => {
    const key = at(from.zone, from.i)
    if (!key) return
    const target = e.currentTarget as HTMLElement
    const touch = e.pointerType !== 'mouse'
    const x0 = e.clientX
    const y0 = e.clientY
    let picked = false
    let hold: number | undefined
    let to: At | null = null
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
      // 落在筐外面 = 什么都不做（放回原处）。误删太贵，删只走「删掉」那个按钮
      if (picked) {
        if (to) drop(from, to)
        return
      }
      setSelId(key.id ?? null)   // 没拿起来就松手 = 点一下 = 选中它
    }
    const up = () => stop(true)
    const cancel = () => stop(false)

    target.addEventListener('pointermove', onMove)
    target.addEventListener('pointerup', up)
    target.addEventListener('pointercancel', cancel)
  }

  /** 键盘也要能排：← → 本筐里挪，↑ ↓ 换筐，Delete 从条上拿下来 / 在库里删掉 */
  const onChipKey = (e: React.KeyboardEvent, from: At) => {
    const order = zones()
    const n = order.indexOf(from.zone)
    if (e.key === 'ArrowLeft' || e.key === 'ArrowRight') {
      e.preventDefault()
      drop(from, { zone: from.zone, i: from.i + (e.key === 'ArrowLeft' ? -1 : 2) })
    } else if (e.key === 'ArrowUp' || e.key === 'ArrowDown') {
      e.preventDefault()
      const z = order[Math.min(Math.max(n + (e.key === 'ArrowUp' ? -1 : 1), 0), order.length - 1)]
      if (z !== from.zone) drop(from, { zone: z, i: z === 'lib' ? lib.length : (bar[z - 1]?.length ?? 0) })
    } else if (e.key === 'Delete' || e.key === 'Backspace') {
      e.preventDefault()
      if (from.zone === 'lib') del(at('lib', from.i))
      else drop(from, { zone: 'lib', i: lib.length })
    }
  }

  /* ------------------------------------------------------------ 库里的增删改 */

  const patchSel = (f: (k: SoftKey) => SoftKey) =>
    setLib((prev) => prev.map((k) => (k.id === selId ? f(k) : k)))

  /** 彻底删掉一个定义：条上的引用一起清掉，不然保存时就是「引用了不存在的按键」 */
  const del = (k?: SoftKey) => {
    if (!k?.id) return
    const used = bar.reduce((n, r) => n + r.filter((id) => id === k.id).length, 0)
    setLib((prev) => prev.filter((x) => x.id !== k.id))
    setBar((prev) => prev.map((r) => r.filter((id) => id !== k.id)))
    setSelId(null)
    if (used) toast(`「${k.label}」删了，条上那 ${used} 处也一起去掉了`)
  }

  const add = () => {
    if (lib.length >= max) { toast(`「我的按键」最多 ${max} 个`); return }
    const id = minter(lib)()
    setLib((prev) => [...prev, { id, label: '', send: '' }])
    setSelId(id)
  }

  /** 把内置预设灌进「我的按键」（已经有的按「名字 + 干什么」跳过） */
  const loadPresets = () => {
    const have = new Set(lib.map(sig))
    const want = presets.flatMap((g) => g.items).filter((it) => !have.has(sig(it)))
    if (!want.length) { toast('预设都已经在「我的按键」里了'); return }
    const id = minter(lib)
    const fresh = want.slice(0, Math.max(0, max - lib.length)).map((k) => ({ ...k, id: id() }))
    setLib((prev) => [...prev, ...fresh])
    toast(fresh.length < want.length
      ? `加了 ${fresh.length} 个（到上限 ${max} 了）`
      : `加了 ${fresh.length} 个，按住拖到上面就上条`)
  }

  /** 切一行 / 两行。切回一行时第二行的引用接到第一行末尾（服务端也是这么算的） */
  const setLaneCount = (n: 1 | 2) => {
    setRows(n)
    setBar((prev) => (n === 1
      ? [[...(prev[0] ?? []), ...(prev[1] ?? [])]]
      : [prev[0] ?? [], prev[1] ?? []]))
  }

  /* ---------------------------------------------------------------- 存 / 复位 */

  const save = async () => {
    setErr('')
    try {
      const r = await api.put<SoftkeysConfig>('/softkeys', { rows, lib, bar })
      take(r)
      onSaved(r.lib, r.bar)
      setSelId(null)
      toast('软键条已保存')
    } catch (e) {
      setErr((e as Error).message)   // 服务端会指出是第几个按键、哪里不认
    }
  }

  const reset = async () => {
    setErr('')
    try {
      const r = await api.del<SoftkeysConfig>('/softkeys')
      take(r)
      onSaved(r.lib, r.bar)
      setSelId(null)
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

  /** 一个筐。zone 是 'lib' 时是「我的按键」（不出 ✕），否则是条上的一行 */
  const box = (zone: Zone, label: string, hint: string, count: number) => (
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
        {Array.from({ length: count }, (_, i) => {
          const k = at(zone, i)
          if (!k) return null
          return (
            <div key={`${zone}-${i}`} className="flex items-center">
              {over?.zone === zone && over.i === i && <span className="mr-1 h-6 w-0.5 shrink-0 rounded bg-accent" />}
              <span
                data-chip
                role="button"
                tabIndex={0}
                className={chipCls(k, selId === k.id)}
                title={
                  zone === 'lib'
                    ? `${kindOf(k)} —— 点一下改它，按住拖到上面就上条（库里这个还在）`
                    : `${kindOf(k)}${k.confirm ? '（要点两下）' : ''} —— 按住拖动排序，✕ 从条上拿下来`
                }
                onPointerDown={(e) => onChipDown(e, { zone, i })}
                onKeyDown={(e) => onChipKey(e, { zone, i })}
              >
                {k.label || kindOf(k) || '（空）'}
                {zone !== 'lib' && (
                  <X
                    className="size-3 shrink-0 text-muted hover:text-bad"
                    // 这一下不能起拖，也不能冒到键上去
                    onPointerDown={(ev) => { ev.stopPropagation(); ev.preventDefault() }}
                    onClick={(ev) => { ev.stopPropagation(); drop({ zone, i }, { zone: 'lib', i: lib.length }) }}
                  />
                )}
              </span>
            </div>
          )
        })}
        {over?.zone === zone && over.i >= count && <span className="h-6 w-0.5 rounded bg-accent" />}
        {count === 0 && over?.zone !== zone && (
          <span className="px-1 py-1.5 text-[11.5px] text-muted">{hint}</span>
        )}
      </div>
    </div>
  )

  const sel = lib.find((k) => k.id === selId)

  const body = (
    <>
      {/* 行数 + 存盘。行数放最前面：它决定下面画一栏还是两栏 */}
      <div className="mb-2 flex flex-wrap items-center gap-1.5">
        <span className="text-[11.5px] text-muted">软键条</span>
        <Button size="tiny" on={rows === 1} onClick={() => setLaneCount(1)} title="只要一行（键多了横滑）">一行</Button>
        <Button size="tiny" on={rows === 2} onClick={() => setLaneCount(2)} title="要两行，每行各自横滑">两行</Button>
        <span className="ml-auto text-[11.5px] text-muted tabular-nums">{bar.reduce((n, r) => n + r.length, 0)} 个在条上</span>
        <Button size="tiny" variant="primary" onClick={save}>保存</Button>
        <Button size="tiny" variant="danger" onClick={reset}>恢复默认</Button>
      </div>

      <div className="flex flex-col gap-1.5">
        {box(1, rows === 2 ? '第一行' : '按键', '从下面「我的按键」拖上来', bar[0]?.length ?? 0)}
        {rows === 2 && box(2, '第二行', '拖上来的键排在第二行', bar[1]?.length ?? 0)}
      </div>

      {/* 我的按键：所有定义都在这儿，新增 / 改 / 删都在这儿 */}
      <div className="mt-3 border-t border-line pt-2">
        <div className="mb-1.5 flex flex-wrap items-center gap-1.5">
          <span className="text-[11.5px] text-muted">
            我的按键 <span className="tabular-nums">{lib.length}/{max}</span>：点一下改它，<strong>按住拖到上面</strong>就上条
          </span>
          <Button
            size="tiny"
            className="ml-auto shrink-0"
            title="把内置预设加进「我的按键」（已经有的跳过），之后每个都能自己改"
            onClick={loadPresets}
          >
            <Download className="size-3" />载入预设
          </Button>
          <Button size="tiny" className="shrink-0" title="加一个空的，自己填按键谱" onClick={add}>
            <Plus className="size-3" />新增
          </Button>
        </div>

        {box('lib', '我的键', '空的。点「载入预设」把内置的那几十个灌进来，或者「新增」自己写一个', lib.length)}

        {/* 选中的那个定义 —— 改这里，条上所有引用一起变 */}
        {sel && (
          <div className="mt-1.5 flex flex-wrap items-center gap-1.5 rounded-[8px] bg-fg/5 p-1.5">
            <Input
              className="w-[5.5em] shrink-0"
              value={sel.label}
              maxLength={12}
              placeholder="名字"
              onChange={(e) => patchSel((x) => ({ ...x, label: e.target.value }))}
            />
            <Input
              className="min-w-0 flex-1"
              value={kindOf(sel)}
              placeholder="ctrl+b c"
              onChange={(e) => patchSel((x) => parseKind(e.target.value, x))}
            />
            <label className="flex shrink-0 items-center gap-1 text-[11px] text-muted" title="占宽一点">
              <Checkbox checked={!!sel.wide} onCheckedChange={(v) => patchSel((x) => ({ ...x, wide: !!v }))} />
              宽
            </label>
            <label className="flex shrink-0 items-center gap-1 text-[11px] text-muted" title="点两下才真发出去：第一下只是举起来（变红），3 秒不点就放下">
              <Checkbox checked={!!sel.confirm} onCheckedChange={(v) => patchSel((x) => ({ ...x, confirm: !!v }))} />
              两下
            </label>
            <Button size="tiny" variant="danger" className="shrink-0" title="彻底删掉这个键（条上的引用一起去掉）" onClick={() => del(sel)}>
              <Trash2 className="size-3" />删掉
            </Button>
          </div>
        )}
      </div>

      {err && <p className="mt-1.5 text-[11.5px] text-bad">{err}</p>}

      <p className="mt-2 text-[11.5px]/relaxed text-muted">
        条上放的是「我的按键」里那个键的<strong>引用</strong>：拖上去是选中它，库里那个还在，
        所以<strong>同一个键两行各放一个也行</strong>；改一处定义，条上所有引用一起变；
        ✕ 只是从条上拿下来，不是删。<br />
        <strong>两行各自横向滚动</strong>：手机上一行放得下四五个键，常用的放第一行、次常用的
        放第二行，比十几个键排成一条长龙好找。切回一行时第二行会接到第一行末尾。<br />
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
