// 跑在原生壳（`app/android`）里时，壳往页面里注入的那条线。另一半在
// `app/android/.../Bridge.kt`，消息格式两边要对得上。为什么是「壳 + WebView」见
// docs/dev/NATIVE-APP.md。
//
// 壳用 `addWebMessageListener` 注入 `herdrNative`，而且**只对它连的那台服务器的 origin 注入**
// —— 所以「有这个对象」就是「我在 App 里，而且是 App 认的那台」。局域网直连切到别的
// origin 上时它不在，页面退回普通浏览器的行为（全屏走 Fullscreen API 之类），不用特判。
//
// 对象在页面脚本跑之前就在了（document start 注入），所以模块顶层读一次就够。

export type NativeMsg = { t: string; [k: string]: unknown }

type Port = {
  postMessage(data: string): void
  onmessage: ((e: { data: string }) => void) | null
}

const port = (globalThis as unknown as { herdrNative?: Port }).herdrNative
const subs = new Map<string, Set<(m: NativeMsg) => void>>()

if (port) {
  port.onmessage = (e) => {
    let m: NativeMsg
    try {
      m = JSON.parse(e.data) as NativeMsg
    } catch {
      return
    }
    subs.get(m.t)?.forEach((f) => f(m))
  }
  // 壳要先收到一次 hello 才拿得到回信的通道（返回键、全屏结果都靠它往回发）
  postNative({ t: 'hello' })
}

export function isNative(): boolean {
  return !!port
}

export function postNative(m: NativeMsg): void {
  port?.postMessage(JSON.stringify(m))
}

/** 订阅壳发来的某一类消息。返回退订函数 */
export function onNative(t: string, f: (m: NativeMsg) => void): () => void {
  let s = subs.get(t)
  if (!s) subs.set(t, (s = new Set()))
  s.add(f)
  return () => { s.delete(f) }
}

/**
 * 把当前底色告诉壳：系统栏那一条（状态栏 / 导航栏）是壳画的，不跟着就会出现「亮色主题顶上
 * 一条黑」。读的是 CSS 变量（色值只有 index.css 一份出处），所以必须在 `.light` 类落到
 * `<html>` 上**之后**调（和终端读主题色是同一条顺序规矩）。
 */
export function syncNativeTheme(): void {
  if (!port) return
  const bg = getComputedStyle(document.documentElement).getPropertyValue('--color-bg').trim()
  if (!bg) return
  postNative({ t: 'theme', bg, light: document.documentElement.classList.contains('light') })
}
