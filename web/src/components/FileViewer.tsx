import { useCallback, useEffect, useState } from 'react'
import { Download, ExternalLink, FolderOpen, Copy, X, Send } from 'lucide-react'
import { filesApi, type FileStat, type FileText } from '@/lib/api'
import { writeClipboard } from '@/lib/clipboard'
import { Button } from './ui/button'
import { cn } from '@/lib/utils'

/**
 * 看一个文件。铺满整屏（不是浮层）—— 主要用途是**看 agent 刚生成的那张图**，
 * 手机上一张图值得整块屏幕。
 *
 * 图片走的是服务端那条 `/_f/<票>` 短时链接，不是 blob：
 *
 *   - `<img src>` 设不了 CSRF 头，走 /api 一律 403（这是做这个功能第一个撞上的墙）；
 *   - 用真 URL 而不是 blob，才能「在新标签打开」「长按存到相册」「拖出去」——
 *     这些在手机上比什么都实用，而 blob: 在 iOS 上这几条都不太行。
 *
 * 代价是票会过期（十几分钟）。所以 `<img>` 的 onError 里换一张票再试一次 —— 开着
 * 看了半天再切回来时，图不该变成一个碎图标。
 */
export function FileViewer({
  path, base, onClose, onSend, onBrowse, toast,
}: {
  /** 要打开的路径，可能是相对的（终端里点出来的那种） */
  path: string
  /** 相对路径的解析基准：那个 pane 的 cwd */
  base?: string
  onClose: () => void
  /** 把绝对路径投进发件箱 / 终端 —— 和「传图」是同一个模型：agent 自己去读磁盘 */
  onSend: (absPath: string) => void
  /** 打开所在目录（切到文件浏览面板） */
  onBrowse: (dir: string) => void
  toast: (m: string) => void
}) {
  const [stat, setStat] = useState<FileStat | null>(null)
  const [text, setText] = useState<FileText | null>(null)
  const [err, setErr] = useState<string | null>(null)
  const [zoom, setZoom] = useState(false)

  useEffect(() => {
    let dead = false
    setStat(null); setText(null); setErr(null); setZoom(false)
    void (async () => {
      try {
        const s = await filesApi.stat(path, base)
        if (dead) return
        setStat(s)
        if (s.info.kind === 'text') {
          const t = await filesApi.text(s.info.path)
          if (!dead) setText(t)
        }
      } catch (e) {
        if (!dead) setErr((e as Error).message)
      }
    })()
    return () => { dead = true }
  }, [path, base])

  // Esc 关掉。用捕获阶段是因为 App 那边也在捕获阶段兜 Esc（会转发给终端）——
  // 这一层在上面，得先把它吃掉。
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape' || e.isComposing) return
      e.preventDefault(); e.stopPropagation()
      onClose()
    }
    addEventListener('keydown', onKey, true)
    return () => removeEventListener('keydown', onKey, true)
  }, [onClose])

  // 票过期了就换一张。只在 onError 里做，不定时刷 —— 多数图看两眼就关了。
  const renew = useCallback(async () => {
    if (!stat) return
    try {
      const l = await filesApi.link(stat.info.path)
      setStat((s) => (s ? { ...s, url: l.url, expires: l.expires } : s))
    } catch (e) {
      setErr((e as Error).message)
    }
  }, [stat])

  const info = stat?.info
  const copyPath = async () => {
    if (!info) return
    if (await writeClipboard(info.path)) toast('路径已复制')
    else toast('复制不了，长按上面那行路径自己选')
  }

  return (
    <div className="absolute inset-0 z-20 flex flex-col bg-bg" data-testid="file-viewer">
      <div className="flex shrink-0 items-center gap-2 border-b border-line bg-bar px-3 py-2">
        <div className="min-w-0 flex-1">
          <div className="truncate text-sm font-medium tracking-tight">{info?.name ?? path.split('/').pop()}</div>
          {/* 完整路径要能看见也能选：这是「投给 agent」之外把路径拿出去的另一条路 */}
          <div className="truncate font-mono text-[11px] text-muted select-text" title={info?.path ?? path}>
            {info?.path ?? path}
            {info && <span className="ml-2 text-faint">{human(info.size)}{info.kind === 'image' && info.mime ? ` · ${info.mime}` : ''}</span>}
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-1">
          {info && (
            <>
              <Button variant="ghost" size="icon" title="复制路径" onClick={() => void copyPath()}>
                <Copy className="size-4" />
              </Button>
              {info.parent && (
                <Button variant="ghost" size="icon" title="打开所在目录" onClick={() => onBrowse(info.parent!)}>
                  <FolderOpen className="size-4" />
                </Button>
              )}
              {stat?.url && (
                <Button variant="ghost" size="icon" asChild
                  title={info.kind === 'image' ? '在新标签打开（那儿能长按存到相册）' : '下载'}>
                  <a href={stat.url} target="_blank" rel="noopener noreferrer">
                    {info.kind === 'image' ? <ExternalLink className="size-4" /> : <Download className="size-4" />}
                  </a>
                </Button>
              )}
              {/* 「投给 agent」= 把绝对路径当文本给出去。和传图完全同一个模型：
                  herdr 的 socket 里没有文件通道，agent 是自己去读磁盘的。 */}
              <Button size="tiny" title="把这个路径投给 agent（它自己去读文件）" onClick={() => onSend(info.path)}>
                <Send className="size-3.5" /> 投给 agent
              </Button>
            </>
          )}
          <Button variant="ghost" size="icon" aria-label="关闭" onClick={onClose}>
            <X className="size-4" />
          </Button>
        </div>
      </div>

      <div className={cn('min-h-0 flex-1 overflow-auto overscroll-contain', info?.kind === 'image' && 'grid place-items-center bg-black/20 p-2')}>
        {err && <Note bad>{err}</Note>}
        {!err && !stat && <Note>读取中…</Note>}

        {info?.kind === 'image' && stat?.url && (
          // 默认 contain 铺满可视区，点一下切到原始尺寸（外层容器负责滚动）。
          // 手机上双指缩放照样有用 —— 这个只是省掉「小图被拉伸 / 大图看不清细节」。
          <img
            src={stat.url}
            alt={info.name}
            onError={() => void renew()}
            onClick={() => setZoom((z) => !z)}
            title={zoom ? '点一下：缩到刚好' : '点一下：看原始尺寸'}
            className={cn('cursor-zoom-in', zoom ? 'max-w-none cursor-zoom-out' : 'max-h-full max-w-full object-contain')}
          />
        )}

        {info?.kind === 'text' && text && (
          <>
            {text.truncated && (
              <Note>文件太大，只显示了前 {human(text.text.length)}（共 {human(text.bytes)}）。整份下载下来看。</Note>
            )}
            {/* select-text：外面那层为了终端手势整体禁了选中 */}
            <pre className="min-w-full p-3 font-mono text-xs/relaxed whitespace-pre text-fg select-text">{text.text}</pre>
          </>
        )}

        {info?.kind === 'binary' && (
          <Note>
            这是二进制文件，页面里没法预览 —— 上面那个下载按钮把它取下来。
            <br />
            （只有按魔数认出来的 png / jpg / gif / webp 才会在页面里渲染。别的一律当附件下载，
            因为从本站的源上渲染一个 agent 写的文件，就等于让它跑在这个页面里。）
          </Note>
        )}
        {info?.kind === 'special' && <Note bad>这不是常规文件（设备 / socket / 管道），读它会把请求永远挂住，所以不给读。</Note>}
        {info?.dir && (
          <Note>
            这是个目录。
            <Button size="tiny" className="ml-2" onClick={() => onBrowse(info.path)}>在文件浏览里打开</Button>
          </Note>
        )}
      </div>
    </div>
  )
}

function Note({ children, bad }: { children: React.ReactNode; bad?: boolean }) {
  return <p className={cn('p-4 text-xs/relaxed', bad ? 'text-bad' : 'text-muted')}>{children}</p>
}

/** 字节数写成人看的。1024 进制，和 `ls -lh` 对得上 */
export function human(n: number) {
  if (n < 1024) return `${n} B`
  const u = ['KB', 'MB', 'GB', 'TB']
  let v = n / 1024
  let i = 0
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++ }
  return `${v < 10 ? v.toFixed(1) : Math.round(v)} ${u[i]}`
}
