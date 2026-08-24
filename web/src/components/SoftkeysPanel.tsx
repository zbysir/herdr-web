import { useEffect, useRef, useState } from 'react'
import { Download, Plus, Trash2, X } from 'lucide-react'
import { api, type Pin, type PresetGroup, type SoftKey, type SoftkeysConfig, type SoftkeysResponse } from '@/lib/api'
import { useChipDrag, type ChipAt } from '@/lib/chipdrag'
import { MAX_GROUP_COLS, MAX_SPAN, spanStyle } from '@/lib/keys'
import { KEY_ICONS, keyFace } from '@/keyicons'
import type { KeyAct } from '@/capabilities'
import { Button } from './ui/button'
import { Input } from './ui/input'
import { Checkbox } from './ui/checkbox'
import { Panel } from './ui/panel'
import { cn } from '@/lib/utils'

/**
 * 拖放的筐：软键条第一行 / 第二行 / 「我的按键」/ 选中那个**弹出组**的格子。
 * 手势本身在 lib/chipdrag（和顶栏编辑器共用）。
 *
 * `'pad'` 和别的筐**语义不一样**：它是定长网格，落哪一格就**替换**那一格（别的筐是
 * 插进序列里）。命中判定也另一套，见 chipdrag 的 `slots`。
 */
type Zone = 1 | 2 | 'lib' | 'group'
type At = ChipAt<Zone>

/** 是条上的一行吗（1 / 2）。剩下两个筐（库 / 弹出组的格子）都不是「行」 */
const isRow = (z: Zone): z is 1 | 2 => z === 1 || z === 2

/** 定位格的筐（网格，落哪一格就是哪一格）：目前只有「选中那个弹出组」 */
const isSlots = (z: Zone) => z === 'group'

/** 「按键」栏怎么显示：sticky/act 用 `sticky:ctrl` 这种写法，其余就是按键谱。 */
const kindOf = (k: SoftKey) =>
  (k.group ? `组·${k.group.cols} 列`
    : k.sticky ? `sticky:${k.sticky}`
      : k.act ? `act:${k.act}`
        : (k.spec ?? k.send ?? ''))

