import { useCallback, useEffect, useState } from 'react'
import { ArrowDown, ArrowUp, Plus, X } from 'lucide-react'
import { api, type PresetGroup, type SoftKey, type SoftkeysResponse } from '@/lib/api'
import { Button } from './ui/button'
import { Input } from './ui/input'
import { Select } from './ui/select'
import { Checkbox } from './ui/checkbox'
import { Panel } from './ui/panel'

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

  const move = (i: number, d: number) => {
    const j = i + d
    if (j < 0 || j >= draft.length) return
    const next = [...draft]
    ;[next[i], next[j]] = [next[j], next[i]]
    setDraft(next)
  }

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
      <div className="flex flex-col gap-1">
        {draft.length === 0 && <p className="px-0.5 py-1.5 text-xs text-muted">一个按键都没有，点「加一个」</p>}
        {draft.map((k, i) => (
          <div key={i} className="flex items-center gap-1.5 rounded-[7px] bg-fg/4 p-1.5">
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
            <Select
              className="max-w-[8.5em] shrink-0"
              value=""
              title="从常用里挑一个填进这一行"
              onChange={(e) => {
                const it = flat[Number(e.target.value)]
                if (it) patch(i, () => ({ ...it }))
              }}
            >
              <option value="">常用…</option>
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
            <label className="flex shrink-0 items-center gap-1 text-[11px] text-muted" title="占宽一点">
              <Checkbox checked={!!k.wide} onCheckedChange={(v) => patch(i, (x) => ({ ...x, wide: !!v }))} />
              宽
            </label>
            <div className="flex shrink-0 gap-1">
              <Button size="tiny" title="往左" onClick={() => move(i, -1)}><ArrowUp className="size-3" /></Button>
              <Button size="tiny" title="往右" onClick={() => move(i, 1)}><ArrowDown className="size-3" /></Button>
              <Button size="tiny" variant="danger" title="删掉" onClick={() => setDraft((d) => d.filter((_, j) => j !== i))}>
                <X className="size-3" />
              </Button>
            </div>
          </div>
        ))}
      </div>

      <div className="mt-2.5 flex items-center gap-1.5">
        <Button size="tiny" onClick={() => setDraft((d) => [...d, { label: '', send: '' }])}>
          <Plus className="size-3" />加一个
        </Button>
        <Button size="tiny" variant="primary" onClick={save}>保存</Button>
        <Button size="tiny" variant="danger" onClick={reset}>恢复默认</Button>
        {err && <span className="text-[11.5px] text-bad">{err}</span>}
      </div>

      <p className="mt-1.5 text-[11.5px]/relaxed text-muted">
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
