import type { KeyAct } from '@/capabilities'

// 所有接口都在 /api 下。
//
// 认证走 **HttpOnly cookie**（服务端下发，JS 读不到也改不了），所以 URL 里不再有任何
// 秘密 —— 书签、浏览器历史、云同步、截图都不再是泄露渠道。凭据绑设备不绑 IP，所以
// 换 Wi-Fi、换网段都不掉线。一台设备配一次，见 docs/dev/SECURITY.md。
//
// 凭据放 cookie 而不是 localStorage 是硬要求：iOS 的 ITP 会把「脚本能写的存储」在站点
// 七天没交互之后清掉，那就变成「隔一周回来又要重新配对」。
//
// `?token=` 只在两种情况下还会用到：旧书签第一次打开（服务端会把它换成 cookie 再
// 302 掉），以及 `make dev` 时前端跑在 vite 上、进不了服务端那条 302。
const TOKEN = new URLSearchParams(location.search).get('token') ?? ''

/**
 * 地址栏里的 herdr session：`https://host/work` → `work`，`https://host/` → `''`（默认）。
 *
 * 页面就是靠这一段决定「我对着哪个 herdr」：终端里敲的是 `herdr --session work`，
 * 而**命名 session 有自己的 socket**，所以发件箱、面板一览这些请求也必须带上它 ——
 * 不带就会拿默认 session 的 pane 列表去投一个 work 里的 agent，投进另一个 herdr 而
 * 屏幕上一切正常（服务端那边也有一道判断，见 internal/server/session.go）。
 *
 * 这里的字符集要和 Go 那边的 config.ValidSessionName 对齐；这一份只是别把明显不合法的
 * 东西发过去，**真正的判据在服务端**（拿它拼命令行和 socket 路径的是那一侧）。
 */
export const SESSION = (() => {
  let seg = location.pathname.split('/')[1] ?? ''
  try {
    seg = decodeURIComponent(seg) // 坏的 %xx 会抛，那种路径本来也不是合法名字
  } catch {
    return ''
  }
  return /^[A-Za-z0-9][A-Za-z0-9._-]{0,39}$/.test(seg) && !seg.includes('..') ? seg : ''
})()

/**
 * installId：**这一个浏览器**的标识，只为「这台设备用哪一套排布」服务（profile，见
 * internal/profiles 的包注释）。
 *
 * 为什么不用 auth 那个设备 ID：本机直连（loopback / 旧 token）压根没有设备 ID，而那正是
 * 桌面上最常见的情形。为什么不放 cookie：它不是凭据，服务端也不拿它做任何权限判断 ——
 * 拿到它最多只能说「我是那台平板」，然后读到那台平板的快捷键条排布。
 *
 * 清掉 localStorage 就丢了绑定：那时候服务端按 deviceKind() 重新猜一套，人在设置里再点
 * 一下就好（比把它塞进凭据、再为它做一套过期 / 撤销强得多）。
 */
const INSTALL_KEY = 'installId'
const INSTALL_RE = /^[A-Za-z0-9_-]{6,64}$/
export const INSTALL = (() => {
  const cur = localStorage.getItem(INSTALL_KEY)
  if (cur && INSTALL_RE.test(cur)) return cur
  // 局域网直连那一跳带过来的（见 hooks/useLanDirect.ts）。换 origin 就是换了一份
  // localStorage，不带过来的话同一台平板在新 origin 上算「第一次来」，服务端会按
  // deviceKind() 重新猜一套排布 —— 明明是同一台设备、同一个人。
  //
  // 走 fragment 而不是 query：它不是秘密（服务端不拿它做任何权限判断），但也没有
  // 理由留在地址栏、浏览器历史和访问日志里。
  const carried = new URLSearchParams(location.hash.slice(1)).get('install')
  if (carried && INSTALL_RE.test(carried)) {
    localStorage.setItem(INSTALL_KEY, carried)
    return carried
  }
  // getRandomValues 在非安全上下文里也有（randomUUID 没有 —— 局域网 http 上就是它）
  const b = new Uint8Array(12)
  crypto.getRandomValues(b)
  const v = [...b].map((n) => n.toString(16).padStart(2, '0')).join('')
  localStorage.setItem(INSTALL_KEY, v)
  return v
})()

// 上面读完就把 fragment 抹掉：地址栏干净，而且刷新时不会再走一遍「采纳」那条路。
if (location.hash.includes('install=')) {
  history.replaceState(null, '', location.pathname + location.search)
}

export type DeviceKind = 'phone' | 'tablet' | 'desktop'

/**
 * 这台设备是什么。**只在「这个浏览器第一次来、还没绑过」那一下用得上**：服务端拿它挑一套
 * 默认的 profile，挑完就落盘，之后再也不猜（见 internal/profiles 的包注释）。
 *
 * 用 screen 的**短边**而不是 innerWidth：分屏、转屏、开着开发者工具都会让 innerWidth 变，
 * 而这个判断该反映的是「这是台什么机器」。
 */
export const deviceKind = (): DeviceKind => {
  if (!matchMedia('(pointer: coarse)').matches) return 'desktop'
  return Math.min(screen.width, screen.height) < 480 ? 'phone' : 'tablet'
}

