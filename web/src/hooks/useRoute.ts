import { useEffect, useState } from 'react'
import { isNative, onNative } from '@/lib/native'
import { onLanNow, onLoopback } from '@/hooks/useLanDirect'

/**
 * 「现在走的是直连还是中继」—— 顶栏那个状态点的颜色（绿 = 直连、蓝 = 中继）。
 *
 * - **App 里**：壳报的是**此刻开着的**隧道各走哪条路（`LanRoute.kt`）。不按「新连接往哪儿拨」
 *   算：切到局域网之后终端那条长连接还在中继上，按新连接画就是「直连」，而人最在意的终端其实
 *   还绕着公网。两条路都有（刚切过去）就算中继 —— 说的是最慢的那条。
 * - **浏览器里**：没有换路这回事，看地址就够了 —— 站在直连口的 IP origin 上、或者本机打开的
 *   就是直连，别的（域名）都是经中继进来的。
 */
export type Route = { via: 'lan' | 'relay'; detail: string }

type NativeRoute = { lan: number; relay: number; next: string; proxied: boolean }

/*
 * 壳报来的最新一份**存在模块里**，不存在各自的 state 里。壳只在 hello 和「变了」的时候发，
 * 后挂上的组件（设置里那一节是点开才挂的）自己订阅的话一条都收不到，于是退回「按地址猜」——
 * App 里地址永远是域名，猜出来就是中继：顶栏那颗点是绿的、设置里写「正走中继」（用户截图报的，
 * 而且报了两次：第一次是两边各算各的，第二次就是这个）。
 */
let last: NativeRoute | null = null
const subs = new Set<(r: NativeRoute) => void>()
if (isNative()) {
  onNative('route', (m) => {
    last = {
      lan: Number(m.lan) || 0,
      relay: Number(m.relay) || 0,
      next: String(m.next ?? ''),
      proxied: !!m.proxied,
    }
    subs.forEach((f) => f(last!))
  })
}

export function useRoute(lanOrigins: string[] | undefined): Route {
  const [native, setNative] = useState<NativeRoute | null>(last)

  useEffect(() => {
    subs.add(setNative)
    if (last) setNative(last) // 挂上之前那一刻到的也别漏
    return () => { subs.delete(setNative) }
  }, [])

  if (native?.proxied) {
    if (native.relay > 0 && native.lan > 0) {
      return { via: 'relay', detail: '刚切到局域网：新连接走直连，终端还在中继上（重连一次就过去了）' }
    }
    if (native.relay > 0) return { via: 'relay', detail: '经云端中继' }
    if (native.lan > 0) return { via: 'lan', detail: '局域网直连' }
    return native.next === 'lan'
      ? { via: 'lan', detail: '局域网直连' }
      : { via: 'relay', detail: '经云端中继' }
  }
  if (onLoopback()) return { via: 'lan', detail: '本机' }
  if (onLanNow(lanOrigins ?? [])) return { via: 'lan', detail: '局域网直连' }
  return { via: 'relay', detail: '经云端中继' }
}
