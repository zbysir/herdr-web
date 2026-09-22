// 发件箱的状态机。
//
// 「这是我自己写的」和「远端现在是什么」必须分成两个东西：
// 一开始只用「文本 !== 上次对齐的文本」判断草稿，结果开着「双向」时草稿被推到远端
// 之后，对齐文本就等于草稿本身，于是草稿看起来「没改过」→ 解锁目标 → 下一拍把用户
// 正在写的东西直接覆盖掉。所以 own 单独负责所有权，synced 只负责发现远端变化。
import { useCallback, useEffect, useRef, useState } from 'react'
import { api, FOLLOW, type DraftResult, type GotoResult, type Pane, type PresetGroup, type SayResult, type SyncResult, type UploadResult } from '@/lib/api'

const HIST_KEY = 'composeHist'
const HIST_MAX = 30

export interface ComposeCfg { poll: number; push: number }

/** 刚投出去还没在转录里露面的那一条（chat 模式的乐观回显） */
export interface SentEcho {
  /** 服务端解析出来的真实 pane —— 「跟随焦点」那一档下它和你以为的不一定是同一个 */
  target: string
  text: string
  at: number
}

/** 最多留几条回显。同时挂着好几条「投递中」本身就说明出问题了，留多了只是噪音 */
const SENT_MAX = 8

