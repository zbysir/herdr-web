// 系统通知：把「某个 agent 在等你」送出浏览器窗口，锁屏 / 切到别的 app 也看得见。
//
// **只在页面开着的时候有效**（前台或后台标签页都算，但页面被关掉就没有了）：真正的
// 「关掉浏览器也能收到」要 Web Push —— 那需要 VAPID 密钥、订阅落盘、服务端主动推，
// 是另一整套东西。这一版先把「切出去干别的」这个最常见的场景覆盖掉。
//
// iOS 上有两条硬要求，别踩：
//   1. 必须**添加到主屏幕**从主屏打开（Safari 标签页里的网页拿不到通知权限）；
//   2. `new Notification()` 这个构造函数在 iOS 上**不存在**，只能
//      `registration.showNotification()` —— 所以这里统一走 service worker 那条路。
// 另外权限申请必须在**用户手势**里（设置里点那一下），定时器里偷偷申请一律被拒。

/** 'unsupported' 浏览器没有；'insecure' 不是安全上下文（http 且非 localhost）；其余是权限状态 */
export type NotifyState = 'unsupported' | 'insecure' | 'default' | 'granted' | 'denied'

export function notifyState(): NotifyState {
  if (typeof Notification === 'undefined' || !('serviceWorker' in navigator)) return 'unsupported'
  // http 上（既不是 https 也不是 localhost）连 SW 都注册不了，先说清楚，别让人以为是权限问题
  if (!window.isSecureContext) return 'insecure'
  return Notification.permission as NotifyState
}

/** 注册 SW（按需，不打开这个开关的人身上不会有）。返回 null = 这个环境用不了 */
async function reg(): Promise<ServiceWorkerRegistration | null> {
  if (!('serviceWorker' in navigator) || !window.isSecureContext) return null
  try {
    return await navigator.serviceWorker.register('/sw.js', { scope: '/' })
  } catch {
    return null // 注册失败（http、SW 被禁用）就当没有，界面上按 unsupported 说
  }
}

/**
 * 开通知：注册 SW + 申请权限。**必须在用户手势里调**（设置面板里点那一下）。
 * 返回申请之后的状态，界面照它说话。
 */
export async function enableNotify(): Promise<NotifyState> {
  const s = notifyState()
  if (s === 'unsupported' || s === 'insecure') return s
  await reg()
  try {
    return (await Notification.requestPermission()) as NotifyState
  } catch {
    return 'denied'
  }
}

/**
 * 弹一条。tag 传 terminal_id：同一个 agent 的新提示会**替换**掉旧那条，
 * 而不是在通知中心里堆成一摞（一个 agent 反复跑完的话那摞会很长）。
 */
export async function showNotify(o: { title: string; body: string; tag: string; pane: string }) {
  if (notifyState() !== 'granted') return
  const opts: NotificationOptions = {
    body: o.body,
    tag: o.tag,
    icon: '/icon-192.png',
    badge: '/icon-192.png',
    data: { pane: o.pane },
  }
  const r = await reg()
  if (r) {
    await r.showNotification(o.title, opts)
    return
  }
  try {
    new Notification(o.title, opts) // 没有 SW 的老浏览器还能走构造函数这条
  } catch { /* 弹不出来就算了：提示卡和红点还在，不该为这个报错 */ }
}

/**
 * 「人这会儿在不在看这一页」。**不能只看 `document.hidden`** —— macOS 上你切到别的 app、
 * Chrome 只是失焦，标签页仍然算 visible，`hidden` 一直是 false。于是「切出去干别的」这个
 * 最主要的场景一条通知都不弹（这就是第一版报「系统通知没出来」的原因）。
 *
 * 加上 `hasFocus()`：窗口不在最前面就算「没在看」，这才是这个功能真正想覆盖的状态。
 */
export const away = () => document.hidden || !document.hasFocus()

/** 设置里那个「试一下」：立刻弹一条样例，用来确认权限和系统那侧真的通了 */
export async function testNotify() {
  await showNotify({
    title: '等你回答 · 图片识别',
    body: '这是一条测试通知。真的提示长这样：点一下会把页面拉到前台并跳到那个 pane。',
    tag: 'herdr-web-test',
    pane: '',
  })
}

/** 点了系统通知之后 SW 会回一条消息，页面拿它跳到那个 pane。返回取消订阅的函数 */
export function onNotifyClick(fn: (pane: string) => void): () => void {
  if (!('serviceWorker' in navigator)) return () => {}
  const h = (e: MessageEvent) => {
    const d = e.data as { type?: string; pane?: string } | null
    if (d?.type === 'notice-click' && d.pane) fn(d.pane)
  }
  navigator.serviceWorker.addEventListener('message', h)
  return () => navigator.serviceWorker.removeEventListener('message', h)
}
