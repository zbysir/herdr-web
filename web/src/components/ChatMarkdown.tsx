import { createContext, useContext, useMemo, type ComponentPropsWithoutRef, type ReactNode } from 'react'
import Markdown, { type Components } from 'react-markdown'
import remarkGfm from 'remark-gfm'
import remarkCjkFriendly from 'remark-cjk-friendly'
import { PATH_SCHEME, rehypePaths } from '@/lib/mdpaths'
import { remarkCjkAutolink } from '@/lib/mdautolink'
import { cn } from '@/lib/utils'

/**
 * chat 气泡里的 Markdown。**这个文件是按需加载的那个 chunk**（见 ChatPanel 的 `lazy`）。
 *
 * # 安全：永远不要打开原始 HTML
 *
 * 转录里那些字是 agent 输出的 —— 在这一层等于**不可信文本**（agent 会把网页内容、别人的
 * issue、命令输出原样复述出来），而这个页面能调 `/api/herdr/say`。所以：
 *
 *   - **不装 `rehype-raw`**（react-markdown 默认就不渲染 HTML，别去打开它）
 *   - **外链只放 http/https/mailto**：`javascript:` 那种伪协议在 `<a href>` 里点一下就是
 *     同源脚本执行，而「agent 输出里有一条链接」再正常不过
 *
 * 这和文件浏览那条「吐内容绝不能是 `text/html`」是同一类规矩（见 internal/files 的包注释）：
 * 同源的 HTML 就是一个跳板。
 *
 * # 本地路径走**文件浏览**那条路，不走 href
 *
 * 用户报的：agent 给了一条 `[下载图片](/Users/…/x.png)`，点了什么都不发生。原因是
 * `urlTransform` 把非 http 的 href 清空了 —— 而就算不清空也没用：`file://` 在网页里点不开
 * （浏览器拦死），`/Users/…` 会被当成本站路径去导航。
 *
 * 正确的路早就有：终端里那套（`web/src/term/paths.ts` → App 的 `openPath`）——
 * 先 `stat` 再决定开图 / 开文本 / 开目录。所以这儿**复用同一个判据和同一个动作**：
 * 一条像本地路径的东西渲染成可点的，点下去调 `onPath`，绝不给它真的 `href`。
 *
 * # 正文里光秃秃的路径也要可点
 *
 * agent 更常见的写法压根不是 markdown 链接，而是「写到了 /Users/x/y.png」。终端里那条是
 * 可点的，chat 里不可点的话人会以为「chat 模式功能缺了」。判据直接借 `findPaths`
 * （至少两段或带扩展名、剥尾部标点和行号、截断过的不给链接 —— 每条都是终端那边实测踩出来的），
 * 不另写一份正则。
 *
 * **代码块（`pre`）里不拆**：那儿一屏路径全画上下划线是噪音，而人不会去点代码。
 * 内联 `code` 里拆 —— agent 写路径时十有八九加反引号。
 *
 * # 中文里的 `**加粗**` 要靠 remark-cjk-friendly
 *
 * CommonMark 判「这个 `**` 是不是强调符」看的是它左右挨着什么（flanking 规则），而那套规则
 * 是按西文空格和标点定的：星号紧贴中文标点时**不算**强调符。于是
 * `**「对话」**` 这种写法解析不出来，`**` 原样显示在屏幕上 —— 而 agent 写中文时几乎全是
 * 这种写法（真机截图里抓到的），不接这个插件就是满屏星号。
 *
 * # 裸链接要在中文标点处断开（`remarkCjkAutolink`）
 *
 * `**http://x:5188**。我在…` 按 GFM 会被吃成一条拖到半句话的链接，连带加粗也不闭合。
 * 理由和做法见 lib/mdautolink.ts。
 *
 * # 排版跟着终端那套 token 走
 *
 * 组件里**不写具体颜色**，只用 `@theme` 里那些名字（见 CLAUDE.md 的配色那节）。
 * 代码块用 `bg-bg`（画布那一档）压在气泡的 `bg-ctl` 上，比气泡底色更深一点，
 * 这样它在两种气泡（人的淡绿底 / agent 的灰底）里都分得出来。
 */

