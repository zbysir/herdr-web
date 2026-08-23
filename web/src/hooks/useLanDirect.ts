import { useCallback, useEffect, useState } from 'react'
import { api, INSTALL, SESSION, type State } from '@/lib/api'

/**
 * 局域网直连：页面是从公网那条路（隧道 / 反代）加载的，但如果此刻就在同一个局域网里，
 * 就换到直连那个 origin 上去。一次按键的往返从「两跳公网」变成「一跳交换机」。
 *
 * 三条**方法上**的硬事实，都是浏览器的规矩，绕不过去：
 *
 * ① **嗅探目标必须是 https。** https 页面对 http 目标的 fetch 一律算 active mixed
 *    content 被拦死（`mode:'no-cors'` 也拦），所以局域网那个口是明文的话这条路整个
 *    不存在。服务端那边 `HERDR_WEB_LAN_PORT` 开的就是个自签 TLS 口。
 *
 * ② **`mode:'no-cors'` 是这里唯一能用的探法。** 读不到响应内容，但它能分清「有响应」
 *    （resolve，401 也算）和「连不上 / 证书不认」（reject）—— 而这正好就是要的全部
 *    信息。用普通 fetch 会因为没有 CORS 头而 reject，那就把「通」也当成「不通」了。
 *
 * ③ **换过去就是换了 origin**，cookie 是 host-only 的，新 origin 上一份凭据都没有。
 *    所以跳之前先要一个一次性配对码带过去（`/api/handoff`），落地时服务端的 `?pair=`
 *    那条路自己换成 cookie 再把 URL 洗干净。
 *
 * 还有一条是这个部署形态特有的：那张自签证书要靠**人在那个 origin 上点过一次「继续
 * 访问」**才被认。所以第一次得手动开一次；而局域网 IP 一变就是新 origin，那一下信任
 * 也跟着没了 —— 于是「探不通」有两种完全不同的原因（在外面 / 地址变了），必须分开报，
 * 见下面 `moved`。只静默失败的话，表现是「某天开始就是不快了，而且没有任何线索」。
 */

const KEY = 'lanDirect' // 'on' = 已经同意走这条路 | 'off' = 说过不要
const LAST = 'lanDirectAt' // 上次真的走通过的 origin，用来认「地址变了」

/** 嗅探超时。局域网的 RTT 是个位数毫秒，900ms 已经宽到能容下一次 TLS 握手 + 首包。
 *  再放大只会让「在外面」那种情况白等 —— 而那是最常见的一种。 */
const TIMEOUT_MS = 900

/* 下面这三个给设置面板用（components/SettingsPanel.tsx 的「局域网直连」那一节）。
   导出而不是让那边自己写一遍 localStorage 的键：两处各写一份字符串，改一处忘一处的
   表现是「面板上的开关关了，但下次打开还是自动切」—— 而那种不一致查起来很费劲。 */

/** 「探到就自动切」开着没有。没记过 = 开着（第一次会先问一句，见 LanPrompt）。 */
export function lanAuto(): boolean {
  return localStorage.getItem(KEY) !== 'off'
}

export function setLanAuto(on: boolean) {
  localStorage.setItem(KEY, on ? 'on' : 'off')
}

/** 这个页面此刻是不是已经走在直连上了。 */
export function onLanNow(origins: string[]): boolean {
  return origins.includes(location.origin)
}

export type LanDirect =
  | { kind: 'idle' }
  /** 通了，但还没问过人要不要走 */
  | { kind: 'ask'; origin: string }
  /** 走过这条路，现在探不通，而且服务端报的地址和上次走通的**不是同一个** */
  | { kind: 'moved'; origin: string }

