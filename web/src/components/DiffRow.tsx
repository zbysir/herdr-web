import type { DiffMode, GitChange } from '@/lib/api'
import { cn } from '@/lib/utils'

/**
 * 改动清单里的一行。**一份，两处用**：
 *
 *   - 面板里那份清单（挑一个开始看、看一眼这次动了多大）
 *   - 补丁页顶栏点开的那张索引（正在流里滑着，想直接跳到某个文件）
 *
 * 两处各写一遍的话，「改名怎么显示」「暂存角标什么时候出」这类只在少数情况下才现形的
 * 规则就有了两个版本，而它们**长得一样**才是这个功能好用的前提 —— 索引里认得出的那一行，
 * 就是清单里点过的那一行。
 */

export const base = (p: string) => p.replace(/\/+$/, '').split('/').pop() || p
export const dirOf = (p: string) => p.replace(/\/?[^/]+\/?$/, '')

/** 状态字母 + 配色。红 = 没了，绿 = 新的，黄 = 改了 —— 和终端里 git 自己那套对得上 */
const BADGE: Record<string, { t: string; cls: string; hint: string }> = {
  add: { t: 'A', cls: 'border-ok/45 bg-ok/12 text-ok', hint: '新增' },
  untracked: { t: '?', cls: 'border-ok/45 bg-ok/12 text-ok', hint: '还没被 git 跟踪的新文件' },
  modify: { t: 'M', cls: 'border-warn/45 bg-warn/12 text-warn', hint: '改了内容' },
  delete: { t: 'D', cls: 'border-bad/45 bg-bad/12 text-bad', hint: '删掉了' },
  rename: { t: 'R', cls: 'border-line-hi bg-ctl text-muted', hint: '改了名字' },
  copy: { t: 'C', cls: 'border-line-hi bg-ctl text-muted', hint: '从别的文件复制来的' },
  type: { t: 'T', cls: 'border-line-hi bg-ctl text-muted', hint: '类型变了（文件 ↔ 软链之类）' },
  conflict: { t: '!', cls: 'border-bad/45 bg-bad/12 text-bad', hint: '有冲突没解决' },
}

export function ChangeRow({
  c, mode, on, onPick,
}: {
  c: GitChange
  mode: DiffMode
  /** 正看着的那个（索引里高亮它 —— 不然「我在哪」要自己一行行找） */
  on?: boolean
  onPick: () => void
}) {
  const b = BADGE[c.kind] ?? BADGE.modify
  const dir = dirOf(c.path)
  return (
    <li>
      <button
        className={cn('flex w-full items-center gap-2 rounded px-2 py-1.5 text-left hover:bg-ctl',
          on && 'bg-brand/12 hover:bg-brand/18')}
        onClick={onPick}
      >
        <span
          title={b.hint}
          className={cn('grid size-4 shrink-0 place-items-center rounded-[3px] border font-mono text-[10px] leading-none', b.cls)}
        >
          {b.t}
        </span>
        <span className="min-w-0 flex-1">
          <span className={cn('block truncate text-xs', on ? 'text-brand' : 'text-fg')}>
            {c.dir ? c.path : base(c.path)}
            {/* 改名要说清**改的是哪一半**：文件名一样时（只是挪了个目录）显示旧目录，
                否则显示旧文件名 —— 两处都显示文件名的话，「a.go ← a.go」看着像个 bug */}
            {c.old && (
              <span className="text-faint"> ← {base(c.old) === base(c.path) ? (dirOf(c.old) || '/') : base(c.old)}</span>
            )}
          </span>
          {/* 目录那一行是次要信息，但**不能不给** —— 一个仓库里同名文件到处都是 */}
          {(dir || c.dir) && (
            <span className="block truncate font-mono text-[10px] text-faint" title={c.path}>
              {c.dir ? '整个目录还没被跟踪 · 点开去文件面板翻' : dir}
            </span>
          )}
        </span>
        {/* mode=all 时同一个文件可能一半在暂存区一半没有 —— 不说的话「我明明 add 过了」
            没有任何线索 */}
        {mode === 'all' && c.staged && (
          <span
            className="shrink-0 rounded border border-brand/40 bg-brand/12 px-1 text-[10px] leading-4 text-brand"
            title={c.unstaged ? '一部分改动已经 add 了，还有一部分没有' : '这些改动已经 add 过了'}
          >
            {c.unstaged ? '半暂存' : '暂存'}
          </span>
        )}
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
    </li>
  )
}
