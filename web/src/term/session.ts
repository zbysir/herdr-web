// xterm.js + 一层「补协议」的胶水。
//
// 这一层是命令式的，故意不做成 React 组件：它要直接摸 xterm 的 parser、
// 逐字节收 WebSocket、按 rAF 补重绘 —— 套上 React 的渲染周期只会碍事。
// React 那边只拿一个 ref 挂载它，再订阅几个状态回调。
//
// herdr 启动时会请求这些能力（实测抓包）：
//   CSI > 7 u        kitty 键盘协议    xterm.js 不支持 → 这里自己编码
//   OSC 10;? / 11;?  查询前后景色      xterm.js 不回   → 这里自己回
//   CSI ? 2031 h     主题变更通知      xterm.js 不支持 → 这里自己发
//   1049/1000/1002/1003/1006/2004/1004/2026  xterm.js 原生支持
import { Terminal, type IDisposable } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebglAddon } from '@xterm/addon-webgl'
import { WebLinksAddon } from '@xterm/addon-web-links'
import { Unicode11Addon } from '@xterm/addon-unicode11'
import { ClipboardAddon } from '@xterm/addon-clipboard'
import { writeClipboard } from '@/lib/clipboard'
import { ptyURL } from '@/lib/api'

/**
 * 打开终端里的链接（OSC 8 和自动识别出来的 URL）。
 *
 * 终端上显示什么是**跑在里面的程序完全说了算**的，而 agent 天天读不可信内容 ——
 * 所以点开之前先看 scheme，只放 http / https / mailto。现代浏览器基本会拦
 * `javascript:` 和 `data:` 的顶层导航，但那是它们的好心，不是我们的防线。
 */
const SAFE_SCHEMES = ['http:', 'https:', 'mailto:']

function openLink(uri: string) {
  let u: URL
  try {
    u = new URL(uri, location.href)
  } catch {
    return
  }
  if (!SAFE_SCHEMES.includes(u.protocol)) {
    console.warn('[herdr-web] 拒绝打开这个 scheme 的链接：', uri)
    return
  }
  window.open(u.href, '_blank', 'noopener,noreferrer')
}
import { linkAtCell, pathLinkProvider } from './paths'
import { THEMES, type Scheme } from './themes'
import { attachTouch } from './touch'
import { hitsSwitchButton } from './mobilebar'

export interface Cap { key: string; label: string; ok: boolean }

export interface SessionCallbacks {
  onStatus: (text: string, cls: 'on' | 'err' | '') => void
  onOverlay: (msg: string | null, btn?: string) => void
  onHeal: (n: number) => void
  onKeyboardChange: (up: boolean) => void
  /**
   * 剪贴板写不进去（手机上没有用户手势那一档，见 lib/clipboard.ts）。
   * 文本交回 UI，让它出一个「点一下复制」—— 那一下点击本身就是手势。
   */
  onCopyBlocked: (text: string) => void
  /**
   * 终端里点了一条文件路径。**给的是屏幕上的原样**（可能是相对的、可能带 `~`）——
   * 解析基准是那个 pane 的 cwd，而「哪个 pane」只有 React 那边知道（面板列表在它手上），
   * 所以这一层只管认出来往上传，不管解。
   */
  onPath: (raw: string) => void
  /**
   * 点了 herdr 移动端顶栏那个 `switch`（只在设置里开着「点 switch 开面板一览」时）。
   * 这一下**不会**发给 herdr，改开我们自己的面板一览 —— 判据见 `term/mobilebar.ts`。
   */
  onSwitchPanel: () => void
}

// 程序请求过的私有模式 → 人话 + 我们是否真支持
const KNOWN: Record<number, [string, boolean]> = {
  25: ['光标显隐', true],
  1000: ['鼠标点击上报', true],
  1002: ['鼠标拖拽上报', true],
  1003: ['鼠标移动上报', true],
  1005: ['UTF-8 鼠标坐标', false],
  1006: ['SGR 鼠标坐标', true],
  1015: ['urxvt 鼠标坐标', false],
  1016: ['像素级鼠标坐标', true],
  1004: ['焦点进出上报', true],
  1049: ['备用屏幕', true],
  2004: ['括号粘贴', true],
  2026: ['同步输出（防撕裂）', true],
  2031: ['主题变更通知', true],
}

const isMac = /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent)

// 冻帧的几个时长（毫秒），见下面 freeze()
const THAW_GRACE = 120 // 新画面画上之后再多盖一会儿 —— 那一帧还是旧内容，herdr 的 SIGWINCH 重绘还在路上
const THAW_CAP = 500 // 一直等不到重绘也得撤，别糊着一张旧图不放
const THAW_FADE = 200 // 淡出时长，跟内联的 transition 对齐
const FREEZE_MAX = 700 // 连续重排最多接着用同一张（拖窗口时别把终端冻死）

// 尺寸什么时候落地（毫秒），见 relayout()
const SETTLE_MS = 90 // 布局停下来多久才动 xterm（面板开合这类一步到位的变化）
const VV_HOLD = 260 // 视口自己在动（呼输入法、转屏、地址栏收放）：从最后一下起再等这么久
const VV_CAP = 900 // 视口一直在动也得落一次（iOS 上地址栏能来回弹好几秒）

// 自动重连（见下面的 retry / wake）。
//
// 手机和平板锁屏时系统会把页面挂起，WebSocket 跟着断 —— 这一侧拦不住，那是系统在
// 回收后台标签页的资源，页面里没有任何开关能留住它。所以不去「解决断线」，而是让
// 回来的时候自己连上：一条 WebSocket 一个 PTY，重连拿到的是**新的登录 shell**，
// 但 herdr 的 pane 都活在 herdr server 里，`herdr` 一敲就 attach 回去 —— 和人手点
// 那一下完全等价，那就别让人手点。
const RETRY_MS = [400, 800, 1500, 3000, 5000, 8000] // 最后一档一直用下去
const MAX_RETRY = 8 // 连不上就别无限敲后端（前台可见时约 30 秒），改成把原因摊在遮罩上
const PROBE_MS = 3000 // 「你还活着吗」的等回音时间

