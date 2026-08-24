import { useEffect, useState } from 'react'
import { X } from 'lucide-react'
import { AArrowDown, AArrowUp, CircleHalf } from '@/icons'
import type { ProfilesResponse, SoftkeysConfig, State } from '@/lib/api'
import { enableNotify, notifyState, testNotify, type NotifyState } from '@/lib/notify'
import { Panel } from './ui/panel'
import { Button } from './ui/button'
import { Checkbox } from './ui/checkbox'
import { Select } from './ui/select'
import { SoftkeysPanel } from './SoftkeysPanel'
import { TopbarPanel } from './TopbarPanel'
import { ProfilePicker } from './ProfilePicker'
import { DevicesPanel } from './DevicesPanel'
import { lanAuto, onLanNow, setLanAuto } from '@/hooks/useLanDirect'
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
 *
 * 分页条**上面**还有一行「这台设备用哪一套排布」（profile）：顶栏和软键条两页改的是那一套
 * 里的东西，所以它不能做成第五个分页 —— 得一直看得见。见 ProfilePicker。
 */
/** 每种权限状态说一句人话 —— 「开不了」的原因差别很大，笼统一句「不支持」查不出所以然 */
const NOTIFY_HINT: Record<NotifyState, string> = {
  granted: '已开。默认只在你没看这一页时弹（切到别的 app、锁屏、切标签页都算）。页面被彻底关掉就收不到 —— 那要 Web Push，还没做',
  default: '点一下会问你要权限',
  denied: '被拒过。手机上到「设置 → 通知」或浏览器的网站设置里把这个站的通知改成允许，再回来点',
  insecure: '这个地址不是安全上下文（要 https，或者 localhost），浏览器不给通知权限',
  unsupported: 'iPhone / iPad：Safari 标签页里拿不到通知权限，要先「添加到主屏幕」再从主屏打开（iOS 16.4+）。桌面上换 Chrome / Firefox / Safari',
}

export type TermOpts = { kitty: boolean; meta: boolean; copyOnSelect: boolean; sync2026: boolean; switchPanel: boolean }

export type SettingsTab = 'term' | 'topbar' | 'keys' | 'devices'

const TABS: { id: SettingsTab; label: string }[] = [
  { id: 'term', label: '终端' },
  { id: 'topbar', label: '顶栏' },
  { id: 'keys', label: '软键条' },
  { id: 'devices', label: '设备' },
]