/** 外链只放这几个协议。其余（含 `javascript:` / `data:`）一律丢掉 href，字还在、只是不可点 */
const WEB = /^(https?:|mailto:)/i

/**
 * 像本地路径的 href：`/abs`、`~/x`、`./x`、`file://…`，以及正文里拆出来的那条假 scheme
 * （`herdr-path:`，见 lib/mdpaths.ts）。**这些绝不给真 href** —— 走 onPath 那条路。
 */
const LOCAL = new RegExp(`^(${PATH_SCHEME}|file://|~/|\\.{1,2}/|/)`)

/*
 * # 这几个组件必须在**模块级**，不能在渲染里现建
 *
 * react-markdown 的 `components` 是「标签名 → 组件」，它拿这个值去 `createElement`。
 * 所以**函数引用一变就是「换了一个组件类型」**，React 会把那棵子树整个卸掉重挂 ——
 * 而写成 `components={{ a: (p) => …, pre: (p) => … }}` 的话，每次渲染都是新函数。
 *
 * 表现是用户报的「chat 每 3 秒整个重绘，表格滚到最后了又跳回开头」：agent 在跑时
 * `useTick` 让面板每秒重渲染一次，于是**每秒**把所有 `<a>` / `<pre>` / 路径 span 销毁重建，
 * 里面的 DOM 状态（代码块和表格的横向滚动位置、选中的文字）全丢。真机上量过：
 * 路径 span 那个节点 4.5 秒内就不是同一个对象了，而默认标签（`table` / `td`）活着 ——
 * 正好把「只有自定义组件那几棵子树在重挂」这件事分了出来。
 *
 * `onPath` 是唯一的动态输入，所以它走 **context** 而不是闭包：这样组件身份永远不变，
 * 哪怕调用方每次传一个新函数进来也不会触发重挂。
 */

/** 点了一条本地路径要调的东西。走 context 是为了让下面那几个组件能待在模块级（见上） */
const PathHit = createContext<((p: string) => void) | undefined>(undefined)

/** 可点的路径长什么样：和终端里那套一致（品牌色 + 下划线），但它不是 `<a>` */
function PathSpan({ path, children }: { path: string; children: ReactNode }) {
  const onPath = useContext(PathHit)
  return (
    <span
      role="button"
      tabIndex={0}
      title={`打开 ${path}`}
      onClick={() => onPath?.(path)}
      onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') onPath?.(path) }}
      className="cursor-pointer text-brand underline decoration-dotted underline-offset-2"
    >
      {children}
    </span>
  )
}

/**
 * 链接分两种：
 *
 *	外链        真的 `<a>`，新标签打开 + noreferrer（这个页面的 URL 里有 session 名，
 *	            没必要随着 Referer 漏给 agent 复述出来的那个站）
 *	本地路径    **不给 href**，点一下走文件浏览那条路（`file://` 浏览器拦死，
 *	            `/Users/…` 会被当成本站路径去导航 —— 两种都是「点了没反应」）
 *
 * `node` 要从透传里摘掉：react-markdown 会把 hast 节点一起传进来，原样 spread 到 `<a>` 上
 * 是个非法 DOM 属性（React 会一路报 warning）。
 */
function Anchor({ children, href, node: _node, ...p }: ComponentPropsWithoutRef<'a'> & { node?: unknown }) {
  const onPath = useContext(PathHit)
  const h = String(href ?? '')
  if (onPath && h && LOCAL.test(h)) {
    return <PathSpan path={localPath(h)}>{children}</PathSpan>
  }
  return <a {...p} href={h || undefined} target="_blank" rel="noopener noreferrer">{children}</a>
}

