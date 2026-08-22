// 触屏手势：滑动 = 滚轮上报，单击 = 发给程序但不弹键盘，长按 = 抓住（按下左键不放），
// 之后滑动就是拖。
//
// **没有双击。** 双击原来是「呼出 / 收起系统键盘」的入口，去掉了 —— 代价太大：为了分清
// 「这是单击」还是「这是双击的第一下」，每一次单击都得压着等一个双击窗口（320ms）才敢发
// 出去；而只要不等，第一下就会漏进 pane 里那个程序 —— Claude Code 自己有可点的东西
// （展开一段、**选一个选项**），漏一下就把选项给选了。键盘现在只走软键条上的 ⌨
// （`act:kbd`，出厂配置第一个键就是它）和顶栏上那个「系统键盘」按钮：都是明确的按钮，
// 点了就是要键盘，不用猜，也没有延迟。
// **手指落在 pane 分界线附近时不用长按**，直接就是拖 —— 那条线是 herdr 的边框，
// 在上面滑动本来也没别的含义，抓住就拉正是桌面上鼠标的做法。
//
// 为什么要自己写：xterm.js 只把 wheel 事件翻译成鼠标上报，完全不管 touch。像 herdr
// 这样「占着备用屏幕（本地没 scrollback 可滚）+ 开了鼠标上报」的程序，手机上手指划了
// 等于没划 —— 两头都不响应。而点击和长按又都会落到隐藏 textarea 上，浏览器顺手就把
// 键盘顶出来；在 TUI 里十次里九次只是想点个 pane，不是想打字。
import type { Terminal } from '@xterm/xterm'

interface Hooks {
  send: (d: string) => void
  /**
   * 这一格上有链接就打开它（文件路径 / URL），没有就什么都不做。
   *
   * 为什么要多这一条：xterm 的 linkifier 靠 `mousemove → mousedown → mouseup` 驱动，
   * 而这一层把单指手势全接管了（下面 onStart 里的 preventDefault，不让浏览器补发兼容
   * 鼠标事件，否则一划就变成拖选）—— 于是触屏上**所有**链接都点不动，包括原来那些
   * URL。触屏本来也没有 hover，那条路走不通，只能在 tap 那一刻自己判一次。
   */
  openLinkAt: (col: number, row: number) => void
  /**
   * 这一下点击被网页自己接走了吗（true = 别再发给程序）。
   *
   * 目前只有一处：herdr 移动端顶栏右上角那个 `switch` 按钮 —— 设置里开着的时候点它开的是
   * 我们的「面板一览」（见 `term/mobilebar.ts`）。接走了就**不能**再把鼠标上报发出去，
   * 否则 herdr 自己那张面板会在我们这张底下一起开着，跳完 pane 回来还得再关一次。
   */
  claimTap: (col: number, row: number) => boolean
}

interface TouchState {
  x: number; y: number; x0: number; y0: number
  /**
   * 按下的时刻，取的是**事件自带的时间戳**（`e.timeStamp`）而不是处理函数里的
   * `Date.now()`。
   *
   * 差别很要紧：终端正忙着重绘的时候（herdr 刷屏、连上时那一大坨输出）JS 定时器
   * 和事件派发都会被推后，实测一次 60ms 的点击在处理函数里量出来是 994ms —— 于是
   * 被「超过 500ms 算长按，什么都不做」那条挡掉，表现就是「输出一多点哪儿都没反应」。
   * 事件时间戳是事件**产生**时打的，不受处理延迟影响。
   */
  at: number
  acc: number; owned: boolean
  /** 长按计时器：到点了就「抓住」 */
  hold: ReturnType<typeof setTimeout> | undefined
  /** 抓住之后左键一直按着，这里记上次上报的格子 */
  drag: { col: number; row: number } | null
}

// 长按多久算抓住。再短会和「想滑一下」打架，再长手指容易先动
const HOLD_MS = 380
/**
 * 长按期间允许手指晃多少 px。
 *
 * 原来是 8px，太苛刻 —— 平板上按住不动的手指本来就会飘十几个 px，一飘长按就被
 * 撤了，表现就是「长按根本没反应」。
 */
const HOLD_SLOP = 16
/**
 * 吸附半径按**像素**算，不是按格数。
 *
 * 这是「手机上拖不动 pane 边框」的真正原因：原来只允许差一格，而平板上 211 列宽的
 * 屏幕一格才 ~6px，手指落点偏十几个 px 是常事，press 就落进 pane 里去了（agent 收到
 * 一次拖动，屏幕上什么也没发生）。24px 差不多是一根手指的落点误差。
 */
