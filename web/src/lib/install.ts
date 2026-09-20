// 「装成 app」那个按钮背后的东西。
//
// **这个模块必须在 React 之前被 import**（main.tsx 里第一批），因为 Chromium 的
// `beforeinstallprompt` **在页面加载早期就触发一次，而且只触发一次** —— 等设置面板挂载
// 时再挂 listener 是**抓不到的**，表现是「浏览器菜单里明明有『安装』，我们自己的按钮却
// 一直说不能装」。所以在模块顶层就挂住，把那个事件存下来，谁要用谁来取。
// （同一条坑在验证时也踩过：用自动化事后挂 listener 会得出「不可安装」这个错结论。）
//
// 另外那个事件对象是**一次性**的：`prompt()` 只能调一次，调完这一份就作废了，
// 想再装得重新加载页面。所以用掉之后要把它丢掉，界面跟着改口。

/** 'installed' 已经在独立窗口里跑了；'ready' 点一下就能装；'no' 浏览器没给我们这条路 */
export type InstallState = 'installed' | 'ready' | 'no'

type Prompt = Event & {
  prompt: () => Promise<void>
  userChoice: Promise<{ outcome: 'accepted' | 'dismissed' }>
}

let deferred: Prompt | null = null
const subs = new Set<() => void>()
const emit = () => subs.forEach((f) => f())

/**
 * 已经从主屏 / 独立窗口打开了？iOS Safari 只有 `navigator.standalone` 这一个判据。
 *
 * 导出是因为**「呼出键盘自动全屏」那条路要拿它当闸**：装成 app 之后地址栏和工具条本来
 * 就没有，`requestFullscreen` 再要一次拿不回任何高度，只会赔上一次重排（清画布 +
 * SIGWINCH + herdr 清屏重画）和 Android 上那条「已进入全屏」的系统提示。见 App 的 enterFull。
 *
 * 一个 document 活着的时候这个值不会变（装完 app 之后，当前这个标签页仍然是 browser
 * 模式，人得从图标重新打开），所以读一次就够，没上 matchMedia 的 change 监听。
 */
export function isStandalone(): boolean {
  if (typeof matchMedia === 'function' && matchMedia('(display-mode: standalone)').matches) return true
  return (navigator as unknown as { standalone?: boolean }).standalone === true
}

export function installState(): InstallState {
  if (isStandalone()) return 'installed'
  return deferred ? 'ready' : 'no'
}

/** 订阅状态变化（事件来了 / 用掉了 / 装好了）。返回退订函数 */
export function onInstallChange(fn: () => void): () => void {
  subs.add(fn)
  return () => { subs.delete(fn) }
}

/**
 * 弹浏览器自己那个安装对话框。**必须在用户手势里调**。
 * 返回 'accepted' | 'dismissed' | 'gone'（'gone' = 这一份已经用掉了，得刷新页面）。
 */
export async function promptInstall(): Promise<'accepted' | 'dismissed' | 'gone'> {
  const p = deferred
  if (!p) return 'gone'
  // 先丢掉再弹：这一份不管结果如何都作废了，留着只会让按钮看起来还能再点一次
  deferred = null
  emit()
  await p.prompt()
  const { outcome } = await p.userChoice
  return outcome
}

if (typeof window !== 'undefined') {
  window.addEventListener('beforeinstallprompt', (e) => {
    // 拦下浏览器自己那个横幅，改由设置里那个按钮来弹 —— 不拦的话 Android 上会在页面
    // 底下自己冒一条，而这个页面底下正是发件箱和快捷键条
    e.preventDefault()
    deferred = e as Prompt
    emit()
  })
  window.addEventListener('appinstalled', () => {
    deferred = null
    emit()
  })
}
