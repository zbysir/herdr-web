import type { ITheme } from '@xterm/xterm'

export type Scheme = 'dark' | 'light'

/*
  只有**灰阶和光标**跟着界面走（见 index.css：底色 = --color-bg，光标和选区跟着**主题色**
  走，见下面的 termTheme）；红黄蓝品青那几个色相**一个都没动**。
  那六个是程序输出的颜色 —— diff 的红绿、agent 的高亮全靠它们，换掉等于把别人的
  输出改了；而底色偏蓝（原来的 #1b1e24）才是「界面显脏」的来源。

  这份是**基底**：光标和选区那两个值是出厂绿，真正用的时候由 termTheme 按当前主题色盖掉。
  不跟着盖的两处（OSC 10/11 查询、冻帧铺的底色）读的都是前景 / 背景，和主题色无关。
*/
export const THEMES: Record<Scheme, ITheme> = {
  dark: {
    background: '#121212', foreground: '#d4d4d4', cursor: '#3ecf8e', cursorAccent: '#121212',
    selectionBackground: '#3ecf8e33',
    black: '#242424', red: '#e06c75', green: '#98c379', yellow: '#e5c07b',
    blue: '#61afef', magenta: '#c678dd', cyan: '#56b6c2', white: '#b8b8b8',
    brightBlack: '#6f6f6f', brightRed: '#ef8b93', brightGreen: '#a9d989', brightYellow: '#efd094',
    brightBlue: '#7fc1f5', brightMagenta: '#d79ae6', brightCyan: '#74ccd6', brightWhite: '#f0f0f0',
  },
  light: {
    background: '#fcfcfc', foreground: '#2b2b2b', cursor: '#157f56', cursorAccent: '#fcfcfc',
    selectionBackground: '#157f5626',
    black: '#2b2b2b', red: '#e45649', green: '#50a14f', yellow: '#c18401',
    blue: '#4078f2', magenta: '#a626a4', cyan: '#0184bc', white: '#fcfcfc',
    brightBlack: '#9b9b9b', brightRed: '#ca4a3f', brightGreen: '#437a3f', brightYellow: '#9a6a00',
    brightBlue: '#3059c4', brightMagenta: '#8a1f88', brightCyan: '#016a99', brightWhite: '#ffffff',
  },
}

/**
 * 开页面时是亮还是暗：**自己点过就照自己点的**（那一下进了 profile，见 lib/prefs.ts），
 * 没点过才跟系统走。
 *
 * 读的是 localStorage 那份镜像而不是等服务端：主题得在第一帧就定下来，晚一拍就是白底
 * 闪一下再变黑。
 */
export const initialScheme = (): Scheme => {
  const own = localStorage.getItem('scheme')
  if (own === 'dark' || own === 'light') return own
  // **没选过就是暗色，不跟浏览器偏好**（用户点名的）。原来是按 prefers-color-scheme 给，
  // 系统白天自动亮、晚上自动暗 —— 人要的是「默认暗、想亮自己切」
  return 'dark'
}

/**
 * 当前该给 xterm 的那份主题 = 基底 + **把光标和选区换成当前主题色**。
 *
 * 颜色是从 `<html>` 上现读 CSS 变量（`--color-brand`）的，不在 TS 里再写一份色表 ——
 * 主题色有 6 套 × 明暗两份，抄一份到这边就是 24 个值要跟着 index.css 一起改，而对不上
 * 的表现是「界面紫了、光标还是绿的」这种没人会去报的小毛病。
 *
 * 由此来的一条**顺序**规矩：调它之前，`.light` 类和 `data-brand` 必须已经落在 `<html>`
 * 上（见 App.tsx 那两个 effect）—— 反过来的话读到的是上一套的值，而且会一直错到下次切换。
 *
 * 读 CSS 变量本身是即时的（自定义属性没有过渡），不受「后台标签页量不出样式」那条影响
 * （那条说的是 transition 停在 currentTime 0，见 CLAUDE.md）。读不出来就退回基底。
 */
export function termTheme(scheme: Scheme): ITheme {
  const base = THEMES[scheme]
  let brand = ''
  try {
    brand = getComputedStyle(document.documentElement).getPropertyValue('--color-brand').trim()
  } catch { /* 拿不到就用基底那份绿 */ }
  if (!/^#[0-9a-fA-F]{6}$/.test(brand)) return base
  // 选区的透明度照旧：暗色 0x33、亮色 0x26（亮底上同样的 alpha 会把字压得看不清）
  return { ...base, cursor: brand, selectionBackground: brand + (scheme === 'dark' ? '33' : '26') }
}
