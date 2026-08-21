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
 *   - **拼回来不能用 translateToString**。有中文时它返回的字符数和格子数对不上，
 *     匹配到的下标映射回 (x, y) 就全错位了。所以逐格读，顺手记下每个字符的坐标。
 *   - **TUI 会用 `…` 截断路径**。截断过的路径是**认不出来的**，别猜一个短的出来
 *     然后报「没有这个文件」—— 直接不给链接，用户还知道去别处找。
 *   - 路径后面常挂着标点（`见 /tmp/a.png。`）、行号（`a.go:42:7`）、引号反引号。
 */

import type { ILink, ILinkProvider, Terminal } from '@xterm/xterm'

/**
 * 路径里**不可能**出现的字符。除了空白和成对符号，**中文标点也在里面** ——
 * 实测踩过：agent 打出来的是「生成好了 /tmp/a.png。相对的 …」，中文和路径之间没有
 * 空格，不把 `。` 当终止符的话整段 `a.png。相对的` 会被当成一个路径：点开报「没有这个
 * 文件」，而屏幕上那条下划线看着完全正常。中文**汉字**照样放行（`/tmp/图表.png` 是
 * 合法文件名），挡的只是标点。
 */
const STOP = '\\s"\'`()\\[\\]{}<>|,;。，、；：！？（）【】《》「」『』“”‘’…　'

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

/**
 * 逻辑行：把连着 `isWrapped` 的那一串拼回一行，**顺带记下每个字符落在哪个格子**。
 *
 * 为什么不用 `translateToString`：有中文（或者 emoji）时它返回的字符数和格子数对不上，
 * 拿匹配下标去算 x 坐标会整条错位 —— 下划线画在半个词上、点下去取到的是另一段。
 * 所以逐格读，xs/ys 和 text 一一对应。
 */
function logical(term: Terminal, row: number) {
  const buf = term.buffer.active
  let top = row
  while (top > 0 && buf.getLine(top)?.isWrapped) top--
  let bottom = row
  while (bottom + 1 < buf.length && buf.getLine(bottom + 1)?.isWrapped) bottom++

  let text = ''
  const xs: number[] = []
  const ys: number[] = []
  for (let y = top; y <= bottom; y++) {
    const ln = buf.getLine(y)
    if (!ln) continue
    for (let x = 0; x < term.cols; x++) {
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
        // 折行的路径跨好几行；只把**盖住当前这一行**的那些交回去
        if (ys[a] > row || ys[b] < row) continue
        links.push({
          text: hit.path,
          range: {
            start: { x: xs[a] + 1, y: ys[a] + 1 },
            end: { x: xs[b] + 1, y: ys[b] + 1 },
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
