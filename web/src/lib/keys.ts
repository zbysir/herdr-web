import type { CSSProperties } from 'react'

/**
 * 一个键最多占几格宽。和服务端 `softkeys.MaxSpan` 是同一个数。
 *
 * 3 格（≈144px，手机上 ≈120px）已经是半条屏幕，再宽就该拆成两个键了。
 */
export const MAX_SPAN = 3

/**
 * 「占几格宽」→ 行内 `min-width`。
 *
 * 格宽（`--sk-w`）和键之间的空（`--sk-gap`）是 CSS 变量（见 index.css，手机竖屏窄一档），
 * 所以 span N 正好是 N 个键位**加上中间那些空** —— 少算 gap 的话两格的键比上面两个一格的
 * 键窄 6px，一眼就看出没对齐，而对齐是固定块（网格）唯一的卖点。
 *
 * 为什么用行内 style 而不是 Tailwind 类：span 是**数据**（1/2/3 从配置来），拼类名的话
 * 每一档都得进 safelist，以后改上限还要再加一条 —— 而 CSP 那边行内 style 本来就放行了
 * （xterm 自己插 style，面板拖动也在用）。
 *
 * 1 格返回 undefined：那就是 `variant.key` 自带的 min-w，别白挂一条行内样式上去。
 */
export function spanStyle(span?: number): CSSProperties | undefined {
  const n = Math.min(MAX_SPAN, Math.max(1, Math.round(span ?? 1)))
  if (n === 1) return undefined
  return { minWidth: `calc(var(--sk-w) * ${n} + var(--sk-gap) * ${n - 1})` }
}

/**
 * 弹出组最多几列。和服务端 `softkeys.MaxGroupCols` 是同一个数。
 * 浮窗要能落在屏幕里，5 列 ≈ 244px 已经接近手机竖屏的宽度。
 */
export const MAX_GROUP_COLS = 5
