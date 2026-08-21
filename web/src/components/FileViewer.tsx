import { useCallback, useEffect, useRef, useState } from 'react'
import { Download, ExternalLink, FolderOpen, Copy, X, Send } from 'lucide-react'
import { filesApi, type FileStat, type FileText } from '@/lib/api'
import { writeClipboard } from '@/lib/clipboard'
import { usePhone } from '@/hooks/usePhone'
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
  stat, onClose, onSend, onBrowse, toast,
}: {
  /**
   * **已经 stat 过的结果**，不是一条待解析的路径 —— 解析和分流（目录直接进文件浏览）
   * 在 App 的 openPath 里做完了。所以这儿不会拿到目录，也不用先转一圈圈再显示内容。
   */
  stat: FileStat
  onClose: () => void
  /** 把绝对路径投进发件箱 / 终端 —— 和「传图」是同一个模型：agent 自己去读磁盘 */
  onSend: (absPath: string) => void
  /** 打开所在目录（切到文件浏览面板） */
  onBrowse: (dir: string) => void
  toast: (m: string) => void
}) {
  const info = stat.info
  const [url, setUrl] = useState(stat.url)
  const [text, setText] = useState<FileText | null>(null)
  const [err, setErr] = useState<string | null>(null)
  const [zoom, setZoom] = useState(false)

  /**
   * **开的时候把焦点接过来。**
   *
   * 不接的话焦点还留在 xterm 那个隐藏 textarea 上（实测确实如此）——「按 Esc 关掉」
   * 就得指望这一下键在到达页面之前没被别人碰过，而路上真的有人：中文输入法的候选框
   * 会先吃一下，浏览器全屏状态也会先吃一下。那时候的表现正好是「要按两下」。
   *
   * 焦点在弹窗自己身上之后，Esc 是**这个弹窗的**按键，跟终端和输入法都没关系了。
   * 顺带也把方向键 / 空格这些从终端手里拿开 —— 看图的时候那些不该跑进 PTY。
   *
   * 关掉时把焦点还回去（接着敲键盘），但**手机上不还** —— 那一下会把系统键盘顶出来，
   * 而刚看完一张图多半不是要打字（和「跳 pane 之后不 focus」同一个道理）。
   */
  const shell = useRef<HTMLDivElement>(null)
  const phone = usePhone()
  const phoneRef = useRef(phone)
  phoneRef.current = phone
  useEffect(() => {
    const prev = document.activeElement as HTMLElement | null
    shell.current?.focus()
    return () => {
      if (!phoneRef.current && prev?.isConnected) prev.focus()
    }
  }, [])

  useEffect(() => {
    let dead = false
    setUrl(stat.url); setText(null); setErr(null); setZoom(false)
    if (info.kind !== 'text') return
    void filesApi.text(info.path).then(
      (t) => { if (!dead) setText(t) },
      (e: Error) => { if (!dead) setErr(e.message) },
    )
    return () => { dead = true }
  }, [stat, info])

  // 票过期了就换一张。只在 <img> 的 onError 里做，不定时刷 —— 多数图看两眼就关了。
  const renew = useCallback(async () => {
    try {
      setUrl((await filesApi.link(info.path)).url)
    } catch (e) {
      setErr((e as Error).message)
    }
  }, [info.path])
  const copyPath = async () => {
    if (await writeClipboard(info.path)) toast('路径已复制')
    else toast('复制不了，长按上面那行路径自己选')
  }

  return (
    <div
      ref={shell}
      // tabIndex=-1：能用脚本聚焦，但不进 Tab 顺序。outline-none 是因为这一整块
      // 拿到焦点只是为了收键盘，画一圈焦点环反而像是哪儿点错了。
      tabIndex={-1}
      role="dialog"
      aria-modal="true"
      aria-label={info.name}
      // 全局那条（App 里按「谁在上面谁先关」排）正常情况下先跑并 stopPropagation，
      // 这一条是兜底：焦点既然在这儿，Esc 就该由这儿收，不依赖全局那条的注册顺序。
      onKeyDown={(e) => {
        if (e.key === 'Escape' && !e.nativeEvent.isComposing) {
          e.preventDefault()
          e.stopPropagation()
          onClose()
        }
      }}
      className="absolute inset-0 z-20 flex flex-col bg-bg outline-none"
      data-testid="file-viewer"
    >
      <div className="flex shrink-0 items-center gap-2 border-b border-line bg-bar px-3 py-2">
        <div className="min-w-0 flex-1">
          <div className="truncate text-sm font-medium tracking-tight">{info.name}</div>
          {/* 完整路径要能看见也能选：这是「投给 agent」之外把路径拿出去的另一条路 */}
          <div className="truncate font-mono text-[11px] text-muted select-text" title={info.path}>
            {info.path}
            <span className="ml-2 text-faint">{human(info.size)}{info.mime ? ` · ${info.mime}` : ''}</span>
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-1">
          <Button variant="ghost" size="icon" title="复制路径" onClick={() => void copyPath()}>
            <Copy className="size-4" />
          </Button>
          {info.parent && (
            <Button variant="ghost" size="icon" title="打开所在目录" onClick={() => onBrowse(info.parent!)}>
              <FolderOpen className="size-4" />
            </Button>
          )}
          {url && (
            <Button variant="ghost" size="icon" asChild
              title={info.kind === 'image' ? '在新标签打开（那儿能长按存到相册）' : '下载'}>
              <a href={url} target="_blank" rel="noopener noreferrer">
                {info.kind === 'image' ? <ExternalLink className="size-4" /> : <Download className="size-4" />}
              </a>
            </Button>
          )}
          {/* 「投给 agent」= 把绝对路径当文本给出去。和传图完全同一个模型：
              herdr 的 socket 里没有文件通道，agent 是自己去读磁盘的。 */}
          <Button size="tiny" title="把这个路径投给 agent（它自己去读文件）" onClick={() => onSend(info.path)}>
            <Send className="size-3.5" /> 投给 agent
          </Button>
          <Button variant="ghost" size="icon" aria-label="关闭" onClick={onClose}>
            <X className="size-4" />
          </Button>
        </div>
      </div>

      <div className={cn('min-h-0 flex-1 overflow-auto overscroll-contain', info.kind === 'image' && 'grid place-items-center bg-black/20 p-2')}>
        {err && <Note bad>{err}</Note>}

        {info.kind === 'image' && url && (
          // 默认 contain 铺满可视区，点一下切到原始尺寸（外层容器负责滚动）。
          // 手机上双指缩放照样有用 —— 这个只是省掉「小图被拉伸 / 大图看不清细节」。
          <img
            src={url}
            alt={info.name}
            onError={() => void renew()}
            onClick={() => setZoom((z) => !z)}
            title={zoom ? '点一下：缩到刚好' : '点一下：看原始尺寸'}
            className={cn('cursor-zoom-in', zoom ? 'max-w-none cursor-zoom-out' : 'max-h-full max-w-full object-contain')}
          />
        )}

        {info.kind === 'text' && !err && (
          text ? (
            <>
              {text.truncated && (
                <Note>文件太大，只显示了前 {human(text.text.length)}（共 {human(text.bytes)}）。整份下载下来看。</Note>
              )}
              {/* select-text：外面那层为了终端手势整体禁了选中 */}
              <pre className="min-w-full p-3 font-mono text-xs/relaxed whitespace-pre text-fg select-text">{text.text}</pre>
            </>
          ) : <Note>读取中…</Note>
        )}

        {info.kind === 'binary' && (
          <Note>
            这是二进制文件，页面里没法预览 —— 上面那个下载按钮把它取下来。
            <br />
            （只有按魔数认出来的 png / jpg / gif / webp 才会在页面里渲染。别的一律当附件下载，
            因为从本站的源上渲染一个 agent 写的文件，就等于让它跑在这个页面里。）
          </Note>
        )}
        {info.kind === 'special' && <Note bad>这不是常规文件（设备 / socket / 管道），读它会把请求永远挂住，所以不给读。</Note>}
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
