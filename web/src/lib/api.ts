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

function url(path: string) {
  if (!TOKEN) return `/api${path}`
  const sep = path.includes('?') ? '&' : '?'
  return `/api${path}${sep}token=${encodeURIComponent(TOKEN)}`
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
  herdrSocket: string
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

/**
 * 「跳到某个 pane」的结果。
 *
 * zoomed 是**整个 tab** 的放大状态，不是这个 pane 的（herdr 放大的永远是当前焦点
 * pane）；singlePane 是「这个 tab 只有一个 pane，没什么可放大的」—— 跟「放大失败」
 * 不是一回事，得分开说，不然用户以为按钮没生效。
 */
export interface GotoResult { target: string; zoomed: boolean; focusChanged: boolean; singlePane?: boolean }

export interface SoftKey {
  id?: string         // 稳定标识，软键条按这个引用（服务端存盘时补齐）
  label: string
  wide?: boolean
  confirm?: boolean   // 要点两下才发（防误触）
  send?: string    // 解析出来的字节（前端照发）
  spec?: string    // 用户写的按键谱（编辑器回显）
  sticky?: 'ctrl' | 'alt'
  act?: 'kbd' | 'img' | 'panes' // 网页端自己处理，不发字节
}
export interface PresetGroup { group: string; items: SoftKey[] }
/**
 * 软键条配置分两半（见服务端 internal/softkeys 的包注释）：
 * `lib` 是「我的按键」的定义，`bar` 每行是一串指向 lib 的 **id**。
 * 存 id 才能做到「同一个键两行各放一个」「改一处定义条上全变」。
 */
export interface SoftkeysConfig { rows: 1 | 2; lib: SoftKey[]; bar: string[][] }
export interface SoftkeysResponse extends SoftkeysConfig { max: number; maxBar: number; presets: PresetGroup[] }

/** 把 bar 里的 id 换成真的按键定义。认不出的 id 直接跳过（服务端不该给出这种，防一手） */
export function resolveBar(lib: SoftKey[], bar: string[][]): SoftKey[][] {
  const by = new Map(lib.map((k) => [k.id, k]))
  return bar.map((row) => row.map((id) => by.get(id)).filter((k): k is SoftKey => !!k))
}
