import { useCallback, useEffect, useRef, useState } from 'react'
import { Keyboard, Pencil, CircleHalf, Gear, AArrowDown, AArrowUp, Maximize, Minimize } from './icons'
import { api, resolveBar, UNAUTHED, type SoftKey, type SoftkeysResponse, type State, type UnauthedDetail, type WhoAmI } from '@/lib/api'
import { Session } from '@/term/session'
import { initialScheme, type Scheme } from '@/term/themes'
import { useViewportHeight } from '@/hooks/useViewportHeight'
import { useCompose } from '@/hooks/useCompose'
import { usePhone } from '@/hooks/usePhone'
import { useKeyboardUp } from '@/hooks/useKeyboardUp'
import { Button } from '@/components/ui/button'
import { Toast } from '@/components/ui/toast'
import { Dock } from '@/components/Dock'
import { Softkeys } from '@/components/Softkeys'
import { Compose } from '@/components/Compose'
import { SettingsPanel, type SettingsTab, type TermOpts } from '@/components/SettingsPanel'
import { Pairing } from '@/components/Pairing'
import { cn } from '@/lib/utils'

/** Safari 的私有全屏 API。lib.dom 里没有这几个，本地补上，省得到处 as any */
type FsDoc = Document & {
  webkitFullscreenElement?: Element | null
  webkitExitFullscreen?: () => Promise<void> | void
}
type FsEl = HTMLElement & { webkitRequestFullscreen?: () => Promise<void> | void }

const lsBool = (k: string, def: boolean) => {
  const v = localStorage.getItem(k)
  return v === null ? def : v === '1'
}

// URL 上的 ?poll= / ?push= 可以临时覆盖服务端下发的默认值，方便在平板上试手感
const urlNum = (k: string) => {
  const v = Number(new URLSearchParams(location.search).get(k))
  return v > 0 ? Math.max(100, v) : null
}

// pairHint 把服务端 302 回来的失败原因翻成一句话。
// 服务端只在 URL 上留一个 e=xxx，不留任何细节 —— 「码不对」和「没有这个设备」
// 在响应上要长得一样。
function pairHint(): string | undefined {
  switch (new URLSearchParams(location.search).get('e')) {
    case 'code':
      return '那个配对码不对，或者已经过期了（5 分钟）。在机器上重新 herdr-web pair 一个。'
    case 'token':
      return '链接里的旧 token 不对。旧 token 只能用来换一次凭据，换完就该删掉了。'
    default:
      return undefined
  }
}