export class Session {
  readonly term: Terminal
  private fit = new FitAddon()
  private ws: WebSocket | null = null
  private caps = new Map<string, Cap>()
  private kitty = { flags: 0, stack: [] as number[] }
  private sticky = { ctrl: false, alt: false }
  private scheme: Scheme
  private alive = false
  private exited = false
  private want = false // 人点过「连接」= 表态要连着，掉线就自动重连
  private retries = 0
  private retryTimer: ReturnType<typeof setTimeout> | undefined
  private probeTimer: ReturnType<typeof setTimeout> | undefined
  private paintTimer: ReturnType<typeof setTimeout> | undefined
  private paintHeals = 0
  private settleTimer: ReturnType<typeof setTimeout> | undefined
  private settleFloor = 0 // 早于这个时刻不落地（视口还在动）
  private settleCap = 0 // 晚于这个时刻必须落地（0 = 不设上限）
  private detachTouch: (() => void) | null = null
  private freezeEl: HTMLCanvasElement | null = null
  private freezeAt = 0
  private freezeAwait = false // 冻着，等 herdr 的 SIGWINCH 重画（见 awaitRedraw）
  private thawTimer: ReturnType<typeof setTimeout> | undefined
  private thawWatch: IDisposable | null = null
  private fadingEl: HTMLCanvasElement | null = null
  private fadeTimer: ReturnType<typeof setTimeout> | undefined

  /** 面板里的开关，App 直接改 */
  opts = { kitty: true, meta: true, copyOnSelect: false, sync2026: true, switchPanel: true }

  constructor(private host: HTMLElement, private cb: SessionCallbacks, scheme: Scheme, fontSize: number) {
    this.scheme = scheme
    this.term = new Terminal({
      allowProposedApi: true,
      fontFamily: 'ui-monospace, SFMono-Regular, "SF Mono", Menlo, "Cascadia Mono", Consolas, "PingFang SC", monospace',
      fontSize,
      lineHeight: 1.12,
      cursorBlink: true,
      cursorStyle: 'block',
      macOptionIsMeta: true, // alt+1 / alt+g 这类 herdr 快捷键需要
      rightClickSelectsWord: false,
      scrollback: 2000,
      drawBoldTextInBrightColors: false,
      theme: THEMES[scheme],
      linkHandler: { activate: (_e, uri) => openLink(uri) },
    })

    this.term.loadAddon(this.fit)
    this.term.loadAddon(new WebLinksAddon((_e, uri) => openLink(uri)))
    // 文件路径可点：agent 打出 `/Users/x/out/chart.png`，点一下直接开图。
    // 这条路**天生不关心图在不在当前 workspace 下** —— 路径是 agent 自己给的。
    this.term.registerLinkProvider(pathLinkProvider(this.term, (p) => this.cb.onPath(p)))
    this.term.loadAddon(new Unicode11Addon())
    this.term.unicode.activeVersion = '11'
    // OSC 52（终端里的程序自己写剪贴板 —— herdr 的 COPY 模式按 `y` 走的就是这条）。
    // **必须换成自己的 provider**：默认那个直接调 navigator.clipboard，手机上没有用户
    // 手势就是一个没人接的 rejected promise，复制静默失败。走 this.copy 才有兜底。
    this.term.loadAddon(
      new ClipboardAddon(undefined, {
        readText: () => navigator.clipboard?.readText?.().catch(() => '') ?? Promise.resolve(''),
        writeText: (_sel: string, text: string) => void this.copy(text),
      }),
    )
    this.term.open(host)
    try {
      // preserveDrawingBuffer：合成完别把绘制缓冲丢掉，不然改尺寸前读不出画面（见 freeze()）
      const webgl = new WebglAddon(true)
      webgl.onContextLoss(() => webgl.dispose())
      this.term.loadAddon(webgl)
    } catch {
      /* 没有 WebGL 就退回 DOM 渲染 */
    }

    this.installParsers()
    this.installKeyboard()
    this.term.onData((d) => this.send(this.applySticky(d)))
    this.term.onBinary((d) => {
      if (this.ws?.readyState !== WebSocket.OPEN) return
      const buf = new Uint8Array(d.length)
      for (let i = 0; i < d.length; i++) buf[i] = d.charCodeAt(i) & 255
      this.ws.send(buf)
    })
    this.term.onSelectionChange(() => {
      if (this.opts.copyOnSelect && this.term.hasSelection()) void this.copy(this.term.getSelection())
    })

    host.addEventListener('contextmenu', (e) => e.preventDefault())
    this.detachTouch = attachTouch(host, this.term, {
      send: (d) => this.send(d),
      // 触屏上点链接走这条（桌面走 xterm 的 linkifier）。两条路认的是同一份规则：
      // 文件路径都出自 findPaths，URL 那边桌面归 WebLinksAddon、这边一个粗正则。
      openLinkAt: (col, row) => {
        const hit = linkAtCell(this.term, col, row)
        if (!hit) return
        if (hit.kind === 'url') openLink(hit.text)
        else this.cb.onPath(hit.text)
      },
      // herdr 移动端顶栏右上角那个 switch：开着这个开关时，点它开的是我们的面板一览，
      // 这一下不再发给 herdr（不然两张面板会一起开着）。窄屏之外根本没有那个按钮，
      // 判据自己会返回 false，不用在这儿判是不是手机。
      claimTap: (col, row) => {
        if (!this.opts.switchPanel || !hitsSwitchButton(this.term, col, row)) return false
        this.cb.onSwitchPanel()
        return true
      },
    })

    for (const ev of ['focus', 'blur']) {
      this.kbdEl()?.addEventListener(ev, () => this.cb.onKeyboardChange(this.keyboardUp()))
    }
    // 「人回来了」的三个信号（见 wake）：切回这个标签页 / 解锁、网络回来、
    // pageshow（iOS 从 BFCache 里把页面拿回来时不发 visibilitychange）。
    document.addEventListener('visibilitychange', this.wake)
    addEventListener('online', this.wake)
    addEventListener('pageshow', this.wake)
    this.fit.fit()
  }

