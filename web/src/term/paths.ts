/**
 * 把终端里打出来的**文件路径**变成可点的链接。
 *
 * 这是「打开 agent 生成的那张图」的主入口，而且它天生不关心「图在不在当前 workspace
 * 下」—— 路径是 agent 自己打出来的绝对路径，直接照着开就行。文件浏览面板只是兜底。
 *
 * 几个实测出来的坑，都在下面对应的地方标了：
 *
 *   - **路径会被折行断开**。80 列的 pane 里一条长路径就是跨两行的，按「一行一条正则」
 *     去找只能找到半截。所以这里先把**逻辑行**（连着 isWrapped 的那一串）拼回来。
 *   - **herdr 里那层折行 xterm 压根不知道**。pane 是绝对定位重画的，外层 xterm 收到的
 *     每一行都是独立一行，`isWrapped` 永远是 false —— 只认 isWrapped 的话，窄屏上被
 *     agent 自己折成两行的长路径还是只找得到前半截。所以另有一套判据，见 `tuiWrap()`。
 *   - **拼回来不能用 translateToString**。有中文时它返回的字符数和格子数对不上，
 *     匹配到的下标映射回 (x, y) 就全错位了。所以逐格读，顺手记下每个字符的坐标。
 *   - **TUI 会用 `…` 截断路径**。截断过的路径是**认不出来的**，别猜一个短的出来
 *     然后报「没有这个文件」—— 直接不给链接，用户还知道去别处找。
 *   - 路径后面常挂着标点（`见 /tmp/a.png。`）、行号（`a.go:42:7`）、引号反引号。
 */

import type { ILink, ILinkProvider, Terminal } from '@xterm/xterm'

/**
 * 路径里**不可能**出现的字符。除了空白和成对符号，**中文标点和框线也在里面**。
 *
 * 中文标点是实测踩到的：agent 打出来的是「生成好了 /tmp/a.png。相对的 …」，中文和路径
 * 之间没有空格，不把 `。` 当终止符的话整段 `a.png。相对的` 会被当成一个路径：点开报
 * 「没有这个文件」，而屏幕上那条下划线看着完全正常。中文**汉字**照样放行
 * （`/tmp/图表.png` 是合法文件名），挡的只是标点。
 *
 * 框线是 herdr 分栏时踩到的：路径的最后一个字紧挨着 pane 的竖线，`│` 不当终止符的话
 * 会被吞进去变成 `/tmp/a.pn│`，那条下划线一直画到分隔线上。
 */
const STOP = '\\s"\'`()\\[\\]{}<>|,;。，、；：！？（）【】《》「」『』“”‘’…　│┃║─━═╭╮╰╯┌┐└┘├┤┬┴┼'

/**
 * 候选路径。三种：
 *
 *   1. `/…`、`~/…`、`./…`、`../…` —— 有根的，最常见
 *   2. `docs/plan.md` —— 光秃秃的相对路径，**必须带扩展名**才认（不然 `and/or`、
 *      `w5/p3`、`2026/08/21` 这种全成了链接）
 *   3. `file:///…` —— 有些工具会打这个，剥掉前缀就是 1
 *
 * 边界字符**包在匹配里**（第一组）而不是用后顾断言：Safari 16.4 之前不支持 lookbehind，
 * 而正则是在解析期编译的 —— 不支持的话整个 chunk 直接 SyntaxError，那不是「这个功能
 * 没了」而是「页面白屏」。
 *
 * 也不吃跟在 `:` 或 `/` 后面的东西，所以 `https://x/a.png` 里的 `//x/a.png` 不会被
 * 重复画一遍（那条归 WebLinksAddon 管）。
 */
const PATH_RE = new RegExp(
  `(^|[^\\w:/~.@+-])(` +
    `file:///[^${STOP}]*` +                       // file:///…
    `|(?:~|\\.{1,2})?/[^${STOP}]*` +               // /… ~/… ./… ../…
    `|[\\w@][\\w.@+-]*(?:/[\\w.@+-]+)+` +          // docs/plan.md（光秃秃的相对路径）
  `)`,
  'g',
)