/**
 * 代码块：**横向自己滚，不折行**。
 *
 * 折行在这儿是错的 —— 代码的缩进和对齐本身带信息，折过的代码在手机上比横滚更难读
 * （正文折行是对的，那是 chat 存在的理由之一，两件事别混）。
 *
 * `pre` 自己是滚动容器，所以要 `overflow-x-auto` + `whitespace-pre`；里面那个
 * `code` 得把气泡上那套内联样式清掉（`p-0 bg-transparent`），不然代码块里每一段
 * 都顶着一个内联代码的小底色。
 */
function Pre({ children }: { children?: ReactNode }) {
  return (
    <pre className={cn('my-1.5 overflow-x-auto overscroll-x-contain rounded-md border border-line',
      'bg-bg px-2 py-1.5 text-[0.88em] leading-relaxed',
      '[&_code]:whitespace-pre [&_code]:bg-transparent [&_code]:p-0')}>
      {children}
    </pre>
  )
}

/** GFM 的任务列表：去掉那个圆点，让方框顶上去 */
function TaskBox({ node: _node, ...p }: ComponentPropsWithoutRef<'input'> & { node?: unknown }) {
  return <input {...p} disabled className="mr-1 align-middle" />
}

/** 这三个都得是**同一个对象、同一批函数**，理由见上面那段 */
const COMPONENTS: Components = { a: Anchor, pre: Pre, input: TaskBox }
const REMARK = [remarkGfm, remarkCjkFriendly, remarkCjkAutolink]
const REHYPE = [rehypePaths]
const NO_PLUGINS: [] = []

/** 本地路径要放进来（`Anchor` 会把它接走），只挡伪协议 */
const urlOK = (u: string) => (WEB.test(u) || LOCAL.test(u) ? u : '')

/**
 * 两套排版。**同一套插件、同一套安全规矩，只有样式分开。**
 *
 *	chat   气泡里的一段话：标题只比正文大一点点、段距很紧 —— 气泡本来就窄，
 *	       agent 回复里的 `##` 多半只是个小节提示，放大了一屏装不下几句
 *	doc    文件查看器里的整份 md：要能一眼扫出结构。用户报的「正文和标题根本无法区分」
 *	       就是拿 chat 那套去排一份文档 —— h2 只大 8% 又是 medium 字重，
 *	       在 16px 上跟正文差一两个像素
 *
 * 字号都用 em：外面那层给的是 chat 字号那个设置（`chatFont`），这里只定比例。
 */
const CHAT_CLS = `[&_p]:my-1 [&>*:first-child]:mt-0 [&>*:last-child]:mb-0
                   [&_ul]:my-1 [&_ul]:list-disc [&_ul]:pl-4
                   [&_ol]:my-1 [&_ol]:list-decimal [&_ol]:pl-5
                   [&_li]:my-0.5
                   [&_h1]:my-1.5 [&_h1]:text-[1.15em] [&_h1]:font-medium
                   [&_h2]:my-1.5 [&_h2]:text-[1.08em] [&_h2]:font-medium
                   [&_h3]:my-1 [&_h3]:text-[1em] [&_h3]:font-medium
                   [&_blockquote]:my-1 [&_hr]:my-2 [&_table]:my-1
                   [&_strong]:font-medium`
const DOC_CLS = `[&_p]:my-[0.8em] [&>*:first-child]:mt-0 [&>*:last-child]:mb-0
                   [&_ul]:my-[0.8em] [&_ul]:list-disc [&_ul]:pl-5
                   [&_ol]:my-[0.8em] [&_ol]:list-decimal [&_ol]:pl-6
                   [&_li]:my-[0.3em]
                   [&_h1]:mt-[1.2em] [&_h1]:mb-[0.6em] [&_h1]:text-[1.6em] [&_h1]:font-semibold [&_h1]:leading-snug
                   [&_h1]:border-b [&_h1]:border-line [&_h1]:pb-[0.3em]
                   [&_h2]:mt-[1.6em] [&_h2]:mb-[0.5em] [&_h2]:text-[1.3em] [&_h2]:font-semibold [&_h2]:leading-snug
                   [&_h2]:border-b [&_h2]:border-line [&_h2]:pb-[0.25em]
                   [&_h3]:mt-[1.3em] [&_h3]:mb-[0.4em] [&_h3]:text-[1.12em] [&_h3]:font-semibold
                   [&_h4]:mt-[1.2em] [&_h4]:mb-[0.4em] [&_h4]:font-semibold
                   [&_h5]:font-semibold [&_h6]:font-semibold [&_h6]:text-muted
                   [&_blockquote]:my-[0.8em] [&_hr]:my-[1.5em] [&_table]:my-[0.8em]
                   [&_strong]:font-semibold`