  /* ------------------------------------------------------------- 补协议 */

  private installParsers() {
    // CSI ? Pm h（开启私有模式）—— 只做旁听，返回 false 让 xterm.js 继续处理
    this.term.parser.registerCsiHandler({ prefix: '?', final: 'h' }, (params) => {
      for (const p of params) {
        const code = Array.isArray(p) ? p[0] : p
        const known = KNOWN[code]
        if (known) this.noteCap(`DEC ${code}`, known[0], known[1])
        if (code === 2031) this.sendScheme() // 程序刚订阅，先告诉它当前是什么主题
      }
      // 面板里关掉同步输出时就地吞掉，别让 xterm.js 进「只攒不画」的状态（见下面的看门狗）。
      // 只吞单独的 `CSI ? 2026 h`（herdr 就是这么发的），混在别的参数里一律放过去。
      const only = params.length === 1 ? (Array.isArray(params[0]) ? params[0][0] : params[0]) : -1
      return only === 2026 && !this.opts.sync2026
    })

    // CSI ? 996 n —— 主动查询当前配色方案
    this.term.parser.registerCsiHandler({ prefix: '?', final: 'n' }, (params) => {
      const p = Array.isArray(params[0]) ? params[0][0] : params[0]
      if (p === 996) {
        this.sendScheme()
        return true
      }
      return false
    })

    // kitty 键盘协议：> 入栈  < 出栈  = 直接设置  ? 查询
    this.term.parser.registerCsiHandler({ prefix: '>', final: 'u' }, (params) => {
      this.kitty.stack.push(this.kitty.flags)
      this.kitty.flags = (Array.isArray(params[0]) ? params[0][0] : params[0]) || 1
      this.noteCap('kitty kbd', `键盘协议 flags=${this.kitty.flags}（这里实现了消歧子集）`, true)
      return true
    })
    this.term.parser.registerCsiHandler({ prefix: '<', final: 'u' }, () => {
      this.kitty.flags = this.kitty.stack.pop() || 0
      return true
    })
    this.term.parser.registerCsiHandler({ prefix: '=', final: 'u' }, (params) => {
      this.kitty.flags = (Array.isArray(params[0]) ? params[0][0] : params[0]) || 0
      return true
    })
    this.term.parser.registerCsiHandler({ prefix: '?', final: 'u' }, () => {
      this.send(`\x1b[?${this.kitty.flags}u`)
      return true
    })

    // OSC 10/11 查询前景 / 背景色
    for (const [code, pick] of [[10, 'foreground'], [11, 'background']] as const) {
      this.term.parser.registerOscHandler(code, (data) => {
        if (data !== '?') return false
        this.noteCap(`OSC ${code}`, code === 10 ? '查询前景色' : '查询背景色（用来判断明暗）', true)
        this.send(`\x1b]${code};${toRgbSpec(THEMES[this.scheme][pick] as string)}\x1b\\`)
        return true
      })
    }
    // 这两个只登记不处理，返回 false 让内置 / 插件继续
    this.term.parser.registerOscHandler(52, () => {
      this.noteCap('OSC 52', '用转义序列写系统剪贴板', true)
      return false
    })
    this.term.parser.registerOscHandler(8, () => {
      this.noteCap('OSC 8', '终端超链接（点得开）', true)
      return false
    })
  }

  private noteCap(key: string, label: string, ok: boolean) {
    if (this.caps.has(key)) return
    this.caps.set(key, { key, label, ok })
  }

  private sendScheme() {
    this.send(`\x1b[?997;${this.scheme === 'dark' ? 1 : 2}n`)
  }

  /* ---------------------------------------------------------- 渲染看门狗 */
  // xterm.js 6.0 的 RenderService 有三条「收下重绘请求但不画」的路：
  //   1. DEC 2026 同步输出开着时，refreshRows() 把范围攒进 SynchronizedOutputHandler，
  //      等 `CSI ? 2026 l`（ESU）或 1s 兜底超时才真画；
  //   2. 真正的绘制在 requestAnimationFrame 里，后台标签页里 rAF 完全不跑；
  //   3. IntersectionObserver 判定终端不可见时直接挂起。
  // herdr 三条全踩：它常驻开着 2026，一帧几 KB 还会被 WebSocket 和 xterm.js 的写队列
  // 拆成多次 write，跨好几个 rAF —— 攒漏一次，屏幕上就留一块没画上的空白。
  // 缓冲区里字一直是好的（滚一下或改字号强制重绘就回来了），所以这里只补重绘。
  private armRepaint() {
    clearTimeout(this.paintTimer)
    this.paintTimer = setTimeout(() => this.repaint(), 180)
  }

  repaint() {
    clearTimeout(this.paintTimer)
    if (this.term.modes.synchronizedOutputMode) {
      // 流都停了还开着 = 这一帧的 ESU 没等到。自己收尾（xterm.js 自带的 1s 兜底太慢），
      // 这一句写下去本身就会触发全屏重绘。
      this.paintHeals++
      this.term.write('\x1b[?2026l')
      this.cb.onHeal(this.paintHeals)
      return
    }
    this.term.refresh(0, this.term.rows - 1)
  }

  /** 面板里关同步输出时，把当前可能正开着的那一帧收尾，否则要等下一个 BSU 才生效。 */
  flushSync() {
    if (this.term.modes.synchronizedOutputMode) this.term.write('\x1b[?2026l')
  }

  /* ------------------------------------------------------------- 键盘 */

