import { Suspense, lazy, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { ArrowDown, Check, ChevronDown, ChevronUp, LoaderCircle, Play, Terminal, Wrench, AlertCircle, MessageSquare, X } from 'lucide-react'
import { ApiError, chatApi, type ChatLog, type ChatMsg, type Pane } from '@/lib/api'
import type { SentEcho } from '@/hooks/useCompose'
import { STATUS_DOT } from '@/lib/agentstatus'
import { paneTitle, tabName } from '@/lib/panename'
import { cn } from '@/lib/utils'

/**
 * chat 模式：把 agent 的对话读成**一条流**，而不是那一屏 TUI。
 *
 * 为什么要这块界面（按 docs/dev/TUI-VS-GUI.md 的判据，触发的是 ① 和 ⑥）：pane 里那个
 * TUI 是给**有键盘的大屏**做的 —— 一行就是一行不能折，翻历史要靠 pager 的快捷键，而
 * 触屏上「滚动」是把按键发过去等它重画一整屏。同一份对话放进网页的普通滚动容器里，
 * 折行、惯性、随手一甩几百行全都白拿。
 *
 * # 数据不读屏
 *
 * 内容来自 agent **自己写在磁盘上的转录**（claude 的 `~/.claude/projects/**.jsonl`、
 * codex 的 rollout），服务端那一半在 `internal/transcript`。为什么不是读屏、不是问
 * herdr（它没有聊天层的接口），全在 docs/dev/CHAT.md 里。
 *
 * **落盘粒度是一次 API 请求**，不是逐 token —— 所以这儿是「每隔几秒冒出一块」，
 * 而不是逐字长出来。想看逐字的那种就回终端（那才是真 PTY）。
 *
 * # 只读
 *
 * 没有发送框：**发言走现成的发件箱**（底下那一行，`agent.prompt`）。另开一条通道就要把
 * OUTBOX.md 里那几个坑再踩一遍（清空要 2N−1 次、`text:…enter` 的回车要隔 200ms、回车
 * 发送必须挡输入法）。审批、选择器、会改状态的事也一律回终端（TUI-VS-GUI.md §2）。
 *
 * # 四件都是真机上报出来的
 *
 * ① **状态**（头上那个点 + 词）：转录按「一次 API 请求」flush，agent 想事情时文件一个
 * 字节都不动（实测 15.58 秒），那段时间对话流完全静止 —— 没有这一档看着像卡住了。
 * 数据跟对话**同一拍**来，不是第二条轮询（服务端那个口本来就调了 `pane.get`）。
 *
 * ② **往上翻更早的**：首屏只给文件尾那一窗，而「翻不到之前的历史」是功能缺了一半，
 * 不是「已知限制」。拿上一批的 `start` 当 `before` 再问一段，接在前面。
 *
 * ③ **人没在底部时，新动态要给个提示**（那个浮在底下的小药丸），点它回到底部。
 * 自动跟随只在人**贴着底**时才做 —— 往上翻历史时把人拽回去是最烦的一种。
 *
 * ④ **刚投出去的话先本地回显**：投稿到它出现在转录里之间有几秒空窗，那几秒屏幕上
 * 一点动静都没有，看着像没投出去。
 *
 * # 「看哪个 pane」只给当前工作空间那几个
 *
 * 和改动面板同一条教训（那条是用户报的 bug 换来的）：herdr 里同时开着几十个 pane 是常态
 * （实测 54 个 / 19 个带 agent），全铺出来的话选择器是一长串八竿子打不着的东西，默认落在
 * 哪个上面全看顺序。所以候选 = **焦点 pane 那个工作空间**里带 agent 的那几个，按
 * `workspaceId` 认（`workspace` 是给人看的标签，两个工作空间同名是常态）。
 */

/** 轮询间隔。转录是按「一次 API 请求」flush 的（几秒一块），3 秒一拍跟得上，又不至于空转 */
const POLL_MS = 3000

/** 手上最多留多少条。面板开一整天的话不掐住就会一直涨 —— 而人往上翻十几条就回终端了 */
const KEEP = 600

/** 留几个 pane 的现场。A↔B 来回切是最常见的，几个就够 —— 留多了白占内存 */
const KEEP_PANES = 4

/** 贴底的容差。别要求像素级贴底 —— 惯性滚动停下来时常常差几像素 */
const BOTTOM_SLACK = 48

/**
 * 「开一个 agent」等多久还没起来就说清楚（见 Nobody 的 ②）。
 *
 * 取 25 秒是照着两段加起来留的余量：claude 冷启动实测几秒，herdr 那边「这个 pane 里有
 * agent 了」最迟再等一拍（`POLL_MS`）。短了会在正常的慢启动上乱报，长了人已经去终端看了。
 */
const WAIT_MS = 25_000

/**
 * Markdown 渲染是**按需加载**的。
 *
 * 为什么不打进主包：unified/micromark 那一套有几十 KB，而这个应用是手机上的终端 ——
 * 首屏那几个连接是要紧的（「刷新页面顶部始终闲动」那条 bug 就是首屏抢连接抢出来的）。
 * chat 面板一打开才拉这个 chunk，之后有缓存。
 *
 * **不装 rehype-raw，也永远不要装**：转录里那些字是 agent 输出的，在这一层等于不可信文本，
 * 而这个页面能调 `/api/herdr/say` —— 放开原始 HTML 就是同源 XSS，和文件浏览那条
 * 「吐内容绝不能是 text/html」是同一类（见 internal/files 的包注释）。react-markdown
 * 默认就不渲染 HTML，这儿只要别去打开它。
 */
const Markdown = lazy(() => import('./ChatMarkdown'))

export function ChatPanel({
  panes, sent, onDropSent, onClose, onToast, onOpenPath, onHealth, focus, chatFont, nudge, onPickPane, hidden,
}: {
  /**
   * 关掉 = **藏起来，不卸载**（用户点名的：每次打开都从头读一遍、重新排版，太慢）。
   * 藏着的时候不轮询（和页面不可见同一条：没人看就别在那台机器上读文件），再打开时
   * `next` 还在，读的是增量。用 `invisible` 不用 `hidden`（display:none）：后者会把
   * 滚动容器的 `scrollTop` 丢掉，打开时停在顶上。
   */
  hidden?: boolean
  /** 点头上那行「看哪个 pane」→ 开面板一览（换一个 agent 看） */
  onPickPane?: () => void
  /** 对话区的字号（px）。**和终端那个 fontSize 是两回事**，见列表容器上那段注释 */
  chatFont: number
  /**
   * 「刚往 pane 里发过东西，立刻补一拍」的计数器（见 App 的 keyNudge）。
   *
   * 按 Esc / `/clear` 这类键是会改对面状态的动作，等 3 秒一拍才看到反应，人会以为
   * 「没点成功」（用户报的）。它一变就读一次。
   */
  nudge?: number
  /** herdr 的 pane 列表。**这就是「看哪个 pane」的候选来源** */
  panes: Pane[]
  /** 刚投出去还没在转录里露面的那几条（见 useCompose 的 sent） */
  sent?: SentEcho[]
  /**
   * 读到一条人话了，让发件箱把对应的回显撤掉。
   * `fifo` = 「对不上原文就丢最老那条」，只有**增量**批次能给真（见 useCompose 的 dropSent）。
   */
  onDropSent?: (pane: string, text: string, fifo?: boolean) => void
  onClose: () => void
  /**
   * 这一拍读到没读到（给顶栏那个状态点用）。
   *
   * chat 走的是 **HTTP 轮询**，和终端那条 WebSocket 是两回事 —— 终端断了 chat 照旧能用。
   * 左上角那个点原来只说终端，在 chat 模式下就变成「看着像整个 app 掉线了」（用户报的）。
   */
  onHealth?: (ok: boolean) => void
  /** 刚切过去的那个 pane（抢跑用，见 App 的 focusHint）。没给就按列表里的 focused 走 */
  focus?: string | null
  /** 一键作答的反馈走 toast（这一下是「顺手做完、结果马上看得见」那类，见 CLAUDE.md） */
  onToast?: (m: string) => void
  /**
   * 点了对话里一条本地路径。**和终端里点路径走的是同一个 openPath** ——
   * 先 stat 再决定开图 / 开文本 / 开目录。两处一个动作，别写第二份。
   */
  onOpenPath?: (p: string) => void
}) {
  /**
   * 看的**永远是焦点 pane**，没有选择器。
   *
   * 第一版在顶上放了个下拉，列出这个工作空间里所有 agent pane。去掉的两条理由（用户报的）：
   *
   *	它是第二条做同一件事的路   换 agent 已经有「面板一览」了，那儿信息全得多（状态点、
   *	                          几分钟前、搜索），在这儿再放一个更差的选择器就是两份平行的
   *	                          东西 —— 这个仓库里那种东西的下场都一样
   *	它的语义不对              下拉问的是「这个工作空间里有 4 个 claude pane」，而人想的是
   *	                          「我现在在这个 agent 上」。后者是**一个 pane 的事**
   *	                          （和 CLAUDE.md 里「『当前』那个标只给焦点 pane 那一条」同一条）
   *
   * 焦点 pane 不是 agent（普通 shell）时**不去随便挑一个** —— 挑哪个全看列表顺序，
   * 而「默认落在哪个上面全看顺序」正是改动面板那个 bug 的来路。那时候明说一句，
   * 让人从面板一览挑。
   */
  /**
   * 抢跑：`focus` 是「刚点过去的那个 pane」，比列表里那个 `focused` 标记早一到两个往返
   * 到手（见 App 的 focusHint）。列表里已经有那条 pane 的 agent / cwd，所以这儿什么都不缺。
   */
  const cur = (focus ? panes.find((p) => p.id === focus) : undefined) ?? panes.find((p) => p.focused)
  const info = cur?.agent ? cur : undefined
  const active = info?.id ?? null

  const [msgs, setMsgs] = useState<ChatMsg[]>([])
  const [meta, setMeta] = useState<Pick<ChatLog, 'sig' | 'more' | 'agent' | 'file' | 'status' | 'start' | 'turn' | 'shells'> | null>(null)
  const [err, setErr] = useState<{ msg: string; reason?: string } | null>(null)
  const [first, setFirst] = useState(true)
  const [older, setOlder] = useState(false) // 正在往上翻
  /** 人没贴着底时攒下来的「有几条新动态」。贴底时恒为 0 */
  const [unseen, setUnseen] = useState(0)
  const [atBottom, setAtBottom] = useState(true)
  /** 哪几串工具调用被点开了（key 见 group）。默认全折着 */
  const [open, setOpen] = useState<Set<string>>(() => new Set())

  // next / sig 放 ref 里而不是 state：轮询那个闭包每拍都要读最新的，进 state 就得把它们
  // 塞进依赖，而依赖一变整个轮询就重启（改动面板那边的读队列踩过同一个坑）。
  const next = useRef(0)
  const sig = useRef('')

  /**
   * 每个 pane 的现场（消息 + 读到哪儿了）。**来回切 agent 就不闪了。**
   *
   * 切 pane 时原来是「清空 → 读取中 → 填上」，于是屏幕上是**内容 → 空白 → 内容**
   * （用户报的「闪动非常明显」）。而那一下清空是必须的 —— 不清就会「头上写着新 pane、
   * 底下还是旧 pane 的对话」，那比闪一下糟得多。
   *
   * 所以换个办法：**把上一个 pane 的现场存起来**，切回去时直接摆出来（连 `next` 一起存，
   * 所以接着读的是增量，不用整份重来）。A↔B 来回切是最常见的用法，这样是瞬时的。
   *
   * 只留最近几个：一份现场是最多 KEEP 条消息，几个 pane 就够了，留多了白占内存。
   */
  const cache = useRef(new Map<string, { msgs: ChatMsg[]; meta: typeof meta; next: number; sig: string }>())
  /** 上一个 active，用来知道「该把现场存到哪个 key 上」 */
  const prev = useRef<string | null>(null)
  // 存现场时要读此刻的 msgs / meta，而把它们塞进 effect 依赖会让整个重置逻辑乱跑 —— 用镜像
  const msgsRef = useRef<ChatMsg[]>([])
  msgsRef.current = msgs
  /** 「现在屏幕上是哪个 pane」的镜像 —— 迟到的响应拿它当判据，见 tick 里那段 */
  const activeRef = useRef<string | null>(active)
  activeRef.current = active
  const metaRef = useRef<typeof meta>(null)
  metaRef.current = meta
  // 贴底也得有一份 ref：滚动回调和轮询回调都要**同步**读它，state 那份在闭包里是旧的。
  const bottom = useRef(true)

  const box = useRef<HTMLDivElement | null>(null)

  const dropLanded = useCallback((list: ChatMsg[], paneID: string, fifo: boolean) => {
    if (!onDropSent) return
    for (const m of list) {
      if (m.kind === 'human' && m.text) onDropSent(paneID, m.text, fifo)
    }
  }, [onDropSent])

  const tick = useCallback(async (paneID: string) => {
    // **页面不可见就不问**：手机切走 / 锁屏那会儿问了也没人看，而这是在跑着 agent 的
    // 那台机器上读文件（和 useGitDirty 同一条）。
    if (document.hidden) return
    // 这一拍是不是「整份读」。**只有整份那次说得准「上面还有没有更早的」** ——
    // 增量那次只读了文件尾部新增的一段，对顶端一无所知。
    const initial = next.current === 0
    try {
      const log = await chatApi.log(paneID, next.current, sig.current)
      // 换 pane 那一下上一拍的响应可能后到 —— 服务端把 pane 回了一遍，对不上就丢掉。
      // 不丢的话新 pane 的对话里会混进旧 pane 的几条，而且看着完全正常。
      /*
        **迟到的响应要和「现在屏幕上是哪个 pane」比，不是和「这个请求是为谁发的」比。**

        原来比的是 `paneID` —— 那就是发起这次请求的那个 pane，所以旧 pane 的响应**一路通过**，
        接着把它的 `sig` / `msgs` / `next` 写进了新 pane 的状态（`setMsgs(旧的)`，而旧的那次
        多半是「增量、没有新消息」= 空数组）。表现就是切 pane 时偶发「这条会话里还没有对话」。
        `active` 不在 tick 的依赖里（那会让每次换 pane 都重建一遍轮询），所以用镜像 ref 现读。

        连 `first` 都不许动：清掉的话新 pane 在自己的响应回来之前就会显示空。
      */
      // 本质是一句话：**只应用「现在正显示的那个 pane」的响应**。两道都要 ——
      // 老服务端不回 `log.pane`，那时只有前一道拦得住。
      if (paneID !== activeRef.current) return
      if (log.pane && log.pane !== activeRef.current) return
      setErr(null)
      onHealth?.(true)
      // sig 变了 = 换了会话（/clear、/resume、压缩）→ 手上那份整份丢掉。
      // 不丢的话新旧两段对话会接在一起，看着像 agent 突然跳回上一个话题。
      if (log.sig !== sig.current) {
        sig.current = log.sig
        setMsgs(log.msgs)
        setUnseen(0)
        // 整份重读那次也要撤回显（人发完就刷新过页面的话，真的那条就在这一批里），
        // 但**不给 fifo** —— 这一批带着几十条历史人话，按 FIFO 丢会把还没落地的误撤掉。
        dropLanded(log.msgs, paneID, false)
      } else if (log.msgs.length) {
        setMsgs((old) => {
          // 按 id 去重：往上翻那一段和手上这批是**有意重叠**的（服务端那边 Start 报的是
          // 整窗起点，见 transcript.Read 的注释），而重复的 key 在 React 里是少画一条（不报错）。
          const seen = new Set(old.map((m) => m.id))
          const add = log.msgs.filter((m) => !seen.has(m.id))
          if (!add.length) return old
          // 人没贴着底就攒一个数给他提示（③）。贴着底的话自动跟随会把他带过去，不用提示。
          if (!bottom.current) setUnseen((n) => n + add.length)
          const all = [...old, ...add]
          return all.length > KEEP ? all.slice(all.length - KEEP) : all
        })
        // 真的那一条从转录里读回来了 → 把本地那条「投递中」撤掉（④）。增量那批里冒出人话
        // 就说明落地了，所以**对不上原文也算**（fifo=true，理由见 useCompose 的 dropSent）。
        dropLanded(log.msgs, paneID, true)
      }
      // **前面某几条的结果到了**（工具成没成 / 提问选了哪个）——「结果那一行」常常落在下一批里，
      // 不打这些补丁就是「工具永远正在跑、提问那张卡永远没选」，只有刷新才对（用户报的）。
      if (log.updates?.length) {
        setMsgs((old) => patch(old, log.updates!))
      }
      next.current = log.next
      // `more` / `start` **只从整份那次采纳，增量那次保住手上的**。
      // 写成每拍都盖的后果是：首屏说了「上面还有」，第二拍（增量）把它覆盖成 false，
      // 「看更早的」那个按钮活不过 3 秒就消失了 —— 真机上抓到的。
      setMeta((m) => ({
        sig: log.sig,
        agent: log.agent,
        file: log.file,
        status: log.status,
        turn: log.turn,
        shells: log.shells,
        more: initial || !m ? log.more : m.more,
        start: initial || !m ? log.start : m.start,
      }))
    } catch (e) {
      // 读不出来要**说清是哪一种**：hook 还没装 / 同一目录好几个 pane / 别的。
      // 混成一句「打不开」的话，最常见那种（agent 是装 hook 之前起来的）永远查不出来。
      setErr({
        msg: e instanceof Error ? e.message : String(e),
        reason: e instanceof ApiError ? e.reason : undefined,
      })
      // 「还没对话」不是故障：顶栏那个点别变红（它回答的是「chat 这条路通不通」）
      onHealth?.(e instanceof ApiError && e.reason === 'no_transcript')
      setFirst(false)
      return
    }
    setFirst(false)

  }, [dropLanded, onHealth])

  /*
    换 pane：把上一个的现场存起来，新的那个**有现场就直接摆出来**（不闪），没有才走加载。

    **必须是 `useLayoutEffect`。** 普通 effect 是**画完之后**才跑的，而 `active` 一变
    React 先提交一帧 —— 那一帧是「新 pane 的头 + 旧 pane 的对话」（`msgs` 还没换）。
    肉眼看到的就是切 pane 时闪一下别人的内容（用户报的「闪动非常明显」里的第一下）。
    放在 layout 里，下面那个 `setMsgs` 会在**同一次绘制之前**被冲掉，屏幕上只有一帧。
  */
  useLayoutEffect(() => {
    const p = prev.current
    // **只有真读到过的现场才存**（`sig` 是第一次成功响应才写的）。存一个还在加载的空快照，
    // 下次切回来就是「现场还在」+ 一条消息都没有 → 屏幕上写「这条会话里还没有对话」，
    // 而其实只是没读完（用户报的「切面板时有很小的几率说当前面板没有对话」）。
    if (p && p !== active && sig.current) {
      cache.current.delete(p) // 先删再插 = 让它排到最后（只留最近几个）
      cache.current.set(p, { msgs: msgsRef.current, meta: metaRef.current, next: next.current, sig: sig.current })
      while (cache.current.size > KEEP_PANES) {
        const oldest = cache.current.keys().next().value
        if (oldest === undefined) break
        cache.current.delete(oldest)
      }
    }
    prev.current = active

    bottom.current = true
    setAtBottom(true)
    setUnseen(0)
    setErr(null)
    setOpen(new Set())

    const hit = active ? cache.current.get(active) : undefined
    if (hit) {
      // 现场还在：连 next 一起恢复，所以下一拍读的是**增量**，不用整份重来
      setMsgs(hit.msgs)
      setMeta(hit.meta)
      next.current = hit.next
      sig.current = hit.sig
      setFirst(false)
      return
    }
    next.current = 0
    sig.current = ''
    setMsgs([])
    setMeta(null)
    setFirst(true)
  }, [active])

  /*
    刚发过键就补几拍（见 nudge）。

    **一次不够。** chat 的状态来自 herdr 的 `agent_status`，而 herdr 自己那个状态是**刮屏**
    得出来的（见 docs/dev/HERDR-API.md）—— 按下 Esc 之后它要过一会儿才翻，所以「立刻读
    一次」很可能读到的还是翻转前的值，然后就得等下一拍（3 秒）。用户报的「按了 esc 状态
    还是慢半拍」就是这一段。

    所以按完键之后**在 1.6 秒里多采几次**（120 / 400 / 900 / 1600ms），采到就采到了 ——
    重复读的代价只是几次 `pane.get`，而这几拍正是人盯着屏幕等反应的那一段。
    **不进上面那个轮询的依赖**：那会把整条心跳重建一次（清掉计时器再从头排）。
  */
  useEffect(() => {
    if (!nudge || !active || hidden) return
    const at = [0, 120, 400, 900, 1600]
    const timers = at.map((ms) => window.setTimeout(() => void tick(active), ms))
    return () => timers.forEach(clearTimeout)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [nudge])

  // 自排队的 setTimeout，不用 setInterval：网络一慢 setInterval 会把请求叠起来
  // （提示和角标那两条同理）。回到前台立刻补一拍，不等这一轮的计时器。
  useEffect(() => {
    if (!active || hidden) return
    let alive = true
    let timer = 0
    const loop = async () => {
      if (!alive) return
      /*
        **这一下必须包起来。** `tick` 一旦抛出来（它里面那个 try 只兜住了网络那一段），
        `loop` 就在这儿断了，后面那个 `setTimeout` 再也不排 —— 轮询**永久停住**，
        状态冻在当时那一刻，而屏幕上一个字都不报。刚切过去那一下冻住就是一片空白
        「这条会话里还没有对话」，而且自己不会好，只能把面板关掉重开（整个重挂）。
        用户报的正是这个。所以：错就错一拍，下一拍照旧排。
      */
      try {
        await tick(active)
      } catch {
        onHealth?.(false)
      }
      if (alive) timer = window.setTimeout(loop, POLL_MS)
    }
    void loop()
    const wake = () => { if (!document.hidden) void tick(active) }
    document.addEventListener('visibilitychange', wake)
    return () => {
      alive = false
      clearTimeout(timer)
      document.removeEventListener('visibilitychange', wake)
    }
  }, [active, tick, onHealth, hidden])

  /* --------------------------------------------------------------- 滚动 */

  const onScroll = useCallback(() => {
    const el = box.current
    if (!el) return
    const at = el.scrollHeight - el.scrollTop - el.clientHeight < BOTTOM_SLACK
    bottom.current = at
    setAtBottom(at)
    // 一滚到底就算「都看过了」，那个提示自己消失。
    if (at) setUnseen(0)
  }, [])

  /**
   * 点药丸回到底部。**瞬时跳，不用 smooth。**
   *
   * 真机上量过：从上面翻历史的位置跳回底部常常是五千多像素，smooth 要跑好一会儿，而这段
   * 动画期间状态对不上账 —— 我们已经把「贴底了」置上（药丸消失），但中途每一次 scroll
   * 事件算出来的还是「离底很远」（药丸闪回来），这期间来新消息还会重新计数。
   * 瞬时跳没有中间态，而且聊天类界面本来就是这么跳的。
   */
  const toBottom = useCallback(() => {
    const el = box.current
    if (!el) return
    el.scrollTop = el.scrollHeight
    bottom.current = true
    setAtBottom(true)
    setUnseen(0)
  }, [])

  /** 贴回底部（不带动画）—— 自动跟随和「容器变矮了」两处都用它 */
  const stick = useCallback(() => {
    const el = box.current
    if (!el || !bottom.current) return
    el.scrollTop = el.scrollHeight
  }, [])

  /*
    自动跟随：**只在人贴着底时**。往上翻历史时把人拽回底下是最烦的一种
    （和 DiffViewer 的 selfScroll 同一条：别跟人抢滚动）。

    **同样必须是 `useLayoutEffect`。** 这是「改了 DOM 再把滚动位置纠正回来」那一类，
    而纠正跑在绘制之后就一定看得见中间态：切 pane 时新对话是用**旧的 `scrollTop`**
    画出来的（旧的 3000 落进一份两万像素长的新对话里，看着就是停在顶部附近），
    然后才被拽到底 —— 用户报的「像先滚到顶部再一下子滚到底部」正是这一帧。
    放在 layout 里就没有那一帧：位置在同一次绘制里就已经是对的。
  */
  useLayoutEffect(stick, [msgs, sent, stick])

  /**
   * **人一发言就强制滚到底**（不管他此刻在哪儿）。
   *
   * 自动跟随那条只在「本来就贴着底」时才动，这是对的 —— 翻历史时别跟人抢滚动。但**发言是
   * 个明确动作**：人刚投出去，肯定是要看结果的，这时候还把他留在半屏之上就等于「投了没反应」
   * （用户报的）。所以这一下越过那个条件。
   *
   * 判据是「最新那条回显的时间戳比上次见过的大」—— 单调，所以回显被撤掉（`sent` 变短）
   * 时不会误触发。只认投给**当前这个 pane** 的：投给别的 pane 时这屏的底部没什么新东西。
   */
  const lastSent = useRef(0)
  useEffect(() => {
    const mine = (sent ?? []).filter((x) => x.target === active)
    const newest = mine.length ? mine[mine.length - 1].at : 0
    if (newest > lastSent.current) {
      lastSent.current = newest
      toBottom()
    }
  }, [sent, active, toBottom])

  /**
   * **容器一变矮就得重新贴底。**
   *
   * 手机上呼输入法是最常见的那一下（用户报的「弹出输入法会导致自动贴底不生效」）：视口一缩，
   * 这个滚动容器的 `clientHeight` 跟着变小，而 `scrollTop` 不动 —— 于是「原来贴着底」当场变成
   * 「离底还有半屏」。而那一下**不触发 scroll 事件**（容器变矮只是把 max scrollTop 变大，
   * 浏览器没有理由去夹 scrollTop），所以光靠 onScroll 是发现不了的，`bottom.current` 还是 true，
   * 但屏幕上人已经看不到最新那条了 —— 得等下一条消息进来才被动跟上。
   *
   * 盯的是**容器自己的尺寸**而不是 `visualViewport`：呼键盘、转屏、快捷键条开合、拖发件箱
   * 把手，这些都会改它的高度，而它们在这儿是同一件事。一个 ResizeObserver 全收。
   *
   * 「呼一次输入法不是一下」（见 CLAUDE.md）—— 视口是一格一格变过来的，所以这儿**不做防抖**：
   * 每一格都贴一次，才跟得住键盘那段动画。代价只是几次 `scrollTop` 赋值。
   */
  useEffect(() => {
    const el = box.current
    if (!el || typeof ResizeObserver === 'undefined') return
    const ro = new ResizeObserver(stick)
    ro.observe(el)
    // 后台标签页里 ResizeObserver 不一定送（它跟着帧走），所以回到前台补一次 ——
    // 锁屏回来时视口尺寸多半已经变过了。
    const wake = () => { if (!document.hidden) stick() }
    document.addEventListener('visibilitychange', wake)
    return () => {
      ro.disconnect()
      document.removeEventListener('visibilitychange', wake)
    }
  }, [stick])

  /**
   * 往上翻更早的（②）。
   *
   * **接上去之前先记住位置，接完把长高的那一截补回 `scrollTop`** —— 往前面插内容会把
   * 正在看的那一块往下顶，不补的话手一松屏幕就跳走了（和 DiffViewer 那条「换上真内容时
   * 要把差值补回去」是同一个问题，Safari 不支持 overflow-anchor，指望不上浏览器）。
   */
  const loadEarlier = useCallback(async () => {
    const el = box.current
    const from = meta?.start ?? 0
    if (!active || !el || older || from <= 0) return
    setOlder(true)
    const before = el.scrollHeight
    const keep = el.scrollTop
    try {
      const log = await chatApi.earlier(active, from)
      // **只取 msgs / start / more**：这一批的 next 指的是文件尾，采纳它会让增量从中间
      // 重读一大段（见 chatApi.earlier 的注释）。
      setMsgs((old) => {
        const seen = new Set(old.map((m) => m.id))
        const add = log.msgs.filter((m) => !seen.has(m.id))
        return add.length ? [...add, ...old] : old
      })
      // **没有进展就别再给「看更早的」了。** 服务端保证 start 会往前走（见 ReadBefore 的
      // 注释，真机上踩过原地打转），但这儿再挡一道：万一哪天它退步，也不能让人一直点着
      // 一个什么都不发生的按钮、每点一次还打一个请求。
      const stuck = log.start >= from
      setMeta((m) => (m ? { ...m, start: log.start, more: !stuck && !!log.more } : m))
      // 等这一批画上去之后再补位置。用 rAF 而不是直接算：DOM 还没重排，此刻的
      // scrollHeight 是旧的。
      requestAnimationFrame(() => {
        const e2 = box.current
        if (!e2) return
        e2.scrollTop = keep + (e2.scrollHeight - before)
      })
    } catch (e) {
      setErr({
        msg: e instanceof Error ? e.message : String(e),
        reason: e instanceof ApiError ? e.reason : undefined,
      })
    } finally {
      setOlder(false)
    }
  }, [active, meta?.start, older])

  /**
   * 哪条提问**还没答** —— 判据和服务端那边一样：**最后一条工具调用**带 ask 且没有结果。
   *
   * 一定要是最后那条：早先那些早就答过了，而对着一条答过的提问发 ↓↵ 就是往输入框里打回车
   * （会把草稿提交出去）。服务端在真发键之前还会自己核一遍同样的判据，这儿只决定「画不画
   * 那几个可点的按钮」。
   */
  /**
   * **此刻真在等人答的那张卡**（判据：最后一条工具调用是提问且还没有结果）。
   *
   * 这里和下面那个「能不能在这儿代答」**必须分成两个值** —— 原来是一个，于是多题 / 多选
   * 那种（代答不了）被一路当成「不是在等你答」，卡底下那行小字就落到最后那句
   * 「这个问题已经答过了」上：agent 明明红着「在等你回答」，屏幕上却说你答过了
   * （用户报的「我没有答啊」）。这条错的方向最糟 —— 人会因此**不去答**，而对面就一直卡着。
   */
  const pendingAskID = useMemo(() => {
    for (let i = msgs.length - 1; i >= 0; i--) {
      const m = msgs[i]
      if (m.kind !== 'tool') continue
      if (!m.ask || m.ok !== undefined) return null
      return m.id
    }
    return null
  }, [msgs])

  /**
   * 其中**能在这儿一键代答**的那种：单个问题 + 单选。
   *
   * 多选是空格勾选、多题要一题一题走，按键序列都不是「↓ ×n + ↵」—— 宁可不给点，别替人选错
   * （服务端那个口也会自己再核一遍，见 chatapi.go 的 pendingAsk）。
   */
  const answerableAskID = useMemo(() => {
    if (!pendingAskID) return null
    const qs = msgs.find((m) => m.id === pendingAskID)?.ask?.questions ?? []
    if (!qs.length) return null
    // **判据要和服务端那份一样**（chatapi.go 的 pendingAsk）：按键是单个数字字符，
    // 所以选项超过 9 个就发不了。前端不挡的话是「点了报错」，那比不给点更糟。
    return qs.every((q) => q.options.length > 0 && q.options.length <= 9) ? pendingAskID : null
  }, [msgs, pendingAskID])

  /**
   * 在焦点那个 pane 里开一个 agent。
   *
   * **寻址用 `cur?.id` 不是 `active`** —— 走到这一步正因为那个 pane 里没有 agent，
   * 而 `active` 恰恰是「有 agent 的那个 pane」，此刻必然是空的（拿它发就是 400「要带上 pane」）。
   *
   * 这儿**故意不 catch**：失败要画在那一屏上（`Nobody` 接着往下写红字），
   * 而 toast 贴在整屏最下沿 —— 人的眼睛在屏幕正中那两个按钮上（见 Nobody 的 ②）。
   */
  const start = useCallback(async (agent: 'claude' | 'codex') => {
    const id = cur?.id
    if (!id) throw new Error('还不知道焦点在哪个 pane 上')
    await chatApi.start(id, agent)
  }, [cur?.id])

  /**
   * 替人答那个选择框。
   *
   * **失败故意不在这儿接** —— 让它抛给那张卡（AskCard 会把原因写在提交键底下）。
   * 原来是在这儿 catch 成 toast，而 toast 贴在整屏最下沿，人的眼睛在刚点的那个按钮上
   * （CLAUDE.md 那条「点了没反应多半是反馈离手指太远」）。成功那一下照旧走 toast：
   * 那是「顺手做完、结果马上看得见」的那类（对话流当场就会动）。
   */
  const answer = useCallback(async (picks: number[][], other: string[]) => {
    if (!active) throw new Error('还不知道在看哪个 pane')
    const r = await chatApi.answer(active, picks, other)
    // 立刻补一拍：答完 agent 马上就动起来了，等 3 秒才更新看着像没答上
    void tick(active)
    onToast?.(`已选「${r.picked}」`)
  }, [active, tick, onToast])

  /* --------------------------------------------------------------- 画 */

  // 回显：只画「投给当前这个 pane」的（不然会在 A 的对话里看到投给 B 的话），
  // 而且 60 秒还没被真的那条顶掉就不再画 —— 一直挂着「投递中」比没有更让人不放心。
  const pending = (sent ?? []).filter((x) => x.target === active && Date.now() - x.at < 60_000)
  const st = meta?.status ?? info?.status ?? ''

  return (
    /*
     * **这是一个模式，不是一个弹窗。**
     *
     * 用户报的原话：「chat 模式我是想常驻的……现在它更像一个弹窗，而不是一个模式」。
     * 弹窗那几个特征是一起来的：浮在中间的圆角卡片 + 阴影 + 一个 ×、切个 pane 就没了、
     * 刷新一下也没了。所以这儿**不用 `Panel`**（那是浮层的壳），自己铺满终端那块区域：
     *
     *   - `inset-0` 铺满**这个定位祖先**，也就是终端那块 —— 底下那一行发件箱在它外面，
     *     照旧点得到（一边看一边说才是这块界面的用法）
     *   - `z-6`：在**未连接那张遮罩之上**（chat 走 HTTP，终端没连上也能看对话）、
     *     **浮层之下**（面板一览 z-10 —— 开它换 agent 时浮在 chat 上面，挑完一收 chat 还在）
     *   - 开着的状态存在 localStorage 里，刷新还在（见 App 的 chatOpen）
     *   - 切 pane **不关它**（gotoPane 里对 chat 例外），跟着新 pane 走
     */
    <section
      className={cn('absolute inset-0 z-6 flex flex-col bg-bar', hidden && 'invisible pointer-events-none')}
      // inert：藏着时里面的按钮 / 输入框别被 Tab 到、也别被读屏念出来
      inert={hidden}
      aria-hidden={hidden}
    >
      {/* 第一排：看哪个 pane + 状态 + × 。和改动面板一样不给标题栏 —— 手机上那一整行就是
          44px 的空白，而「这是什么面板」看内容就知道 */}
      <div className="flex shrink-0 items-center gap-1.5 border-b border-line bg-bar px-3 py-2">
        {/*
          「我在哪个 agent 上」照面板一览那一行的样子：tab 名 + agent 小标 + cwd。

          **整行可点，点开面板一览**（用户点名要的）：这一行回答的就是「我在看谁」，
          那么「换一个看」最该在它自己身上，而不是绕到顶栏那个 ▦ 去。
          用真 `<button>` 不是 `div + onClick`：触屏上丢 click 那条兜底认的是
          `[role=button]`（见 lib/tap.ts），而按钮天生就有。
          **只有左边这段身份是按钮** —— 右边的状态药丸和 ✕ 各自独立，不然点关闭会先开个面板。
        */}
        <button
          type="button"
          onClick={onPickPane}
          disabled={!onPickPane}
          title="换一个 agent 看（打开面板一览）"
          className="flex min-w-0 flex-1 items-center gap-1.5 rounded-md px-1 py-0.5 text-left
                     enabled:hover:bg-ctl disabled:opacity-100"
        >
          {info ? (
            <>
              {/*
                **标题占主位，路径垫底**（用户点名的）：手机上那一行就那么宽，而人要的是
                「我在哪个对话里」—— 标题（claude 自己给这轮起的那个）直接回答它，
                而 `~/dev/bysir/herdr-web` 那串在手机上几乎总被截断、也说不出是哪个对话。

                所以：标题 `flex-1` 拿走剩下的宽度、最后才被截；tab 名和路径降成次要，
                **路径 `shrink` 先被挤掉**。标题拿不到时退回 tab 名 / id ——
                shell pane、拿不到 title 的、以及标题是泛名字（`Claude Code`）的都走这条 —— 和面板一览共用
                那一份（`paneTitle`，见 lib/panename.ts）。
              */}
              <span className="min-w-0 flex-1 truncate text-[1em]">
                {paneTitle(info) || tabName(info) || info.id}
              </span>
              {/*
                **tab 名和路径拆成两段，而且各自有上限。**

                原来它俩是一个 span：标题 `flex-1`（basis 0，只分**剩余**空间），而这一段是
                `shrink`（basis 按内容算）—— 于是路径先按 `2.herdr-web ll · ~/dev/bysir/herdr-web`
                的完整宽度占位，标题被挤到只剩一个字。用户报的是「多出一个『手』字是什么东西」，
                那其实是他这一轮的标题「**手**机端 tab 和 Space 管理」的头一个字，看着像个
                莫名其妙的图标 —— 比纯粹截断更糟，因为它不像被截断的。

                所以：tab 名封顶 40%（一行里它只是用来分开同项目的几个 tab），**路径在手机
                竖屏上整个不画** —— 那儿它总是被截成 `~/dev/bys…`，说不出是哪个对话，而这
                一行最该回答的就是「我在哪个对话里」（和标题占主位是同一条）。
              */}
              {/* 标题拿不到、主位已经是 tab 名时就别再摆一遍（截图里是「1 … 1」两个） */}
              {info.tab && paneTitle(info) && (
                <span className="min-w-0 max-w-[40%] shrink truncate text-xs text-faint">{tabName(info)}</span>
              )}
              <span className="min-w-0 shrink truncate text-xs text-faint max-phone:hidden">
                {shortPath(info.cwd)}
              </span>
            </>
          ) : (
            <span className="min-w-0 truncate text-xs text-muted">
              {cur ? `当前 pane（${cur.id}）里没有 agent` : '还没拿到 pane 列表'}
            </span>
          )}
        </button>
        <Status status={st} model={meta?.turn?.model} agent={info?.agent} />
        {/*
          这儿原来还有一个 `>_`「回终端看这个 pane」。**去掉了**，它是 chat 还是浮动面板时的
          遗留：那会儿 chat 能通过下拉看**另一个** pane，所以需要「跳过去」这个动作。
          现在两个前提都没了 —— chat 永远跟焦点（要跳的就是当前那个，focus 是空操作），
          而它又不退出 chat 模式，于是点下去的实际效果是「herdr 把一个你看不见的 pane 放大了」，
          屏幕上什么都不动。那正是这个仓库最不能留的那种按钮（「点了没反应且不报错」）。

          真正的「回到终端」就是下面那个 ×（退出模式）。
        */}
        <button
          type="button"
          onClick={onClose}
          title="回到终端（退出 chat 模式）"
          className="flex size-7 shrink-0 items-center justify-center rounded-md text-muted hover:bg-ctl hover:text-fg"
          aria-label="回到终端"
        >
          ✕
        </button>
      </div>

      {/* relative：那个「回到底部 / 有新动态」的小药丸是 absolute 贴在这一块底下的 */}
      <div className="relative flex min-h-0 flex-1 flex-col">
        <div
          ref={box}
          onScroll={onScroll}
          // 宽屏（lg）两边留**固定**的白，消息铺满中间（见 Bubble 那条注释）
          className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-3 lg:px-8"
        >
          {/*
            **「没有 agent」要排在「读取中」前面。**
            它是个**确定状态**，不是加载中 —— 而且那时候轮询压根不跑（`if (!active) return`），
            `first` 永远翻不成 false。顺序写反的表现是这一屏一直转「读取中…」，而头上明明
            写着「当前 pane 里没有 agent」（用户报的）。
          */}
          {!active ? (
            <Nobody pane={cur?.id} onClose={onClose} onStart={start} />
          ) : err && !msgs.length ? (
            /*
              **手上有对话时，出错只占顶上一条细带**（见下面那个 Strip），别把整块换掉。
              原来是 `err ? <Problem/>`，于是**一次网络抖动就把人正在读的对话清空了** ——
              而 chat 走的是 3 秒一拍的 HTTP 轮询，抖一下再正常是常态。
              只有「压根没东西可看」时才铺满整屏说清原因。
            */
            <Problem err={err} agent={info?.agent} />
          ) : first ? (
            /*
              **骨架，不是空白。** 首次进一个 pane 要等一个往返（手机走隧道两三百毫秒），
              那段时间给一片灰条比给一行「读取中…」稳 —— 后者在一大块空白正中间，读起来
              就是「内容没了」（用户报的「先空白，再加载出内容」）。
              来回切的那种压根走不到这儿：现场存着，直接摆出来（见 cache）。
            */
            <Skeleton />
          ) : !msgs.length && !pending.length ? (
            <p className="py-6 text-center text-xs text-faint">
              这条会话里还没有对话{meta?.file ? <><br /><span className="text-faint">{meta.file}</span></> : null}
            </p>
          ) : (
            /* 对话区的字号是**一个独立的设置项**（`chatFont`，跟着排布走）——
               终端那个 `fontSize` 管的是 xterm，两回事（用户点名要分开）。挂在这一层而不是
               整个面板上：顶栏那行 pane 名、底下那颗「新动态」药丸是**外壳**，跟着一起放大
               只会把固定高度的地方撑变形，而人要调的是「对话看得清不清」。
               里面那几个组件的字号都写成 em，所以全跟着这一个数走。 */
            <div className="flex flex-col gap-2 py-3" style={{ fontSize: `${chatFont}px` }}>
              {err && <Strip msg={err.msg} />}
              {meta?.more ? (
                <button
                  type="button"
                  onClick={() => void loadEarlier()}
                  disabled={older}
                  className="mx-auto flex items-center gap-1 rounded-md border border-line bg-ctl px-2.5 py-1
                             text-xs text-muted hover:bg-ctl-hi hover:text-fg disabled:opacity-60"
                >
                  <ChevronUp className="size-3.5" />
                  {older ? '读取中…' : '看更早的'}
                </button>
              ) : msgs.length ? (
                <p className="text-center text-xs text-faint">—— 这条会话的开头 ——</p>
              ) : null}

              {group(msgs).map((r) => (r.kind === 'msg'
                ? (
                  <Bubble
                    key={r.msg.id}
                    m={r.msg}
                    // **只有最后那条没答的提问才给点。** 早先那些早就答过了，
                    // 而对着一条答过的提问发 ↓↵ 就是往输入框里打回车。
                    live={r.msg.id === pendingAskID}
                    onAnswer={r.msg.id === answerableAskID ? answer : undefined}
                    onOpenPath={onOpenPath}
                  />
                )
                : (
                  <ToolRun
                    key={r.key}
                    items={r.items}
                    open={open.has(r.key)}
                    onToggle={() => setOpen((s2) => {
                      const n = new Set(s2)
                      if (n.has(r.key)) n.delete(r.key)
                      else n.add(r.key)
                      return n
                    })}
                  />
                )))}

              {/* 刚投出去还没露面的那几条（④） */}
              {pending.map((x) => (
                <div key={`pending:${x.at}`} className="flex justify-end lg:justify-start">
                  {/*
                    和真的那条**一模一样，只是半透明**（用户要的）。
                    原来还挂了一个小时钟图标 —— 去掉了：它和旁边那条落地的气泡形状不一样，
                    看着像另一种东西，而这一条其实就是同一句话、只是还没记进转录。
                    半透明已经把「还没落地」说清了，多一个图标只是噪音。
                  */}
                  {/* 字号写成 em：这个气泡和真消息长一样，字号也得跟着对话字号走
                      （漏了就是「调了字号，投递中那条不跟着变」—— 用户报的） */}
                  <div className="max-w-[85%] rounded-card border border-brand/40 bg-brand/12 px-3 py-2
                                  text-[1em]/relaxed text-fg opacity-50">
                    <span className="whitespace-pre-wrap break-words">{x.text}</span>
                  </div>
                </div>
              ))}

              {/* 状态那一行，照 claude 自己那条来。在跑时它是那段静止里唯一的活物（①）；
                  跑完之后留一行「用了多久 · 几点完的」—— 那是回头看最想知道的两件事 */}
              <Running status={st} turn={meta?.turn} shells={meta?.shells} />
            </div>
          )}
        </div>

        {/* 没贴着底时那个药丸：有新动态就说几条（点一下回到底部），没有就只是「回到底部」（③⑤） */}
        {!atBottom && !err && (msgs.length > 0 || pending.length > 0) && (
          <button
            type="button"
            onClick={toBottom}
            className={cn(
              'absolute bottom-3 left-1/2 flex -translate-x-1/2 items-center gap-1.5 rounded-full border px-3 py-1.5',
              'text-xs shadow-[0_8px_24px_-8px_rgba(0,0,0,.8)]',
              /*
                **底色要基本不透。** 原来「有新动态」那档用的是 `bg-brand/12`（12% 的绿），
                而这颗药丸是浮在**正在滚的对话**上面的 —— 字后面的内容直接透过来，看着就是
                「全透明」（用户报的）。留一点点透是好的（知道底下有东西），所以 `bg-bar/95`。
                强调仍然按仓库那套走：绿边 + 绿字，不是整块涂满（见配色那节）。
              */
              'bg-bar/95',
              unseen > 0 ? 'border-brand/40 text-brand' : 'border-line text-muted hover:text-fg',
            )}
          >
            <ArrowDown className="size-3.5" />
            {unseen > 0 ? `${unseen} 条新动态` : '回到底部'}
          </button>
        )}
      </div>
    </section>
  )
}

/**
 * 把「结果到了」那些补丁打到手上那份消息里。
 *
 * 按 `ref`（claude 的 tool_use_id）认那一条。没认到的补丁直接丢 —— 那条工具在窗口外面
 * （太早，已经被截掉了），没什么可打的。
 *
 * 返回新数组只在**真有东西变**时，不然 React 会因为身份变了白重画一次（这一层每 3 秒跑一趟）。
 */
function patch(list: ChatMsg[], ups: NonNullable<ChatLog['updates']>): ChatMsg[] {
  const by = new Map(ups.filter((u) => u.ref).map((u) => [u.ref, u]))
  let hit = false
  const out = list.map((m) => {
    const u = m.ref ? by.get(m.ref) : undefined
    if (!u) return m
    // 已经打过了就别再造一个新对象（同一条补丁会跟着后面每一批重复送来吗？不会 ——
    // 补丁只在「结果那一行」被读到的那一批里出现一次。但整份重读时会连消息一起重来，
    // 那时候消息本身已经带着结果了，这儿就成了空打）
    const okSame = u.ok === undefined || m.ok === u.ok
    const ans = u.answers
    const needAsk = !!(ans && m.ask?.questions.some((q) => !q.picked && ans[q.question]))
    if (okSame && !needAsk) return m
    hit = true
    return {
      ...m,
      ok: u.ok ?? m.ok,
      ask: ans && m.ask
        ? {
          questions: m.ask.questions.map((q) => (
            !q.picked && ans[q.question] ? { ...q, picked: ans[q.question] } : q
          )),
        }
        : m.ask,
    }
  })
  return hit ? out : list
}

/**
 * 把连续的工具调用并成一串。
 *
 * 为什么：一次 turn 里十几条工具调用是常态（实测扫下来 CommandExecution 比 AgentMessage
 * 多三倍），一条一行铺开的话，**人真正要看的那几句话被挤出屏幕**。所以一串只占一行、
 * 显示最新那条，点开才展开 —— 和「工具只送摘要不送输出」是同一个取舍的延伸。
 *
 * 只并**连续**的：中间夹着正文或思考就断开，那是两次不同的活。
 */
type Row =
  | { kind: 'msg'; msg: ChatMsg }
  | { kind: 'tools'; key: string; items: ChatMsg[] }

function group(msgs: ChatMsg[]): Row[] {
  const out: Row[] = []
  for (const m of msgs) {
    const last = out[out.length - 1]
    // 「agent 在问你」那条**不并进工具串**：它是要人看、要人答的东西，折起来就等于藏了。
    if (m.kind === 'tool' && m.ask) {
      out.push({ kind: 'msg', msg: m })
      continue
    }
    if (m.kind === 'tool') {
      if (last?.kind === 'tools') {
        last.items.push(m)
        continue
      }
      // key 用**这一串里第一条**的 id，不是最后一条：串还在增长时最后一条一直在变，
      // 拿它当 key 的话每来一条工具调用就把「我刚点开这一串」丢掉一次。
      out.push({ kind: 'tools', key: m.id, items: [m] })
      continue
    }
    out.push({ kind: 'msg', msg: m })
  }
  return out
}

/**
 * 「运行中…」那一行，照着 claude 自己那条状态行来
 * （`✳ Ebbing… (3m 40s · ↓ 10.3k tokens · thinking with xhigh effort)`）。
 *
 * 要它是因为转录按「一次 API 请求」flush，agent 想事情时文件十几秒不动（实测 15.58 秒）——
 * 那段静止里这一行是唯一的活物，而「跑了多久」正是那时候最想知道的事。
 *
 * **秒数在本地往前走，但基准是服务端给的。** 不能在前端拿时间戳减 `Date.now()`：
 * 那个时间戳是跑 agent 那台机器写的，手机时钟差几分钟是常事，减出来是个看着像真的错数字。
 * 所以服务端给「已经跑了几秒」，这儿每秒加一，下一拍来了再对齐回去。
 */
/*
  「投出去了但这条没被处理」这行字**试过又撤掉了**，记一下免得再做一遍：

  判据本身是能算的（最后一条是人话 ＋ 不在 working ＋ 这一轮 0 token），但它在**正常**
  情形下也成立：人连投几条、最后一条还没轮到处理时就是这个样子。于是屏幕上多一句
  「这条还没被处理（按过 Esc 就是被打断了，否则是它还没开始）」—— 用户的反馈是
  「不明所以」，而且那句话还把两种原因都摊出来，等于没说。

  更根本的是：**投出去又按 Esc，消息本身并没有被撤回**。它以 `type:"user"` 实实在在写进了
  转录（真机上核过），claude 自己的 TUI 也照旧留着它 —— 所以气泡留着是对的，不需要解释。
  真·被打断那种（打断在工具调用 / 输出中途）转录里有 `[Request interrupted by user]`，
  那条我们已经画成一行小字了（见 internal/transcript 的 claudeSay）。

  结论：**空着比编一句好**（和「几分钟前」那列同一条规矩）。
*/
function Running({ status, turn, shells }: {
  status: string
  turn?: ChatLog['turn']
  shells?: number
}) {
  const working = status === 'working'
  // 在跑时秒数在本地往前走；跑完了就用服务端算的那一轮时长（`secs` 那时还在涨 ——
  // 它算到「现在」，拿它显示就成了「跑了 20 分钟」，而其实人只是二十分钟没再说话）
  const live = useTick(working ? turn?.secs : undefined)
  const bits: string[] = []
  const secs = working ? live : (turn?.ran ?? 0)
  if (secs > 0) bits.push(dur(secs))
  if (!working && turn?.doneAt) bits.push(clock(turn.doneAt))
  // **这段是不是已经旧了**。服务端算的秒数（见 lib/api.ts 的 idle）。
  //
  // 两分钟以内不说 —— 刚跑完就挂一句「0 分钟前」是噪音。够旧了才有信息量，而最需要它的
  // 是 codex 的 `/clear`：那一下**不新建 rollout、旧文件也不再长**，herdr 的会话 id 要
  // 等新会话写盘才更新，于是这段窗口里 chat 显示的是**已经作废的上一段对话**（用户报的
  // 「停留到 clear 之前的内容」）。数据层认不出那件事（没有结束标记），所以把「最后更新
  // 是多久以前」摆出来让人自己判断 —— 说下一句话它就自己跟到新会话上了。
  if (!working && (turn?.idle ?? 0) >= STALE_SEC) bits.push(`${sinceWord(turn!.idle!)}没更新`)
  if (turn?.tokens) bits.push(`↓ ${kilo(turn.tokens)} tokens`)
  if (working && turn?.effort) bits.push(`${turn.effort} effort`)
  // 还在跑的后台任务 —— 跑完之后这条最要紧（「它停了，但还有东西在后台跑」）
  if (shells) bits.push(`${shells} 个后台任务运行中`)

  // 闲着 + 什么都算不出来：整行不画（别留一行空括号）
  if (!working && !bits.length) return null

  return (
    <p className="flex items-center gap-1.5 text-xs text-faint">
      <span className={cn('size-1.5 shrink-0 rounded-full',
        working ? 'animate-pulse bg-warn' : 'bg-muted')} />
      {working && <span className="shrink-0 text-warn">运行中…</span>}
      {/* 算不出来（这一轮太长、回扫窗口里找不到起点）就只有状态词 ——
          「空着比编一个数好」，和面板一览那列「几分钟前」同一条规矩 */}
      {bits.length > 0 && <span className="min-w-0 truncate font-mono text-[11.5px]">({bits.join(' · ')})</span>}
    </p>
  )
}

/** 多久没更新了才值得说一句。刚跑完挂一句「0 分钟前」是噪音 */
const STALE_SEC = 120

/**
 * 秒数 → `12 分钟`/`3 小时`/`2 天`。
 *
 * **只给一档精度**：这是「旧不旧」的量级判断，不是计时 —— `12 分钟 30 秒前` 里那个秒
 * 一个字的信息都没有，还占宽度（这一行在手机上本来就要 truncate）。
 */
function sinceWord(s: number) {
  const m = Math.round(s / 60)
  if (m < 60) return `${m} 分钟`
  const h = Math.round(m / 60)
  return h < 24 ? `${h} 小时` : `${Math.round(h / 24)} 天`
}

/**
 * RFC3339 → `14:41`。
 *
 * **按看页面这台设备的时区格式化**：服务端给的是绝对时刻，而「几点完的」这件事人是按自己
 * 手上的钟来理解的。这儿不做差，所以手机时钟偏了也不影响它（和那个秒数正相反 ——
 * 那个必须服务端算，见 internal/transcript/turn.go）。
 */
function clock(iso: string) {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')} 完成`
}

/**
 * 把服务端给的秒数在本地往前走。
 *
 * 每拍（3 秒）会对齐一次，所以不会越走越偏；`base` 变了就重新起算。
 * 停掉计时器的条件是 `base` 变成 undefined（agent 不跑了）—— 那时整行都不画了。
 */
function useTick(base?: number) {
  const [n, setN] = useState(base ?? 0)
  useEffect(() => {
    setN(base ?? 0)
    if (base === undefined) return
    const t = window.setInterval(() => setN((x) => x + 1), 1000)
    return () => clearInterval(t)
  }, [base])
  return n
}

/** `3m 40s` / `45s` / `1h 2m`。和 claude 自己那条状态行一个写法 */
function dur(s: number) {
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m ${s % 60}s`
  return `${Math.floor(m / 60)}h ${m % 60}m`
}

/** `10.3k` / `980`。token 数在手机上占不起五位数的宽度 */
function kilo(n: number) {
  if (n < 1000) return String(n)
  return `${(n / 1000).toFixed(1)}k`
}

/** 长路径缩成 ~/…：家目录前缀在每一条里都是同一段（和改动面板那边同一个 short） */
const shortPath = (p: string) => (p || '').replace(/^\/(?:Users|home)\/[^/]+/, '~')

/**
 * 头上那个状态：一个点 + 一句话。
 *
 * 颜色用共用那份（`lib/agentstatus.ts`）—— 面板一览那一列点是同一套语义
 * （红 = 等你，绿 = 跑完了，黄 = 在跑，灰 = 闲着），两份平行的色表迟早对不上。
 */
const STATUS_WORD: Record<string, string> = {
  blocked: '在等你回答',
  working: '运行中',
  done: '跑完了',
  idle: '闲着',
}

/**
 * 把模型 id 变成人看的名字。规则是照**真转录里的值**定的，不是猜的：
 *
 *	claude-opus-5             → Opus 5
 *	claude-haiku-4-5-20251001 → Haiku 4.5
 *	gpt-6-sol                 → GPT 6 Sol
 *	gpt-5.6-luna              → GPT 5.6 Luna
 *
 * 认不出的形状**原样给出去**（截断留给 CSS）—— 模型 id 是别人定的，会一直变，
 * 硬套规则不如把原文摆出来。
 */
export function modelName(raw?: string): string {
  if (!raw) return ''
  return raw
    .replace(/-\d{6,}$/, '')        // 尾巴上那串日期（claude 的 -20251001）
    .replace(/^claude-/, '')         // 「是哪家」由名字本身说，不用再挂个前缀
    .replace(/(\d)-(\d)/g, '$1.$2') // 4-5 是 4.5，不是两段
    .split('-')
    .map((w) => (/^gpt$/i.test(w) ? 'GPT' : w.charAt(0).toUpperCase() + w.slice(1)))
    .join(' ')
}

/**
 * 那个药丸上写什么：**三档常规状态写模型名，只有「在等你」写中文**。
 *
 * 用户点名的，理由成立：灰 / 黄 / 绿三档光看颜色就分得清（闲着 / 在跑 / 完成），那三个
 * 中文词等于把一格宽度花在颜色已经说过的话上；而「我这会儿跟哪个模型说话」原来根本没地方
 * 看（左边那个 `claude` / `codex` 小标只说得出是哪家，说不出是 Opus 5 还是 Haiku）。
 *
 * **红的那档例外**：「等你回答」是要人动手的，它得是一句话，不能靠颜色去猜 —— 而且那时候
 * 模型名正是最不重要的信息。
 *
 * 拿不到模型名（转录里还没出现过 model、或者那一轮回扫算不出来）就**退回状态词** ——
 * 空着一个药丸比写个没用的词更糟，而这条退路顺带保住了老转录。
 */
function Status({ status, model, agent }: { status: string; model?: string; agent?: string }) {
  const dot = STATUS_DOT[status]
  // 认不出的状态（herdr 哪天加了一档）就什么都不画 —— 编一个词出来比空着糟。
  if (!dot) return null
  const word = STATUS_WORD[status] ?? status
  const name = modelName(model)
  const showModel = status !== 'blocked' && !!name
  return (
    <span
      className={cn(
        'flex shrink-0 items-center gap-1 rounded-md border px-1.5 py-0.5 text-xs',
        status === 'blocked' ? 'border-bad/50 bg-bad/15 text-bad'
          : status === 'working' ? 'border-warn/40 bg-warn/12 text-warn'
            : 'border-line bg-ctl text-muted',
      )}
      // 药丸上只剩模型名时，状态那句话挪到 title 里（桌面上悬停看得到）
      title={status === 'blocked'
        ? '有东西在等你回答 —— 审批和选择框都在终端里，点右边那个按钮过去'
        : [word, model, agent].filter(Boolean).join(' · ')}
    >
      <span className={cn('size-1.5 rounded-full', dot, status === 'working' && 'animate-pulse')} />
      <span className={cn('truncate', showModel && 'max-w-32 font-mono')}>{showModel ? name : word}</span>
    </span>
  )
}

/**
 * 首次进一个 pane 时那片骨架。
 *
 * 按对话的实际样子摆：一条人话（靠右、窄）、一条 agent 的（靠左、宽）、一条工具行（细长）。
 * 目的只有一个 —— **别让那一下读起来像「内容没了」**。宽度写死成几档而不是随机，
 * 免得每次渲染都换个样子（那会看着像在抖）。
 */
function Skeleton() {
  const rows: [string, boolean][] = [
    ['w-[45%]', true], ['w-[78%]', false], ['w-[60%]', false],
    ['w-[35%]', true], ['w-[70%]', false],
  ]
  return (
    <div className="flex flex-col gap-2 py-3" aria-hidden>
      {rows.map(([w, mine], i) => (
        <div key={i} className={cn('flex', mine ? 'justify-end lg:justify-start' : 'justify-start')}>
          <div className={cn('h-12 animate-pulse rounded-card bg-ctl/70', w, !mine && 'lg:w-full')} />
        </div>
      ))}
    </div>
  )
}

/**
 * 读不到时顶上那条细带（手上已经有对话的情况）。
 *
 * 一次抖动不该把人正在读的东西清掉 —— 它只是「这一拍没读到」，下一拍多半就好了。
 */
function Strip({ msg }: { msg: string }) {
  return (
    <p className="flex items-center gap-1.5 rounded-md border border-line bg-ctl/60 px-2 py-1 text-xs text-muted">
      <AlertCircle className="size-3.5 shrink-0 text-faint" />
      <span className="min-w-0 truncate" title={msg}>这一拍没读到：{msg}</span>
    </p>
  )
}

/**
 * 焦点 pane 里没有 agent 时那一屏。
 *
 * **要给一条明路回终端。** 原来这儿只有一行小字 + 右上角那个 ×，而这一屏是铺满的 ——
 * 人看到的是「一整块空白盖着我的终端」，那个 × 又不显眼，等于死胡同（用户报的）。
 *
 * **不自动退出 chat 模式**：那是他明确要过的「常驻」性质 —— 点一下 shell pane 就把模式
 * 关掉的话，回到 agent 还得再开一次。所以给按钮，走不走由人定。
 */
/** 这一屏上开得出来的 agent。和服务端那张白名单（`startable`）是同一件事的两半 */
const STARTABLE = ['claude', 'codex'] as const

/**
 * 「这个 pane 里没有 agent」那一屏。
 *
 * 除了「回到终端」，这儿还能**直接开一个 agent**（往那个 pane 里敲 `claude` / `codex`
 * 加回车）—— 用户报的：焦点落在一个 shell pane 上时，这一屏原来只剩一条退路。
 *
 * # 「开」这件事有三条
 *
 * ① **不走终端那条 WebSocket**，走服务端的 `/chat/start`（herdr 的 `pane.send_input`）。
 *    chat 在终端断着时照旧能用（左上角那个状态点就是为这个才分开说的），按钮跟着终端连接
 *    一起失效说不通。
 * ② **点下去必须当场有话，而且绝不停在「正在开…」上。** 这一屏在手机上是整块空白，
 *    「点了没反应」是这个项目里反复出现的那一类（CLAUDE.md 那条：反馈离手指太远）。
 *    所以失败就在按钮底下写一行红字（不发 toast —— 那个贴在整屏最下沿，而眼睛在屏幕正中），
 *    而且**成功之后还有一道兜底**：agent 起不来 / herdr 半天不报，`WAIT_MS` 到了就说清楚
 *    「敲下去了，但这个 pane 里还是没检测到」，而不是无限转。
 * ③ 起来之后这一屏**自己就没了** —— `active` 是从 App 那份 pane 列表推的（3 秒一拍），
 *    herdr 一报 `agent` 这个组件就整个卸掉。所以这儿不需要自己去轮，也别自己去猜。
 */
function Nobody({
  pane, onClose, onStart,
}: {
  pane?: string
  onClose: () => void
  /** 开一个 agent。失败时 throw（错误原文画在这一屏上） */
  onStart?: (agent: 'claude' | 'codex') => Promise<void>
}) {
  /** 正在开哪个（空 = 没在开）。成功之后一直挂着，直到这一屏被卸掉（见 ③） */
  const [busy, setBusy] = useState<string | null>(null)
  const [bad, setBad] = useState('')
  /** 敲下去了但 agent 迟迟没出现（见 ②） */
  const [slow, setSlow] = useState(false)

  // 换 pane 了就整个复位 —— 这一屏不会因为换 pane 卸掉（另一个 shell pane 照旧是这一屏），
  // 不复位的话上一个 pane 的「正在开…」会挂在新 pane 头上。
  useEffect(() => {
    setBusy(null)
    setBad('')
    setSlow(false)
  }, [pane])

  useEffect(() => {
    if (!busy) return
    const t = window.setTimeout(() => setSlow(true), WAIT_MS)
    return () => clearTimeout(t)
  }, [busy])

  const go = async (agent: 'claude' | 'codex') => {
    if (!onStart || busy) return
    setBad('')
    setSlow(false)
    setBusy(agent)
    try {
      await onStart(agent)
    } catch (e) {
      // 连不上 / herdr 不在 / 那个 pane 里已经有 agent 了，都落在这儿。
      // **要把 busy 收掉**，不然按钮停在「正在开…」上再也点不动（②）。
      setBusy(null)
      setBad(e instanceof Error ? e.message : String(e))
    }
  }

  return (
    <div className="flex h-full flex-col items-center justify-center gap-3 px-6 text-center">
      <p className="text-[13px]/relaxed text-muted">
        当前这个 pane{pane ? `（${pane}）` : ''}里没有跑着的 agent。
      </p>
      <p className="text-xs/relaxed text-faint">
        chat 看的永远是焦点那个 pane —— 从「面板一览」挑一个 agent，它会跟着切过去。
      </p>

      {onStart && pane && (
        <div className="mt-1 flex flex-wrap items-center justify-center gap-2">
          {STARTABLE.map((a) => (
            <button
              key={a}
              type="button"
              disabled={!!busy}
              onClick={() => void go(a)}
              title={`在 ${pane} 里敲 ${a} 并回车`}
              // disabled:opacity-100：唯一要人看见的就是「正在开…」这一下，
              // 按钮基类那个 disabled:opacity-45 正好把它压暗（savebutton 那条同理）
              className="flex items-center gap-1.5 rounded-md border border-brand/40 bg-brand/12
                         px-3 py-1.5 text-[13px] text-brand disabled:opacity-100"
            >
              <Play className="size-3.5" />
              {busy === a ? `正在开 ${a}…` : `开 ${a}`}
            </button>
          ))}
        </div>
      )}

      {bad && <p className="max-w-[320px] text-xs/relaxed text-bad">{bad}</p>}
      {busy && !bad && (
        <p className="max-w-[320px] text-xs/relaxed text-faint">
          {slow
            ? `${busy} 已经敲下去了，但这个 pane 里还是没检测到 agent —— 回终端看一眼它起没起来。`
            : '敲下去了，起来之后这一屏自己就变成对话了。'}
        </p>
      )}

      <button
        type="button"
        onClick={onClose}
        className="mt-1 flex items-center gap-1.5 rounded-md border border-line bg-ctl
                   px-3 py-1.5 text-[13px] text-fg"
      >
        <Terminal className="size-3.5" />
        回到终端
      </button>
    </div>
  )
}

/**
 * 读不出来时说清是哪一种。
 *
 * 判据用的是服务端给的 `reason`，**不是错误文案** —— 按文案 `includes()` 判断改一个字
 * 就静默失效（见 lib/api.ts 的 ApiError）。
 */
function Problem({ err, agent }: { err: { msg: string; reason?: string }; agent?: string }) {
  const a = agent === 'codex' ? 'codex' : 'claude'
  /*
    **还没对话**：会话身份有了，文件还没有 —— agent 要等你说第一句才开始写盘。这是空状态，
    不是故障：不画警告图标、不印那串 session id（原来是「找不到会话 670b5857-… 的转录文件」，
    看着像坏了）。说完第一句下一拍就读到了，所以这里只告诉人「在下面说话就行」。
  */
  if (err.reason === 'no_transcript') {
    return (
      <div className="flex flex-col items-center gap-2 py-16 text-center text-xs/relaxed text-muted">
        <MessageSquare className="size-6 text-faint" />
        <p className="text-[13px] text-fg">还没有对话</p>
        <p>在下面说第一句话，{a === 'codex' ? 'codex' : 'claude'} 回你之后这里就会出现。</p>
      </div>
    )
  }
  return (
    <div className="flex flex-col gap-2 py-6 text-xs/relaxed text-muted">
      <div className="flex items-start gap-2">
        <AlertCircle className="mt-0.5 size-4 shrink-0 text-faint" />
        <p className="text-fg">{err.msg}</p>
      </div>
      {err.reason === 'need_install' && (
        <div className="ml-6 flex flex-col gap-1.5">
          <p>
            herdr 还不知道这个 pane 里那个会话是哪一份。它是
            <strong className="text-fg">装 integration 之后开的 agent</strong> 才会自己报上来的：
          </p>
          <code className="rounded border border-line bg-ctl px-2 py-1 font-mono text-[11.5px] text-fg">
            herdr integration install {a}
          </code>
          <p>
            装过了还是这句话的话，说明这个 agent 是装之前起来的 —— 那个 hook 只在 agent
            <strong className="text-fg">启动那一下</strong>报一次，把它重开一次就好了。
          </p>
        </div>
      )}
      {err.reason === 'ambiguous' && (
        <p className="ml-6">
          这个目录下同时开着好几个跑同一个 agent 的 pane，而 herdr 还没拿到会话身份 ——
          这时候只能靠目录猜，而猜错的表现是<strong className="text-fg">显示的是隔壁那个 pane 的对话</strong>
          （两边都在同一个项目里干活，屏幕上看着完全正常）。所以这儿宁可不猜。装上
          <code className="mx-1 rounded border border-line bg-ctl px-1.5 py-0.5 font-mono text-[11.5px] text-fg">
            herdr integration install {a}
          </code>
          再把 agent 重开一次就分得开了。
        </p>
      )}
    </div>
  )
}

/**
 * 「agent 在问你」那张卡（claude 的 `AskUserQuestion`）。
 *
 * 为什么要它：屏幕上一条 `AskUserQuestion …` 等于什么都没说 —— 被问住的时候人缺的恰恰是
 * **有几个选项、第二个是什么**。而那份负载就在转录里（见 internal/transcript 的 askOf）。
 *
 * # 先选、再提交（多题 / 多选都支持）
 *
 * 选择只在本地攒着，点「提交」才一次发出去。**没有二次确认**：原来点选项要「点两下」，
 * 理由是那串 ↓ ×n 假设「高亮此刻停在第一个选项上」—— 人先在终端里按过方向键就会选错。
 * 现在按的是**选项序号**（实测和高亮位置无关，见 chatapi.go 的 askKeys），那个前提整个
 * 没了；而这儿发出去的就是屏幕上摆着的这几个勾，没选完还按不动，再加一道就是纯多按一下。
 *
 * 那套按键协议是拿真 claude 在隔离的 tmux 里逐键量出来的（单选发序号会自动跳题、多选是
 * 切换要自己 `tab`、最后在 Submit 页发 `1`、而单题单选那种序号本身就提交了），
 * 全写在 `internal/server/chatapi.go` 的 `askKeys` 上 —— **这儿不重复那套逻辑**，
 * 前端只送「每题选了哪几个」。
 *
 * 服务端那三道照旧：这个口只发得出那套序列、按 pane 寻址不走焦点、发之前从**转录**核一遍
 * 「此刻真有一个没答的提问」（不是看 `agent_status` —— 实测那个在开着选择器时报 idle）。
 *
 * 答过的那张卡只显示不给点（对着答过的提问再发一遍就是往输入框里打字）。**「在等你答」
 * 和「这儿能不能代答」是两个判据** —— 混成一个的后果见 `footer`。
 */
function AskCard({ m, onAnswer, live }: {
  m: ChatMsg
  onAnswer?: (picks: number[][], other: string[]) => void
  live?: boolean
}) {
  const qs = m.ask?.questions ?? []
  /** 还没提交、只在本地攒着的选择：每题一串选项下标 */
  const [picks, setPicks] = useState<number[][]>(() => qs.map(() => []))
  /**
   * 每题「自己写」那一格的字（TUI 列表里的 `Type something.`）。
   *
   * 单选题里它和选项是**同一个单选框里的两行**：写了字就把选项清掉、点了选项就把字清掉 ——
   * 服务端也这么核（两样都给报 400），而 TUI 里本来就只能落在一行上。多选题是并存的。
   */
  const [others, setOthers] = useState<string[]>(() => qs.map(() => ''))
  const wrote = (qi: number) => !!others[qi]?.trim()
  /**
   * 提交键的三档：`idle` → 点下去 `sending`（请求在路上）→ 成功后 `sent`，**一直锁到这张卡
   * 答完**（转录里出现回答、`live` 变 false，提交键整个不画了）。
   *
   * 为什么成功了还不放开：请求回来只说明键按下去了，转录要等下一拍（最慢 3 秒）才显示
   * 「答过了」—— 原来那一段里键又变回一个一模一样的「提交」，看着像没点到（用户报的），
   * 再点一次就是往一个已经答完的界面上再灌一串数字键，落进 agent 的输入框里。
   * 兜底 `SENT_UNLOCK` 之后放开：键真没落上（对面卡住了）时人得有办法重来。
   */
  const [phase, setPhase] = useState<'idle' | 'sending' | 'sent'>('idle')
  const sending = phase !== 'idle'
  /** 挡同一帧里的连点：setState 还没生效时第二下 click 看到的 phase 还是 idle */
  const inflight = useRef(false)
  const [bad, setBad] = useState('')
  const canPick = !!(live && onAnswer)
  useEffect(() => {
    if (phase !== 'sent') return
    const t = setTimeout(() => setPhase('idle'), SENT_UNLOCK)
    return () => clearTimeout(t)
  }, [phase])
  /** 当时选了哪几个（多题的话每题一个，用 / 连起来）。空 = 拿不到 */
  const chosen = qs.map((q) => q.picked).filter(Boolean).join(' / ')
  /**
   * 还有哪几题没选（1-based 题号）。
   *
   * **这个必须显示出来。** 提交的判据是「每题都得有选择」（服务端也这么核，发一半会停在
   * 一个半填的选择器上），而手机上第二题常常**在屏幕外面** —— 用户报的正是这个：第一题
   * 4 个全勾上了，提交键却按不动，而屏幕上没有任何一句话说为什么。
   * 所以除了下面那句「还有 N 题没选」，每题头上还画一个 `☐`/`☑`（和 TUI 那条标签栏
   * 同一个办法：`☐ 关注方面 ☒ 确认方式`），一眼能看出缺的是哪一题。
   */
  const missing = qs.map((_, qi) => qi).filter((qi) => !picks[qi]?.length && !wrote(qi))
  const ready = qs.length > 0 && missing.length === 0

  const toggle = (qi: number, oi: number, multi?: boolean) => {
    if (!multi) setOthers((o) => o.map((t, i) => (i === qi ? '' : t)))
    setPicks((prev) => pick(prev, qi, oi, multi))
  }
  const pick = (prev: number[][], qi: number, oi: number, multi?: boolean) => {
    const next = prev.map((a) => [...a])
    if (!multi) next[qi] = [oi]
    // 多选是**切换**（和 TUI 里一致）：再点一下取消
    else if (next[qi].includes(oi)) next[qi] = next[qi].filter((x) => x !== oi)
    else next[qi] = [...next[qi], oi].sort((a, b) => a - b)
    return next
  }

  const write = (qi: number, text: string, multi?: boolean) => {
    setOthers((o) => o.map((t, i) => (i === qi ? text : t)))
    if (!multi && text.trim()) setPicks((p) => p.map((a, i) => (i === qi ? [] : a)))
  }

  return (
    <div className="flex flex-col gap-2 rounded-card border border-brand/40 bg-brand/10 px-3 py-2.5">
      {qs.map((q, qi) => (
        <div key={qi} className="flex flex-col gap-1.5">
          <span className="text-[0.9em] text-brand">
            {canPick && <span className={cn('mr-1', picks[qi]?.length || wrote(qi) ? 'text-brand' : 'text-faint')}>
              {picks[qi]?.length || wrote(qi) ? '☑' : '☐'}
            </span>}
            {q.header || `第 ${qi + 1} 问`}{q.multi ? '（可多选）' : ''}
          </span>
          <p className="text-[1em]/relaxed text-fg">{q.question}</p>
          <div className="flex flex-col gap-1">
            {q.options.map((o, oi) => {
              const at = `${qi}:${oi}`
              const on = picks[qi]?.includes(oi)
              // 答过的那张卡要把**当时选的那个**标出来：不标的话往上翻历史看到的是一排
              // 干巴巴的选项，「当时到底定了哪个」还得回终端翻（用户报的）
              const chose = !!q.picked && q.picked.split(', ').includes(o.label)
              const row = (
                <>
                  <span className="shrink-0 font-mono text-[0.85em] text-faint">{oi + 1}</span>
                  <span className="min-w-0">
                    <span className="text-[1em] text-fg">{o.label}</span>
                    {o.description && (
                      <span className="block text-[0.9em] text-muted">{o.description}</span>
                    )}
                  </span>
                </>
              )
              // 不能点的时候画成一行静态的：别画一个点了没反应的按钮
              // （「点开一片报错比没有这个入口更糟」同一条道理）
              if (!canPick) {
                return (
                  <div
                    key={at}
                    className={cn(
                      'flex items-start gap-1.5 rounded-md border px-2 py-1',
                      chose
                        ? 'border-brand/40 bg-brand/12'
                        // **在等你答的时候别压暗**：那时候这几个选项正是你要读的东西
                        // （你在终端里答，照着这儿看）。压暗只留给答过的历史 ——
                        // 那时候压暗是为了让「选了哪个」一眼跳出来。
                        : live
                          ? 'border-line bg-ctl'
                          : 'border-line bg-ctl/60 opacity-60',
                    )}
                  >
                    {row}
                  </div>
                )
              }
              return (
                <button
                  key={at}
                  type="button"
                  disabled={sending}
                  onClick={() => toggle(qi, oi, q.multi)}
                  title={q.multi ? '点一下勾上／取消' : '点一下选它'}
                  className={cn(
                    'flex items-start gap-1.5 rounded-md border px-2 py-1 text-left disabled:opacity-100',
                    // 选中态是「淡绿底 + 绿边」，不是涂满 —— 涂满留给这张卡上唯一的
                    // 主操作（那个提交键），见配色那节。
                    // **不再额外画一个 ✓**（用户点名去掉的）：底色和边框已经说完了「选了」这件事，
                    // 右边再顶一个勾只是噪音，而且在窄屏上会把选项文字挤窄一截。
                    on ? 'border-brand/40 bg-brand/12' : 'border-line bg-ctl hover:bg-ctl-hi',
                  )}
                >
                  {row}
                </button>
              )
            })}
            {canPick && (q.multi || q.options.length + 1 <= 9) && (
              /*
                「自己写」那一格（TUI 里的 `Type something.`）。按下去的键在服务端算（askKeys：
                单选按 n+1 进编辑态、多选要把光标挪过去 —— 两种走法完全不同），这儿只送字。
                **回车不提交**：手机输入法上回车常常是「换行 / 完成」，写到一半就替人答出去了；
                服务端也会把换行换成空格。
              */
              <label
                className={cn(
                  'flex items-center gap-1.5 rounded-md border px-2 py-1',
                  wrote(qi) ? 'border-brand/40 bg-brand/12' : 'border-line bg-ctl',
                )}
              >
                <span className="shrink-0 font-mono text-[0.85em] text-faint">{q.options.length + 1}</span>
                <input
                  type="text"
                  value={others[qi] ?? ''}
                  disabled={sending}
                  onChange={(e) => write(qi, e.target.value, q.multi)}
                  onKeyDown={(e) => { if (e.key === 'Enter') e.preventDefault() }}
                  placeholder={q.multi ? '自己写（和上面勾的一起交）' : '自己写…'}
                  className="min-w-0 flex-1 bg-transparent text-[1em] text-fg outline-none placeholder:text-faint"
                />
              </label>
            )}
            {!canPick && customOf(q) && (
              // 答过的卡：自己写的那句不在选项里，单独画一行标出来（不画的话「当时写了什么」
              // 只剩底下那行小字）
              <div className="flex items-start gap-1.5 rounded-md border border-brand/40 bg-brand/12 px-2 py-1">
                <span className="shrink-0 text-[0.85em] text-faint">自己写</span>
                <span className="min-w-0 text-[1em] text-fg">{customOf(q)}</span>
              </div>
            )}
          </div>
        </div>
      ))}

      {canPick && (
        /*
          **提交不做二次确认。** 原来点选项要「点两下」，理由是那串 ↓×n 假设「高亮此刻停在
          第一个选项上」—— 人先在终端里按过方向键就会选错，所以要举一下再确认。
          现在按的是**选项序号**（实测和高亮位置无关，见 chatapi.go 的 askKeys），那个前提
          整个没了；而这儿发出去的就是屏幕上摆着的这几个勾，没选完还按不动。
          再要一道二次确认就是纯多按一下。
        */
        <>
        <div className="mt-0.5 flex flex-wrap items-center gap-2">
          <button
            type="button"
            disabled={!ready || sending}
            onClick={async () => {
              if (inflight.current) return
              inflight.current = true
              setBad('')
              setPhase('sending')
              try {
                await onAnswer?.(picks, others.map((t) => t.trim()))
                setPhase('sent')
              } catch (e) {
                // **失败要画在这张卡上**，不能只发 toast —— 那个贴在整屏最下沿，而眼睛在
                // 刚点的这个按钮上（CLAUDE.md 那条「反馈离手指太远」）。
                setBad(e instanceof Error ? e.message : String(e))
                setPhase('idle')
              } finally {
                inflight.current = false
              }
            }}
            className={cn(
              'inline-flex shrink-0 items-center gap-1.5 rounded-md border px-3 py-1 text-[1em] disabled:opacity-100',
              ready
                ? 'border-brand-line bg-brand-bg text-brand-fg'
                : 'border-line bg-ctl text-faint',
              // 锁着的那段稍微压一点：一眼看得出「现在点不了」，但别压到看不清转圈
              sending && 'opacity-80',
            )}
          >
            {sending && <LoaderCircle className="size-3.5 animate-spin" />}
            {phase === 'sending' ? '提交中…' : phase === 'sent' ? '已提交' : '提交'}
          </button>
          {/* 按不动的时候**就在按钮旁边**说为什么（见 missing 那段注释） */}
          {!ready && (
            <span className="text-[0.9em] text-warn">
              还有 {missing.length} 题没选{missing.length <= 3 ? `（第 ${missing.map((i) => i + 1).join('、')} 问）` : ''}
            </span>
          )}
        </div>
        {bad && <p className="text-[0.9em]/relaxed text-bad">{bad}</p>}
        </>
      )}

      <p className="text-[0.9em] text-faint">{footer(canPick, live, ready, chosen)}</p>
    </div>
  )
}

/**
 * ask 那张卡底下那行小字。
 *
 * 四种状态各说一句 —— 尤其是「答过了」那种要说清**选了哪个**：不说的话往上翻历史看到的
 * 是一排干巴巴的选项，「当时到底定了哪个」还得回终端翻（用户报的）。
 * 拿不到选项时（问题文案两处对不上）才退回那句干话，不编一个出来。
 *
 * **`live` 和 `canPick` 是两件事**：在等你答、但这儿代答不了（选项超过 9 个）时要说清
 * 「在等你答，回终端」，绝不能落到「已经答过了」那句上 —— 那会让人干脆不去答，而对面
 * 一直卡着（用户报的「我没有答啊 为什么说我答过了」）。
 */
/**
 * 答过的题里「自己写」的那部分：claude 记进转录的是 `勾的, 勾的, 自己写的`，把能对上选项的
 * 去掉，剩下的就是自己写的。（自己写的字里本身带 `, ` 会被切开再拼回去，不影响。）
 */
function customOf(q: { picked?: string; options: { label: string }[] }) {
  if (!q.picked) return ''
  const labels = new Set(q.options.map((o) => o.label))
  return q.picked.split(', ').filter((x) => !labels.has(x)).join(', ')
}

/** 提交成功后锁多久还没看到「答过了」就放开（见 AskCard 的 phase） */
const SENT_UNLOCK = 20_000

function footer(canPick: boolean, live: boolean | undefined, ready: boolean, chosen: string) {
  if (canPick) {
    // 缺哪几题由按钮旁边那行说（那儿离手指近），这儿只讲怎么用
    return ready ? '点「提交」就替你在终端里按下去' : '每题都要选（或者自己写一句），选完点「提交」'
  }
  if (live) return '选项太多，这儿发不了 —— 回终端答'
  return chosen ? `已选：${chosen}` : '这个问题已经答过了'
}

/**
 * 一串连续的工具调用。
 *
 * **多于一条就默认折起来，只显示最新那条**（用户要的）—— 串还在跑的时候那一行就跟着最新
 * 那条走，正好也是「它现在在干什么」。左边那个小钮上写着这一串一共几条，点开看全部。
 *
 * 显示**最新**那条而不是第一条：人看 chat 是想知道此刻在干什么，而不是这一串从哪儿起的。
 */
function ToolRun({ items, open, onToggle }: { items: ChatMsg[]; open: boolean; onToggle: () => void }) {
  if (items.length === 1) return <ToolLine m={items[0]} />
  return (
    <div className="flex flex-col gap-1">
      {open ? (
        items.map((m, i) => <ToolLine key={m.id} m={m} toggle={i === 0 ? { open, n: items.length, onToggle } : undefined} />)
      ) : (
        <ToolLine m={items[items.length - 1]} toggle={{ open, n: items.length, onToggle }} />
      )}
    </div>
  )
}

/**
 * 工具占**一行**，不是气泡：一次 turn 里十几条工具调用是常态，每条都做成气泡的话
 * 人要说的话就被挤出屏幕了。而这一行要回答的只有「它在干什么、成没成」。
 */
function ToolLine({ m, toggle }: { m: ChatMsg; toggle?: { open: boolean; n: number; onToggle: () => void } }) {
  return (
    <div className="flex items-center gap-1.5 text-[0.9em] text-muted">
      {toggle ? (
        <button
          type="button"
          onClick={toggle.onToggle}
          title={toggle.open ? '折起这一串工具调用' : `这一串一共 ${toggle.n} 条，点开看全部`}
          className="flex shrink-0 items-center gap-0.5 rounded border border-line bg-ctl px-1
                     font-mono text-[0.85em] text-muted hover:bg-ctl-hi hover:text-fg"
        >
          {toggle.open ? <ChevronUp className="size-3" /> : <ChevronDown className="size-3" />}
          {toggle.n}
        </button>
      ) : (
        <Wrench className="size-3 shrink-0 text-faint" />
      )}
      <span className="shrink-0 font-mono text-[0.88em] text-fg">{m.tool}</span>
      {/* **不 flex-1**：原来参数这段撑满剩下的宽度，于是后面那个 ✓ 有参数时被推到行尾
          （宽屏上离工具名一两千像素），没参数时又贴着工具名 —— 同一列里忽左忽右（用户报的
          「这个 icon 感觉奇怪」）。现在它只占自己的长度、放不下才截，✓ 永远紧跟在这行字后面 */}
      {m.meta && <span className="min-w-0 truncate font-mono text-[0.88em] text-faint">{m.meta}</span>}
      {/* ok 是 undefined 就是「还不知道」（结果还没落盘）—— 那时画一个省略号，
          而不是画成成功。画成成功的话「正在跑」和「跑完了」看着一样。
          用和左边扳手同一套的线条图标，不用 ✓ / ✕ 字符（字形各字体不一、粗细对不上） */}
      {m.ok === undefined ? <span className="shrink-0 text-faint">…</span>
        : m.ok ? <Check className="size-3 shrink-0 text-brand" strokeWidth={2.5} aria-label="成功" />
          : <X className="size-3 shrink-0 text-bad" strokeWidth={2.5} aria-label="失败" />}
    </div>
  )
}

/** 一条。几种画法：人 / agent（气泡）/ 思考（铺开，暗一档 + 左竖线）/ 提示（一行小字）。工具那种见 ToolRun */
function Bubble({ m, onAnswer, live, onOpenPath }: {
  m: ChatMsg
  /** 点了第 index 个选项（已经过二次确认）。不给 = 这条 ask 只显示不给点 */
  onAnswer?: (picks: number[][], other: string[]) => void
  /** 这条 ask 是不是**还没答**（= 它是最后一条工具调用且没有结果）。只有它才给点 */
  live?: boolean
  /** 点了正文里一条本地路径（走终端那套 openPath） */
  onOpenPath?: (p: string) => void
}) {
  if (m.ask) return <AskCard m={m} onAnswer={onAnswer} live={live} />

  if (m.kind === 'notice') {
    return <p className="py-1 text-center text-[0.9em] text-faint">{m.text}</p>
  }

  if (m.kind === 'think') {
    /*
      思考**直接铺开**，当一条正常的消息读（用户点名的：「我需要看这个」）。原来折成一行
      「想了一会儿（点开看）」—— 理由是它比正文长、会把真正说给你听的那几句淹掉；可在手机上
      盯着 agent 干活时，思考正是「它此刻在想什么、打算怎么做」的那一段，折起来就只剩一行没用的字。
      和正文分开靠**暗一档的字 + 左边一条竖线**，不靠折叠；照样走 Markdown（思考里常有列表和代码）。
    */
    return (
      <div className="min-w-0 border-l-2 border-line pl-3 text-[0.92em]/relaxed text-muted">
        <Suspense fallback={<span className="whitespace-pre-wrap break-words">{m.text}</span>}>
          <Markdown text={m.text ?? ''} onPath={onOpenPath} />
        </Suspense>
      </div>
    )
  }

  const mine = m.kind === 'human'
  /*
   * **手机和宽屏两种排法**（用户点名的）：
   *
   *	手机    人话靠右、agent 靠左、都封顶 85% —— 窄屏上左右分开一眼就知道谁说的
   *	宽屏    **都靠左**，agent 的回复铺满整行（两边留白由外面那层的 lg:px-8 定），
   *	        人话按内容收缩、靠绿底和它分开。宽屏上还左右分的话，人话在屏幕最右、回复在最左，
   *	        读一轮对话眼睛要来回跨一整个屏宽；agent 气泡按内容收缩的话又长短参差，
   *	        一条三个字的回复旁边是一条铺满的表格，看着就乱（截图里那样）
   *
   * 断点用 lg（视口 ≥ 1024）不用 md：手机横屏 870 上下，还该是手机那一档
   */
  return (
    <div className={cn('flex', mine ? 'justify-end lg:justify-start' : 'justify-start')}>
      <div
        className={cn(
          'max-w-[85%] min-w-0 rounded-card px-3 py-2 text-[1em]/relaxed',
          !mine && 'lg:w-full lg:max-w-none',
          /*
           * 人说的话用**淡绿底 + 绿边**（`bg-brand/12 border-brand/40`，仓库里「打开 /
           * 选中态」的通用写法），**不是 `bg-brand-bg`** —— 那个 token 是主按钮那套饱和
           * 填充（`#1c6a47`），而配色那节写着它只留给「一屏一个的主操作」。
           * 拿它铺每一条人话的后果在真机截图上很明显：一屏几条绿块，比 agent 说的话还抢眼，
           * 而这儿要区分的只是「谁说的」。
           */
          mine
            ? 'border border-brand/40 bg-brand/12 text-fg'
            : 'border border-line bg-ctl text-fg',
        )}
      >
        {/* 兜底先按纯文本画（chunk 还在路上、或者拉不下来时），别让气泡空着 */}
        <Suspense fallback={<span className="whitespace-pre-wrap break-words">{m.text}</span>}>
          <Markdown text={m.text ?? ''} onPath={onOpenPath} />
        </Suspense>
      </div>
    </div>
  )
}
