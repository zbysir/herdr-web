import { useEffect, useState } from 'react'

/**
 * 手机竖屏这一档的上界（px，不含）。和 index.css 里的 `--breakpoint-phone` 是同一个数 ——
 * 那边给 Tailwind 的 `phone:` / `max-phone:` 变体用，这边给必须在 JS 里分岔的地方用
 * （行内 style、要不要**渲染**把手，这些 CSS 盖不掉）。改一个就得改另一个。
 */
export const PHONE_MAX = 440

// 439.98 而不是 439：`< 440px` 的准确写法。CSS 的范围语法（width < 440px）老一点的
// 国产浏览器不一定认，这里用兼容写法。
const mq = () => matchMedia(`(max-width: ${PHONE_MAX - 0.02}px)`)

/** 现在是不是手机竖屏那一档（跟着窗口 / 转屏变）。 */
export function usePhone() {
  const [phone, setPhone] = useState(() => mq().matches)
  useEffect(() => {
    const m = mq()
    const f = () => setPhone(m.matches)
    f()
    m.addEventListener('change', f)
    return () => m.removeEventListener('change', f)
  }, [])
  return phone
}
