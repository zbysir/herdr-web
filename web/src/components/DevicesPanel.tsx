import { useCallback, useEffect, useRef, useState } from 'react'
import { api, INSTALL, type Device, type Profile, type ProfileInstall, type ProfilesResponse } from '@/lib/api'
import { isCancel, passkeySupported, registerPasskey, type PasskeyInfo } from '@/lib/passkey'
import { Panel } from './ui/panel'
import { Button } from './ui/button'
import { Select } from './ui/select'

function ago(iso: string) {
  const d = (Date.now() - new Date(iso).getTime()) / 1000
  if (d < 60) return '刚刚'
  if (d < 3600) return `${Math.floor(d / 60)} 分钟前`
  if (d < 86400) return `${Math.floor(d / 3600)} 小时前`
  return `${Math.floor(d / 86400)} 天前`
}

// Go 的零值时间会序列化成 0001-01-01，那表示「永不过期」
function expiry(iso: string) {
  const t = new Date(iso)
  return t.getFullYear() < 2000 ? '永不过期' : `${t.toLocaleDateString()} 到期`
}

/**
 * 设备面板：看谁配过对、踢人、登出这台、给新设备要一个配对码。
 *
 * 「踢掉」和「全部踢掉」都要点两下才生效 —— 和软键条那套防误触一个道理：撤销之后
 * 那台设备下一个请求就 401，人不在机器前的话得重新配对，误触代价不小。
 *
 * 底下还有一节「哪台设备用哪一套排布」。为什么摆在这一页而不是另开一页：那就是**一张
 * 设备表**，人找它的时候想的是「设备」。而它最值钱的用法恰恰是改**别人**那台 ——
 * 手机上软键条排布调坏了的时候，那台手机自己反而是最难操作的一台。
 *
 * 注意这一节里的「设备」和上面那张表**不是同一个东西**：上面是配过对的凭据（auth 的
 * 设备 ID），这一节是浏览器自己生成的 installId —— 本机直连压根没有凭据那一层，而桌面
 * 上最常见的正是本机直连（见 internal/profiles 的包注释）。所以两张表的行对不上是正常的。
 */