/** 把「按键」栏的文本解回一条 SoftKey。id / 名字 / 宽 / 两下这些原样留着。 */
function parseKind(spec: string, k: SoftKey): SoftKey {
  const keep = { id: k.id, label: k.label, span: k.span, icon: k.icon, confirm: k.confirm }
  const m = spec.match(/^(sticky|act):(.+)$/)
  if (m) {
    return m[1] === 'sticky'
      ? { ...keep, sticky: m[2].trim() as 'ctrl' | 'alt' }
      : { ...keep, act: m[2].trim() as KeyAct }   // 认不认由服务端那张表判，报错里会列全
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
  onClose, onSaved, toast, embedded, profile,
}: {
  onClose?: () => void
  /** 存好之后把**整份**配置交回去（rows / lib / bar / pad），软键条立刻跟着变 */
  onSaved: (c: SoftkeysConfig) => void
  toast: (m: string) => void
  embedded?: boolean
  /**
   * 改**哪一套**排布（见 internal/profiles）。id 一路带到 GET 和 PUT 上：
   * 存的时候原样带回去，中间要是有人在别的设备上改了这台的绑定，也不会静默存到另一套上。
   * 名字只是显示用 —— 「我在改哪一套」得一直看得见，这一页能拖十分钟。
   */
  profile: { id: string; name: string }
}) {
  const [lib, setLib] = useState<SoftKey[]>([])
  const [bar, setBar] = useState<string[][]>([[]])
  const [rows, setRows] = useState<1 | 2>(1)
  const [presets, setPresets] = useState<PresetGroup[]>([])
  const [max, setMax] = useState(120)
  const [maxBar, setMaxBar] = useState(40)
  /**
   * 每行两端**钉住**几个（不跟着横滑）。存的是**个数**，`bar` 那一行照旧是完整顺序 ——
   * 见服务端 internal/softkeys 的 Pin。
   */
  const [pin, setPin] = useState<Pin[]>([])
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
    setPin(c.pin ?? [])
  }

  useEffect(() => {
    void (async () => {
      try {
        const r = await api.get<SoftkeysResponse>(`/softkeys?profile=${encodeURIComponent(profile.id)}`)
        take(r)
        setPresets(r.presets)
        setMax(r.max || 120)
        setMaxBar(r.maxBar || 40)
      } catch (e) {
        setErr((e as Error).message)
      }
    })()
    // 换了一套就整份重读（面板开着的时候也能换，上面那一行就在同一块面板里）
  }, [profile.id])

  const byId = new Map(lib.map((k) => [k.id, k]))
  /** 某个筐里第 i 个是哪个键（认不出就当空，正常不会有） */
  /** 正在改的那个定义（下面那条编辑它）。选中一个弹出组时它的格子也能拖 */
  const sel = lib.find((k) => k.id === selId)

  const at = (zone: Zone, i: number): SoftKey | undefined =>
    zone === 'lib' ? lib[i]
      : zone === 'group' ? byId.get(sel?.group?.cells[i] ?? '')
        : byId.get(bar[zone - 1]?.[i] ?? '')

  /** 改选中那个组的格子。组是**定义**，所以改的是 lib 里那一条 */
  const setGroupCells = (f: (cells: string[]) => string[]) =>
    setLib((prev) => prev.map((k) => (k.id === selId && k.group
      ? { ...k, group: { ...k.group, cells: f(k.group.cells) } }
      : k)))

  /* ------------------------------------------------------------ 拖动 */

  const zoneEl = useRef<Partial<Record<Zone, HTMLDivElement | null>>>({})
  const zones = (): Zone[] => [
    ...(rows === 2 ? [1, 2] as Zone[] : [1] as Zone[]),
    'lib',
    // 选中的那个组排在库**后面**：屏幕上它就在库下面（键盘换筐按这个顺序）
    ...(sel?.group ? ['group'] as Zone[] : []),
  ]

  /** 把某个定位格清空 */
  const clearSlot = (a: At) =>
    setGroupCells((cells) => cells.map((c, j) => (j === a.i ? '' : c)))

  /** 把条上某一格的引用去掉（定义不动） */
  const dropFromRow = (a: At) => {
    if (!isRow(a.zone)) return
    const fi = a.zone - 1
    setBar((prev) => prev.map((r, ri) => (ri === fi ? r.filter((_, j) => j !== a.i) : r)))
  }

  /** 往条上某一行插一个引用 */
  const putInRow = (z: 1 | 2, i: number, id: string) => {
    const ti = z - 1
    setBar((prev) => {
      if (prev[ti].length >= maxBar) { toast(`一行最多 ${maxBar} 个`); return prev }
      const next = prev.map((r) => [...r])
      next[ti].splice(i, 0, id)
      return next
    })
  }

  /** 落一次拖动 */
  const drop = (from: At, to: At) => {
    const key = at(from.zone, from.i)
    if (!key?.id) return
    const id = key.id

    // 库里排序：库大了（载入预设六十多个）之后，常用的排前面找得快
    if (from.zone === 'lib' && to.zone === 'lib') {
      setLib((prev) => {
        const next = [...prev]
        const [k] = next.splice(from.i, 1)
        next.splice(from.i < to.i ? to.i - 1 : to.i, 0, k)
        return next
      })
      return
    }

    // 落进定位格（弹出组的格子）：**替换**那一格（格子按位置排，「插到第 3 格前面」
    // 没有意义）。同一个筐内互拖 = 两格**对调** —— 顶掉原来那个不对：网格上人想的是
    // 「换个位置」，而被顶掉的那个键会静悄悄消失
    if (isSlots(to.zone)) {
      const put = setGroupCells
      // 组里不能再放组（服务端也挡着）—— 嵌套只会变成「点开还要再点开」
      if (to.zone === 'group' && key.group) { toast('弹出组里不能再放一个弹出组'); return }
      if (from.zone === to.zone) {
        put((cells) => cells.map((c, j) => (j === to.i ? cells[from.i] : j === from.i ? cells[to.i] : c)))
        return
      }
      put((cells) => cells.map((c, j) => (j === to.i ? id : c)))
      if (isRow(from.zone)) dropFromRow(from)         // 从条上搬过来 = 条上那个拿掉
      else if (isSlots(from.zone)) clearSlot(from)     // 从另一个网格搬过来
      return
    }

    // 从定位格拖出去：那一格清空
    if (isSlots(from.zone)) {
      clearSlot(from)
      if (isRow(to.zone)) putInRow(to.zone, to.i, id)
      else setSelId(id) // 拖回库里 = 只是从格子上拿下来（定义还在）
      return
    }

    // 从「我的按键」拖到条上 = 选中它（库里那个不动，所以能重复放）
    if (from.zone === 'lib' && isRow(to.zone)) {
      putInRow(to.zone, to.i, id)
      return
    }
    // 条上 → 条上：挪引用（同行内排序 / 跨行搬）
    if (isRow(from.zone) && isRow(to.zone)) {
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
    if (isRow(from.zone) && to.zone === 'lib') {
      dropFromRow(from)
      setSelId(id)
    }
  }

  // 手势（按住才拿起、落在哪一格、影子跟手指）在 lib/chipdrag 里，和顶栏编辑器共用一份
  const { drag, over, onChipDown } = useChipDrag<Zone>({
    zones,
    elOf: (z) => zoneEl.current[z],
    slots: isSlots, // 弹出组的格子是定长网格，落哪一格就是哪一格
    onDrop: drop,
    onTap: (a) => setSelId(at(a.zone, a.i)?.id ?? null), // 没拿起来就松手 = 选中它，下面那条改它
  })

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
      // 落到哪一格：库 / 条都是「接到末尾」，定位格是「第一个空格」
      // —— 定长网格没有「末尾」这回事
      const cellsOf = z === 'group' ? sel?.group?.cells : undefined
      const i = z === 'lib' ? lib.length
        : cellsOf ? Math.max(0, cellsOf.findIndex((c) => !c))
          : isRow(z) ? (bar[z - 1]?.length ?? 0) : 0
      if (z !== from.zone) drop(from, { zone: z, i })
    } else if (e.key === 'Delete' || e.key === 'Backspace') {
      e.preventDefault()
      if (from.zone === 'lib') del(at('lib', from.i))
      else drop(from, { zone: 'lib', i: lib.length }) // 条上 / 格子里：只拿下来
    }
  }

  /* ------------------------------------------------------------ 库里的增删改 */

  const patchSel = (f: (k: SoftKey) => SoftKey) =>
    setLib((prev) => prev.map((k) => (k.id === selId ? f(k) : k)))

  /** 彻底删掉一个定义：条上的引用一起清掉，不然保存时就是「引用了不存在的按键」 */
  const del = (k?: SoftKey) => {
    if (!k?.id) return
    const inGroups = lib.reduce((n, x) => n + (x.group?.cells.filter((id) => id === k.id).length ?? 0), 0)
    const used = bar.reduce((n, r) => n + r.filter((id) => id === k.id).length, 0) + inGroups
    // 删掉定义本身，**并且把所有弹出组里指向它的格子清空** —— 漏了的话保存直接被服务端拒
    // （「引用了不存在的按键」），而用户只是删了一个键
    setLib((prev) => prev
      .filter((x) => x.id !== k.id)
      .map((x) => (x.group
        ? { ...x, group: { ...x.group, cells: x.group.cells.map((id) => (id === k.id ? '' : id)) } }
        : x)))
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

  /* ---------------------------------------------------------------- 弹出组 */

  /**
   * 加一个**方向键的弹出组**：条上只占一格，点开才是那片网格
   *
   *     ·  ↑  ·
   *     ←  ↓  →
   *
   * 为什么是弹出组而不是摊在条上：摊开要 3×2 六格，手机竖屏上那是半条屏幕（用户的原话：
   * 「一下占掉 4 个位置太离谱」）。也不是「加一个空的自己拖」—— 那是六步（新增 → 选列数 →
   * 拖四次），而这个功能的卖点恰恰就是那六步的结果。
   *
   * 四个方向键库里没有就从**内置预设**里取一份补进去（和「载入预设」同一份来源），
   * 所以新装一台机器上也是一下点出来。
   */
  const addArrowGroup = () => {
    const want = ['up', 'down', 'left', 'right']
    const mint = minter(lib)
    const next = [...lib]
    const id: Record<string, string> = {}
    for (const spec of want) {
      const have = next.find((k) => kindOf(k) === spec)
      if (have?.id) { id[spec] = have.id; continue }
      const from = presets.flatMap((g) => g.items).find((k) => kindOf(k) === spec)
      if (!from || next.length >= max) continue
      const fresh = { ...from, id: mint() }
      next.push(fresh)
      id[spec] = fresh.id
    }
    if (Object.keys(id).length < want.length) {
      toast('方向键在「我的按键」里找不到 —— 先点「载入预设」')
      return
    }
    const cols = 3
    const cells = ['', id.up, '', id.left, id.down, id.right]
    while (cells.length < cols * 3) cells.push('')
    const gid = mint()
    next.push({ id: gid, label: '方向', group: { cols, cells } })
    setLib(next)
    // 上条：**只占一格**（这就是这个功能的全部意义）
    setBar((prev) => { const out = prev.map((r) => [...r]); out[0].push(gid); return out })
    setSelId(gid)
    const added = next.length - lib.length - 1
    toast(`方向键加好了 —— 条上只占一格，点开是那四个键${added ? `（顺便补了 ${added} 个方向键定义）` : ''}`)
  }

  /** 加一个空的弹出组，自己往格子里拖 */
  const addGroup = () => {
    if (lib.length >= max) { toast(`「我的按键」最多 ${max} 个`); return }
    const gid = minter(lib)()
    setLib((prev) => [...prev, { id: gid, label: '组', group: { cols: 3, cells: Array(9).fill('') } }])
    setSelId(gid)
  }

  /** 改选中那个组的列数。格子**按行重映射** —— 线性截断会把第二行整段错位 */
  const setGroupCols = (cols: number) => setLib((prev) => prev.map((k) => {
    if (k.id !== selId || !k.group) return k
    const oc = k.group.cols
    const old = k.group.cells
    const cells: string[] = []
    for (let r = 0; r < 3; r++) {
      for (let c = 0; c < cols; c++) cells.push(c < oc ? (old[r * oc + c] ?? '') : '')
    }
    return { ...k, group: { cols, cells } }
  }))

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
      const r = await api.put<SoftkeysConfig>(
        `/softkeys?profile=${encodeURIComponent(profile.id)}`, { rows, lib, bar, pin },
      )
      take(r)
      onSaved(r)
      setSelId(null)
      toast(`「${profile.name}」的软键条已保存`)
    } catch (e) {
      setErr((e as Error).message)   // 服务端会指出是第几个按键、哪里不认
    }
  }

  const reset = async () => {
    setErr('')
    try {
      const r = await api.del<SoftkeysConfig>(`/softkeys?profile=${encodeURIComponent(profile.id)}`)
      take(r)
      onSaved(r)
      setSelId(null)
      toast('已恢复默认')
    } catch (e) {
      setErr((e as Error).message)
    }
  }

  /* ------------------------------------------------------------------- 渲染 */

  const chipCls = (on: boolean) =>
    cn(
      'flex shrink-0 items-center gap-1 rounded-md border border-line bg-ctl px-2.5 py-1.5',
      'font-mono text-xs text-fg cursor-grab select-none active:cursor-grabbing',
      'transition-[background-color,border-color,color] duration-100 hover:border-line-hi hover:bg-ctl-hi',
      // 宽窄跟条上同一个算法（style 里的 spanStyle），不然编辑器里看着一样、拖上去才
      // 发现不一样。
      // 正在改的那个：淡绿底 + 绿字。别整块涂满 —— 库里几十个键并排，
      // 一块饱和色会把周围的键全压下去
      on && 'border-brand/50 bg-brand/12 text-brand hover:border-brand/50 hover:bg-brand/12',
    )

  /** 一个筐。zone 是 'lib' 时是「我的按键」（不出 ✕），否则是条上的一行 */
  const box = (zone: Zone, label: string, hint: string, count: number) => (
    <div className="flex items-start gap-2">
      <span className="w-11 shrink-0 pt-2.5 text-xs text-faint">{label}</span>
      <div
        ref={(el) => { zoneEl.current[zone] = el }}
        data-testid={`keys-zone-${zone}`}
        className={cn(
          // 虚线框 + 比面板暗一档的底：一眼看出「这是个筐，东西能拖进来」
          'flex min-h-[44px] flex-1 flex-wrap content-start items-start gap-1.5 rounded-lg p-2',
          'border border-dashed border-line-hi/70 bg-bg/40 transition-colors duration-100',
          over?.zone === zone && 'border-brand/60 bg-brand/8',
        )}
      >
        {Array.from({ length: count }, (_, i) => {
          const k = at(zone, i)
          if (!k) return null
          // 钉住那两段和滑动那段之间画一条竖线：一眼看出哪几个不跟着滑
          const rowPin = isRow(zone) ? pinAt(zone - 1) : {}
          const pinL = rowPin.left ?? 0
          const pinR = rowPin.right ?? 0
          const edge = (pinL > 0 && i === pinL) || (pinR > 0 && i === count - pinR)
          return (
            <div key={`${zone}-${i}`} className="flex items-center">
              {edge && <span title="这条线外面那几个不跟着横滑" className="mr-1.5 h-6 w-px shrink-0 bg-line-hi" />}
              {over?.zone === zone && over.i === i && <span className="mr-1 h-6 w-0.5 shrink-0 rounded-full bg-brand" />}
              <span
                data-chip
                role="button"
                tabIndex={0}
                className={chipCls(selId === k.id)}
                style={spanStyle(k.span)}
                title={
                  zone === 'lib'
                    ? `${kindOf(k)} —— 点一下改它，按住拖到上面就上条（库里这个还在）`
                    : `${kindOf(k)}${k.confirm ? '（要点两下）' : ''} —— 按住拖动排序，✕ 从条上拿下来`
                }
                onPointerDown={(e) => onChipDown(e, { zone, i })}
                onKeyDown={(e) => onChipKey(e, { zone, i })}
              >
                {keyFace(k.icon, k.label || kindOf(k) || '（空）')}
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
        {over?.zone === zone && over.i >= count && <span className="h-6 w-0.5 rounded-full bg-brand" />}
        {count === 0 && over?.zone !== zone && (
          <span className="px-1 py-1.5 text-xs text-faint">{hint}</span>
        )}
      </div>
    </div>
  )

  /**
   * 弹出组那个筐。**定长网格**：空格子也画出来（而且也带 `data-chip`）—— 「往空格里放
   * 一个」是这种筐最主要的用法，不画的话它压根不是落点（见 chipdrag 的 `slots`）。
   *
   * 列宽用 `minmax(var(--sk-w), auto)`：这一格至少是条上一个键位那么宽，但方块上还有名字
   * 和 ✕，撑得开就让它撑 —— 编辑器给的是**排布**（哪一格放什么），真正的宽度在条上。
   */
  /* ---------------------------------------------------------------- 钉住 */

  const pinAt = (ri: number): Pin => pin[ri] ?? {}

  /** 改一行的钉住个数。夹在 [0, 这一行剩下多少] 里 —— 和服务端 resolvePin 一个判据 */
  const setPinAt = (ri: number, side: 'left' | 'right', n: number) => setPin((prev) => {
    const out = Array.from({ length: Math.max(prev.length, rows) }, (_, i) => ({ ...(prev[i] ?? {}) }))
    const len = bar[ri]?.length ?? 0
    const other = (side === 'left' ? out[ri].right : out[ri].left) ?? 0
    out[ri][side] = Math.max(0, Math.min(n, len - other))
    return out
  })

  /**
   * 一行下面那条「钉住 左 − n + 右 − n +」。
   *
   * 为什么是**个数**而不是「选中这几个键→钉住」：条上那一行是有序的，钉住只能是两端连续的
   * 一段（中间那段才是滑的）。个数就是那条界线的位置，拖动排序的语义一个字都不用改。
   */
  const pinRow = (ri: number) => {
    const len = bar[ri]?.length ?? 0
    if (!len) return null
    const p = pinAt(ri)
    const step = (side: 'left' | 'right', label: string) => {
      const v = (side === 'left' ? p.left : p.right) ?? 0
      const other = (side === 'left' ? p.right : p.left) ?? 0
      return (
        <span className="flex items-center gap-1">
          {label}
          <span className="flex overflow-hidden rounded-md border border-line">
            <Button
              size="tiny" className="rounded-none border-0 border-r border-line px-1.5"
              disabled={v <= 0} title={`少钉一个`} onClick={() => setPinAt(ri, side, v - 1)}
            >
              −
            </Button>
            <span className="grid w-6 place-items-center bg-ctl text-xs tabular-nums">{v}</span>
            <Button
              size="tiny" className="rounded-none border-0 border-l border-line px-1.5"
              disabled={v + other >= len} title={`多钉一个`} onClick={() => setPinAt(ri, side, v + 1)}
            >
              +
            </Button>
          </span>
        </span>
      )
    }
    return (
      <div key={ri} className="flex flex-wrap items-center gap-2 pl-[52px] text-xs text-faint">
        <span>{rows === 2 ? `第${ri === 0 ? '一' : '二'}行钉住` : '钉住'}</span>
        {step('left', '左')}
        {step('right', '右')}
        {(p.left || p.right)
          ? <span className="text-muted">这几个不跟着横滑</span>
          : <span>头几个 / 尾几个可以不跟着横滑</span>}
      </div>
    )
  }

  /**
   * 弹出组那片定位格。**空格子也画出来、也带 `data-chip`** ——
   * 「往空格里放一个」是这种筐最主要的用法，不画的话它压根不是落点（见 chipdrag 的 `slots`）。
   *
   * 列宽用 `minmax(var(--sk-w), auto)`：至少一个键位宽，但方块上还有名字和 ✕，撑得开就让它撑
   * —— 编辑器给的是**排布**（哪一格放什么），真正的宽度在条上 / 浮窗里。
   */
  const gridBox = (zone: 'group', label: string, cols: number, cells: string[]) => (
    <div className="flex items-start gap-2">
      <span className="w-11 shrink-0 pt-2.5 text-xs text-faint">{label}</span>
      <div
        ref={(el) => { zoneEl.current[zone] = el }}
        data-testid={`keys-zone-${zone}`}
        className={cn(
          'grid w-max gap-1.5 rounded-lg border border-dashed border-line-hi/70 bg-bg/40 p-2',
          'transition-colors duration-100',
          over?.zone === zone && 'border-brand/60 bg-brand/8',
        )}
        style={{ gridTemplateColumns: `repeat(${cols}, minmax(var(--sk-w), auto))` }}
      >
        {cells.map((id, i) => {
          const k = byId.get(id)
          const hot = over?.zone === zone && over.i === i
          if (!k) {
            return (
              <span
                key={i}
                data-chip
                data-testid={`${zone}-slot-${i}`}
                className={cn(
                  'grid h-[30px] select-none place-items-center rounded-md border border-dashed',
                  'border-line text-xs text-faint',
                  hot && 'border-brand bg-brand/12 text-brand',
                )}
              >
                ·
              </span>
            )
          }
          return (
            <span
              key={i}
              data-chip
              data-testid={`${zone}-cell-${i}`}
              role="button"
              tabIndex={0}
              className={cn(chipCls(selId === k.id), 'justify-center px-1.5', hot && 'border-brand')}
              title={`${kindOf(k)} —— 按住拖到别的格子就对调，✕ 从这儿拿下来`}
              onPointerDown={(e) => onChipDown(e, { zone, i })}
              onKeyDown={(e) => onChipKey(e, { zone, i })}
            >
              {keyFace(k.icon, k.label || kindOf(k))}
              <X
                className="size-3 shrink-0 text-muted hover:text-bad"
                onPointerDown={(ev) => { ev.stopPropagation(); ev.preventDefault() }}
                onClick={(ev) => { ev.stopPropagation(); clearSlot({ zone, i }) }}
              />
            </span>
          )
        })}
      </div>
    </div>
  )


  const body = (
    <>
      {/* 行数 + 存盘。行数放最前面：它决定下面画一栏还是两栏 */}
      <div className="mb-2.5 flex flex-wrap items-center gap-2">
        <span className="text-[13px] font-medium">软键条</span>
        {/* 「在改哪一套」要一直看得见：这一页能拖十分钟，改错了套还得重来一遍 */}
        <span className="rounded border border-line bg-ctl px-1.5 py-0.5 text-xs text-muted">{profile.name}</span>
        {/* 「一行 / 两行」是二选一，贴成一个分段控件 —— 两个独立按钮并排时看不出
            它们是同一个选择（原来就是两个按钮，谁开着全靠颜色深浅去猜） */}
        <div className="flex overflow-hidden rounded-md border border-line">
          <Button
            size="tiny" on={rows === 1} onClick={() => setLaneCount(1)} title="只要一行（键多了横滑）"
            className="rounded-none border-0 border-r border-line"
          >
            一行
          </Button>
          <Button
            size="tiny" on={rows === 2} onClick={() => setLaneCount(2)} title="要两行，每行各自横滑"
            className="rounded-none border-0"
          >
            两行
          </Button>
        </div>
        <span className="ml-auto text-xs text-faint tabular-nums">{bar.reduce((n, r) => n + r.length, 0)} 个在条上</span>
        <Button
          size="tiny"
          variant="danger"
          title="只把这一套的条恢复成出厂那一排（「我的按键」里缺的补上，别的定义和别的套都不动）"
          onClick={reset}
        >
          恢复默认
        </Button>
        <Button size="tiny" variant="primary" onClick={save}>保存</Button>
      </div>

      <div className="flex flex-col gap-2">
        {box(1, rows === 2 ? '第一行' : '按键', '从下面「我的按键」拖上来', bar[0]?.length ?? 0)}
        {rows === 2 && box(2, '第二行', '拖上来的键排在第二行', bar[1]?.length ?? 0)}

        {/* 钉住：每行两端不跟着横滑的那几个。控件放在行下面，因为它管的是**那一行** */}
        {Array.from({ length: rows }, (_, ri) => pinRow(ri))}
      </div>

      {/* 我的按键：所有定义都在这儿，新增 / 改 / 删都在这儿 */}
      <div className="mt-4 border-t border-line pt-3">
        <div className="mb-2 flex flex-wrap items-center gap-2">
          <span className="text-[13px] font-medium">
            我的按键 <span className="ml-0.5 text-xs font-normal text-faint tabular-nums">{lib.length}/{max}</span>
          </span>
          {/* 原来这儿有一句「点一下改它，按住拖到上面就上条」。按钮多到四个之后它被挤成
              三行，而底下「怎么用」第一条说的就是同一件事 —— 去掉重复那一份 */}
          <span className="min-w-0 flex-1" />
          <Button
            size="tiny"
            className="shrink-0"
            title="把内置预设加进「我的按键」（已经有的跳过），之后每个都能自己改"
            onClick={loadPresets}
          >
            <Download className="size-3" />载入预设
          </Button>
          <Button
            size="tiny"
            className="shrink-0"
            title="加一个「方向」键：条上只占一格，点开浮出 ↑ ← ↓ → 四个键"
            onClick={addArrowGroup}
          >
            <Plus className="size-3" />方向键
          </Button>
          <Button
            size="tiny"
            className="shrink-0"
            title="加一个空的弹出组：条上占一格，点开浮出一小片键，自己往格子里拖"
            onClick={addGroup}
          >
            <Plus className="size-3" />弹出组
          </Button>
          <Button size="tiny" className="shrink-0" title="加一个空的，自己填按键谱" onClick={add}>
            <Plus className="size-3" />新增
          </Button>
        </div>

        {box('lib', '我的键', '空的。点「载入预设」把内置的那几十个灌进来，或者「新增」自己写一个', lib.length)}

        {/* 选中的那个定义 —— 改这里，条上所有引用一起变 */}
        {sel && (
          <div className="mt-2 flex flex-wrap items-center gap-2 rounded-lg border border-line bg-bg/50 p-2">
            <Input
              className="w-[5.5em] shrink-0"
              value={sel.label}
              maxLength={12}
              placeholder="名字"
              onChange={(e) => patchSel((x) => ({ ...x, label: e.target.value }))}
            />
            {sel.group ? (
              // 弹出组没有按键谱：它的内容是下面那片网格。这儿只给列数
              <div className="flex shrink-0 items-center gap-1.5 text-xs text-muted">
                <div className="flex overflow-hidden rounded-md border border-line">
                  {Array.from({ length: MAX_GROUP_COLS }, (_, i) => i + 1).map((n) => (
                    <Button
                      key={n}
                      size="tiny"
                      on={sel.group?.cols === n}
                      title={`${n} 列`}
                      className={cn('rounded-none border-0 px-2', n < MAX_GROUP_COLS && 'border-r border-line')}
                      onClick={() => setGroupCols(n)}
                    >
                      {n}
                    </Button>
                  ))}
                </div>
                列 · 点开是浮窗，条上只占一格
              </div>
            ) : (
              <Input
                className="min-w-0 flex-1"
                value={kindOf(sel)}
                placeholder="ctrl+b c"
                onChange={(e) => patchSel((x) => parseKind(e.target.value, x))}
              />
            )}
            {/* 宽度是**几格**，不是一个「宽不宽」的勾：格数才能跨行对齐（见 lib/keys.ts）。
                贴成分段控件，和上面「一行 / 两行」同一个样子 —— 三个独立按钮并排时看不出
                它们是同一个选择 */}
            <div className="flex shrink-0 items-center gap-1.5 text-xs text-muted">
              宽
              <div className="flex overflow-hidden rounded-md border border-line">
                {Array.from({ length: MAX_SPAN }, (_, i) => i + 1).map((n) => (
                  <Button
                    key={n}
                    size="tiny"
                    on={(sel.span ?? 1) === n}
                    title={`占 ${n} 格宽`}
                    className={cn('rounded-none border-0 px-2', n < MAX_SPAN && 'border-r border-line')}
                    onClick={() => patchSel((x) => ({ ...x, span: n }))}
                  >
                    {n}
                  </Button>
                ))}
              </div>
            </div>
            <label className="flex shrink-0 cursor-pointer items-center gap-1.5 text-xs text-muted" title="点两下才真发出去：第一下只是举起来（变红），3 秒不点就放下">
              <Checkbox checked={!!sel.confirm} onCheckedChange={(v) => patchSel((x) => ({ ...x, confirm: !!v }))} />
              两下
            </label>
            <Button size="tiny" variant="danger" className="shrink-0" title="彻底删掉这个键（条上的引用一起去掉）" onClick={() => del(sel)}>
              <Trash2 className="size-3" />删掉
            </Button>
            {/* 图标：挑一个内置的,条上就画它不画字（`⌨` 那种字形在很多字体里缺 / 难看 /
                基线对不齐）。**名字还留着** —— 编辑器里认它,title 里也显示它。
                一行铺开自己横滑,比弹一个下拉少一次点击 */}
            <div className="flex w-full items-center gap-1.5">
              <span className="shrink-0 text-xs text-faint">图标</span>
              <div className="flex min-w-0 flex-1 gap-1 overflow-x-auto [scrollbar-width:none] [&::-webkit-scrollbar]:hidden">
                <Button
                  size="tiny"
                  on={!sel.icon}
                  title="不用图标，条上画名字"
                  className="shrink-0 px-1.5"
                  onClick={() => patchSel((x) => ({ ...x, icon: undefined }))}
                >
                  文字
                </Button>
                {KEY_ICONS.map((ic) => (
                  <Button
                    key={ic.id}
                    size="tiny"
                    on={sel.icon === ic.id}
                    title={ic.hint}
                    className="shrink-0 px-1.5"
                    onClick={() => patchSel((x) => ({ ...x, icon: ic.id }))}
                  >
                    {ic.icon}
                  </Button>
                ))}
              </div>
            </div>

            {/* 组里放什么：往空格（·）上拖就是放进那一格，格里两格互拖是对调 */}
            {sel.group && (
              <div className="w-full">{gridBox('group', '组里', sel.group.cols, sel.group.cells)}</div>
            )}
          </div>
        )}
      </div>

      {err && <p className="mt-2 text-xs text-bad">{err}</p>}

      {/* 就地说明。**只写「怎么用」和「按键谱查什么」，不写「为什么这么设计」** ——
          理由那部分归 docs/dev/MOBILE.md，混进来的后果是用户面前这一块变成一堵墙（真机截图看着
          「不知所云」，用户报的）。按键谱那张表是唯一必须在手边的参考，所以给它一个
          两列的 dl，别混在句子里。
          裸 <code> 统一挂一个小灰块：分得出哪儿是要照抄的字面量。 */}
      <div className="mt-4 border-t border-line pt-3 text-xs/relaxed text-muted
                      [&_code]:rounded [&_code]:border [&_code]:border-line [&_code]:bg-ctl
                      [&_code]:px-1 [&_code]:py-px [&_code]:font-mono [&_code]:text-[11px] [&_code]:text-fg
                      [&_strong]:font-medium [&_strong]:text-fg">
        <p className="mb-1 font-medium text-fg">怎么用</p>
        <ul className="mb-3 ml-3.5 list-disc space-y-0.5">
          <li>库里的键<strong>点一下改它</strong>，<strong>按住拖到上面</strong>就上条。
            条上的 ✕ 只是拿下来，定义还在库里 —— 同一个键两行各放一个也行。</li>
          <li><strong>图标</strong>：挑一个内置的，条上就画它不画字（<code>⌨</code> 这种字形
            在很多字体里缺、难看、基线还对不齐）。<strong>名字还留着</strong> —— 编辑器里认它，
            指上去也显示它。选「文字」就是不用图标。</li>
          <li>改一处定义，条上（和顶栏上）所有引用一起变。</li>
          <li>两行<strong>各自横滑</strong>，放不下就滑，不换行。</li>
          <li><strong>宽</strong> = 占几格（1 / 2 / 3），一格就是一个键位那么宽。</li>
          <li><strong>两下</strong> = 点两次才真发出去（第一下键变红，3 秒不点自己放下）。
            关 pane / 关标签这种勾上。</li>
          <li><strong>弹出组</strong> = 条上<strong>只占一格</strong>，点开在它上面浮出一小片键
            （<strong>不占条上的地方，条也不重排</strong>）。点<strong>「方向键」</strong>一下就摆好；
            要别的组合点「弹出组」自己往格子里拖。</li>
          <li><strong>钉住</strong>：每行下面那条「钉住 左 n 右 n」说的是<strong>头几个 / 尾几个
            不跟着横滑</strong>。「呼键盘」「Esc」这种每隔十秒要按一次的键，滑走了就等于没有 ——
            钉住它，只有中间那段跟着滑。框里那条竖线就是界线。</li>
          <li>弹出组的格子里往空格 <code>·</code> 上拖 = 放进那一格，两格互拖 = 对调。</li>
        </ul>

        <p className="mb-1 font-medium text-fg">按键谱（「按键」那一栏）</p>
        <p className="mb-1.5">
          空格分隔可以连发多下：<code>ctrl+b c</code> 就是 herdr 的前缀加 c，一下点出来。
        </p>
        <dl className="mb-3 grid grid-cols-[auto_1fr] gap-x-2.5 gap-y-1">
          <dt className="text-faint">组合</dt>
          <dd><code>ctrl+x</code> <code>alt+x</code> <code>shift+tab</code></dd>
          <dt className="text-faint">具名</dt>
          <dd><code>esc tab enter space bs del ins up down left right home end pgup pgdn f1-f12</code></dd>
          <dt className="text-faint">文本</dt>
          <dd>
            <code>"herdr" enter</code>、<code>text:/new enter</code>
            （带空格要引号：<code>text:"git status"</code>）
          </dd>
          <dt className="text-faint">粘滞</dt>
          <dd><code>sticky:ctrl</code> <code>sticky:alt</code></dd>
          <dt className="text-faint">网页动作</dt>
          <dd>
            <code>act:kbd</code> <code>act:img</code> <code>act:panes</code> <code>act:files</code>{' '}
            <code>act:clip</code> <code>act:paste</code>（网页端自己处理，不发字节）
          </dd>
        </dl>

        <p>
          <strong>几行 / 条上放哪些 / 钉住几个</strong>是「{profile.name}」这一套自己的，换一套整份换掉；
          <strong>「我的按键」的定义所有套共用</strong>（包括弹出组）—— 在这儿删掉一个，别的套条上
          那些引用也一起消失。切成一行时第二行接到第一行后面。「恢复默认」只管这一套的条。
        </p>
      </div>

      {/* 跟着手指走的残影。fixed + pointer-events-none：它自己不能挡住命中判定 */}
      {drag && (
        <span
          className="pointer-events-none fixed z-50 -translate-x-1/2 -translate-y-1/2 rounded-md border border-brand-line bg-brand-bg px-2.5 py-1.5 font-mono text-xs text-brand-fg shadow-[0_10px_24px_-8px_rgba(0,0,0,.7)]"
          style={{ left: drag.x, top: drag.y }}
        >
          {(() => { const k = at(drag.from.zone, drag.from.i); return k ? keyFace(k.icon, k.label || kindOf(k)) : '' })()}
        </span>
      )}
    </>
  )

  return embedded ? body : <Panel title="软键条" onClose={onClose ?? (() => {})}>{body}</Panel>
}
