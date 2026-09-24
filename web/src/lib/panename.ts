/**
 * 「这个 pane 叫什么」那一份，**面板一览和 chat 头共用**（各写一份迟早只有一边对）。
 */

/**
 * 去掉标题前面那个状态字形。Claude Code 会在终端标题前挂一个转圈的符号（`✳ 图片识别`、
 * `◐ Herdr session URL 路由`），herdr 的 `terminal_title_stripped` 只剥掉了一部分
 * （实拍见过 ◐ 留在里面）。这一行左边已经有一个状态点了，再挂一个抖动的字形只是噪音。
 *
 * 只吃「符号 + 空白」这种开头，所以 `~/subhub`（符号后面没空格）不会被误伤。
 */
export const cleanTitle = (t: string) => t.replace(/^[^\p{L}\p{N}\s]+\s+/u, '')

/**
 * 「这个 pane 在聊什么」—— 拿不到就回空串，调用方自己退回 tab 名 / cwd。
 *
 * **泛标题当没有**：刚开的会话、还没聊出话题时，claude 把终端标题写成 `Claude Code`
 * （真机上一把 pane 里就有两个），而那五个字一个信息都不带 —— 摆在主位比 tab 名还差。
 * 判据只认「整条就是那几个泛名字」，所以 `Claude Code 插件调研` 这种照旧留着。
 */
const GENERIC = /^(claude code|claude|codex|zsh|bash|node)$/i

export function paneTitle(p: { agent?: string; title?: string }): string {
  if (!p.agent) return '' // shell pane 的标题多半是 shell 名 / 路径，没意义
  const t = cleanTitle(p.title ?? '').trim()
  return GENERIC.test(t) ? '' : t
}

/**
 * tab 叫什么：**「Tab 名 · Space 名」**（中间是间隔号 `·`，不是小数点），所有 tab 都这样。
 *
 * 为什么带上 space：没起过名的 tab 在 herdr 里就是个序号（`1`、`2`），而好几个 space 里
 * 都有一个 `1` —— 单独摆出来一个信息都不带（用户报的：chat 头上就一个孤零零的「1」）。
 * 起过名的也一样带上：同名的 tab 在不同 space 里是常态，带上 space 才分得开。
 * 两个一样（tab 就叫 space 的名字）时只留一个，别摆成 `videomake · videomake`。
 */
export function tabName(p: { tab?: string; workspace?: string }): string {
  const t = (p.tab ?? '').trim()
  const w = (p.workspace ?? '').trim()
  if (!t) return w
  if (!w || w === t) return t
  return `${t} · ${w}`
}
