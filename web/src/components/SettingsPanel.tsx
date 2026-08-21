import { AArrowDown, AArrowUp, CircleHalf } from '@/icons'
import type { SoftKey, State } from '@/lib/api'
import { Panel } from './ui/panel'
import { Button } from './ui/button'
import { Checkbox } from './ui/checkbox'
import { SoftkeysPanel } from './SoftkeysPanel'
import { DevicesPanel } from './DevicesPanel'

/**
 * 一个设置面板，装完所有设置。
 *
 * 之前是三个各自为政的小面板（终端能力 / 软键条 / 设备），顶栏为此挂了三个图标 ——
 * 平板上顶栏本来就挤，而且「设置」这件事被切成三块，找起来全靠记哪个图标是哪个。
 * 现在**只有顶栏这一个 ⚙**，里面分页。软键条右下角原来还有一个直通「软键条」页的 ⚙，
 * 去掉了：它跟键抢地方（尤其竖屏），而设置这种一次调完的事不值得占一个常驻位置。
 *
 * 原来那个「程序请求的终端能力」列表（DEC 1049 / OSC 10 那一串）去掉了：那是当初补
 * 协议时的调试视图，日常没人看。能力本身还照样在 session 里记着（主题变更通知要用），
 * 只是不再摆到界面上。
 */
export type TermOpts = { kitty: boolean; meta: boolean; copyOnSelect: boolean; sync2026: boolean }

export type SettingsTab = 'term' | 'keys' | 'devices'

const TABS: { id: SettingsTab; label: string }[] = [
  { id: 'term', label: '终端' },
  { id: 'keys', label: '软键条' },
  { id: 'devices', label: '设备' },
]

export function SettingsPanel({
  tab, onTab, onClose, opts, setOpt, heals, onSaved, toast, state,
  fontSize, onFont, scheme, onScheme,
}: {
  tab: SettingsTab
  onTab: (t: SettingsTab) => void
  onClose: () => void
  opts: TermOpts
  setOpt: (k: keyof TermOpts, v: boolean) => void
  heals: number
  onSaved: (keys: SoftKey[], rows: 1 | 2) => void
  toast: (m: string) => void
  /** 启动时那次 /api/state 的结果，「终端」页底下当环境信息显示；没拿到就不显示 */
  state?: State | null
  fontSize: number
  /** 字号加减（传 +1 / -1）。手机竖屏顶栏放不下这两个图标，入口在这儿 */
  onFont: (d: number) => void
  scheme: 'dark' | 'light'
  onScheme: () => void
}) {
  return (
    <Panel title="设置" onClose={onClose} className="w-[560px]">
      {/* 分页条粘在顶上：软键条那页很长，滚下去还得能换页。
          按键用默认尺寸而不是 tiny —— 手指要点得中 */}
      <nav className="sticky top-0 z-1 -mx-3 mb-2 flex gap-1.5 border-b border-line bg-bar px-3 pt-0.5 pb-2">
        {TABS.map((t) => (
          <Button
            key={t.id}
            data-testid={`settings-tab-${t.id}`}
            on={tab === t.id}
            onClick={() => onTab(t.id)}
          >
            {t.label}
          </Button>
        ))}
      </nav>

      {tab === 'term' && (
        <TermSection
          opts={opts} setOpt={setOpt} heals={heals} state={state}
          fontSize={fontSize} onFont={onFont} scheme={scheme} onScheme={onScheme}
        />
      )}
      {tab === 'keys' && <SoftkeysPanel embedded onSaved={onSaved} toast={toast} />}
      {tab === 'devices' && <DevicesPanel embedded toast={toast} />}
    </Panel>
  )
}

function TermSection({
  opts, setOpt, heals, state, fontSize, onFont, scheme, onScheme,
}: {
  opts: TermOpts
  setOpt: (k: keyof TermOpts, v: boolean) => void
  heals: number
  state?: State | null
  fontSize: number
  onFont: (d: number) => void
  scheme: 'dark' | 'light'
  onScheme: () => void
}) {
  const row = (k: keyof TermOpts, label: string) => (
    <label className="flex cursor-pointer items-start gap-1.5">
      <Checkbox checked={opts[k]} onCheckedChange={(v) => setOpt(k, !!v)} />
      <span>{label}</span>
    </label>
  )
  return (
    <div className="flex flex-col gap-1.5">
      {/* 字号和明暗：手机竖屏（< 440px）的顶栏放不下这三个图标，那一档只能从这儿调。
          宽屏顶栏里那三个还在，这里是同一套动作，不是另一份状态 */}
      <div className="mb-1 flex flex-wrap items-center gap-1.5 border-b border-line pb-2.5">
        <span className="text-muted">字号 {fontSize}px</span>
        <Button size="icon" title="缩小字号" onClick={() => onFont(-1)}><AArrowDown className="size-4" /></Button>
        <Button size="icon" title="放大字号" onClick={() => onFont(1)}><AArrowUp className="size-4" /></Button>
        <Button size="icon" className="ml-1.5" title="切换明暗" onClick={onScheme}><CircleHalf className="size-4" /></Button>
        <span className="text-muted">{scheme === 'dark' ? '暗' : '亮'}</span>
      </div>

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

      <p className="mt-1 border-t border-line pt-2.5 text-[11.5px]/relaxed text-muted">
        复制：⌘C / Ctrl+Shift+C　粘贴：⌘V　清屏：⌘K　浏览器自己吃掉的键：⌘W ⌘T ⌘N Ctrl+Tab
      </p>

      {state && (
        <dl className="m-0 grid grid-cols-[auto_1fr] gap-x-2.5 gap-y-1 text-[11.5px] text-muted">
          <dt>后端</dt>
          <dd className="m-0 truncate">{state.user}@{state.hostname} · {state.shell}</dd>
          <dt>socket</dt>
          <dd className="m-0 truncate">{state.herdrSocket}</dd>
        </dl>
      )}
    </div>
  )
}