  /** kitty 模式生效中？（程序声明过 CSI > 1 u，且面板里没关） */
  private kittyOn() {
    return this.opts.kitty && !!(this.kitty.flags & 1)
  }

  /**
   * Esc 的字节。
   *
   * kitty 的 disambiguate flag（0b1）就是为这个键存在的：bare 0x1b 是**所有**转义
   * 序列的前缀，程序收到它没法立刻判断这是一次真实的 Esc 还是一段序列的开头，只能
   * 等超时或者干脆丢掉 —— 表现就是「网页上按 Esc 没反应」。声明了这个 flag 的程序
   * （herdr、Claude Code）等的是 CSI 27 u。
   */
  escBytes(mods = 1) {
    if (!this.kittyOn()) return '\x1b'
    return mods === 1 ? '\x1b[27u' : `\x1b[27;${mods}u`
  }

  /**
   * 快捷键条 / 发件箱转发过来的现成字节。
   *
   * 这些字节是服务端按「按键谱」解析出来的，服务端不知道 kitty 模式开没开，所以
   * 孤立的 ESC 到这儿要按当前模式重新编码。只特判这一个 —— 其余控制码（\r、\t、
   * ctrl+x）legacy 编码本来就不含歧义，程序两种都认。
   */
  sendKey(bytes: string) {
    this.send(bytes === '\x1b' ? this.escBytes() : bytes)
  }

  private kittySeq(e: KeyboardEvent): string | null {
    if (!this.kittyOn() || e.metaKey) return null

    const mods = 1 + (e.shiftKey ? 1 : 0) + (e.altKey ? 2 : 0) + (e.ctrlKey ? 4 : 0)
    if (e.code === 'Escape') return this.escBytes(mods)

    if (!e.ctrlKey) return null

    let code: number | null = null
    if (/^Key[A-Z]$/.test(e.code)) code = e.code.charCodeAt(3) + 32 // KeyB -> 'b'
    else if (/^Digit[0-9]$/.test(e.code)) code = e.code.charCodeAt(5)
    else if (e.code === 'Enter' || e.code === 'NumpadEnter') code = 13
    else if (e.code === 'Backspace') code = 127
    else if (e.code === 'Tab') code = 9
    else return null

    // legacy 已经能唯一表达的（纯 Ctrl+字母 = 控制码）就不插手
    const ambiguous = e.shiftKey || code === 13 || code === 9 || (code >= 48 && code <= 57)
    if (!ambiguous) return null

    return `\x1b[${code};${mods}u`
  }

  private installKeyboard() {
    this.term.attachCustomKeyEventHandler((e) => {
      if (e.type !== 'keydown') return true
      if (isMac && e.metaKey) {
        const k = e.key.toLowerCase()
        if (k === 'c' && this.term.hasSelection()) {
          void this.copy(this.term.getSelection())
          e.preventDefault()
          return false
        }
        if (k === 'k') {
          this.term.clear()
          e.preventDefault()
          return false
        }
        return true // ⌘V 及其它 ⌘ 组合留给浏览器
      }
      if (!isMac && e.ctrlKey && e.shiftKey && e.code === 'KeyC' && this.term.hasSelection()) {
        void this.copy(this.term.getSelection())
        e.preventDefault()
        return false
      }
      const seq = this.kittySeq(e)
      if (seq) {
        this.send(seq)
        e.preventDefault()
        return false
      }
      return true
    })
  }

  // 手机的虚拟键盘不一定给出可靠的 keydown，所以粘滞修饰键在数据层做
  private applySticky(d: string) {
    if (!this.sticky.ctrl && !this.sticky.alt) return d
    if (d.length !== 1) {
      this.setSticky('ctrl', false)
      this.setSticky('alt', false)
      return d
    }
    let out = d
    if (this.sticky.ctrl) {
      const c = d.toLowerCase().charCodeAt(0)
      if (c >= 97 && c <= 122) out = String.fromCharCode(c - 96)
      else if (d === ' ') out = '\x00'
      else if ('[\\]^_'.includes(d)) out = String.fromCharCode(d.charCodeAt(0) - 64)
      else if (d === '?') out = '\x7f'
    }
    if (this.sticky.alt) out = '\x1b' + out
    this.setSticky('ctrl', false)
    this.setSticky('alt', false)
    return out
  }

  private stickyListener: ((s: { ctrl: boolean; alt: boolean }) => void) | null = null
  onSticky(fn: (s: { ctrl: boolean; alt: boolean }) => void) {
    this.stickyListener = fn
  }
  setSticky(which: 'ctrl' | 'alt', on: boolean) {
    this.sticky[which] = on
    this.stickyListener?.({ ...this.sticky })
  }
  toggleSticky(which: 'ctrl' | 'alt') {
    this.setSticky(which, !this.sticky[which])
  }

  /**
   * 复制到系统剪贴板。三个入口都走这里：⌘C / Ctrl+Shift+C、「选中即复制」、
   * OSC 52（herdr 的 COPY 模式）。
   *
   * **写不进去时一定要说话。** 手机上真正会走到这儿的两条路（COPY 模式、选中即复制）
   * 都不是点击，浏览器于是不给写剪贴板；以前这里 catch 完就完了，表现是「选区好好的、
   * 什么都没发生、剪贴板里还是上一次的东西」。现在把文本交给 UI 出一个「点一下复制」。
   */
  async copy(text: string) {
    if (!text) return
    if (await writeClipboard(text)) return
    this.cb.onCopyBlocked(text)
  }

  /* ------------------------------------------------------------- 键盘显隐 */

  private kbdEl() {
    return this.host.querySelector('textarea.xterm-helper-textarea') as HTMLTextAreaElement | null
  }
  keyboardUp() {
    return document.activeElement === this.kbdEl()
  }
  toggleKeyboard() {
    const el = this.kbdEl()
    if (!el) return
    if (this.keyboardUp()) el.blur()
    else el.focus()
    this.cb.onKeyboardChange(this.keyboardUp())
  }

