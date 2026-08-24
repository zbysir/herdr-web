import { useEffect, useState } from 'react'

/**
 * 系统键盘**大概**弹起来了 —— 按视口被压掉多少判断。
 *
 * 为什么不用「xterm 那个隐藏 textarea 聚焦了没有」（Session 报的 kbdUp）：那只覆盖
 * 「对着终端打字」。这个项目最常见的姿势是**在发件箱里口述**，那时候焦点在发件箱的
 * textarea 上，终端那边一无所知，而键盘照样占掉半个屏幕。
 *
 * 判据是「比这个朝向上见过的最高那次矮了 20% 以上」。**不能拿 window.innerHeight 当
 * 基准**（原来就是那么写的，安卓上整个信号是死的）：viewport meta 里有
 * `interactive-widget=resizes-content`（不加的话 iOS 上键盘直接盖住软键条和发件箱），
 * 而它的语义正是「键盘同时缩布局视口」，于是 innerHeight 跟着 visualViewport 一起变矮、
 * 比值恒等于 1。**这个失败是完全静默的**：iOS 上照旧对（iOS 从不缩布局视口），安卓上
 * 表现是「在输入法自己那个收起按钮上收掉键盘之后，⌨ 那个键还亮着，要点两下才能把键盘
 * 叫回来」—— 因为安卓收键盘不 blur 页面元素，而 App.tsx 里那条补救（视口长回去了就把
 * 焦点摘掉，`sawRoom`）压根等不到 kbRoom 变 true。
 *
 * 阈值 0.8：键盘一般吃掉 35%~50% 的高度，而地址栏 / 工具条 / 全屏进出的差不到 15% ——
 * 中间这段空得足够宽。基线**只往上取、不往下修**，所以「进出一次全屏」不会误判；代价是
 * 「页面加载时键盘已经弹着」先当成没弹，等它收一次基线就自己校准了。
 *
 * 基线按**朝向**作废重量：横竖屏高度差得远，拿竖屏那个基线判横屏就是「键盘永远弹着」。
 * 认的是宽度变没变（键盘不改宽度，转屏一定改），而且看 innerWidth 而不是
 * visualViewport.width —— 后者会跟着缩放变。
 *
 * 为什么不用 navigator.virtualKeyboard（安卓 Chrome 有，还带 geometrychange 事件）：
 * 那套要先 `overlaysContent = true`，而那正是「键盘谁都不缩、你自己让开」的模式 ——
 * boundingRect 和事件都只在那个模式下才有值，换过去等于把 --vvh 那条路整个重写。
 *
 * 认不认得出来都不影响正确性：有些浏览器键盘弹出时页面高度纹丝不动（README 里记过），
 * 那儿就退化成「顶栏不收 + 那条补救不生效」，不会出错。
 */
export function useKeyboardUp() {
  const [up, setUp] = useState(false)
  useEffect(() => {
    const vv = window.visualViewport
    if (!vv) return
    let w = 0
    let base = 0
    const f = () => {
      if (window.innerWidth !== w) { w = window.innerWidth; base = 0 } // 转屏：旧基线作废
      base = Math.max(base, vv.height)
      setUp(vv.height < base * 0.8)
    }
    f()
    vv.addEventListener('resize', f)
    return () => vv.removeEventListener('resize', f)
  }, [])
  return up
}
