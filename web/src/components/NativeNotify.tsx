import { useEffect, useState } from 'react'
import { onNative, postNative } from '@/lib/native'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'

type State = { on: boolean; perm: boolean; battery: boolean }

/**
 * 原生壳里的「后台通知」那一块，替代网页的系统通知开关。
 *
 * 网页那套（lib/notify.ts）在 WebView 里用不了（Android WebView 没有 Notification），而且
 * 本来就只在页面开着时有效。壳里是一个前台服务长连服务器（app/android 的 NoticeService），
 * App 关着 / 锁屏也收得到 —— 所以这一块的状态全在壳那边，这里只画、只转发点击。
 */
export function NativeNotify() {
  const [st, setSt] = useState<State | null>(null)

  useEffect(() => {
    const off = onNative('notify', (m) => setSt({ on: !!m.on, perm: !!m.perm, battery: !!m.battery }))
    postNative({ t: 'notify', op: 'get' })
    return off
  }, [])

  if (!st) return null
  const live = st.on && st.perm
  return (
    <div className="pt-1">
      <label className="flex cursor-pointer items-start gap-2.5 rounded-md transition-colors hover:text-fg">
        <span className="pt-px">
          <Checkbox
            checked={st.on}
            onCheckedChange={(v) => postNative({ t: 'notify', op: 'set', on: !!v })}
          />
        </span>
        <span className="text-[13px]/relaxed">
          后台通知（App 关着 / 锁屏也能收到）
          <span className="mt-0.5 block text-xs text-faint">
            {!st.on
              ? '关着。agent 等你回答、跑完了，只有打开 App 才看得到。'
              : !st.perm
                ? '系统没给通知权限 —— 点一下开关或「试一下」申请；拒过的话要去系统设置里打开。'
                : '开着。通知栏常驻一条 agent 状态一览（几个在跑、几个等你），状态一变就刷新。'}
          </span>
        </span>
      </label>

      <div className="mt-1 flex flex-wrap items-center gap-2 pl-7">
        <Button size="tiny" onClick={() => postNative({ t: 'notify', op: 'test' })}>
          试一下
        </Button>
        {live && !st.battery && (
          <Button size="tiny" onClick={() => postNative({ t: 'notify', op: 'battery' })}>
            允许后台运行
          </Button>
        )}
      </div>
      {live && (
        <p className="mt-1 pl-7 text-xs/relaxed text-faint">
          {st.battery
            ? '已经不受电池优化限制。'
            : '手机省电策略会把后台服务杀掉，点「允许后台运行」放行。'}
          小米 / 华为 / OPPO / vivo 还要在系统设置里给这个 App 开「自启动」和「后台运行」，
          不然锁屏一会儿就收不到了。
        </p>
      )}
    </div>
  )
}
