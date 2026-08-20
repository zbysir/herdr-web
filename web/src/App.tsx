import { useCallback, useEffect, useRef, useState } from 'react'
import { Keyboard, Pencil, CircleHalf, Command, AArrowDown, AArrowUp } from './icons'
import { api, TOKEN, type SoftKey, type SoftkeysResponse, type State } from '@/lib/api'
import { Session, type Cap } from '@/term/session'
import { initialScheme, type Scheme } from '@/term/themes'
import { useViewportHeight } from '@/hooks/useViewportHeight'
import { useCompose } from '@/hooks/useCompose'
import { Button } from '@/components/ui/button'
import { Toast } from '@/components/ui/toast'
import { Softkeys } from '@/components/Softkeys'
import { SoftkeysPanel } from '@/components/SoftkeysPanel'
import { Compose } from '@/components/Compose'
import { CapsPanel, type CapsPanel_Opts } from '@/components/CapsPanel'
import { cn } from '@/lib/utils'

const lsBool = (k: string, def: boolean) => {
  const v = localStorage.getItem(k)
  return v === null ? def : v === '1'
}

// URL 上的 ?poll= / ?push= 可以临时覆盖服务端下发的默认值，方便在平板上试手感
const urlNum = (k: string) => {
  const v = Number(new URLSearchParams(location.search).get(k))
  return v > 0 ? Math.max(100, v) : null
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
  const [caps, setCaps] = useState<Cap[]>([])
  const [heals, setHeals] = useState(0)
  const [sticky, setSticky] = useState({ ctrl: false, alt: false })
  const [kbdUp, setKbdUp] = useState(false)
  const [scheme, setScheme] = useState<Scheme>(initialScheme)
  const [fontSize, setFontSize] = useState(
    () => Number(localStorage.getItem('fontSize')) || (matchMedia('(pointer: coarse)').matches ? 11 : 13),
  )
  const [opts, setOpts] = useState<CapsPanel_Opts>({
    kitty: true, meta: true, copyOnSelect: false, sync2026: lsBool('sync2026', true),
  })

  const [panel, setPanel] = useState<'caps' | 'softkeys' | null>(null)
  const [showCompose, setShowCompose] = useState(() => lsBool('compose', true))
  const [showKeys, setShowKeys] = useState(() =>
    lsBool('softkeys', matchMedia('(pointer: coarse)').matches),
  )
  const [live, setLive] = useState(() => lsBool('composeLive', false))
  const [keys, setKeys] = useState<SoftKey[]>([])
  const [cfg, setCfg] = useState({ poll: urlNum('poll') ?? 500, push: urlNum('push') ?? 700 })

  const [toastMsg, setToastMsg] = useState<string | null>(null)
  const toastTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const toast = useCallback((m: string) => {
    setToastMsg(m)
    clearTimeout(toastTimer.current)
    toastTimer.current = setTimeout(() => setToastMsg(null), 2600)
  }, [])

  const compose = useCompose(cfg, showCompose, live, toast)

  /* --------------------------------------------------------- 终端生命周期 */
  useEffect(() => {
    if (!host.current) return
    const s = new Session(
      host.current,
      {
        onStatus: (text, cls) => setStatus({ text, cls }),
        onCaps: setCaps,
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
    }
    // 只在挂载时建一次；主题 / 字号变化走下面的 effect
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  useViewportHeight(useCallback(() => sess.current?.relayout(), []))

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

  // 布局变化（软键条 / 发件箱开合）都要重排终端
  useEffect(() => { sess.current?.relayout() }, [showCompose, showKeys])

  /* --------------------------------------------------------- 启动拉配置 */
  useEffect(() => {
    if (!TOKEN) {
      setOverlay({
        msg: 'URL 里缺 token。复制启动时打印的那个链接（手机可以直接扫终端里的二维码）。',
        btn: '仍然试试',
      })
      return
    }
    void (async () => {
      try {
        const st = await api.get<State>('/state')
        setCfg({
          poll: urlNum('poll') ?? st.compose.pollMs,
          push: urlNum('push') ?? st.compose.pushMs,
        })
      } catch { /* 拿不到就用默认值 */ }
      try {
        const sk = await api.get<SoftkeysResponse>('/softkeys')
        setKeys(sk.keys)
      } catch { /* 软键条拿不到就先空着，面板里还能改 */ }
      void compose.loadPanes(true)
    })()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

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

  const iconBtn = (title: string, on: boolean, onClick: () => void, child: React.ReactNode) => (
    <Button variant="default" size="icon" on={on} title={title} onClick={onClick} onMouseDown={(e) => e.preventDefault()}>
      {child}
    </Button>
  )

  return (
    <div className="flex h-full flex-col">
      <header className="flex shrink-0 flex-wrap items-center gap-2.5 border-b border-line bg-bar px-2.5 py-[7px] select-none max-md:gap-1.5 max-md:px-2">
        <div className="flex min-w-0 flex-1 items-center gap-[7px]">
          <span
            className={cn(
              'size-2 shrink-0 rounded-full',
              status.cls === 'on' && 'bg-ok shadow-[0_0_0_3px_color-mix(in_srgb,var(--color-ok)_22%,transparent)]',
              status.cls === 'err' && 'bg-bad',
              status.cls === '' && 'bg-muted',
            )}
          />
          <span className="truncate text-muted tabular-nums max-md:text-[11.5px]">{status.text}</span>
        </div>

        <div className="flex shrink-0 items-center gap-1.5">
          <Button onClick={connect}>连接</Button>
          <Button title="向终端写入 herdr↵" onClick={() => { sess.current?.send('herdr\r'); sess.current?.focus() }}>
            敲 herdr
          </Button>
        </div>

        <div className="flex shrink-0 items-center gap-1">
          {iconBtn('语音投稿发件箱（说话打字 → 投进 agent pane）', showCompose, () => toggleCompose(!showCompose), <Pencil className="size-4" />)}
          {iconBtn('软键盘条（Ctrl / Esc / 方向键）', showKeys, () => toggleKeys(!showKeys), <Keyboard className="size-4" />)}
          {iconBtn('缩小字号', false, () => bumpFont(-1), <AArrowDown className="size-4" />)}
          {iconBtn('放大字号', false, () => bumpFont(1), <AArrowUp className="size-4" />)}
          {iconBtn('切换明暗', false, () => setScheme(scheme === 'dark' ? 'light' : 'dark'), <CircleHalf className="size-4" />)}
          {iconBtn('终端能力', panel === 'caps', () => setPanel(panel === 'caps' ? null : 'caps'), <Command className="size-4" />)}
        </div>
      </header>

      <main className="relative min-h-0 flex-1">
        <div ref={host} className="term-host absolute inset-0 pt-1.5 pr-1 pb-1 pl-2" />
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
        {panel === 'caps' && (
          <CapsPanel
            onClose={() => setPanel(null)}
            caps={caps}
            opts={opts}
            heals={heals}
            setOpt={(k, v) => setOpts((o) => ({ ...o, [k]: v }))}
          />
        )}
        {panel === 'softkeys' && (
          <SoftkeysPanel onClose={() => setPanel(null)} onSaved={setKeys} toast={toast} />
        )}
        <Toast msg={toastMsg} />
      </main>

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
          onEscape={() => sess.current?.sendKey('\x1b')}
          pollMs={cfg.poll}
          pushMs={cfg.push}
        />
      )}

      {showKeys && (
        <Softkeys
          keys={keys}
          sticky={sticky}
          kbdUp={kbdUp}
          onSend={(b) => { sess.current?.sendKey(b); if (kbdUp) sess.current?.focus() }}
          onSticky={(w) => sess.current?.toggleSticky(w)}
          onKeyboard={() => sess.current?.toggleKeyboard()}
          onEdit={() => setPanel(panel === 'softkeys' ? null : 'softkeys')}
        />
      )}
    </div>
  )
}
