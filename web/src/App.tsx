import { useCallback, useEffect, useRef, useState } from 'react'
import { Maximize, Minimize } from './icons'
import { api, deviceKind, filesApi, libMap, resolveBar, resolvePad, SESSION, topbarKeyRef, UNAUTHED, type ClipResult, type FileStat, type Notice, type ProfilesResponse, type ResolvedPad, type SoftKey, type SoftkeysConfig, type SoftkeysResponse, type State, type TopbarResponse, type UnauthedDetail, type WhoAmI } from '@/lib/api'
import { applyPrefs, pushPref } from '@/lib/prefs'
import { readClipboard, writeClipboard } from '@/lib/clipboard'
import { Session } from '@/term/session'
import { initialScheme, type Scheme } from '@/term/themes'
import { useViewportHeight } from '@/hooks/useViewportHeight'
import { useCompose } from '@/hooks/useCompose'
import { useNotices } from '@/hooks/useNotices'
import { useLanDirect } from '@/hooks/useLanDirect'
import { away, onNotifyClick, showNotify } from '@/lib/notify'
import { usePhone } from '@/hooks/usePhone'
import { useKeyboardUp } from '@/hooks/useKeyboardUp'
import { useArm } from '@/hooks/useArm'
import { spanStyle } from '@/lib/keys'
import { Button } from '@/components/ui/button'
import { Toast } from '@/components/ui/toast'
import { Dock } from '@/components/Dock'
import { Softkeys } from '@/components/Softkeys'
import { Compose } from '@/components/Compose'
import { SettingsPanel, type SettingsTab, type TermOpts } from '@/components/SettingsPanel'
import { PaneSwitcher, paneZoomPref } from '@/components/PaneSwitcher'
import { CAP_BY_ID, TOPBAR_DEFAULT, type CapId, type PanelId } from '@/capabilities'
import { AUTO_MS_DEFAULT, Notices } from '@/components/Notices'
import { FilesPanel } from '@/components/FilesPanel'
import { FileViewer } from '@/components/FileViewer'
import { Pairing } from '@/components/Pairing'
import { CopyPrompt } from '@/components/CopyPrompt'
import { LanPrompt } from '@/components/LanPrompt'
import { PastePrompt } from '@/components/PastePrompt'
import { Logo } from '@/components/Logo'
import { KeyGroupPopup } from '@/components/KeyGroupPopup'
import { cn } from '@/lib/utils'

/** Safari 的私有全屏 API。lib.dom 里没有这几个，本地补上，省得到处 as any */
type FsDoc = Document & {
  webkitFullscreenElement?: Element | null
  webkitExitFullscreen?: () => Promise<void> | void
}
type FsEl = HTMLElement & { webkitRequestFullscreen?: () => Promise<void> | void }

/**
 * 把焦点从输入框上摘掉 —— 手机上这就是「收起系统键盘」（Web 上没有别的办法，没有一个
 * 「收键盘」的 API，只能靠 blur）。
 *
 * 焦点不在输入框上时什么都不做：按钮的焦点也一起摘掉的话，键盘用户会丢掉焦点环。
 */
const blurInput = () => {
  const el = document.activeElement
  if (!(el instanceof HTMLElement)) return
  if (el.tagName === 'TEXTAREA' || el.tagName === 'INPUT' || el.isContentEditable) el.blur()
}

/**
 * 这台机器有实体指针吗（鼠标 / 触控板）。
 *
 * 「要不要顺手把焦点还给终端」得按这个判，**不能按屏幕宽度**：原来用的是 `phone`
 * （< 440px），可平板和横屏的手机都比 440 宽 —— 那些机器上 focus 终端就等于把系统输入法
 * 顶出来。用户本来把键盘收着，换个 pane、点一下字号，键盘自己冒出来，这是错的（用户报的）。
 * 有鼠标的机器上聚焦是白送的方便，没有代价。
 */
