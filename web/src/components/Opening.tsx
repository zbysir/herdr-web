import { useEffect, useState } from 'react'
import { X } from 'lucide-react'

/**
 * 「正在打开某个文件」那一屏。
 *
 * # 为什么要它
 *
 * 点一条路径到画面上出东西之间是一次 `stat`，而那一步**可以很久**：
 *
 *	系统授权     macOS 第一次访问 `~/Downloads` / `~/Desktop` / `~/Documents` 会弹授权框，
 *	             而那次读**阻塞在对话框上** —— 没人去点的话它永远不回
 *	慢盘 / 大目录 网络盘、外置盘、几万个文件的目录
 *	隧道慢       手机走公网那条路
 *
 * 之前这段时间屏幕上**一点动静都没有**，表现就是「点了没反应」（用户报的，而且他先怀疑的是
 * 链接坏了 —— 真因是那台机器上弹了授权框）。这和 CLAUDE.md 里那条「『点了没反应』多半是反馈
 * 离手指太远」是同一类，而结论也一样：反馈要**当场、铺满、不可能看不见**。
 *
 * # 三条是刻意的
 *
 * ① **必须能退出来**。`stat` 卡在授权框上时那个 promise 永远不回，没有出口就是真卡死了 ——
 *    所以有 ×（也吃 Esc，在 App 那边收口）。退出之后**迟到的结果要丢掉**，不然人已经走开了，
 *    过一会儿突然弹出一个查看器（那条在 App 的 `openSeq` 里）。
 * ② **久了要说清可能在等什么**。「读取中…」转三十秒不如一句「那台机器可能在等一个系统授权」——
 *    后者人能立刻去处理，前者只会让人再点一次。
 * ③ 路径**从中间省略**而不是掐尾：`/Users/…/Downloads/iShot_2026-04-21_18.11.57.png` 里
 *    要紧的是**文件名**，掐尾正好把它掐掉。
 */

/** 多久之后开始提示「可能在等系统授权」。太短会在正常的慢盘上乱报 */
const HINT_MS = 2500

export function Opening({ path, onCancel }: { path: string; onCancel: () => void }) {
  const [slow, setSlow] = useState(false)
  useEffect(() => {
    setSlow(false)
    const t = window.setTimeout(() => setSlow(true), HINT_MS)
    return () => clearTimeout(t)
  }, [path])

  return (
    <div className="absolute inset-0 z-20 flex flex-col bg-bg/95">
      <div className="flex shrink-0 items-center gap-2 border-b border-line px-3 py-2">
        <span className="min-w-0 flex-1 truncate font-mono text-xs text-muted" title={path}>
          {middle(path)}
        </span>
        <button
          type="button"
          onClick={onCancel}
          aria-label="取消"
          title="取消打开（Esc 也行）"
          className="flex size-7 shrink-0 items-center justify-center rounded-md text-muted hover:bg-ctl hover:text-fg"
        >
          <X className="size-4" />
        </button>
      </div>
      <div className="flex min-h-0 flex-1 flex-col items-center justify-center gap-2 px-6 text-center">
        <span className="size-2 animate-pulse rounded-full bg-brand" />
        <p className="text-[13px] text-muted">读取中…</p>
        {/* 注意：JSX 里写 `**…**` 会原样显示星号（这一处踩过两次）—— 要加粗就用 <strong> */}
        {slow && (
          <p className="max-w-[320px] text-xs/relaxed text-faint">
            有点久了。跑 herdr 那台机器可能在等一个<strong className="text-muted">系统授权</strong> ——
            macOS 第一次访问「下载 / 桌面 / 文档」会弹一个框，去点一下「允许」就好了。
          </p>
        )}
      </div>
    </div>
  )
}

/**
 * 路径太长时**从中间省略**：要紧的是文件名，掐尾正好把它掐掉。
 * 家目录前缀也缩掉（每条里都是同一段）。
 */
function middle(p: string, max = 52) {
  const s = p.replace(/^\/(?:Users|home)\/[^/]+/, '~')
  if (s.length <= max) return s
  const keep = Math.floor((max - 1) / 2)
  return `${s.slice(0, keep)}…${s.slice(s.length - keep)}`
}
