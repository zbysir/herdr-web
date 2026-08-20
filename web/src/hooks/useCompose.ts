// 发件箱的状态机。
//
// 「这是我自己写的」和「远端现在是什么」必须分成两个东西：
// 一开始只用「文本 !== 上次对齐的文本」判断草稿，结果开着「双向」时草稿被推到远端
// 之后，对齐文本就等于草稿本身，于是草稿看起来「没改过」→ 解锁目标 → 下一拍把用户
// 正在写的东西直接覆盖掉。所以 own 单独负责所有权，synced 只负责发现远端变化。
import { useCallback, useEffect, useRef, useState } from 'react'
import { api, FOLLOW, type DraftResult, type Pane, type PresetGroup, type SayResult, type SyncResult } from '@/lib/api'

const HIST_KEY = 'composeHist'
const HIST_MAX = 30

export interface ComposeCfg { poll: number; push: number }

export function useCompose(cfg: ComposeCfg, visible: boolean, live: boolean, toast: (m: string) => void) {
  const [text, setText] = useState('')
  const [panes, setPanes] = useState<Pane[]>([])
  const [presets, setPresets] = useState<PresetGroup[]>([])
  const [sel, setSel] = useState<string>(() => localStorage.getItem('composeTarget') || FOLLOW)
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
   * 「跟随焦点」不能一路跟到按下按钮那一刻：你为 A 写了一段话，中途焦点漂到了 B
   * （herdr 自己会因为 agent 状态变化换焦点），投出去就落到 B 了。所以**自己改过的
   * 草稿**会把目标锁定在当初瞄准的 pane 上，框空了才重新跟随。
   *
   * 判据是 own 而不是「框里有没有字」：自动拉回来还没动过的内容不算草稿，那时候切
   * pane 就该跟着换成新 pane 的内容。用「有没有字」当判据的话，只要框里有东西目标
   * 就被钉死，切 pane 后 input 再也不更新。
   */
  const aimed = useCallback(() => {
    if (sel !== FOLLOW) return sel
    return pinned.current && own.current ? pinned.current : FOLLOW
  }, [sel])

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
      const r = await api.get<{ panes: Pane[] }>('/herdr/panes')
      setPanes(r.panes ?? [])
    } catch (e) {
      setPanes([])
      // socket 在跑 herdr server 的那台机器上，不一定是跑 herdr-web 的这台
      say2(`连不上 herdr：${(e as Error).message}`, true)
      if (!quiet) toast('连不上 herdr：' + (e as Error).message)
    }
  }, [say2, toast])

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
    const switched = r.target !== resolved.current
    resolved.current = r.target
    const pinNote = target === FOLLOW ? '' : ' · 草稿已锁定这个 pane'

    if (switched) {
      // 焦点换了 pane：框里是远端来的就直接换成新 pane 的内容，是自己写的就留着
      if (own.current) say2(`${label(r)} · 本地有草稿，没自动拉回（点「拉回」覆盖）`)
      else { adopt(r.text ?? '', r.target); say2(label(r)) }
      return
    }
    if (!own.current && (r.text ?? '') !== synced.current) {
      adopt(r.text ?? '', r.target)
      say2(`${label(r)} · 已跟随远端改动`)
      return
    }
    say2(`${label(r)}${own.current ? ' · 本地草稿未投' : ''}${pinNote}`)
  }, [aimed, adopt, label, say2, visible])

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

  const submit = useCallback(async () => {
    const body = textRef.current
    if (!body.trim()) { toast('框里是空的'); return }
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
      synced.current = ''
      own.current = false
      pinned.current = ''                              // 框空了，重新跟随焦点
      resolved.current = r.target
      say2(`已投给 ${r.target}[${r.agent || 'shell'}] · ${r.chars} 字`)
    } catch (e) {
      say2('投稿失败：' + (e as Error).message, true)
      toast('投稿失败：' + (e as Error).message)
    } finally {
      inFlight.current = false
      setBusy(false)
    }
  }, [aimed, say2, toast])

  const recall = useCallback((dir: number) => {
    if (!hist.current.length) return
    histIdx.current = Math.max(-1, Math.min(hist.current.length - 1, histIdx.current + dir))
    const v = histIdx.current < 0 ? '' : hist.current[histIdx.current]
    setText(v)
    textRef.current = v
    own.current = !!v
  }, [])

  /** 传图：落盘到 herdr 那台机器，把绝对路径插进提示词。 */
  const attach = useCallback(async (files: FileList | File[], insertAt: () => number) => {
    const imgs = [...files].filter((f) => f.type.startsWith('image/') || /\.(png|jpe?g|gif|webp|heic)$/i.test(f.name))
    if (!imgs.length) return
    setBusy(true)
    try {
      for (let i = 0; i < imgs.length; i++) {
        say2(`上传第 ${i + 1}/${imgs.length} 张…`)
        const blob = await normalizeImage(imgs[i])
        const r = await api.upload(blob)
        const at = insertAt()
        const before = textRef.current.slice(0, at)
        const chunk = (before && !/\s$/.test(before) ? ' ' : '') + r.path + ' '
        onChangeText(before + chunk + textRef.current.slice(at))
        say2(`已插入 ${r.name}（${(r.bytes / 1024).toFixed(0)} KB）· 路径已在框里，agent 会去读这个文件`)
      }
    } catch (e) {
      say2('传图失败：' + (e as Error).message, true)
      toast('传图失败：' + (e as Error).message)
    } finally {
      setBusy(false)
    }
  }, [onChangeText, say2, toast])

  const selectTarget = useCallback((v: string) => {
    setSel(v)
    localStorage.setItem('composeTarget', v)
    resolved.current = ''   // 逼下一拍当成「切了 pane」处理
  }, [])

  return {
    text, setText: onChangeText, panes, presets, sel, selectTarget,
    info, bad, busy, aimed,
    loadPanes, loadSoftkeyPresets, tick, pull, submit, recall, attach,
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
