import { useEffect, useState } from 'react'

/**
 * 系统键盘**大概**弹起来了 —— 按 visualViewport 被压掉多少判断。
 *
 * 为什么不用「xterm 那个隐藏 textarea 聚焦了没有」（Session 报的 kbdUp）：那只覆盖
 * 「对着终端打字」。这个项目最常见的姿势是**在发件箱里口述**，那时候焦点在发件箱的
 * textarea 上，终端那边一无所知，而键盘照样占掉半个屏幕。
 *
 * 阈值 0.8：键盘一般吃掉 35%~50% 的高度，而地址栏 / 工具条收起来那种变化不到 15% ——
 * 中间这段空得足够宽，不至于把「滚一下页面地址栏消失」误判成键盘。
 *
 * 认不认得出来都不影响正确性：这个信号只用来决定顶栏收不收（省几十像素）。有些浏览器
 * 键盘弹出时页面高度纹丝不动（README 里记过），那儿就退化成「顶栏不收」，不会出错。
 */
export function useKeyboardUp() {
  const [up, setUp] = useState(false)
  useEffect(() => {
    const vv = window.visualViewport
    if (!vv) return
    const f = () => setUp(vv.height < window.innerHeight * 0.8)
    f()
    vv.addEventListener('resize', f)
    return () => vv.removeEventListener('resize', f)
  }, [])
  return up
}
