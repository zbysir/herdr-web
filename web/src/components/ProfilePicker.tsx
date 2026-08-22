import { useEffect, useRef, useState } from 'react'
import { Check, Copy, Pencil, Trash2, X } from 'lucide-react'
import { api, deviceKind, INSTALL, type Profile, type ProfilesResponse } from '@/lib/api'
import { Button } from './ui/button'
import { Input } from './ui/input'
import { Select } from './ui/select'

/**
 * 「这台设备用哪一套排布」—— 设置面板最上面那一行。
 *
 * 为什么摆在**分页条上面**而不是单开一页：下面「顶栏」和「软键条」两页改的就是这一套里的
 * 东西，而「我正在改哪一套」是看这两页时必须一直看得见的。塞进第五页的话，人在软键条页上
 * 拖了十分钟才发现改的是手机那套。
 *
 * 换一套 = 把**这台设备**绑过去（服务端记着，见 internal/profiles）。**不按屏幕宽度自动
 * 切**：分屏、转屏、外接屏都会让宽度跳变，而这里面装的正是软键条那种一跳就手忙脚乱的东西。
 *
 * 「新建」默认**复制当前这一套**：从平板那套拷过来再删几个键，比从零拖快得多 ——
 * 这也是这个功能最常用的动作。
 */
export function ProfilePicker({
  onChanged, toast,
}: {
  /** 名册 / 绑定 / 开关变了：把整份响应交出去，App 那边重拉排布并把开关刷一遍 */
  onChanged: (r: ProfilesResponse) => void
  toast: (m: string) => void
}) {
  const [list, setList] = useState<Profile[]>([])
  const [cur, setCur] = useState('')
  const [max, setMax] = useState(8)
  const [maxName, setMaxName] = useState(16)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  /** 正在输名字：'new' 是新建、'rename' 是改名。null = 不在输 */
  const [editing, setEditing] = useState<'new' | 'rename' | null>(null)
  const [name, setName] = useState('')
  /** 删除举起来了没有（和设备面板那套一样：点两下才真删） */
  const [armed, setArmed] = useState(false)
  const disarm = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)

  const take = (r: ProfilesResponse, tell = true) => {
    setList(r.profiles ?? [])
    setCur(r.current)
    setMax(r.max || 8)
    setMaxName(r.maxName || 16)
    setEditing(null)
    setArmed(false)
    setErr('')
    if (tell) onChanged(r)
  }

  useEffect(() => {
    void (async () => {
      try {
        // 只读一份现状：报到（绑定 / 记住这台设备）是 App 启动时那一下干的
        take(await api.get<ProfilesResponse>('/profiles'), false)
      } catch (e) {
        setErr((e as Error).message)
      }
    })()
    return () => clearTimeout(disarm.current)
  }, [])

  /** 包一层：转圈、报错、成功之后把新的一份铺开 */
  const run = async (f: () => Promise<ProfilesResponse>, ok?: string) => {
    setBusy(true)
    setErr('')
    try {
      take(await f())
      if (ok) toast(ok)
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const now = list.find((p) => p.id === cur)

  const switchTo = (id: string) => {
    if (id === cur) return
    void run(() => api.post<ProfilesResponse>('/profiles/bind', { install: INSTALL, profile: id }),
      `这台设备换成「${list.find((p) => p.id === id)?.name ?? id}」了`)
  }

  const create = () => {
    const n = name.trim()
    if (!n) { setErr('给它起个名字，比如「手机」'); return }
    void run(async () => {
      // 建 → 绑过来：两下，各干一件事。中间断了就是「多了一套没人用的」，人在下拉里
      // 点一下就好；反过来（一个口里又建又绑）出错时说不清到底成了哪一半。
      const made = await api.post<ProfilesResponse & { created?: string }>(
        '/profiles', { name: n, kind: deviceKind(), copyFrom: cur },
      )
      if (!made.created) return made
      return api.post<ProfilesResponse>('/profiles/bind', { install: INSTALL, profile: made.created })
    }, `「${n}」建好了，是从「${now?.name ?? cur}」复制过来的`)
  }

  const rename = () => {
    const n = name.trim()
    if (!n) { setErr('名字不能空着'); return }
    void run(() => api.put<ProfilesResponse>(`/profiles/${encodeURIComponent(cur)}`, { name: n }), '改好了')
  }

  const del = () => {
    if (!armed) {
      setArmed(true)
      clearTimeout(disarm.current)
      disarm.current = setTimeout(() => setArmed(false), 3000) // 3 秒不点就放下
      return
    }
    void run(() => api.del<ProfilesResponse>(`/profiles/${encodeURIComponent(cur)}`),
      `「${now?.name ?? cur}」删了，这台设备回到「默认」`)
  }

  return (
    <div className="mb-1 flex flex-col gap-1.5">
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-[13px]">这台设备用</span>
        <Select
          value={cur}
          disabled={busy || !!editing}
          aria-label="这台设备用哪一套排布"
          data-testid="profile-select"
          onChange={(e) => switchTo(e.target.value)}
        >
          {list.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
        </Select>

        {editing ? (
          <>
            <Input
              className="w-[7em]"
              autoFocus
              value={name}
              maxLength={maxName}
              placeholder={editing === 'new' ? '手机' : now?.name}
              onChange={(e) => setName(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  e.preventDefault()
                  if (editing === 'new') create()
                  else rename()
                }
                if (e.key === 'Escape') setEditing(null)
              }}
            />
            <Button size="tiny" variant="primary" disabled={busy} onClick={() => (editing === 'new' ? create() : rename())}>
              <Check className="size-3" />{editing === 'new' ? '建' : '改'}
            </Button>
            <Button size="tiny" disabled={busy} onClick={() => setEditing(null)}><X className="size-3" />取消</Button>
          </>
        ) : (
          <div className="ml-auto flex items-center gap-1.5">
            <Button
              size="tiny"
              disabled={busy || list.length >= max}
              title={list.length >= max ? `最多 ${max} 套` : '新建一套，排布从当前这一套复制过来'}
              onClick={() => { setName(''); setEditing('new') }}
            >
              <Copy className="size-3" />新建
            </Button>
            <Button size="tiny" disabled={busy} title="给这一套改个名字" onClick={() => { setName(now?.name ?? ''); setEditing('rename') }}>
              <Pencil className="size-3" />改名
            </Button>
            {/* 「默认」删不掉：所有没绑过的设备都落在它身上（服务端也拦一道） */}
            {cur !== 'default' && (
              <Button
                size="tiny"
                variant={armed ? 'destructive' : 'danger'}
                disabled={busy}
                title="删掉这一套（这台设备回到「默认」）"
                onClick={del}
              >
                <Trash2 className="size-3" />{armed ? '再点一次' : '删掉'}
              </Button>
            )}
          </div>
        )}
      </div>

      {/* 一句话说清「什么各一份、什么共用」—— 这是这个功能唯一容易想错的地方。
          再长就不值了：它常驻在分页条上面，每次开设置都要占掉两三行 */}
      <p className="text-xs/relaxed text-faint [&_strong]:font-medium [&_strong]:text-muted">
        {editing === 'new' ? (
          '新建那一套的排布从当前这套复制过来，建完这台设备就用新的那套。'
        ) : (
          <>
            「顶栏」「软键条」两页改的就是<strong>这一套</strong>，换一套整条换掉；
            「我的按键」里的定义<strong>所有套共用</strong>。别的设备绑哪一套在「设备」页里改。
          </>
        )}
      </p>
      {err && <p className="text-xs text-bad">{err}</p>}
    </div>
  )
}