  /* ------------------------------------------------------------- 连接 */

  send(data: string) {
    if (this.ws?.readyState === WebSocket.OPEN) this.ws.send(JSON.stringify({ t: 'i', d: data }))
  }

  /**
   * 收掉当前这条连接，并且**摘掉它的回调**。
   *
   * 「连接」按钮随时能按，断开也不一定 close 得干净（平板息屏时 WS 常常是僵着的）。
   * 不自己先收掉的话：服务端会再起一个登录 shell（一条连接一个 PTY），两个 shell
   * 的输出往同一个 xterm 里灌，屏幕当场花掉；输入只到新的那个，旧的那个只要连接
   * 还在就一直活着。摘回调是因为 close 是异步的 —— 旧连接的 onclose / 还在路上的
   * 消息不能再改新连接的状态。
   */
  private teardown() {
    clearTimeout(this.probeTimer)
    this.probeTimer = undefined
    const ws = this.ws
    this.ws = null
    if (!ws) return
    ws.onmessage = null
    ws.onclose = null
    ws.onerror = null
    if (ws.readyState === WebSocket.CONNECTING || ws.readyState === WebSocket.OPEN) ws.close()
  }

  /**
   * 把终端恢复成「刚打开」的状态。
   *
   * 每次连接都是一个**全新的登录 shell**（服务端在断开时就把 PTY 杀了），但 xterm
   * 实例是复用的 —— 上一次 herdr 打开的那些私有模式还留在里面，于是重连之后：
   *
   * - **鼠标上报（1003 移动 + 1006 SGR）还开着**：指针 / 手写笔一动，xterm 就往新
   *   shell 的命令行里灌 `ESC [ < 35;120;36 M`。zsh 的 ZLE 把认不出的 `ESC [ <`
   *   前缀吃掉、余下的自插进命令行，屏幕上就是 `35;120;36M35;115;37M…` 这一串。
   * - **kitty 键盘协议的 flags 还留着**：Esc 会被编成 CSI 27 u，新 shell 里就是 `[27u`。
   * - **屏幕上还是上一次 herdr 的残帧**：新 shell 不清屏，看着像「连上了但没好」。
   *
   * `term.reset()` 把缓冲区、DEC 私有模式和鼠标协议一起归零（自定义键盘处理器和
   * 注册过的 parser 回调都留着，不用重装）。我们自己攒的那几个状态在这儿一并清掉 ——
   * 能力清单也得清：那是上一个 shell 里的程序声明的，跟新 shell 没关系。
   */
  private resetForNewSession() {
    clearTimeout(this.paintTimer)
    this.thaw(true) // 冻帧是上一个 shell 的画面，reset 之后留着只会误导
    this.term.reset()
    this.kitty = { flags: 0, stack: [] }
    this.sticky = { ctrl: false, alt: false }
    this.stickyListener?.({ ...this.sticky })
    this.caps.clear()
    this.paintHeals = 0
    this.cb.onHeal(0)
  }

  /**
   * 连上（人点「连接」走这条）。
   *
   * 手点这一下同时是**「我要一直连着」的表态**：之后不管是锁屏挂起、切网还是后端重启，
   * 都由 retry / wake 自己接回来，不再让人回到页面上先点一次按钮。
   */
  connect() {
    this.want = true
    this.retries = 0
    this.open()
  }

  private open() {
    clearTimeout(this.retryTimer)
    this.teardown()
    this.resetForNewSession()
    this.exited = false
    this.cb.onOverlay(null)
    this.cb.onStatus('连接中…', '')
    const ws = new WebSocket(ptyURL(this.term.cols, this.term.rows))
    ws.binaryType = 'arraybuffer'
    this.ws = ws

    ws.onmessage = (ev) => {
      if (this.ws !== ws) return // 已经被 teardown 换掉了，别再往终端里写
      // 有任何东西进来就说明这条连接是活的，探活的计时器可以撤了（不必等 `p` 那一帧）
      if (this.probeTimer !== undefined) {
        clearTimeout(this.probeTimer)
        this.probeTimer = undefined
      }
      if (typeof ev.data !== 'string') {
        this.term.write(new Uint8Array(ev.data as ArrayBuffer))
        this.armRepaint()
        // 等的就是这个：resize 之后 herdr 重画来了，现在才轮到撤冻帧（见 awaitRedraw）
        if (this.freezeAwait) {
          this.freezeAwait = false
          this.watchThaw()
        }
        return
      }
      const m = JSON.parse(ev.data) as { t: string; label?: string; msg?: string; code?: number }
      if (m.t === 'ready') {
        this.alive = true
        this.retries = 0
        this.cb.onStatus(`${m.label}  ${this.term.cols}×${this.term.rows}`, 'on')
        if (!matchMedia('(pointer: coarse)').matches) this.term.focus()
      } else if (m.t === 'p') {
        /* 探活的回音，上面已经把计时器撤了 */
      } else if (m.t === 'exit') {
        this.exited = true
        this.alive = false
        this.cb.onStatus(`已退出（code ${m.code}）`, 'err')
        this.cb.onOverlay('会话结束了。', '重新连接')
      } else if (m.t === 'fatal') {
        this.exited = true
        this.cb.onStatus('起不来', 'err')
        this.cb.onOverlay(m.msg ?? '未知错误', '重试')
      }
    }
    ws.onclose = () => {
      if (this.ws !== ws) return
      const hadReady = this.alive
      this.alive = false
      // exit / fatal 是「说明白了才断」的：那两条自己弹了遮罩，重连只会把话冲掉
      if (this.exited) return
      void this.dropped(ws, hadReady)
    }
    ws.onerror = () => {
      if (this.ws !== ws) return
      if (!this.alive) this.cb.onStatus('连不上', 'err')
    }
  }

