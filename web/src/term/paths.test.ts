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

/* 12. **切成三段的 `file:///` 路径**（真机抓的那一屏：codex 打出自己生成的图，43 列的
   手机 pane，见用户报的「这些地址点不开」那张截图）。三条以前没有用例盯着：
   ① 折三行（原来所有用例都只折两行 —— 而 `logical` 是一路往下拼的，断在哪一段上
   完全看不出来）；② `file:///` 前缀（点开要的是剥掉 scheme 之后那个真路径）；
   ③ **首行**也要点得中 —— 老用例一律 tap 在续行上，而人眼看到路径开头就在第一行，
   手指自然点那儿。 */
const CODEX = '/Users/bysir/.codex/generated_images/01a041b8-273a-7361-ae5b-a329fdf4886d/exec-e43ae3c9-3575-4096-a5d5-a27873a291aa.png'
const codex = mk([
  '  file:///Users/bysir/.codex/generated_imag',
  'es/01a041b8-273a-7361-ae5b-a329fdf4886d/exe',
  'c-e43ae3c9-3575-4096-a5d5-a27873a291aa.png',
], 43)
check('三行的 file:// 路径：三行都给同一条链接',
  [links(codex, 0), links(codex, 1), links(codex, 2)], [[CODEX], [CODEX], [CODEX]])
check('三行的 file:// 路径：首行 tap', linkAtCell(codex, 10, 1)?.text, CODEX)
check('三行的 file:// 路径：中间那行 tap', linkAtCell(codex, 10, 2)?.text, CODEX)
check('三行的 file:// 路径：末行 tap', linkAtCell(codex, 10, 3)?.text, CODEX)

/* 13. 同一屏上那两条**缩进续行**的路径（codex 列出它写了哪几个文件）：续行缩 5 格，
   而缩进那几格要跳掉（`tuiWrap` 的 `next`），不然拼出来的路径中间多一截空格。 */
const LIST = '/Users/bysir/dev/bysir/game/recursion-game/art-assets/storybook-experiments/detective-man.png'
const list = mk([
  '• 1. /Users/bysir/dev/bysir/game/recursion-',
  '     game/art-assets/storybook-experiments/',
  '     detective-man.png',
], 43)
check('缩进续行的路径：首行 tap', linkAtCell(list, 10, 1)?.text, LIST)
check('缩进续行的路径：末行 tap', linkAtCell(list, 10, 3)?.text, LIST)

/* 14. **用户报的那一条（截图 20260830-1013 / 电脑上）：codex 的列表项。**
   `• /Users/…/recursion-game/art-` 这一行离**本带右边界差四格** —— codex（ratatui）自己
   那层留了滚动条的一列，列表块又有右留白。原来 `EDGE_SLACK` 只容两格，于是折行没认出来，
   而续行 `assets/chapter-7/pianist-nochair.png` **自己就是一条合法的相对路径**（带扩展名），
   按焦点 pane 的 cwd 一解就成了 `…/recursion-game/assets/…` —— 屏幕上那条下划线看着完全
   正常，点下去报「找不到」。所以这条用例两头都要钉：拼出来的必须是全的，**而且续行不许
   单独变成那条相对路径**。 */
const ART = '/Users/bysir/dev/bysir/game/recursion-game/art-assets/chapter-7/pianist-nochair.png'
const CW = 53 // 内容宽 53，那一行 49 格 —— 差四格
const codexList = mk([
  '│' + '─ Worked ─────'.padEnd(CW) + '│',
  '│' + '• /Users/bysir/dev/bysir/game/recursion-game/art-'.padEnd(CW) + '│',
  '│' + '  assets/chapter-7/pianist-nochair.png'.padEnd(CW) + '│',
  '│' + '─ Worked for 1m 09s ─────'.padEnd(CW) + '│',
], CW + 2)
check('差四格的折行（codex 列表项）：拼回来', links(codexList, 2), [ART])
check('差四格的折行：续行 tap 也是全的', linkAtCell(codexList, 6, 3)?.text, ART)

