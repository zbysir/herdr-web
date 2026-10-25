import { useEffect } from 'react'
import type { State } from '@/lib/api'
import { isNative, postNative } from '@/lib/native'

/**
 * 局域网直连：人在家时连接不绕公网，直接走这台机器的局域网口。
 *
 * **只有 App 走这条路**：换路是壳的事，**不换 origin**（docs/dev/RELAY.md §4）。壳拿服务端
 * 报的那几个地址按本页的域名做 TLS 握手探测（真证书、验域名），通了就让 WebView 的连接
 * 从那儿走 —— 地址栏、cookie、passkey 全不变。这里只负责把地址递过去。
 *
 * 浏览器里**不做**（2026-10-25 下掉的）。原来那条是「嗅探直连口 → 带交接令牌跳到
 * `https://192.168.x.x:7790`」，而它注定要碰一张浏览器眼里不对的证书（证书签给域名、按 IP
 * 连）。人在那个 IP 上点过一次「继续访问」之后，嗅探请求带着证书错误**成功**，Chromium
 * 就把公网那一页记成「加载过证书有错的内容」：页面信息里多一句「还包含其他不安全资源」，
 * passkey（M110 起）一律 NotAllowedError，刷新都不好、得重启浏览器（用户报的「浏览器不给
 * 我签 passkey 了」）。**别加回来**：任何从页面里发往直连口的请求（fetch / img / iframe）
 * 都是同一个坑，CSP 的 connect-src 也因此收回到只有 'self'（guard.go 的 secHeaders）。
 *
 * 人自己手打 IP 地址进来照样能用（直连口还开着），只是没人替他切过去。
 */

/** 这个页面此刻是不是已经走在直连上了（人自己手打 IP 进来的）。 */
export function onLanNow(origins: string[]): boolean {
  return origins.includes(location.origin)
}

/**
 * 这个页面本来就是从**本机**开的（localhost / 127.x / ::1）—— 请求压根没出过这台机器。
 *
 * 判据只收 loopback，**不扩到所有私网地址**：VPN（Tailscale 的 100.64/10、WireGuard
 * 的 10.x）发的也是私网 IP，而那时候对面可能在几百公里外。
 */
export function onLoopback(): boolean {
  const h = location.hostname
  return h === 'localhost' || h.endsWith('.localhost') // RFC 6761 保留给回环
    || h === '::1' || h === '[::1]' // hostname 一般已经脱了方括号，兜一手
    || /^127\./.test(h)
}

export function useLanDirect(lan: State['lan'], enabled: boolean) {
  useEffect(() => {
    const origins = lan?.origins ?? []
    // 老版本的壳不认这条消息，等于没有直连（它本来就因为证书对不上 IP 连不上直连口）
    if (enabled && origins.length && isNative()) postNative({ t: 'lan', origins })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [enabled, lan?.origins?.join(',')])
}