  /* --------------------------------------------------------- 断了之后 */

  /**
   * 掉线了，决定是自己接回来还是把原因摊出来。
   *
   * **连上过**（收到过 ready）说明后端和凭据都没问题，断的是网络 / PTY —— 直接重连。
   * **压根没连上**得先问一次 `/api/state`：凭据被撤了这种情况重连一万次也没用，而重连
   * 的动静（每次 open 都 `term.reset()`）反而会把真正的原因刷掉。
   */
  private async dropped(ws: WebSocket, hadReady: boolean) {
    if (!hadReady) {
      const v = await this.diagnose()
      if (this.ws !== ws) return // 等这个 fetch 的工夫人已经手点过「连接」了
      if (!v.retry) {
        this.cb.onStatus('连不上', 'err')
        this.cb.onOverlay(v.msg, '重试')
        return
      }
    }
    this.retry()
  }

  /** 排下一次重连。退避是为了后端真挂了 / 真没网时别把手机的电烧在握手上。 */
  private retry() {
    if (!this.want || this.exited) return
    // 页面不可见时重连是白费：iOS 锁屏之后定时器基本不跑，就算真连上了系统也马上
    // 再把它掐掉（还白起一个登录 shell）。挂着不动，等 wake() —— 回到前台那一下才是
    // 真正该连的时刻，而且从最短那档重新开始。
    if (document.visibilityState !== 'visible' || navigator.onLine === false) {
      this.cb.onStatus('已断开', 'err')
      return
    }
    if (this.retries >= MAX_RETRY) {
      this.cb.onStatus('连不上', 'err')
      void this.diagnose().then((v) => this.cb.onOverlay(v.msg, '重试'))
      return
    }
    const wait = RETRY_MS[Math.min(this.retries, RETRY_MS.length - 1)]
    this.retries++
    this.cb.onStatus(`断了，重连中…（第 ${this.retries} 次）`, 'err')
    clearTimeout(this.retryTimer)
    this.retryTimer = setTimeout(() => this.open(), wait)
  }

  /**
   * 页面回到前台 / 网络回来了 —— 也就是「人回来了」。
   *
   * 三种情况：连接已经没了就立刻连（不等退避，从最短那档重新数）；正在连就让它连；
   * 看着还开着的**也要探一下**，因为锁屏挂起之后的 WebSocket 常常是僵的（readyState
   * 还是 OPEN、send 也不报错，但对面早没了）。浏览器里读不到协议层的 ping/pong（那是
   * UA 自己处理、不过 JS 的手），所以只能在应用层自己发一帧问一句。
   */
  private wake = () => {
    if (!this.want || this.exited) return
    if (document.visibilityState !== 'visible' || navigator.onLine === false) return
    const st = this.ws?.readyState
    if (st === WebSocket.CONNECTING) return
    if (st === WebSocket.OPEN) {
      this.probe()
      return
    }
    this.retries = 0
    this.open()
  }

  private probe() {
    const ws = this.ws
    if (!ws || ws.readyState !== WebSocket.OPEN || this.probeTimer !== undefined) return
    try {
      ws.send(JSON.stringify({ t: 'p' }))
    } catch {
      // send 都抛了，那这条连接肯定是废的
      this.retries = 0
      this.open()
      return
    }
    this.probeTimer = setTimeout(() => {
      this.probeTimer = undefined
      if (this.ws !== ws) return
      console.warn('[herdr-web] 连接没回音，当断了处理')
      this.retries = 0
      this.open() // open 里会先 teardown，僵着的那条连同它的 PTY 一起收掉
    }, PROBE_MS)
  }

  // 连不上的几种原因表现一样（WS 都是直接 close），用一次 HTTP 请求区分开。
  // retry 说这个原因值不值得再试：凭据 / 跨站是「再试一万次也一样」，那就别自动重连了。
  private async diagnose(): Promise<{ retry: boolean; msg: string }> {
    let r: Response
    try {
      r = await fetch('/api/state', { credentials: 'same-origin', headers: { 'x-herdr-web': '1' } })
    } catch {
      // 后端可能是正在重启（改完代码 make run 一下），值得再试
      return { retry: true, msg: '后端没在跑。到 herdr-web 目录里执行 <code>make run</code>（或 <code>./herdr-web</code>）。' }
    }
    if (r.status === 401) {
      // 配对页会被 App 顶上来（api.ts 在 401 上抛了事件），这里只把话说清楚
      return {
        retry: false,
        msg: '后端在跑，但<b>这台设备的凭据没了</b>（被撤销、或者浏览器数据被清过）。在跑 herdr-web 的机器上执行 <code>herdr-web pair</code> 重新配一次。',
      }
    }
    if (r.status === 403) {
      return { retry: false, msg: '后端在跑、凭据也对，但请求被当成跨站拒了。地址栏里的域名要和 <code>HERDR_WEB_HOSTNAME</code> 一致。' }
    }
    return { retry: true, msg: '后端在跑、凭据也对，但 WebSocket 建不起来。中间有反代 / frp 的话确认它转发了 Upgrade 头。' }
  }

  /* ------------------------------------------------------------- 尺寸 / 主题 */

