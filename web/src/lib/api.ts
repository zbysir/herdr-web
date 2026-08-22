// 所有接口都在 /api 下。
//
// 认证走 **HttpOnly cookie**（服务端下发，JS 读不到也改不了），所以 URL 里不再有任何
// 秘密 —— 书签、浏览器历史、云同步、截图都不再是泄露渠道。凭据绑设备不绑 IP，所以
// 换 Wi-Fi、换网段都不掉线。一台设备配一次，见 SECURITY.md。
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
 * 拿到它最多只能说「我是那台平板」，然后读到那台平板的软键条排布。
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
// install 同理挂在每个请求上：软键条 / 顶栏的 GET 靠它算出「这台设备用哪一套」，不带
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

async function handle<T>(r: Response): Promise<T> {
  const j = await r.json().catch(() => ({ error: `HTTP ${r.status}` }))
  if (r.status === 401) {
    const need = (j as { need?: 'passkey' }).need
    dispatchEvent(new CustomEvent<UnauthedDetail>(UNAUTHED, { detail: { need } }))
  }
  if (!r.ok) throw new Error((j as { error?: string }).error ?? `HTTP ${r.status}`)
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
  passkeyAvailable: boolean // 这个部署能不能用（裸 IP 访问时为 false）
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
  workspace: string
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
   * 空的时候只报状态，别硬凑一句话（编内容比空着糟得多）。
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

export interface SoftKey {
  id?: string         // 稳定标识，软键条按这个引用（服务端存盘时补齐）
  label: string
  wide?: boolean
  confirm?: boolean   // 要点两下才发（防误触）
  send?: string    // 解析出来的字节（前端照发）
  spec?: string    // 用户写的按键谱（编辑器回显）
  sticky?: 'ctrl' | 'alt'
  /**
   * 网页端自己处理的动作，不发字节。剪贴板那两个是**两个键**：手机浏览器只在用户手势里
   * 给读 / 写剪贴板，所以「取」（机器剪贴板 → 手机）和「粘」（手机剪贴板 → 终端）各要
   * 用户自己点一下，合不成一个「同步」。
   */
  act?: 'kbd' | 'img' | 'panes' | 'clip' | 'paste' | 'files'
}
export interface PresetGroup { group: string; items: SoftKey[] }
/**
 * 软键条配置分两半（见服务端 internal/softkeys 的包注释）：
 * `lib` 是「我的按键」的定义，`bar` 每行是一串指向 lib 的 **id**。
 * 存 id 才能做到「同一个键两行各放一个」「改一处定义条上全变」。
 */
export interface SoftkeysConfig {
  rows: 1 | 2
  lib: SoftKey[]
  bar: string[][]
  /** 这一份是**哪一套**排布的（服务端算出来的，不是请求里那个）—— 编辑器照它写标题 */
  profile?: string
}
export interface SoftkeysResponse extends SoftkeysConfig { max: number; maxBar: number; presets: PresetGroup[] }

/**
 * 顶栏配置：`items` 是**一串按钮 id**（顺序就是顶栏上的顺序），按钮长什么样在
 * `components/topbarItems.tsx`。`actions` 是服务端认的全部 id、`pinned` 是不能删的那几个
 * （设置 ⚙ —— 删了就没路回来改配置了），`max` 是上限。
 */
export interface TopbarResponse { items: string[]; actions: string[]; pinned: string[]; max: number; profile?: string }

/**
 * 一套排布（profile）。**装的是「这类设备上怎么排」**：软键条几行 / 哪些键、顶栏放哪几个、
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

/** 把 bar 里的 id 换成真的按键定义。认不出的 id 直接跳过（服务端不该给出这种，防一手） */
export function resolveBar(lib: SoftKey[], bar: string[][]): SoftKey[][] {
  const by = new Map(lib.map((k) => [k.id, k]))
  return bar.map((row) => row.map((id) => by.get(id)).filter((k): k is SoftKey => !!k))
}
