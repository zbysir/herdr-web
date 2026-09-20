// 注册 service worker。**进页面就注册**，不再等「打开系统通知」那一下。
//
// 为什么改：SW 现在有两个消费者，而第二个要求它一直在。
//   1. 系统通知 —— iOS 上只能 `registration.showNotification()`（见 lib/notify.ts）；
//   2. **可安装**（用户报的：手机上点安装只给「创建快捷方式」，没有「安装」那一档）——
//      Chromium 判一个站点能不能装成 PWA，要求**页面上已经有一个带 fetch 处理器的 SW**。
//      按需注册的话，没开通知的人（绝大多数）身上压根没有 SW，浏览器就只把这儿当成
//      一个普通网页，菜单里自然只剩「创建快捷方式」。
// 代价是所有人身上都会有一个 SW；换来的是它只接管导航请求且 network-first，
// 「改了立刻生效」这条性质没动过（理由写在 web/public/sw.js 的头注释里）。
//
// 两条实现上的讲究：
// ① **幂等**：注册这件事全局只做一次，重复调拿的是同一个 promise（notify.ts 每次弹
//    通知都要 registration，不能每次都去 register 一遍）；
// ② **等 load 之后再注册**：首屏那几拍（whoami → state → profiles → 连 WebSocket）
//    是人在盯着的，SW 的 install 还要顺手抓一次 /offline.html，别去抢那几个连接。

let pending: Promise<ServiceWorkerRegistration | null> | null = null

/** 返回注册好的 SW；null = 这个环境用不了（不支持 / 非安全上下文 / 被禁用） */
export function registerSW(): Promise<ServiceWorkerRegistration | null> {
  if (pending) return pending
  pending = (async () => {
    // http 上（既不是 https 也不是 localhost）连注册都发不出去，别留个报错在控制台
    if (!('serviceWorker' in navigator) || !window.isSecureContext) return null
    try {
      return await navigator.serviceWorker.register('/sw.js', { scope: '/' })
    } catch {
      return null // 注册失败（SW 被禁用、隐私模式）就当没有，界面按「不支持」说
    }
  })()
  return pending
}

/** 首屏那几拍过去之后再注册。main.tsx 里调一次。 */
export function registerSWWhenIdle() {
  if (document.readyState === 'complete') {
    void registerSW()
    return
  }
  window.addEventListener('load', () => { void registerSW() }, { once: true })
}
