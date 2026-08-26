/**
 * 折行拼回来那套判据的回归测试（`tuiWrap`，见 paths.ts）。
 *
 * **为什么是这个跑法**：`node --experimental-strip-types` 直接跑这个 .ts 文件，不装
 * 测试框架。前端这边只有这一处需要「拿真机量出来的几何钉住」，为它引一整套 vitest
 * （加依赖、加配置、CI 多装一遍）不值当；而这条判据已经**静默坏过两次**了 ——
 * 先是只认 xterm 的 `isWrapped`（herdr 的 pane 永远是 false），后是把「顶到右边界」
 * 判成「一个尾随空格都不剩」（Ink 自己还让出两列）。两次的表现一模一样：手机上折行
 * 的路径只认出前半截，点开报「找不到」，而屏幕上那条下划线看着完全正常。
 *
 * 跑：`node web/src/term/paths.test.ts`（`make test` 里有）。
 */
import type { Terminal } from '@xterm/xterm'
import { linkAtCell, pathLinkProvider } from './paths.ts'

/**
 * 手搓一个假 buffer。
 *
 * 42 列不是随手挑的：那是用户报「超链接换行分成了两段」那张截图里的 pane 宽度
 * （量法：herdr 的顶栏铺满 0..41 列，消息框里的字最多画到第 39 列 —— 差两格）。
 */
const COLS = 42

/**
 * 宽字符（中文）占两格，第二格没有自己的内容 —— 和真 buffer 一样。
 * 末尾那一段是**全角标点**（`。、《》「」`），它们也占两格 —— 下面 URL 那个案子的关键
 * 就在 `。` 的第二格上：它正好落在中文和 URL 中间。
 */
const WIDE = /[ᄀ-ᅟ⺀-䶿一-鿿豈-﫿︰-﹏＀-｠￠-￦　-〿]/

function mk(lines: string[], cols = COLS): Terminal {
  const grid = lines.map((s) => {
    const cells: { ch: string; w: number }[] = []
    for (const ch of s) {
      const w = WIDE.test(ch) ? 2 : 1
      cells.push({ ch, w })
      if (w === 2) cells.push({ ch: '', w: 0 })
    }
    while (cells.length < cols) cells.push({ ch: ' ', w: 1 })
    return cells
  })
  const active = {
    length: grid.length,
    viewportY: 0,
    getLine: (y: number) => (y < 0 || y >= grid.length ? undefined : {
      isWrapped: false, // herdr 的 pane 就是这样：xterm 压根不知道 TUI 自己折了行
      getCell: (x: number) => {
        const c = grid[y][x]
        return c ? { getChars: () => c.ch, getWidth: () => c.w } : undefined
      },
    }),
  }
  return { cols, buffer: { active } } as unknown as Terminal
}

/** 可见宽度：宽字符占两格。拼分栏那几行的 padding 要用 */
function wid(s: string): number {
  let n = 0
  for (const ch of s) n += WIDE.test(ch) ? 2 : 1
  return n
}
/** `│ 内容 │`：herdr 的 pane 框，内容区固定 `inner` 格 */
function pane(s: string, inner: number): string {
  return `│ ${s}${' '.repeat(Math.max(0, inner - wid(s)))} │`
}

