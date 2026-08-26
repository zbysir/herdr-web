import { useCallback, useEffect, useMemo, useState } from 'react'
import { ArrowUp, File, FileImage, FileText, Folder, Link2, RefreshCw, Search, Clock, SortAsc } from 'lucide-react'
import { filesApi, type FileEntry, type FileListing, type FileRoot, type FileRoots, type Pane } from '@/lib/api'
import { Panel } from './ui/panel'
import { Button } from './ui/button'
import { Input } from './ui/input'
import { ago } from './PaneSwitcher'
import { human } from './FileViewer'
import { cn } from '@/lib/utils'

/**
 * 文件浏览：**兜底入口**。
 *
 * 主入口是终端里那行路径可点（见 term/paths.ts）—— agent 打出来的路径直接开，
 * 根本不用管它在哪个目录。这个面板是给「路径没看见 / 被截断了 / 想翻翻旁边还有什么」
 * 准备的。
 *
 * # 起点，不是牢笼
 *
 * 「agent 生成的图不在当前 workspace 下」这个问题，解法不是把边界画大一点，而是
 * **不画边界，只给起点**：
 *
 *   - herdr 各 pane 的 cwd（面板一览用的同一份数据，白拿的）
 *   - 上传目录 / 家目录 / 临时目录（服务端给）
 *   - 最近去过的（本地记 8 条）
 *   - 一个能粘任意绝对路径的框 —— 这一条才是真正兜住所有情况的那个
 *
 * 进去之后 `..` 一路能走到 `/`。理由很直白：能打开这个页面的人已经有一个登录 shell，
 * 白名单挡不住他，只会天天挡路。真要边界就在服务端配 `HERDR_WEB_FILE_ROOTS`
 * （那时候 jailed 为真，这里不显示走不通的路）。
 */
const LS_DIR = 'filesDir'
const LS_SORT = 'filesSort'
const LS_ALL = 'filesAll'
const LS_RECENT = 'filesRecent'
const MAX_RECENT = 8

type Sort = 'mtime' | 'name'

function recentDirs(): string[] {
  try {
    const v: unknown = JSON.parse(localStorage.getItem(LS_RECENT) ?? '[]')
    return Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string').slice(0, MAX_RECENT) : []
  } catch {
    return []
  }
}

function pushRecent(dir: string) {
  const next = [dir, ...recentDirs().filter((d) => d !== dir)].slice(0, MAX_RECENT)
  localStorage.setItem(LS_RECENT, JSON.stringify(next))
}

/** 长路径缩成 ~/…：面板一列就那么宽，而家目录前缀在每一行里都是同一段 */
const short = (p: string) => p.replace(/^\/(?:Users|home)\/[^/]+/, '~')

const ICON = {
  dir: Folder,
  image: FileImage,
  text: FileText,
  binary: File,
  special: File,
} as const