export function DevicesPanel({
  onClose, toast, embedded, onProfiles,
}: {
  onClose?: () => void
  toast: (m: string) => void
  embedded?: boolean
  /** 改了**这台**设备绑哪一套：把整份响应交出去，App 那边当场把排布换过来 */
  onProfiles?: (r: ProfilesResponse) => void
}) {
  const [devs, setDevs] = useState<Device[]>([])
  const [me, setMe] = useState('')
  const [err, setErr] = useState('')
  const [keys, setKeys] = useState<PasskeyInfo[]>([])
  const [pkAvail, setPkAvail] = useState(false)
  /** 「哪台设备用哪一套排布」那一节 */
  const [profs, setProfs] = useState<Profile[]>([])
  const [insts, setInsts] = useState<ProfileInstall[]>([])
  const [armed, setArmed] = useState<string | null>(null)
  const disarm = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)

  const load = useCallback(async () => {
    try {
      const r = await api.get<{ devices: Device[]; me: string }>('/auth/devices')
      setDevs(r.devices ?? [])
      setMe(r.me ?? '')
      setErr('')
    } catch (e) {
      setErr((e as Error).message)
    }
    try {
      const r = await api.get<{ passkeys: PasskeyInfo[]; available: boolean }>('/auth/passkeys')
      setKeys(r.passkeys ?? [])
      setPkAvail(r.available)
    } catch { /* 老版本服务端没这个口，当没有就行 */ }
    try {
      const r = await api.get<ProfilesResponse>('/profiles')
      setProfs(r.profiles ?? [])
      setInsts(r.installs ?? [])
    } catch { /* 老版本服务端没这个口 */ }
  }, [])

  useEffect(() => {
    void load()
    return () => clearTimeout(disarm.current)
  }, [load])

  // 举起来 3 秒不点就放下
  const arm = (key: string) => {
    setArmed(key)
    clearTimeout(disarm.current)
    disarm.current = setTimeout(() => setArmed(null), 3000)
  }

  const kick = async (d: Device) => {
    if (armed !== d.id) return arm(d.id)
    setArmed(null)
    try {
      await api.del(`/auth/devices/${d.id}`)
      if (d.id === me) return location.reload() // 把自己踢了：回配对页
      toast(`已踢掉 ${d.label}`)
      void load()
    } catch (e) {
      setErr((e as Error).message)
    }
  }

  const kickAll = async () => {
    if (armed !== 'all') return arm('all')
    setArmed(null)
    try {
      await api.del('/auth/devices')
      location.reload()
    } catch (e) {
      setErr((e as Error).message)
    }
  }

  const logout = async () => {
    if (armed !== 'logout') return arm('logout')
    try {
      await api.post('/auth/logout')
    } catch { /* 凭据本来就没了也算登出成功 */ }
    location.reload()
  }

  const addPasskey = async () => {
    setErr('')
    try {
      const label = await registerPasskey()
      toast(`已添加 passkey：${label}`)
      void load()
    } catch (e) {
      if (!isCancel(e)) setErr((e as Error).message)
    }
  }

  const delPasskey = async (k: PasskeyInfo) => {
    if (armed !== 'pk' + k.id) return arm('pk' + k.id)
    setArmed(null)
    try {
      await api.del(`/auth/passkeys/${k.id}`)
      toast(`已删除 ${k.label}`)
      void load()
    } catch (e) {
      setErr((e as Error).message)
    }
  }

  /**
   * 把某台设备换到另一套排布上。
   *
   * 改的是**自己**这台时要把结果交给 App（onProfiles）：软键条和顶栏得当场换过去，
   * 不然界面上还是老那一套，而设置里已经写着新名字了。改别人那台不用 —— 那台设备
   * 下次打开页面自己会拿到。
   */
  const rebind = async (install: string, profile: string) => {
    setErr('')
    try {
      const r = await api.post<ProfilesResponse>('/profiles/bind', { install, profile })
      setInsts(r.installs ?? [])
      if (install === INSTALL) onProfiles?.(r)
      toast(`换成「${profs.find((p) => p.id === profile)?.name ?? profile}」了`)
    } catch (e) {
      setErr((e as Error).message)
    }
  }

  const label = (key: string, normal: string) => (armed === key ? '再点一次' : normal)

  const body = (
    <>
      {err && <p className="mb-2 text-xs text-bad">{err}</p>}

      <ul className="list-none p-0">
        {devs.length === 0 && <li className="text-muted">还没有设备。下面「加一台设备」出一个配对码。</li>}
        {devs.map((d) => (
          <li key={d.id} className="flex items-center gap-2 border-b border-line py-2.5 last:border-0">
            <div className="min-w-0 flex-1">
              <div className="truncate">
                {d.label}
                {d.id === me && (
                  <span className="ml-2 rounded border border-brand/40 bg-brand/12 px-1.5 py-px text-[11px] text-brand">
                    这台
                  </span>
                )}
              </div>
              <div className="text-xs text-muted">
                {ago(d.lastSeen)} · {d.lastIp || '—'} · {expiry(d.expires)}
              </div>
            </div>
            <Button
              variant={armed === d.id ? 'destructive' : 'danger'}
              size="tiny"
              onClick={() => void kick(d)}
              title="撤销这台设备的凭据，它下一个请求就会被拒"
            >
              {label(d.id, d.id === me ? '登出' : '踢掉')}
            </Button>
          </li>
        ))}
      </ul>

      {insts.length > 0 && profs.length > 0 && (
        <div className="mt-3 border-t border-line pt-2.5">
          <strong className="text-[13px] font-medium">哪台设备用哪一套排布</strong>
          <p className="mt-1 mb-1.5 text-xs/relaxed text-muted">
            软键条和顶栏的排布是按「套」存的（在设置最上面那一行选）。
            <b>在这儿能改别的设备那一套</b> —— 手机上排布调坏了的时候，那台手机自己最难操作。
          </p>
          <ul className="list-none p-0">
            {insts.map((it) => (
              <li key={it.id} className="flex items-center gap-2 py-1.5">
                <div className="min-w-0 flex-1">
                  <div className="truncate">
                    {it.label || '未知设备'}
                    {it.me && (
                      <span className="ml-2 rounded border border-brand/40 bg-brand/12 px-1.5 py-px text-[11px] text-brand">
                        这台
                      </span>
                    )}
                  </div>
                  <div className="text-xs text-muted">{it.lastSeen ? `${ago(it.lastSeen)}打开过` : '—'}</div>
                </div>
                <Select
                  className="shrink-0"
                  value={it.profile}
                  aria-label={`${it.label || '这台设备'}用哪一套排布`}
                  onChange={(e) => void rebind(it.id, e.target.value)}
                >
                  {profs.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
                </Select>
              </li>
            ))}
          </ul>
        </div>
      )}

      <div className="mt-3 border-t border-line pt-2.5">
        <div className="mb-1.5 flex items-center justify-between">
          <strong className="text-[13px] font-medium">passkey</strong>
          {pkAvail && passkeySupported() && (
            <Button size="tiny" onClick={() => void addPasskey()}>添加</Button>
          )}
        </div>
        {!pkAvail && (
          <p className="text-xs/relaxed text-muted">
            这个部署用不了：WebAuthn 要求标识是域名，用 IP 访问不行。让域名指到这台机器
            （内网地址也可以）就能用了。
          </p>
        )}
        {pkAvail && keys.length === 0 && (
          <p className="text-xs/relaxed text-muted">
            还没有。加一把之后：<b>换新设备不用回机器前</b>（同步的 passkey 在你所有设备上都有），
            而且会话凭据的寿命可以压到一天 —— 就算被偷走，能用的窗口也只有那么长。
          </p>
        )}
        <ul className="list-none p-0">
          {keys.map((k) => (
            <li key={k.id} className="flex items-center gap-2 py-1.5">
              <div className="min-w-0 flex-1">
                <div className="truncate">{k.label}</div>
                <div className="text-xs text-muted">
                  {new Date(k.created).toLocaleDateString()} 添加 · 最后用于 {ago(k.lastUsed)}
                </div>
              </div>
              <Button
                variant={armed === 'pk' + k.id ? 'destructive' : 'danger'}
                size="tiny"
                onClick={() => void delPasskey(k)}
              >
                {label('pk' + k.id, '删除')}
              </Button>
            </li>
          ))}
        </ul>
      </div>

      <div className="mt-3 flex flex-wrap items-center gap-2 border-t border-line pt-2.5">
        <Button variant={armed === 'logout' ? 'destructive' : 'default'} size="tiny" onClick={() => void logout()}>
          {label('logout', '登出这台')}
        </Button>
        <Button variant={armed === 'all' ? 'destructive' : 'danger'} size="tiny" onClick={() => void kickAll()}>
          {label('all', '全部踢掉')}
        </Button>
      </div>

      <p className="mt-3 border-t border-line pt-3 text-xs/relaxed text-muted
                    [&_code]:rounded [&_code]:border [&_code]:border-line [&_code]:bg-ctl
                    [&_code]:px-1 [&_code]:py-px [&_code]:font-mono [&_code]:text-[11px] [&_code]:text-fg
                    [&_strong]:font-medium [&_strong]:text-fg">
        凭据在 HttpOnly cookie 里、服务端只存哈希，绑设备不绑 IP —— 换 Wi-Fi 和换网段都不用重新配对。
        撤销之后那台设备的下一个请求就是 401。命令行上是 <code>herdr-web devices / revoke</code>。
        <br />
        要加一台设备：到机器上敲 <code>herdr-web pair</code>。网页上<strong>不出</strong>配对码 ——
        码创造的是一份不随这台设备一起被撤销的独立凭据，而且它只打在主机终端上，那是唯一的带外因子。
      </p>
    </>
  )

  // embedded：嵌在设置面板里当一页用，外壳（标题 / 关闭）归设置面板
  return embedded ? body : (
    <Panel title="已配对的设备" onClose={onClose ?? (() => {})} className="w-[420px]">{body}</Panel>
  )
}
