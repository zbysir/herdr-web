/**
 * 主题色两边对得上的测试 —— 用 node 直接跑这个 .ts（和 term/paths.test.ts 同一个做法，
 * 在 `make test` 里）。
 *
 * 为什么值一份测试：主题色是**跨语言的一份清单** —— id 和名字在 `lib/prefs.ts`，色值和
 * 规则块在 `index.css`，而两边对不上是**完全静默**的：
 *
 *   - CSS 里少一个 `--sw-<id>`：设置面板里那个色块画出来是透明的（一个洞），
 *     而它旁边五个都好好的，看着像「这个颜色没配好」；
 *   - CSS 里少一个 `:root[data-brand=<id>]` 块：点下去 `<html>` 上的属性是变了的、
 *     按钮也高亮了，但界面一点没变 —— 「点了没反应且不报错」；
 *   - 亮色那半边少写：暗色下一切正常，切到亮色才发现某个主题还是绿的（或者是暗色那版
 *     的亮色，在白底上根本看不清）。
 *
 * 还盯着一条**顺序**规矩：`:root[data-brand=x]` 和 `:root.light` 特异性一样（都是 0,2,0），
 * 谁在后面谁赢。整段 data-brand 必须写在 `:root.light` 之后，不然亮色下选什么都还是绿。
 *
 * 和 Go 那边的 TestPrefsMatchJS 一样，读的是**源码文本**不是 import 那个模块：prefs.ts
 * 挂着 api.ts 那一串浏览器依赖，为一条清单把它们全拖进 node 里不值当。
 */
import { readFileSync } from 'node:fs'

const prefs = readFileSync(new URL('./prefs.ts', import.meta.url), 'utf8')
const css = readFileSync(new URL('../index.css', import.meta.url), 'utf8')

let fails = 0
function check(why: string, ok: boolean, detail = '') {
  if (ok) {
    console.log('ok  ', why)
    return
  }
  fails++
  console.log('FAIL', why)
  if (detail) console.log('     ', detail)
}

// —— prefs.ts 那份清单
const block = /export const BRANDS = \[(.*?)\] as const/s.exec(prefs)
if (!block) {
  console.log('FAIL prefs.ts 里找不到 BRANDS')
  process.exit(1)
}
const ids = [...block[1].matchAll(/id: '([a-z]+)'/g)].map((m) => m[1])
check('BRANDS 不是空的', ids.length > 0, String(ids))

// —— 每套在 CSS 里都得齐：色块变量（暗 + 亮）+ 两个规则块
for (const id of ids) {
  // --sw-<id> 出现两次：一次暗色（:root）、一次亮色（:root.light）
  const sw = [...css.matchAll(new RegExp(`--sw-${id}:`, 'g'))].length
  check(`${id}: --sw-${id} 暗亮各一份`, sw === 2, `实际出现 ${sw} 次`)

  const dark = new RegExp(`:root\\[data-brand="${id}"\\]\\s*\\{([^}]*)\\}`).exec(css)
  check(`${id}: 有 :root[data-brand] 块`, !!dark)
  if (dark) {
    for (const tok of ['--color-brand:', '--color-brand-bg:', '--color-brand-line:', '--color-brand-fg:']) {
      check(`${id}: 暗色块里有 ${tok}`, dark[1].includes(tok), dark[1].trim())
    }
  }

  const light = new RegExp(`:root\\.light\\[data-brand="${id}"\\]\\s*\\{([^}]*)\\}`).exec(css)
  check(`${id}: 有 :root.light[data-brand] 块`, !!light)
  if (light) {
    // 亮色不用重写 --color-brand（--sw-<id> 自己分明暗），但填充那三个必须各写一份：
    // 亮底上主按钮是「主色当底 + 白字」，暗底上是「深色底 + 近白字」，方向是反的
    for (const tok of ['--color-brand-bg:', '--color-brand-line:', '--color-brand-fg:']) {
      check(`${id}: 亮色块里有 ${tok}`, light[1].includes(tok), light[1].trim())
    }
  }
}

// —— 反过来：CSS 里不能有清单外的 id（选不到它，等于一段死代码，而且改配色时会误导人）
const cssIds = new Set([...css.matchAll(/\[data-brand="([a-z]+)"\]/g)].map((m) => m[1]))
for (const id of cssIds) {
  check(`CSS 里的 ${id} 在 BRANDS 里`, ids.includes(id), `多出来的：${id}`)
}

// —— 顺序：整段 data-brand 必须在那些**不带属性**、而且真在写颜色的 :root.light 块之后。
// 「真在写颜色」这一条不能省：文件末尾还有一个只写 color-scheme 的 :root.light，
// 拿它当界限的话这条断言永远不过（而它跟谁盖谁毫无关系）。
const lightAt = [...css.matchAll(/:root\.light\s*\{[^}]*?(?:--color-brand:|--sw-)/g)].map((m) => m.index!)
// 找的是**真选择器**（带引号那种）：上面那段注释里就写着 `:root[data-brand=x]`，
// 拿 indexOf('[data-brand=') 会先命中注释，量出来的位置是假的
const brandAt = /\[data-brand="[a-z]+"\]/.exec(css)?.index ?? -1
check(
  'data-brand 那段写在 :root.light 之后（同特异性，靠位置分胜负）',
  lightAt.length > 0 && brandAt > Math.max(...lightAt),
  `:root.light 在 ${lightAt.join(',')}，第一个 data-brand 在 ${brandAt}`,
)

console.log(fails ? `\nbrand: ${fails} 条不过` : '\nbrand: 全过')
process.exit(fails ? 1 : 0)
