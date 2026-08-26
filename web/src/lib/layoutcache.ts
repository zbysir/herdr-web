import { INSTALL, type Pin, type SoftKey } from './api'

/**
 * 上一次拿到的排布，镜像在 localStorage 里 —— **只为了第一帧别闪**。
 *
 * 真相在服务端（换台设备、清了缓存、换个浏览器都还在，见 internal/profiles），可那两个 GET
 * 排在 `whoami → state → profiles/hello` 后面，最快也要好几百毫秒：这段时间顶栏画的是前端
 * 那份出厂顺序、快捷键条是空的、顶栏上的「我的按键」（`key:` 引用）连定义都还没有，等响应回来
 * 整条栏跳一下。用户报的就是「刷新页面顶部始终会闪动」。
 *
 * 所以和 prefs 一个模型（见 prefs.ts）：**服务端为准 + localStorage 镜像**。区别是这份镜像
 * **只在初值那一下读**，响应回来照旧整份盖上去 —— 盖上去的要是一样，React 渲染出的 DOM 就
 * 一样，屏幕上什么都不动；真变了（在别的设备上改过排布）才跳那一下，那本来就该跳。
 *
 * 三条：
 *
 *   - 只存**渲染要的那点东西**：按键定义、每行放哪几个、顶栏那串 id。`presets` / `max` /
 *     `actions` 是编辑器才用的（预设那几组好几十 KB），而编辑器打开时本来就要现拉一次。
 *   - **带 installId**：「这台设备用哪一套」是按它算的（见 api.ts）。局域网直连那一跳会把
 *     另一个 install 采纳过来，那时这份镜像是**别的浏览器**那套排布，对不上就当没有。
 *   - 形状认不出一律当没有（旧版本存的、手改坏的）。这只是「第一帧画什么」，退回出厂顺序
 *     没有任何代价，而让一份坏 JSON 把 App 拦在白屏上是灾难。
 */
const KEY = 'layoutCache'

export interface LayoutCache {
  /** 这份镜像是哪个浏览器（installId）的。对不上就整份作废 */
  install: string
  /** 这台设备当时绑在哪一套上（设置面板的标题、pushPref 都要它） */
  profile?: { id: string; name: string }
  /** 快捷键条：`resolveRows` / `libMap` 要的三样，见 lib/api.ts */
  softkeys?: { lib: SoftKey[]; bar: string[][]; pin?: Pin[] | null }
  /** 顶栏那串 id（**已经过滤过**：认不出的按钮和坏引用不进镜像） */
  topbar?: string[]
}

const strs = (v: unknown): v is string[] => Array.isArray(v) && v.every((x) => typeof x === 'string')

/** 读镜像。没有 / 不是这个浏览器的 / 形状不对都给 null（调用方退回出厂那份） */
export function readLayoutCache(): LayoutCache | null {
  let c: LayoutCache | null = null
  try {
    c = JSON.parse(localStorage.getItem(KEY) ?? 'null') as LayoutCache | null
  } catch {
    return null
  }
  if (!c || typeof c !== 'object' || c.install !== INSTALL) return null

  const out: LayoutCache = { install: INSTALL }
  const p = c.profile
  if (p && typeof p.id === 'string' && typeof p.name === 'string') out.profile = { id: p.id, name: p.name }
  const s = c.softkeys
  if (s && Array.isArray(s.lib) && Array.isArray(s.bar) && s.bar.every(strs)) {
    // lib 里混进 null 的话 libMap 会当场抛（它读 k.id）—— 这一层是给「文件被手改过」兜底的
    out.softkeys = {
      lib: s.lib.filter((k): k is SoftKey => !!k && typeof k === 'object'),
      bar: s.bar,
      pin: Array.isArray(s.pin) ? s.pin : null,
    }
  }
  if (strs(c.topbar)) out.topbar = c.topbar
  return out
}

/**
 * 更新镜像里的一部分（快捷键条和顶栏是两个口，分两次回来）。
 *
 * 写不进去就算了（存储满了、Safari 隐私模式）：最坏的后果是下次刷新第一帧退回出厂顺序，
 * 为它弹一条提示反而是把一个没人在乎的失败摆到脸上。
 */
export function cacheLayout(patch: Partial<Omit<LayoutCache, 'install'>>) {
  const cur = readLayoutCache() ?? { install: INSTALL }
  try {
    localStorage.setItem(KEY, JSON.stringify({ ...cur, ...patch, install: INSTALL }))
  } catch { /* 见上 */ }
}
