import type { ITheme } from '@xterm/xterm'

export type Scheme = 'dark' | 'light'

export const THEMES: Record<Scheme, ITheme> = {
  dark: {
    background: '#1b1e24', foreground: '#c8ccd4', cursor: '#61afef', cursorAccent: '#1b1e24',
    selectionBackground: '#3e4451',
    black: '#282c34', red: '#e06c75', green: '#98c379', yellow: '#e5c07b',
    blue: '#61afef', magenta: '#c678dd', cyan: '#56b6c2', white: '#abb2bf',
    brightBlack: '#5c6370', brightRed: '#ef8b93', brightGreen: '#a9d989', brightYellow: '#efd094',
    brightBlue: '#7fc1f5', brightMagenta: '#d79ae6', brightCyan: '#74ccd6', brightWhite: '#e6e9ef',
  },
  light: {
    background: '#fafafa', foreground: '#383a42', cursor: '#4078f2', cursorAccent: '#fafafa',
    selectionBackground: '#d4d8e0',
    black: '#383a42', red: '#e45649', green: '#50a14f', yellow: '#c18401',
    blue: '#4078f2', magenta: '#a626a4', cyan: '#0184bc', white: '#fafafa',
    brightBlack: '#a0a1a7', brightRed: '#ca4a3f', brightGreen: '#437a3f', brightYellow: '#9a6a00',
    brightBlue: '#3059c4', brightMagenta: '#8a1f88', brightCyan: '#016a99', brightWhite: '#ffffff',
  },
}

export const initialScheme = (): Scheme =>
  matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark'