export function FilesPanel({
  panes, start, onClose, onOpen,
}: {
  /** herdr 的 pane 列表：拿它们的 cwd 当起点。**这才是「当前 workspace」的真正来源** */
  panes: Pane[]
  /** 打开时直接定位到这个目录（从查看器的「所在目录」过来）。空 = 回到上次那个 */
  start?: string
  onClose: () => void
  onOpen: (path: string) => void
}) {
  const [dir, setDir] = useState<string | null>(() => start ?? localStorage.getItem(LS_DIR))
  const [list, setList] = useState<FileListing | null>(null)
  const [roots, setRoots] = useState<FileRoots | null>(null)
  const [err, setErr] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [q, setQ] = useState('')
  const [jump, setJump] = useState('')
  const [sort, setSort] = useState<Sort>(() => (localStorage.getItem(LS_SORT) === 'name' ? 'name' : 'mtime'))
  const [all, setAll] = useState(() => localStorage.getItem(LS_ALL) === '1')

  useEffect(() => { if (start) setDir(start) }, [start])

  useEffect(() => {
    void filesApi.roots().then(setRoots).catch(() => { /* 起点拿不到还有 pane 的 cwd 和手输 */ })
  }, [])

  const load = useCallback(async (p: string) => {
    setBusy(true)
    setErr(null)
    try {
      const l = await filesApi.list(p, { sort, all })
      setList(l)
      setDir(l.path)
      localStorage.setItem(LS_DIR, l.path)
      pushRecent(l.path)
    } catch (e) {
      setErr((e as Error).message)
      setList(null)
    } finally {
      setBusy(false)
    }
  }, [sort, all])

  useEffect(() => { if (dir) void load(dir) }, [dir, load])

  /**
   * 起点列表。**pane 的 cwd 排在最前面** —— 十有八九要找的东西就在某个 agent
   * 正在跑的那个目录里，而那份数据前端手上已经有了（面板一览用的同一份），
   * 不用为它再打一次 herdr socket。
   *
   * pane 那一段自己再分三轮：**焦点 pane → 同一个工作空间里的别的 pane → 别的工作空间**，
   * 前两轮带一个「当前」标。理由是 herdr 里同时开着好几个工作空间、几十个 pane 是常态
   * （实测 48 个 pane / 34 个不同 cwd），不排一下的话「我正在做的这个项目」那一条就淹在
   * 一长串里，而第一条又恰好是最好点的那条 —— 「第 1 个一定是当前这个工作空间」是用户
   * 点名要的（改动面板那边是同一条道理，见 DiffPanel）。
   *
   * 去重按路径，所以**先加的那一轮赢** —— 同一个目录在两个工作空间里都开着时，
   * 「当前」这个标该留在当前那一条上。
   */
  const starts = useMemo(() => {
    const out: (FileRoot & { hint?: string; cur?: boolean })[] = []
    const seen = new Set<string>()
    const add = (path: string, label: string, hint?: string, cur?: boolean) => {
      if (!path || seen.has(path)) return
      seen.add(path)
      out.push({ path, label, hint, cur })
    }
    const name = (p: Pane) => (p.agent ? `${p.agent} · ${p.workspace}/${p.tab}` : `${p.workspace}/${p.tab}`)
    const focus = panes.find((p) => p.focused)
    if (focus) add(focus.cwd, name(focus), 'pane', true)
    // 「同一个工作空间」认 `workspaceId` 不认 `workspace`（后者是标签，同名是常态）
    for (const p of panes) if (focus && p.workspaceId === focus.workspaceId) add(p.cwd, name(p), 'pane', true)
    for (const p of panes) add(p.cwd, name(p), 'pane')
    for (const r of roots?.roots ?? []) add(r.path, r.label)
    for (const d of recentDirs()) add(d, '最近去过')
    return out
  }, [panes, roots])

  const rows = useMemo(() => {
    const kw = q.trim().toLowerCase()
    if (!list) return []
    return kw ? list.entries.filter((e) => e.name.toLowerCase().includes(kw)) : list.entries
  }, [list, q])

  const go = (p: string) => { setQ(''); setDir(p) }

  // 是文件还是目录不用在这儿猜：onOpen 那条路（App 的 openPath）会先 stat 一次 ——
  // 目录就自己绕回这个面板并定位过去，文件就开查看器。
  const openJump = () => {
    const p = jump.trim()
    if (!p) return
    setJump('')
    onOpen(p)
  }

  const flip = (k: 'sort' | 'all') => {
    if (k === 'sort') {
      const n: Sort = sort === 'mtime' ? 'name' : 'mtime'
      setSort(n); localStorage.setItem(LS_SORT, n)
    } else {
      setAll(!all); localStorage.setItem(LS_ALL, all ? '0' : '1')
    }
  }

  return (
    // 宽屏上留一截底：文件面板下面就是发件箱，挡住它就没法「看一眼图再接着说」。
    // 那个 34px 是跟着 Panel 的 top-1.5 算的（改 Panel 的 top 就得跟着改这儿）
    <Panel title="文件" onClose={onClose} className="max-md:bottom-2 md:max-h-[calc(100%-34px)]">
      {/* 粘任意绝对路径。**这一条兜住所有「不在任何起点下面」的情况** ——
          agent 往 /var/folders/xx/T/ 里写了张图，这儿粘进去就完了。 */}
      <div className="mb-2 flex gap-1.5">
        <Input
          value={jump}
          onChange={(e) => setJump(e.target.value)}
          onKeyDown={(e) => { if (e.key === 'Enter') openJump() }}
          placeholder="粘一个绝对路径（/tmp/out.png、~/Downloads）"
          className="min-w-0 flex-1"
          spellCheck={false}
          autoCapitalize="off"
          autoCorrect="off"
        />
        <Button size="default" onClick={openJump} disabled={!jump.trim()}>打开</Button>
      </div>

      {dir === null ? (
        <>
          {/* 只在**真有边界**时说一句。原来这儿还有一句「从哪儿开始看。进去之后一路 ..
              能走到 /。」，删了：列表自己就长得像起点列表，而那半句是句永远为真的废话，
              占掉的是手机上最贵的那一屏顶部 */}
          {roots?.jailed && (
            <p className="mb-1.5 text-[11px] text-muted">
              这台机器配了 HERDR_WEB_FILE_ROOTS，只有这几棵树看得到。
            </p>
          )}
          <ul className="flex flex-col gap-0.5">
            {starts.map((s) => (
              <li key={s.path}>
                <button
                  className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left hover:bg-ctl"
                  onClick={() => go(s.path)}
                >
                  <Folder className="size-4 shrink-0 text-muted" />
                  <span className="min-w-0 flex-1">
                    <span className="block truncate font-mono text-xs text-fg">{short(s.path)}</span>
                    <span className="block truncate text-[11px] text-faint">{s.label}</span>
                  </span>
                  {/* 「当前」= 这一条是当前工作空间的。淡绿底 + 绿边 + 绿字那一套
                      （面板一览里焦点那行同款），不是整块涂满 —— 见配色那节 */}
                  {s.cur && (
                    <span className="shrink-0 rounded border border-brand/40 bg-brand/12 px-1 py-px text-[10px] text-brand">
                      当前
                    </span>
                  )}
                </button>
              </li>
            ))}
            {starts.length === 0 && <li className="px-2 py-3 text-xs text-muted">一个起点都没有 —— 上面那个框里粘一个绝对路径。</li>}
          </ul>
        </>
      ) : (
        <>
          <div className="mb-1.5 flex items-center gap-1">
            <Button
              variant="ghost" size="icon" title={list?.parent ? `上一级：${short(list.parent)}` : '到头了'}
              disabled={!list?.parent} onClick={() => list?.parent && go(list.parent)}
            >
              <ArrowUp className="size-4" />
            </Button>
            <button
              className="min-w-0 flex-1 truncate rounded px-1 py-0.5 text-left font-mono text-[11px] text-muted hover:bg-ctl"
              title={`${dir}（点一下回到起点列表）`}
              onClick={() => { setDir(null); localStorage.removeItem(LS_DIR) }}
            >
              {short(dir)}
            </button>
            <Button
              variant="ghost" size="icon" on={sort === 'name'} onClick={() => flip('sort')}
              title={sort === 'mtime' ? '现在按「最近改动」排（找刚生成的东西）。点一下换成按名字' : '现在按名字排。点一下换回「最近改动」'}
            >
              {sort === 'mtime' ? <Clock className="size-4" /> : <SortAsc className="size-4" />}
            </Button>
            <Button
              variant="ghost" size="icon" on={all} onClick={() => flip('all')}
              title={all ? '正在显示点开头的文件' : `显示点开头的文件${list?.hidden ? `（这儿有 ${list.hidden} 个）` : ''}`}
            >
              <span className="font-mono text-[13px] leading-none">.*</span>
            </Button>
            <Button variant="ghost" size="icon" title="刷新" disabled={busy} onClick={() => void load(dir)}>
              <RefreshCw className={cn('size-4', busy && 'animate-spin')} />
            </Button>
          </div>

          {(list?.entries.length ?? 0) > 12 && (
            <div className="relative mb-1.5">
              <Search className="pointer-events-none absolute left-2 top-1/2 size-3.5 -translate-y-1/2 text-faint" />
              <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="筛名字" className="w-full pl-7" />
            </div>
          )}

          {err && <p className="px-1 py-2 text-xs/relaxed text-bad">{err}</p>}

          <ul className="flex flex-col gap-px">
            {rows.map((e) => <Row key={e.path} e={e} onGo={go} onOpen={onOpen} />)}
          </ul>

          {!err && rows.length === 0 && (
            <p className="px-1 py-3 text-xs text-muted">
              {list?.entries.length ? '没有匹配的' : '空目录'}
              {!all && list?.hidden ? `（还有 ${list.hidden} 个点开头的没显示）` : ''}
            </p>
          )}
          {/* 截断必须说出来：不说的话「这儿没有那张图」就是一句假话 */}
          {!!list?.truncated && (
            <p className="px-1 pt-2 text-[11px] text-warn">
              这个目录太大，只列了 {list.entries.length} 条，还有 {list.truncated} 条没显示。
              换个排序看得到另一批，或者上面粘完整路径直接打开。
            </p>
          )}
        </>
      )}
      {roots?.jailed && dir !== null && (
        <p className="pt-2 text-[11px] text-faint">这台机器配了 HERDR_WEB_FILE_ROOTS，范围之外的目录打不开。</p>
      )}
    </Panel>
  )
}

function Row({ e, onGo, onOpen }: { e: FileEntry; onGo: (p: string) => void; onOpen: (p: string) => void }) {
  const Icon = ICON[e.kind] ?? File
  return (
    <li>
      <button
        className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left hover:bg-ctl disabled:opacity-45"
        disabled={e.kind === 'special'}
        title={e.kind === 'special' ? `${e.name}：不是常规文件（设备 / socket / 管道），打不开` : e.path}
        onClick={() => (e.dir ? onGo(e.path) : onOpen(e.path))}
      >
        <Icon className={cn('size-4 shrink-0', e.dir ? 'text-brand' : 'text-muted')} />
        <span className="min-w-0 flex-1 truncate text-xs text-fg">{e.name}</span>
        {e.link && <Link2 className="size-3 shrink-0 text-faint" />}
        {!e.dir && <span className="shrink-0 text-[11px] text-faint tabular-nums">{human(e.size)}</span>}
        <span className="w-8 shrink-0 text-right text-[11px] text-faint tabular-nums">{ago(e.mtime)}</span>
      </button>
    </li>
  )
}