export default function App() {
  const host = useRef<HTMLDivElement>(null)
  const sess = useRef<Session | null>(null)
  const [ready, setReady] = useState(false)

  const [status, setStatus] = useState<{ text: string; cls: 'on' | 'err' | '' }>({ text: '未连接', cls: '' })
  const [overlay, setOverlay] = useState<{ msg: string; btn: string } | null>({
    msg: '点「连接」开一个本机登录 shell，然后敲 <code>herdr</code>。',
    btn: '连接',
  })
  // 'checking' → 'ok' | 'pair'。后端没起时故意当成 ok：那时候该让终端那边的诊断
  // 说「后端没在跑」，弹配对页只会让人以为是凭据坏了。
  const [gate, setGate] = useState<'checking' | 'ok' | 'pair' | 'reauth'>('checking')
  const [heals, setHeals] = useState(0)
  const [sticky, setSticky] = useState({ ctrl: false, alt: false })
  const [kbdUp, setKbdUp] = useState(false)
  const [scheme, setScheme] = useState<Scheme>(initialScheme)
  const [fontSize, setFontSize] = useState(
    () => Number(localStorage.getItem('fontSize')) || (matchMedia('(pointer: coarse)').matches ? 11 : 13),
  )
  const [opts, setOpts] = useState<TermOpts>({
    kitty: true, meta: true, copyOnSelect: false, sync2026: lsBool('sync2026', true),
  })

  const [settings, setSettings] = useState(false)
  // 记住上次看的那一页
  const [tab, setTab] = useState<SettingsTab>('term')
  const [showCompose, setShowCompose] = useState(() => lsBool('compose', true))
  const [showKeys, setShowKeys] = useState(() =>
    lsBool('softkeys', matchMedia('(pointer: coarse)').matches),
  )
  const [live, setLive] = useState(() => lsBool('composeLive', false))
  // 软键条每行的按键（已按 id 解析好）。几行、哪个键在哪一行都是服务端存的配置，
  // 编辑器存完把整份配置回传过来
  const [bar, setBar] = useState<SoftKey[][]>([])
  const [cfg, setCfg] = useState({ poll: urlNum('poll') ?? 500, push: urlNum('push') ?? 700 })
  const [state, setState] = useState<State | null>(null)

  // 手机竖屏那一档：顶栏收成一行，键盘弹起时干脆整条收掉（见下面的 barHidden）
  const phone = usePhone()
  const kbRoom = useKeyboardUp()
  const [peek, setPeek] = useState(false)

  const [toastMsg, setToastMsg] = useState<string | null>(null)
  const toastTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const toast = useCallback((m: string) => {
    setToastMsg(m)
    clearTimeout(toastTimer.current)
    toastTimer.current = setTimeout(() => setToastMsg(null), 2600)
  }, [])

  // gate 没过时别让自动拉回的心跳跑起来：那是 500ms 一拍的 401 风暴，
  // 而且设备被撤销时（gate 翻回 pair）也要跟着停
  const compose = useCompose(cfg, showCompose && gate === 'ok', live, toast)

  /* --------------------------------------------------------- 终端生命周期 */
  //
  // 跟着 gate 走，不能只在挂载时建一次。踩过：首次配对时 gate 从 checking 翻到 pair，
  // 主界面整棵子树被卸掉、Session 被 dispose，配完再翻回 ok 时这个 effect（deps 是
  // 空数组）不会重跑 —— 页面看着正常，但 sess.current 是 null，终端是空的、点什么都
  // 没反应，只有刷新一次才好。deps 挂 gate 之后，关一次开一次都会重新建。
  useEffect(() => {
    if (gate !== 'ok' || !host.current) return
    const s = new Session(
      host.current,
      {
        onStatus: (text, cls) => setStatus({ text, cls }),
        onOverlay: (msg, btn) => setOverlay(msg === null ? null : { msg, btn: btn ?? '连接' }),
        onHeal: setHeals,
        onKeyboardChange: setKbdUp,
      },
      scheme,
      fontSize,
    )
    s.onSticky(setSticky)
    sess.current = s
    setReady(true)
    const ro = new ResizeObserver(() => s.relayout())
    ro.observe(host.current.parentElement!)
    const onOrient = () => s.relayout()
    addEventListener('orientationchange', onOrient)
    return () => {
      ro.disconnect()
      removeEventListener('orientationchange', onOrient)
      s.dispose()
      sess.current = null
      // 状态也要退回去：不然配对页上方还挂着上一个会话的「zsh 254×45」，
      // 绿灯亮着但底下什么都没有，看起来像连上了其实是死的
      setReady(false)
      setStatus({ text: '未连接', cls: '' })
    }
    // gate 开合各建一次；主题 / 字号变化走下面的 effect
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [gate])

  const relayout = useCallback(() => sess.current?.relayout(), [])
  useViewportHeight(relayout)

  useEffect(() => { sess.current?.setScheme(scheme) }, [scheme])
  useEffect(() => {
    document.documentElement.classList.toggle('light', scheme === 'light')
  }, [scheme])
  useEffect(() => {
    if (!sess.current) return
    sess.current.opts = opts
    sess.current.term.options.macOptionIsMeta = opts.meta
    if (!opts.sync2026) sess.current.flushSync()
    localStorage.setItem('sync2026', opts.sync2026 ? '1' : '0')
  }, [opts, ready])

  // 跟随系统明暗
  useEffect(() => {
    const mq = matchMedia('(prefers-color-scheme: light)')
    const f = (e: MediaQueryListEvent) => setScheme(e.matches ? 'light' : 'dark')
    mq.addEventListener('change', f)
    return () => mq.removeEventListener('change', f)
  }, [])

  /**
   * Esc 的 document 级兜底 —— 不管焦点在哪都能用。
   *
   * 之前只在发件箱的 textarea 里转发，漏了最常见的情况：**焦点落在按钮或 body 上**。
   * 点过一下界面（顶栏、软键条、面板）焦点就会停在按钮上，此时 Esc 既不进 xterm
   * 也不进 textarea，谁都不处理，一个字节都发不出去。用户的 PTY 输入日志里只有
   * 焦点上报（CSI I / CSI O）和鼠标上报、没有任何键盘字节，就是这个原因。
   *
   * 只兜 Esc 这一个键：它在网页控件上没有语义，而 TUI 那边到处要用。
   */
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape' || e.isComposing) return
      // 面板开着就先收面板。必须 stopPropagation：否则这一下会既收面板又发给终端。
      if (settings) {
        setSettings(false)
        e.preventDefault()
        e.stopPropagation()
        return
      }
      // 终端自己聚焦时什么都不做，让 xterm 走它的正常编码路径，别重复发一次
      if (sess.current?.keyboardUp()) return
      e.preventDefault()
      sess.current?.sendKey('\x1b')
    }
    // 用 capture 阶段：xterm.js 会在自己那个隐藏 textarea 上 stopPropagation，
    // 挂 bubble 的话终端一聚焦就收不到事件了（「面板开着按 Esc 却发给了终端」就是这么来的）。
    addEventListener('keydown', onKey, true)
    return () => removeEventListener('keydown', onKey, true)
  }, [settings])

  // 布局变化（软键条 / 发件箱开合、顶栏收放）都要重排终端
  useEffect(() => { sess.current?.relayout() }, [showCompose, showKeys, peek])

  /**
   * 手机上**键盘一弹起来就把顶栏收掉**，只留一条能点开的缝。
   *
   * 那一刻屏幕上只剩 ~430px 高，而顶栏（45px）里的东西那时候一个都用不上 ——
   * 正在打字的人要的是软键条和发件箱。「连接」「敲 herdr」是连之前的事。
   *
   * 收起是临时的：手动点开（peek）只管这一次，键盘一收就自动恢复常态，不留状态 ——
   * 否则用户会记不住自己上次是展开还是收起的，下次打字时顶栏在不在全靠碰。
   */
  // 两个信号都要：kbdUp 是「对着终端打字」，kbRoom 是「视口被键盘压掉了」——
  // 在发件箱里口述时只有后者认得出来（焦点不在终端上）
  const typing = kbdUp || kbRoom
  useEffect(() => { if (!typing) setPeek(false) }, [typing])
  const barHidden = phone && typing && !peek

  /* --------------------------------------------------------- 配对门 */
  useEffect(() => {
    // 凭据随时可能没了（在别的地方 revoke 过、浏览器数据被清过），不只是启动时要查。
    // need=passkey 是另一回事：配过对，只是该重新验一次了 —— 那种别弹配对页，
    // 弹了会让人以为凭据坏了，跑回机器前拿码。
    const onUnauthed = (e: Event) =>
      setGate((e as CustomEvent<UnauthedDetail>).detail?.need === 'passkey' ? 'reauth' : 'pair')
    addEventListener(UNAUTHED, onUnauthed)
    void (async () => {
      try {
        const who = await api.get<WhoAmI>('/auth/whoami')
        setGate(who.authed ? 'ok' : 'pair')
      } catch {
        setGate('ok')
      }
    })()
    return () => removeEventListener(UNAUTHED, onUnauthed)
  }, [])

  /* --------------------------------------------------------- 启动拉配置 */
  useEffect(() => {
    if (gate !== 'ok') return
    void (async () => {
      try {
        const st = await api.get<State>('/state')
        setState(st) // 设置面板的「终端」页底下显示后端环境
        setCfg({
          poll: urlNum('poll') ?? st.compose.pollMs,
          push: urlNum('push') ?? st.compose.pushMs,
        })
      } catch { /* 拿不到就用默认值 */ }
      try {
        const sk = await api.get<SoftkeysResponse>('/softkeys')
        setBar(resolveBar(sk.lib, sk.bar))
      } catch { /* 软键条拿不到就先空着，面板里还能改 */ }
      void compose.loadPanes(true)
    })()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [gate])

  /* --------------------------------------------------------- 传图 */

  /**
   * 传一张图，然后把**路径**放到该放的地方。
   *
   * 图片本身传不进 herdr —— socket API 里没有图片通道，agent 是去读磁盘上那个文件的。
   * 所以「传图」＝落盘到 herdr 那台机器 + 把绝对路径当文本给出去。
   *
   * 路径去哪儿看发件箱开没开：开着就接到草稿末尾（还能接着说话），没开就**直接打进
   * 终端**，等于替你把路径敲进当前 pane 的输入行。以前只有发件箱里那个「图」按钮，
   * 不开发件箱就没法传图，而多数时候只是想把刚截的图丢给 agent。
   */
  const putImages = useCallback(async (files: FileList | File[]) => {
    const done = await compose.upload(files)
    if (!done.length) return
    const chunk = done.map((r) => r.path).join(' ') + ' '
    if (showCompose) {
      compose.append(chunk)
      toast(`已插入 ${done.length} 张的路径到发件箱`)
    } else {
      sess.current?.send(chunk)
      toast(`已把 ${done.length} 张的路径敲进终端`)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [showCompose, toast, compose.upload, compose.append])

  const picker = useRef<HTMLInputElement>(null)

  /**
   * 全页粘贴：剪贴板里是图就传上去。
   *
   * 用捕获阶段挂在 window 上，为的是抢在 xterm 那个隐藏 textarea 的粘贴处理之前 ——
   * 剪贴板里只有图片时它会把一段空文本粘进终端。发件箱自己的 textarea 有更准的处理
   * （插在光标处），所以落在发件箱里的粘贴直接放过去。
   */
  useEffect(() => {
    const onPaste = (e: ClipboardEvent) => {
      const files = [...(e.clipboardData?.files ?? [])].filter((f) => f.type.startsWith('image/'))
      if (!files.length) return
      if ((e.target as Element | null)?.closest?.('[data-testid="compose"]')) return
      e.preventDefault()
      void putImages(files)
    }
    addEventListener('paste', onPaste, true)
    return () => removeEventListener('paste', onPaste, true)
  }, [putImages])

  /* ------------------------------------------------------------- 全屏 */
  // 平板上地址栏 + 那一圈工具条能吃掉一大截高度，全屏之后终端能多出好几行。
  //
  // iOS Safari 只认 webkit 前缀那套（iPhone 上更是压根没有），所以两套都试，
  // 都没有就直说 —— 按钮点了没反应比没有这个按钮更糟。
  const [full, setFull] = useState(false)
  useEffect(() => {
    const d = document as FsDoc
    const sync = () => {
      setFull(!!(d.fullscreenElement ?? d.webkitFullscreenElement))
      relayout()   // 视口尺寸变了，xterm 要重排
    }
    document.addEventListener('fullscreenchange', sync)
    document.addEventListener('webkitfullscreenchange', sync)
    return () => {
      document.removeEventListener('fullscreenchange', sync)
      document.removeEventListener('webkitfullscreenchange', sync)
    }
  }, [relayout])

  const toggleFull = () => {
    const d = document as FsDoc
    const el = document.documentElement as FsEl
    if (d.fullscreenElement ?? d.webkitFullscreenElement) {
      void (d.exitFullscreen?.() ?? d.webkitExitFullscreen?.())
      return
    }
    const req = el.requestFullscreen ?? el.webkitRequestFullscreen
    if (!req) {
      toast('这个浏览器不给网页全屏。iPad / iPhone 上把页面「添加到主屏幕」，从主屏打开就没有地址栏了')
      return
    }
    // 用户手势之外调用、或者被策略挡住都会 reject，别让它变成一个没人看见的报错
    void Promise.resolve(req.call(el)).catch((e: unknown) => toast('全屏失败：' + (e as Error).message))
  }

  /* --------------------------------------------------------- 顶栏动作 */
  const connect = () => { setOverlay(null); sess.current?.connect() }
  const bumpFont = (d: number) => {
    const n = sess.current?.setFontSize(fontSize + d) ?? fontSize
    setFontSize(n)
    localStorage.setItem('fontSize', String(n))
    sess.current?.focus()
  }
  const toggleCompose = (v: boolean) => {
    setShowCompose(v)
    localStorage.setItem('compose', v ? '1' : '0')
    if (v && compose.panes.length === 0) void compose.loadPanes(true)
  }
  const toggleKeys = (v: boolean) => {
    setShowKeys(v)
    localStorage.setItem('softkeys', v ? '1' : '0')
  }

  const iconBtn = (title: string, on: boolean, onClick: () => void, child: React.ReactNode, cls?: string) => (
    <Button variant="default" size="icon" on={on} title={title} className={cls} onClick={onClick} onMouseDown={(e) => e.preventDefault()}>
      {child}
    </Button>
  )

  // 还在查凭据：什么都别渲染。这一步是本机调用，快到看不见；而要是先把主界面铺出来，
  // 就会白建一个 Session —— 服务端那边等于白起一个登录 shell、还按 HERDR_WEB_ONCONNECT
  // 敲一遍 herdr，whoami 一回来又立刻拆掉。
  if (gate === 'checking') return <div className="h-full" />

  // 没配对（或者该重新验一次）就只给这一页。半死的顶栏和发件箱留着只会让人以为是坏了
  if (gate === 'pair' || gate === 'reauth') {
    return (
      <div className="relative h-full">
        <Pairing
          mode={gate === 'reauth' ? 'reauth' : 'pair'}
          hint={gate === 'pair' ? pairHint() : undefined}
          onDone={() => {
            setGate('ok')
            history.replaceState(null, '', location.pathname) // 把 ?e= 之类的洗掉
          }}
        />
      </div>
    )
  }

  return (
    <div className="flex h-full flex-col">
      {/*
        传图用的文件框。顶栏不放按钮 —— 入口是软键条里的 act:img（自己配，位置随便放）
        和全页粘贴。accept=image/* 在手机上会同时给出「相机」和「相册」。

        **必须挂在根一级，不能塞进顶栏**：手机上键盘一弹起来顶栏整段就不渲染了，藏在里面
        的 input 跟着一起卸掉，picker.current 变成 null —— 那时候点软键条上的「传图」
        一点反应都没有（踩过）。这个框和顶栏在不在没有任何关系。
      */}
      <input
        ref={picker}
        data-testid="top-file"
        type="file"
        accept="image/*"
        multiple
        hidden
        onChange={(e) => { if (e.target.files) void putImages(e.target.files); e.target.value = '' }}
      />

      {/* 键盘弹起时顶栏收成这一条：8px 高、通屏宽，点一下把顶栏放回来。
          比「什么都不留」稳 —— 软键条也可能是关着的，那时候没有别的路能把顶栏找回来 */}
      {barHidden && (
        <button
          data-testid="header-peek"
          className="flex h-2 shrink-0 items-center justify-center border-b border-line bg-bar"
          title="点一下展开顶栏"
          onClick={() => setPeek(true)}
          onMouseDown={(e) => e.preventDefault()}
        >
          <span className="h-0.5 w-10 rounded-full bg-fg/25" />
        </button>
      )}

      {/* 手机竖屏（< 440px）收成一行：状态只留那个彩点（文字进 title）、连上之后不显示
          「连接」、字号和明暗那三个图标挪进设置 →「终端」页。七个图标在 393px 上排不下，
          折成两行就白吃掉 ~36px（约三行终端），而这三个都是一次调完的东西。 */}
      {!barHidden && (
      <header className="flex shrink-0 flex-wrap items-center gap-2.5 border-b border-line bg-bar px-2.5 py-[7px] select-none max-md:gap-1.5 max-md:px-2">
        <div className="flex min-w-0 flex-1 items-center gap-[7px] max-phone:flex-none">
          <span
            title={status.text}
            className={cn(
              'size-2 shrink-0 rounded-full',
              status.cls === 'on' && 'bg-ok shadow-[0_0_0_3px_color-mix(in_srgb,var(--color-ok)_22%,transparent)]',
              status.cls === 'err' && 'bg-bad',
              status.cls === '' && 'bg-muted',
            )}
          />
          <span className="truncate text-muted tabular-nums max-md:text-[11.5px] max-phone:hidden">{status.text}</span>
        </div>

        <div className="flex shrink-0 items-center gap-1.5">
          {/* 连上之后「连接」没用了（真断了会弹遮罩，那上面有自己的连接按钮），
              手机上这 54px 让给别的 */}
          {(status.cls !== 'on' || !phone) && <Button onClick={connect}>连接</Button>}
          <Button title="向终端写入 herdr↵" onClick={() => { sess.current?.send('herdr\r'); sess.current?.focus() }}>
            敲 herdr
          </Button>
        </div>

        <div className="flex shrink-0 items-center gap-1">
          {iconBtn('语音投稿发件箱（说话打字 → 投进 agent pane）', showCompose, () => toggleCompose(!showCompose), <Pencil className="size-4" />)}
          {iconBtn('软键盘条（Ctrl / Esc / 方向键）', showKeys, () => toggleKeys(!showKeys), <Keyboard className="size-4" />)}
          {iconBtn('缩小字号', false, () => bumpFont(-1), <AArrowDown className="size-4" />, 'max-phone:hidden')}
          {iconBtn('放大字号', false, () => bumpFont(1), <AArrowUp className="size-4" />, 'max-phone:hidden')}
          {iconBtn('切换明暗', false, () => setScheme(scheme === 'dark' ? 'light' : 'dark'), <CircleHalf className="size-4" />, 'max-phone:hidden')}
          {iconBtn(full ? '退出全屏' : '全屏（去掉地址栏和工具条，终端多几行）', full, toggleFull,
            full ? <Minimize className="size-4" /> : <Maximize className="size-4" />)}
          {iconBtn('设置（终端 / 软键条 / 设备）', settings, () => setSettings(!settings), <Gear className="size-4" />)}
        </div>
      </header>
      )}

      <main className="relative min-h-0 flex-1">
        {/* overflow-hidden：容器一缩（呼输入法）到终端重排完之间，xterm 的画布还是旧的高度，
            不裁的话它会画到发件箱上面去；冻帧那张图也靠这个裁 */}
        <div ref={host} className="term-host absolute inset-0 overflow-hidden pt-1.5 pr-1 pb-1 pl-2" />
        {overlay && (
          <div className="absolute inset-0 z-5 grid place-items-center bg-bg/85 p-5 backdrop-blur-[3px]">
            <div className="max-w-[460px] text-center">
              <h1 className="mb-2 text-[17px] font-semibold tracking-[.2px]">herdr in the browser</h1>
              <p className="text-muted [&_code]:rounded [&_code]:bg-fg/10 [&_code]:px-1.5 [&_code]:py-px"
                 dangerouslySetInnerHTML={{ __html: overlay.msg }} />
              <Button variant="primary" className="mt-1.5 px-[22px] py-2.5 text-sm" onClick={connect}>
                {overlay.btn}
              </Button>
            </div>
          </div>
        )}
        {settings && (
          <SettingsPanel
            tab={tab}
            onTab={setTab}
            onClose={() => setSettings(false)}
            opts={opts}
            setOpt={(k, v) => setOpts((o) => ({ ...o, [k]: v }))}
            heals={heals}
            onSaved={(lib, b) => setBar(resolveBar(lib, b))}
            toast={toast}
            state={state}
            fontSize={fontSize}
            onFont={bumpFont}
            scheme={scheme}
            onScheme={() => setScheme(scheme === 'dark' ? 'light' : 'dark')}
          />
        )}
        <Toast msg={toastMsg} />
      </main>

      {/* 底部一块面板：发件箱 + 软键条同一套边框、同一个宽度（见 Dock）。
          两块都关掉时整块不出现，那点边框和内边距不该白占终端的地方 */}
      {(showCompose || showKeys) && (
        <Dock
          onLayout={relayout}
          keys={showKeys ? (
            <Softkeys
              bar={bar}
              sticky={sticky}
              kbdUp={kbdUp}
              onSend={(b) => { sess.current?.sendKey(b); if (kbdUp) sess.current?.focus() }}
              onSticky={(w) => sess.current?.toggleSticky(w)}
              onKeyboard={() => sess.current?.toggleKeyboard()}
              onImage={() => picker.current?.click()}
            />
          ) : null}
        >
          {showCompose && (
            <Compose
              text={compose.text}
              onChangeText={compose.setText}
              panes={compose.panes}
              sel={compose.sel}
              onSelect={compose.selectTarget}
              info={compose.info}
              bad={compose.bad}
              busy={compose.busy}
              live={live}
              onLive={(v) => { setLive(v); localStorage.setItem('composeLive', v ? '1' : '0') }}
              onPull={compose.pull}
              onSubmit={compose.submit}
              onReload={() => void compose.loadPanes()}
              onAttach={compose.attach}
              onRecall={compose.recall}
              pollMs={cfg.poll}
              pushMs={cfg.push}
            />
          )}
        </Dock>
      )}
    </div>
  )
}
