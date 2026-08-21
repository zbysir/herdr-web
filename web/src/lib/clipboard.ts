/**
 * 写系统剪贴板。**手机上这件事会失败，而且默认是静默失败的** —— 这个模块存在的理由
 * 就是把「失败」变成一件看得见、点一下能补救的事。
 *
 * 两条限制叠在一起：
 *
 *  1. `navigator.clipboard` **只在安全上下文里存在**。局域网 http（`http://192.168.x.x:7788`）
 *     上压根没有这个对象，只剩下面那条老办法。
 *  2. 手机浏览器要求写剪贴板发生在**用户手势**里（transient activation）。iOS Safari 和
 *     Android Chrome 在没有手势时直接 reject，`execCommand('copy')` 同样不给。
 *
 * 而终端里真正会触发复制的两条路**都不是点击**：herdr 自己的 COPY 模式（`y` → OSC 52 →
 * 这里）和「选中即复制」（选区变化）。所以手机上它们必然掉到第 2 条上，屏幕上选区好好的、
 * 一句提示都没有，人以为复制成功了 —— 这就是「手机上选中之后没有复制成功」。
 *
 * 兜底在 UI 那边：写不进去就把文本交回去，页面上出现一个「点一下复制」，**那一下点击
 * 本身就是手势**，这时候第 1 条才允许写。见 components/CopyPrompt.tsx。
 */

/**
 * 等 `navigator.clipboard.writeText` 的上限（毫秒）。超时就当写不进去。
 *
 * 为什么不能光 `await`：**标签页不可见时 Chrome 让这个 promise 永远挂着** —— 实测在隐藏
 * 的标签页里发起一次写，26 秒后既没 resolve 也没 reject，剪贴板里也确实没变。光 await
 * 的话「写不进去」这件事永远不会被发现，又回到静默失败。
 *
 * 1.2 秒对一次「写几行文本」是很宽的（正常是毫秒级）。超时之后那个 promise 可能后来才
 * 落地（标签页被切回来时），那时候剪贴板里就是同一段文本，最多是提示条白出现一次。
 */
const WRITE_TIMEOUT = 1200

/**
 * `?nocopy=1` 把两条路都当成写不进去，用来在**手边这台机器上**看那个「点一下复制」的提示条。
 *
 * 为什么要留这么个开关：真实的失败只在手机上发生（没有用户手势那一档），而桌面浏览器里
 * `execCommand` 那条几乎总是成功 —— 这段兜底逻辑于是没法在改它的那台机器上验。
 * 和 `?poll=` / `?push=` 一样是调试参数，见 README。
 */
const FORCE_FAIL = new URLSearchParams(location.search).has('nocopy')

/** 试着写剪贴板，成功返回 true。别在这儿抛错 —— 调用方要的是「成了没有」。 */
export async function writeClipboard(text: string): Promise<boolean> {
  if (!text) return true
  if (FORCE_FAIL) return false
  if (await asyncClipboard(text)) return true
  return legacyCopy(text)
}

async function asyncClipboard(text: string): Promise<boolean> {
  if (!navigator.clipboard?.writeText) return false // 不是安全上下文（局域网 http）
  try {
    return await Promise.race([
      navigator.clipboard.writeText(text).then(() => true),
      new Promise<boolean>((done) => setTimeout(() => done(false), WRITE_TIMEOUT)),
    ])
  } catch {
    return false // 手机上没有用户手势时就是这一条（NotAllowedError）
  }
}

/**
 * `execCommand('copy')` 那套老办法。
 *
 * 几个细节都是 iOS 逼出来的：框**不能真的隐藏**（`display:none` / `opacity:0` 的框选不中，
 * 直接失败），所以挪到屏幕外；`readonly` 是为了别把系统键盘顶出来；选中只认
 * `setSelectionRange`，`select()` 在 iOS 上对 textarea 不管用；字号写 16px 免得 iOS
 * 顺手把整个页面缩放一下。
 */
function legacyCopy(text: string): boolean {
  const active = document.activeElement as HTMLElement | null
  const ta = document.createElement('textarea')
  ta.value = text
  ta.readOnly = true
  ta.style.cssText = 'position:fixed;top:0;left:-9999px;font-size:16px'
  document.body.appendChild(ta)
  ta.focus()
  ta.setSelectionRange(0, text.length)
  let ok = false
  try {
    ok = document.execCommand('copy')
  } catch {
    ok = false
  }
  ta.remove()
  // 焦点还回去（多半是 xterm 那个隐藏 textarea）。**不主动 focus 终端** —— 手机上那一下
  // 会把键盘顶出来，而这会儿人是在复制，不是要打字。
  active?.focus?.()
  return ok
}

/**
 * 等读剪贴板的上限（毫秒）。比写那边宽得多是因为**这一步可能在等人**：Chrome 读剪贴板
 * 会弹一次「粘贴」确认（Android 上是个系统小浮层），用户点了才 resolve。
 *
 * 但也不能不设上限：`readText` 和 `writeText` 一样，在标签页不可见时**永远挂着**
 * （实测点一下「粘」什么都不发生 —— 既没粘进来，也没弹出兜底的框）。超时之后就摊那个
 * 「长按这儿粘贴」的框，那条路不依赖任何权限。
 */
const READ_TIMEOUT = 8000

/**
 * 读手机剪贴板。`null` = 读不到（不是安全上下文、没有这个 API、用户拒了、或者超时）。
 *
 * 和写一样吃**用户手势**那条限制，所以只能在点击的处理函数里调 —— 软键条上那个「粘」键
 * 就是为这一下存在的。
 *
 * 读不到就交给 UI 摊一个框让人长按粘（components/PastePrompt.tsx）—— 手机上「长按 →
 * 粘贴」永远是通的，因为那是浏览器自己的菜单，不受这套权限管。
 *
 * 超时之后那次读可能**后来才落地**（用户过了很久才点确认）；调用方拿到的是 null，
 * 那份迟到的文本直接丢掉 —— 宁可让人在框里再粘一次，也不能过一会儿突然往终端里塞一段。
 */
export async function readClipboard(ms = READ_TIMEOUT): Promise<string | null> {
  if (!navigator.clipboard?.readText) return null
  try {
    return await Promise.race([
      navigator.clipboard.readText(),
      new Promise<null>((done) => setTimeout(() => done(null), ms)),
    ])
  } catch {
    return null
  }
}
