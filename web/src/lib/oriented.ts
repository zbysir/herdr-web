import { useEffect, useState } from 'react'

/**
 * 「横屏一套、竖屏一套」的本地存储。
 *
 * 同一块面板在横屏和竖屏下想要的尺寸位置根本不是一回事：竖屏里发件箱贴在下半屏、
 * 快捷键条排三行；转成横屏那份位置立刻变得离谱（而且反过来会把另一份覆盖掉）。所以
 * 按朝向各存一份，转屏就换成那一份 —— 各自调各自的，互不影响。
 *
 * 朝向用宽高比判断而不是 `screen.orientation`：桌面窗口、分屏、平板底座模式下宽高比
 * 才是真正决定布局的东西，而 orientation API 在这些情况里要么没有要么骗人。
 */
export type Orient = 'land' | 'port'

export const orient = (): Orient => (window.innerWidth >= window.innerHeight ? 'land' : 'port')

/** 朝向变了就重渲染（转屏、拖窗口、平板分屏都算） */
export function useOrient(): Orient {
  const [o, setO] = useState(orient)
  useEffect(() => {
    const fix = () => setO(orient())
    addEventListener('resize', fix)
    addEventListener('orientationchange', fix)
    return () => {
      removeEventListener('resize', fix)
      removeEventListener('orientationchange', fix)
    }
  }, [])
  return o
}

/**
 * 读当前朝向那一份。
 *
 * 没有的话退回**不带后缀的老键**（分朝向之前存的），顺手迁到当前朝向名下 ——
 * 用户已经调好的那一份不该因为升级就没了。
 */
export function readOriented<T>(key: string, valid: (v: unknown) => boolean): T | null {
  const own = `${key}:${orient()}`
  for (const k of [own, key]) {
    const raw = localStorage.getItem(k)
    if (raw == null) continue
    try {
      const v = JSON.parse(raw) as unknown
      if (!valid(v)) {
        localStorage.removeItem(k)
        continue
      }
      if (k !== own) {
        localStorage.setItem(own, raw)
        localStorage.removeItem(k)
      }
      return v as T
    } catch {
      localStorage.removeItem(k) // 存坏了，别让它一直挡着
    }
  }
  return null
}

export function writeOriented(key: string, value: unknown) {
  localStorage.setItem(`${key}:${orient()}`, JSON.stringify(value))
}

export function clearOriented(key: string) {
  localStorage.removeItem(`${key}:${orient()}`)
}
