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

  /**
   * 要不要画那个 passkey 按钮。**三个条件缺一不可**，最后那个是漏掉过的：
   * `passkeyAvailable` 是**按当前 origin 算**的（裸 IP 上为 false，见 auth.UsableOn）——
   * 少了它，从局域网直连那条 IP 路进来的人会看到一个按下去必然报错的按钮（服务端在
   * passkeyGate 那儿一律 409）。一个必然失败的按钮比没有按钮糟得多：人会以为是自己
   * 指纹没录好，反复试。下面页脚那句会告诉他该换哪个地址。
   */
  const canPasskey = !!who?.passkeys && who.passkeyAvailable && passkeySupported()
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
            /* 只留「怎么进去」。这道关卡为什么存在（把凭据被偷的窗口压到一天）是设计理由，
               不是站在门口的人此刻要读的东西 —— 写在这儿只是把那一行按钮往下推。
               要讲的地方是 SECURITY.md 和设置里 passkey 那一段。 */
            <>这台设备配过对，只是太久没做过生物验证了。</>
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

        {/* 页脚只放「站在门口的人用得上」的话。
            「只有坐在机器前的人能出码」原来也在这儿 —— 那是这套设计**对谁的承诺**（写在
            SECURITY.md 里），可对着这一页的人来说是一句他做不了什么的话：想进去的人手上
            要么有码要么没有，读完这句还是那样。 */}
        <p className="mt-5 border-t border-line pt-4 text-xs leading-relaxed text-faint">
          配对码 5 分钟过期、用一次就废，所以截图和二维码被拍走都没有长期风险。
          {who && !who.passkeyAvailable && (
            <>
              <br />
              这个地址上用不了 passkey（裸 IP 不能当 WebAuthn 的标识）——{' '}
              {/* 知道确切地址就把地址给出来：「换用域名那条路访问」这句话，站在手机前的人
                  常常答不上来那条路是什么（域名是部署时配的）。空的时候才退回泛泛地讲，
                  见 lib/api.ts 上 passkeyURL 那条注释。 */}
              {who.passkeyURL ? (
                <>
                  换{' '}
                  <a className="text-brand underline underline-offset-2" href={who.passkeyURL}>
                    {who.passkeyURL}
                  </a>{' '}
                  访问就有。
                </>
              ) : (
                <>换用域名那条路访问就有。</>
              )}
            </>
          )}
        </p>
      </div>
    </div>
  )
}
