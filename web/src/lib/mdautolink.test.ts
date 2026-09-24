/**
 * `lib/mdautolink.ts` 的测试 —— node 直接跑（同 mdpaths.test.ts，在 `make test` 里）。
 * 跑的是和 ChatMarkdown **同一套插件**，断言解析出来的树，不是断言中间那一步的字符串。
 */
import assert from 'node:assert/strict'
import { unified } from 'unified'
import remarkParse from 'remark-parse'
import remarkGfm from 'remark-gfm'
import remarkCjkFriendly from 'remark-cjk-friendly'
import { remarkCjkAutolink } from './mdautolink.ts'

type N = { type: string; value?: string; url?: string; children?: N[] }

const proc = unified().use(remarkParse).use(remarkGfm).use(remarkCjkFriendly).use(remarkCjkAutolink as never)

/** 压成一行：链接写成 `[文字](url)`、加粗写成 `**…**`、行内代码写成反引号 */
function flat(n: N): string {
  const inner = (n.children ?? []).map(flat).join('')
  switch (n.type) {
    case 'text': return n.value ?? ''
    case 'inlineCode': return '`' + n.value + '`'
    case 'code': return '```' + n.value + '```'
    case 'strong': return `**${inner}**`
    case 'link': return `[${inner}](${n.url})`
    default: return inner
  }
}
const md = (s: string) => flat(proc.parse(s) as N)

// 用户报的那条
assert.equal(
  md('开发服务器跑在 **http://localhost:5188**。我在浏览器里看过桌面端的 5 个场景'),
  '开发服务器跑在 **[http://localhost:5188](http://localhost:5188)**。我在浏览器里看过桌面端的 5 个场景',
)
// 不带加粗
assert.equal(md('跑在 http://localhost:5188。好'), '跑在 [http://localhost:5188](http://localhost:5188)。好')
assert.equal(md('见 https://a.com/x，然后'), '见 [https://a.com/x](https://a.com/x)，然后')
assert.equal(md('（https://a.com/x）'), '（[https://a.com/x](https://a.com/x)）')
// 中文字本身不断
assert.equal(md('看 https://a.com/路径/中文 好'), '看 [https://a.com/路径/中文](https://a.com/路径/中文) 好')
// 代码里一个字都不动
assert.equal(md('`http://a.com。x`'), '`http://a.com。x`')
assert.equal(md('```\nhttp://a.com。x\n```'), '```http://a.com。x```')
// 显式链接不动
assert.equal(md('[点这](http://a.com/。x)'), '[点这](http://a.com/。x)')
// 同一段里两条
assert.equal(
  md('http://a.com。和 http://b.com，'),
  '[http://a.com](http://a.com)。和 [http://b.com](http://b.com)，',
)

console.log('mdautolink ok')
