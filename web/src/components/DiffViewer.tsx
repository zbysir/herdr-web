import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { ChevronDown, ChevronRight, Copy, FileText, UnfoldVertical, WrapText, X } from 'lucide-react'
import { gitApi, lineText, type DiffFile, type DiffLine, type DiffMode, type GitChange, type GitPatch, type GitRepo } from '@/lib/api'
import { diffWrap, pushPref } from '@/lib/prefs'
import { writeClipboard } from '@/lib/clipboard'
import { usePhone } from '@/hooks/usePhone'
import { Button } from './ui/button'
import { ChangeRow } from './DiffRow'
import { cn } from '@/lib/utils'

/**
 * 补丁页：**这一次改动的全部文件是一条连续的流**，从 a 一直滑到 b，不用退回清单再点一次。
 *
 * 一开始这儿是「一次看一个文件」，用户报的就是这个：「多个文件改动没办法一次性看完」。
 * review 一次改动天然是**顺序**的（19 个文件从头看到尾），而退出→再点一次是每个文件两次
 * 交互 + 一次上下文丢失。
 *
 * 铺满整屏（不是浮层）—— 和看图一样，手机上这东西值一整块屏幕。
 *
 * # 四件终端里做不到的事
 *
 *  1. **折行**（`wrap`）。默认折：40 列的屏上不折就得横滑，而横滑正是终端那份的毛病。
 *     宽屏上关掉更好读（缩进对得齐，一眼看得出层级），所以这个开关**跟着排布那一套走**
 *     （手机一套、电脑一套，见 lib/prefs.ts）。
 *  2. **按词高亮**：服务端把配得上对的删除 / 新增行按字符求了公共前后缀，只有真正变了的
 *     那截是深色底（见 internal/gitdiff/parse.go）。
 *  3. **段头粘在顶上**：滚下去几百行之后还知道自己在哪个函数里。
 *  4. **顶栏那行跟着滚**：滚到哪个文件，上面就写哪个文件（`第 3 / 19`）。所以文件之间
 *     那条分隔带**不用**再 sticky —— 两层 sticky 要手工对齐高度，对不齐就是重叠或者一道缝，
 *     而「我现在在哪个文件」这个问题顶栏已经答了。
 *
 * # 为什么是「滚到哪儿读到哪儿」，不是一次全读回来
 *
 * 一次改动 19 个文件、几千行是常事，而**每个文件的补丁是跑一次 git**（在用户那台正跑着
 * agent 的机器上）。所以：每个文件先占一块**按 `+n −m` 估出来的高度**，滚到跟前
 * （`IntersectionObserver`，往下多探 900px）才去读，而且**一次只读一个**（顺着看的顺序），
 * 不给那台机器同时 fork 十几个 git。
 *
 * 由此来的一条必须做的补偿：占位块和真内容高度不一样，**在视口上面的那一块变高会把下面
 * 的内容整体推下去**（读的人正看着的地方突然往下跳）。所以每次替换前记一下它的位置，
 * 替换完发现它整块都在视口上面，就把 `scrollTop` 补上那个差值 —— 补完屏幕上什么都没动。
 * Safari 那边不能指望浏览器自己的 scroll anchoring（`overflow-anchor` 它不支持）。
 */

/** 估高用的行高（px）。只为占位，不用和真实行高严格一致 —— 差一点由上面那条补偿兜着 */
const EST_LINE = 18
/** 占位块的上下限：一个文件改一行也别缩成一条缝，改两千行也别撑出一屏又一屏的空白 */
const EST_MIN = 60
const EST_MAX = 2400

const estHeight = (c: GitChange) =>
  Math.max(EST_MIN, Math.min(EST_MAX, (c.add + c.del + 6) * EST_LINE))

