/**
 * 「第一下只收键盘，第二下才生效」的兜底：手指抬起之后**没有等到 click 就自己补一个**。
 *
 * 手机上的现象（真机反复踩到，面板一览、设置、文件浏览的关闭 ✕ 都中过）：键盘弹着的时候
 * 点面板里任何一个按钮，那一下只把键盘收掉，按钮没反应，再点一次才动。
 *
 * 为什么会这样。这个项目里几条路都**刻意不让浏览器改焦点** —— 软键条在 mousedown 上
 * `preventDefault`（不然点个 Ctrl 就把发件箱的焦点抢走），终端那层把 `touchstart` 整个
 * 吃掉（不然手指一划变成拖选，见 `term/touch.ts`）。于是面板浮出来的时候，发件箱 / 终端
 * 那个输入框还聚着、键盘还占着半个屏。这时候点一下：焦点先被带走 → 系统键盘收起 →
 * visualViewport 一变，`--vvh` 和顶栏一起重排 → 手指底下那个元素已经不在原位，
 * 浏览器于是把 click 派给了别人，或者干脆不派。touch 事件是**照常来**的，丢的只有 click。
 *
 * 所以这里只做一件事：pointerdown / pointerup 落在同一个可点控件上、手指基本没动，
 * 而 click 在 250ms 内没来 —— 那就补一次 `el.click()`。
 *
 * 几条边界，都是刻意的：
 * - **鼠标不管**（`pointerType === 'mouse'`）：桌面上 click 从不丢，补了只会重复。
 * - **手指走过 10px 就不算点**，`pointercancel`（开始滚了）也不算 —— 滚列表不该点到东西。
 * - **元素已经从文档里没了就不补**：那说明这一下其实生效了（面板关掉、行跳走了）。
 * - **浏览器迟到的那一下要吃掉**：补过之后**同一个手势里**（中间没有新的 pointerdown）
 *   再来一个 click，拦掉 —— 不然「关闭」会关两次，第二次落到底下的终端上，等于往 herdr
 *   里点了一下。判据用「手势序号」而不是时间窗，这样用户自己在 700ms 内点第二下（连点
 *   一个开关）不会被误吞：那一下有它自己的 pointerdown。
 * - 自己补的那一下靠**一个标志**认，不靠 `isTrusted`：合成事件的 isTrusted 也是 false，
 *   拿它当判据的话，别处派出来的合成 click 会被当成「我们补的」而漏记（测试里就踩到了）。
 *
 * 为什么放在 document 上做而不是每个按钮自己处理：这个毛病和按钮是谁无关，凡是「浮层里
 * 的控件 + 键盘正弹着」就会中，一个个去接的话总会漏掉下一个新面板。列表那一行是例外，
 * 它自己在 `pointerup` 上收工（见 PaneSwitcher）—— 那儿不只是「丢了 click」，还可能
 * **派给隔壁那一行**，跳错 pane 比没反应更糟，所以那条路要自己认准元素。
 */

/** 认得出「点了会有事发生」的控件；文本框不在里面（补一下 click 也不会聚焦，没意义） */
const CLICKABLE = 'button, [role="button"], [role="checkbox"], [role="tab"], a[href], label, summary'

/** 手指抬起后等多久还没 click 就认为它丢了。Chrome 正常是紧接着同一拍就派出来 */
const WAIT_MS = 250
/** 补过之后多久之内的真 click 要吃掉（浏览器偶尔会在重排之后迟到） */
const DEDUPE_MS = 700
/** 手指允许的位移，超了当成在滚 */
const SLOP_PX = 10

export function installTapRescue() {
  let down: { el: HTMLElement; x: number; y: number; id: number; seq: number } | null = null
  let lastClickAt = 0
  /** 手势序号：每次 pointerdown +1，用来分清「同一下的迟到 click」和「用户点的第二下」 */
  let seq = 0
  /** 正在派我们自己补的那一下 click */
  let synth = false
  let rescued: { el: HTMLElement; at: number; seq: number } | null = null

  addEventListener('pointerdown', (e) => {
    seq++
    const t = e.target
    down = e.pointerType === 'mouse' || !(t instanceof HTMLElement)
      ? null
      : { el: t, x: e.clientX, y: e.clientY, id: e.pointerId, seq }
  }, true)

  addEventListener('pointercancel', () => { down = null }, true)

  addEventListener('click', (e) => {
    if (synth) return // 自己补的那一下，放过去
    const r = rescued
    if (r && r.seq === seq && performance.now() - r.at < DEDUPE_MS && e.target instanceof Node && r.el.contains(e.target)) {
      // 同一个手势里已经替它点过了，这是迟到的重复
      e.preventDefault()
      e.stopPropagation()
      return
    }
    lastClickAt = performance.now()
  }, true)

  addEventListener('pointerup', (e) => {
    const d = down
    down = null
    if (!d || d.id !== e.pointerId) return
    if (Math.abs(e.clientX - d.x) > SLOP_PX || Math.abs(e.clientY - d.y) > SLOP_PX) return
    const el = d.el.closest?.(CLICKABLE) as HTMLElement | null
    if (!el || el.hasAttribute('disabled') || el.getAttribute('aria-disabled') === 'true') return
    const at = performance.now()
    setTimeout(() => {
      if (lastClickAt >= at) return // 浏览器自己派出来了
      if (!el.isConnected) return // 那一下其实已经生效（元素都没了）
      if (seq !== d.seq) return // 已经开始下一个手势了，这一下过期
      rescued = { el, at: performance.now(), seq: d.seq }
      synth = true
      try {
        el.click()
      } finally {
        synth = false
      }
    }, WAIT_MS)
  }, true)
}
