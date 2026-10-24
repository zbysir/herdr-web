/**
 * 端口代理（internal/portproxy）：agent 说「打开 http://localhost:7830/x 看看」，这个链接在手机上
 * 点了是手机自己的 localhost。服务端开了端口代理时，把它换成 `https://7830.<域名>/x` —— 那边
 * 先过 herdr-web 的认证（没登录就绕主站签一张票），再反代到这台机器的 127.0.0.1:7830。
 *
 * 只换「指向本机回环」的 http(s) 链接，别的原样。在跑 herdr-web 的那台机器上开的页面不换：
 * 那里的 localhost 就是对的，绕子域名一圈反而多一跳。
 */
let domain = ''

/** App 拿到 /api/state 之后告诉这里（空 = 没开端口代理）。 */
export function setPortDomain(d: string | undefined) {
  domain = d ?? ''
}

const LOOPBACK = new Set(['localhost', '127.0.0.1', '0.0.0.0', '[::1]'])

export function portLink(href: string): string {
  if (!domain || LOOPBACK.has(location.hostname)) return href
  let u: URL
  try {
    u = new URL(href)
  } catch {
    return href
  }
  if ((u.protocol !== 'http:' && u.protocol !== 'https:') || !LOOPBACK.has(u.hostname) || !u.port) return href
  const p = Number(u.port)
  if (!(p >= 1024 && p <= 65535)) return href // 和服务端同一道线（portproxy.Allowed），别的照原样
  return `https://${p}.${domain}${u.pathname}${u.search}${u.hash}`
}
