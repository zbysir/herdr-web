import { useCallback, useEffect, useRef, useState } from 'react'
import { GripVertical, Plus, X } from 'lucide-react'
import { api, type PresetGroup, type SoftKey, type SoftkeysResponse } from '@/lib/api'
import { Button } from './ui/button'
import { Input } from './ui/input'
import { Select } from './ui/select'
import { Checkbox } from './ui/checkbox'
import { Panel } from './ui/panel'
import { cn } from '@/lib/utils'

/** 一行的「按键」栏怎么显示：sticky/act 用 `sticky:ctrl` 这种写法，其余就是按键谱。 */
const kindOf = (k: SoftKey) => (k.sticky ? `sticky:${k.sticky}` : k.act ? `act:${k.act}` : (k.spec ?? k.send ?? ''))

/** 把「按键」栏的文本解回一条 SoftKey。 */
function parseKind(spec: string, label: string, wide: boolean): SoftKey {
  const m = spec.match(/^(sticky|act):(.+)$/)
  if (m) {
    return m[1] === 'sticky'
      ? { label, wide, sticky: m[2].trim() as 'ctrl' | 'alt' }
      : { label, wide, act: m[2].trim() as 'kbd' }
  }
  return { label, wide, send: spec }
}

export function SoftkeysPanel({
  onClose, onSaved, toast,
}: { onClose: () => void; onSaved: (keys: SoftKey[]) => void; toast: (m: string) => void }) {
  const [draft, setDraft] = useState<SoftKey[]>([])
  const [presets, setPresets] = useState<PresetGroup[]>([])
  const [err, setErr] = useState('')

  useEffect(() => {
    void (async () => {
      try {
        const r = await api.get<SoftkeysResponse>('/softkeys')
        setDraft(r.keys.map((k) => ({ ...k, send: k.spec ?? k.send })))
        setPresets(r.presets)
      } catch (e) {
        setErr((e as Error).message)
      }
    })()
  }, [])

  // 拍平之后按下标取。别用「组名 + 分隔符 + 序号」拼字符串当 option value ——
  // 分隔符本身就是个坑（踩过：本该是空格的那个字节写成了 NUL）。
  const flat = presets.flatMap((g) => g.items)

  const patch = useCallback((i: number, f: (k: SoftKey) => SoftKey) => {
    setDraft((d) => d.map((k, j) => (j === i ? f(k) : k)))
  }, [])

  const reorder = useCallback((from: number, to: number) => {
    setDraft((d) => {
      if (to < 0 || to >= d.length || from === to) return d
      const next = [...d]
      next.splice(to, 0, ...next.splice(from, 1))
      return next
    })
  }, [])

  /* ---------------------------------------------------------------- 拖动排序 */
  // 用 Pointer Events 手写，不用 HTML5 drag-and-drop —— 后者在触屏上根本不触发，
  // 而平板是这个项目的主设备。
  //
  // 只有那个拖柄能起拖（`touch-action: none`），面板本身的纵向滚动因此不受影响：
  // 摸拖柄 = 拖行，摸别处 = 滚列表，没有歧义，也不用猜阈值。
  const rowsRef = useRef<HTMLDivElement>(null)
  const dragFrom = useRef<number | null>(null)
  const [dragging, setDragging] = useState<number | null>(null)

  const onGripDown = (e: React.PointerEvent, i: number) => {
    e.preventDefault()
    ;(e.currentTarget as HTMLElement).setPointerCapture(e.pointerId)
    dragFrom.current = i
    setDragging(i)
  }

  const onGripMove = (e: React.PointerEvent) => {
    if (dragFrom.current === null) return
    const rows = [...(rowsRef.current?.children ?? [])] as HTMLElement[]
    if (!rows.length) return
    const y = e.clientY

    // 拖到哪一行：谁的矩形包住指针就是它；出了两头就吸到首 / 尾
    let target = dragFrom.current
    const first = rows[0].getBoundingClientRect()
    const last = rows[rows.length - 1].getBoundingClientRect()
    if (y < first.top) target = 0
    else if (y > last.bottom) target = rows.length - 1
    else {
      for (let k = 0; k < rows.length; k++) {
        const r = rows[k].getBoundingClientRect()
        if (y >= r.top && y <= r.bottom) { target = k; break }
      }
    }

    // 边缘自动滚：14 行在手机上一屏放不下，不然拖不到列表另一头。
    // Panel 的滚动容器就是最近的 .overflow-auto 祖先。
    const sc = rowsRef.current?.closest('.overflow-auto') as HTMLElement | null
    if (sc) {
      const r = sc.getBoundingClientRect()
      const edge = 44
      if (y < r.top + edge) sc.scrollTop -= 12
      else if (y > r.bottom - edge) sc.scrollTop += 12
    }

    if (target !== dragFrom.current) {
      reorder(dragFrom.current, target)   // 边拖边就位，不用做浮动残影
      dragFrom.current = target
      setDragging(target)
    }
  }

  const onGripUp = () => { dragFrom.current = null; setDragging(null) }

  const save = async () => {
    setErr('')
    try {
      const r = await api.put<{ keys: SoftKey[] }>('/softkeys', { keys: draft })
      onSaved(r.keys)
      toast('软键条已保存')
    } catch (e) {
      setErr((e as Error).message)   // 服务端会指出是第几个按键、哪里不认
    }
  }

  const reset = async () => {
    setErr('')
    try {
      const r = await api.del<{ keys: SoftKey[] }>('/softkeys')
      onSaved(r.keys)
      setDraft(r.keys.map((k) => ({ ...k, send: k.spec ?? k.send })))
      toast('已恢复默认')
    } catch (e) {
      setErr((e as Error).message)
    }
  }

  return (
    <Panel title="软键条" onClose={onClose}>
      <div ref={rowsRef} className="flex flex-col gap-1">
        {draft.length === 0 && <p className="px-0.5 py-1.5 text-xs text-muted">一个按键都没有，从下面「常用」里挑，或者点「加一个」手输</p>}
        {draft.map((k, i) => (
          <div
            key={i}
            className={cn(
              'flex items-center gap-1.5 rounded-[7px] bg-fg/4 p-1.5',
              dragging === i && 'bg-accent/20 outline outline-accent',
            )}
          >
            <button
              type="button"
              aria-label={`拖动排序：${k.label || '第 ' + (i + 1) + ' 个'}`}
              title="按住拖动排序（也可以聚焦后按 ↑ ↓）"
              className={cn(
                'shrink-0 touch-none cursor-grab rounded px-0.5 py-1 text-muted',
                'hover:text-fg focus:outline focus:outline-accent active:cursor-grabbing',
              )}
              onPointerDown={(e) => onGripDown(e, i)}
              onPointerMove={onGripMove}
              onPointerUp={onGripUp}
              onPointerCancel={onGripUp}
              // 拖柄也支持键盘：没有指针的时候照样能挪
              onKeyDown={(e) => {
                if (e.key === 'ArrowUp') { e.preventDefault(); reorder(i, i - 1) }
                if (e.key === 'ArrowDown') { e.preventDefault(); reorder(i, i + 1) }
              }}
            >
              <GripVertical className="size-4" />
            </button>
            <Input
              className="w-[5.5em] shrink-0"
              value={k.label}
              maxLength={12}
              placeholder="名字"
              onChange={(e) => patch(i, (x) => ({ ...x, label: e.target.value }))}
            />
            <Input
              className="min-w-0 flex-1"
              value={kindOf(k)}
              placeholder="ctrl+b c"
              onChange={(e) => setDraft((d) => d.map((x, j) => (j === i ? parseKind(e.target.value, x.label, !!x.wide) : x)))}
            />
            <label className="flex shrink-0 items-center gap-1 text-[11px] text-muted" title="占宽一点">
              <Checkbox checked={!!k.wide} onCheckedChange={(v) => patch(i, (x) => ({ ...x, wide: !!v }))} />
              宽
            </label>
            <Button size="tiny" variant="danger" className="shrink-0" title="删掉"
              onClick={() => setDraft((d) => d.filter((_, j) => j !== i))}>
              <X className="size-3" />
            </Button>
          </div>
        ))}
      </div>

      <div className="mt-2.5 flex flex-wrap items-center gap-1.5">
        {/* 预设只在这里出现一次：它的用途是「填一个新键」，不是每行都要挑一遍。
            已有的行直接改「按键」那一栏就行。 */}
        <Select
          data-testid="preset-add"
          className="max-w-[14em]"
          value=""
          title="从常用里挑一个，追加到最后"
          onChange={(e) => {
            const it = flat[Number(e.target.value)]
            if (it) setDraft((d) => [...d, { ...it }])
            e.target.value = ''
          }}
        >
          <option value="">+ 从常用添加…</option>
          {(() => {
            let n = -1
            return presets.map((g) => (
              <optgroup key={g.group} label={g.group}>
                {g.items.map((it) => {
                  n += 1
                  return (
                    <option key={n} value={n}>
                      {it.label}{it.send ? ` — ${it.send}` : ''}
                    </option>
                  )
                })}
              </optgroup>
            ))
          })()}
        </Select>
        <Button size="tiny" onClick={() => setDraft((d) => [...d, { label: '', send: '' }])}>
          <Plus className="size-3" />加一个
        </Button>
        <Button size="tiny" variant="primary" onClick={save}>保存</Button>
        <Button size="tiny" variant="danger" onClick={reset}>恢复默认</Button>
        {err && <span className="w-full text-[11.5px] text-bad">{err}</span>}
      </div>

      <p className="mt-1.5 text-[11.5px]/relaxed text-muted">
        左边的握把可以<strong>按住拖动排序</strong>（触屏一样好使；聚焦它按 ↑ ↓ 也行）。<br />
        「按键」一栏写按键谱，空格分隔可以连发多下 —— <code>ctrl+b c</code> 就是 herdr 的前缀加 c，一下点出来。<br />
        支持：<code>ctrl+x</code> <code>alt+x</code> <code>shift+tab</code>、具名键{' '}
        <code>esc tab enter space bs del ins up down left right home end pgup pgdn f1-f12</code>、
        双引号里的原样文本（<code>"herdr" enter</code> = 敲 herdr 再回车）。<br />
        <code>Ctrl</code> / <code>Alt</code> 这种粘滞修饰键写 <code>sticky:ctrl</code> / <code>sticky:alt</code>，
        呼出键盘写 <code>act:kbd</code>。
      </p>
    </Panel>
  )
}