export function DiffViewer({
  repo, mode, changes, startPath, profile, onClose, onOpenFile, toast,
}: {
  repo: GitRepo
  mode: DiffMode
  /** 这一档的**全部**改动，顺序就是清单上的顺序 —— 这条流按它铺 */
  changes: GitChange[]
  /** 从哪个文件开始看（清单上点的那个）。别的文件在它上下，滑过去就是 */
  startPath: string
  /** 当前这套排布的 ID：折行那个开关存在它名下 */
  profile: string
  onClose: () => void
  /** 「打开整个文件」：给的是仓库相对路径，拼绝对路径的事交给上层（它手上有 root） */
  onOpenFile: (path: string) => void
  toast: (m: string) => void
}) {
  // 未跟踪的**目录**没有补丁可看（git 把整个新目录折成一条），不进这条流
  const files = useMemo(() => changes.filter((c) => !c.dir), [changes])
  const startIdx = useMemo(
    () => Math.max(0, files.findIndex((c) => c.path === startPath)),
    [files, startPath],
  )

  const [wrap, setWrap] = useState(diffWrap)
  /** 上下文行数。3 是 git 的默认；10 那一档是「这块改动前后到底是什么」 */
  const [ctx, setCtx] = useState(3)
  /** 滚到哪个文件了（顶栏那行写它，几个按钮也作用在它身上） */
  const [cur, setCur] = useState(startIdx)
  /** 每个文件单独要过更多行时的上限（「再多给一些」）。进缓存键，所以一改就会重读 */
  const [limits, setLimits] = useState<Record<string, number>>({})
  const [got, setGot] = useState<Record<string, GitPatch>>({})
  const [errs, setErrs] = useState<Record<string, string>>({})
  /** 折起来的文件（点分隔带那一下）：大文件挡路时一下跳过去 */
  const [fold, setFold] = useState<Record<string, boolean>>({})
  /** 哪些文件已经滚到跟前了（该读了）。键是下标 */
  const [want, setWant] = useState<Record<number, boolean>>({ [startIdx]: true })
  /** 队列的心跳：每读完一件加一，好让下一件接上（见下面 finally 那段） */
  const [pulse, setPulse] = useState(0)
  /** 索引开着没有（顶栏那个「第几/共几」点开的） */
  const [index, setIndex] = useState(false)

  const scroller = useRef<HTMLDivElement>(null)
  const secs = useRef<(HTMLElement | null)[]>([])
  const busy = useRef(false)
  /**
   * 「要落在哪个文件上」的锚。**开进来那一下对不齐**，所以要留着这个：
   *
   * 点清单里最后那个文件时，它下面没有内容了 —— `scrollIntoView` 想把它顶到最上面，
   * 但滚动条已经到底，浏览器只能把它停在半屏下面（**滚动位置被夹住了**）。等上面那个
   * 大文件读回来、内容长出来之后，位置才够，可那时候没人再对齐一次 —— 屏幕上就是
   * 「点了第二个文件，过一会看的却是第一个」（用户报的）。
   *
   * 所以：钉住它，等它自己读回来（或者读失败）之后再对齐一次，然后松开。
   */
  const anchor = useRef<number | null>(null)
  /** 上一次滚动是不是我们自己弄的（自己弄的不算「人在滚」，不该把锚松掉） */
  const selfScroll = useRef(false)
  const tail = useRef<HTMLDivElement>(null)
  /**
   * 替换占位块之前记下的位置，给下面那条滚动补偿用。
   *
   * 是个**队列**不是一个格子：两块内容有可能落在同一次渲染里（读得快的时候真会撞上），
   * 一个格子的话后来的把先来的挤掉，那一份高度差就没人补 —— 表现是屏幕偶尔往下挪一小截，
   * 而且只在读得快的时候出现，最难查的那种。
   */
  const swaps = useRef<{ el: HTMLElement; top: number; h: number }[]>([])

  const keyOf = useCallback((c: GitChange) => `${c.path}@${limits[c.path] ?? 0}`, [limits])

  /**
   * 把末尾那块空白撑到「**最后一个文件也能顶到屏幕最上面**」。
   *
   * 不撑的话，点清单里最后那个文件会落成这样：`scrollIntoView` 想把它顶上去，但它下面
   * 只剩几十像素、滚动条已经到底 —— 浏览器只能把它停在半屏以下，于是屏幕上大半还是
   * **上一个**文件的尾巴（用户报的「点第二个进去，看到的却是第一个」）。
   *
   * 只补差额（不是一律留一屏空白）：最后那个文件本来就比一屏长时，这儿就是 0。
   * 直接写 DOM 而不是走 state：它得在**同一次 layout 里**先生效，紧接着那下对齐才不会
   * 又被夹住 —— 走 state 要等下一次渲染，顺序就反了。
   */
  const fitTail = useCallback(() => {
    const root = scroller.current
    const last = secs.current[files.length - 1]
    if (!root || !last || !tail.current) return
    const need = Math.max(64, Math.round(root.clientHeight - last.getBoundingClientRect().height))
    if (tail.current.offsetHeight !== need) tail.current.style.height = `${need}px`
  }, [files.length])

  // 焦点接过来，Esc 才是**这个弹窗的**按键（不接的话焦点还在 xterm 那个隐藏 textarea
  // 上，中文输入法和全屏状态会各吃掉一下，表现是「要按两下」）。理由同 FileViewer。
  const shell = useRef<HTMLDivElement>(null)
  const phone = usePhone()
  const phoneRef = useRef(phone)
  phoneRef.current = phone
  useEffect(() => {
    const prev = document.activeElement as HTMLElement | null
    shell.current?.focus()
    return () => { if (!phoneRef.current && prev?.isConnected) prev.focus() }
  }, [])

  // 开的时候直接落到点的那个文件上。上面那些文件是占位块（高度是估的），所以这一下
  // 落点不会精确到像素 —— 但它自己就在视口顶上，读回来之后是往下长，不会顶着人跳。
  useEffect(() => {
    fitTail()
    if (startIdx > 0) {
      anchor.current = startIdx
      selfScroll.current = true
      secs.current[startIdx]?.scrollIntoView({ block: 'start' })
    }
    scan() // 第一屏该读哪几个也得自己算一次（没有 observer 替我们发第一枪了）
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  /** 「代」：上下文行数一换，在途那份就是旧的了（结果回来时按它判，别写进新一代里） */
  const gen = useRef(0)
  /**
   * 上下文行数一变，读回来的那些全作废（每个文件都要重新问一次 git）。
   *
   * **挂载那一下不算换档**（`first`）：React 严格模式下 effect 会跑两遍，白涨两代，
   * 而第一个请求带的是第一代 —— 回来时对不上，结果被自己扔掉。表现是「开进来停在那儿
   * 永远读取中，一滚才开始读」。
   */
  const first = useRef(true)
  useEffect(() => {
    if (first.current) { first.current = false; return }
    gen.current++
    setGot({}); setErrs({})
  }, [ctx])

  /**
   * 扫一趟：**该读哪几个**（滚到跟前的）+ **滚到哪个文件了**（顶栏那行）。
   *
   * 两件事一起算，因为它们要的是同一批 `getBoundingClientRect`。
   *
   * # 为什么不是 IntersectionObserver + rAF
   *
   * 因为这两样在**后台标签页里根本不跑**（Chrome 不渲染时没有「渲染时机」，IO 的回调和
   * rAF 一起停）。这条路上真会撞见：手机上切出去接个电话、锁屏再回来，都可能停在半路 ——
   * 而它俩停下来的样子是「滚了半天什么都不加载、顶栏那行也不动」，看着就像坏了。
   * 用滚动事件 + 时间节流的话，页面一回到前台，下一次滚动就把状态补齐了。
   * （这条坑 CLAUDE.md 里记着，本来就是这个项目吃过两次的那个。）
   *
   * 往下多探 900px（顺着读的方向提前拿），往上只探 300px —— 往上翻多半是回去看一眼，不急。
   */
  const scan = useCallback(() => {
    const root = scroller.current
    if (!root) return
    const rr = root.getBoundingClientRect()
    const h = root.clientHeight
    let at = -1
    const near: number[] = []
    for (let i = 0; i < files.length; i++) {
      const el = secs.current[i]
      if (!el) continue
      const r = el.getBoundingClientRect()
      const top = r.top - rr.top
      const bottom = r.bottom - rr.top
      if (bottom > -300 && top < h + 900) near.push(i)
      // 顶栏那行写的是「上边缘已经滚过去、但还没滚完」的那个文件
      if (at < 0 && bottom > 48) at = i
    }
    if (at >= 0) setCur((c) => (c === at ? c : at))
    setWant((w) => {
      let next = w
      for (const i of near) {
        if (!next[i]) {
          if (next === w) next = { ...w }
          next[i] = true
        }
      }
      return next
    })
  }, [files])

  /** 滚动事件按时间节流（80ms），末尾补一次 —— 停下来那一下的位置才是要的 */
  const last = useRef(0)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const onScroll = useCallback(() => {
    // 自己弄出来的那一下不算「人在滚」—— 不然刚设的锚立刻被自己松掉
    if (selfScroll.current) selfScroll.current = false
    else anchor.current = null
    const now = Date.now()
    if (now - last.current < 80) {
      clearTimeout(timer.current)
      timer.current = setTimeout(onScroll, 80)
      return
    }
    last.current = now
    scan()
  }, [scan])
  useEffect(() => () => clearTimeout(timer.current), [])

  /**
   * 读队列：**一次只读一个**，按下标从小到大（也就是从上往下、顺着人读的方向）。
   *
   * 不并发是刻意的：每个文件都是对面那台机器上一次 `git diff`，而那台机器上正跑着 agent。
   * 十几个一起 fork 换来的那点速度，不值得跟别人抢 CPU 和 git 的锁。
   */
  useEffect(() => {
    if (busy.current) return
    /*
     * 挑下一件：**从正看着的那个开始，由近及远**（同样远近时下面的先来 —— 顺着读的方向）。
     *
     * 原来是「从上往下第一个没读的」，那在「点清单里第 8 个文件」时正好最糟：人已经落在
     * 第 8 个上面盯着「读取中…」，而队列在从第 1 个开始慢慢读。
     */
    const center = anchor.current ?? cur
    let i = -1
    let best = Infinity
    for (let k = 0; k < files.length; k++) {
      if (!want[k]) continue
      const kk = keyOf(files[k])
      if (got[kk] || errs[kk]) continue
      const d = Math.abs(k - center) * 2 + (k < center ? 1 : 0)
      if (d < best) { best = d; i = k }
    }
    if (i < 0) return
    const c = files[i]
    const k = keyOf(c)
    const my = gen.current
    busy.current = true
    void gitApi.diff({
      dir: repo.root, mode, path: c.path, old: c.old,
      untracked: c.kind === 'untracked', context: ctx, limit: limits[c.path],
    }).then(
      (p) => {
        if (my !== gen.current) return // 中途改了上下文行数，这份是旧的
        // 替换之前记一下这块在哪儿 —— 见上面那条滚动补偿
        const el = secs.current[i]
        if (el) { const r = el.getBoundingClientRect(); swaps.current.push({ el, top: r.top, h: r.height }) }
        setGot((g) => ({ ...g, [k]: p }))
      },
      (e: Error) => { if (my === gen.current) setErrs((x) => ({ ...x, [k]: e.message })) },
    ).finally(() => {
      busy.current = false
      // **一件干完就去看下一件**：别指望「依赖恰好又变了一次」来推队列 —— 结果被丢掉
      // （换了档）、或者人停在那儿不滚时，依赖根本不会再变，队列就那么停住了。
      setPulse((n) => n + 1)
    })
    // **这儿不能返回 cleanup 去掐掉在途的那一个**：这个 effect 的依赖里有 want / got，
    // 而它们每滚一段就变一次 —— 「重跑就作废」写下去的表现是每个请求都在半路被自己人
    // 掐掉，一个文件都读不出来（就这么坏过一次，屏幕上是所有文件永远「读取中…」）。
  }, [pulse, cur, want, got, errs, files, keyOf, limits, ctx, mode, repo.root])

  /**
   * 滚动补偿：刚换上去那块要是**整块都在视口上面**，它长高会把下面的内容整体推下去 ——
   * 正在读的地方当场跳一截。把差值补回 scrollTop，屏幕上就什么都没动。
   */
  useLayoutEffect(() => {
    const list = swaps.current
    swaps.current = []
    if (list.length === 0 || !scroller.current) return
    fitTail()
    // 判据是**这一块的上边缘在不在可视区上面**，而且要和**滚动容器的上边**比 ——
    // 不是和窗口顶上比。上面还有一条顶栏（48px 左右），拿 0 当界的话，「刚好被顶栏
    // 挡住的那一块」会被判成「还在视口里」而不补 —— 那正是用户报的那一下：
    // 两个文件，点第二个进去，第一个读回来之后屏幕整个被顶下去，看的变成了第一个。
    const top = scroller.current.getBoundingClientRect().top
    let delta = 0
    for (const s of list) {
      if (s.top >= top - 1) continue // 从可视区里开始的：内容往下长，不影响正在读的位置
      delta += s.el.getBoundingClientRect().height - s.h
    }
    if (delta === 0) return
    selfScroll.current = true
    scroller.current.scrollTop += delta
  }, [got, fitTail])

  /**
   * 锚：**每读回来一块就把它重新顶到最上面，直到这一阵读完为止**。
   *
   * 为什么不是「它自己读回来就对齐一次然后松手」：那样只对了一半。周围那几块是紧接着
   * 读回来的，每一块都会改上面那截的高度，而上面那条补偿是按「记下来的位置 + 新高度」
   * 一块一块补的 —— 占位块的估高、亚像素、以及滚到两头时被夹住，都会让它差一点点。
   * 差的那点会攒起来：实测「点清单里第 8 个」落成了「第 8 个的上面 300px」，稳定复现。
   *
   * 所以干脆盯到底：队列里还有它周围没读完的就不松手，每次内容一变就重新对齐。
   * 这一阵一般不到半秒，而人只要自己滚一下就把锚松掉了（见 onScroll），不会跟人抢。
   */
  useLayoutEffect(() => {
    const i = anchor.current
    if (i === null) return
    const c = files[i]
    if (!c) { anchor.current = null; return }
    const k = keyOf(c)
    if (!got[k] && !errs[k]) return // 它自己还没回来，先不动（那时候对齐的是个占位块）
    fitTail() // 先撑够余量，这一下对齐才不会又被夹在半屏以下
    selfScroll.current = true
    secs.current[i]?.scrollIntoView({ block: 'start' })
    setCur(i)
    const pending = files.some((f, kk) => want[kk] && !got[keyOf(f)] && !errs[keyOf(f)])
    if (!pending) anchor.current = null
  }, [got, errs, want, files, keyOf, fitTail])

  // 读进来一块之后再扫一次：内容长高了、下面又露出新的一块，而**人并没有滚**，
  // 滚动事件不会来。少这一下的表现是「停在那儿不动就只读进来一个文件」。
  useEffect(() => { scan() }, [got, scan])

  const here = files[cur] ?? files[0]

  /**
   * 跳到某个文件。**跳完要自己扫一遍**：位置是瞬间变的，没有滚动事件，
   * 不扫的话落点那一片没人去读（屏幕上停在「读取中…」不动）。
   */
  const jump = (i: number) => {
    // 和开进来那一下同一个道理：跳到最后一个文件时滚动位置会被夹住，
    // 等它读回来之后要再对齐一次（见 anchor）
    anchor.current = i
    fitTail()
    selfScroll.current = true
    secs.current[i]?.scrollIntoView({ block: 'start' })
    setCur(i)
    scan()
  }

  const flipWrap = () => {
    const v = !wrap
    setWrap(v)
    pushPref(profile, 'diffWrap', v ? '1' : '0', toast)
  }

  const copyPath = async () => {
    if (!here) return
    const p = `${repo.root}/${here.path}`
    if (await writeClipboard(p)) toast('路径已复制')
    else toast('复制不了，长按上面那行路径自己选')
  }

  const more = (c: GitChange) => setLimits((l) => ({
    ...l,
    [c.path]: Math.min(((got[keyOf(c)]?.limit ?? 2000) * 5), 20000),
  }))

  return (
    <div
      ref={shell}
      tabIndex={-1}
      role="dialog"
      aria-modal="true"
      aria-label={here?.path ?? 'diff'}
      onKeyDown={(e) => {
        if (e.key === 'Escape' && !e.nativeEvent.isComposing) {
          e.preventDefault(); e.stopPropagation(); onClose()
        }
      }}
      className="absolute inset-0 z-20 flex flex-col bg-bg outline-none"
      data-testid="diff-viewer"
    >
      <div className="flex shrink-0 items-center gap-1 border-b border-line bg-bar px-2 py-2">
        <div className="min-w-0 flex-1 pl-1">
          <div className="truncate text-sm font-medium tracking-tight">{here?.path.split('/').pop()}</div>
          {/* 「第几个 / 一共几个」是这条流里唯一的方位感 —— 没有它，滚了半天不知道还剩多少 */}
          <div className="flex min-w-0 items-center gap-1 font-mono text-[11px] text-muted">
            {/* 「第几 / 共几」点得动：一条流顺着滑是主路，但**想直接跳某个文件**时不该
                退回清单再点一次（那正是这条流要省掉的那一步）。索引和面板里那份清单
                是同一个行渲染（DiffRow），认得出的那一行就是点过的那一行。 */}
            <button
              className="shrink-0 rounded px-1 text-faint hover:bg-ctl hover:text-fg"
              title="跳到别的文件"
              onClick={() => setIndex((v) => !v)}
            >
              {cur + 1}/{files.length} ⌄
            </button>
            <span className="min-w-0 flex-1 truncate select-text" title={here ? `${repo.root}/${here.path}` : ''}>
              {here?.old && <span className="text-faint">{here.old} → </span>}
              {here?.path}
            </span>
          </div>
        </div>
        <Button variant="ghost" size="icon" on={wrap} onClick={flipWrap}
          title={wrap ? '长行折着（点一下改成横滑）。这个选择记在「排布」里' : '长行横滑（点一下改成折行）。这个选择记在「排布」里'}>
          <WrapText className="size-4" />
        </Button>
        <Button variant="ghost" size="icon" on={ctx > 3} onClick={() => setCtx(ctx > 3 ? 3 : 10)}
          title={ctx > 3 ? '前后多给了 10 行；点一下回到 3 行' : '前后各多给几行（改动周围到底是什么）'}>
          <UnfoldVertical className="size-4" />
        </Button>
        <Button variant="ghost" size="icon" title="复制这个文件的完整路径" onClick={() => void copyPath()}>
          <Copy className="size-4" />
        </Button>
        <Button variant="ghost" size="icon" title="打开整个文件（不是 diff）"
          disabled={!here} onClick={() => here && onOpenFile(here.path)}>
          <FileText className="size-4" />
        </Button>
        <Button variant="ghost" size="icon" aria-label="关闭" onClick={onClose}>
          <X className="size-4" />
        </Button>
      </div>

      {index && (
        // 盖在流上面的一张索引。**开着它不动流**：跳过去之后关掉，位置还在那儿。
        // 点外面任何地方就收（和弹出组同一个道理，见 KeyGroupPopup）
        <>
          <div className="absolute inset-0 z-[25]" onClick={() => setIndex(false)} />
          <div className="absolute inset-x-2 top-14 z-30 max-h-[60%] overflow-auto overscroll-contain
                          rounded-card border border-line bg-bar p-2 shadow-[0_24px_60px_-16px_rgba(0,0,0,.75)]
                          md:inset-x-auto md:right-2 md:w-[420px]">
            <ul className="flex flex-col gap-px">
              {files.map((c, i) => (
                <ChangeRow
                  key={c.kind + c.path}
                  c={c}
                  mode={mode}
                  on={i === cur}
                  onPick={() => { setIndex(false); jump(i) }}
                />
              ))}
            </ul>
          </div>
        </>
      )}

      {/* 不折行时靠这一层横滑；折行时它只管竖着滚 */}
      <div
        ref={scroller}
        onScroll={onScroll}
        className={cn('min-h-0 flex-1 overflow-auto overscroll-contain', !wrap && 'overflow-x-auto')}
      >
        {files.map((c, i) => {
          const k = keyOf(c)
          const p = got[k]
          const folded = !!fold[c.path]
          return (
            <section key={c.path} data-i={i} ref={(el) => { secs.current[i] = el }}>
              <Band c={c} folded={folded} onFold={() => setFold((f) => ({ ...f, [c.path]: !folded }))} />
              {folded ? null
                : errs[k] ? <Note bad>{errs[k]}</Note>
                  : p ? p.files.map((f, j) => (
                    <FileBlock key={j} f={f} wrap={wrap} over={p.over} onMore={() => more(c)} />
                  ))
                    : <Hold h={estHeight(c)} reading={!!want[i]} />}
            </section>
          )
        })}
        {files.length === 0 && <Note>这一档里没有能看的文件。</Note>}
        {/* 末尾那一截：高度是算出来的（见 fitTail）—— 它撑着「最后一个文件也能顶到最上面」 */}
        <div ref={tail} style={{ height: 64 }} />
      </div>
    </div>
  )
}

/**
 * 文件之间那条分隔带。**不 sticky**（「我在哪个文件」由顶栏那行答，见组件头上的注释），
 * 但点一下能把这个文件折起来 —— 一份两千行的锁文件挡在中间时，这是唯一的绕法。
 */
function Band({ c, folded, onFold }: { c: GitChange; folded: boolean; onFold: () => void }) {
  const dir = c.path.replace(/\/?[^/]+\/?$/, '')
  const name = c.path.split('/').pop()
  return (
    <button
      onClick={onFold}
      title={folded ? '展开这个文件' : '折起这个文件'}
      className="sticky left-0 flex w-full items-center gap-2 border-y border-line bg-bar px-2 py-1.5 text-left hover:bg-ctl"
    >
      {folded ? <ChevronRight className="size-3.5 shrink-0 text-faint" /> : <ChevronDown className="size-3.5 shrink-0 text-faint" />}
      <span className="min-w-0 flex-1 truncate font-mono text-[11px]">
        {dir && <span className="text-faint">{dir}/</span>}
        <span className="text-fg">{name}</span>
        {c.old && <span className="text-faint"> ← {c.old}</span>}
      </span>
      {c.binary ? (
        <span className="shrink-0 text-[11px] text-faint">二进制</span>
      ) : (
        <span className="shrink-0 text-[11px] tabular-nums">
          {!!c.add && <span className="text-ok">+{c.add}</span>}
          {!!c.add && !!c.del && ' '}
          {!!c.del && <span className="text-bad">−{c.del}</span>}
        </span>
      )}
    </button>
  )
}

/**
 * 还没读回来的那一块：按 `+n −m` 估一个高度占着。
 *
 * 占位**必须有高度** —— 全是零高的话滚动条一路缩到底，人一滑就飞过好几个文件，
 * 而且每读回来一个，下面的内容就往下跳一大截。
 */
function Hold({ h, reading }: { h: number; reading: boolean }) {
  return (
    <div style={{ height: h }} className="px-3 py-2 text-xs text-faint">
      {reading ? '读取中…' : ''}
    </div>
  )
}

function FileBlock({ f, wrap, over, onMore }: { f: DiffFile; wrap: boolean; over?: boolean; onMore: () => void }) {
  const empty = f.hunks.every((h) => h.lines.length === 0)
  return (
    <div>
      {f.mode && <Note>权限位：{f.mode}</Note>}
      {f.binary && <Note>二进制文件 —— 页面里不给看内容。用上面那个「打开整个文件」下载下来。</Note>}
      {!f.binary && empty && (
        <Note>{f.kind === 'rename' ? '只改了名字，内容一个字都没动。' : '没有内容变化。'}</Note>
      )}
      {f.hunks.map((h, i) => (
        <section key={i}>
          {/* 粘在顶上：滚下去几百行之后，`@@` 后面那截（多半是函数名）是唯一能认位置的东西 */}
          <div className="sticky top-0 z-[2] flex gap-2 border-y border-line bg-bar px-2 py-1 font-mono text-[11px] text-faint">
            <span className="shrink-0">@@ −{h.os},{h.ol} +{h.ns},{h.nl}</span>
            {h.head && <span className="truncate text-muted">{h.head}</span>}
          </div>
          {h.lines.map((l, k) => <Row key={k} l={l} wrap={wrap} />)}
        </section>
      ))}
      {!!f.cut && (
        <div className="flex items-center gap-2 px-2 py-3">
          {/* 截断必须说出来 —— 不说的话「就改了这么多」是句假话 */}
          <span className="text-[11px] text-warn">还有 {f.cut} 行没显示{over ? '（这份补丁太大，读到上限就停了）' : ''}。</span>
          {!over && <Button size="tiny" onClick={onMore}>再多给一些</Button>}
        </div>
      )}
    </div>
  )
}

/**
 * 一行。三段：行号槽 / 正负号 / 正文。
 *
 * 不折行时整行要能超出容器宽度（外面那层横滑），所以那时候行是 `w-max min-w-full`、
 * 正文不参与 flex 伸缩；行号槽 `sticky left-0` 钉住 —— 横滑到第 200 列时还看得见行号，
 * 不然滑出去就不知道自己在哪一行了。每一行都给一个**实底色**（上下文行是 bg-bg），
 * 因为那个 sticky 的行号槽是靠 `bg-inherit` 遮住底下滑过去的正文的。
 */
function Row({ l, wrap }: { l: DiffLine; wrap: boolean }) {
  if (l.t === '\\') {
    // `\ No newline at end of file` —— 不是内容，别按代码画
    return <div className="px-2 py-0.5 font-mono text-[10px] text-faint">文件末尾没有换行</div>
  }
  const add = l.t === '+'
  const del = l.t === '-'
  return (
    <div
      className={cn(
        'flex font-mono text-xs leading-[1.5] [tab-size:4]',
        add ? 'bg-ok/10' : del ? 'bg-bad/10' : 'bg-bg',
        !wrap && 'w-max min-w-full',
      )}
    >
      <span className="sticky left-0 w-9 shrink-0 select-none bg-inherit pr-1.5 text-right text-[10px] leading-[inherit] text-faint tabular-nums">
        {del ? l.o : l.n}
      </span>
      <span className={cn('w-3 shrink-0 select-none text-center', add ? 'text-ok' : del ? 'text-bad' : 'text-faint')}>
        {add ? '+' : del ? '−' : ''}
      </span>
      <span
        className={cn(
          'select-text pr-2 text-fg',
          // break-words 而不是 break-all：先在空格处折，实在折不动的长串（URL、
          // 长标识符）才从中间切开。break-all 会把 `creghtmodel.CouponStatus`
          // 拦腰断成两截，读起来比横滑还费劲
          wrap ? 'min-w-0 flex-1 whitespace-pre-wrap break-words' : 'whitespace-pre',
        )}
      >
        {l.segs
          ? l.segs.map((s, i) => (
            s.eq
              ? <span key={i}>{s.s}</span>
              // 真正变了的那截：加深一档。**只在配对成功的行上有**（服务端很保守，
              // 两条毫不相干的行不会被配上，见 internal/gitdiff 的 markWords）
              : <span key={i} className={cn('rounded-[2px]', add ? 'bg-ok/30' : 'bg-bad/30')}>{s.s}</span>
          ))
          : lineText(l)}
      </span>
    </div>
  )
}

function Note({ children, bad }: { children: React.ReactNode; bad?: boolean }) {
  return <p className={cn('px-3 py-2 text-xs/relaxed', bad ? 'text-bad' : 'text-muted')}>{children}</p>
}
