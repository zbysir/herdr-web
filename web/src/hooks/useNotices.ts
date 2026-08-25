// 提示的轮询：agent 一有「轮到你了」的变化，右上角弹一下、面板图标上挂个红点。
//
// **为什么是轮询**：herdr-web 到浏览器只有 /pty 那一条二进制流（终端的字节），往里塞
// JSON 等于给终端加一套协议；而这个页面本来就每 500ms 为发件箱打一次接口，再加一拍
// 几秒一次的 GET 微不足道（这一拍在服务端只读内存，不打 herdr socket）。
// 见 README「是轮询，不是推送」。
//
// 两个「已读」的概念，别混：
//   - **弹窗**（items）只装「页面开着的这段时间里新来的」。首次对底那些**只记未读不弹**：
//     那可能是半小时前发生的事，弹出来像刚发生的一样就是编时间。
//   - **未读**（unread）是「还有哪几条你没看过」（界面上只是个红点，不报数），跨刷新记在 localStorage 里 ——
//     手机上刷一下页面红点就没了的话，这个提示等于白做。
//
// **未读是按条算的，不是一个开关。** 两个 agent 都在等你时，点进去看了一个，红点得留着
// （还有一个没看）；两个都点进去了才灭。所以这里存的是「哪几条还没看」而不是一个布尔值 ——
// 早先那版点一条就整片清掉，等于把另一个 agent 悄悄吞了。
//
// 落盘要存两样：**水位线**（seq，它以下的全算看过了）+ **水位线以上已经看过的那几条**。
// 只存水位线的话表达不了「第 7 条看了、第 6 条还没看」；等未读清空了就把水位线推上去、
// 那份名单也跟着清掉，所以它平时长度就是个位数。
import { useCallback, useEffect, useRef, useState } from 'react'
import { api, SESSION, type Notice, type NoticesResult } from '@/lib/api'

// 每个 session 一份：命名 session 是**另一个 herdr**，那边的 agent 跟这边没关系
// （seq 也是各数各的）。
const SEEN_KEY = `noticeSeen${SESSION ? `:${SESSION}` : ''}`
const READ_KEY = `noticeRead${SESSION ? `:${SESSION}` : ''}`

const readSeen = () => Number(localStorage.getItem(SEEN_KEY)) || 0

/** 水位线以上、已经点进去看过的那几条 seq */
function readRead(): number[] {
  try {
    const v = JSON.parse(localStorage.getItem(READ_KEY) || '[]')
    return Array.isArray(v) ? v.filter((x) => typeof x === 'number') : []
  } catch {
    return [] // 存坏了就当没看过：多点一次红点，比把没看的说成看过了强
  }
}

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

export function useNotices(pollMs: number, enabled: boolean, onNew?: (n: Notice) => void) {
  const [items, setItems] = useState<Notice[]>([])
  /** 还没看过的那几条（整条留着：系统通知点回来时要按 pane 找） */
  const [unread, setUnread] = useState<Notice[]>([])
  // 放 ref 里：这个回调每次渲染都是新的（它捕获了「系统通知开没开」那个开关），
  // 直接进 tick 的依赖会让轮询每渲染一次就重启一次。
  const newCb = useRef(onNew)
  newCb.current = onNew

  // 下一拍从哪儿要起。null = 还没对过底（第一拍要从「上次看到哪儿」要，好把红点点上）
  const since = useRef<number | null>(null)
  const seen = useRef(readSeen())     // 水位线：seq 比它小的一律算看过了
  const read = useRef(readRead())     // 水位线以上、已经看过的那几条

  /** 把「看到哪儿了」写回本地。未读空了就顺手把水位线推到最新，名单清掉 */
  const persist = useCallback((left: Notice[]) => {
    if (!left.length && since.current !== null) {
      seen.current = since.current
      read.current = []
    }
    localStorage.setItem(SEEN_KEY, String(seen.current))
    localStorage.setItem(READ_KEY, JSON.stringify(read.current))
  }, [])

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

    // 水位线以上、又不在「已经看过」名单里的，才算未读
    const fresh = (r.notices ?? []).filter((n) => n.seq > seen.current && !read.current.includes(n.seq))
    if (!fresh.length) return
    // 首拍拿到的是「上次看到那会儿之后攒的全部」，所以是**换一份**；之后每拍只拿得到
    // 这一拍新来的（since 已经推到服务端的 seq 上了），所以是**接上去**。
    setUnread((u) => (first ? fresh : [...u, ...fresh]))
    if (first) return // 首次对底：只点红点，不弹（见文件头）

    // 系统通知走这儿：**首次对底那批不发**（那些可能是半小时前的事，弹到锁屏上像刚发生
    // 一样），和卡片是同一条规矩。
    for (const n of fresh) newCb.current?.(n)

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

  /**
   * 全都算看过了：红点灭掉，弹窗收掉。开「面板一览」时调 —— 那儿就是看这些变化的地方，
   * 一眼扫完整张列表，剩下几条没点开也不该再挂着红点。
   */
  const markSeen = useCallback(() => {
    setUnread([])
    setItems([])
    persist([])
  }, [persist])

  /**
   * 看过**这个 pane** 了（点提示卡跳过去 / 点系统通知回来）：它名下的未读全消掉，卡片收掉。
   *
   * 按 pane 而不是按那一条：你跳过去看的是**那个 agent 现在的屏幕**，它先前那条
   * 「跑完了」也一并看见了；只销掉点中的那一条的话，红点会莫名其妙地还亮着，
   * 而你已经无处可点（那条的卡片早自己走了）。
   *
   * 别的 agent 的未读一条不动 —— 两个 agent 在等你，看了一个不等于两个都看了。
   */
  const seePane = useCallback((pane: string) => {
    setUnread((cur) => {
      const hit = cur.filter((n) => n.pane === pane)
      if (!hit.length) return cur
      for (const n of hit) if (!read.current.includes(n.seq)) read.current.push(n.seq)
      const left = cur.filter((n) => n.pane !== pane)
      setItems((its) => its.filter((n) => n.pane !== pane))
      persist(left)
      return left
    })
  }, [persist])

  /** 单独关掉一张卡（红点不动：没去看那个 pane，就不算看过了） */
  const dismiss = useCallback((seq: number) => {
    setItems((cur) => cur.filter((n) => n.seq !== seq))
  }, [])

  return { items, unread, markSeen, seePane, dismiss }
}
