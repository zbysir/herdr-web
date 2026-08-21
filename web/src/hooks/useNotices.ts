// 提示的轮询：agent 一有「轮到你了」的变化，右上角弹一下、面板图标上点个红点。
//
// **为什么是轮询**：herdr-web 到浏览器只有 /pty 那一条二进制流（终端的字节），往里塞
// JSON 等于给终端加一套协议；而这个页面本来就每 500ms 为发件箱打一次接口，再加一拍
// 几秒一次的 GET 微不足道（这一拍在服务端只读内存，不打 herdr socket）。
// 见 README「是轮询，不是推送」。
//
// 两个「已读」的概念，别混：
//   - **弹窗**（items）只装「页面开着的这段时间里新来的」。首次对底那些**只点红点不弹**：
//     那可能是半小时前发生的事，弹出来像刚发生的一样就是编时间。
//   - **红点**（unread）是「有没有你还没看过的」，跨刷新记在 localStorage 里（记的是
//     seq，不是条数）—— 手机上刷一下页面红点就没了的话，这个提示等于白做。
import { useCallback, useEffect, useRef, useState } from 'react'
import { api, SESSION, type Notice, type NoticesResult } from '@/lib/api'

// 每个 session 一份：命名 session 是**另一个 herdr**，那边的 agent 跟这边没关系
// （seq 也是各数各的）。
const SEEN_KEY = `noticeSeen${SESSION ? `:${SESSION}` : ''}`

const readSeen = () => Number(localStorage.getItem(SEEN_KEY)) || 0

/** 右上角最多同时挂几张卡。再多就叠成一摞看不清了，多出来的只报个数 */
export const MAX_CARDS = 3

/** 手里最多攒几条（多出来的界面上收成「还有 N 条」）。给卡片上限留了一倍余量 */
const MAX_KEEP = MAX_CARDS * 2

/**
 * 攒满了先丢**「跑完了」**那种，「等你回答」的留住。
 *
 * 一串 agent 同时跑完能一口气顶掉七八条，而其中那个真的停在那儿等你的恰恰是唯一
 * 需要你动手的 —— 按「先来先丢」的话它最先被挤掉，提示就成了摆设。
 */
function trim(list: Notice[]): Notice[] {
  if (list.length <= MAX_KEEP) return list
  const drop = new Set<number>()
  const need = list.length - MAX_KEEP
  for (const n of list) { // 老的在前，所以顺着走就是从最老的开始丢
    if (drop.size >= need) break
    if (n.status !== 'blocked') drop.add(n.seq)
  }
  for (const n of list) { // 全是「等你回答」时才丢它们（同样从最老的开始）
    if (drop.size >= need) break
    drop.add(n.seq)
  }
  return list.filter((n) => !drop.has(n.seq))
}

export function useNotices(pollMs: number, enabled: boolean) {
  const [items, setItems] = useState<Notice[]>([])
  const [unread, setUnread] = useState(0)

  // 下一拍从哪儿要起。null = 还没对过底（第一拍要从「上次看到哪儿」要，好把红点点上）
  const since = useRef<number | null>(null)
  const seen = useRef(readSeen())

  const tick = useCallback(async () => {
    let r: NoticesResult
    try {
      r = await api.get<NoticesResult>(`/herdr/notices?since=${since.current ?? seen.current}`)
    } catch {
      return // 连不上就下一拍再说：提示不该在页面上报错，终端那边已经会说了
    }
    const first = since.current === null
    since.current = r.seq

    // 服务端重启过（内存里那个环没了，seq 从 0 重新数）：本地记的「看到哪儿」比它还大，
    // 留着的话新提示要等 seq 追上来才认得出。跟着它退回去。
    if (r.seq < seen.current) {
      seen.current = r.seq
      localStorage.setItem(SEEN_KEY, String(r.seq))
    }

    const fresh = (r.notices ?? []).filter((n) => n.seq > seen.current)
    if (!fresh.length) return
    // 首拍拿到的是「上次看到那会儿之后攒的全部」，所以是**赋值**；之后每拍只拿得到
    // 这一拍新来的（since 已经推到服务端的 seq 上了），所以是**累加**。
    setUnread((u) => (first ? fresh.length : u + fresh.length))
    if (first) return // 首次对底：只点红点，不弹（见文件头）

    setItems((cur) => {
      // 同一个终端只留最新那条：一个 agent 连着「跑完了 → 等你回答」时，两张卡叠着看
      // 只会挡住新的那张。认 term 不认 pane（pane_id 会被重新分配给别人）。
      const merged = [...cur.filter((c) => !fresh.some((n) => n.term === c.term)), ...fresh]
      return trim(merged)
    })
  }, [])

  // 自排队的 setTimeout，不用 setInterval：网络一慢 setInterval 会把请求叠起来
  // （发件箱那边同理，见 useCompose）。
  useEffect(() => {
    if (!enabled || pollMs <= 0) return
    let stop = false
    let timer: ReturnType<typeof setTimeout>
    const loop = (wait: number) => {
      timer = setTimeout(async () => {
        await tick()
        if (!stop) loop(pollMs)
      }, wait)
    }
    loop(0) // 第一拍立刻走：红点该在开页面时就亮着
    return () => { stop = true; clearTimeout(timer) }
  }, [enabled, pollMs, tick])

  // 手机锁屏 / 切走再回来：立刻对一次，别等下一拍。这恰恰是最想看到提示的时刻。
  useEffect(() => {
    if (!enabled || pollMs <= 0) return
    const f = () => { if (!document.hidden) void tick() }
    addEventListener('focus', f)
    document.addEventListener('visibilitychange', f)
    return () => { removeEventListener('focus', f); document.removeEventListener('visibilitychange', f) }
  }, [enabled, pollMs, tick])

  /** 看过了：红点灭掉，弹窗收掉。开「面板一览」时调 —— 那儿就是看这些变化的地方 */
  const markSeen = useCallback(() => {
    if (since.current !== null) {
      seen.current = since.current
      localStorage.setItem(SEEN_KEY, String(since.current))
    }
    setUnread(0)
    setItems([])
  }, [])

  /** 单独关掉一张卡（红点不动：没去看那个 pane，就不算看过了） */
  const dismiss = useCallback((seq: number) => {
    setItems((cur) => cur.filter((n) => n.seq !== seq))
  }, [])

  return { items, unread, markSeen, dismiss }
}
