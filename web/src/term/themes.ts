import type { ITheme } from '@xterm/xterm'

export type Scheme = 'dark' | 'light'

/*
  只有**灰阶和光标**跟着界面走（见 index.css：底色 = --color-bg，光标 = 品牌绿，
  选区是半透明的绿）；红黄蓝品青那几个色相**一个都没动**。
  那六个是程序输出的颜色 —— diff 的红绿、agent 的高亮全靠它们，换掉等于把别人的
  输出改了；而底色偏蓝（原来的 #1b1e24）才是「界面显脏」的来源。
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

export const initialScheme = (): Scheme =>
  matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark'
