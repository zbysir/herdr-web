/**
 * GFM 的裸链接（`http://…` 不带 `<>` 也不带 `[]()`）**在中文标点处断开**（chat 模式用，
 * 见 components/ChatMarkdown.tsx）。
 *
 * # 为什么要
 *
 * GFM 的 autolink literal 规则是按西文定的：链接一直吃到**空白**为止，末尾只剥 ASCII 的
 * `.,:;!?*_~` 那几个。全角标点不算终止符，于是 agent 最常见的写法
 *
 *	开发服务器跑在 **http://localhost:5188**。我在浏览器里看过桌面端的 5 个场景
 *
 * 被解析成一条 `http://localhost:5188**。我在浏览器里看过桌面端的` 的链接（用户报的）——
 * 下划线拖了半句话，点开是个错地址，而且链接把后面那个 `**` 吞了，**加粗也跟着没闭合**，
 * 前面那个 `**` 原样显示在屏幕上。终端里那份（claude 自己的 markdown 渲染）是对的。
 *
 * # 为什么是「改源文本再解析一次」，不是改 mdast
 *
 * 只把链接节点截短治不了第二个症状：那个没闭合的加粗是**解析时**就定下的，事后在树上
 * 补一个 strong 等于自己重写一遍强调符配对规则。所以找到截断点之后，把那一段 URL 在源文本里
 * 包成 `<…>`（CommonMark 的尖括号 autolink，边界是写死的），再整份重新解析：
 * `**<http://localhost:5188>**。` 里的 `**` 自然就配上了。
 *
 * 截断点靠**第一遍解析**找，不在源文本上跑正则 —— 那样分不清代码块 / 行内代码里的 URL，
 * 而那里面的字一个都不该动。只有第一遍真找到了才解析第二遍，绝大多数消息只解析一次。
 *
 * # 判据
 *
 *	断在第一个**非 ASCII 的标点**上（`\p{P}`：。，、；：！？「」（）【】《》“”…—）
 *	然后照 GFM 自己的规矩把剩下那截末尾的 `*_~.,:;!?'"` 剥掉（`**` 就是这么剥的）
 *
 * **中文字本身不断**：`https://zh.wikipedia.org/wiki/中文` 是一条合法链接。
 * 只处理带 scheme 的那种 —— `www.x.com。` 包成 `<www.x.com>` 不是合法的 autolink
 * （尖括号那种必须有 scheme），那一类很少见，留着原样。
 */

// 相对路径 + 显式 `.ts` 的理由同 mdpaths.ts：测试是 node 直接跑的

type Node = {
  type: string
  url?: string
  children?: Node[]
  position?: { start: { offset?: number }; end: { offset?: number } }
}

const WIDE_PUNCT = /[^\x00-\x7f](?<=\p{P})/u
const TRAIL = /[*_~.,:;!?'"]+$/
const SCHEME = /^[a-z][a-z0-9+.-]*:\/\//i

/** 在源文本里要包成 `<…>` 的那几段（`[start, end)`，按出现顺序） */
export function cuts(doc: string, tree: Node): [number, number][] {
  const out: [number, number][] = []
  const walk = (n: Node) => {
    if (n.type === 'link' && n.url) {
      const s = n.position?.start.offset
      const e = n.position?.end.offset
      // 源文本就是 URL 本身的才是裸链接（`[x](…)` 和 `<…>` 两种都不是）
      if (s !== undefined && e !== undefined && doc.slice(s, e) === n.url && SCHEME.test(n.url)) {
        const m = WIDE_PUNCT.exec(n.url)
        if (m) {
          const url = n.url.slice(0, m.index).replace(TRAIL, '')
          // 剥完只剩个 scheme（`http://。`）就不算链接了，别包
          if (url.length > (SCHEME.exec(url)?.[0].length ?? Infinity)) out.push([s, s + url.length])
        }
      }
      return
    }
    n.children?.forEach(walk)
  }
  walk(tree)
  return out
}

export function wrap(doc: string, at: [number, number][]) {
  let out = ''
  let i = 0
  for (const [s, e] of at) {
    out += doc.slice(i, s) + '<' + doc.slice(s, e) + '>'
    i = e
  }
  return out + doc.slice(i)
}

/**
 * remark 插件：包在 `remark-parse` 装好的那个 parser 外面（react-markdown 先 use 它，
 * 再 use 我们给的插件，所以挂上来时 `this.parser` 已经在了）。和 remark-gfm 谁先谁后
 * 无所谓 —— micromark 扩展是在真正解析那一刻才读的。
 */
export function remarkCjkAutolink(this: { parser?: (doc: string, file: never) => Node }) {
  const parse = this.parser
  if (!parse) return
  this.parser = (doc, file) => {
    const tree = parse(doc, file)
    const at = cuts(doc, tree)
    return at.length ? parse(wrap(doc, at), file) : tree
  }
}
