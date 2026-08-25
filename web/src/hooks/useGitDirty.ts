import { useCallback, useEffect, useRef, useState } from 'react'
import { gitApi, type GitDirty } from '@/lib/api'

/**
 * 顶栏「改动」那个按钮上的角标：**有你还没看过的改动**。
 *
 * # 为什么不是「有改动就亮」
 *
 * 因为那在一个正干活的仓库里**永远为真** —— agent 一直在写，工作区从早到晚都是脏的，
 * 那个点就等于焊在图标上，没有任何信息量（提示红点那边是同一条道理，见 App 的 dotEl）。
 * 所以判据是「和你上次看过的那份**不一样**」：服务端给一个指纹（`sig`），这边跟本地
 * 记着的那个比；开一次面板就算看过，指纹存下来，点灭掉。
 *
 * # 代价说清楚
 *
 * 这是**在对面那台机器上按秒跑 git**（一拍两次：status + shortstat），而那台机器上正
 * 跑着 agent。所以：
 *
 *   - 12 秒一拍，不是 4 秒（角标不是提示，晚十几秒没有代价）；
 *   - **页面不可见时不问**（手机切走 / 锁屏那会儿问了也没人看），回到前台立刻补一拍；
 *   - 服务端那边还有 2 秒缓存，开着几个标签页也只跑一次；
 *   - 关掉「红点」这个开关就整个不问了（见 App 里怎么接的）—— 不想要角标的人不该为它
 *     的轮询买单。
 *
 * 盯的是**上次看的那个仓库**（面板记在 `diffRepo` 里），拿不到就用焦点 pane 的 cwd。
 * 不是「所有仓库一起盯」：那台机器上开着几十个 pane 是常态（实测 34 个不同 cwd），
 * 一拍几十次 git 换一个小红点，不值当。
 */
const SEEN = 'diffSeen'

function readSeen(): Record<string, string> {
  try {
    const v: unknown = JSON.parse(localStorage.getItem(SEEN) ?? '{}')
    return v && typeof v === 'object' ? (v as Record<string, string>) : {}
  } catch {
    return {}
  }
}

export function useGitDirty(dir: string | undefined, enabled: boolean, pollMs = 12000) {
  const [st, setSt] = useState<GitDirty | null>(null)
  const [seen, setSeen] = useState<Record<string, string>>(readSeen)
  const stRef = useRef<GitDirty | null>(null)
  stRef.current = st

  const tick = useCallback(async () => {
    if (!dir || document.hidden) return
    try {
      setSt(await gitApi.dirty(dir))
    } catch {
      // 不是仓库 / 这个部署关掉了 / 一次网络抖动：**安静地没有角标**。
      // 这是个探测，不是一次操作，不该弹任何东西。
      setSt(null)
    }
  }, [dir])

  // 自排队的 setTimeout，不用 setInterval：网络一慢 setInterval 会把请求叠起来
  // （提示那边同理，见 useNotices）
  useEffect(() => {
    if (!enabled || !dir) { setSt(null); return }
    let stop = false
    let timer: ReturnType<typeof setTimeout>
    const loop = (wait: number) => {
      timer = setTimeout(async () => {
        await tick()
        if (!stop) loop(pollMs)
      }, wait)
    }
    loop(0) // 第一拍立刻走：角标该在开页面时就是对的
    return () => { stop = true; clearTimeout(timer) }
  }, [enabled, dir, pollMs, tick])

  // 切回来立刻对一次（上面那个 tick 在页面不可见时是直接跳过的）
  useEffect(() => {
    if (!enabled) return
    const f = () => { if (!document.hidden) void tick() }
    addEventListener('focus', f)
    document.addEventListener('visibilitychange', f)
    return () => { removeEventListener('focus', f); document.removeEventListener('visibilitychange', f) }
  }, [enabled, tick])

  /** 看过了：把这个仓库当前的指纹记下来，点灭掉。开「改动」面板时调 */
  const markSeen = useCallback(() => {
    const cur = stRef.current
    if (!cur) return
    setSeen((s) => {
      const next = { ...s, [cur.root]: cur.sig }
      try { localStorage.setItem(SEEN, JSON.stringify(next)) } catch { /* 满了就算了 */ }
      return next
    })
  }, [])

  return {
    files: st?.files ?? 0,
    /** 亮不亮：有改动，而且和上次看过的那份不一样 */
    fresh: !!st && st.files > 0 && seen[st.root] !== st.sig,
    markSeen,
  }
}
