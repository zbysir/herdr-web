import { useEffect, useRef, useState } from 'react'
import { X, TriangleAlert, Check } from 'lucide-react'
import { api, ApiError, type WhoAmI } from '@/lib/api'
import { isCancel, loginPasskey, passkeySupported } from '@/lib/passkey'
import { cn } from '@/lib/utils'
import { Button } from './ui/button'
import { Input } from './ui/input'
import { Logo } from './Logo'

/**
 * 「批准命令行登录」：另一台电脑上跑 `herdr-web connect`，它显示一个码，人在这儿**手输**
 * 进来、看清是谁、刷一次 passkey（流程见 internal/auth/cliapprove.go）。
 *
 * **单独一个全屏页，不在设置里**（用户点名的）：这是一次授权 —— 给一台电脑一个登录 shell ——
 * 和配对页是同一个分量，塞在设置底下那一小块里既看不清发起方是谁，也显得像个无关紧要的开关。
 * 所以长得和配对页一样（那张卡片就是「门」），分三步：输码 → 看清是谁再批准 → 结果。
 *
 * 三条别改：
 *   - **码必须手输，不从 URL 里带进来**。`/?cli` 只负责把这一页打开 —— 一条「点开就
 *     填好码」的链接正是钓鱼要的东西（发起请求谁都能做，包括这台机器上被注入的 agent，
 *     它只差让你帮它点一下）。
 *   - **批准前先 peek**，把发起方的主机名 / IP / 新设备还是重验摊出来给人看；发起方就是
 *     这台机器本身时显式警告（正经用法是在**别的**电脑上跑 connect）。
 *   - 批准要**当场**刷 passkey：服务端回 403 `stepup` 时这儿刷一次再重试一次（同
 *     registerPasskey 那条；只重试一次，第二次还被挡说明刷那一下没真成）。
 */

/** 这一页是不是从 `/?cli` 进来的（`herdr-web connect` 打印的就是这条）。模块加载时读一次 */
export const CLI_ASKED = new URLSearchParams(location.search).has('cli')

interface Req {
  label: string
  ip: string
  local: boolean
  device: string
  deviceLabel?: string
  expires: string
}

type Step = { at: 'code' } | { at: 'confirm'; req: Req } | { at: 'done'; ok: boolean; reauth: boolean }

/** 8 位码按 4-4 显示（和终端里打出来的一个样子），存的时候去掉分隔 */
const fmt = (v: string) => {
  const c = v.replace(/[^0-9a-z]/gi, '').toUpperCase().slice(0, 8)
  return c.length > 4 ? `${c.slice(0, 4)}-${c.slice(4)}` : c
}