  /**
   * 布局变了，**等它停下来**再动 xterm。
   *
   * 一次 resize 的代价不是一帧：清画布（见 freeze()）+ SIGWINCH + herdr 自己清屏重画，
   * 加起来几十到上百毫秒，屏幕上是实实在在闪一下。所以这里要的是**一次布局变化只
   * resize 一次**。
   *
   * 原来是「防抖 80ms」，而手机上呼一次输入法压根不是「一下」：视口是一格一格变过来的
   * （iOS 每帧一个 resize；安卓分几段，中间还夹着「顶栏收掉」和进全屏那两下）。事件之间
   * 一超过 80ms 就漏成两次、三次 —— 用户看到的就是「呼一次键盘重绘很多次」。而单纯把
   * 防抖调长又不行：面板开合那种一步到位的变化会跟着白等。
   *
   * 所以分档：`viewport = true` 的那几下（**视口自己在动**：呼输入法、转屏、进出全屏、
   * 地址栏收放）额外压一个 VV_HOLD 的地板 —— 那是一段动画，最后一下之后还得再等等；
   * 别的（面板开合、拖发件箱、改字号）照旧只等 SETTLE_MS。
   *
   * 代价是键盘升起的那 ~300–500ms 里终端还是老行数（底下几行被键盘盖着），换来的是只闪一次。
   * VV_CAP 是兜底：视口有可能一直在动（iOS 上地址栏能来回弹好几秒），不能因此永远不重排。
   */
  relayout(viewport = false) {
    const now = performance.now()
    if (this.settleTimer === undefined) {
      // 一串变化的头一下
      this.settleFloor = 0
      this.settleCap = 0
    }
    if (viewport) {
      this.settleFloor = Math.max(this.settleFloor, now + VV_HOLD)
      if (!this.settleCap) this.settleCap = now + VV_CAP
    }
    this.armSettle(now)
  }

  /**
   * 排下一次落地。每次布局变化都重新排，所以计时器真响的时候一定「已经静了
   * SETTLE_MS」—— 这就是防抖那一半。
   */
  private armSettle(now: number) {
    let due = Math.max(now + SETTLE_MS, this.settleFloor)
    if (this.settleCap) due = Math.min(due, this.settleCap)
    clearTimeout(this.settleTimer)
    this.settleTimer = setTimeout(() => this.settle(), Math.max(0, due - now))
  }

  private settle() {
    this.settleTimer = undefined
    const d = this.fit.proposeDimensions()
    // 容器还没测量出来（字号刚改完那一帧、面板动画中）就等下一次
    if (!d || !Number.isFinite(d.cols) || !Number.isFinite(d.rows)) return
    // 行列没变就别碰 xterm：resize 一次要黑一下（见 freeze()），白闪不值
    if (d.cols === this.term.cols && d.rows === this.term.rows) return
    this.applySize(d)
  }

