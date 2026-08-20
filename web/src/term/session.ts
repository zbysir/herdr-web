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
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebglAddon } from '@xterm/addon-webgl'
import { WebLinksAddon } from '@xterm/addon-web-links'
import { Unicode11Addon } from '@xterm/addon-unicode11'
import { ClipboardAddon } from '@xterm/addon-clipboard'
import { TOKEN } from '@/lib/api'
import { THEMES, type Scheme } from './themes'
import { attachTouch } from './touch'

export interface Cap { key: string; label: string; ok: boolean }

export interface SessionCallbacks {
  onStatus: (text: string, cls: 'on' | 'err' | '') => void
  onCaps: (caps: Cap[]) => void
  onOverlay: (msg: string | null, btn?: string) => void
  onHeal: (n: number) => void
  onKeyboardChange: (up: boolean) => void
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
  private paintTimer: ReturnType<typeof setTimeout> | undefined
  private paintHeals = 0
  private resizeTimer: ReturnType<typeof setTimeout> | undefined
  private detachTouch: (() => void) | null = null

  /** 面板里的开关，App 直接改 */
  opts = { kitty: true, meta: true, copyOnSelect: false, sync2026: true }

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
      linkHandler: { activate: (_e, uri) => window.open(uri, '_blank', 'noopener') },
    })

    this.term.loadAddon(this.fit)
    this.term.loadAddon(new WebLinksAddon((_e, uri) => window.open(uri, '_blank', 'noopener')))
    this.term.loadAddon(new Unicode11Addon())
    this.term.unicode.activeVersion = '11'
    this.term.loadAddon(new ClipboardAddon()) // OSC 52
    this.term.open(host)
    try {
      const webgl = new WebglAddon()
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
      if (this.opts.copyOnSelect && this.term.hasSelection()) this.copy(this.term.getSelection())
    })

    host.addEventListener('contextmenu', (e) => e.preventDefault())
    this.detachTouch = attachTouch(host, this.term, {
      send: (d) => this.send(d),
      toggleKeyboard: () => this.toggleKeyboard(),
    })

    for (const ev of ['focus', 'blur']) {
      this.kbdEl()?.addEventListener(ev, () => this.cb.onKeyboardChange(this.keyboardUp()))
    }
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
    this.cb.onCaps([...this.caps.values()])
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
   * 软键条 / 发件箱转发过来的现成字节。
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
          this.copy(this.term.getSelection())
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
        this.copy(this.term.getSelection())
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

  copy(text: string) {
    if (!text) return
    ;(navigator.clipboard?.writeText(text) ?? Promise.reject()).catch(() => {
      // http 的局域网地址不是安全上下文，navigator.clipboard 不可用，退回老办法
      const ta = document.createElement('textarea')
      ta.value = text
      ta.style.position = 'fixed'
      ta.style.opacity = '0'
      document.body.appendChild(ta)
      ta.select()
      document.execCommand('copy')
      ta.remove()
      this.term.focus()
    })
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

  connect() {
    this.exited = false
    this.cb.onOverlay(null)
    this.cb.onStatus('连接中…', '')
    const q = new URLSearchParams({
      token: TOKEN,
      cols: String(this.term.cols),
      rows: String(this.term.rows),
    })
    const proto = location.protocol === 'https:' ? 'wss' : 'ws'
    const ws = new WebSocket(`${proto}://${location.host}/pty?${q}`)
    ws.binaryType = 'arraybuffer'
    this.ws = ws

    ws.onmessage = (ev) => {
      if (typeof ev.data !== 'string') {
        this.term.write(new Uint8Array(ev.data as ArrayBuffer))
        this.armRepaint()
        return
      }
      const m = JSON.parse(ev.data) as { t: string; label?: string; msg?: string; code?: number }
      if (m.t === 'ready') {
        this.alive = true
        this.cb.onStatus(`${m.label}  ${this.term.cols}×${this.term.rows}`, 'on')
        if (!matchMedia('(pointer: coarse)').matches) this.term.focus()
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
      this.alive = false
      if (this.exited) return
      this.cb.onStatus('已断开', 'err')
      void this.diagnose()
    }
    ws.onerror = () => {
      if (!this.alive) this.cb.onStatus('连不上', 'err')
    }
  }

  // 连不上的两种原因表现一样（WS 都是直接 close），用一次 HTTP 请求区分开
  private async diagnose() {
    let r: Response
    try {
      r = await fetch(`/api/state?token=${encodeURIComponent(TOKEN)}`)
    } catch {
      this.cb.onOverlay('后端没在跑。到 herdr-web 目录里执行 <code>make run</code>（或 <code>./herdr-web</code>）。', '重试')
      return
    }
    if (r.status === 401) {
      this.cb.onOverlay(
        '后端在跑，但地址栏里的 <b>token 不对</b>。<br>换成 server 启动时打印的那个链接 —— token 存在 <code>~/.herdr-web/token</code>，重启也不会变。',
        '重试',
      )
      return
    }
    this.cb.onOverlay('后端在跑、token 也对，但 WebSocket 建不起来。中间有反代的话确认它转发了 Upgrade 头。', '重试')
  }

  /* ------------------------------------------------------------- 尺寸 / 主题 */

  relayout() {
    clearTimeout(this.resizeTimer)
    this.resizeTimer = setTimeout(() => {
      try {
        this.fit.fit()
      } catch {
        /* 容器还没测量出来 */
      }
      if (this.ws?.readyState === WebSocket.OPEN) {
        this.ws.send(JSON.stringify({ t: 'r', cols: this.term.cols, rows: this.term.rows }))
      }
    }, 80)
  }

  setScheme(s: Scheme) {
    this.scheme = s
    this.term.options.theme = THEMES[s]
    if (this.caps.has('DEC 2031')) this.sendScheme() // 程序订阅过就通知它重绘
  }

  setFontSize(n: number) {
    this.term.options.fontSize = Math.max(7, Math.min(28, n))
    this.relayout()
    return this.term.options.fontSize!
  }

  focus() {
    this.term.focus()
  }

  dispose() {
    this.detachTouch?.()
    clearTimeout(this.paintTimer)
    clearTimeout(this.resizeTimer)
    this.ws?.close()
    this.term.dispose()
  }
}

const toRgbSpec = (hex: string) => {
  const h = hex.replace('#', '')
  const p = (i: number) => (parseInt(h.slice(i, i + 2), 16) * 257).toString(16).padStart(4, '0')
  return `rgb:${p(0)}/${p(2)}/${p(4)}`
}