// session 挂在**每个** /api 请求上，不是只挂发件箱那几个：漏一个的表现是「这一个口
// 悄悄读了默认 session」，而漏没漏只能一个调用点一个调用点去看。用不上的口（softkeys、
// auth）服务端直接忽略这个参数。
//
// install 同理挂在每个请求上：快捷键条 / 顶栏的 GET 靠它算出「这台设备用哪一套」，不带
// 就一律给默认那一套 —— 漏了的表现是「平板上打开是手机那套排布」。
function url(path: string) {
  const extra = new URLSearchParams()
  if (TOKEN) extra.set('token', TOKEN)
  if (SESSION) extra.set('session', SESSION)
  extra.set('install', INSTALL)
  const q = extra.toString()
  if (!q) return `/api${path}`
  return `/api${path}${path.includes('?') ? '&' : '?'}${q}`
}

// 跨站请求设不了自定义头（会触发 preflight，而服务端压根不答 preflight），
// 所以这个头是 CSRF 的第三道防线（前两道是 SameSite=Strict 和 Origin 校验）。
const CSRF = { 'x-herdr-web': '1' }

/**
 * 401 有两种，UI 完全不同，所以事件里带上原因：
 *   - 没配对 / 凭据被撤销 → 要配对码（得回机器前）
 *   - `need: 'passkey'` → 配过对，只是太久没验证了 → 点一下 Face ID 就行
 */
export const UNAUTHED = 'hw-unauthed'
export type UnauthedDetail = { need?: 'passkey' }

/**
 * 带上原始响应的错误。
 *
 * 为什么要它：有些口除了一句话还会给一个**机器判据**（chat 那个 409 的 `reason`：
 * 是「hook 还没装」还是「同一个目录好几个 agent pane」）—— 两种的下一步完全不同，
 * 界面上要说的话也不一样。光抛 `new Error(msg)` 的话那个字段在这儿就丢了，而按错误
 * **文案**去 `includes()` 判断是最脆的一种耦合（改一个字就静默失效）。
 *
 * 老代码一律当普通 Error 用（`.message` 没变），所以加这个不影响任何现有调用点。
 */
export class ApiError extends Error {
  status: number
  /** 服务端给的机器判据（如 chat 的 'need_install' / 'ambiguous'） */
  reason?: string
  constructor(msg: string, status: number, reason?: string) {
    super(msg)
    this.name = 'ApiError'
    this.status = status
    this.reason = reason
  }
}

async function handle<T>(r: Response): Promise<T> {
  const j = await r.json().catch(() => ({ error: `HTTP ${r.status}` }))
  if (r.status === 401) {
    const need = (j as { need?: 'passkey' }).need
    dispatchEvent(new CustomEvent<UnauthedDetail>(UNAUTHED, { detail: { need } }))
  }
  if (!r.ok) {
    const b = j as { error?: string; reason?: string }
    throw new ApiError(b.error ?? `HTTP ${r.status}`, r.status, b.reason)
  }
  return j as T
}

