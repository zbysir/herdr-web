// 所有接口都在 /api 下，统一用 ?token= 认证。token 从 URL 里来（手机扫码进来就带着）。
export const TOKEN = new URLSearchParams(location.search).get('token') ?? ''

function url(path: string) {
  const sep = path.includes('?') ? '&' : '?'
  return `/api${path}${sep}token=${encodeURIComponent(TOKEN)}`
}

async function req<T>(method: string, path: string, body?: unknown): Promise<T> {
  const r = await fetch(url(path), {
    method,
    headers: body === undefined ? undefined : { 'content-type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  const j = await r.json().catch(() => ({ error: `HTTP ${r.status}` }))
  if (!r.ok) throw new Error((j as { error?: string }).error ?? `HTTP ${r.status}`)
  return j as T
}

export const api = {
  get: <T>(p: string) => req<T>('GET', p),
  post: <T>(p: string, b?: unknown) => req<T>('POST', p, b),
  put: <T>(p: string, b?: unknown) => req<T>('PUT', p, b),
  del: <T>(p: string) => req<T>('DELETE', p),
  /** 上传裸字节（图片），不走 JSON。 */
  async upload(blob: Blob) {
    const r = await fetch(url('/herdr/upload'), {
      method: 'POST',
      headers: { 'content-type': blob.type || 'application/octet-stream' },
      body: blob,
    })
    const j = await r.json().catch(() => ({ error: `HTTP ${r.status}` }))
    if (!r.ok) throw new Error((j as { error?: string }).error ?? `HTTP ${r.status}`)
    return j as UploadResult
  },
}

/* ------------------------------------------------------------------ 形状 */

export const FOLLOW = '__focused'

export interface State {
  shell: string
  user: string
  hostname: string
  secureContext: boolean
  compose: { pollMs: number; pushMs: number; settleMs: number }
  herdrSocket: string
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

export interface SoftKey {
  label: string
  wide?: boolean
  confirm?: boolean   // 要点两下才发（防误触）
  send?: string    // 解析出来的字节（前端照发）
  spec?: string    // 用户写的按键谱（编辑器回显）
  sticky?: 'ctrl' | 'alt'
  act?: 'kbd'
}
export interface PresetGroup { group: string; items: SoftKey[] }
export interface SoftkeysResponse { keys: SoftKey[]; max: number; presets: PresetGroup[] }