/* 15. 放宽到四格之后，「正常断句」那道还得站得住：一行在**差四格**的地方断在空格上，
   下一行接着念 —— 这不是被切开的词，不许拼（第 3 条：两截加起来还不到一行宽）。 */
check('差四格的正常断句：还是不拼', links(mk([
  '  I will now check whether the deploy',   // 37 格，band 41 → 差四格
  '  target is reachable from here',
], 41), 0), [])

/* 16. **用户报的那一条（截图 20260902-2058）：URL 被切在扩展名当中。**
   `…__contact_qr.pn` + `g` —— 点开少最后一个 `g`，而屏幕上那条链接看着完全正常。
   卡住的是第 4 道（`FINISHED`：结尾看着已经是个完整文件名就别拼）：`.pn` 一样满足
   「点后面 1–5 位字母数字」，于是一截**被切开的扩展名**被判成「已经完整」。
   分得开这两种的证据在**上一行**：它在**同一列**上也是断的（末字符还是路径字符），
   那这一列就是这个 TUI 的硬折宽度 ——「凑巧差几格」这个前提不成立，照拼。 */
const QR = 'https://fsu.creght.com/site/2087143196390854656/1786454035627__contact_qr.png'
const qr = mk([
  '  基准原图：https://fsu.creght.com/site/20871', // 顶到第 45 列（46 列的带，差一格）
  '  43196390854656/1786454035627__contact_qr.pn', // 同一列上断的 —— 硬折宽度就在这儿
  '  g',
], 46)
check('切在扩展名当中的 URL：首行 tap', linkAtCell(qr, 20, 1)?.text, QR)
check('切在扩展名当中的 URL：末行 tap', linkAtCell(qr, 3, 3)?.text, QR)

/* 17. 第 16 条的边界：上一行**不在**同一列上断（普通一行字），那就还是老判据 ——
   结尾看着是完整文件名就不许把下一行的头一个词粘上来。 */
check('上一行没顶到同一列：结尾像文件名还是不拼', links(mk([
  '  刚跑完了',
  '  /Users/bysir/dev/bysir/herdr/a/report.txt', // 43 格（46 列的带，差三格）
  '  found 3 matches',
], 46), 1), ['/Users/bysir/dev/bysir/herdr/a/report.txt'])

/* 18. **用户报的那一条（截图 20260902-2138）：URL 前面直接粘着中文，没有空格。**
   `- B · 更大胆:https://p7boof571u9u.preview.c` + `reght.cn/?hero=b` —— 点开只有
   `https://p7boof571u9u.preview.c`。和第 9 条同一处（`FINISHED` 把被切开的主机名当成
   完整文件名），但 scheme 那道剥不掉：**「词」是按空格切的**，而这一条的词是
   `更大胆:https://…`，`^` 锚在词首的正则压根匹配不上。所以 `SCHEME` 前面那个 `.*`
   是承重的 —— agent 说话时 URL 前面常常直接粘着中文和标点。
   （45 列的 pane，Ink 在自己那层让出一格 —— 正是 slack 那一档，`FINISHED` 才生效） */
const HERO = 'https://p7boof571u9u.preview.creght.cn/?hero=b'
const ab = mk([
  '  - A · 贴源站(当前默认):https://p7boof571u9u',
  '    .preview.creght.cn/',
  '  - B · 更大胆:https://p7boof571u9u.preview.c',
  '    reght.cn/?hero=b',
], 46)
check('中文直接粘着的 URL：A 那条', linkAtCell(ab, 30, 1)?.text, 'https://p7boof571u9u.preview.creght.cn/')
check('中文直接粘着的 URL：B 那条（首行 tap）', linkAtCell(ab, 30, 3)?.text, HERO)
check('中文直接粘着的 URL：B 那条（续行 tap）', linkAtCell(ab, 6, 4)?.text, HERO)

if (fails) {
  console.error(`\n${fails} 处不对`)
  process.exit(1)
}
console.log('\npaths: all ok')
