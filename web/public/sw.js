/*
 * 只为「系统通知」存在的 service worker。
 *
 * 它**不缓存任何东西**（连 fetch 事件都不听）：这个页面是个终端，离线缓存没有意义，
 * 而一个会拦请求的 SW 在这种应用上只会制造「明明改了却没生效」这类查半天的怪事。
 *
 * 为什么非要有它：iOS 上把页面装到主屏之后，`new Notification()` 这个构造函数**根本不存在**，
 * 唯一能弹的路子是 `registration.showNotification()` —— 那就要求有一个注册好的 SW。
 * 桌面浏览器两条路都行，所以统一走这条，少一套分支。
 *
 * 它是**按需注册**的（设置里打开「系统通知」那一下才注册），不打开的人身上没有 SW。
 */
self.addEventListener('install', () => self.skipWaiting())
self.addEventListener('activate', (e) => e.waitUntil(self.clients.claim()))

/*
 * 点通知：把已经开着的那个页面拉到前台，并告诉它跳去哪个 pane（页面自己去调 goto，
 * 因为跳转要带 cookie 和 session 参数，SW 这儿不该再复制一份那套逻辑）。
 * 一个都没开着就开一个 —— 那时候只能落到首页，pane 跟着 URL 带过去。
 */
self.addEventListener('notificationclick', (event) => {
  const pane = (event.notification.data && event.notification.data.pane) || ''
  event.notification.close()
  event.waitUntil((async () => {
    const all = await self.clients.matchAll({ type: 'window', includeUncontrolled: true })
    for (const c of all) {
      if (new URL(c.url).origin === self.location.origin) {
        await c.focus()
        c.postMessage({ type: 'notice-click', pane })
        return
      }
    }
    await self.clients.openWindow(pane ? `/?goto=${encodeURIComponent(pane)}` : '/')
  })())
})