export function CliApprovePage({ onClose }: { onClose: () => void }) {
  const [code, setCode] = useState('')
  const [step, setStep] = useState<Step>({ at: 'code' })
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  const [who, setWho] = useState<WhoAmI | null>(null)
  const box = useRef<HTMLInputElement>(null)

  useEffect(() => {
    void api.get<WhoAmI>('/auth/whoami').then(setWho).catch(() => {})
  }, [])
  useEffect(() => { if (step.at === 'code') box.current?.focus() }, [step.at])
  // Esc 关掉（桌面上）
  useEffect(() => {
    const k = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose() }
    addEventListener('keydown', k)
    return () => removeEventListener('keydown', k)
  }, [onClose])

  const raw = code.replace(/-/g, '')

  const peek = async (c = raw) => {
    if (c.length !== 8 || busy) return
    setErr('')
    setBusy(true)
    try {
      setStep({ at: 'confirm', req: await api.post<Req>('/auth/cli/peek', { code: c }) })
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const approve = async (req: Req) => {
    setErr('')
    setBusy(true)
    try {
      try {
        await api.post('/auth/cli/approve', { code: raw })
      } catch (e) {
        if (!(e instanceof ApiError && e.reason === 'stepup')) throw e
        await loginPasskey() // 服务端据此刷新这台的 PasskeyAt
        await api.post('/auth/cli/approve', { code: raw })
      }
      setStep({ at: 'done', ok: true, reauth: !!req.device })
    } catch (e) {
      if (!isCancel(e)) setErr((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const deny = async (req: Req) => {
    setErr('')
    try {
      await api.post('/auth/cli/deny', { code: raw })
      setStep({ at: 'done', ok: false, reauth: !!req.device })
    } catch (e) {
      setErr((e as Error).message)
    }
  }

  // 这个地址上刷不了 passkey（裸 IP），批准按钮按下去必然失败 —— 提前说，别让人白刷
  const canPasskey = passkeySupported() && (who ? who.passkeyAvailable : true)

  return (
    <div className="fixed inset-0 z-50 grid place-items-center overflow-auto bg-bg p-5">
      <Button variant="ghost" size="icon" className="absolute top-3 right-3" onClick={onClose} aria-label="关闭">
        <X className="size-5" />
      </Button>
      <div className="w-full max-w-[420px] rounded-card border border-line bg-bar p-5
                      shadow-[0_24px_60px_-16px_rgba(0,0,0,.6)]">
        <Logo size={40} className="mb-3" />
        <h1 className="mb-1.5 text-[17px] font-medium tracking-tight">批准命令行登录</h1>

        {step.at === 'code' && (
          <>
            <p className="mb-4 text-[13px] leading-relaxed text-muted
                          [&_code]:rounded [&_code]:border [&_code]:border-line [&_code]:bg-ctl
                          [&_code]:px-1.5 [&_code]:py-0.5 [&_code]:font-mono [&_code]:text-xs [&_code]:text-fg">
              在另一台电脑上跑 <code>herdr-web connect</code> 时，终端里会显示一个 8 位码，把它输到这里。
            </p>
            <div className="flex gap-2">
              <Input
                ref={box}
                value={code}
                inputMode="text"
                autoCapitalize="characters"
                autoComplete="off"
                spellCheck={false}
                placeholder="XXXX-XXXX"
                aria-label="命令行显示的码"
                className={cn('flex-1 text-center text-base uppercase', code && 'tracking-[.25em]')}
                onChange={(e) => {
                  const v = fmt(e.target.value)
                  setCode(v)
                  setErr('')
                  // 输够 8 位就自己去查 —— 手机上少点一下
                  const c = v.replace(/-/g, '')
                  if (c.length === 8) void peek(c)
                }}
                onKeyDown={(e) => { if (e.key === 'Enter') void peek() }}
              />
              <Button variant="primary" disabled={busy || raw.length !== 8} onClick={() => void peek()}>
                {busy ? '…' : '下一步'}
              </Button>
            </div>
            <p className="mt-4 border-t border-line pt-3 text-xs leading-relaxed text-faint">
              <b className="font-medium text-muted">只有你自己刚在那台电脑上跑了它，才输码批准。</b>
              谁让你「帮忙输一下这个码」都别理 —— 批准了就是给那台电脑一个能登录 shell 的凭据。
            </p>
          </>
        )}

        {step.at === 'confirm' && (
          <>
            <p className="mb-3 text-[13px] leading-relaxed text-muted">看清是哪台电脑再批准：</p>
            <dl className="mb-4 grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 rounded-md border border-line bg-bg px-3 py-2.5 text-[13px]">
              <dt className="text-faint">设备</dt>
              <dd className="m-0 truncate font-medium text-fg">{step.req.label}</dd>
              <dt className="text-faint">来自</dt>
              <dd className="m-0 truncate font-mono text-muted">{step.req.ip || '—'}</dd>
              <dt className="text-faint">做什么</dt>
              <dd className="m-0 text-muted">
                {step.req.device
                  ? <>给已有的「{step.req.deviceLabel || step.req.device}」重新验证</>
                  : '登录一台新设备（批准后出现在设备列表里）'}
              </dd>
            </dl>
            {step.req.local && (
              <p className="mb-4 flex gap-2 rounded-md border border-warn/40 bg-warn/10 px-3 py-2 text-xs leading-relaxed text-warn">
                <TriangleAlert className="mt-px size-4 shrink-0" />
                <span>
                  这个请求是从跑 herdr-web 的<b>这台机器本身</b>发起的。你没在这台机器上跑 connect 的话别批准
                  —— 那可能是某个 agent 在让你替它开门。
                </span>
              </p>
            )}
            <Button
              variant="primary"
              className="w-full py-2.5"
              disabled={busy || !canPasskey}
              onClick={() => void approve(step.req)}
            >
              {busy ? '等 passkey…' : '用 passkey 批准'}
            </Button>
            {!canPasskey && (
              <p className="mt-1.5 text-xs text-muted">
                这个地址上用不了 passkey（裸 IP 不能当 WebAuthn 的标识）
                {who?.passkeyURL ? <> —— 换 <a className="text-brand underline underline-offset-2" href={`${who.passkeyURL.replace(/\/$/, '')}/?cli`}>{who.passkeyURL}</a> 打开这一页。</> : '，换用域名那条路打开这一页。'}
              </p>
            )}
            <div className="mt-2 flex gap-2">
              <Button variant="danger" className="flex-1" disabled={busy} onClick={() => void deny(step.req)}>拒绝</Button>
              <Button className="flex-1" disabled={busy} onClick={() => { setStep({ at: 'code' }); setCode(''); setErr('') }}>换一个码</Button>
            </div>
          </>
        )}

        {step.at === 'done' && (
          <>
            <p className={cn('mb-4 flex items-center gap-2 text-[15px]', step.ok ? 'text-brand' : 'text-muted')}>
              {step.ok ? <Check className="size-5" /> : <X className="size-5" />}
              {step.ok ? (step.reauth ? '已批准，那台设备续上了' : '已批准') : '已拒绝'}
            </p>
            <p className="mb-4 text-[13px] leading-relaxed text-muted">
              {step.ok ? '命令行那边会自己连上，这一页可以关了。' : '命令行那边会收到「被拒绝」，这个码也作废了。'}
            </p>
            <Button className="w-full py-2.5" onClick={onClose}>关闭</Button>
          </>
        )}

        {err && <p className="mt-3 text-[13px] text-bad">{err}</p>}
      </div>
    </div>
  )
}