export function SettingsPanel({
  tab, onTab, onClose, opts, setOpt, dot, onDot, os, onOS, osFg, onOSFg, cardMs, onCardMs,
  kbdFull, onKbdFull, heals, onSaved, onTopbar, toast, state,
  fontSize, onFont, scheme, onScheme, profile, onProfiles,
}: {
  tab: SettingsTab
  onTab: (t: SettingsTab) => void
  onClose: () => void
  opts: TermOpts
  setOpt: (k: keyof TermOpts, v: boolean) => void
  /** 面板图标上那个未读数角标画不画（有人不喜欢）。跟着这套排布走，见 lib/prefs.ts */
  dot: boolean
  onDot: (v: boolean) => void
  /** 系统通知开没开（浏览器权限另算，面板里当场问） */
  os: boolean
  onOS: (v: boolean) => void
  /** 系统通知：人正看着这一页时也弹 */
  osFg: boolean
  onOSFg: (v: boolean) => void
  /** 「跑完了」的卡片挂多久（ms，0 = 一直挂着） */
  cardMs: number
  onCardMs: (v: number) => void
  /** 呼出键盘就自动全屏（收起键盘不退出） */
  kbdFull: boolean
  onKbdFull: (v: boolean) => void
  heals: number
  onSaved: (c: SoftkeysConfig) => void
  /** 顶栏存好了：把新的那一串 id 交回去，顶栏立刻跟着变（不用刷新页面） */
  /** 顶栏那一串：内置按钮的 id，也可能是 `key:<定义ID>`（「我的按键」上了顶栏） */
  onTopbar: (items: string[]) => void
  toast: (m: string) => void
  /** 启动时那次 /api/state 的结果，「终端」页底下当环境信息显示；没拿到就不显示 */
  state?: State | null
  fontSize: number
  /** 字号加减（传 +1 / -1）。手机竖屏顶栏放不下这两个图标，入口在这儿 */
  onFont: (d: number) => void
  scheme: 'dark' | 'light'
  onScheme: () => void
  /** 这台设备用哪一套排布（顶栏 / 软键条两页改的就是它），见 internal/profiles */
  profile: { id: string; name: string }
  /** 名册 / 绑定 / 那一套的开关变了 —— App 据此重拉排布、把开关刷一遍 */
  onProfiles: (r: ProfilesResponse) => void
}) {
  return (
    // 不给 title：那一行「设置」+ × 占 44px，而这块面板本来就最缺高度（软键条那页要拖）。
    // × 并进分页条右边 —— 分页条是粘在顶上的，滚到哪儿关闭都在手边，比原来更好点。
    <Panel onClose={onClose} className="w-[560px]">
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
      {/* 「这台设备用哪一套」摆在分页条**上面**：下面「顶栏」「软键条」两页改的就是这一套，
          而「我在改哪一套」是看那两页时必须一直看得见的（见 ProfilePicker 的注释） */}
      <ProfilePicker onChanged={onProfiles} toast={toast} />

      <nav className="sticky -top-2 z-1 -mx-4 -mt-2 mb-3 flex items-center gap-5 border-b border-line bg-bar px-4 pt-2">
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
        {/* 关闭：顶在分页条最右边（这块面板没有标题栏，见上面 Panel 那条注释）。
            -mb-px 把它和分页条那条下边框对齐，不然按钮会把这一行撑高一像素。 */}
        <Button
          variant="ghost"
          size="icon"
          className="-mb-px ml-auto shrink-0 self-center"
          onClick={onClose}
          aria-label="关闭"
        >
          <X className="size-4" />
        </Button>
      </nav>

      {tab === 'term' && (
        <TermSection
          opts={opts} setOpt={setOpt} dot={dot} onDot={onDot} os={os} onOS={onOS}
          kbdFull={kbdFull} onKbdFull={onKbdFull}
          osFg={osFg} onOSFg={onOSFg} cardMs={cardMs} onCardMs={onCardMs}
          heals={heals} state={state} toast={toast}
          fontSize={fontSize} onFont={onFont} scheme={scheme} onScheme={onScheme}
        />
      )}
      {tab === 'topbar' && <TopbarPanel onSaved={onTopbar} toast={toast} profile={profile} />}
      {tab === 'keys' && <SoftkeysPanel embedded onSaved={onSaved} toast={toast} profile={profile} />}
      {tab === 'devices' && <DevicesPanel embedded toast={toast} onProfiles={onProfiles} />}
    </Panel>
  )
}

