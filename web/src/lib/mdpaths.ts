// 相对路径 + 显式 `.ts`：这个文件的测试是 **node 直接跑**的（见 mdpaths.test.ts），
// 而 node 解不了 vite 的 `@/` 别名，也不会替你补扩展名。
import { findPaths } from '../term/paths.ts'

/**
 * 把 Markdown 正文里像**本地路径**的那几段变成可点的（chat 模式用，见 components/ChatMarkdown.tsx）。
 *
 * # 为什么要
 *
 * agent 写路径最常见的形式压根不是 markdown 链接，而是「写到了 /Users/x/y.png」。终端里
 * 那条是可点的（`term/paths.ts` 的 link provider），chat 里不可点的话人会以为「chat 模式
 * 功能缺了」。判据直接借**同一个** `findPaths` —— 至少两段或带扩展名、剥尾部标点和行号、
 * 截断过的（带 `…`）不给链接，每一条都是终端那边在真机上踩出来的，不另写一份正则。
 *
 * # 为什么产出的是 `<a href="herdr-path:…">` 而不是带 data 属性的 span
 *
 * 第一版想用 `<span data-path="…">`，靠 react-markdown 把 hast 的 `dataPath` 透传成
 * `data-path`。那是**没验过的假设**，而它失效的样子是完全静默的：路径照旧显示成普通文字，
 * 没有报错、没有下划线，查起来得先怀疑正则、再怀疑遍历，最后才想到属性没透传。
 *
 * `href` 是标准属性、必然透传，而且调用方本来就要处理「这个链接是本地路径还是外链」
 * （agent 也会写 `[下载图片](/Users/…/x.png)`）—— 所以这儿产出同一种形状，两条路合成一条。
 * 前缀选一个不存在的 scheme，调用方**绝不能**把它当真 href 交给浏览器。
 *
 * # 哪儿不拆
 *
 *	代码块（`pre` 里面）  一屏路径全画上下划线是噪音，而人不会去点代码
 *	链接里面（`a` 里面）  它自己已经是可点的了，再拆一层是嵌套 `<a>`（非法 HTML）
 *
 * 内联 `code` 里**要拆** —— agent 写路径时十有八九加反引号。
 */

/** 可点路径那条假 scheme。调用方认它，浏览器永远看不到它 */
export const PATH_SCHEME = 'herdr-path:'

/** hast 里的一个节点（只用得上这几个字段，不为此装 @types/hast） */
export interface MdNode {
  type: string
  tagName?: string
  value?: string
  properties?: Record<string, unknown>
  children?: MdNode[]
}

/**
 * rehype 插件：遍历文本节点，把路径那几段换成 `<a href="herdr-path:…">`。
 *
 * 自己走一遍树而不是装 `unist-util-visit`：要的是「带着祖先链的遍历」（`pre` 里面不拆），
 * 而那个包的 visitor 拿不到祖先链，还得自己攒。十几行的事，不值一个依赖。
 */
export function rehypePaths() {
  return (tree: MdNode) => {
    walk(tree, false)
    return tree
  }
}

function walk(node: MdNode, inPre: boolean) {
  const kids = node.children
  if (!kids) return
  const pre = inPre || node.tagName === 'pre'
  for (let i = 0; i < kids.length; i++) {
    const k = kids[i]
    if (k.type === 'element') {
      if (k.tagName !== 'a') walk(k, pre)
      continue
    }
    if (k.type !== 'text' || pre || !k.value) continue
    const out = split(k.value)
    if (!out) continue
    kids.splice(i, 1, ...out)
    i += out.length - 1
  }
}

/** 把一段文本按路径切开。没有路径就返回 null（调用方原样保留那个文本节点） */
function split(text: string): MdNode[] | null {
  const hits = findPaths(text)
  if (!hits.length) return null
  const out: MdNode[] = []
  let at = 0
  for (const h of hits) {
    if (h.start > at) out.push({ type: 'text', value: text.slice(at, h.start) })
    out.push({
      type: 'element',
      tagName: 'a',
      properties: { href: PATH_SCHEME + h.path },
      children: [{ type: 'text', value: text.slice(h.start, h.end) }],
    })
    at = h.end
  }
  if (at < text.length) out.push({ type: 'text', value: text.slice(at) })
  return out
}
