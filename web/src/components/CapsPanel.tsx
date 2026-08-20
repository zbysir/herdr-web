import { Panel } from './ui/panel'
import { Checkbox } from './ui/checkbox'
import type { Cap } from '@/term/session'

export function CapsPanel({
  onClose, caps, opts, setOpt, heals,
}: {
  onClose: () => void
  caps: Cap[]
  opts: { kitty: boolean; meta: boolean; copyOnSelect: boolean; sync2026: boolean }
  setOpt: (k: keyof CapsPanel_Opts, v: boolean) => void
  heals: number
}) {
  const row = (k: keyof CapsPanel_Opts, label: string) => (
    <label className="flex cursor-pointer items-start gap-1.5">
      <Checkbox checked={opts[k]} onCheckedChange={(v) => setOpt(k, !!v)} />
      <span>{label}</span>
    </label>
  )
  return (
    <Panel title="程序请求的终端能力" onClose={onClose} className="w-[340px]">
      <ul className="max-h-[40vh] list-none overflow-auto p-0">
        {caps.length === 0 && <li className="text-muted">连接后这里会列出程序实际用到的转义序列</li>}
        {caps.map((c) => (
          <li key={c.key} className="flex items-baseline gap-2 py-[3px]">
            <code className="w-[84px] shrink-0 bg-transparent p-0 text-[11px] text-accent">{c.key}</code>
            <span className={c.ok ? '' : 'text-muted line-through'}>{c.label}</span>
          </li>
        ))}
      </ul>
      <div className="mt-2 flex flex-col gap-1.5 border-t border-line pt-2.5">
        {row('kitty', 'kitty 键盘协议（Ctrl+Shift+x / Ctrl+数字 / Ctrl+Enter）')}
        {row('meta', 'Option 当作 Meta（alt+1、alt+g 这类快捷键）')}
        {row('copyOnSelect', '选中即复制')}
        {row('sync2026', '同步输出 DEC 2026（防画面撕裂；留一块空白画不上来时关它）')}
        {heals > 0 && (
          <p className="text-[11.5px]/relaxed text-muted">
            同步输出补过 {heals} 次收尾：herdr 的 2026 帧没等到 ESU，重绘被攒住了
            （缓冲区没坏，只是没画上）。频繁出现就把上面的同步输出关掉。
          </p>
        )}
        <p className="text-[11.5px]/relaxed text-muted">
          复制：⌘C / Ctrl+Shift+C　粘贴：⌘V　清屏：⌘K　浏览器自己吃掉的键：⌘W ⌘T ⌘N Ctrl+Tab
        </p>
      </div>
    </Panel>
  )
}

export type CapsPanel_Opts = { kitty: boolean; meta: boolean; copyOnSelect: boolean; sync2026: boolean }
