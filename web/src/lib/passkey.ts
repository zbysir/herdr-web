import { api } from './api'

/**
 * WebAuthn 的浏览器侧胶水。
 *
 * 服务端（go-webauthn）用 base64url 传 challenge / credential id 这些字节串，而浏览器 API
 * 要的是 ArrayBuffer，返回的又是 ArrayBuffer —— 所以两个方向都得转一遍。
 *
 * 新浏览器有 `PublicKeyCredential.toJSON()` 能省掉这段，但 Safari 是 18 才有，
 * 手上的 iPad 不一定到，所以手写。
 */

function fromB64u(s: string): Uint8Array {
  const b = atob(s.replace(/-/g, '+').replace(/_/g, '/'))
  return Uint8Array.from(b, (c) => c.charCodeAt(0))
}

function toB64u(b: ArrayBuffer): string {
  let s = ''
  for (const byte of new Uint8Array(b)) s += String.fromCharCode(byte)
  return btoa(s).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

export const passkeySupported = () =>
  typeof PublicKeyCredential !== 'undefined' && !!navigator.credentials

/** 用户取消（Face ID 划掉、点了取消）不是错误，别弹红字。 */
export function isCancel(e: unknown) {
  const n = (e as { name?: string } | null)?.name
  return n === 'NotAllowedError' || n === 'AbortError'
}

// 服务端下发的是 JSON 形态（字节串都是 base64url 字符串），和浏览器 API 要的结构
// 只差那几个字段，所以单独描述一下，别去和 DOM 的类型硬碰。
interface CreateJSON {
  challenge: string
  user: { id: string; name: string; displayName: string }
  excludeCredentials?: { id: string; type: string }[]
  [k: string]: unknown
}
interface RequestJSON {
  challenge: string
  allowCredentials?: { id: string; type: string }[]
  [k: string]: unknown
}
interface Begin<T> {
  options: { publicKey: T }
  ceremony: string
}

/** 注册一把新 passkey（要求当前设备已经认证过）。 */
export async function registerPasskey(): Promise<string> {
  const { options, ceremony } = await api.post<Begin<CreateJSON>>('/auth/passkey/register/begin')
  const pk = options.publicKey

  const req = {
    ...pk,
    challenge: fromB64u(pk.challenge),
    user: { ...pk.user, id: fromB64u(pk.user.id) },
    excludeCredentials: (pk.excludeCredentials ?? []).map((c) => ({ ...c, id: fromB64u(c.id) })),
  } as unknown as PublicKeyCredentialCreationOptions

  const cred = (await navigator.credentials.create({ publicKey: req })) as PublicKeyCredential | null
  if (!cred) throw new Error('浏览器没有返回凭据')
  const att = cred.response as AuthenticatorAttestationResponse

  const r = await api.post<{ label: string }>(
    `/auth/passkey/register/finish?c=${encodeURIComponent(ceremony)}`,
    {
      id: cred.id,
      rawId: toB64u(cred.rawId),
      type: cred.type,
      clientExtensionResults: cred.getClientExtensionResults(),
      response: {
        clientDataJSON: toB64u(att.clientDataJSON),
        attestationObject: toB64u(att.attestationObject),
      },
    },
  )
  return r.label
}

/**
 * 用 passkey 登录 / 重新验证。
 *
 * 走的是 discoverable（无用户名）流程，所以手机上就是「点一下 → Face ID」，
 * 不用先说自己是谁。这也是「换新设备不用回机器前」的那条路。
 */
export async function loginPasskey(): Promise<string> {
  const { options, ceremony } = await api.post<Begin<RequestJSON>>('/auth/passkey/login/begin')
  const pk = options.publicKey

  const req = {
    ...pk,
    challenge: fromB64u(pk.challenge),
    allowCredentials: (pk.allowCredentials ?? []).map((c) => ({ ...c, id: fromB64u(c.id) })),
  } as unknown as PublicKeyCredentialRequestOptions

  const cred = (await navigator.credentials.get({ publicKey: req })) as PublicKeyCredential | null
  if (!cred) throw new Error('浏览器没有返回凭据')
  const asr = cred.response as AuthenticatorAssertionResponse

  const r = await api.post<{ label: string }>(
    `/auth/passkey/login/finish?c=${encodeURIComponent(ceremony)}`,
    {
      id: cred.id,
      rawId: toB64u(cred.rawId),
      type: cred.type,
      clientExtensionResults: cred.getClientExtensionResults(),
      response: {
        clientDataJSON: toB64u(asr.clientDataJSON),
        authenticatorData: toB64u(asr.authenticatorData),
        signature: toB64u(asr.signature),
        userHandle: asr.userHandle ? toB64u(asr.userHandle) : undefined,
      },
    },
  )
  return r.label
}

export interface PasskeyInfo {
  id: string
  label: string
  created: string
  lastUsed: string
}