function TermSection({
  opts, setOpt, dot, onDot, os, onOS, osFg, onOSFg, cardMs, onCardMs, kbdFull, onKbdFull,
  heals, state, fontSize, onFont, scheme, onScheme, toast,
}: {
  opts: TermOpts
  setOpt: (k: keyof TermOpts, v: boolean) => void
  dot: boolean
  onDot: (v: boolean) => void
  os: boolean
  onOS: (v: boolean) => void
  osFg: boolean
  onOSFg: (v: boolean) => void
  cardMs: number
  onCardMs: (v: number) => void
  kbdFull: boolean
  onKbdFull: (v: boolean) => void
  toast: (m: string) => void
  heals: number
  state?: State | null
  fontSize: number
  onFont: (d: number) => void
  scheme: 'dark' | 'light'
  onScheme: () => void
}) {
  // 上次自动全屏失败的原因（成功过就没有了）。面板一开读一次就够
  const kbdErr = localStorage.getItem('kbdFullErr')
  // 浏览器那侧的通知权限。面板一开就问一次真实值（用户可能在浏览器设置里撤掉过）
  const [perm, setPerm] = useState<NotifyState>(notifyState)
  useEffect(() => { setPerm(notifyState()) }, [])

  // 打开的那一下**必须**是用户手势 —— 浏览器只在手势里给权限弹窗（定时器里申请一律静默拒绝）
  const flipOS = async (v: boolean) => {
    if (!v) { onOS(false); return }
    const got = await enableNotify()
    setPerm(got)
    onOS(got === 'granted')
  }

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
      {/* 说清「鼠标」：触屏上单指手势被终端那层接管了，压根没有选区 —— 手机上开这个不起
          作用，而它看着正是手机上想要的那个功能（真实误会过）。手机上复制走 herdr 的
          COPY 模式，见 README 的「手机上怎么复制」。 */}
      {row('copyOnSelect', '选中即复制（鼠标选中时；触屏上没有选区）')}
      {row('sync2026', '同步输出 DEC 2026（防画面撕裂；留一块空白画不上来时关它）')}

      {/* 手机上打字那一下最缺高度：键盘吃掉半屏，地址栏和工具条又占一截。
          **收键盘不退出**是刻意的 —— 每打一次字闪进闪出一次全屏，比不全屏还难受。 */}
      <label className="flex cursor-pointer items-start gap-2.5 rounded-md py-1 transition-colors hover:text-fg">
        <span className="pt-px"><Checkbox checked={kbdFull} onCheckedChange={(v) => onKbdFull(!!v)} /></span>
        <span className="text-[13px]/relaxed">
          呼出键盘时自动全屏
          <span className="mt-0.5 block text-xs text-faint">
            收起键盘<b>不</b>退出（退出用顶栏那个按钮）。键盘吃掉半屏时，地址栏那一截最值钱
          </span>
          {/* 手机上没有控制台，「为什么没全屏」只能靠这一句。成功一次就自己消失 */}
          {kbdErr && <span className="mt-0.5 block text-xs text-bad">上次没成功：{kbdErr}</span>}
        </span>
      </label>

      {/* 「点 switch 开面板一览」和上面那串终端行为不是一类：它改的是「点 herdr 那个按钮会
          发生什么」。和下面那个角标一样是「这类设备上顺手不顺手」的偏好（跟着排布那一套走，
          见 lib/prefs.ts），所以并在同一条线下面。 */}
      <label className="mt-2 flex cursor-pointer items-start gap-2.5 rounded-md border-t border-line pt-3 transition-colors hover:text-fg">
        <span className="pt-px">
          <Checkbox checked={opts.switchPanel} onCheckedChange={(v) => setOpt('switchPanel', !!v)} />
        </span>
        <span className="text-[13px]/relaxed">
          点 herdr 的 switch 就开「面板一览」（手机 / 平板）
          <span className="mt-0.5 block text-xs text-faint">
            窄屏时 herdr 顶栏右上角有个 switch 按钮，点开的是它自己那张切换面板。开着这条时，
            触屏上点它就不再发给 herdr，改开我们的面板一览（一行一个 pane，点一下跳过去并铺满）。
            代价是 herdr 那张里的「+ new workspace / + new tab / settings / detach」这一路走不到 ——
            要用就把这条关掉，或者从软键条走 herdr 的前缀键。
          </span>
        </span>
      </label>

      {/* 提示那一条**不属于**「终端」，但设置面板只有三页（终端 / 软键条 / 设备），
          为一个开关单开一页不值当。用一条分隔线隔开，别混进上面那串终端行为里去。 */}
      <label className="mt-1 flex cursor-pointer items-start gap-2.5 rounded-md py-1 transition-colors hover:text-fg">
        <span className="pt-px"><Checkbox checked={dot} onCheckedChange={(v) => onDot(!!v)} /></span>
        <span className="text-[13px]/relaxed">
          面板图标上的角标（还有几条没看：等你回答 / 刚跑完）
          <span className="mt-0.5 block text-xs text-faint">
            点进去看过一条就少一个；关掉只是不画这个角标，右上角的提示卡照常出。整套提示要关是服务端那侧的
            <code className="mx-1 rounded border border-line bg-ctl px-1 py-px font-mono text-[11px]">HERDR_WEB_NOTICE_MS=0</code>
          </span>
        </span>
      </label>

      {/* 系统通知：本地开关 + 浏览器权限两件事。权限**当场问一次**再显示 ——
          用户可能在浏览器设置里把它撤了，只信 localStorage 会显示成开着但一条都不弹。 */}
      <label
        className={cn(
          'flex items-start gap-2.5 rounded-md pt-1 transition-colors',
          perm === 'unsupported' || perm === 'insecure' ? 'opacity-60' : 'cursor-pointer hover:text-fg',
        )}
      >
        <span className="pt-px">
          <Checkbox
            checked={os && perm === 'granted'}
            disabled={perm === 'unsupported' || perm === 'insecure'}
            onCheckedChange={(v) => { void flipOS(!!v) }}
          />
        </span>
        <span className="text-[13px]/relaxed">
          系统通知（切到别的 app / 锁屏也能知道）
          <span className="mt-0.5 block text-xs text-faint">{NOTIFY_HINT[perm]}</span>
        </span>
      </label>

      {/* 「试一下」不看开关也不看「在不在看这一页」，就是当场弹一条 —— 「到底卡在哪」
          光靠猜是猜不出来的（权限？系统的专注模式？iOS 没装到主屏？），弹一次就知道了 */}
      <div className="mt-1 flex flex-wrap items-center gap-2 pl-7">
        <Button
          size="tiny"
          onClick={() => {
            void (async () => {
              const p = notifyState()
              if (p !== 'granted') {
                const got = await enableNotify() // 这一下是用户手势，可以申请权限
                setPerm(got)
                if (got !== 'granted') { toast(NOTIFY_HINT[got]); return }
                onOS(true)
              }
              await testNotify()
              toast('已经发出去一条了。没看见的话看下系统的通知设置 / 专注模式')
            })()
          }}
        >
          试一下
        </Button>
        <label className="flex cursor-pointer items-center gap-2 text-xs text-muted hover:text-fg">
          <Checkbox checked={osFg} onCheckedChange={(v) => onOSFg(!!v)} />
          我正看着这一页时也弹
        </label>
      </div>

      {/* 卡片停留多久。「等你回答」那种不受这个管 —— 它是真的停在那儿等你，
          自己飘走就又回到「不知道谁在等」了 */}
      <div className="mt-3 flex flex-wrap items-center gap-2 border-t border-line pt-3">
        <span className="text-[13px]">提示卡停留</span>
        <Select
          value={String(cardMs)}
          onChange={(e) => onCardMs(Number(e.target.value))}
          aria-label="提示卡停留多久"
        >
          <option value="5000">5 秒</option>
          <option value="12000">12 秒</option>
          <option value="30000">30 秒</option>
          <option value="60000">1 分钟</option>
          <option value="0">一直挂着</option>
        </Select>
        <span className="text-xs text-faint">「等你回答」那种一直挂着，不受这个管</span>
      </div>

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
          {/* 哪个 herdr session（地址栏里 /{name} 那一段），下面那个 socket 就是它的 */}
          <dt className="text-faint">session</dt>
          <dd className="m-0 truncate text-muted">{state.session || '默认'}</dd>
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

      {state?.lan && state.lan.origins.length > 0 && <LanDirectRow origins={state.lan.origins} />}

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

/**
 * 「局域网直连」那一小节（只在服务端开了 `HERDR_WEB_LAN_PORT` 时出现）。
 *
 * 为什么这一节非有不可：那个地址原来**只印在终端的启动横幅上**，而这条路要求每台设备
 * 先手动开一次、点「继续访问」（自签证书）—— 平板上的人压根看不到横幅，等于这个地址
 * 是不可知的，而不知道地址就永远迈不出第一步。地址还会变（DHCP），所以也不能靠人抄。
 *
 * 顺带补掉另一个死角：横幅上点过「不用」之后，以前只有清 localStorage 才能反悔。
 *
 * 链接开新标签页：证书警告是**整页**的，在当前页跳过去就把这个终端会话的页面顶掉了。
 */
function LanDirectRow({ origins }: { origins: string[] }) {
  const here = onLanNow(origins)
  const [auto, setAuto] = useState(lanAuto)
  return (
    <div className="mt-3 border-t border-line pt-3">
      <div className="flex items-center gap-2">
        <strong className="text-[13px] font-medium">局域网直连</strong>
        <span
          className={cn(
            'rounded border px-1.5 py-px text-[11px]',
            here ? 'border-brand/40 bg-brand/12 text-brand' : 'border-line text-muted',
          )}
        >
          {here ? '正走直连' : '正走公网'}
        </span>
      </div>

      <p className="mt-1 text-xs/relaxed text-muted">
        {here
          ? '这个页面已经是直连的 —— 按键不再绕公网。'
          : '在这台设备上先开一次下面的地址、点「继续访问」（自签证书），之后从公网地址进来就会自动切过去。'}
      </p>

      <ul className="m-0 mt-1.5 list-none p-0">
        {origins.map((o) => (
          <li key={o} className="flex items-center gap-2 py-0.5">
            <a
              href={o + '/'}
              target="_blank"
              rel="noreferrer"
              className="truncate font-mono text-xs text-brand underline decoration-brand/40 hover:decoration-brand"
            >
              {o}
            </a>
          </li>
        ))}
      </ul>

      <label className="mt-1.5 flex cursor-pointer items-start gap-2.5 rounded-md py-1 transition-colors hover:text-fg">
        <span className="pt-px">
          <Checkbox
            checked={auto}
            onCheckedChange={(v) => { setLanAuto(!!v); setAuto(!!v) }}
          />
        </span>
        <span className="text-[13px]/relaxed">
          探到局域网就自动切过去
          <span className="block text-xs text-faint">
            关掉之后就一直走公网那条路，不再问也不再切。
          </span>
        </span>
      </label>
    </div>
  )
}