/** 结尾常挂着的标点。中英文都要，agent 输出里中文句号很常见。`/` 留着（目录） */
const TAIL = /[.,;:!?)\]}'">»。，、；：！？）】》]+$/

/** 编译器 / 栈回溯风格的行号：`main.go:42` `App.tsx:42:7` */
const LINENO = /:\d+(?::\d+)?$/

export interface PathHit {
  /** 清理干净的路径（可能是相对的，要拿 pane 的 cwd 去解） */
  path: string
  /** 在逻辑行里的字符下标区间，[start, end) */
  start: number
  end: number
}

/**
 * 从一行文本里找出所有像路径的东西。
 *
 * 判据比正则更严一层：**至少两个 `/`，或者最后一段里有 `.`**。
 * 少了这一条，`and /or`、`读/写` 这种全都会画上下划线 —— 一屏噪音，而点下去只会
 * 得到「没有这个文件」。
 */
export function findPaths(line: string): PathHit[] {
  const out: PathHit[] = []
  PATH_RE.lastIndex = 0
  for (let m = PATH_RE.exec(line); m; m = PATH_RE.exec(line)) {
    const raw = m[2]
    const start = m.index + m[1].length

    // 截断过的路径认不出来就是认不出来 —— 别猜一个短的出来然后报「没有这个文件」。
    // `…` 可能挂在后面（`/very/long/pa…`），也可能被 TUI 塞在前面（`…/long/path`，
    // 那时候它正好落在边界组里）。
    if (raw.includes('…') || line[start + raw.length] === '…' || m[1] === '…') continue

    // 尾部标点和行号在**原文上**剥，range 的下标才算得准
    let text = raw.replace(TAIL, '')
    if (text.length > 1) text = text.replace(LINENO, '')
    const end = start + text.length

    let path = text
    if (path.startsWith('file://')) {
      try {
        path = decodeURIComponent(path.slice('file://'.length))
      } catch {
        continue // 坏的 %xx，那不是个能用的路径
      }
    }
    if (!path || path === '/' || path === '~') continue

    // 比正则再严一层，而且**有根和没根两套判据**：
    //
    //   有根（`/…` `~/…` `./…`）：至少两段，或者最后一段有扩展名。
    //     挡掉「and /or」里那个 `/or`。`/usr/local/bin` 这种没扩展名的目录照样认。
    //   没根（`docs/plan.md`）：**必须有扩展名**，只数斜杠不够。
    //     实测踩到的：`2026/08/21` 有两个斜杠，按「两段就算」会被画成链接。日期、
    //     `100/200`、`读/写` 都是这么混进来的。
    const rooted = /^(?:file:\/\/|~?\/|\.{1,2}\/)/.test(text)
    const hasExt = path.slice(path.lastIndexOf('/') + 1).includes('.')
    const slashes = (path.match(/\//g) ?? []).length
    if (rooted ? slashes < 2 && !hasExt : !hasExt) continue

    out.push({ path, start, end })
  }
  return out
}

/** 竖线：herdr 的 pane 分隔、TUI 自己画的框。用来定「内容区右边界在哪儿」 */
const FRAME = /[│┃║╎╏┆┇┊┋]/

/** 折断点两侧都得是路径能用的字符，不然那是正常断句，不是被切开的 token */
const PATHCH = /[\w~@+%:./-]/

/**
 * 「顶到右边界」容几格空白。**不是 0** —— TUI 在自己那一层还会让出几列。
 *
 * 实测（手机上 42 列的 pane，Claude Code v2.1.x）：herdr 的顶栏铺满 0..41 列，而
 * 消息框的底色只画到第 40 列、框里的字最多画到第 39 列 —— 也就是**离右边界差两格**。
 * 按「一个尾随空格都不剩」判的话，窄屏上每一条折行的路径都只认得出前半截（用户报的：
 * 「超链接换行分成了两段」）。
 */
const EDGE_SLACK = 2

/**
 * 结尾那个词**看着已经是个完整文件名**了：`…/a.png`、`…/main.go`。
 *
 * 判据是「最后一段里有个不在开头的点，点后面 1–5 位字母数字」—— 开头那个点不算，
 * `.herdr-web` / `.bin` 是目录名不是扩展名（而路径正好被切在那儿是常事）。
 */
const FINISHED = /\/[^/]*[^/.]\.[A-Za-z0-9]{1,5}$/

/** 一格的内容。空格子和宽字符的第二格都是空串 */
function chAt(term: Terminal, y: number, x: number): string {
  return term.buffer.active.getLine(y)?.getCell(x)?.getChars() ?? ''
}

/**
 * 从 `edge` 往左找最后一个有内容的格子：返回它**结束在哪一列**（宽字符占两格，所以
 * 是起始列 + 宽度）和那个字符本身。整段空白返回 -1。
 */
function lastEnd(term: Terminal, y: number, edge: number) {
  const ln = term.buffer.active.getLine(y)
  for (let x = edge - 1; x >= 0; x--) {
    const cell = ln?.getCell(x)
    if (!cell) continue
    const w = cell.getWidth()
    if (w === 0) continue // 宽字符的第二格，内容在左边那格上
    const ch = cell.getChars()
    if (!ch || ch === ' ') continue
    return { end: x + w, ch }
  }
  return { end: -1, ch: '' }
}

/** 这一格算不算空。宽字符的第二格也是空串，当空看 —— 路径里没有宽字符，宁可算短 */
function blankAt(ch: string): boolean {
  return !ch || ch === ' '
}

/**
 * 这一格是**真的空格**，不是宽字符的第二格。
 *
 * 和 `blankAt` 的区别就在宽字符那一格上，而这个区别是**必须的**：下面量「被切开的那个
 * 词有多宽」时，词的边界只能是空格 —— wrap-ansi 切词就是 `split(' ')`，而**中文里一个
 * 空格都没有**，所以 `这一批全上线了,预览无报错。https://…` 整条是**一个词**（六十多格，
 * 超过行宽，这才是它被 `hard` 切开的原因）。
 *
 * 把宽字符的第二格当成空格的话，往左走会停在中文和 URL 的接缝上，量出来的词只剩
 * `https://p54f` 那一小截，于是「两截拼起来还不到一行宽」成立、判成正常断句 ——
 * 表现是**中文后面紧跟的那条 URL 永远只认出前半截**（用户报的），而屏幕上它看着完全正常。
 */
function spaceAt(term: Terminal, y: number, x: number): boolean {
  const cell = term.buffer.active.getLine(y)?.getCell(x)
  if (!cell) return true
  if (cell.getWidth() === 0) return false // 宽字符的第二格：是那个字的一部分，不是空格
  return blankAt(cell.getChars())
}

/** [from, to) 这一段是不是全空 */
function blank(term: Terminal, y: number, from: number, to: number): boolean {
  for (let x = from; x < to; x++) {
    if (!blankAt(chAt(term, y, x))) return false
  }
  return true
}

/**
 * 「这一行是被 TUI 自己折断的」—— 是的话给出**这一行读到哪一列为止**（`to`）和下一行
 * 接着念的起始列（`next`）。
 *
 * **为什么要有这条**：herdr 的 pane 是绝对定位重画的，外层 xterm 收到的每一行都是
 * 独立一行，`isWrapped` 永远是 false。于是窄屏上一条 58 字符的路径被 agent 折成两行
 * 之后，这边只认出前半截，点下去报「没有这个文件」—— 屏幕上那条下划线还看着挺正常。
 *
 * 判据四条，缺哪条都会**静默**出错（拼错的链接看着和好的一模一样）：
 *
 *   1. **顶到右边界（容 `EDGE_SLACK` 格）。** Claude Code（Ink）折行走的是 `wrap-ansi`
 *      的 `hard: true`（bundle 里 `if(K==="wrap")return _q6(A,q,{trim:!1,hard:!0})`），
 *      路径这种断不了词的长 token 会正好切在**它自己那一层的列宽**上；正常断句断在
 *      空格上，右边一定剩一截。差别在于「它自己那一层」比 pane 窄几列（根 Box 让一列、
 *      框里还有 padding），所以判据不能是「一格都不剩」，见 `EDGE_SLACK`。
 *      框线（`│ 内容 │`）里内容和竖线之间另有一格 padding，所以有竖线时再容一格。
 *   2. **这两行的内容全落在同一条带子里。** 分栏时另一半 pane 的内容在带子外面。
 *   3. **被切开的那个词真的一行装不下。** 光靠第 1 条会把「英文单词正好压线」当成折断，
 *      实测在真 pane 上就有 3 处 —— 见下面那段。
 *   4. **结尾那个词不能看着已经是个完整文件名。** 这条是第 1 条容了两格之后才需要的：
 *      「一条正好差一两格填满这行的路径」和「被切开的路径」几何上分不开了，而这时第 3 条
 *      压根不起作用（结尾那个词自己就顶满一行）。见 `FINISHED` 那儿。
 *
 * 中文不用单独防：它占两格，落在边界上会剩 1–2 格，断点也不是路径字符，前两条自然挡掉。
 */
function tuiWrap(term: Terminal, y: number): { to: number; next: number } | null {
  const buf = term.buffer.active
  if (!buf.getLine(y) || !buf.getLine(y + 1)) return null

  // 内容区右边界：整行最右，或者最右那条竖线所在的列
  let edge = term.cols
  let tail = lastEnd(term, y, edge)
  if (tail.end < 0) return null
  if (FRAME.test(tail.ch)) {
    edge = tail.end - 1
    tail = lastEnd(term, y, edge)
    if (tail.end < 0) return null
  }
  // 顶到右边界。**容 `EDGE_SLACK` 格**（TUI 自己那一层比 pane 窄几列），有竖线的话
  // 再容一格（框线和内容之间那个 padding）。`strict` 是原来那条严判据，第 4 条要用它
  // 分辨「这一下是不是靠 slack 才过的」
  const strict = edge - (edge === term.cols ? 0 : 1)
  if (tail.end < strict - EDGE_SLACK) return null
  if (!PATHCH.test(tail.ch)) return null
  // 右边那条竖线在下一行也得在，不然这两行不是同一个内容带
  if (edge < term.cols && !FRAME.test(chAt(term, y + 1, edge))) return null

  // 左边界：往左找最近的一条竖线。**要求同一列在下一行也是竖线** —— 竖着连成一条的
  // 才是分隔，内容里偶然出现的一个 `│` 不算
  let lb = 0
  for (let x = edge - 1; x >= 0; x--) {
    if (FRAME.test(chAt(term, y, x)) && FRAME.test(chAt(term, y + 1, x))) {
      lb = x + 1
      break
    }
  }

  // **只在「这两行的内容全落在这条带子里」时才拼。** 分栏时另一半 pane 的内容在带子
  // 外面，一拼就把它整段丢掉了 —— 那一行上别人的路径当场点不动。宁可这一条不拼（只是
  // 少认一半，和现在一样），也不能把没坏的那半弄坏
  if (lb > 0 && (!blank(term, y, 0, lb - 1) || !blank(term, y + 1, 0, lb - 1))) return null
  if (edge < term.cols && (!blank(term, y, edge + 1, term.cols) || !blank(term, y + 1, edge + 1, term.cols)))
    return null

  // 下一行从内容区左边接着念（跳过缩进 —— `⏺` 块的续行缩两格、`⎿` 缩五格）
  let next = -1
  for (let x = lb; x < edge; x++) {
    const ch = chAt(term, y + 1, x)
    if (blankAt(ch)) continue
    if (!PATHCH.test(ch)) return null
    next = x
    break
  }
  if (next < 0) return null

  // **最后一道，也是最要紧的一道：被切开的那个词，得真的长到一行装不下。**
  //
  // wrap-ansi 只在「一个词比整行还长」的时候才切开它，装得下就整个挪到下一行去。所以
  // 把两截拼回来还不到一行宽的话，这压根不是被切开的词，而是**正常断句刚好停在边界上**。
  // 这条不是推的：拿这台机器上 14 个能定宽的真 pane 扫了一遍，「顶满 + 两侧都是路径
  // 字符」命中 3 处，**全是**英文单词压线（`…whether` / `…deploy to` / `…that`），
  // 加上这一条之后 3 处全挡掉。少了它就会把下一行的头一个词粘到路径屁股上 —— 而那种
  // 链接看着完全正常，点下去才报「没有这个文件」。
  //
  // 宽度按「这一行实际填到哪儿」算：顶满时就是 `tail.end - lb`，框线里那种末尾让了一格
  // padding，左边也对称地让一格，所以再减一。
  //
  // 「词」的边界**只能是空格**，见 `spaceAt` —— 中文紧跟着的那条链接全指着它。代价是
  // 「中文和 ASCII 粘在一起、又正好停在边界上」和真被切开的分不开了（几何上本来就分不
  // 开，见上面 slack 那条）：那种会多拼一次，但它要一个巧合，比「每条中文后面的链接都
  // 断掉」少见得多。
  const width = tail.end - lb - (tail.end < edge ? 1 : 0)
  let ws = tail.end
  while (ws > lb && !spaceAt(term, y, ws - 1)) ws--
  let we = next
  while (we < edge && !spaceAt(term, y + 1, we)) we++
  if (tail.end - ws + (we - next) < width) return null

  // **第四道：靠 slack 才算「顶到边界」的那些，结尾那个词不能看着已经是个完整文件名。**
  //
  // 上面那道（切开的词得装不下）在这儿是空的：结尾那个词自己就顶满一行，它一个人就够长。
  // 于是「一条正好差一两格填满这行的路径」会把下一行的头一个词粘上来 —— 原来那条好好的
  // 链接变成点开报「找不到」的。真被切开的路径**极少**正好切在扩展名后面，而切在那儿
  // 的话本来也不用拼（路径已经完整，下一行是别的内容）。
  if (tail.end < strict) {
    let word = ''
    for (let x = ws; x < tail.end; x++) word += chAt(term, y, x)
    if (FINISHED.test(word)) return null
  }

  // 这一行读到**最后一个字符为止**，不是读到 edge：框线里内容和竖线之间还有一格
  // padding，把它读进来就等于在路径中间插了个空格，白拼
  return { to: tail.end, next }
}

/**
 * 逻辑行：把折在一起的那几行拼回一行，**顺带记下每个字符落在哪个格子**。
 *
 * 两种折行都要认：xterm 自己折的（`isWrapped`，普通 shell 里的长行）和 TUI 自己折的
 * （`tuiWrap`，herdr 的 pane / agent 的输出）。后者只把**内容区**那一段拼进来，竖线和
 * 续行缩进要跳掉，不然拼出来的路径中间会多一截框线。
 *
 * 为什么不用 `translateToString`：有中文（或者 emoji）时它返回的字符数和格子数对不上，
 * 拿匹配下标去算 x 坐标会整条错位 —— 下划线画在半个词上、点下去取到的是另一段。
 * 所以逐格读，xs/ys 和 text 一一对应。
 */
function logical(term: Terminal, row: number) {
  const buf = term.buffer.active
  // 往上找这条逻辑行的头。判据要和下面往下走的那套**一模一样**，否则同一条路径在
  // 两行上会算出两个不同的区间
  let top = row
  while (top > 0 && (buf.getLine(top)?.isWrapped || tuiWrap(term, top - 1))) top--

  let text = ''
  const xs: number[] = []
  const ys: number[] = []
  let from = 0
  for (let y = top; y < buf.length; y++) {
    const ln = buf.getLine(y)
    const wrapped = buf.getLine(y + 1)?.isWrapped
    const tui = wrapped ? null : tuiWrap(term, y)
    const to = tui ? tui.to : term.cols
    for (let x = from; ln && x < to; x++) {
      const cell = ln.getCell(x)
      // 宽字符占两格，第二格没有自己的内容（getWidth() === 0）—— 跳过，
      // 否则每个中文都会往 text 里多塞一个空格
      if (!cell || cell.getWidth() === 0) continue
      const chars = cell.getChars() || ' '
      for (let i = 0; i < chars.length; i++) {
        xs.push(x)
        ys.push(y)
      }
      text += chars
    }
    if (wrapped) from = 0
    else if (tui) from = tui.next
    else break
  }
  return { text, xs, ys }
}

/**
 * 给 xterm 用的链接提供者：终端里的文件路径可点。
 *
 * 和 WebLinksAddon 井水不犯河水 —— 那边只认 http/https，而这边的正则不吃跟在 `:` 或
 * `/` 后面的东西，所以 `https://x/a.png` 里的 `//x/a.png` 不会被重复画一遍。
 */
export function pathLinkProvider(term: Terminal, onOpen: (p: string) => void): ILinkProvider {
  return {
    provideLinks(bufferLineNumber, cb) {
      const row = bufferLineNumber - 1 // xterm 给的是 1-based
      const { text, xs, ys } = logical(term, row)
      if (!text) return cb(undefined)

      const links: ILink[] = []
      for (const hit of findPaths(text)) {
        const a = hit.start
        const b = Math.min(hit.end, xs.length) - 1
        if (b < a) continue
        // 折行的路径跨好几行：**只交回落在这一行的那一截**。整段交回去的话 xterm 会把
        // 中间的续行缩进和竖线一起划上下划线（它按「首行从 x 到行尾、末行从行首到 x」
        // 铺），而这一行的另一截等悬到那儿时自己会再问一次
        let s = -1
        let e = -1
        for (let i = a; i <= b; i++) {
          if (ys[i] !== row) continue
          if (s < 0) s = i
          e = i
        }
        if (s < 0) continue
        links.push({
          text: hit.path,
          range: {
            start: { x: xs[s] + 1, y: row + 1 },
            end: { x: xs[e] + 1, y: row + 1 },
          },
          decorations: { pointerCursor: true, underline: true },
          activate: (e) => {
            e.preventDefault()
            onOpen(hit.path)
          },
        })
      }
      cb(links.length ? links : undefined)
    },
  }
}

/**
 * URL 的粗匹配，**只给触屏的 tap 命中判断用**。
 *
 * 桌面上 URL 归 WebLinksAddon 管（hover 出下划线、点击打开），这儿不碰它。但触屏
 * 上那条路整个走不通：xterm 的 linkifier 是 `mousemove → mousedown → mouseup` 驱动的，
 * 而 touch.ts 把单指手势全接管了（`preventDefault`，不让浏览器补发兼容鼠标事件，
 * 否则一划就变成拖选）。**触屏本来也没有 hover**，所以「点哪儿算链接」只能在 tap
 * 那一刻自己判一次。多这一个正则是这个取舍的代价。
 */
const URL_RE = new RegExp(`\\b(?:https?|mailto):[^${STOP}]+`, 'g')

/** 一次 tap 落在哪个链接上。col / row 是 **1-based 的视口坐标**（touch.ts 给的那套）。 */
export function linkAtCell(
  term: Terminal,
  col: number,
  row: number,
): { kind: 'path' | 'url'; text: string } | null {
  const bufRow = term.buffer.active.viewportY + row - 1
  const { text, xs, ys } = logical(term, bufRow)
  if (!text) return null

  // 手指落在哪个字符上：xs/ys 和 text 一一对应（见 logical，中文占两格也对得上）
  let at = -1
  for (let i = 0; i < xs.length; i++) {
    if (ys[i] === bufRow && xs[i] === col - 1) { at = i; break }
  }
  if (at < 0) return null

  for (const hit of findPaths(text)) {
    if (at >= hit.start && at < hit.end) return { kind: 'path', text: hit.path }
  }
  URL_RE.lastIndex = 0
  for (let m = URL_RE.exec(text); m; m = URL_RE.exec(text)) {
    const u = m[0].replace(TAIL, '')
    if (at >= m.index && at < m.index + u.length) return { kind: 'url', text: u }
  }
  return null
}