const SNAP_PX = 24

/**
 * 「点穿」的闸门开着多久之后自己撤掉（毫秒）。
 *
 * 这个数**不是判据**，只是兜底：判据是「一次性」——手指在终端外面抬起就把闸门打开，
 * 补发的那一下鼠标事件用掉就关。给个超时是因为那一下未必会落到终端上（比如浮层没关，
 * 补发的事件落在浮层自己身上），闸门不能一直开着。
 *
 * 故意给得很宽（1.5 秒）：主线程忙起来时这几个事件会被推后很多 —— 实测「关掉面板 + herdr
 * 重绘」那一下，60ms 的等待量出来是 1008ms。原来这儿是「700ms 之内算点穿」的时间窗，就是
 * 因此漏掉的（用户报了两次「切完 pane 键盘还是弹出来」）。
 */
const GHOST_MS = 1500

/** 是不是画框线的字符（U+2500–U+259F：制表符 + 方块元素） */
const isBorderGlyph = (ch: string) => {
  const c = ch.codePointAt(0) ?? 0
  return c >= 0x2500 && c <= 0x259f
}

export function attachTouch(host: HTMLElement, term: Terminal, hooks: Hooks): () => void {
  const screenEl = () => host.querySelector('.xterm-screen') as HTMLElement | null

  const cellBox = () => {
    const el = screenEl()
    if (!el || !term.rows || !term.cols) return null
    const r = el.getBoundingClientRect()
    return { r, w: r.width / term.cols, h: r.height / term.rows }
  }

  // 鼠标上报开着的时候，滚动得发给程序；否则滚本地 scrollback
  const mouseReporting = () => term.modes.mouseTrackingMode !== 'none'

  const cellAt = (x: number, y: number, box: NonNullable<ReturnType<typeof cellBox>>) => ({
    col: Math.min(term.cols, Math.max(1, Math.floor((x - box.r.left) / box.w) + 1)),
    row: Math.min(term.rows, Math.max(1, Math.floor((y - box.r.top) / box.h) + 1)),
  })

  let touch: TouchState | null = null

  const glyphAt = (col: number, row: number) => {
    const buf = term.buffer.active
    return buf.getLine(buf.viewportY + row - 1)?.getCell(col - 1)?.getChars() ?? ''
  }

  /** 这一列 / 这一行里框线字符占了多少格 */
  const runLen = (fixed: number, vertical: boolean) => {
    const n = vertical ? term.rows : term.cols
    let hit = 0
    for (let i = 1; i <= n; i++) {
      if (isBorderGlyph(vertical ? glyphAt(fixed, i) : glyphAt(i, fixed))) hit++
    }
    return hit
  }

  /**
   * 手指附近的贯穿线，长按抓取时用来**吸附**（手指偏十几个 px 也能抓到一格宽的竖线）。
   *
   * 只认**贯穿的长线**（占了 70% 以上、至少 6 格）：agent 自己画的短横线（消息之间的
   * 分隔、「2 new messages」那种）都被挡在外面。挡不住的是 agent 的**外框竖边** ——
   * 它和 herdr 的 pane 边框一样贯穿整屏，从字符层面分不出来；不过既然只是「按下点
   * 挪最多 24px」，猜错了也就是这一次拖动落在框边上，不会有别的后果。
   *
   * 代价：2×2 布局里那条横向分界只占半屏宽，吸附不到，得按准一点。
   */
  const nearestDivider = (col: number, row: number, box: NonNullable<ReturnType<typeof cellBox>>) => {
    const long = (n: number, total: number) => n >= Math.max(6, Math.round(total * 0.7))
    const dc = Math.max(1, Math.round(SNAP_PX / box.w))
    const dr = Math.max(1, Math.round(SNAP_PX / box.h))
    for (let d = 0; d <= Math.max(dc, dr); d++) {
      for (const sign of d === 0 ? [0] : [-1, 1]) {
        const c = col + d * sign
        if (d <= dc && c >= 1 && c <= term.cols && long(runLen(c, true), term.rows)) {
          return { col: c, row }
        }
      }
      for (const sign of d === 0 ? [0] : [-1, 1]) {
        const r = row + d * sign
        if (d <= dr && r >= 1 && r <= term.rows && long(runLen(r, false), term.cols)) {
          return { col, row: r }
        }
      }
    }
    return null
  }

  // SGR 鼠标（1006）编码：0 = 左键按下，32 = 按着左键移动，末尾 m = 松开。
  // herdr 常驻开着 1002/1003/1006，所以这里直接按 SGR 发。
  /** 按下左键不放。手指附近有贯穿线就吸到线上（一格宽的竖线按不准） */
  const grab = () => {
    const box = cellBox()
    if (!touch || !box) return
    const under = cellAt(touch.x, touch.y, box)
    const cell = nearestDivider(under.col, under.row, box) ?? under
    touch.drag = cell
    hooks.send(`\x1b[<0;${cell.col};${cell.row}M`)
    navigator.vibrate?.(12) // 有振动的机器上给一下「抓住了」的反馈
  }

  const release = (st: TouchState) => {
    if (!st.drag) return
    hooks.send(`\x1b[<0;${st.drag.col};${st.drag.row}m`)
    st.drag = null
  }

  const onStart = (e: TouchEvent) => {
    if (e.touches.length !== 1) {
      // 第二根手指落下：拖动到此结束，别把左键留在按下的状态
      if (touch) { clearTimeout(touch.hold); release(touch) }
      touch = null
      return
    }
    const t = e.touches[0]
    touch = {
      x: t.clientX, y: t.clientY, x0: t.clientX, y0: t.clientY,
      acc: 0, at: e.timeStamp, owned: mouseReporting(),
      hold: undefined, drag: null,
    }
    // 单指手势整个由这一层接管，**不分有没有鼠标上报**都吃掉默认行为。
    //
    // 不吃的话浏览器会补发兼容鼠标事件，xterm 当成「按下 + 拖选」—— 手指一滑变成选中
    // 文字、终端一动不动（鼠标上报关着的时候必现：还没 attach herdr，或者 pane 里跑着
    // 不收鼠标的程序）。触屏上想滚屏远比想选字常见，所以这里选滚屏。
    //
    // 两个代价，都是有意的：触屏不再能拖选（要复制用桌面鼠标，或者 herdr 自己的 COPY
    // 模式 —— 前缀进去之后 hjkl 选、y 复制），点一下也不再由浏览器顺手聚焦隐藏
    // textarea，改成自己在 touchend 里 focus（见 onEnd）。
    if (e.cancelable) e.preventDefault()
    if (!touch.owned) return // 普通 shell 里拖一下没有任何意义
    // 抓取**只认长按**。曾经试过「落在框线附近就立刻抓」，翻车了：agent 自己画的框
    // （Claude Code 每个 pane 一个圆角框）竖边同样贯穿整屏，判据分不出来，于是在框边
    // 上一划就变成往 agent 里拖鼠标 —— 手指想滚屏，屏幕上却在选文字。
    // 滑动必须永远是滑动，拖动是要「先按住」才换挡的那种动作。
    touch.hold = setTimeout(grab, HOLD_MS)
  }

  const onMove = (e: TouchEvent) => {
    if (!touch || e.touches.length !== 1) return
    const box = cellBox()
    if (!box) return
    const t = e.touches[0]

    // 抓住之后：这一路都是拖动，不再滚屏
    if (touch.drag) {
      if (e.cancelable) e.preventDefault()
      touch.x = t.clientX
      touch.y = t.clientY
      const at = cellAt(t.clientX, t.clientY, box)
      if (at.col !== touch.drag.col || at.row !== touch.drag.row) {
        touch.drag = at
        hooks.send(`\x1b[<32;${at.col};${at.row}M`)
      }
      return
    }
    // 手指走远了就不算长按了（不撤的话滑到一半会突然被抓住）。
    // 阈值给到 16px：按住不动的手指本来就会飘，太苛刻等于长按永远不成立。
    if (Math.abs(t.clientY - touch.y0) > HOLD_SLOP || Math.abs(t.clientX - touch.x0) > HOLD_SLOP) {
      clearTimeout(touch.hold)
    }

    touch.acc += t.clientY - touch.y
    touch.y = t.clientY

    if (e.cancelable) e.preventDefault() // 页面橡皮筋 / 原生选区都别来
    // 之前手忙脚乱选中的那块（比如用鼠标选过又来滑）先清掉，不然它一直亮着
    if (term.hasSelection()) term.clearSelection()

    const steps = Math.trunc(touch.acc / box.h)
    if (!steps) return
    touch.acc -= steps * box.h

    const dir = steps > 0 ? -1 : 1 // 手指下滑 = 看更早的内容 = 往上滚
    const n = Math.min(Math.abs(steps), 8)
    if (mouseReporting()) {
      const { col, row } = cellAt(touch.x, t.clientY, box)
      const btn = dir < 0 ? 64 : 65 // SGR：64 上滚，65 下滚
      for (let i = 0; i < n; i++) hooks.send(`\x1b[<${btn};${col};${row}M`)
    } else {
      term.scrollLines(dir * n)
    }
  }

  const onEnd = (e?: TouchEvent) => {
    const tc = touch
    touch = null
    if (!tc) return
    clearTimeout(tc.hold)
    // 拖动结束必须发松开：不发的话 herdr 那边左键一直是按着的
    if (tc.drag) { release(tc); return }
    if (Math.abs(tc.y - tc.y0) > 8 || Math.abs(tc.x - tc.x0) > 8) return // 是滑动，已经处理过了

    // 长按什么都不做（重点是别弹键盘）。有鼠标上报的时候长按已经在上面被「抓住」接走了，
    // 走到这儿只剩普通 shell，以及抓取还没到点就松手的情况。
    // 时间戳用事件自带的那个（和 tc.at 一个时间轴），不是处理函数里的 Date.now()。
    if ((e?.timeStamp ?? performance.now()) - tc.at > 500) return

    const box = cellBox()
    if (!box) return
    const { col, row } = cellAt(tc.x, tc.y, box)

    // 下面全部**当场做**。没有双击之后就没有「这一下到底算什么」的悬念，也就不用压着等 ——
    // 那 320ms 的延迟和「第一下漏出去」两件事都是双击带来的（见文件开头那段）。
    //
    // 先问一句这一下是不是被网页接走了（herdr 顶栏那个 switch）。放在链接判断**之前**：
    // 那个按钮上不会有链接，但既然接走了，就该彻底不往下走。
    if (hooks.claimTap(col, row)) return

    // 链接先判，但**不吞掉这一下点击**：桌面上点链接时 xterm 也照样把点击上报给程序
    // （linkifier 和鼠标上报是两条各自独立的监听，互不知情），这儿保持一致 ——
    // 触屏另立一套「点了链接就不算点击」的规矩，只会变成第二种要记的行为。
    hooks.openLinkAt(col, row)

    if (tc.owned) {
      hooks.send(`\x1b[<0;${col};${row}M`)      // 单击照样发给程序，只是不抢焦点
      hooks.send(`\x1b[<0;${col};${row}m`)
    } else {
      term.focus()                              // 普通 shell 里点一下就是想打字
    }
  }

  /**
   * 「点穿」：**关掉浮层的那一下，浏览器会把兼容鼠标事件补发到底下的终端上**，
   * xterm 的 mousedown 处理顺手 focus 它那个隐藏输入框 —— 手机上那就是输入法冒出来。
   *
   * 真机上的样子（用户报的）：键盘本来收着，在「面板一览」里点一行跳过去，面板当场关掉，
   * 键盘自己弹了出来。**那一下不是用户在点终端**，是点击落在了刚消失的面板下面。
   *
   * 判据是**一次性闸门**，不是时间窗：手指在终端外面抬起就把闸门打开，接下来第一串鼠标事件
   * 用掉它 —— 落在终端上就挡掉，落在别处就白用掉。为什么不看时间：主线程忙的时候这几个
   * 事件会被推后到一秒开外（实测 1008ms），按时间窗判必然漏。
   *
   * **开闸认的是 `pointerup`，不能认 `touchend`** —— 这是这个 bug 修了三轮都没修掉的地方：
   * 浮层是在 `pointerup` 的处理里被卸掉的（React 对离散事件是同步 flush 的），而 touch
   * 事件的 target 在 touchstart 那一刻就钉死了，规范说得很清楚 ——「target 被移出文档之后，
   * 事件仍然派给它，于是不再冒泡到 document」。所以那一下的 `touchend` **结构性地到不了
   * document**，闸门永远开不了。`pointerup` 不一样：传播路径在派发开始时就算好了（那时浮层
   * 还在），处理器里把浮层卸掉也不影响它沿既定路径走完，document 的捕获段一定收得到；
   * 触摸的 pointer 事件还有隐式捕获，`pointerup` 的 target 恒等于 `pointerdown` 的 target，
   * 「在不在终端里」的判断和原来一字不差。真鼠标（`pointerType === 'mouse'`）直接放过。
   *
   * 我们自己的手势在 `touchstart` 上就 `preventDefault` 了，所以**落在终端上的触摸浏览器
   * 根本不补发兼容鼠标事件** —— 闸门只可能被终端外面那一下打开。
   *
   * **补发的那一串要整串挡掉**（mousemove / mousedown / mouseup / click），不是只挡
   * mousedown。聚焦是 mousedown 带来的，但 herdr 常驻开着鼠标上报（1002/1003/1006），
   * xterm 会把这些鼠标事件**当成真的点击上报给 herdr** —— 于是那一下幻影点击落在哪个 pane
   * 上，herdr 的焦点就跳到哪儿。表现是用户报的这一条：「弹窗说切换成功，其实面板还在老的
   * 那边」—— 跳过去的调用和幻影点击在抢，点击后到就把焦点又拽回了手指底下那个 pane。
   */
  let ghostOpen = false
  let ghostTimer: ReturnType<typeof setTimeout> | undefined
  const arm = (target: Node | null) => {
    ghostOpen = !!target && !host.contains(target)
    clearTimeout(ghostTimer)
    if (ghostOpen) ghostTimer = setTimeout(() => { ghostOpen = false }, GHOST_MS)
  }
  const onDocPointerUp = (e: PointerEvent) => {
    if (e.pointerType === 'mouse') return
    arm(e.target as Node)
  }
  // touchend 那条留着当双保险：判定和 pointerup 永远同向，而它在**浮层没被卸掉**的情况下
  // （比如点了浮层上一个不关闭它的按钮）照样能到 document
  const onDocTouchEnd = (e: TouchEvent) => arm(e.target as Node)
  const onDocMouse = (e: MouseEvent) => {
    const inside = host.contains(e.target as Node)
    /**
     * 一条不依赖闸门状态的判据（Chrome / Safari 有，别的浏览器是 undefined，那就退回闸门）：
     * `sourceCapabilities.firesTouchEvents` 为真 = 这个鼠标事件是**触摸派生出来的**。
     *
     * 落在终端上的真触摸永远在 `touchstart` 那一下就被 `preventDefault` 掉了，浏览器不会
     * 为它补发兼容鼠标事件 —— 所以「终端里出现一个触摸派生的鼠标事件」本身就等于点穿，
     * 不用问闸门开没开。闸门留着兜住没有这个字段的浏览器。
     */
    if (inside && (e as MouseEvent & { sourceCapabilities?: { firesTouchEvents?: boolean } })
      .sourceCapabilities?.firesTouchEvents) {
      e.preventDefault()
      e.stopPropagation()
      return
    }
    if (!ghostOpen) return
    // click 是这一串的最后一个；落在终端外面说明这一串跟终端无关 —— 两种情况都算用掉了
    if (e.type === 'click' || !inside) {
      ghostOpen = false
      clearTimeout(ghostTimer)
    }
    if (!inside) return
    e.preventDefault()
    e.stopPropagation()
  }
  const ghostTypes = ['mousemove', 'mousedown', 'mouseup', 'click'] as const

  host.addEventListener('touchstart', onStart, { passive: false })
  host.addEventListener('touchmove', onMove, { passive: false })
  host.addEventListener('touchend', onEnd, { passive: true })
  host.addEventListener('touchcancel', onEnd, { passive: true })
  // 这几条挂在 document 上：点穿是从**别的元素**上那一下来的，挂在终端上就已经晚了；
  // 而鼠标事件要在 xterm 自己那个监听之前跑到（它的 mousedown 监听是「无条件 focus」，
  // 见包注释里那段），所以走文档级的捕获阶段。
  document.addEventListener('pointerup', onDocPointerUp, { capture: true, passive: true })
  document.addEventListener('touchend', onDocTouchEnd, { capture: true, passive: true })
  for (const t of ghostTypes) document.addEventListener(t, onDocMouse, true)
  return () => {
    clearTimeout(ghostTimer)
    host.removeEventListener('touchstart', onStart)
    host.removeEventListener('touchmove', onMove)
    host.removeEventListener('touchend', onEnd)
    host.removeEventListener('touchcancel', onEnd)
    document.removeEventListener('pointerup', onDocPointerUp, true)
    document.removeEventListener('touchend', onDocTouchEnd, true)
    for (const t of ghostTypes) document.removeEventListener(t, onDocMouse, true)
  }
}
