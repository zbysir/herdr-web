import { AArrowDown, AArrowUp, CircleHalf } from '@/icons'
import type { SoftKey, State } from '@/lib/api'
import { Panel } from './ui/panel'
import { Button } from './ui/button'
import { Checkbox } from './ui/checkbox'
import { SoftkeysPanel } from './SoftkeysPanel'
import { DevicesPanel } from './DevicesPanel'
import { cn } from '@/lib/utils'

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
  onSaved: (lib: SoftKey[], bar: string[][]) => void
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
          下划线式，不是三个填充按钮 —— 三个色块并排时「当前是哪一页」只能靠颜色深浅
          去猜，而下划线是位置信息，扫一眼就知道自己在第几页。手指要点得中的那点高度
          靠 py-2.5 撑（约 38px），不靠按钮外壳。 */}
      {/* `-top-2 -mt-2 pt-2` 这三个是**一套**，不是凑好看的：外面那层滚动容器有 pt-2
          （见 ui/panel），而 sticky 的定位是相对**内容盒**顶边算的 —— 只写 top-0 的话
          分页条最高只能停在内容盒顶上，上面那 8px 的内边距就是个口子，下面的内容会从
          分页条上方那条缝里滚过去（截图实拍）。所以：-mt-2 把它抻到滚动区真正的顶边、
          -top-2 让 sticky 允许它停在那儿（只给 -mt-2 会被 sticky 又推回去，实测），
          再用自己的 pt-2 把分页条的视觉位置还原。改了 panel 的 pt 就得同步改这儿。 */}
      <nav className="sticky -top-2 z-1 -mx-4 -mt-2 mb-3 flex gap-5 border-b border-line bg-bar px-4 pt-2">
        {TABS.map((t) => (
          <button
            key={t.id}
            type="button"
            data-testid={`settings-tab-${t.id}`}
            className={cn(
              'relative -mb-px cursor-pointer border-b-2 px-0.5 py-2.5 text-[13px] outline-none',
              'transition-colors focus-visible:ring-2 focus-visible:ring-brand/35',
              tab === t.id ? 'border-brand text-fg' : 'border-transparent text-muted hover:text-fg',
            )}
            onClick={() => onTab(t.id)}
          >
            {t.label}
          </button>
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
  // 每条一行、勾在最左边。gap 给 2.5：勾和字贴太近时一排四条看着像一坨
  const row = (k: keyof TermOpts, label: string) => (
    <label className="flex cursor-pointer items-start gap-2.5 rounded-md py-1 transition-colors hover:text-fg">
      <span className="pt-px"><Checkbox checked={opts[k]} onCheckedChange={(v) => setOpt(k, !!v)} /></span>
      <span className="text-[13px]/relaxed">{label}</span>
    </label>
  )
  return (
    <div className="flex flex-col gap-0.5">
      {/* 字号和明暗：手机竖屏（< 440px）的顶栏放不下这三个图标，那一档只能从这儿调。
          宽屏顶栏里那三个还在，这里是同一套动作，不是另一份状态 */}
      <div className="mb-2 flex flex-wrap items-center gap-2 border-b border-line pb-3">
        <span className="text-xs text-muted tabular-nums">字号 {fontSize}px</span>
        {/* 加减贴成一个控件：它们操作的是同一个量，分开两个方块看着像两件事 */}
        <div className="flex overflow-hidden rounded-md border border-line">
          <Button size="icon" className="rounded-none border-0 border-r border-line" title="缩小字号" onClick={() => onFont(-1)}>
            <AArrowDown className="size-4" />
          </Button>
          <Button size="icon" className="rounded-none border-0" title="放大字号" onClick={() => onFont(1)}>
            <AArrowUp className="size-4" />
          </Button>
        </div>
        <span className="ml-2 text-xs text-muted">{scheme === 'dark' ? '暗色' : '亮色'}</span>
        <Button size="icon" title="切换明暗" onClick={onScheme}><CircleHalf className="size-4" /></Button>
      </div>

      {row('kitty', 'kitty 键盘协议（Ctrl+Shift+x / Ctrl+数字 / Ctrl+Enter）')}
      {row('meta', 'Option 当作 Meta（alt+1、alt+g 这类快捷键）')}
      {row('copyOnSelect', '选中即复制')}
      {row('sync2026', '同步输出 DEC 2026（防画面撕裂；留一块空白画不上来时关它）')}

      {heals > 0 && (
        <p className="text-xs/relaxed text-muted">
          同步输出补过 {heals} 次收尾：herdr 的 2026 帧没等到 ESU，重绘被攒住了
          （缓冲区没坏，只是没画上）。频繁出现就把上面的同步输出关掉。
        </p>
      )}

      <p className="mt-3 border-t border-line pt-3 text-xs/relaxed text-muted">
        复制：⌘C / Ctrl+Shift+C　粘贴：⌘V　清屏：⌘K　浏览器自己吃掉的键：⌘W ⌘T ⌘N Ctrl+Tab
      </p>

      {state && (
        <dl className="m-0 mt-2 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs">
          <dt className="text-faint">后端</dt>
          <dd className="m-0 truncate text-muted">{state.user}@{state.hostname} · {state.shell}</dd>
          <dt className="text-faint">socket</dt>
          <dd className="m-0 truncate font-mono text-muted">{state.herdrSocket}</dd>
          {state.version && (
            <>
              <dt className="text-faint">版本</dt>
              <dd className="m-0 truncate text-muted">{state.version.current}</dd>
            </>
          )}
        </dl>
      )}

      {/* 有新版本就显式提一句，并把命令给全。这里看到提示的人手边就有一个终端，
          能就地敲 —— 所以这条提示是可执行的，不是「回机器前再说」。
          升级要重启服务，而重启会掐掉这个页面自己的终端会话，这点必须写出来。
          用「淡绿底 + 绿边 + 绿字」（brand/12 + brand/40）而不是 brand-bg 那套实心填充：
          实心是留给一屏一个的主操作的，这只是条提示。 */}
      {state?.version?.outdated && (
        <p className="mt-2 rounded-md border border-brand/40 bg-brand/12 px-2.5 py-2 text-xs/relaxed text-muted
                      [&_code]:rounded [&_code]:border [&_code]:border-brand/40 [&_code]:px-1 [&_code]:py-px
                      [&_code]:font-mono [&_code]:text-[11px] [&_code]:text-brand">
          有新版本 <code>{state.version.latest}</code>。在终端里敲 <code>{state.version.how}</code> 就能升。
          <br />
          升完<strong className="font-medium text-fg">要重启服务才生效</strong>
          （<code>herdr-web service restart</code>），而重启会断开所有终端会话 —— 包括这个页面里的。
        </p>
      )}
    </div>
  )
}
