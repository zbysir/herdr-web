/*
 * service worker：两件事 —— 系统通知，和让浏览器认这个站点「可安装」。
 *
 * 【为什么有它】
 * 1. 通知：iOS 上把页面装到主屏之后，`new Notification()` 这个构造函数**根本不存在**，
 *    唯一能弹的路子是 `registration.showNotification()` —— 那就要求有一个注册好的 SW。
 *    桌面浏览器两条路都行，所以统一走这条，少一套分支。
 * 2. 可安装：Chromium（Chrome / Edge）给「装 PWA」和「建个快捷方式」分了两档，
 *    菜单里那个**「安装」**要同时满足：manifest（名字 / 192+512 图标 / start_url /
 *    display=standalone）、**证书有效的 https**、以及**一个带 `fetch` 处理器的 SW**。
 *    少最后这条的表现正是用户报的那个：手机上点安装只给「创建快捷方式」，
 *    装出来的还是个开浏览器的书签，没有独立窗口。
 *
 * 【这个 fetch 处理器有意做得很小】
 * 它**只接管导航请求**（`mode === 'navigate'`，也就是开页面那一下），而且是
 * network-first：网络通就原样用网络那份，一个字节都不缓存。别的请求（/api、/pty 的
 * WebSocket、带哈希的 assets、/_f/ 吐文件）**碰都不碰** —— 这个页面是个终端，
 * 缓存资源没有意义，而一个会拦请求的 SW 在这种应用上只会制造「明明改了却没生效」
 * 这类查半天的怪事。network-first + 只管导航，等于「改了立刻生效」这条性质没动过。
 *
 * 【为什么还是预缓存了一个 offline.html】
 * 光有一个 `respondWith(fetch(e.request))` 的空处理器在手机上就够了，但桌面 Chrome
 * 还会**真的断网探一次 start_url**（offline capability 检查），探不到 200 一样不给装。
 * 所以缓存一张自包含的离线页兜底。**不缓存 index.html**：它引的是带哈希的 assets，
 * 那些没缓存，离线打开只会白屏 —— 比一张说清楚状况的静态页更糟。
 *
 * 【改这个文件要知道的两条】
 * ① `fetch` 监听器删了 / 改成不调 `respondWith`，「安装」那一档当场消失，而页面
 *    一切正常，**没有任何症状**；② 缓存名带版本，加东西就把 `SHELL` 的版本号 +1，
 *    否则 activate 那一步不会清掉旧的那份。
 */
const SHELL = 'herdr-web-shell-v1'
const OFFLINE = '/offline.html'

self.addEventListener('install', (e) => {
  e.waitUntil((async () => {
    try {
      const c = await caches.open(SHELL)
      // cache:'reload' 绕开 http 缓存，别把一张过期的离线页焊进来
      await c.add(new Request(OFFLINE, { cache: 'reload' }))
    } catch {
      // 抓不到就算了：**绝不能让 install 失败**，那样整个 SW 装不上，
      // 连通知都一起没了 —— 而离线页只是可安装判据里锦上添花的那一条。
    }
    await self.skipWaiting()
  })())
})

self.addEventListener('activate', (e) => {
  e.waitUntil((async () => {
    for (const k of await caches.keys()) {
      if (k !== SHELL) await caches.delete(k)
    }
    await self.clients.claim()
  })())
})

self.addEventListener('fetch', (e) => {
  // 只管开页面那一下。其余一律不 respondWith —— 让浏览器按原样走，
  // 这样 /api、WebSocket、带哈希的资源全都不经过这里。
  if (e.request.mode !== 'navigate') return
  e.respondWith((async () => {
    try {
      return await fetch(e.request)
    } catch {
      // 真的连不上（飞行模式 / 后端没跑 / 隧道断了）才走到这儿
      const hit = await caches.match(OFFLINE)
      return hit || Response.error()
    }
  })())
})

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
