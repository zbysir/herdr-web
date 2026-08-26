import { useCallback, useEffect, useMemo, useState } from 'react'
import { RefreshCw, GitBranch, X } from 'lucide-react'
import { gitApi, type DiffMode, type GitRepo, type GitStatus, type Pane } from '@/lib/api'
import { Panel } from './ui/panel'
import { Button } from './ui/button'
import { Input } from './ui/input'
import { DiffViewer } from './DiffViewer'
import { ChangeRow } from './DiffRow'
import { cn } from '@/lib/utils'

/**
 * 看 diff：**先一份改动清单，点进去才是补丁**。
 *
 * 为什么要这个面板：用户报的是「手机上终端的 git diff 太难看了」。终端里那份读不了是
 * 三件事叠起来的 —— 长行不能折（40 列的屏上不是被切就是横滚）、一整行红一整行绿看不出
 * 改了哪个词、翻页只能靠 pager（触屏上要点方向键）。这三件在终端里一件都解决不了，
 * 所以只能另开一层：折行、按词高亮、按文件分开点（补丁在 internal/gitdiff 那边解析好）。
 *
 * # 「在哪个仓库」不问人，看**当前这个工作空间**
 *
 * 和文件浏览的起点列表同一个思路（那份数据前端手上本来就有）：把 pane 的 cwd 丢给
 * 服务端问一句「哪些是 git 仓库」，按仓库根去重。人心里那个东西是**仓库**不是目录 ——
 * 好几个 pane 开在同一个仓库的不同子目录里是常态。
 *
 * 但**只丢当前工作空间那几个 pane**：一个工作空间就是「我现在在做的这个项目」，而
 * herdr 里同时开着好几个工作空间是常态（实测 48 个 pane / 34 个不同 cwd）。把所有
 * cwd 都丢进去问的话，选择器里是一长串八竿子打不着的仓库，默认落在哪个上面全看顺序
 * —— 用户报的就是这个：面板打开看到的是**另一个项目**的 diff。
 * 所以下拉框现在的语义也跟着变实了：**它出现 = 这个工作空间下真有好几个 git 项目**。
 *
 * # 只读
 *
 * 这一层没有 add / commit / checkout，也不打算有：会改仓库的事在终端里做，那儿有完整的
 * git，还看得见输出。一个从公网点得到的「一键 checkout」按钮，值不了它带来的那些问题。
 */
const LS_MODE = 'diffMode'

const MODES: { id: DiffMode; label: string; hint: string }[] = [
  { id: 'all', label: '改动', hint: '工作区相对上次提交（含新建的文件）—— agent 干了什么看这个' },
  { id: 'staged', label: '已暂存', hint: '`git add` 过、还没提交的那部分' },
  { id: 'head', label: '上次提交', hint: '最近那一次提交自己带的改动' },
]

/** 长路径缩成 ~/…：仓库那一行就那么宽，而家目录前缀在每一条里都是同一段 */
const short = (p: string) => p.replace(/^\/(?:Users|home)\/[^/]+/, '~')