export default function ChatMarkdown({ text, onPath, variant = 'chat' }: {
  text: string
  /** 点了一条本地路径。不给的话路径照旧渲染成普通文字（不画成可点的） */
  onPath?: (p: string) => void
  variant?: 'chat' | 'doc'
}) {
  /*
    **按 `text` memo 住。** 光把组件身份固定下来只治了「重挂」，没治「重算」：面板在
    agent 跑的时候每秒重渲染一次，而每次渲染 react-markdown 都要把整段正文重新过一遍
    unified（remark + rehype）。一屏四十个气泡、每秒一遍，手机上是白烧的。
    正文没变就直接给回上一次那个元素，React 连子树都不用进。
  */
  const body = useMemo(() => (
    <Markdown
      remarkPlugins={REMARK}
      rehypePlugins={onPath ? REHYPE : NO_PLUGINS}
      urlTransform={urlOK}
      components={COMPONENTS}
    >
      {text}
    </Markdown>
  ), [text, onPath])

  return (
    <PathHit.Provider value={onPath}>
      <div
        className={cn('min-w-0 break-words', variant === 'doc' ? DOC_CLS : CHAT_CLS, `
                   [&_a]:text-brand [&_a]:underline [&_a]:underline-offset-2
                   [&_strong]:text-fg
                   [&_blockquote]:border-l-2 [&_blockquote]:border-line
                   [&_blockquote]:pl-2 [&_blockquote]:text-muted
                   [&_hr]:border-line
                   [&_code]:rounded [&_code]:bg-bg [&_code]:px-1 [&_code]:py-0.5
                   [&_code]:font-mono [&_code]:text-[0.88em]
                   [&_table]:block [&_table]:w-full [&_table]:overflow-x-auto
                   [&_th]:border [&_th]:border-line [&_th]:px-1.5 [&_th]:py-0.5 [&_th]:text-left [&_th]:font-medium
                   [&_td]:border [&_td]:border-line [&_td]:px-1.5 [&_td]:py-0.5`)}
      >
        {body}
      </div>
    </PathHit.Provider>
  )
}

/**
 * href → 真正的路径。
 *
 *	`herdr-path:/Users/x`   正文里拆出来的（见 lib/mdpaths.ts）
 *	`file:///Users/x`       agent 写的 markdown 链接
 *	`/Users/x` / `~/x`      原样
 *
 * 解不开的 %xx 就原样给回去（`openPath` 那边会报找不到，比在这儿吞掉好）。
 *
 * **markdown 链接里的 `/Users/…` 也要解码，不只是 `file://`**（用户报的「找不到
 * /Users/…/%E6%97%81…md」）：agent 写的是中文原样 `[稿.md](/Users/x/旁白稿.md)`，
 * 但 micromark 按 CommonMark 把链接目标**百分号编码**了（`normalizeUri`），到这儿的
 * href 已经是 `%E6%97%81…`，原样拿去 stat 一定找不到 —— 带中文或空格的文件名全中。
 * `herdr-path:` 那条不解：它是 rehype 那一步直接塞进 hast 的，没被编码过，解了反而会
 * 把文件名里真的 `%` 弄坏。
 */
function localPath(h: string) {
  if (h.startsWith(PATH_SCHEME)) return h.slice(PATH_SCHEME.length)
  const raw = h.startsWith('file://') ? h.slice('file://'.length) : h
  try {
    return decodeURIComponent(raw)
  } catch {
    return raw
  }
}
