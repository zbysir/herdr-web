/**
 * `lib/mdpaths.ts` 的测试 —— 用 node 直接跑这个 .ts（和 term/paths.test.ts 同一个做法，
 * 在 `make test` 里）。
 *
 * 为什么这块值一份测试：它决定「chat 里正文那条路径点不点得开」，而它坏掉的样子是**完全
 * 静默的** —— 路径照旧显示成普通文字，没有下划线、没有报错。第一版就是这么栽的：靠
 * 「react-markdown 会把 hast 的 `dataPath` 透传成 `data-path`」这个没验过的假设，
 * 而验不到的假设失效时和「这个功能没做」长得一模一样。
 */
import assert from 'node:assert/strict'
import { PATH_SCHEME, rehypePaths, type MdNode } from './mdpaths.ts'

const t = (value: string): MdNode => ({ type: 'text', value })
const el = (tagName: string, children: MdNode[]): MdNode => ({ type: 'element', tagName, children })
const root = (children: MdNode[]): MdNode => ({ type: 'root', children })

/** 把树压成一行好断言：文本原样，拆出来的路径写成 `[文字](路径)` */
function flat(n: MdNode): string {
  if (n.type === 'text') return n.value ?? ''
  const inner = (n.children ?? []).map(flat).join('')
  const href = String(n.properties?.href ?? '')
  if (n.tagName === 'a' && href.startsWith(PATH_SCHEME)) {
    return `[${inner}](${href.slice(PATH_SCHEME.length)})`
  }
  if (n.tagName === 'a') return `<a href=${href}>${inner}</a>`
  return inner
}

function run(tree: MdNode) {
  rehypePaths()(tree)
  return flat(tree)
}

let fails = 0
function check(why: string, got: string, want: string) {
  if (got === want) {
    console.log('ok  ', why)
    return
  }
  fails++
  console.log('FAIL', why)
  console.log('     想要:', want)
  console.log('     实际:', got)
}

// 正文里光秃秃的路径 —— agent 最常见的写法
check('段落里的绝对路径',
  run(root([el('p', [t('写到了 /Users/bysir/Downloads/a.png 了')])])),
  '写到了 [/Users/bysir/Downloads/a.png](/Users/bysir/Downloads/a.png) 了')

// 中文句号要剥掉（不剥的话路径带着句号去 stat，永远「找不到」）
check('结尾中文句号不算路径的一部分',
  run(root([el('p', [t('放在 /tmp/out/x.png。')])])),
  '放在 [/tmp/out/x.png](/tmp/out/x.png)。')

// 一段里有好几条
check('一段里两条路径',
  run(root([el('p', [t('从 /a/b/x.go 到 /c/d/y.go')])])),
  '从 [/a/b/x.go](/a/b/x.go) 到 [/c/d/y.go](/c/d/y.go)')

// 内联 code 里**要拆**：agent 写路径时十有八九加反引号
check('内联 code 里的路径也拆',
  run(root([el('p', [t('看 '), el('code', [t('/Users/x/y.png')])])])),
  '看 [/Users/x/y.png](/Users/x/y.png)')

// 代码块里**不拆**：一屏下划线是噪音，而人不会去点代码
check('代码块里不拆',
  run(root([el('pre', [el('code', [t('cat /Users/x/y.png\n')])])])),
  'cat /Users/x/y.png\n')

// 链接里面不拆 —— 否则是嵌套 <a>（非法 HTML），而且它本来就可点
check('链接里面不拆',
  run(root([{ type: 'element', tagName: 'a', properties: { href: 'https://x.com' }, children: [t('/Users/x/y.png')] }])),
  '<a href=https://x.com>/Users/x/y.png</a>')

// 截断过的不给链接（终端那边的规矩）—— 猜一个短的出来只会报「找不到」
check('带 … 的截断路径不给链接',
  run(root([el('p', [t('写到了 /Users/bysir/very/long/pa…')])])),
  '写到了 /Users/bysir/very/long/pa…')

// 不像路径的东西别画上下划线（`and/or`、`读/写` 这种）
check('不像路径的不动',
  run(root([el('p', [t('读/写 都行，and/or 随便')])])),
  '读/写 都行，and/or 随便')

// 没有路径时**原样保留那个文本节点**（别把树拆碎了）
{
  const tree = root([el('p', [t('一句没有路径的话')])])
  rehypePaths()(tree)
  const p = tree.children![0]
  assert.equal(p.children!.length, 1, '没有路径时不该把文本节点拆开')
  assert.equal(p.children![0].type, 'text')
  console.log('ok  ', '没有路径时文本节点原样保留')
}

console.log(fails ? `\nmdpaths: ${fails} 条不过` : '\nmdpaths: 全过')
process.exit(fails ? 1 : 0)
