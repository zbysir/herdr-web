import { useEffect } from 'react'

/**
 * 把页面高度定到 visualViewport 上。
 *
 * 光重排终端不够：iOS 的键盘**从不**缩布局视口，Android 也要 viewport meta 里的
 * interactive-widget 才缩。html/body 写 height:100% 的话，100% 指的是没缩过的布局
 * 视口，于是键盘直接盖住底下的快捷键条和发件箱 —— 终端重排了也看不见。
 */
export function useViewportHeight(onResize?: () => void) {
  useEffect(() => {
    const vv = window.visualViewport
    if (!vv) return
    const apply = () => {
      document.documentElement.style.setProperty('--vvh', `${Math.round(vv.height)}px`)
      // 键盘弹出时浏览器有时会把整页顶上去，拉回来，否则顶栏跑出屏幕
      if (vv.offsetTop || window.scrollY) window.scrollTo(0, 0)
    }
    const onVVResize = () => { apply(); onResize?.() }
    apply()
    vv.addEventListener('resize', onVVResize)
    vv.addEventListener('scroll', apply)
    return () => { vv.removeEventListener('resize', onVVResize); vv.removeEventListener('scroll', apply) }
  }, [onResize])
}