let fails = 0
function links(term: Terminal, row: number): string[] {
  let got: { text: string }[] = []
  pathLinkProvider(term, () => {}).provideLinks(row + 1, (ls) => { got = ls ?? [] })
  return got.map((l) => l.text)
}
function check(name: string, got: unknown, want: unknown) {
  const ok = JSON.stringify(got) === JSON.stringify(want)
  if (!ok) fails++
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${name}`)
  if (!ok) console.log(`     got  ${JSON.stringify(got)}\n     want ${JSON.stringify(want)}`)
}

const JPG = '/Users/bysir/.herdr-web/uploads/20260824-173712-e33266.jpg'

/* 1. 用户报的那一屏：Claude Code 的消息框把路径切成两行，字只画到第 39 列 */
const real = mk([
  '❯ /clear',
  '',
  '❯ /Users/bysir/.herdr-web/uploads/202608',
  '  24-173712-e33266.jpg clear',
  '  超过了按钮宽度',
])
check('折行的路径拼回来（首行）', links(real, 2), [JPG])
check('折行的路径拼回来（续行）', links(real, 3), [JPG])
check('续行上 tap 也认得', linkAtCell(real, 4, 4)?.text, JPG)

/* 2. 正常断句（右边剩一截）不许拼 —— 拼出来的链接看着和好的一模一样 */
check('正常断句不拼', links(mk([
  '  I will now check whether the deploy',
  '  target is reachable from here',
]), 0), [])

/* 3. 结尾已经是个完整文件名：差一两格也不许把下一行的头一个词粘上来 */
check('差一格但结尾是完整文件名：不拼', links(mk([
  '/Users/bysir/dev/bysir/herdr/a/report.txt', // 41 字符 → 只差一格
  'found 3 matches',
]), 0), ['/Users/bysir/dev/bysir/herdr/a/report.txt'])

/* 4. 分栏：**左右两条带各算各的**。左边那条该拼的拼回来，右边那条不能因为左边拼了就
   找不着 —— 原来这里是「一行上有分隔线就整个放弃拼行」（宁可少认一半，也别把另一半
   pane 上那条好好的路径弄成点不动的），按「只读这一条带」之后两边同时成立。 */
check('分栏：两条带各算各的', links(mk([
  '/Users/bysir/.herdr-web/uploads/2026 │ /tmp/a.png',
  '08-24-x.jpg                          │ hi',
], 54), 0), ['/Users/bysir/.herdr-web/uploads/202608-24-x.jpg', '/tmp/a.png'])

/* 5. 竖线框里（`╭…╮` 的问题块、左右分栏的左边那半）：离竖线差两格也算顶到边 */
check('框里的路径拼回来', links(mk([
  '│ /Users/bysir/.herdr-web/uploads/2026  │',
  '│ 0824-173712-e33266.jpg                │',
]), 0), [JPG])

/* 6. 续行是中文：断点不是路径字符，只认前半截 */
check('续行是中文：只认前半截', links(mk([
  '❯ /Users/bysir/.herdr-web/uploads/202608',
  '  中文接着说',
]), 0), ['/Users/bysir/.herdr-web/uploads/202608'])

/* 7. **中文后面紧跟一条 URL，被折成两行。**（真机截图 20260824-200248：手机上点不动）
   断点两侧那个「词」要按**空格**算：wrap-ansi 切词是 `split(' ')`，中文里一个空格都没有，
   所以 `这一批全上线了,预览无报错。https://…` 整条是一个六十多格的词 —— 超过行宽，这才是
   它被 hard 切开的原因。把宽字符的第二格当成空格的话，量出来的词只剩 `https://p54f` 那
   一截，「两截拼起来还不到一行宽」成立，于是判成正常断句、URL 只认出前半截。 */
const SITE = 'https://p54fi1e2ddoy.preview.creght.cn/'
const site = mk([
  '⏺ 这一批全上线了,预览无报错。https://p54f',  // 顶到第 41 列（42 列的 pane，Ink 让出一格）
  '  i1e2ddoy.preview.creght.cn/',
])
check('中文后面那条 URL：首行 tap', linkAtCell(site, 30, 1)?.text, SITE)
check('中文后面那条 URL：断点上 tap', linkAtCell(site, 41, 1)?.text, SITE)
check('中文后面那条 URL：续行 tap', linkAtCell(site, 3, 2)?.text, SITE)

/* 8. 同一条路上的另一个出口：中文后面紧跟的**路径**（走 findPaths，不是那个 URL 正则）。
   两个出口一起钉住 —— 断的是共用的那套折行判据 */
const zh = mk([
  '⏺ 图存好了,在这儿。/Users/bysir/.herdr-w',
  '  eb/uploads/20260824-173712-e33266.jpg',
])
check('中文后面那条路径：拼回来', linkAtCell(zh, 3, 2)?.text, JPG)

/* 9. **同一条 URL，切在域名当中。**（真机截图 20260825-203949：点开只有
   `https://p54fi1e2ddoy.preview.creg`，用户报「不是一个完整的域名」）
   scheme 里那两个斜杠**不是路径分隔符** —— 紧跟在它后面的是主机名，而主机名里的点是
   标签分隔（`.creg` 是被切开的 `.creght`），不是扩展名。而 `FINISHED`（「结尾看着已经是
   个完整文件名就别拼」）只按「最后一个斜杠之后有没有扩展名」判，于是 `//` 被当成路径
   分隔、`.creg` 被当成扩展名 —— 判成「已经完整」，不拼。
   偏偏这一行只差一格顶满（Ink 在自己那一层让出一格），FINISHED 那道正是这时候才生效的。 */
const HASH = 'https://p54fi1e2ddoy.preview.creght.cn/#diff'
const cut = mk([
  '  预览: https://p54fi1e2ddoy.preview.creg', // 字画到第 40 列（42 列的 pane，差一格）
  '  ht.cn/#diff',
])
check('切在域名当中的 URL：首行 tap', linkAtCell(cut, 20, 1)?.text, HASH)
check('切在域名当中的 URL：续行 tap', linkAtCell(cut, 4, 2)?.text, HASH)

/* 10. 反过来那一半还得管住：URL 结尾**真是个文件名**时，差一两格顶满也不许把下一行的头
   一个词粘上来（`/photo.png` + `next` → 点开找不到）。这是第 9 条那个改动的边界。 */
const png = mk([
  '  见 https://ex.com/img/photo.png',
  '  next word here',
], 35)
check('URL 结尾是文件名：不粘下一行', linkAtCell(png, 10, 1)?.text, 'https://ex.com/img/photo.png')

/* 11. **分栏（平板上左右两个 pane）里的那条 URL。**（真机截图 20260825-235355：95×46 的
   终端、左右两个 pane，URL 被切在 `https://p` 上，点开直接跳到 `https://p`）
   「内容带」（这一行被竖直分隔线切成的几段）**要按落点算**，不能拿「整行最右那个非空格」
   去推 —— 那个字在**另一个 pane** 里，量出来的右边界离本带的字十万八千里，「顶到右边界」
   一条永远不成立，于是分栏时折行压根不拼。 */
const INNER = 43
const two = (l: string, r: string) => '    ' + pane(l, INNER) + pane(r, INNER)
const SITE2 = 'https://p54fi1e2ddoy.preview.creght.cn/'
const split2 = mk([
  two('⏺ 改完推了草稿（还是没 publish）：https://p', ''),
  two('  54fi1e2ddoy.preview.creght.cn/', ''),
], 4 + (INNER + 4) * 2)
check('分栏里的 URL：首行 tap', linkAtCell(split2, 45, 1)?.text, SITE2)
check('分栏里的 URL：续行 tap', linkAtCell(split2, 12, 2)?.text, SITE2)

if (fails) {
  console.error(`\n${fails} 处不对`)
  process.exit(1)
}
console.log('\npaths: all ok')