async function reachable(origin: string): Promise<string> {
  // 不用 AbortSignal.timeout：这里要兼容的是平板上的 Safari，手搓一个没有下限风险
  const ac = new AbortController()
  const t = setTimeout(() => ac.abort(), TIMEOUT_MS)
  try {
    // 打 /api/state 而不是另开一个 ping 口：跨 origin 的请求带不上 cookie，所以它必然
    // 在 requireAuth 那里 401 —— 而 401 也是一个响应，opaque fetch 照样 resolve。
    // 于是「探活」不需要服务端多一个未认证就能打的口。
    await fetch(origin + '/api/state', { mode: 'no-cors', cache: 'no-store', signal: ac.signal })
    return origin
  } finally {
    clearTimeout(t)
  }
}

/** 谁先通用谁。全都不通就返回 null。 */
async function probe(origins: string[]): Promise<string | null> {
  try {
    return await Promise.any(origins.map(reachable))
  } catch {
    return null
  }
}

/** 带着凭据跳过去。 */
async function jump(origin: string) {
  let u = origin + (SESSION ? '/' + encodeURIComponent(SESSION) : '/')
  try {
    // 交接令牌，**不是配对码**：60 秒、一次性、只能在直连那个监听上兑换、兑出来的设备
    // 随上级一起被撤销。为什么不能用配对码，见 internal/auth 的 MintHandoff 和
    // SECURITY.md §11 —— 那是这块唯一不能走捷径的地方。
    const { handoff } = await api.post<{ handoff: string }>('/handoff')
    u += '?handoff=' + encodeURIComponent(handoff)
  } catch {
    // 拿不到令牌也照样跳：那个 origin 上很可能已经有 cookie 了（第二次之后都是这样）。
    // 真没有的话落地会是配对页，比卡在这儿不动强。
  }
  location.replace(u + '#install=' + encodeURIComponent(INSTALL))
}

export function useLanDirect(lan: State['lan'], enabled: boolean) {
  const [ask, setAsk] = useState<LanDirect>({ kind: 'idle' })

  useEffect(() => {
    const origins = lan?.origins ?? []
    if (!enabled || !origins.length) return

    // 已经在直连这条路上了：把「走通过」记下来（IP 变了要靠它认），然后什么都不做。
    if (onLanNow(origins)) {
      localStorage.setItem(LAST, location.origin)
      // 只在**还没记过**的时候写 on（人是手打地址进来的，那就别以后再问一遍）。
      // 不能无条件写：设置面板里那个开关关掉之后，人如果此刻正站在直连这个 origin 上，
      // 无条件写会把他刚关掉的开关又打开 —— 表现是「关了，刷新一下又是开的」。
      if (localStorage.getItem(KEY) === null) localStorage.setItem(KEY, 'on')
      return
    }
    if (localStorage.getItem(KEY) === 'off') return

    let dropped = false
    void (async () => {
      const hit = await probe(origins)
      if (dropped) return
      if (hit) {
        if (localStorage.getItem(KEY) === 'on') {
          localStorage.setItem(LAST, hit)
          void jump(hit) // 同意过一次之后就不再问，静默切
        } else {
          setAsk({ kind: 'ask', origin: hit })
        }
        return
      }
      // 探不通。两种原因，处置完全不同：
      //   - 服务端报的地址里还有上次走通的那个 → 人在外面，安静走公网就对了
      //   - 报的是别的地址 → 那台机器换了网段，旧 origin 上那一下「继续访问」作废了，
      //     得让人知道去新地址上再点一次，否则这条路就永久静默失效
      const last = localStorage.getItem(LAST)
      if (localStorage.getItem(KEY) === 'on' && last && !origins.includes(last)) {
        setAsk({ kind: 'moved', origin: origins[0] })
      }
    })()
    return () => { dropped = true }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [enabled, lan?.origins?.join(',')])

  const accept = useCallback(() => {
    if (ask.kind !== 'ask') return
    localStorage.setItem(KEY, 'on')
    localStorage.setItem(LAST, ask.origin)
    void jump(ask.origin)
  }, [ask])

  const decline = useCallback(() => {
    localStorage.setItem(KEY, 'off')
    setAsk({ kind: 'idle' })
  }, [])

  const dismiss = useCallback(() => setAsk({ kind: 'idle' }), [])

  return { ask, accept, decline, dismiss }
}