const finePointer = () => !matchMedia('(pointer: coarse)').matches

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
    // 局域网直连的交接令牌只活 60 秒、只能用一次。走到这儿基本只有两种情况：
    // 页面在后台压了一会儿才跳过来，或者这条链接被复制到了别处。刷新公网那个地址重来一次就好。
    case 'handoff':
      return '局域网直连的交接令牌过期了（只有 60 秒）。回到公网那个地址刷一下，它会重新交接一次。'
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
    // 地址栏里点名了 session 就把那条命令写全 —— 这一屏是「这个页面会干什么」的唯一说明。
    // SESSION 已经过白名单（只有字母数字和 ._-），拼进 innerHTML 是安全的。
    msg: SESSION
      ? `点「连接」开一个本机登录 shell，然后敲 <code>herdr --session ${SESSION}</code>。`
      : '点「连接」开一个本机登录 shell，然后敲 <code>herdr</code>。',
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
    // 这五个**整组**跟着 profile 走（见 lib/prefs.ts）。前三个原来压根没落盘 ——
    // 取消勾选、刷新一次又回来了，那是个 bug，不是「只在这次会话里生效」的设计。
    kitty: lsBool('kitty', true), meta: lsBool('meta', true), copyOnSelect: lsBool('copyOnSelect', false),
    sync2026: lsBool('sync2026', true),
    // 默认开：只有窄屏（herdr 换成移动端布局）才有那个 switch 按钮，也就是只有手机 /
    // 平板上会碰到 —— 而在那儿 herdr 自己那张面板正是最难用的一处（多 pane 平铺读不了）。
    // 宽屏上根本没有那个按钮，这条开着也不改变任何行为。
    switchPanel: lsBool('switchPanel', true),
  })

  /**
   * 顶栏点出来的那几块面板：**同一时刻只开一块**。
   *
   * 为什么是一个「当前是哪一块」而不是三个布尔量：三块都浮在同一个角上（现在还贴着顶栏、
   * 更高了），两块同时开就是互相遮盖。用一个字段的话，互斥是「**存不下**第二块」——
   * 而三个布尔量要靠每个入口都记得把另外两个关掉，漏一个就是一个只在某条路径上出现的
   * 遮挡 bug（而且入口不止顶栏：软键条的 act:、herdr 的 switch、提示卡的「更多」、
   * 终端里点一条路径都能开）。
   *
   * 面板一览（panes）在手机上是换 pane 唯一走得通的路，见 PaneSwitcher。
   */
  // 哪块浮层开着。类型从那份清单推（`PanelId`）—— 以前这儿手写第三份枚举
  const [panel, setPanel] = useState<PanelId | null>(null)
  const settings = panel === 'settings'
  const panesOpen = panel === 'panes'
  const filesOpen = panel === 'files'
  /**
   * 文件浏览的两块东西：
   *   panel === 'files'  兜底的目录浏览面板（filesAt 是「打开时定位到哪儿」）
   *   viewing            查看器（图 / 文本）。**主入口是终端里点一条路径**，那时候面板根本不开。
   *
   * viewing **不进上面那个互斥**：它是刻意压在文件面板上面的一层 —— 从面板里点开一张图，
   * 退出来还该回到那个目录（见下面渲染那儿的注释）。
   *
   * viewing 存的是**已经 stat 过的结果**，不是一条待解析的路径：是文件还是目录得在
   * 开弹窗之前就知道（见 openPath），不然点一个目录会先摊一句「这是个目录」再让人
   * 多点一下 —— 点路径的人要的就是「打开它」。
   */
  const [filesAt, setFilesAt] = useState<string | undefined>(undefined)
  const [viewing, setViewing] = useState<FileStat | null>(null)
  // 记住上次看的那一页
  const [tab, setTab] = useState<SettingsTab>('term')
  const [showCompose, setShowCompose] = useState(() => lsBool('compose', true))
  const [showKeys, setShowKeys] = useState(() =>
    lsBool('softkeys', matchMedia('(pointer: coarse)').matches),
  )
  const [live, setLive] = useState(() => lsBool('composeLive', false))
  /**
   * 面板图标上那个角标（还有几条没看）画不画。**只管角标，不管弹窗** —— 有人嫌它一直挂着扎眼，
   * 而提示卡是自己会走的，两件事分开。整套提示要关在服务端（`HERDR_WEB_NOTICE_MS=0`）。
   *
   * 跟着**这套排布**走（见 lib/prefs.ts）：这是「这类设备上看着舒服不舒服」的偏好，不是部署
   * 的策略。localStorage 里那份是镜像，服务端为准。
   */
  const [noticeDot, setNoticeDot] = useState(() => lsBool('noticeDot', true))
  /**
   * 系统通知开没开（浏览器的通知，锁屏 / 切到别的 app 也看得见）。
   *
   * 两个东西都要对：本地这个开关 + 浏览器的权限。开关记在 localStorage，权限归浏览器管 ——
   * 用户可能在浏览器设置里把权限撤掉，所以每次开面板都重新问一次真实权限（见 SettingsPanel）。
   */
  const [noticeOS, setNoticeOS] = useState(() => lsBool('noticeOS', false))
  /** 系统通知：**人正看着这一页时也弹**。默认关（那时候右上角那张卡已经在说了） */
  const [noticeOSFg, setNoticeOSFg] = useState(() => lsBool('noticeOSFg', false))
  /**
   * 呼出键盘就自动全屏（**收起键盘不退出**）。**默认开。**
   *
   * 手机上打字那一下最缺高度：键盘吃掉半屏，地址栏和工具条又占一截，剩下的终端只有
   * 三五行。全屏能把后者要回来。**收键盘不退出**是刻意的 —— 每打一次字闪进闪出一次
   * 全屏，比不全屏还难受；退出全屏用顶栏那个按钮，一次的事。
   *
   * 默认开的代价是「浏览器不给全屏」的人会白挨一次提示，所以那条提示**一台设备只说一次**
   * （见 fullWarned）：默认开的功能反复吐 toast 就成了噪音，而一次都不说又会变成
   * 「这开关点了没反应」那种查不出来的毛病。桌面上没有软键盘，这个开关等于不存在。
   */
  const [kbdFull, setKbdFull] = useState(() => lsBool('kbdFull', true))
  /** 「跑完了」那种卡片挂多久（ms）；0 = 一直挂着。「等你回答」的永远挂着，不受这个管 */
  const [noticeMs, setNoticeMs] = useState(
    () => Number(localStorage.getItem('noticeCardMs') ?? AUTO_MS_DEFAULT) || 0,
  )
  // 软键条每行的按键（已按 id 解析好）。几行、哪个键在哪一行都是服务端存的配置，
  // 编辑器存完把整份配置回传过来
  const [bar, setBar] = useState<SoftKey[][]>([])
  /**
   * 「我的按键」按 ID 索引。软键条的 `bar` 已经解析成定义了，这一份是给**顶栏**用的 ——
   * 顶栏上放的是 `key:<定义ID>`（见 internal/topbar），渲染时才落到定义上。
   *
   * 为什么不在服务端就解析好：定义是全局的、顶栏配置是分套的，服务端那边一解析就等于把
   * 两个口的数据焊在一起，而「读盘不核引用」那条规矩正是靠它们分开才成立的。
   */
  const [keyLib, setKeyLib] = useState<Map<string, SoftKey>>(new Map())
  /**
   * 固定块：钉在软键条一端、**不跟着横滑**的一小片对齐网格（方向键那种，见 lib/api.ts
   * 的 `Pad`）。null = 这一套没配。
   */
  const [pad, setPad] = useState<ResolvedPad | null>(null)
  /**
   * 顶栏上那排按钮：**放哪几个、什么顺序**也是服务端存的配置（`topbar.json`，见
   * internal/topbar），在设置 →「顶栏」页里拖。这里存的就是那一串 id。
   *
   * 一项可能是内置按钮的 id（`CAP_BY_ID` 里那些），也可能是 `key:<定义ID>` ——
   * 「我的按键」里的一个键放到了顶栏上。所以这儿是 `string[]` 不是 `CapId[]`。
   *
   * 初值用前端那份出厂顺序，别先渲染一条空栏 —— 请求回来之前那一两拍顶栏是空的话，
   * 看着就像坏了（而且「设置」那个入口也不在，连改都没法改）。
   */
  const [topbar, setTopbar] = useState<string[]>(TOPBAR_DEFAULT)
  /**
   * 这台设备用哪一套排布（profile，见 internal/profiles）。
   *
   * 初值就是「默认」那一套，不等服务端：软键条 / 顶栏的 GET 服务端自己会按绑定算，这里这份
   * 只用来显示名字和往 prefs 上写 —— 名字先写「默认」，报到回来再纠正，比先画一片空白好。
   */
  const [profile, setProfile] = useState({ id: 'default', name: '默认' })
  const [cfg, setCfg] = useState({ poll: urlNum('poll') ?? 500, push: urlNum('push') ?? 700 })
  const [state, setState] = useState<State | null>(null)

  // 手机竖屏那一档：顶栏收成一行，键盘弹起时干脆整条收掉（见下面的 barHidden）
  const phone = usePhone()
  const kbRoom = useKeyboardUp()
  const [peek, setPeek] = useState(false)

  // 剪贴板写不进去时手里那段文字（手机上没有用户手势那一档，见 lib/clipboard.ts）。
  // null = 没有待复制的东西。
  const [pendingCopy, setPendingCopy] = useState<string | null>(null)

  // 「长按这儿粘贴」开着没有：手机剪贴板读不到时的那条路
  const [pasteOpen, setPasteOpen] = useState(false)

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

  /**
   * 提示：哪个 agent 等你回答了 / 跑完了（右上角弹一下 + ▦ 上挂个未读数）。
   *
   * 间隔是服务端下发的（`HERDR_WEB_NOTICE_MS`，0 = 这个部署关了提示）。state 还没拉回来
   * 之前是 0，也就是不轮询 —— 差的那一两拍无所谓，而默认值写在前端就成了第二个真相源。
   */
  /* 局域网直连：探得通就换到直连那个 origin 上去（见 hooks/useLanDirect.ts）。
     等 gate === 'ok' 才开始 —— 配对页上跳走没有意义，凭据还没有。 */
  const lanDirect = useLanDirect(state?.lan, gate === 'ok')

  const notices = useNotices(
    state?.notice?.pollMs ?? 0,
    gate === 'ok',
    useCallback((n: Notice) => {
      // **只在页面看不见的时候弹系统通知**：你正看着这一页时右上角那张卡已经把话说完了，
      // 再弹一条系统通知就是同一件事说两遍（而且 macOS 上还会盖住页面右上角那张卡）。
      // 读 localStorage 而不是闭包里那几个 state：这个回调进了 hook 的 ref，拿闭包值的话
      // 开关刚改完的那几拍还是旧的。
      if (!lsBool('noticeOS', false)) return
      // **判据是「人在不在看这一页」，不是 document.hidden**：macOS 上切到别的 app 时
      // Chrome 只是失焦，hidden 一直是 false —— 只看 hidden 的话最主要的场景一条都不弹。
      if (!away() && !lsBool('noticeOSFg', false)) return
      void showNotify({
        title: `${n.status === 'blocked' ? '等你回答' : '跑完了'} · ${n.title || n.pane}`,
        // 通知里放不下长文，系统自己也会截；这儿先收一刀，别把整段塞给它
        body: n.text.slice(0, 200) || `${n.agent || 'agent'} · ${n.pane}`,
        tag: n.term, // 同一个 agent 的新提示替换旧那条，别在通知中心堆成一摞
        pane: n.pane,
      })
    }, []),
  )

  // 点了系统通知：service worker 把页面拉到前台，然后告诉我们跳去哪个 pane
  useEffect(() => onNotifyClick((pane) => { void gotoPane(pane, paneZoomPref()) }), [])

  /* --------------------------------------------------------- 文件 */

  /**
   * 焦点 pane 的 cwd，**放 ref 里**。
   *
   * 终端那层的回调是建 Session 的时候捕获的（那个 effect 只依赖 gate），闭包里的值
   * 会永远停在建立那一刻 —— 直接用 state 的话，点相对路径解出来的永远是第一次连上时
   * 那个 pane 的目录，而屏幕上一点异常都看不出来。
   */
  const cwdRef = useRef('')
  useEffect(() => {
    // 认**焦点** pane 而不是发件箱瞄准的那个：屏幕上那行字是焦点 pane 打出来的，
    // 而发件箱可能被钉在别的 pane 上（框里一有草稿就锁定，见 useCompose）。
    cwdRef.current = compose.panes.find((p) => p.focused)?.cwd ?? ''
  }, [compose.panes])


  /**
   * 把一个路径投出去。和「传图」是**同一个模型**：herdr 的 socket 里没有文件通道，
   * 能投的只有文本，agent 自己去读磁盘。所以传图是「路径进去」，文件浏览是「路径出来」。
   *
   * 带空格的路径加一层单引号 —— 这段字符串下一步可能被敲进 shell 的输入行，
   * 不加的话 `~/My Files/a.png` 会被当成两个参数。
   */
  const sendPath = useCallback((p: string) => {
    const chunk = (/[\s]/.test(p) && !p.includes("'") ? `'${p}'` : p) + ' '
    if (showCompose) {
      compose.append(chunk)
      toast('路径已插入发件箱')
    } else {
      sess.current?.send(chunk)
      toast('路径已敲进终端')
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [showCompose, toast, compose.append])

  /**
   * 开文件浏览面板。at 给了就直接定位到那个目录（点到一个目录、查看器的「所在目录」
   * 都走这条）。
   *
   * 每次都刷一遍 pane 列表：起点列表里最靠前的就是各 pane 的 cwd，而 pane 的增删和
   * 切目录随时在发生，这个面板又多半是隔了好一会儿才再打开一次（和 openPanes 一个道理）。
   */
  const openFiles = useCallback((at?: string) => {
    blurInput() // 和 openPanes 同理：键盘占着半个屏时，点面板里第一下只会收键盘
    setFilesAt(at)
    setPanel('files')
    void compose.loadPanes(true)
  }, [compose.loadPanes])

  /**
   * 打开一条路径 —— 终端里点的、面板里点的，都走这儿。
   *
   * 给的是**屏幕上的原样**（可能是相对的、可能带 `~`），解析交给服务端
   * （files.Resolve），基准是那个 pane 的 cwd。
   *
   * **先 stat 再决定开哪个**：是目录就直接进文件浏览。以前是把路径丢给查看器、让它
   * 自己 stat 完再摊一句「这是个目录」+ 一个按钮 —— 点路径的人要的就是「打开它」，
   * 是文件还是目录该由我们看出来，不该让人多点一下。
   *
   * 顺带也不用在这儿判「是不是图」：那得读文件才知道（服务端按魔数认，不认扩展名），
   * stat 的响应里已经带上了。
   *
   * 失败只出一条 toast，不弹壳：这条路最常见的失败就是终端里那行路径被折断 / 被 `…`
   * 截断了，那时候摊一个空弹窗还得再关一次。
   */
  const openPath = useCallback(async (raw: string) => {
    blurInput() // 终端里点路径那条路（触屏那层不让浏览器改焦点，键盘会一直挂着）
    try {
      const s = await filesApi.stat(raw, cwdRef.current || undefined)
      if (s.info.dir) openFiles(s.info.path)
      else setViewing(s)
    } catch (e) {
      toast((e as Error).message)
    }
  }, [openFiles, toast])

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
        onKeyboardChange: (up) => {
          setKbdUp(up)
          /*
           * **⌨ 那个键唤起的键盘也算「呼出键盘」。** 这一条是漏掉过的：我只接了输入框的
           * pointerdown。（那时候终端上还有「双击呼出键盘」，也是从这儿接的；双击后来
           * 去掉了 —— 见 term/touch.ts 开头。）
           *
           * 挂在这儿是因为 `toggleKeyboard()` 会**同步**回调过来，那一下还在按键的
           * click 里 —— 手势还没过期，requestFullscreen 才给过（挂在「视口变矮」上
           * 就是因为脱离了手势才一直失败）。
           *
           * 读 localStorage 而不是闭包里的 kbdFull：这个回调是建 Session 时传进去的，
           * 只在 gate 变化时重建，闭包里那份很快就是旧的。
           */
          if (up && lsBool('kbdFull', true)) enterFull(true)
        },
        onCopyBlocked: setPendingCopy,
        onPath: (raw) => void openPath(raw),
        onSwitchPanel: () => openPanesRef.current(),
      },
      scheme,
      fontSize,
    )
    s.onSticky(setSticky)
    sess.current = s
    setReady(true)
    // connect() 里记的那笔：这个标签页之前是连着的，只是页面被系统丢掉重载了
    try {
      if (sessionStorage.getItem('connected')) {
        setOverlay(null)
        s.connect()
      }
    } catch {
      /* 读不到就当没连过 */
    }
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
    // 这里**不写** localStorage：这个 effect 挂载时也跑一遍，那一下会拿本地默认值盖掉
    // 服务端刚要下发的那份（报到的响应还在路上）—— 表现就是「这两个开关换台设备就忘」。
    // 落盘 + 推服务端都在 setOpt 里，那儿只在人真的点了的时候跑。
  }, [opts, ready])

  // 跟随系统明暗 —— **但自己点过一次就钉住**（那一下进了 profile，见 lib/prefs.ts）。
  // 不钉的话：系统一切换就把 profile 里存的值冲掉，下次报到又把它读回来，两边来回打。
  useEffect(() => {
    const mq = matchMedia('(prefers-color-scheme: light)')
    const f = (e: MediaQueryListEvent) => {
      if (localStorage.getItem('scheme')) return
      setScheme(e.matches ? 'light' : 'dark')
    }
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
      // 查看器盖在最上面，**必须第一个判**。
      //
      // 以前它自己挂了一个捕获阶段的监听，结果要按两下：同一个 target 上的捕获监听
      // 按注册顺序跑，而这一条是挂载时就注册的、永远在前面 —— 从文件浏览面板里点开一张
      // 图时，第一下 Esc 被下面那块（filesOpen）吃掉了，第二下才轮到图。所有浮层的
      // Esc 都收在这儿按「谁在上面谁先关」排，比每层各挂一个监听靠顺序碰运气可靠。
      if (viewing) {
        setViewing(null)
        e.preventDefault()
        e.stopPropagation()
        return
      }
      if (panel) {
        setPanel(null)
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
  }, [panel, viewing])

  // 布局变化（软键条 / 发件箱开合、顶栏收放）都要重排终端
  useEffect(() => { sess.current?.relayout() }, [showCompose, showKeys, peek])

  /**
   * 手机上**键盘一弹起来就把顶栏收掉**，只留一条能点开的缝。
   *
   * 那一刻屏幕上只剩 ~430px 高，而顶栏（45px）里的东西那时候一个都用不上 ——
   * 正在打字的人要的是软键条和发件箱，「连接」是连之前的事。
   *
   * 收起是临时的：手动点开（peek）只管这一次，键盘一收就自动恢复常态，不留状态 ——
   * 否则用户会记不住自己上次是展开还是收起的，下次打字时顶栏在不在全靠碰。
   */
  // 两个信号都要：kbdUp 是「对着终端打字」，kbRoom 是「视口被键盘压掉了」——
  // 在发件箱里口述时只有后者认得出来（焦点不在终端上）
  const typing = kbdUp || kbRoom
  useEffect(() => { if (!typing) setPeek(false) }, [typing])
  const barHidden = phone && typing && !peek

  /**
   * **系统把输入法收掉时，顺手把焦点也摘掉。**
   *
   * Android 上用返回键 / 系统手势收键盘**不会 blur 页面元素** —— 终端那个隐藏输入框还聚着
   * 焦，于是 `kbdUp` 一直是 true：顶栏一直收成那条缝（键盘明明已经没了），而且再点任何一个
   * 「不改焦点」的按钮，Chromium 都会把 IME 重新弹出来（处理完 tap 手势之后无条件
   * `ShowVirtualKeyboard()`，只看此刻聚焦的元素可不可编辑，`preventDefault` 拦不住）。
   * 焦点一摘，这两件事同时消失：状态回到「没在打字」，而且没有可编辑元素可弹。
   *
   * 判据是「视口刚才被压过、这会儿长回去了」，而且**只在这个浏览器确实会压视口时才认**
   * （`sawRoom`）：有的浏览器键盘弹出时页面高度纹丝不动（见 README「手机」那节），那儿
   * `kbRoom` 恒 false，拿它当判据会把正在打字的人的焦点摘掉。
   */
  const sawRoom = useRef(false)
  useEffect(() => {
    if (kbRoom) { sawRoom.current = true; return }
    if (sawRoom.current) blurInput()
  }, [kbRoom])

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

  /**
   * 拉这台设备该用的那一套排布（软键条 + 顶栏）。
   *
   * 不带 `?profile=`：**哪一套由服务端按这台设备的绑定算**，前端这份 profile.id 只是显示用。
   * 换一套之后也走这儿 —— 绑定在服务端已经改了，这一趟拿回来的就是新那一套。
   */
  /**
   * 一份软键条配置铺到界面上。**读回来和编辑器存完都走这一条** —— 两处各摊一遍字段的话，
   * 加一个字段（rows / pad 都是这么来的）就会漏掉一处，表现是「存完没变，刷新才出来」。
   */
  const applySoftkeys = useCallback((c: SoftkeysConfig) => {
    setBar(resolveBar(c.lib, c.bar))
    setKeyLib(libMap(c.lib))
    setPad(resolvePad(c.lib, c.pad, c.rows))
  }, [])

  const loadLayout = useCallback(async () => {
    try {
      applySoftkeys(await api.get<SoftkeysResponse>('/softkeys'))
    } catch { /* 软键条拿不到就先空着，面板里还能改 */ }
    try {
      const tb = await api.get<TopbarResponse>('/topbar')
      // 认不出的直接跳过（服务端不该给，防一手 —— 新版本存的配置在旧前端上读到过）。
      // `key:` 引用只查形状：那个定义在不在，渲染时拿 keyLib 查（服务端读盘也不核，
      // 见 internal/topbar 的包注释）
      setTopbar(tb.items.filter((id) => CAP_BY_ID.has(id as CapId) || !!topbarKeyRef(id)))
    } catch { /* 拿不到就用出厂顺序，顶栏不能空 */ }
  }, [])

  /**
   * 名册 / 绑定回来了：记住是哪一套，再把那一套的开关铺开。
   *
   * 顺序是「先盖镜像（applyPrefs），再刷 React 这侧的 state」—— 有几处是直接读
   * localStorage 的（终端回调里的 kbdFull、提示回调里的 noticeOS），见 lib/prefs.ts。
   * 字号还要多过一手终端那层：那是要重新算行列的，光改 state 不重排。
   */
  const applyProfiles = (r: ProfilesResponse) => {
    const p = r.profiles?.find((x) => x.id === r.current)
    setProfile({ id: r.current, name: p?.name ?? r.current })
    applyPrefs(r.prefs)
    setOpts({
      kitty: lsBool('kitty', true), meta: lsBool('meta', true), copyOnSelect: lsBool('copyOnSelect', false),
      sync2026: lsBool('sync2026', true), switchPanel: lsBool('switchPanel', true),
    })
    setKbdFull(lsBool('kbdFull', true))
    setNoticeDot(lsBool('noticeDot', true))
    setNoticeOS(lsBool('noticeOS', false))
    setNoticeOSFg(lsBool('noticeOSFg', false))
    setNoticeMs(Number(localStorage.getItem('noticeCardMs') ?? AUTO_MS_DEFAULT) || 0)
    const sc = localStorage.getItem('scheme')
    if (sc === 'dark' || sc === 'light') setScheme(sc)
    const fs = Number(localStorage.getItem('fontSize'))
    if (fs > 0) setFontSize(sess.current?.setFontSize(fs) ?? fs)
  }

  /**
   * 终端那五个开关，**整组跟着 profile 走**：键盘协议 / Option 当 Meta 是「这台机器的键盘
   * 长什么样」，选中即复制、同步输出、点 switch 开面板一览是「这类设备上怎么用」—— 都是
   * 换台设备就该换一份的东西。
   */
  const setOpt = useCallback((k: keyof TermOpts, v: boolean) => {
    setOpts((o) => ({ ...o, [k]: v }))
    pushPref(profile.id, k, v ? '1' : '0', toast)
  }, [profile.id, toast])

  /** 明暗。点这一下就把它钉在这一套 profile 上（见上面那个 media 监听） */
  const flipScheme = useCallback(() => {
    const next: Scheme = scheme === 'dark' ? 'light' : 'dark'
    setScheme(next)
    pushPref(profile.id, 'scheme', next, toast)
  }, [scheme, profile.id, toast])

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
        // 报到：服务端记住这台设备，第一次来的话按 kind 挑一套绑上（见 internal/profiles）。
        // **在拉排布之前**：那两个 GET 靠这台设备的绑定算「哪一套」，顺序颠倒的话第一次
        // 打开拿到的是默认那一套，绑好的那套要等下次刷新。
        applyProfiles(await api.post<ProfilesResponse>('/profiles/hello', { kind: deviceKind() }))
      } catch { /* 老版本服务端没这个口 / 写不进盘：照旧用默认那一套 */ }
      await loadLayout()
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
  /**
   * 全屏失败说过没有。**记在 localStorage 而不是内存**：这个开关默认开，而手机上页面
   * 重开是家常便饭 —— 只记在内存的话，浏览器不给全屏的人每开一次页面就挨一条 toast。
   * 一台设备说明白一次就够了。
   *
   * 存的是**失败原因本身**（不只是「说过了」）：手机上没有控制台，「为什么没全屏」只能
   * 靠这一句 —— 设置里那条开关下面会把它显示出来（`kbdFullErr`）。
   */
  const fullWarned = useRef(!!localStorage.getItem('kbdFullErr'))
  /** 有一次 requestFullscreen 还在路上：挡住同一下手势里的第二次请求（见 enterFull） */
  const fullPending = useRef(false)
  /**
   * 那把锁的**自动解锁**定时器。
   *
   * 必须有：安卓上见过 requestFullscreen 被静默忽略 —— 既不 resolve 也不 reject、
   * 也不来 fullscreenchange，于是 `.finally` 永远不跑，锁一直合着。之后每次点全屏都被
   * 那一行挡掉，表现就是**「按钮点了没反应，连提示都没有」**（用户报的）。
   * 这把锁本来只为「同一下手势里进来两次」而设，1.5 秒之后放开不会漏掉任何去重。
   */
  const fullUnlock = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
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

  /**
   * 进全屏。已经在全屏里就什么都不做。
   *
   * quiet：键盘那条路会**反复**走到这儿（每弹一次键盘一次），失败提示只说一次就够了 ——
   * 每弹一次键盘吐一条 toast 比不全屏烦得多。但也不能一声不吭：静默失败正是「点了没反应」
   * 那类查不出来的毛病。
   */
  const enterFull = (quiet = false) => {
    const d = document as FsDoc
    const el = document.documentElement as FsEl
    if (d.fullscreenElement ?? d.webkitFullscreenElement) return
    // 同一下手势会从两条路进来（点输入框的 pointerdown + 键盘状态回调），而
    // `fullscreenElement` 要等请求真的落地才有值 —— 不挡住第二次的话，浏览器会拒掉它，
    // 于是「明明全屏成功了，设置里却写着上次没成功」（实拍见过）。
    if (fullPending.current) return
    fullPending.current = true
    const say = (m: string) => {
      localStorage.setItem('kbdFullErr', m) // 原因留着给设置面板显示（手机上没有控制台）
      if (quiet && fullWarned.current) return
      fullWarned.current = true
      toast(m)
    }
    const req = el.requestFullscreen ?? el.webkitRequestFullscreen
    if (!req) {
      fullPending.current = false
      say('这个浏览器不给网页全屏。iPad / iPhone 上把页面「添加到主屏幕」，从主屏打开就没有地址栏了')
      return
    }
    // 用户手势之外调用、或者被策略挡住都会 reject，别让它变成一个没人看见的报错。
    // **同步抛也得接住**：老 webkit 那套（和安卓上一些内核）是当场 throw 而不是 reject，
    // 抛出去的话下面那个 finally 不跑，锁就永远合着（见 fullUnlock）。
    // 「请求发出去了但一声不响」也要有个说法：安卓上见过既不 resolve 也不 reject、
    // fullscreenchange 也不来的情形，那时候什么都不说就是纯粹的「点了没反应」。
    let settled = false
    clearTimeout(fullUnlock.current)
    fullUnlock.current = setTimeout(() => {
      fullPending.current = false
      if (settled || (d.fullscreenElement ?? d.webkitFullscreenElement)) return
      say('全屏没生效：浏览器把这个请求吃掉了（既没成功也没报错）。iPad / 安卓上把页面「添加到主屏幕」，从主屏打开就没有地址栏了')
    }, 1500)
    let p: unknown
    try {
      p = req.call(el)
    } catch (e) {
      fullPending.current = false
      clearTimeout(fullUnlock.current)
      say('全屏失败：' + (e as Error).message)
      return
    }
    void Promise.resolve(p)
      .then(() => {
        settled = true
        // 成功过一次就把旧的失败记录清掉，不然设置里一直挂着一句过期的话
        localStorage.removeItem('kbdFullErr')
        fullWarned.current = false
      })
      // 被拒但**人已经在全屏里**（另一条路先成了）不算失败，别去写那条记录
      .catch((e: unknown) => {
        settled = true
        if (d.fullscreenElement ?? d.webkitFullscreenElement) return
        say('全屏失败：' + (e as Error).message)
      })
      .finally(() => {
        fullPending.current = false
        clearTimeout(fullUnlock.current)
      })
  }

  const toggleFull = () => {
    const d = document as FsDoc
    if (d.fullscreenElement ?? d.webkitFullscreenElement) {
      void (d.exitFullscreen?.() ?? d.webkitExitFullscreen?.())
      return
    }
    enterFull()
  }

  /*
   * 键盘一弹起来就进全屏（开了那个开关才算数）。
   *
   * **收起键盘不退出** —— 见 kbdFull 那条注释。
   *
   * 靠得住的前提是**浏览器还认得出这是用户手势**：键盘是你点输入框点出来的，而
   * requestFullscreen 只在手势的有效期内（Chrome 约 5 秒）给过。键盘 300ms 就上来了，
   * 所以这一下通常在窗口内；真被拒了会 toast 一次，不会闷着。
   */
  useEffect(() => {
    if (kbdFull && kbRoom) enterFull(true)
    // enterFull 只读 ref 和 document，不必进依赖
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [kbdFull, kbRoom])

  /*
   * 真正靠得住的触发点：**点输入框那一下本身**。
   *
   * 上面那个「视口被压掉了就全屏」是兜底，实际很容易失手 —— `requestFullscreen` 要的是
   * **用户手势**，而键盘引起的 visualViewport resize 是那一下点击的**后果**，跑在另一个
   * 任务里，那时候手势往往已经过期或者被别的调用消耗掉了（桌面上模拟一下就报
   * `Permissions check failed`，真机上的表现就是「开了开关也不全屏」）。
   *
   * pointerdown 是捕获阶段、在 focus 之前，所以顺序正好：先进全屏，再聚焦、再弹键盘 ——
   * 键盘是在全屏之后升起来的，不会被全屏那一下的重排顶掉。
   *
   * 只认真正会弹键盘的东西（textarea / input）。**终端那一块不算** —— 手机上点终端多半
   * 是要看、要滚，不是要打字，点一下就全屏太粗暴；从终端进键盘走的是软键条那个 ⌨ 键，
   * 那条路单独接（见 onKeyboard）。
   */
  useEffect(() => {
    if (!kbdFull) return
    const onDown = (e: PointerEvent) => {
      const t = e.target as HTMLElement | null
      if (t?.closest('textarea, input:not([type=checkbox]):not([type=radio])')) enterFull(true)
    }
    document.addEventListener('pointerdown', onDown, true)
    return () => document.removeEventListener('pointerdown', onDown, true)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [kbdFull])

  /* --------------------------------------------------------- 顶栏动作 */
  const connect = () => {
    setOverlay(null)
    // 这个标签页连过一次就记下来（sessionStorage：只对这个标签页，关掉就没了）。
    // iOS 锁屏久了 Safari 会把整个页面丢掉重载，解锁回来时如果又停在「点连接」那一屏，
    // 等于每次解锁都得手点一次 —— 断线重连在 Session 里做了，这条管的是「页面都没了」。
    try {
      sessionStorage.setItem('connected', '1')
    } catch {
      /* 无痕模式之类的写不进去，那就退回手点 */
    }
    sess.current?.connect()
  }
  /**
   * 顺手把焦点还给终端 —— 但**只在不会改变键盘开合的时候**。
   *
   * 两种情况可以聚：键盘本来就开着（焦点在终端上，那就把它还回去），或者这机器有鼠标
   * （聚焦不会弹出任何东西）。触屏上键盘收着的时候一律不聚 —— 聚一下就是把输入法顶出来。
   *
   * 「键盘本来开着没有」认的是**终端那个隐藏输入框**：焦点在发件箱里时这里是 false，
   * 那也正好 —— 不该把人正在口述的那个框的焦点抢过来。
   */
  const refocusTerm = () => { if (kbdUp || finePointer()) sess.current?.focus() }

  const bumpFont = (d: number) => {
    const n = sess.current?.setFontSize(fontSize + d) ?? fontSize
    setFontSize(n)
    pushPref(profile.id, 'fontSize', String(n), toast)
    refocusTerm()
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

  /**
   * 「取」：把**跑 herdr 那台机器**的剪贴板搬到手机剪贴板。
   *
   * herdr 的复制（选中即复制 / COPY 模式）落在它自己那台机器的剪贴板上，浏览器一无所知
   * —— 实测手机上拖选一段，herdr 报「copied 84 chars to clipboard」，那 84 个字进的是
   * Mac 的剪贴板，手机上哪儿都粘不出来。所以要多这一下：读过来，再写进手机剪贴板。
   *
   * **必须是用户点出来的**：写剪贴板要用户手势。写不进去就退到「点一下复制」那条
   * （CopyPrompt）—— 那上面的点击又是一次新的手势。
   */
  const pullClip = async () => {
    let r: ClipResult
    try {
      r = await api.get<ClipResult>('/clip')
    } catch (e) {
      toast('取不到机器上的剪贴板：' + (e as Error).message)
      return
    }
    if (!r.text) {
      toast('那台机器的剪贴板是空的')
      return
    }
    if (await writeClipboard(r.text)) toast(`已进手机剪贴板 · ${[...r.text].length} 字`)
    else setPendingCopy(r.text)
  }

  /**
   * 「粘」：手机剪贴板 → 终端（按括号粘贴送，见 Session.paste）。
   *
   * 读不到就摊一个框让人长按粘（PastePrompt）—— 长按菜单是浏览器自己的，永远通。
   */
  const pastePhone = async () => {
    // 先说一句：这一步可能在等人（Chrome 读剪贴板要点一次「粘贴」确认），不吭声的话
    // 那几秒看着就像按键没反应
    toast('读手机剪贴板…')
    const text = await readClipboard()
    if (text === null) {
      setPasteOpen(true)
      return
    }
    if (!text) {
      toast('手机剪贴板是空的')
      return
    }
    sess.current?.paste(text)
    toast(`已粘进终端 · ${[...text].length} 字`)
  }

  /**
   * 「点 herdr 的 switch 就开面板一览」那条路要用的 ref。
   *
   * 和 cwdRef 一个道理：终端那层的回调是建 Session 时捕获的（那个 effect 只依赖 gate），
   * 而 openPanes 每次渲染都是一个新闭包 —— 直接捕获会一直用第一次那个，里面的
   * loadPanes / markSeen 都是旧的。
   */
  const openPanesRef = useRef(() => {})

  /**
   * 开「面板一览」。顺手刷一次列表 —— agent 状态和 pane 的增删随时在变，
   * 而这个面板多半是隔了好一会儿才再打开一次。
   *
   * **开之前先收键盘。** 开它的三条路（顶栏 ▦、软键条 `act:panes`、点 herdr 顶栏那个
   * switch）都可能是在键盘弹着的时候按的，而且三条都刻意**没让浏览器改焦点**（软键条在
   * mousedown 上 preventDefault，触屏那层把 touchstart 整个吃掉）—— 于是面板浮出来时
   * 发件箱 / 终端那个输入框还聚着，键盘还占着半个屏。那时候点列表里一行，那一下先去把键盘
   * 收掉（焦点一走 `--vvh` 一变，面板跟着重排），于是「第一下不跳、第二下才跳」（真机实拍）。
   * 列表里那一行自己也补了一道（收工在 pointerup 上，见 PaneSwitcher），两条一起才稳。
   */
  /**
   * 面板一览开着时那一拍自动刷新用的。**必须是稳定的**（useCallback）：面板里那个
   * interval 的依赖就是它，每次渲染换一个新函数的话 interval 每次都被重建，永远等不到 4 秒。
   * 静默（quiet=true）—— 失败不弹 toast，见 PaneSwitcher 里那段。
   */
  const reloadPanes = useCallback(() => void compose.loadPanes(true), [compose.loadPanes])

  const openPanes = () => {
    blurInput()
    setPanel('panes')
    void compose.loadPanes(true)
    // 面板一览就是「看这些变化」的地方，开了就算**全部**看过：角标清零、右上角那几张收掉。
    // 点单张卡片只清那个 pane 的（见 Notices 的 onGoto）；关掉一张卡不算看过（那只是嫌它挡着）。
    notices.markSeen()
  }

  useEffect(() => { openPanesRef.current = openPanes })

  /**
   * 跳到某个 pane。
   *
   * 跳完**不改键盘的开合**（见 refocusTerm）：刚跳过去多半是要看，不是要打字，而本来收着的
   * 键盘被跳转顶出来最烦 —— 那时候屏幕只剩一半，还得先把它收掉才能看清跳到哪儿了。
   */
  const gotoPane = async (id: string, zoom: boolean) => {
    setPanel(null)
    const r = await compose.jump(id, zoom)
    if (!r) return
    refocusTerm()
    // **herdr 回的是它自己认的焦点 pane**（`focused_pane_id`），不是我们请求的那个。两者
    // 不一样就是没跳成，这时候绝不能报「已跳到」—— 屏幕上还在老 pane、弹窗却说成功，是最
    // 难查的一种：用户以为是画面没刷新，而实际上焦点真的没动（用户报过）。
    if (r.target !== id) {
      toast(`没跳到 ${id}：herdr 说焦点在 ${r.target}`)
      void compose.loadPanes(true) // 列表可能已经过期了（那个 pane 被关掉之类的）
      return
    }
    // 终端这条连接断着 / 正在重连时，跳转本身是走 HTTP 的，照样成功 —— 但画面是冻住的旧帧，
    // 看起来就是「点了没反应，多点几次也一样」。这时候必须说清是画面旧了，不是没跳过去。
    const offline = status.cls !== 'on'
    toast(
      (r.singlePane
        ? `已跳到 ${r.target}（这个 tab 只有一个 pane）`
        : `已跳到 ${r.target}${r.zoomed ? ' · 全屏' : ' · 已退出全屏'}`)
      + (offline ? '。终端这会儿没连上，画面是旧的 —— 连上就跟过来了' : ''),
    )
  }

  /**
   * 顶栏每个按钮**点了干什么**（`on` 是亮不亮、`badge` 是角标、`hide` 是这个部署没有这项）。
   *
   * 和「按钮长什么样」分开放：图标和名字在 `components/topbarItems.tsx`（编辑器要用同一份），
   * 而这些动作要用 App 的状态和 Session，搬不出去。两边靠 id 对上，服务端那份白名单也是
   * 同一批 id（internal/topbar，有测试盯着两边一致）。
   */
  // 顶栏上放的「我的按键」里也可能有打了 confirm 的（关 pane 那种）。坐标用 item 本身
  // （顶栏上一个按钮只有一个，服务端拒重复），keyLib 换了就放下。见 hooks/useArm
  const { armed, tap } = useArm(keyLib)
  /**
   * 顶栏上开着的那个**弹出组**（那一项 + 它的 DOM，浮窗贴着它摆）。
   * 顶栏在屏幕上半，所以浮窗朝下弹（见 KeyGroupPopup）—— 一个图标格子点开方向键，
   * 这是最省地方的放法。
   */
  const [openGroup, setOpenGroup] = useState<{ item: string; el: HTMLElement } | null>(null)

  const topbarAct: Partial<Record<CapId, {
    on?: boolean
    run: () => void
    /** 覆盖 title（面板一览要写清「几条没看」，全屏要说清是进还是出） */
    title?: string
    /** 覆盖图标（全屏那个进 / 出两个样） */
    icon?: React.ReactNode
    badge?: number
    /** 这个部署没有这项（文件浏览可以在服务端关掉），画出来点开是一片 404 */
    hide?: boolean
  }>> = {
    panes: {
      on: panesOpen,
      run: () => (panesOpen ? setPanel(null) : openPanes()),
      // 说「条」不说「几个 agent」：同一个 agent 连着变几次就是几条，实测挂一会儿就能
      // 攒十几条，写成「10 个 agent」是假的
      title: notices.unread.length
        ? `面板一览：${notices.unread.length} 条还没看（等你回答 / 跑完了）`
        : undefined,
      badge: noticeDot ? notices.unread.length : 0,
    },
    // 文件浏览能在服务端关掉（HERDR_WEB_FILES=0）。那时候连按钮都不画 ——
    // 点开一片 404 比没有这个入口更糟。主入口其实是终端里那行路径可点。
    files: { on: filesOpen, run: () => (filesOpen ? setPanel(null) : openFiles()), hide: state?.files === false },
    compose: { on: showCompose, run: () => toggleCompose(!showCompose) },
    keys: { on: showKeys, run: () => toggleKeys(!showKeys) },
    // 这一下是用户手势，正好在这儿进全屏（键盘那条路见 kbdFull 的注释）
    kbd: { on: kbdUp, run: () => { if (kbdFull && !kbdUp) enterFull(true); sess.current?.toggleKeyboard() } },
    img: { run: () => picker.current?.click() },
    clip: { run: () => void pullClip() },
    paste: { run: () => void pastePhone() },
    'font-': { run: () => bumpFont(-1) },
    'font+': { run: () => bumpFont(1) },
    theme: { run: flipScheme },
    full: {
      on: full,
      run: toggleFull,
      title: full ? '全屏：点一下退出' : '全屏：去掉地址栏和工具条，终端多几行',
      icon: full ? <Minimize className="size-4" /> : <Maximize className="size-4" />,
    },
    settings: { on: settings, run: () => setPanel(settings ? null : 'settings') },
  }

  /**
   * badge：右上角挂一个**数字**角标（还有几条没看过）。0 / 省略 = 不挂。
   *
   * 数字而不是一个点：点只说「有东西」，而这儿的「有几条」是有用的 —— 两个 agent 在等你
   * 和五个在等你，要不要放下手里的事去看是两回事。超过 9 就写 9+（再多那格就撑破了，
   * 而且到那份上具体几条也不重要了）。
   *
   * ring 用顶栏底色，让它像贴在图标上的徽标而不是浮在半空的一块红。
   */
  const badgeEl = (badge?: number) => !!badge && (
    <span
      data-testid="notice-badge"
      className="absolute -top-1 -right-1 grid h-4 min-w-4 place-items-center rounded-full bg-bad px-1
                 font-mono text-[10px]/none font-medium text-white ring-2 ring-bar tabular-nums"
    >
      {badge > 9 ? '9+' : badge}
    </span>
  )

  const iconBtn = (title: string, on: boolean, onClick: () => void, child: React.ReactNode, cls?: string, badge?: number) => (
    <Button variant="default" size="icon" on={on} title={title} className={cn('relative', cls)} onClick={onClick} onMouseDown={(e) => e.preventDefault()}>
      {child}
      {badgeEl(badge)}
    </Button>
  )

  /**
   * 顶栏上的一个**「我的按键」**（items 里的 `key:<定义ID>`，见 internal/topbar）。
   *
   * 画成软键条那种 mono 方块而不是图标：顶栏上「图标 = 内置功能、mono 方块 = 我自己配的键」
   * 一眼分得开，而这两类点下去的后果差得远（一个开面板，一个往 pane 里发字节）。
   * 高度还是 32px —— `variant="key"` 配默认 size，**别用 `size="key"`**：那一档在手机上
   * 矮一号（h-7），混在一排图标里就歪了。
   *
   * act 那一档直接走 `topbarAct`：softkeys 的 act 白名单（kbd / img / panes / files /
   * clip / paste）正好是 CapId 的子集，**同一个 id 就是同一件事** —— 亮不亮、角标、
   * 「这个部署有没有这项」全都跟着内置那份走，不写第二份映射。这也是「动作库只有一份」
   * 这件事在代码里的样子。
   */
  const keyBtn = (item: string, k: SoftKey) => {
    const ta = k.act ? topbarAct[k.act] : undefined
    if (ta?.hide) return null   // 比如文件浏览在服务端关掉了：画出来点开是一片 404
    const up = armed === item   // 举起来了，等第二下
    const isGroup = !!k.members
    return (
      <Button
        variant="key"
        on={isGroup ? openGroup?.item === item : (k.sticky ? sticky[k.sticky] : !!ta?.on)}
        title={up ? '再点一次才真的发出去'
          : isGroup ? `${k.label}：点开一小片键（浮在下面，不占顶栏的地方）`
            : `${k.label} —— ${k.spec || k.sticky || k.act || ''}${k.confirm ? '（要点两下）' : ''}`}
        className={cn('relative',
          // 举起来只换颜色，**不换文字**：改字会让按键变宽，手指底下的键当场挪位置
          up && 'border-bad bg-bad text-white hover:border-bad hover:bg-bad')}
        style={spanStyle(k.span)}
        onMouseDown={(e) => e.preventDefault()}
        onClick={(e) => {
          if (!tap(item, k.confirm)) return   // 这一下只是举起来
          if (isGroup) {
            const el = e.currentTarget as HTMLElement
            setOpenGroup((cur) => (cur?.item === item ? null : { item, el }))
            return
          }
          if (ta) ta.run()
          else if (k.sticky) sess.current?.toggleSticky(k.sticky)
          else if (k.send) { sess.current?.sendKey(k.send); if (kbdUp) sess.current?.focus() }
        }}
      >
        {k.label}
        {badgeEl(ta?.badge)}
      </Button>
    )
  }

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
          /**
           * **touchstart 必须自己原生吃掉，展开也在那儿做。**
           *
           * 用户报过：键盘收着（Android 上用返回键收的）、顶栏收成这条缝，点一下展开 ——
           * 输入法自己又弹出来了。那一下不是页面里任何代码干的：Chromium 处理完一个
           * **GestureTap** 之后**无条件**调 `ShowVirtualKeyboard()`（`WebFrameWidgetImpl::
           * DidHandleGestureEvent`，不看事件有没有被 preventDefault），浏览器那边只看
           * 「此刻聚焦的元素可不可编辑」—— 而 Android 用返回键收键盘时**不会 blur**，终端
           * 那个隐藏 textarea 还聚着焦，于是 IME 被重新顶出来。所以 click / mousedown 上
           * 的 preventDefault 一律拦不住，只有**让这条手势序列压根不产生**才行：touchstart
           * 被 preventDefault 之后 Chromium 会丢掉整条序列（没有 GestureTap、也没有兼容
           * 鼠标事件和 click）—— 这也是点终端本体从来不会这样弹的原因（`term/touch.ts`
           * 的 onStart 就是这么做的）。
           *
           * 两个坑：React 的 `onTouchStart` 挂在 root 上、是 **passive** 的，
           * `preventDefault()` 不生效，必须原生监听 `{ passive: false }`；改成吃 touchend
           * 也不行 —— 「touchend 被 handled + Android」本身就是 Chromium 弹键盘的另一个
           * 触发条件（`WidgetBaseInputHandler`）。
           */
          ref={(el) => {
            if (!el) return
            const onTouch = (e: TouchEvent) => { e.preventDefault(); setPeek(true) }
            el.addEventListener('touchstart', onTouch, { passive: false })
            return () => el.removeEventListener('touchstart', onTouch)
          }}
          onClick={() => setPeek(true)} // 桌面鼠标 / 键盘激活走这条（触屏那条已经在上面接了）
          onMouseDown={(e) => e.preventDefault()}
        >
          <span className="h-0.5 w-10 rounded-full bg-line-hi" />
        </button>
      )}

      {/* 手机竖屏（< 440px）收成一行：状态只留那个彩点（文字进 title）、连上之后不显示
          「连接」、字号和明暗那三个图标挪进设置 →「终端」页。七个图标在 393px 上排不下，
          折成两行就白吃掉 ~36px（约三行终端），而这三个都是一次调完的东西。 */}
      {!barHidden && (
      <header className="flex shrink-0 flex-wrap items-center gap-2.5 border-b border-line bg-bar px-3 py-2 select-none max-md:gap-1.5 max-md:px-2">
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
          <span className="truncate text-xs text-muted tabular-nums max-phone:hidden">{status.text}</span>
          {/* 哪个 herdr session。手机上状态文字会收掉，这个标签留着 —— 「我这会儿在哪个
              session」比「120×34」重要得多：命名 session 是**另一个 herdr**，pane 列表
              和投稿目标全是另一套。 */}
          {SESSION && (
            <span
              title={`herdr session：${SESSION}（地址栏里那一段。默认 session 在 /）`}
              className="max-w-32 shrink-0 truncate rounded border border-line bg-ctl px-1.5 py-px font-mono text-[11px] text-muted"
            >
              {SESSION}
            </span>
          )}
        </div>

        {/* 「敲 herdr」那个按钮去掉了：连上就自动敲（HERDR_WEB_ONCONNECT），退出去了要再敲
            一次的话，软键条预设里有现成的「敲 herdr」键 —— 顶栏这个位置比它值钱。 */}
        <div className="flex shrink-0 items-center gap-1.5">
          {/* 连上之后「连接」没用了（真断了会弹遮罩，那上面有自己的连接按钮），
              手机上这 54px 让给别的 */}
          {(status.cls !== 'on' || !phone) && <Button onClick={connect}>连接</Button>}
        </div>

        {/* 顶栏那排按钮：**放哪几个、什么顺序**是配置（设置 →「顶栏」页里拖，存服务端）。
            一行不换行、放不下自己横滑 —— 和手机上的软键条一个做法。换行的话顶栏会长出
            第二行，白吃掉两行终端；而「拖上去的按钮被藏起来」是最难解释的一种行为，
            所以这儿一个都不藏（原来字号 ± / 明暗在手机竖屏是 CSS 藏掉的，去掉了）。 */}
        <div
          data-testid="topbar-items"
          className="flex min-w-0 shrink items-center gap-1 overscroll-contain
                     [scrollbar-width:none] flex-nowrap overflow-x-auto [&::-webkit-scrollbar]:hidden"
        >
          {topbar.map((item) => {
            // 「我的按键」：定义此刻不在（在别的设备上删掉了）就整项跳过，别画一个
            // 点了没反应的方块。服务端读盘故意不核这个，见 internal/topbar 的包注释
            const ref = topbarKeyRef(item)
            if (ref) {
              const k = keyLib.get(ref)
              // 整项都不画（不是画一个空的 span）—— 外面那层有 gap-1，空 span 会留下一道
              // 说不清来路的缝
              const el = k && keyBtn(item, k)
              return el ? <span key={item} className="shrink-0">{el}</span> : null
            }
            const it = CAP_BY_ID.get(item as CapId)
            const act = topbarAct[item as CapId]
            if (!it || !act || act.hide) return null
            return (
              <span key={item} className="shrink-0">
                {iconBtn(act.title ?? `${it.label}：${it.hint}`, !!act.on, act.run, act.icon ?? it.icon, undefined, act.badge)}
              </span>
            )
          })}
        </div>
      </header>
      )}

      {/* 顶栏上那个组键点开的浮窗。fixed，所以挂在这儿不影响任何布局；
          顶栏收起来（键盘弹起）时 openGroup 也就没有锚点了，一起收掉 */}
      {openGroup && !barHidden && (() => {
        const ref = topbarKeyRef(openGroup.item)
        const k = ref ? keyLib.get(ref) : undefined
        if (!k?.members || !k.group) return null
        return (
          <KeyGroupPopup
            cols={k.group.cols}
            members={k.members}
            anchor={openGroup.el}
            onClose={() => setOpenGroup(null)}
            renderKey={(m, at) => keyBtn(`${openGroup.item}/${at}`, m)}
          />
        )
      })()}

      <main className="relative min-h-0 flex-1">
        {/* overflow-hidden：容器一缩（呼输入法）到终端重排完之间，xterm 的画布还是旧的高度，
            不裁的话它会画到发件箱上面去；冻帧那张图也靠这个裁 */}
        <div ref={host} className="term-host absolute inset-0 overflow-hidden pt-1.5 pr-1 pb-1 pl-2" />
        {overlay && (
          <div className="absolute inset-0 z-5 grid place-items-center bg-bg/80 p-5 backdrop-blur-sm">
            <div className="max-w-[440px] text-center">
              <Logo size={48} className="mx-auto mb-3.5" />
              <h1 className="mb-2 text-[17px] font-medium tracking-tight">herdr in the browser</h1>
              <p className="text-[13px]/relaxed text-muted [&_code]:rounded [&_code]:border [&_code]:border-line
                            [&_code]:bg-ctl [&_code]:px-1.5 [&_code]:py-0.5 [&_code]:font-mono [&_code]:text-xs [&_code]:text-fg"
                 dangerouslySetInnerHTML={{ __html: overlay.msg }} />
              {/* 这一个是「一屏里唯一的主操作」，故意比顶栏那套大一档。高度是写死的
                  （见 ui/button 的 size），所以给 h-10 而不是加 py */}
              <Button variant="primary" className="mt-4 h-10 px-5 text-[13px]" onClick={connect}>
                {overlay.btn}
              </Button>
            </div>
          </div>
        )}
        {panesOpen && (
          <PaneSwitcher
            panes={compose.panes}
            watching={compose.watching}
            onClose={() => setPanel(null)}
            onGoto={(id, zoom) => void gotoPane(id, zoom)}
            onReload={reloadPanes}
          />
        )}
        {filesOpen && (
          <FilesPanel
            panes={compose.panes}
            start={filesAt}
            onClose={() => setPanel(null)}
            onOpen={(p) => void openPath(p)}
          />
        )}
        {/* 查看器盖在最上面（面板也盖住）：从面板里点开一张图之后，退出来还该回到
            那个目录，所以这里**不关**面板，只是压在它上面 */}
        {viewing && (
          <FileViewer
            stat={viewing}
            onClose={() => setViewing(null)}
            onSend={(p) => { setViewing(null); sendPath(p) }}
            onBrowse={(d) => { setViewing(null); openFiles(d) }}
            toast={toast}
          />
        )}
        {settings && (
          <SettingsPanel
            tab={tab}
            onTab={setTab}
            onClose={() => setPanel(null)}
            opts={opts}
            setOpt={setOpt}
            dot={noticeDot}
            onDot={(v) => { setNoticeDot(v); pushPref(profile.id, 'noticeDot', v ? '1' : '0', toast) }}
            os={noticeOS}
            onOS={(v) => { setNoticeOS(v); pushPref(profile.id, 'noticeOS', v ? '1' : '0', toast) }}
            osFg={noticeOSFg}
            onOSFg={(v) => { setNoticeOSFg(v); pushPref(profile.id, 'noticeOSFg', v ? '1' : '0', toast) }}
            cardMs={noticeMs}
            onCardMs={(v) => { setNoticeMs(v); pushPref(profile.id, 'noticeCardMs', String(v), toast) }}
            kbdFull={kbdFull}
            onKbdFull={(v) => { setKbdFull(v); pushPref(profile.id, 'kbdFull', v ? '1' : '0', toast) }}
            heals={heals}
            // 存完把整份配置回传过来：软键条那一条和顶栏上放的「我的按键」是同一份定义
            onSaved={applySoftkeys}
            onTopbar={setTopbar}
            toast={toast}
            state={state}
            fontSize={fontSize}
            onFont={bumpFont}
            scheme={scheme}
            onScheme={flipScheme}
            profile={profile}
            // 换了一套 / 改了名 / 改了绑定：记住新的那一套，再把排布整份换过来
            onProfiles={(r) => { applyProfiles(r); void loadLayout() }}
          />
        )}
        {pasteOpen && (
          <PastePrompt
            onSend={(t) => { sess.current?.paste(t); toast(`已粘进终端 · ${[...t].length} 字`) }}
            onClose={() => setPasteOpen(false)}
          />
        )}
        {pendingCopy !== null && (
          <CopyPrompt
            text={pendingCopy}
            onCopied={() => { setPendingCopy(null); toast('已复制') }}
            onClose={() => setPendingCopy(null)}
          />
        )}
        <LanPrompt
          state={lanDirect.ask}
          onAccept={lanDirect.accept}
          onDecline={lanDirect.decline}
          onDismiss={lanDirect.dismiss}
        />
        {/* 提示浮在终端右上角。面板开着时先让开 —— 那几块浮层就在同一个角上，
            叠上去会把面板的标题栏盖掉 */}
        <Notices
          items={notices.items}
          autoMs={noticeMs}
          hidden={!!panel || !!viewing}
          // 点卡片 = 我去看这个 agent 了：它名下的未读全消掉（角标跟着减），别的一条不动
          onGoto={(id) => { notices.seePane(id); void gotoPane(id, paneZoomPref()) }}
          onDismiss={notices.dismiss}
          onMore={openPanes}
        />
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
              pad={pad}
              sticky={sticky}
              onSend={(b) => { sess.current?.sendKey(b); if (kbdUp) sess.current?.focus() }}
              onSticky={(w) => sess.current?.toggleSticky(w)}
              // act 那一档直接走顶栏那张动作表：softkeys 的 act 白名单是 CapId 的**子集**
              // （internal/capability 那张表里 Key 那一列），同一个 id 就是同一件事 ——
              // 亮不亮、角标、「这个部署有没有这项」全都跟着走，不写第二份映射
              act={(a) => topbarAct[a]}
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
