import { useCallback, useEffect, useState } from 'react'
import { api, type WhoAmI } from '@/lib/api'
import { isCancel, loginPasskey, passkeySupported } from '@/lib/passkey'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { QrScan, qrScanSupported } from './QrScan'
import { Logo } from './Logo'

/**
 * 进门页。两种模式，长得像但含义不同：
 *
 *   - `pair`：这台设备从来没配过对。要一个配对码（只有坐在机器前的人能出）。
 *   - `reauth`：配过对，但太久没做过生物验证了。点一下 passkey 就行 —— 不用回机器前。
 *     这正是 passkey 的价值：它让「凭据活得久」和「凭据被偷就完蛋」这对矛盾解开了，
 *     所以会话凭据的寿命才敢从三个月压到一天。
 *
 * 两种模式都留着配对码输入框：passkey 丢了、或者这台设备上没有，总得有条回去的路。
 */
export function Pairing({
  mode = 'pair',
  hint,
  onDone,
}: {
  mode?: 'pair' | 'reauth'
  hint?: string
  onDone: () => void
}) {
  const [code, setCode] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState(hint ?? '')
  const [who, setWho] = useState<WhoAmI | null>(null)
  const [scan, setScan] = useState(false)
  // 页面内扫码要摄像头（只有安全上下文给）+ 平台自带的解码器，能用才出这个入口
  const canScan = qrScanSupported()

  useEffect(() => {
    void api
      .get<WhoAmI>('/auth/whoami')
      .then(setWho)
      .catch(() => {})
  }, [])

  const submit = async (raw: string) => {
    const c = raw.replace(/[\s-]/g, '').toUpperCase()
    if (c.length !== 8 || busy) return
    setBusy(true)
    setErr('')
    try {
      await api.post('/auth/pair', { code: c })
      onDone()
    } catch (e) {
      setErr((e as Error).message)
      setCode('')
    } finally {
      setBusy(false)
    }
  }

  const usePasskey = useCallback(async () => {
    setBusy(true)
    setErr('')
    try {
      await loginPasskey()
      onDone()
    } catch (e) {
      if (!isCancel(e)) setErr((e as Error).message)
    } finally {
      setBusy(false)
    }
  }, [onDone])

  const canPasskey = !!who?.passkeys && passkeySupported()
  const reauth = mode === 'reauth'

  return (
    <div className="absolute inset-0 z-20 grid place-items-center bg-bg p-5">
      {/* 装进一张卡片：这一页是整个应用的门，裸铺在画布上时看着像还没加载完 */}
      <div className="w-full max-w-[420px] rounded-card border border-line bg-bar p-5
                      shadow-[0_24px_60px_-16px_rgba(0,0,0,.6)]">
        <Logo size={40} className="mb-3" />
        <h1 className="mb-1.5 text-[17px] font-medium tracking-tight">
          {reauth ? '需要再验证一次' : '这台设备还没配对'}
        </h1>
        <p className="mb-4 text-[13px] leading-relaxed text-muted
                      [&_code]:rounded [&_code]:border [&_code]:border-line [&_code]:bg-ctl
                      [&_code]:px-1.5 [&_code]:py-0.5 [&_code]:font-mono [&_code]:text-xs [&_code]:text-fg">
          {reauth ? (
            <>
              这台设备配过对，只是太久没做过生物验证了。
              <br />
              <span className="text-xs">
                这道关卡的作用：就算凭据被偷走，能用的窗口也只有这么长。
              </span>
            </>
          ) : (
            <>
              在跑 herdr-web 的那台机器上执行{' '}
              <code>herdr-web pair</code>
              ，它会打出一个二维码和一个 8 位码。
              <br />
              <span className="text-xs">
                {canScan
                  ? '点下面「用相机扫」对准那个二维码；或者用这台设备的相机 App 扫（码里就是链接，扫完直接进），也可以把 8 位码抄到下面。'
                  : '用这台设备的相机 App 扫那个二维码（码里就是链接，扫完直接进），或者把 8 位码抄到下面。这一页扫不了 —— 摄像头只在 https 下可用，而且要浏览器自带二维码解码（iOS Safari 没有）。'}
              </span>
              <br />
              <span className="text-xs">一台设备配一次，之后这个书签直接进。</span>
            </>
          )}
        </p>

        {canPasskey && (
          <div className="mb-4">
            <Button
              variant="primary"
              disabled={busy}
              className="w-full py-2.5"
              onClick={() => void usePasskey()}
            >
              {busy ? '…' : reauth ? '用 passkey 验证' : '用 passkey 登录'}
            </Button>
            <p className="mt-1.5 text-xs text-muted">
              {reauth ? '一次 Face ID / 指纹就好。' : '不用回机器前 —— 这是 passkey 的主要好处。'}
            </p>
          </div>
        )}

        {scan && (
          <QrScan
            onCode={(c) => { setScan(false); void submit(c) }}
            onClose={() => setScan(false)}
          />
        )}

        {!reauth && canScan && !scan && (
          <Button className="mb-2 w-full py-2.5" disabled={busy} onClick={() => { setErr(''); setScan(true) }}>
            用相机扫
          </Button>
        )}

        <div className="flex gap-2">
          <Input
            autoFocus={!canPasskey}
            value={code}
            inputMode="text"
            autoCapitalize="characters"
            autoComplete="off"
            spellCheck={false}
            maxLength={11} // 8 位 + 用户可能自己加的连字符
            placeholder="配对码"
            aria-label="配对码"
            className={
              'flex-1 text-center text-base uppercase ' + (code ? 'tracking-[.35em]' : 'tracking-normal')
            }
            onChange={(e) => {
              setCode(e.target.value)
              // 抄够 8 位就自动提交 —— 手机上少点一下
              void submit(e.target.value)
            }}
            onKeyDown={(e) => {
              if (e.key === 'Enter') void submit(code)
            }}
          />
          <Button variant={canPasskey ? 'default' : 'primary'} disabled={busy} onClick={() => void submit(code)}>
            {busy ? '…' : reauth ? '重新配对' : '配对'}
          </Button>
        </div>

        {err && <p className="mt-3 text-[13px] text-bad">{err}</p>}

        <p className="mt-5 border-t border-line pt-4 text-xs leading-relaxed text-faint">
          配对码 5 分钟过期、用一次就废，所以截图和二维码被拍走都没有长期风险。
          <br />
          <strong>只有坐在机器前的人能出码</strong> —— 网页上（包括已经配过对的设备）都不行。
          {who && !who.passkeyAvailable && (
            <>
              <br />
              这个部署用不了 passkey：得用域名访问（裸 IP 不能当 WebAuthn 的标识）。
            </>
          )}
        </p>
      </div>
    </div>
  )
}