export function DiffPanel({
  panes, profile, onClose, onBrowse, onOpen, onRepo, toast,
}: {
  /** herdr 的 pane 列表：拿它们的 cwd 找仓库。**这就是「当前在哪个项目」的来源** */
  panes: Pane[]
  /** 当前这套排布的 ID（折行那个开关跟着它走，见 lib/prefs.ts） */
  profile: string
  onClose: () => void
  /** 未跟踪的**目录**点进去是文件面板（git 把一整个新目录折成一条，里面有什么它不说） */
  onBrowse: (dir: string) => void
  /** 「看整个文件」：交给 App 那条 openPath（先 stat 再决定开图还是开文本） */
  onOpen: (path: string) => void
  /** 现在盯的是哪个仓库 —— 顶栏那个角标跟着它走（见 hooks/useGitDirty） */
  onRepo?: (root: string) => void
  toast: (m: string) => void
}) {
  const [repos, setRepos] = useState<GitRepo[] | null>(null)
  /**
   * 正在看哪个仓库。**开面板时不从 localStorage 恢复** —— 上次看的那个多半是别的工作
   * 空间里的项目，恢复出来就是「打开面板看到的不是我正在做的这个」（用户报的），
   * 而且还白跑一次那个仓库的 `git status`。由 probe 定（默认焦点 pane 那个）。
   */
  const [root, setRoot] = useState<string | null>(null)
  const [mode, setMode] = useState<DiffMode>(
    () => (MODES.some((m) => m.id === localStorage.getItem(LS_MODE)) ? localStorage.getItem(LS_MODE) as DiffMode : 'all'),
  )
  const [st, setSt] = useState<GitStatus | null>(null)
  const [err, setErr] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [jump, setJump] = useState('')
  /**
   * 补丁页从**哪个文件**开始看（盖在这块面板上面，退出来还回到清单）。
   *
   * 存路径而不是那一条改动：补丁页拿的是**整份清单**（一次改动的全部文件是一条连续的流，
   * 从 a 一直滑到 b），这儿只是说「落在哪儿」。清单刷新之后那一条对象会换个身份，路径不会。
   */
  const [open, setOpen] = useState<string | null>(null)

  /**
   * 候选目录 = **当前工作空间**那几个 pane 的 cwd，焦点 pane 排第一（十有八九就是它）。
   *
   * 原来还把「上次看的那个仓库」也丢进去问，删了：它当初排第二是为了绕服务端 32 个的
   * 上限（排最后会被切掉），而夹到一个工作空间之后候选本来就只有几个。留着它的代价正是
   * 用户报的那个 bug —— 那个仓库永远在候选里，于是「留着人挑过的那个」这条永远命中，
   * 换到别的项目上去也不换。
   *
   * memo 出来的是**一个字符串**：panes 每刷新一次就是一个新数组，按数组做依赖的话
   * 内容没变也会重探一遍（几十次 fork）。
   */
  const dirKey = useMemo(() => {
    const out: string[] = []
    const add = (d?: string | null) => { if (d && !out.includes(d)) out.push(d) }
    const cur = panes.find((p) => p.focused)
    add(cur?.cwd)
    // 分组认 `workspaceId` 不认 `workspace`（那是标签，两个工作空间同名是常态）。
    // 没有焦点 pane（herdr 没报）时退回「所有 pane」：那会儿夹不出工作空间，
    // 而一个候选都不给比给多了糟
    for (const p of panes) if (!cur || p.workspaceId === cur.workspaceId) add(p.cwd)
    return out.join('\n')
  }, [panes])

  const probe = useCallback(async (extra?: string) => {
    const dirs = dirKey.split('\n').filter(Boolean)
    try {
      const r = await gitApi.repos(extra ? [extra, ...dirs] : dirs)
      setRepos(r.repos)
      setRoot((cur) => {
        // 人自己在下拉里挑过的那个，只要还在候选里就留着（换工作空间时它就不在了，
        // 于是自己回到焦点 pane 那个仓库）；否则落到第一个 —— 候选是按重要性排的
        const next = cur && r.repos.some((x) => x.root === cur) ? cur : (r.repos[0]?.root ?? null)
        if (next) onRepo?.(next)
        return next
      })
      if (extra && !r.repos.some((x) => x.root === extra || extra.startsWith(x.root + '/'))) {
        toast(`${short(extra)} 不在一个 git 仓库里`)
      }
    } catch (e) {
      setErr((e as Error).message)
      setRepos([])
    }
  }, [dirKey, toast, onRepo])

  useEffect(() => { void probe() }, [probe])

  const load = useCallback(async (r: string, m: DiffMode) => {
    setBusy(true)
    setErr(null)
    try {
      setSt(await gitApi.status(r, m))
    } catch (e) {
      setErr((e as Error).message)
      setSt(null)
    } finally {
      setBusy(false)
    }
  }, [])

  useEffect(() => { if (root) void load(root, mode) }, [root, mode, load])

  const pick = (r: string) => { setRoot(r); onRepo?.(r) }
  const pickMode = (m: DiffMode) => { setMode(m); localStorage.setItem(LS_MODE, m) }

  const repo = st?.repo
  const sum = useMemo(() => {
    const cs = st?.changes ?? []
    return {
      n: cs.length,
      add: cs.reduce((a, c) => a + (c.binary ? 0 : c.add), 0),
      del: cs.reduce((a, c) => a + (c.binary ? 0 : c.del), 0),
    }
  }, [st])

  return (
    <>
      <Panel onClose={onClose} className="max-md:bottom-2 md:max-h-[calc(100%-34px)]">
        {/* 仓库这一行。下拉只在**这个工作空间下有好几个 git 项目**时出现
            （一个选项的选择器只是噪音），但路径照旧显示 —— 「我现在看的是哪个项目」不能靠猜 */}
        <div className="mb-1.5 flex items-center gap-1.5">
          {(repos?.length ?? 0) > 1 ? (
            <select
              value={root ?? ''}
              onChange={(e) => pick(e.target.value)}
              className="min-w-0 flex-1 cursor-pointer truncate rounded-md border border-line bg-ctl px-2 py-1.5
                         font-mono text-xs text-fg outline-none hover:border-line-hi focus:border-brand/70"
            >
              {repos?.map((r) => <option key={r.root} value={r.root}>{short(r.root)}</option>)}
            </select>
          ) : (
            <span className="min-w-0 flex-1 truncate font-mono text-xs text-muted" title={root ?? ''}>
              {root ? short(root) : '找不到 git 仓库'}
            </span>
          )}
          <Button
            variant="ghost" size="icon" title="重新读一遍" disabled={busy || !root}
            onClick={() => { if (root) void load(root, mode) }}
          >
            <RefreshCw className={cn('size-4', busy && 'animate-spin')} />
          </Button>
          {/* 关闭并进这一排（面板不再有标题栏）。位置还是右上角那个 —— 只是不再为它单占
              一整行。和「重新读一遍」留一点距离：这是唯一一个「点了就没了」的按钮 */}
          <Button
            variant="ghost" size="icon" className="ml-0.5"
            aria-label="关闭" title="关闭（Esc 也行）" onClick={onClose}
          >
            <X className="size-4" />
          </Button>
        </div>

        {repo && (
          <div className="mb-1.5 flex items-center gap-1.5 text-[11px] text-faint">
            <GitBranch className="size-3 shrink-0" />
            <span className="truncate font-mono text-muted">
              {repo.detached ? `摘着头 @ ${repo.head ?? ''}` : (repo.branch || '(没有分支)')}
            </span>
            {!!repo.ahead && <span title={`比 ${repo.upstream} 多 ${repo.ahead} 个提交`}>↑{repo.ahead}</span>}
            {!!repo.behind && <span title={`比 ${repo.upstream} 少 ${repo.behind} 个提交`}>↓{repo.behind}</span>}
            {repo.unborn && <span>还没有提交</span>}
          </div>
        )}

        <div className="mb-2 flex gap-1">
          {MODES.map((m) => (
            <Button
              key={m.id} size="tiny" on={mode === m.id} title={m.hint}
              // 空仓库里「上次提交」那一档没有东西可看，别让人点进一片空
              disabled={m.id === 'head' && !!repo?.unborn}
              onClick={() => pickMode(m.id)}
            >
              {m.label}
            </Button>
          ))}
        </div>

        {st?.commit && (
          <div className="mb-2 rounded-md border border-line bg-ctl/60 px-2 py-1.5">
            <div className="truncate text-xs text-fg" title={st.commit.subject}>{st.commit.subject}</div>
            <div className="truncate text-[11px] text-faint">
              <span className="font-mono">{st.commit.short}</span> · {st.commit.author}
              {st.commit.merge && ' · 合并提交（只比第一个父提交）'}
            </div>
          </div>
        )}

        {err && <p className="px-1 py-2 text-xs/relaxed text-bad">{err}</p>}

        {!err && repos?.length === 0 && (
          <div className="px-1 py-2">
            <p className="mb-2 text-xs/relaxed text-muted">
              当前工作空间这几个 pane 的目录都不在 git 仓库里。下面粘一个仓库路径试试。
            </p>
            <div className="flex gap-1.5">
              <Input
                value={jump} onChange={(e) => setJump(e.target.value)}
                onKeyDown={(e) => { if (e.key === 'Enter' && jump.trim()) void probe(jump.trim()) }}
                placeholder="~/dev/xxx" className="min-w-0 flex-1"
                spellCheck={false} autoCapitalize="off" autoCorrect="off"
              />
              <Button size="default" disabled={!jump.trim()} onClick={() => void probe(jump.trim())}>打开</Button>
            </div>
          </div>
        )}

        {!err && st && (
          <>
            {st.changes.length > 0 && (
              <div className="mb-1 flex items-center gap-2 px-1 text-[11px] text-faint tabular-nums">
                <span>{sum.n} 个文件</span>
                <span className="text-ok">+{sum.add}</span>
                <span className="text-bad">−{sum.del}</span>
                {/* 补丁页是一条连续的流（全部文件顺着滑），所以「从头看」才是常见的那一次 ——
                    不给这个入口的话，想从头看还得先在清单里挑一个文件，纯属多一步 */}
                <span className="flex-1" />
                {st.changes.some((c) => !c.dir) && (
                  <Button size="tiny" onClick={() => setOpen(st.changes.find((c) => !c.dir)!.path)}>
                    从头看
                  </Button>
                )}
              </div>
            )}
            <ul className="flex flex-col gap-px">
              {st.changes.map((c) => (
                <ChangeRow
                  key={c.kind + c.path}
                  c={c}
                  mode={mode}
                  onPick={() => (c.dir ? onBrowse(`${st.repo.root}/${c.path}`) : setOpen(c.path))}
                />
              ))}
            </ul>
            {st.changes.length === 0 && (
              <p className="px-1 py-3 text-xs text-muted">
                {mode === 'staged' ? '暂存区是空的（没 `git add` 过东西）'
                  : mode === 'head' ? '上次提交没改任何文件'
                    : '工作区是干净的'}
              </p>
            )}
            {/* 截断必须说出来：不说的话「就改了这几个文件」是句假话 */}
            {!!st.truncated && (
              <p className="px-1 pt-2 text-[11px] text-warn">
                改动太多，只列了 {st.changes.length} 条，还有 {st.truncated} 条没显示。
              </p>
            )}
          </>
        )}
      </Panel>

      {/* 补丁盖在面板上面（不关面板）：看完一个文件退出来还该回到这份清单，
          和文件浏览那边「查看器压在目录上面」是同一条 */}
      {open && st && (
        <DiffViewer
          repo={st.repo}
          mode={mode}
          changes={st.changes}
          startPath={open}
          profile={profile}
          onClose={() => setOpen(null)}
          onOpenFile={(path) => { setOpen(null); onOpen(`${st.repo.root}/${path}`) }}
          toast={toast}
        />
      )}
    </>
  )
}