export function useCompose(cfg: ComposeCfg, visible: boolean, live: boolean, toast: (m: string) => void) {
  const [text, setText] = useState('')
  const [panes, setPanes] = useState<Pane[]>([])
  // panes 的镜像：`jump` 是 useCallback，闭包里的 panes 会过期（而它的依赖里不能加 panes ——
  // 那会让「跳转」这个函数每拉一次列表就换一个身份，调用方那边全得跟着重建）
  const panesRef = useRef<Pane[]>([])
  panesRef.current = panes
  // 服务端在盯 agent 状态变化没有。盯着才有「3 分钟前」那一列（herdr 不给时间戳）
  const [watching, setWatching] = useState(false)
  const [presets, setPresets] = useState<PresetGroup[]>([])
  const [info, setInfo] = useState('')
  const [bad, setBad] = useState(false)
  const [busy, setBusy] = useState(false)

  // 这几个不参与渲染，用 ref：放进 state 会让轮询每拍都重建回调
  const own = useRef(false)          // 框里装的是**用户自己写的**东西
  const synced = useRef<string | null>(null) // 远端最后一次读到的文本
  const pinned = useRef('')          // 草稿归属的 pane
  const resolved = useRef('')        // 上一次轮询解析出来的真实 pane
  const inFlight = useRef(false)     // 有请求在飞时暂停轮询，免得自己追自己
  const textRef = useRef('')
  const pushTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const hist = useRef<string[]>([])
  const histIdx = useRef(-1)

  textRef.current = text

  useEffect(() => {
    try { hist.current = JSON.parse(localStorage.getItem(HIST_KEY) || '[]') } catch { /* 存坏了就算了 */ }
  }, [])

  const say2 = useCallback((msg: string, isBad = false) => { setInfo(msg); setBad(isBad) }, [])

  /**
   * 这段草稿到底该投给谁。
   *
   * 只有两种答案：**herdr 里此刻激活的那个**（哨兵值 FOLLOW，服务端在投的那一刻解析），
   * 或者**草稿锁定的那个**。发件箱缩成一行之后那个「投给哪个 pane」的下拉没了 —— 它的
   * 默认值本来就是 FOLLOW，而换目标去「面板一览」里点一下 pane 更实在：那边切的是 herdr
   * 自己的焦点，屏幕跟着一起过去，不像这边记一个目标那样和眼前看到的东西对不上。
   *
   * 「跟随焦点」不能一路跟到按下按钮那一刻：你为 A 写了一段话，中途焦点漂到了 B
   * （herdr 自己会因为 agent 状态变化换焦点），投出去就落到 B 了。所以**自己改过的
   * 草稿**会把目标锁定在当初瞄准的 pane 上，框空了才重新跟随。
   *
   * 判据是 own 而不是「框里有没有字」：自动拉回来还没动过的内容不算草稿，那时候切
   * pane 就该跟着换成新 pane 的内容。用「有没有字」当判据的话，只要框里有东西目标
   * 就被钉死，切 pane 后 input 再也不更新。
   */
  const aimed = useCallback(() => (pinned.current && own.current ? pinned.current : FOLLOW), [])

  const label = useCallback((r: SyncResult | SayResult | DraftResult) => {
    const cached = panes.find((p) => p.id === r.target)
    const where = cached ? `${cached.workspace}/${cached.tab}` : r.workspaceId
    return `${r.followed ? '⟳ ' : ''}${r.target}${where ? ` · ${where}` : ''} · ${r.agent ? `${r.agent} ${r.status}` : 'shell'}`
  }, [panes])

  /** 把远端内容放进框里：这是远端的东西，不是用户的草稿。 */
  const adopt = useCallback((t: string, pane: string) => {
    setText(t)
    textRef.current = t
    synced.current = t
    own.current = false
    if (pane) pinned.current = pane
  }, [])

  const loadPanes = useCallback(async (quiet = false) => {
    try {
      const r = await api.get<{ panes: Pane[]; watching?: boolean }>('/herdr/panes')
      setPanes(r.panes ?? [])
      setWatching(!!r.watching)
    } catch (e) {
      setPanes([])
      // socket 在跑 herdr server 的那台机器上，不一定是跑 herdr-web 的这台
      say2(`连不上 herdr：${(e as Error).message}`, true)
      if (!quiet) toast('连不上 herdr：' + (e as Error).message)
    }
  }, [say2, toast])

  /**
   * 跳到某个 pane：切焦点 +（可选）放大铺满。「面板一览」点一行走的就是这儿。
   *
   * 放在这个 hook 里，是因为 pane 列表和「上一次解析到的 pane」都在这儿 —— 跳完必须把
   * resolved 清掉，逼下一拍当成「切了 pane」处理，立刻把新 pane 输入框里的东西拉进来。
   * 不清的话轮询会觉得什么都没变，框里还挂着上一个 pane 的内容。
   *
   * 投稿目标不用动：默认那条「跟随 herdr 当前 pane」自己就跟过去了。本地有草稿时目标
   * 仍然锁在原来那个 pane 上 —— 那是对的，为 A 写的话不该因为你去 B 看了一眼就投给 B。
   */
  const jump1 = useCallback(async (id: string, zoom: boolean) => {
    try {
      const r = await api.post<GotoResult>('/herdr/goto', { target: id, zoom })
      resolved.current = ''
      /*
        **人自己切过去 = 把草稿的瞄准也交回「跟随焦点」。**

        一打第一个字，目标就被钉在那一刻的焦点 pane 上（见 onChangeText）。那一档是为了防
        **herdr 自己**把焦点飘走（agent 状态一变它就可能换焦点）—— 为 A 写的话不该因为焦点
        自己漂到 B 就投给 B。但**你亲手点着切过去**是明确的「我现在跟这个说话」，被那一档
        挡住的表现是：chat 里整屏已经是 B 的对话了，投出去却落在 A（用户报的「切了面板还是
        投错」）。而那个锁定在打字时**根本看不见** —— 那句「草稿锁在这个 pane 上」写在
        placeholder 里，而 placeholder 只在框空时显示。

        **只清 `pinned`，不动 `own`**：`own` 是「这段话是我自己写的」，清掉它下一拍就会把
        远端输入框的内容抄进来、把你的草稿盖掉。清了 `pinned` 之后 `aimed()` 回 FOLLOW，
        下一个字符会重新钉到**新**的那个 pane 上。
      */
      pinned.current = ''
      /*
        **抢跑：立刻把「投给谁」那行字换成新 pane，别等下一拍。**

        chat 那边有抢跑（按 pane id 读，不等 goto + 列表两次往返，见 App 的 focusHint），
        而这儿原来只清掉 `resolved`、等下一拍才重新显示 —— 于是有一小段时间「上面整屏已经是
        B 的对话，下面还写着 A」。用户报的就是这个时间差：两边口径不一致，人就不敢信它。

        **真正发到哪其实一直是对的**（`aimed()` 回 FOLLOW，服务端按 herdr 此刻的焦点解析，
        而 goto 返回时焦点已经换了）—— 差的只是这行字。所以这儿只补显示，不动别的。

        `resolved.current` 照旧清空（下面那句）：那是逼下一拍当成「切了 pane」处理、
        把新 pane 输入框里的东西拉回来的开关，不能省。
      */
      {
        const p = panesRef.current.find((x) => x.id === r.target)
        const where = p ? `${p.workspace}/${p.tab}` : ''
        say2(`⟳ ${r.target}${where ? ` · ${where}` : ''} · ${p?.agent ? `${p.agent} ${p.status}` : 'shell'}`)
      }
      void loadPanes(true)   // focused 标记变了
      return r
    } catch (e) {
      say2('跳转失败：' + (e as Error).message, true)
      toast('跳转失败：' + (e as Error).message)
      return null
    }
  }, [loadPanes, say2, toast])

  /**
   * 「跳转」要**排成一队发**，不能并发。
   *
   * goto 是一次 HTTP 请求，而 herdr 的焦点是「最后一跳说了算」—— 网络一卡，两次点击的
   * 请求就可能**乱序到达**：点 B 再点 A，如果 B 那一跳后到，herdr 的焦点最终停在 B。
   * 而 chat 那个抢跑提示两秒后就交还给「herdr 说焦点在谁」，于是屏幕**自己跳到 B**
   * （用户报的「没操作、等一会自动跳过去」，而且只在网络不好时出现）。
   *
   * 串起来之后到达顺序 == 点击顺序，**最后一次点击必然是最后一跳**。代价是连点几下时
   * 后面那几次要排队（每次一个往返），而那正是「谁说了算」需要的确定性。
   */
  const chain = useRef<Promise<unknown>>(Promise.resolve())

  const jump = useCallback(async (id: string, zoom: boolean) => {
    const mine = chain.current.then(() => jump1(id, zoom), () => jump1(id, zoom))
    chain.current = mine
    return mine
  }, [jump1])


  const loadSoftkeyPresets = useCallback(async () => {
    try {
      const r = await api.get<{ presets: PresetGroup[] }>('/softkeys')
      setPresets(r.presets ?? [])
    } catch { /* 预设拿不到不影响发件箱 */ }
  }, [])

  /** 一拍：谁是当前 pane、它输入框里是什么。 */
  const tick = useCallback(async () => {
    if (inFlight.current || !visible) return
    const target = aimed()
    let r: SyncResult
    try {
      r = await api.get<SyncResult>(`/herdr/sync?target=${encodeURIComponent(target)}`)
    } catch (e) {
      say2('herdr：' + (e as Error).message, true)
      return
    }
    // first：这是**开页后的第一拍**（还没解析过任何 pane）。它必然满足下面那个 switched，
    // 而那会儿列表刚拉过（openChat / 恢复那个 effect 里），所以不算「切换」——
    // 不分的话每次开页白拉一次 /herdr/panes（几十个 pane，在跑着 agent 那台机器上）。
    const first = !resolved.current
    const switched = r.target !== resolved.current
    resolved.current = r.target
    const pinNote = target === FOLLOW ? '' : ' · 草稿锁在这个 pane 上'
    // 认不出输入框时框里是空的，得说清是「没认出来」而不是「远端把框清空了」——
    // 不然看起来像自动拉回坏了。shell pane 单独说：那边本来就读不到输入行，
    // 但投稿走的是「盲打 + 回车」，照样能用，别让提示看起来像坏了。
    const boxNote = !r.noBox ? ''
      : r.agent ? ' · 认不出输入框（可能正开着全屏界面 / 选择框），这时候不会让你投'
                : ' · shell pane 读不到输入行（投稿照常可用）'

    if (switched) {
      /*
        **焦点换了就把 pane 列表一起刷新。**

        这一拍是整个前端唯一每拍都现问 herdr「焦点在哪」的地方（服务端 `resolve` 走
        `pane.current`）；而 `panes` 那份列表只在**事件**时才重拉（开 chat / 开面板 /
        自己点 goto）。人在**别的终端里**用 herdr 切了 pane 时一个事件都没有，于是两边
        对不上：发件箱这儿跟过去了，而 chat 用的是列表里那个旧的 `focused` 标记，头上
        还挂着上一个项目的对话（用户报的）。

        这不只是显示不一致 —— 投稿跟的是**这儿**解析出来的焦点，而人读的是 chat 那一屏，
        于是「看着 A 的对话，话发给了 B」。所以必须让两边同源。

        放在 `switched` 里面而不是每拍都拉：切 pane 是偶发的，而 `/herdr/panes` 是在跑着
        agent 的那台机器上问几十个 pane（实测 55 个），每拍白拉一次不值当。
      */
      if (!first) void loadPanes(true)
      // 焦点换了 pane：框里是远端来的就直接换成新 pane 的内容，是自己写的就留着
      if (own.current) say2(`${label(r)} · 本地有草稿，没自动拉回（清空框就跟回来）`)
      else { adopt(r.text ?? '', r.target); say2(`${label(r)}${boxNote}`) }
      return
    }
    if (!own.current && (r.text ?? '') !== synced.current) {
      adopt(r.text ?? '', r.target)
      say2(`${label(r)} · 已跟随远端改动${boxNote}`)
      return
    }
    say2(`${label(r)}${own.current ? ' · 本地草稿未投' : ''}${pinNote}${boxNote}`)
  }, [aimed, adopt, label, loadPanes, say2, visible])

  // 自动拉回的心跳。用自排队的 setTimeout 而不是 setInterval：一拍要打 3 次 socket
  // 调用，间隔调小或者网络一慢，setInterval 会把请求叠起来。
  useEffect(() => {
    if (!visible) return
    let stop = false
    let timer: ReturnType<typeof setTimeout>
    const loop = () => {
      timer = setTimeout(async () => {
        await tick()
        if (!stop) loop()
      }, cfg.poll)
    }
    loop()
    return () => { stop = true; clearTimeout(timer) }
  }, [visible, cfg.poll, tick])

  // 从别处切回这个页面 / 标签页时立刻对一次，别等下一拍
  useEffect(() => {
    const f = () => { void tick() }
    addEventListener('focus', f)
    const vis = () => { if (!document.hidden) void tick() }
    document.addEventListener('visibilitychange', vis)
    return () => { removeEventListener('focus', f); document.removeEventListener('visibilitychange', vis) }
  }, [tick])

  /** 双向同步的本地→远端那半边：停手一会儿后把草稿写进远端输入框（不回车）。 */
  const schedulePush = useCallback(() => {
    // 只推**用户自己写的**东西。自动拉回来的内容远端本来就有，推回去纯属多余，
    // 而且中间只要焦点动一下，就会把 A 的内容写进 B 的输入框。
    if (!live || !own.current) return
    clearTimeout(pushTimer.current)
    pushTimer.current = setTimeout(async () => {
      const body = textRef.current
      inFlight.current = true
      try {
        const r = await api.post<DraftResult>('/herdr/draft', { target: aimed(), text: body })
        if (r.skipped === 'not-agent') say2(`${label(r)} · 这个 pane 没有 agent 输入框，没往里推`)
        else if (r.skipped === 'no-box') say2(`${label(r)} · 认不出输入框，这次没推`)
        else if (r.skipped === 'busy') say2(`${label(r)} · 远端正忙，这次没推`)
        else { synced.current = body; pinned.current = r.target; say2(`${label(r)} · 已同步 ${r.pushed} 字到远端`) }
      } catch (e) {
        say2('同步失败：' + (e as Error).message, true)
      } finally {
        inFlight.current = false
      }
    }, cfg.push)
  }, [live, aimed, cfg.push, label, say2])

  const onChangeText = useCallback((v: string) => {
    setText(v)
    textRef.current = v
    own.current = !!v                                  // 框空了就把控制权交回「跟随焦点」
    if (own.current && !pinned.current && resolved.current) pinned.current = resolved.current
    schedulePush()
  }, [schedulePush])

  const pull = useCallback(async () => {
    setBusy(true)
    try {
      const r = await api.get<SyncResult>(`/herdr/pull?target=${encodeURIComponent(aimed())}`)
      resolved.current = r.target
      if (r.noBox) {
        // 认不出输入框就什么都别动：这时候的 '' 不代表「远端是空的」，拿它覆盖
        // 只会把用户正在写的东西白白删掉。
        pinned.current = r.target
        say2(`${label(r)} · ${r.agent ? '认不出输入框，没拉回（可能正开着全屏界面 / 选择框）' : 'shell pane 读不到输入行，没拉回'}`, true)
        return
      }
      adopt(r.text ?? '', r.target)
      // 手动点「拉回」是明确的意图：拿过来编辑。所以算用户的东西，锁定在这个 pane，
      // 别让下一次焦点变化把它冲掉。
      own.current = !!r.text
      say2(`${label(r)} · ${r.text ? `已拉回 ${[...r.text].length} 字` : '输入框是空的'}`)
    } catch (e) {
      say2('拉回失败：' + (e as Error).message, true)
    } finally {
      setBusy(false)
    }
  }, [aimed, adopt, label, say2])

  /**
   * 刚投出去的那几条（**给 chat 模式做乐观回显用的**）。
   *
   * 为什么要这个：投稿到它出现在 chat 里之间有几秒空窗 —— 投稿走 `agent.prompt`，agent 收到
   * 之后才把那一行写进转录，而我们是 3 秒轮一次。那几秒里对话流上一点动静都没有，
   * 看着像没投出去（用户报的）。所以投成功就先在本地摆一条「投递中」的气泡，
   * 等真的那一条从转录里读回来再撤掉。
   *
   * 存的是**投给了哪个 pane**（`target`，服务端解析后的真实 pane）+ 原文 + 时间：
   * chat 那边只回显「投给当前看着这个 pane」的，不然会在 A 的对话里看到投给 B 的话。
   */
  const [sent, setSent] = useState<SentEcho[]>([])

  /**
   * 已经挂上的图。**路径不进输入框，投稿那一刻才拼到文本末尾。**
   *
   * 为什么：那条路径实测 52 个字符（`~/.herdr-web/uploads/20260921-151443-3ad6ae.jpg`），
   * 而手机上那一行输入框大概只放得下二十几个 —— 传一张图就把整行吃光，人想说的话没地方写
   * （用户报的）。而路径**必须**出现在投给 agent 的文本里（API 里没有图片通道，agent 是去读
   * 磁盘的），所以它只能从「显示」里挪走，不能从「投出去的内容」里挪走。
   *
   * chip 放在**那一行里面**（输入框左边）而不是另起一行：发件箱**不能长高** ——
   * 高度一变就是 Dock 变高 → 终端重排 → SIGWINCH + 冻帧（见 CLAUDE.md）。
   */
  const [atts, setAtts] = useState<UploadResult[]>([])
  const attsRef = useRef<UploadResult[]>([])
  attsRef.current = atts

  /** 挂上几张图（发件箱传图和顶栏传图都走这儿） */
  const hold = useCallback((rs: UploadResult[]) => {
    if (rs.length) setAtts((a) => [...a, ...rs])
  }, [])

  /** 去掉最后挂上的那一张（chip 上那个 ×，再点一下再少一张） */
  const dropAtt = useCallback(() => setAtts((a) => a.slice(0, -1)), [])

  /**
   * 投稿。
   *
   * `override` 是**富输入框自己拼好的那段**（图片 chip 就地展开成路径，见 ComposeRich 的
   * `read`）—— 那一版里 chip 住在 DOM 里，这个 hook 手上没有它们。纯 textarea 那一版
   * 不传，照旧用「说的话 + 挂着那几张图的路径」。
   */
  const submit = useCallback(async (override?: string) => {
    // **投出去的内容 = 说的话 + 那几张图的路径。**
    // 路径必须在文本里（agent 是去读磁盘的），只是不在输入框里显示成 52 个字符。
    const said = textRef.current.trim()
    const paths = attsRef.current.map((a) => a.path)
    const body = override !== undefined ? override.trim() : [said, ...paths].filter(Boolean).join(' ')
    // 只挂了图、一个字没写也算数（「看这张图」这种）—— 所以判空要连附件一起看
    if (!body) { toast('框里是空的'); return }
    clearTimeout(pushTimer.current)
    setBusy(true)
    inFlight.current = true
    say2('投递中…')
    try {
      const r = await api.post<SayResult>('/herdr/say', { target: aimed(), text: body })
      hist.current = [body, ...hist.current.filter((x) => x !== body)].slice(0, HIST_MAX)
      histIdx.current = -1
      localStorage.setItem(HIST_KEY, JSON.stringify(hist.current))
      setText('')                                      // 发完就清空，不做增量同步
      textRef.current = ''
      setAtts([])                                      // 附件跟着一起清
      synced.current = ''
      own.current = false
      pinned.current = ''                              // 框空了，重新跟随焦点
      resolved.current = r.target
      // 乐观回显：记下「投给了哪个 pane + 原文」，chat 那边先摆一条「投递中」。
      //
      // **投给没有 agent 的 pane 不记**（`r.agent` 空 = 普通 shell）。用户报的就是这条：
      // 他在终端里敲 `claude` 去**启动** agent —— 那是一条 shell 命令，不是发给 agent 的话，
      // 永远不会出现在 agent 的转录里，于是那条回显一直挂着。更坏的是连带：
      // 「认不出原文就丢最老那条」那个兜底（见 dropSent）会在下一句**真话**到达时把这条
      // `claude` 弹掉，于是真正那句反过来一直显示成「投递中」。
      //
      // 判据就是「这段字有没有进 agent 的输入框」—— 没进去的话转录里注定没有它，
      // 那就不该拿转录去等它。
      if (r.agent) {
        // **只留最近几条**，而且 60 秒没被真的那条顶掉就自己消失 —— 同时挂着好几条
        // 「投递中」本身就说明出问题了，一直挂着比没有更让人不放心。
        setSent((old) => [...old, { target: r.target, text: body, at: Date.now() }].slice(-SENT_MAX))
      }
      say2(`已投给 ${r.target}[${r.agent || 'shell'}] · ${r.chars} 字`)
    } catch (e) {
      say2('投稿失败：' + (e as Error).message, true)
      toast('投稿失败：' + (e as Error).message)
    } finally {
      inFlight.current = false
      setBusy(false)
    }
  }, [aimed, say2, toast])

  /**
   * chat 那边读到一条人话了，把对应的回显撤掉。
   *
   * # 为什么不能只靠「原文一样」
   *
   * 第一版是 `x.text !== text` —— 屏幕上照旧出现两条（用户报的，两张截图）。原因是投出去的
   * 原文和转录里记下来的**不保证逐字相同**：claude 把投稿当粘贴处理，包一层
   * `<pasted_content …>` 还前后加空行（实测 `'\n\n<pasted_content id="8e39">\n…\n</pasted_content …>\n'`），
   * 服务端剥壳 + TrimSpace 之后**通常**就对上了，但只要哪一头多一个空白就认不出 ——
   * 而认不出的表现正好是「同一句话显示两遍」，看着像 bug 里最蠢的那种。
   *
   * 所以判据分两层：
   *
   *	trim 之后相等   正路，能精确对上哪一条
   *	对不上          按**先进先出**丢掉这个 pane 最老那条回显
   *
   * 第二层站得住是因为 **chat 这边只有发件箱一条发言路**：既然这个 pane 冒出了一条人话，
   * 我们挂着的那条「投递中」就是落地了（哪怕被规范化得认不出来）。`fifo` 只在**增量**那种
   * 批次里给真 —— 整份重读那次会一次带回几十条历史人话，那时候按 FIFO 丢就是把还没落地的
   * 回显误撤掉。
   */
  const dropSent = useCallback((target: string, text: string, fifo = false) => {
    setSent((old) => {
      const t = text.trim()
      const i = old.findIndex((x) => x.target === target && x.text.trim() === t)
      if (i >= 0) return old.filter((_, j) => j !== i)
      if (!fifo) return old
      const j = old.findIndex((x) => x.target === target)
      return j >= 0 ? old.filter((_, k) => k !== j) : old
    })
  }, [])

  const recall = useCallback((dir: number) => {
    if (!hist.current.length) return
    histIdx.current = Math.max(-1, Math.min(hist.current.length - 1, histIdx.current + dir))
    const v = histIdx.current < 0 ? '' : hist.current[histIdx.current]
    setText(v)
    textRef.current = v
    own.current = !!v
  }, [])

  /**
   * 传图：落盘到 herdr 那台机器，返回绝对路径（API 里没有图片通道，agent 是**去读磁盘**
   * 上那个文件的，所以「传图」＝落盘 + 把路径当文本给出去）。
   *
   * 只负责上传，路径交给调用方处置 —— 发件箱开着就插进草稿，没开就直接打进终端。
   */
  const upload = useCallback(async (files: FileList | File[], onPath?: (r: UploadResult) => void) => {
    const imgs = [...files].filter((f) => f.type.startsWith('image/') || /\.(png|jpe?g|gif|webp|heic)$/i.test(f.name))
    const out: UploadResult[] = []
    if (!imgs.length) return out
    setBusy(true)
    try {
      for (let i = 0; i < imgs.length; i++) {
        say2(`上传第 ${i + 1}/${imgs.length} 张…`)
        const blob = await normalizeImage(imgs[i])
        const r = await api.upload(blob)
        out.push(r)
        onPath?.(r)
        say2(`已插入 ${r.name}（${(r.bytes / 1024).toFixed(0)} KB）· 路径已给出去，agent 会去读这个文件`)
      }
    } catch (e) {
      say2('传图失败：' + (e as Error).message, true)
      toast('传图失败：' + (e as Error).message)
    } finally {
      setBusy(false)
    }
    return out
  }, [say2, toast])

  /** 发件箱里的传图：把路径插在光标处 */
  /**
   * 发件箱里的传图：**挂成附件，不往输入框里插字**。
   *
   * 原来是把路径插在光标处 —— 那是 52 个字符，一行输入框当场没了（见 atts 的注释）。
   */
  const attach = useCallback(async (files: FileList | File[]) => {
    hold(await upload(files))
  }, [upload, hold])

  /** 往草稿末尾接一段（顶栏传图 / 全页粘贴用，那两处没有光标可言） */
  const append = useCallback((chunk: string) => {
    const cur = textRef.current
    onChangeText(cur + (cur && !/\s$/.test(cur) ? ' ' : '') + chunk)
  }, [onChangeText])

  return {
    text, setText: onChangeText, panes, watching, presets,
    info, bad, busy, aimed,
    loadPanes, loadSoftkeyPresets, tick, pull, submit, recall, attach, upload, append, jump,
    sent, dropSent,
    atts, hold, dropAtt,
  }
}

// 手机照片动辄 4000px / 几 MB。能解码就先缩到长边 2400 再传，顺便把 HEIC 这种
// agent 读不了的格式统一成 PNG / JPEG。解不了就原样传，让服务端按魔数去认。
async function normalizeImage(file: File): Promise<Blob> {
  const MAX_EDGE = 2400
  const png = /png/i.test(file.type)
  try {
    const bmp = await createImageBitmap(file)
    const scale = Math.min(1, MAX_EDGE / Math.max(bmp.width, bmp.height))
    if (scale === 1 && (png || /jpe?g/i.test(file.type))) { bmp.close?.(); return file }
    const w = Math.round(bmp.width * scale)
    const h = Math.round(bmp.height * scale)
    const cv = document.createElement('canvas')
    cv.width = w
    cv.height = h
    cv.getContext('2d')!.drawImage(bmp, 0, 0, w, h)
    bmp.close?.()
    const out = await new Promise<Blob | null>((r) => cv.toBlob(r, png ? 'image/png' : 'image/jpeg', 0.92))
    return out ?? file
  } catch {
    return file
  }
}