  private applySize(d: { cols: number; rows: number }) {
    this.freeze()
    // 直接 term.resize，不走 fit.fit()：fit 会自己再量一次（刚量过），而且在 resize 之前
    // 多调一次 renderService.clear() —— 那正是「改尺寸会黑一下」里的第 2 条。少一次清屏。
    // xterm 那边 bufferService.onResize 自己会触发全屏重绘（读的 RenderService 源码）。
    this.term.resize(d.cols, d.rows)
    this.awaitRedraw()
    this.armRepaint() // resize 后那次全屏重绘可能被 2026 吞掉，让看门狗兜着
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify({ t: 'r', cols: this.term.cols, rows: this.term.rows }))
    }
  }

  /* ---------------------------------------------------------------- 冻帧 */
  // 改尺寸的那一刻屏幕是**真的黑掉**，不是错觉：
  //   1. xterm 的 WebGL 渲染器一改 `canvas.width` 绘制缓冲就清空了（实测清空后整块画布
  //      读出来是全透明的），终端内容整片消失，只剩底下那层背景 —— 就是那一下「黑」；
  //   2. （原来还多一次：`FitAddon.fit()` 在 resize 之前主动 `renderService.clear()`——
  //      所以现在不走 fit，自己量完直接 `term.resize`，见 applySize）；
  //   3. 重画最快也要等下一个 rAF，2026 同步输出正开着的话得等 ESU（上面的看门狗兜，但那是 180ms）；
  //   4. herdr 收到 SIGWINCH 之后自己也要清屏重画一遍，又是几十毫秒。
  // 这几段延迟一个都去不掉（xterm 没有同步重绘的口子），所以改尺寸之前把当前画面拍成
  // 一张图铺在终端上，等新画面画上了再淡出 —— 呼输入法时最明显的那一下全黑就没了。
  private freeze() {
    const now = performance.now()
    if (this.freezeEl) {
      // 键盘动画 / 拖窗口会连着重排好几次，接着用第一张图，只把撤帧时间往后推
      if (now - this.freezeAt < FREEZE_MAX) this.watchThaw()
      return
    }
    const el = this.snapFrame()
    if (!el) return // DOM 渲染兜底路径没有 canvas，那条路也不会黑
    this.freezeEl = el
    this.freezeAt = now
    this.host.appendChild(el)
    this.watchThaw()
  }

  /** 把 `.xterm-screen` 里那几层 canvas 合成一张盖在终端上的图 */
  private snapFrame(): HTMLCanvasElement | null {
    const screen = this.host.querySelector('.xterm-screen') as HTMLElement | null
    if (!screen) return null
    const layers = [...screen.querySelectorAll('canvas')] as HTMLCanvasElement[]
    if (!layers.length || !layers[0].width || !layers[0].height) return null

    const out = document.createElement('canvas')
    out.width = layers[0].width
    out.height = layers[0].height
    const ctx = out.getContext('2d')
    if (!ctx) return null
    // 底色自己铺一遍：这几层里空的地方可能是透明的，光 drawImage 会得到一张半透明的图
    ctx.fillStyle = (THEMES[this.scheme].background as string) || '#000'
    ctx.fillRect(0, 0, out.width, out.height)
    for (const c of layers) {
      if (c.width && c.height) ctx.drawImage(c, 0, 0, out.width, out.height)
    }
    // 读不出画面就别冻 —— WebGL 的绘制缓冲不是随时都读得到（浏览器不支持
    // preserveDrawingBuffer、或者标签页在后台从来没画过），拿一张空图糊上去比闪一下更糟。
    // 判据是「缩到 32×20 之后还有不止一种颜色」，全空的终端也算不值得冻。
    if (!hasContent(out)) return null

    // 按 .xterm-screen 现在（还没 resize）的位置和大小摆，容器缩小时多出来的部分让
    // host 的 overflow:hidden 裁掉。不设 z-index：靠 DOM 顺序压在 xterm 上面就行，
    // 设了会连「点连接」那个遮罩一起压住（host 不是 stacking context）。
    const hostBox = this.host.getBoundingClientRect()
    const box = screen.getBoundingClientRect()
    out.style.cssText =
      `position:absolute;pointer-events:none;` +
      `left:${box.left - hostBox.left}px;top:${box.top - hostBox.top}px;` +
      `width:${box.width}px;height:${box.height}px;` +
      `opacity:1;transition:opacity ${THAW_FADE}ms linear`
    return out
  }

  /**
   * 改完尺寸之后**别按 onRender 撤帧** —— 等 herdr 那边重画到了再说。
   *
   * xterm 自己因为 resize 画的那一帧是「旧内容按新宽度重排」，而 herdr 的 pane 是绝对
   * 定位重画的、压根不按行流重排，所以那一帧看着是花的。跟着它撤帧的话，屏幕上是
   * 「花一下 → herdr 的 SIGWINCH 重画又正一下」**两次**变化 —— 一次呼键盘就闪两回。
   *
   * 判据是「resize 之后收到过字节」：herdr 收到 SIGWINCH 一定会重画（几十到几百毫秒），
   * 那一帧才是该露出来的画面。**兜底照旧是 THAW_CAP**：对面也可能一个字节都不回
   * （比如底下就是一个闲着的 shell 提示符），不能糊着一张旧图不放。
   */
  private awaitRedraw() {
    if (!this.freezeEl) return
    this.thawWatch?.dispose()
    this.thawWatch = null
    this.freezeAwait = true
    this.armThaw(THAW_CAP)
  }

  /** 撤帧的时钟：等到新画面画上再多留 THAW_GRACE，等不到就 THAW_CAP 后硬撤 */
  private watchThaw() {
    this.thawWatch?.dispose()
    // 只认第一帧：herdr 那边有动画（spinner）的话 onRender 会一直来，跟着续就永远撤不掉了
    this.thawWatch = this.term.onRender(() => {
      this.thawWatch?.dispose()
      this.thawWatch = null
      this.armThaw(THAW_GRACE)
    })
    this.armThaw(THAW_CAP)
  }

  private armThaw(ms: number) {
    clearTimeout(this.thawTimer)
    this.thawTimer = setTimeout(() => this.thaw(), ms)
  }

  private thaw(now = false) {
    clearTimeout(this.thawTimer)
    this.freezeAwait = false
    this.thawWatch?.dispose()
    this.thawWatch = null
    const el = this.freezeEl
    if (!el) return
    this.freezeEl = null
    if (now) {
      el.remove()
      this.dropFading()
      return
    }
    // 淡出期间这张图不再归 freezeEl 管，得单独记着：淡出还没完就又来一次重排的话
    // （拖发件箱、连着呼收键盘）它会一直挂在 DOM 里，一次重排漏一个。
    this.dropFading()
    this.fadingEl = el
    el.style.opacity = '0'
    this.fadeTimer = setTimeout(() => this.dropFading(), THAW_FADE)
  }

  private dropFading() {
    clearTimeout(this.fadeTimer)
    this.fadingEl?.remove()
    this.fadingEl = null
  }

  setScheme(s: Scheme) {
    this.scheme = s
    this.term.options.theme = THEMES[s]
    if (this.caps.has('DEC 2031')) this.sendScheme() // 程序订阅过就通知它重绘
  }

  setFontSize(n: number) {
    const v = Math.max(7, Math.min(28, n))
    if (v !== this.term.options.fontSize) {
      this.freeze() // 换字号会重建字形图集、顺带清画布，跟 resize 一样会黑一下
      this.term.options.fontSize = v
      this.relayout()
    }
    return v
  }

  /**
   * 把一段文本当成「粘贴」送进终端。
   *
   * 走 `term.paste` 而不是自己 `send()`：它会按 pane 当前的**括号粘贴模式**（DEC 2004）
   * 编码，agent 那边才知道这是一整段粘贴而不是一个字一个字敲的 —— 差别是多行文本会不会
   * 被当成「每行一次回车」。
   *
   * 不顺手 focus 终端：手机上那一下会把系统键盘顶出来，而粘完多半是要看一眼再决定。
   */
  paste(text: string) {
    if (text) this.term.paste(text)
  }

  focus() {
    this.term.focus()
  }

  dispose() {
    this.detachTouch?.()
    this.want = false // 卸了还重连的话，下一个 Session 建起来时会有两条连接
    clearTimeout(this.retryTimer)
    document.removeEventListener('visibilitychange', this.wake)
    removeEventListener('online', this.wake)
    removeEventListener('pageshow', this.wake)
    clearTimeout(this.paintTimer)
    clearTimeout(this.settleTimer)
    this.thaw(true)
    this.dropFading()
    this.teardown()
    this.term.dispose()
  }
}

/** 缩略图里颜色多于一种 = 这张快照真拍到了东西 */
const hasContent = (src: HTMLCanvasElement) => {
  const probe = document.createElement('canvas')
  probe.width = 32
  probe.height = 20
  const ctx = probe.getContext('2d', { willReadFrequently: true })
  if (!ctx) return false
  ctx.drawImage(src, 0, 0, probe.width, probe.height)
  const d = ctx.getImageData(0, 0, probe.width, probe.height).data
  for (let i = 4; i < d.length; i += 4) {
    if (d[i] !== d[0] || d[i + 1] !== d[1] || d[i + 2] !== d[2]) return true
  }
  return false
}

const toRgbSpec = (hex: string) => {
  const h = hex.replace('#', '')
  const p = (i: number) => (parseInt(h.slice(i, i + 2), 16) * 257).toString(16).padStart(4, '0')
  return `rgb:${p(0)}/${p(2)}/${p(4)}`
}