async function req<T>(method: string, path: string, body?: unknown): Promise<T> {
  return handle<T>(
    await fetch(url(path), {
      method,
      credentials: 'same-origin',
      headers: body === undefined ? CSRF : { ...CSRF, 'content-type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
    }),
  )
}

export const api = {
  get: <T>(p: string) => req<T>('GET', p),
  post: <T>(p: string, b?: unknown) => req<T>('POST', p, b),
  put: <T>(p: string, b?: unknown) => req<T>('PUT', p, b),
  del: <T>(p: string) => req<T>('DELETE', p),
  /** 上传裸字节（图片），不走 JSON。 */
  async upload(blob: Blob) {
    return handle<UploadResult>(
      await fetch(url('/herdr/upload'), {
        method: 'POST',
        credentials: 'same-origin',
        headers: { ...CSRF, 'content-type': blob.type || 'application/octet-stream' },
        body: blob,
      }),
    )
  },
}

/** PTY 的 WebSocket 地址。cookie 会在同源握手时自动带上，不用挂在 query 里。 */
export function ptyURL(cols: number, rows: number) {
  const q = new URLSearchParams({ cols: String(cols), rows: String(rows) })
  if (TOKEN) q.set('token', TOKEN)
  if (SESSION) q.set('session', SESSION) // 服务端据此敲 `herdr --session <name>`
  const proto = location.protocol === 'https:' ? 'wss' : 'ws'
  return `${proto}://${location.host}/pty?${q}`
}

/* ------------------------------------------------------------------ 形状 */

export const FOLLOW = '__focused'

export interface WhoAmI {
  authed: boolean
  kind?: 'device' | 'legacy' | 'loopback'
  label?: string
  deviceId?: string
  expires?: string
  ttlDays: number
  tls: boolean
  legacy: boolean // 服务端还留着旧 token 文件
  passkeys: number // 注册过几把
  passkeyAvailable: boolean // **当前这个 origin** 能不能用（裸 IP 访问时为 false，见 auth.UsableOn）
  /**
   * 用不了的时候「换哪个地址就有」。**空 = 服务端说不出确切地址**，那时只能泛泛地讲
   * 「换用域名那条路」—— 隧道那头是什么地址本进程猜不出来，见 server.passkeyURL。
   */
  passkeyURL?: string
}

export interface Device {
  id: string
  label: string
  created: string
  lastSeen: string
  lastIp: string
  expires: string
}

export interface State {
  shell: string
  user: string
  hostname: string
  secureContext: boolean
  compose: { pollMs: number; pushMs: number; settleMs: number }
  /** 提示（右上角弹窗 + 面板图标的红点）多久问一次。0 / 缺失 = 这个部署把提示关了 */
  notice?: { pollMs: number }
  /** 服务端解析出来的 session 名（空 = 默认 session）。herdrSocket 是**这个 session 的**。 */
  session?: string
  herdrSocket: string
  /** 服务端开着文件浏览没有（HERDR_WEB_FILES=0 时为 false）。关着就别画那个按钮 */
  files?: boolean
  /** 看 diff 那条路开着没有（这台机器上没有 git / HERDR_WEB_GIT=0 时为 false）。同上 */
  git?: boolean
  /** chat 模式开着没有（这台机器上没有 claude / codex 的会话目录 / HERDR_WEB_CHAT=0 时为 false）。同上 */
  chat?: boolean
  /**
   * 局域网直连的候选（缺失 = 这个部署没开这条路，见 HERDR_WEB_LAN_PORT）。
   * origins 是**服务端每次现报**的 —— 局域网 IP 会变，前端不能缓存它。
   */
  lan?: { port: number; origins: string[] }
  /** 版本信息。outdated 为真时才有 latest / how —— 后端只在有新版本时才带这两个，
      前端不用自己比版本号（比法在 Go 那边，只有一份）。 */
  version?: { current: string; latest?: string; outdated?: boolean; how?: string }
}

export interface Pane {
  id: string
  agent: string
  status: string
  /**
   * workspace / tab 是**给人看的标签**（herdr 那两个 list 给的，拿不到就是 id）。
   * **别拿 workspace 分组**：两个工作空间同名是常态（标签多半就是目录名）。
   * 「这个 pane 是不是当前这个工作空间的」认 workspaceId ——
   * 改动面板挑仓库、文件面板排起点都靠它（见 components/DiffPanel.tsx）。
   */
  workspace: string
  workspaceId: string
  tab: string
  title: string
  cwd: string
  focused: boolean
  /**
   * seq = herdr 的 `state_change_seq`（全局递增，每次 agent 状态变化推高一格）。
   * **排序只认它** —— herdr 的 API 里没有任何时间戳，这个计数是唯一一个一直对的依据。
   *
   * changed = 上次状态变化的 unix 毫秒，只有 herdr-web 在盯的这段时间里才有（0 / 缺失
   * = 不知道）。它只管显示「3 分钟前」，不参与排序。
   */
  seq?: number
  changed?: number
}

export interface PaneInfo {
  target: string
  followed: boolean
  agent: string
  status: string
  workspaceId: string
  tabId: string
  title: string
  cwd: string
}

// noBox：远端那一屏上认不出输入框（没有提示符字形）。跟「输入框是空的」不是一回事。
export interface SyncResult extends PaneInfo { text?: string; noBox?: boolean }
export interface SayResult extends PaneInfo { chars: number; lines: number; cleared: { rounds: number; empty: boolean | null } }
export interface DraftResult extends PaneInfo { pushed?: number; skipped?: 'not-agent' | 'busy' | 'no-box' }
export interface UploadResult { path: string; name: string; bytes: number; kind: string; dir: string }
/** GET /api/clip：跑 herdr 那台机器的剪贴板（herdr 的复制落在那儿，不是浏览器里）。 */
export interface ClipResult { text: string; bytes: number }

/**
 * 「跳到某个 pane」的结果。
 *
 * zoomed 是**整个 tab** 的放大状态，不是这个 pane 的（herdr 放大的永远是当前焦点
 * pane）；singlePane 是「这个 tab 只有一个 pane，没什么可放大的」—— 跟「放大失败」
 * 不是一回事，得分开说，不然用户以为按钮没生效。
 */
export interface GotoResult { target: string; zoomed: boolean; focusChanged: boolean; singlePane?: boolean }

/* ------------------------------------------------------------------ 提示 */

/**
 * 一条提示：某个 agent 从「在跑」变成了「等你回答」或者「跑完了」。
 *
 * 服务端只在这两种变化上攒（`→ working` 不攒 —— 那是你自己刚投进去的回声），
 * 而且状态稳住 2.5 秒才算数，见 internal/agentwatch/notice.go。
 */
export interface Notice {
  /** 自增号。前端拿它当 `since` 做增量，也当去重的 key */
  seq: number
  at: number // unix 毫秒
  /** pane_id：点一下要跳过去的地址 */
  pane: string
  /**
   * terminal_id。**「同一个 agent 的旧提示换成新的」认这个，不认 pane** ——
   * pane_id 是 herdr 里的位置编号，pane 一开一关就重新分配给别人了。
   */
  term: string
  agent: string
  /** blocked = 等你回答；idle / done = 跑完了 */
  status: string
  /** agent 自己写的会话标题（「图片识别」那种），可能是空的 */
  title: string
  /**
   * 屏幕上抽出来的那段话。**可能是空的** —— 读屏失败、或者一屏全是装饰行。
   *
   * **界面上现在一处都不画**（提示卡和系统通知都去掉了，理由见 components/Notices.tsx
   * 文件头：常常不准、放不全、反正都要点进去看）。它照旧发着，是因为服务端那边**去重靠它**
   * （「投了又按 Esc」那一档认的就是「抽出来的话和上次一模一样」，见 internal/agentwatch）——
   * 别因为界面不画就把那一层拆了。
   */
  text: string
}

export interface NoticesResult {
  notices: Notice[]
  /** 服务端此刻最新的 seq。下一拍拿它当 since —— **不能从列表里推**，空列表也要推进 */
  seq: number
  watching: boolean
}

/* ------------------------------------------------------------------ 文件浏览 */

/**
 * Kind 是「这个东西能怎么看」，不是文件格式：
 *   dir / image（魔数认出来的 png·jpg·gif·webp，能 inline）/ text（预览源码）
 *   binary（只能下）/ special（设备·socket·管道，列得出来但打不开）
 *
 * **服务端按内容认，不按扩展名** —— 目录列表里的 kind 是按扩展名猜的（两千个文件
 * 不可能一个个读魔数），真打开时会重新认一次，所以列表里的图标偶尔会和实际不符。
 */
export type FileKind = 'dir' | 'image' | 'text' | 'binary' | 'special'

export interface FileEntry {
  name: string
  path: string
  dir: boolean
  size: number
  mtime: number // unix 毫秒
  kind: FileKind
  link?: boolean // symlink，点进去会跳到别处
}

export interface FileListing {
  path: string
  /** 空 = 没有上一级可去（到 / 了，或者上一级被 HERDR_WEB_FILE_ROOTS 挡住） */
  parent: string
  entries: FileEntry[]
  /** 被砍掉多少条。不为 0 时**必须显示出来** —— 不然「这儿没有那张图」是句假话 */
  truncated: number
  /** 有多少条点开头的被过滤了 */
  hidden: number
}

export interface FileInfo {
  path: string
  name: string
  dir: boolean
  size: number
  mtime: number
  kind: FileKind
  mime?: string
  parent?: string
}

/**
 * stat 一次给两样东西：这是什么 + 拿什么 URL 去渲染。
 *
 * url 是一条 `/_f/<票>` 短时链接，**不带 cookie 也能开**。必须这样：cookie 认证的
 * /api 请求要求一个自定义头（CSRF 第三道防线），而 `<img src>`、「在新标签打开」、
 * iOS「长按存到相册」全都设不了头。票绑死一个路径、十几分钟过期、密钥只在服务端内存里
 * （重启即全废）—— 所以过期之后要用 /files/link 换一张，别把它当固定地址存起来。
 */
export interface FileStat { info: FileInfo; url?: string; expires?: number }
export interface FileText { path: string; text: string; bytes: number; truncated: boolean }
export interface FileLink { url: string; path: string; expires: number }
export interface FileRoot { path: string; label: string }
export interface FileRoots {
  roots: FileRoot[]
  /** 配了 HERDR_WEB_FILE_ROOTS：只能看那几棵树。前端据此不显示「往上走」之类的假入口 */
  jailed: boolean
  limits: { entries: number; text: number }
}

/**
 * 文件接口都要带 base：**相对路径的解析基准**。
 *
 * 终端里点到 `./out/chart.png` 时传的是那个 pane 的 cwd（`/api/herdr/panes` 里就有）。
 * 服务端**不猜**基准 —— 猜错了会安安静静打开另一个同名文件，屏幕上看不出异常。
 */
export const filesApi = {
  roots: () => api.get<FileRoots>('/files/roots'),
  list: (path: string, opts?: { sort?: 'mtime' | 'name'; all?: boolean }) =>
    api.get<FileListing>(
      `/files/list?path=${encodeURIComponent(path)}` +
      (opts?.sort ? `&sort=${opts.sort}` : '') + (opts?.all ? '&all=1' : ''),
    ),
  stat: (path: string, base?: string) =>
    api.get<FileStat>(`/files/stat?path=${encodeURIComponent(path)}${base ? `&base=${encodeURIComponent(base)}` : ''}`),
  text: (path: string) => api.get<FileText>(`/files/text?path=${encodeURIComponent(path)}`),
  link: (path: string) => api.post<FileLink>('/files/link', { path }),
}

/* ------------------------------------------------------------------ 看 diff */

/**
 * 跟谁比。**默认是 all** —— agent 多半根本不 `git add`，人想看的就是「相对上次提交，
 * 现在改成什么样了」。服务端那三档在 internal/gitdiff。
 */
export type DiffMode = 'all' | 'staged' | 'head'

export type ChangeKind =
  | 'add' | 'modify' | 'delete' | 'rename' | 'copy' | 'type' | 'conflict' | 'untracked'

export interface GitRepo {
  root: string
  branch?: string
  detached?: boolean
  head?: string
  upstream?: string
  ahead?: number
  behind?: number
  /** 还没有任何提交（`git init` 完还没 commit）：没有「上次提交」那一档可看 */
  unborn?: boolean
}

export interface GitChange {
  path: string          // 仓库相对路径
  old?: string          // 改名前（改名那条**两头都要**，不然点进去是「整个文件都新加的」）
  kind: ChangeKind
  staged?: boolean
  unstaged?: boolean
  add: number
  del: number
  binary?: boolean
  /** 未跟踪的**目录**（git 把一整个新目录折成一条）：点它是去文件面板翻，不是看 diff */
  dir?: boolean
}

export interface GitCommit { hash: string; short: string; author: string; date: string; subject: string; merge?: boolean }

export interface GitStatus {
  repo: GitRepo
  mode: DiffMode
  commit?: GitCommit
  changes: GitChange[]
  /** 被砍掉多少条。不为 0 时**必须显示** —— 不说的话「就改了这几个」是句假话 */
  truncated?: number
}

/**
 * 一行里的一截：`eq` 为真是两边一样的部分，假是真正变了的那截。
 *
 * 服务端发的是**字符串**不是偏移量，故意的：偏移在 Go 那边是 rune、在 JS 里是 UTF-16
 * 码元，一个 emoji 就能让两边错位，而错位的样子是高亮框歪在半个字上。
 */
export interface DiffSeg { eq?: true; s: string }

/** `t` 就是 diff 里那个前缀字符；`\\` 是「文件末尾没有换行」那条注记 */
export interface DiffLine { t: ' ' | '+' | '-' | '\\'; o?: number; n?: number; s?: string; segs?: DiffSeg[] }

export interface DiffHunk { head?: string; os: number; ol: number; ns: number; nl: number; lines: DiffLine[] }

export interface DiffFile {
  path: string
  old?: string
  kind: ChangeKind
  binary?: boolean
  mode?: string
  add: number
  del: number
  hunks: DiffHunk[]
  /** 还有多少行没给（撞上行数上限）。**必须显示** */
  cut?: number
}

export interface GitPatch { files: DiffFile[]; over?: boolean; limit: number }

/**
 * 顶栏那个角标要的东西。
 *
 * `sig` 是这份改动的指纹：角标问的是**「有你还没看过的改动吗」**，不是「有改动吗」——
 * 后者在一个正干活的仓库里永远为真，那个点就等于一直亮着，没有信息量。
 */
export interface GitDirty { root: string; files: number; sig: string }

/** 一行的完整文本：高亮过的行只发 segs（不再重复发一份整行） */
export const lineText = (l: DiffLine) => (l.segs ? l.segs.map((x) => x.s).join('') : (l.s ?? ''))

/**
 * 看 diff 的三个口，**全都是只读的**：这一层不给 add / commit / checkout 留任何入口 ——
 * 会改仓库的事在终端里做，那儿有完整的 git，还看得见输出。
 */
export const gitApi = {
  /** 这批目录里哪些是 git 仓库（拿各个 pane 的 cwd 来问）。认不出来的**不报错**，只是不出现 */
  repos: (dirs: string[]) =>
    api.get<{ repos: GitRepo[] }>('/git/repos?' + dirs.map((d) => `dir=${encodeURIComponent(d)}`).join('&')),
  /** 角标（几个文件改了 + 指纹）。服务端那边有 2 秒缓存，可以按秒问 */
  dirty: (dir: string) => api.get<GitDirty>(`/git/dirty?dir=${encodeURIComponent(dir)}`),
  status: (dir: string, mode: DiffMode) =>
    api.get<GitStatus>(`/git/status?dir=${encodeURIComponent(dir)}&mode=${mode}`),
  diff: (q: { dir: string; mode: DiffMode; path: string; old?: string; untracked?: boolean; context?: number; limit?: number }) =>
    api.get<GitPatch>(
      `/git/diff?dir=${encodeURIComponent(q.dir)}&mode=${q.mode}&path=${encodeURIComponent(q.path)}`
      + (q.old ? `&old=${encodeURIComponent(q.old)}` : '')
      + (q.untracked ? '&untracked=1' : '')
      + (q.context ? `&context=${q.context}` : '')
      + (q.limit ? `&limit=${q.limit}` : ''),
    ),
}

/** chat 模式：一条对话流。服务端那份在 internal/transcript（`Msg` / `Log`） */
export type ChatKind = 'human' | 'agent' | 'think' | 'tool' | 'notice'

/** agent 在问你一个带选项的问题（claude 的 AskUserQuestion）。字段跟着那个工具的输入走 */
export interface ChatAsk {
  questions: {
    /** 问题上面那一行短标签 */
    header?: string
    question: string
    /** 多选。**能不能一键作答看它** —— 多选在 TUI 里是空格勾选再回车，按键序列不一样 */
    multi?: boolean
    options: { label: string; description?: string }[]
    /** 人当时选了哪个（选项的 label）。空 = 还没答 */
    picked?: string
  }[]
}

export interface ChatMsg {
  id: string
  kind: ChatKind
  text?: string
  /** 工具名（kind === 'tool'） */
  tool?: string
  /** 工具的一行摘要（跑的命令 / 改的文件 / 搜的词）—— 服务端已经压成一行、掐过长度 */
  meta?: string
  /** 工具成没成。**undefined = 还不知道**（结果还没落盘）→ 画「正在跑」 */
  ok?: boolean
  /** 生成时刻。**只用来显示，不用来排序** —— 服务端按文件行序给，见 internal/transcript */
  at?: string
  /** 这条工具调用的 id（claude 的 `tool_use_id`）。**结果到了之后靠它认回来**，见 ChatLog.updates */
  ref?: string
  /**
   * 这条工具调用是「agent 在问你」。**只有这一种工具带完整负载**，别的只有 meta 那一行 ——
   * 因为被问住时人缺的恰恰是「有几个选项、第二个是什么」。
   */
  ask?: ChatAsk
}

export interface ChatLog {
  msgs: ChatMsg[]
  /** 这条会话的身份。**变了就是换了会话**（/clear、/resume、压缩），手上那份要整份丢掉 */
  sig: string
  /** 下次从这个字节偏移接着读。**往前翻的那种响应里这个值不能采纳**（见 chatApi.earlier） */
  next: number
  /** 这一批是从哪个字节偏移读起的。**往上翻更早的就拿它当 `before`** */
  start: number
  /** 上面还有更早的 */
  more?: boolean
  /**
   * **前面某几条的结果到了。**
   *
   * 工具成没成、提问选了哪个，这些是「结果那一行」带来的，而那一行常常落在**下一批**里
   * （流式过程中就是这样）。服务端只在同一批里能回填，所以跨批的靠这些补丁：拿 `ref`
   * 在自己手上那份里认回那条，打上去。
   *
   * 不打的表现是**显示的状态和事实相反**：工具永远「正在跑」、提问那张卡永远「没选」，
   * 只有刷新页面才对（用户报的）。
   */
  updates?: { ref: string; ok?: boolean; answers?: Record<string, string> }[]
  agent: string
  /** 转录文件名（只有文件名） */
  file: string
  /**
   * 这个 pane 此刻的 agent 状态（herdr 的 `agent_status`：idle / working / blocked / done）。
   *
   * **跟对话同一拍给**，不是另一条轮询 —— 服务端那个口本来就调了 `pane.get`。
   * 它是 chat 模式里唯一能说出「agent 正在干活」的东西：转录按「一次 API 请求」flush，
   * agent 想事情时文件一个字节都不动（实测 15.58 秒），那段时间对话流完全静止。
   */
  status?: string
  /** 服务端把 pane id 回一遍：换 pane 时上一拍的响应可能后到，靠它认出来丢掉 */
  pane?: string
  /**
   * 这一轮跑了多久 / 多少 token / 什么思考档。**只在 status 是 working 时有。**
   *
   * `secs` 是**服务端算出来的秒数**，不是时间戳 —— 转录里的时间戳是跑 agent 那台机器写的，
   * 而看页面的是手机，两边时钟差几分钟是常事，在前端减出来就是个看着像真的错数字。
   * 前端只负责把它往前走（见 ChatPanel 的 useTick）。
   */
  turn?: {
    /** 到**现在**跑了多久（秒）。在跑时看它 */
    secs?: number
    /** 这一轮从人说话到 agent 最后一次落笔用了多久（秒）。**跑完之后看它** */
    ran?: number
    /** agent 最后一次落笔的时刻（RFC3339）。按**本机时区**格式化成 `14:41` */
    doneAt?: string
    tokens?: number
    effort?: string
    model?: string
  }
  /** 这个会话里还有几个后台任务在跑（claude 那条状态行最后那截） */
  shells?: number
}

export const chatApi = {
  /**
   * 读某个 pane 的对话。`from` 是上一拍的 `next`（0 / 省略 = 整份重来，只给尾部那些）。
   *
   * 读不出来时抛 `ApiError`，`reason` 是机器判据（'need_install' / 'ambiguous'）——
   * 别按文案判断，见 ApiError 的注释。
   */
  log: (pane: string, from = 0, sig = '') =>
    api.get<ChatLog>(
      `/chat/log?pane=${encodeURIComponent(pane)}`
      + (from > 0 ? `&from=${from}` : '')
      // sig 是**手上那份的会话身份**：服务端拿它核一下，对不上就把偏移丢掉。
      // 不带的话 `/clear` 之后那一拍会拿旧文件的偏移去读新文件（从中间某处开始，
      // 前面那一截永远读不到，而且不报错）。
      + (sig ? `&sig=${encodeURIComponent(sig)}` : ''),
    ),

  /**
   * 往上翻更早的那一段：拿手上这批的 `start` 当 `before`。
   *
   * **响应里的 `next` 不能采纳** —— 那一批是往前翻出来的，而 `next` 指的是文件尾；
   * 拿它去盖手上那个「下次从哪儿接着读」，增量就会从中间某处重读一大段
   * （表现是消息成片重复）。这儿只取 `msgs` / `start` / `more`。
   */
  /**
   * 替人答那个选择框：**传选项序号，不传按键**。
   *
   * 键序列（↓ ×n + ↵）是服务端按序号算的 —— 这个口发不出别的任何东西。服务端还会先从
   * 转录里核一遍「此刻真有一个没答的提问」（不是看 `agent_status`，那个实测不可靠），
   * 核不过回 409 + `reason: 'not_pending'`。详见 internal/server/chatapi.go 的注释。
   */
  answer: (pane: string, index: number) =>
    api.post<{ pane: string; picked: string; keys: number }>('/chat/answer', { pane, index }),

  /**
   * 在一个**没有 agent 的** pane 里开一个 agent（往那个 pane 里敲命令名 + 回车）。
   *
   * `agent` 只认服务端那张白名单（现在是 claude / codex）—— 这个口发不出别的命令，
   * 要别的就在快捷键条上配一个 `text:xxx enter` 的键。
   *
   * **走的是 herdr 的 `pane.send_input`，不是终端那条 WebSocket** ——
   * chat 在终端断着时照旧能用，这个按钮不该跟着终端连接一起失效。
   * pane 里已经有 agent 时回 409 + `reason: 'has_agent'`（前端那份 pane 列表最多 3 秒旧）。
   */
  start: (pane: string, agent: 'claude' | 'codex') =>
    api.post<{ pane: string; agent: string }>('/chat/start', { pane, agent }),

  earlier: (pane: string, before: number) =>
    api.get<ChatLog>(`/chat/log?pane=${encodeURIComponent(pane)}&before=${before}`),
}

export interface SoftKey {
  id?: string         // 稳定标识，快捷键条按这个引用（服务端存盘时补齐）
  label: string
  /**
   * 占几格宽（1..MAX_SPAN，见 lib/keys.ts）。一格 = `--sk-w`。
   * 存「几格」而不是像素，是为了固定块里跨行对得齐 —— 见 `spanStyle` 的注释。
   */
  span?: number
  /** @deprecated span 的降级镜像（服务端按 span 现算下发）。新代码只读 span */
  wide?: boolean
  /**
   * 条上画哪个**内置图标**（空 = 画 `label` 那段文字）。清单在 `@/keyicons`。
   * `label` 照旧是**名字** —— 挑了图标它还在，只是条上不画字。
   */
  icon?: string
  /**
   * 图标摆在哪儿：`only`（默认，只画图标）/ `pre`（图标在前）/ `post`（图标在后）。
   * `^B 前缀` 这种键名字里那个 `B` 是有意义的，所以要能「图标 + 文字」一起画。
   */
  iconAt?: 'only' | 'pre' | 'post'
  confirm?: boolean   // 要点两下才发（防误触）
  send?: string    // 解析出来的字节（前端照发）
  spec?: string    // 用户写的按键谱（编辑器回显）
  sticky?: 'ctrl' | 'alt'
  /**
   * 网页端自己处理的动作，不发字节。剪贴板那两个是**两个键**：手机浏览器只在用户手势里
   * 给读 / 写剪贴板，所以「取」（机器剪贴板 → 手机）和「粘」（手机剪贴板 → 终端）各要
   * 用户自己点一下，合不成一个「同步」。
   *
   * 类型是**从那份清单推出来的**（`@/capabilities` 的 `KeyAct`，服务端同一张表在
   * `internal/capability`）——以前这儿手写一份联合类型，和服务端那个白名单对不上时是
   * 完全静默的：键点下去什么都不发生。
   */
  act?: KeyAct
  /**
   * 弹出组：这个键在条上**只占一格**，点一下在它旁边弹一小片网格，里面才是那几个键。
   * 方向键盘就该是这个 —— 摊在条上要 3×2 六格，手机竖屏上那是半条屏幕。
   *
   * 这是**原始形状**（格子里是 ID），给编辑器用。渲染看下面那个 `members`。
   */
  group?: { cols: number; cells: string[] }
  /**
   * `group` 解析好的成员（`libMap` 填上；`null` = 空格子）。**渲染只看这个**。
   * 组里不能再放组（服务端挡着），所以解析一遍就够，不用递归。
   */
  members?: (SoftKey | null)[]
}
export interface PresetGroup { group: string; items: SoftKey[] }

/**
 * 一行两端**钉住**几个键（不跟着横滑）。
 *
 * 存的是**个数**不是另一份列表：`bar` 那一行照旧是**完整顺序**，头 `left` 个钉左、
 * 尾 `right` 个钉右、中间那段跟着滑。降级到只认 bar 的老版本读出来是同样那些键
 * （只是全都跟着滑），不会「钉住的那几个不见了」。
 */
export interface Pin { left?: number; right?: number }

/** 一行解析好的三段 */
export interface RowSegments { left: SoftKey[]; scroll: SoftKey[]; right: SoftKey[] }

export interface SoftkeysConfig {
  rows: 1 | 2
  lib: SoftKey[]
  bar: string[][]
  /** 每行两端钉住几个（缺 = 都不钉）。见 Pin */
  pin?: Pin[] | null
  /** 这一份是**哪一套**排布的（服务端算出来的，不是请求里那个）—— 编辑器照它写标题 */
  profile?: string
}
export interface SoftkeysResponse extends SoftkeysConfig {
  max: number
  maxBar: number
  presets: PresetGroup[]
}

/**
 * 顶栏配置：`items` 是**一串按钮 id**（顺序就是顶栏上的顺序），按钮长什么样在
 * `components/topbarItems.tsx`。`actions` 是服务端认的全部内置 id、`pinned` 是不能删的
 * 那几个（设置 ⚙ —— 删了就没路回来改配置了），`max` 是上限。
 *
 * items 里还可以放 `key:<定义ID>` —— 那不是内置按钮，是「我的按键」里的一个定义
 * （见 `TOPBAR_KEY` 和服务端 internal/topbar 的包注释）。`actions` 里**不列这些**：
 * 那份是内置白名单，引用的合法性靠「定义在不在」判。
 */
export interface TopbarResponse { items: string[]; actions: string[]; pinned: string[]; max: number; profile?: string }

/**
 * 顶栏上「这一项是引用，不是内置按钮」的记号：`key:k3` 指向「我的按键」里 ID 为 k3 的定义。
 * 和服务端 `topbar.KeyPrefix` 是同一个字符串。
 */
export const TOPBAR_KEY = 'key:'

/**
 * 认「这一项是不是引用」，给出被引用的定义 ID（不是引用就给 null）。
 *
 * 只查形状，**不查定义在不在** —— 那一步在渲染的地方做（拿 `libMap` 查不到就整项跳过，
 * 和 `resolveBar` 一个做法）。服务端读盘也不核，理由见 internal/topbar 的包注释。
 */
export function topbarKeyRef(item: string): string | null {
  return item.startsWith(TOPBAR_KEY) ? item.slice(TOPBAR_KEY.length) || null : null
}

/**
 * 一套排布（profile）。**装的是「这类设备上怎么排」**：快捷键条几行 / 哪些键、顶栏放哪几个、
 * 外加几个小开关（见 lib/prefs.ts）。「我的按键」那些定义是全局的，不在这里面 ——
 * 理由见 internal/profiles 的包注释。
 */
export interface Profile { id: string; name: string; kind?: DeviceKind; prefs?: Record<string, string> }

/** 一个浏览器（一台设备上的一个浏览器）绑在哪一套上。label 是服务端从 UA 猜的 */
export interface ProfileInstall { id: string; label?: string; profile: string; lastSeen?: string; me?: boolean }

export interface ProfilesResponse {
  profiles: Profile[]
  /** 这台设备该用哪一套 */
  current: string
  /** current 那一套的开关（键在 lib/prefs.ts 的 PREF_KEYS 里） */
  prefs?: Record<string, string>
  installs: ProfileInstall[]
  max: number
  maxName: number
}

/**
 * 「我的按键」按 ID 索引。快捷键条的 bar、固定块的格子、顶栏的 `key:` 引用都靠它落到定义上。
 *
 * 顺手把**弹出组的格子解析成成员**（`members`）：组里放的是引用，而渲染的地方（快捷键条 /
 * 顶栏）拿到的是一个个已经解析好的键、手上没有整份 lib。组里不能再放组，所以第二遍就够。
 */
export function libMap(lib: SoftKey[]): Map<string, SoftKey> {
  const raw = new Map(lib.filter((k) => !!k.id).map((k) => [k.id!, k]))
  const out = new Map<string, SoftKey>()
  for (const [id, k] of raw) {
    out.set(id, k.group ? { ...k, members: k.group.cells.map((c) => raw.get(c) ?? null) } : k)
  }
  return out
}

/**
 * 把 bar 的每一行切成**钉左 / 跟着滑 / 钉右**三段（id 换成真的定义）。
 *
 * 认不出的 id 直接跳过 —— 那会让行变短，所以个数在这儿也要夹一下（服务端读的时候也夹，
 * 见 resolvePin；这儿再夹是因为前端丢弃的那几个服务端未必丢）。
 */
export function resolveRows(lib: SoftKey[], bar: string[][], pin?: Pin[] | null): RowSegments[] {
  const by = libMap(lib)
  return bar.map((row, i) => {
    const keys = row.map((id) => by.get(id)).filter((k): k is SoftKey => !!k)
    let l = Math.max(0, pin?.[i]?.left ?? 0)
    let r = Math.max(0, pin?.[i]?.right ?? 0)
    if (l + r > keys.length) {
      // 左边优先：钉住的第一个多半是「呼键盘」那种最要紧的
      l = Math.min(l, keys.length)
      r = keys.length - l
    }
    return { left: keys.slice(0, l), scroll: keys.slice(l, keys.length - r), right: keys.slice(keys.length - r) }
  })
}

/** 把 bar 里的 id 换成真的按键定义。认不出的 id 直接跳过（服务端不该给出这种，防一手） */
export function resolveBar(lib: SoftKey[], bar: string[][]): SoftKey[][] {
  const by = libMap(lib)
  return bar.map((row) => row.map((id) => by.get(id)).filter((k): k is SoftKey => !!k))
}
