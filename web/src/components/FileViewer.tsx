import { lazy, Suspense, useCallback, useEffect, useRef, useState, type MutableRefObject } from 'react'
import { Download, ExternalLink, FolderOpen, Copy, X, Pencil } from 'lucide-react'
import { filesApi, type FileStat, type FileText } from '@/lib/api'
import { writeClipboard } from '@/lib/clipboard'
import { usePhone } from '@/hooks/usePhone'
import { useArm } from '@/hooks/useArm'
import { Button } from './ui/button'
import { SaveButton } from './ui/savebutton'
import { cn } from '@/lib/utils'

/** chat 那份 Markdown 组件，同一个按需加载的 chunk（不看 md 的人不用为它付首屏） */
const Markdown = lazy(() => import('./ChatMarkdown'))

/**
 * 按扩展名认 Markdown。服务端只认得出「这是文本」，是不是 md 只能看名字 ——
 * 认错的代价很小（一份 txt 被当 md 渲，最多是几个 `*` 变成了斜体），编辑时看的照旧是原文。
 */
const MD = /\.(md|markdown|mdx|mdown)$/i

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
 *
 * 文本文件能**就地改**（md / txt 这类，一个普通 textarea，不是编辑器）。三条取舍：
 *
 *   - **只有点「保存」才写盘，故意不自动保存** —— 对面正跑着 agent，它也在写这些文件；
 *     自动保存等于每敲一个字就去跟它抢一次。
 *   - **保存要核打开时的 mtime**（服务端做），对不上就是 409：agent 在你改的这几分钟里
 *     动过它。那时候让人自己挑「重新载入」还是「照样覆盖」，别替人决定谁的版本算数。
 *   - **有没存的修改时，关掉要点两下**（Esc、×、「不改了」都算关）——
 *     手机上 Esc 和 × 都离手指很近，一下误触就是白改。
 *
 * Markdown 文件**默认渲染**（和 chat 用同一个组件、同一个字号设置 `chatFont`）——
 * 手机上读一份 md 文档，要看的是排好的版，不是满屏 `##`。点编辑才回到原文。
 * 安全上和 chat 同一条：不开原始 HTML（文件多半是 agent 写的，同样是不可信文本）。
 */
export function FileViewer({
  stat, onClose, onBrowse, onOpenPath, toast, closeRef, chatFont,
}: {
  /**
   * **已经 stat 过的结果**，不是一条待解析的路径 —— 解析和分流（目录直接进文件浏览）
   * 在 App 的 openPath 里做完了。所以这儿不会拿到目录，也不用先转一圈圈再显示内容。
   */
  stat: FileStat
  onClose: () => void
  /** Markdown 里点了一条本地路径（走 App 的 openPath，和终端 / chat 同一条路） */
  onOpenPath: (p: string) => void
  /** chat 模式那个字号设置：Markdown 渲染跟着它走，不另开一档 */
  chatFont: number
  /** 打开所在目录（切到文件浏览面板） */
  onBrowse: (dir: string) => void
  toast: (m: string) => void
  /**
   * App 那条全局 Esc 走这儿关，而不是直接 setViewing(null) —— 有没存的修改时得先拦一下。
   * 查看器挂着时这里是「请求关闭」，卸掉时清成 null。
   */
  closeRef?: MutableRefObject<(() => void) | null>
}) {
  const info = stat.info
  const [url, setUrl] = useState(stat.url)
  const [text, setText] = useState<FileText | null>(null)
  const [err, setErr] = useState<string | null>(null)
  const [zoom, setZoom] = useState(false)
  /** 正在编辑时的草稿；null = 没在编辑 */
  const [draft, setDraft] = useState<string | null>(null)
  /** 上一次保存撞上 409（agent 在这期间改过文件）：保存键换成「照样覆盖」 */
  const [conflict, setConflict] = useState(false)
  const dirty = draft !== null && text !== null && draft !== text.text
  const { armed, tap, disarm } = useArm()

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
    setDraft(null); setConflict(false)
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
  /**
   * 离开这个查看器的所有路子都过这一道：有没存的修改时第一下只举起来（按钮变红、
   * 标题下面出一行字），三秒内再来一下才真走。
   */
  const leave = (go: () => void) => {
    if (!dirty || tap('leave', true)) go()
  }
  const leaveRef = useRef(leave)
  leaveRef.current = leave
  useEffect(() => {
    if (!closeRef) return
    closeRef.current = () => leaveRef.current(onClose)
    return () => { closeRef.current = null }
  }, [closeRef, onClose])
  // 关标签页 / 刷新也拦一下（浏览器自己弹那个框，这是它唯一允许的写法）
  useEffect(() => {
    if (!dirty) return
    const h = (e: BeforeUnloadEvent) => { e.preventDefault() }
    addEventListener('beforeunload', h)
    return () => removeEventListener('beforeunload', h)
  }, [dirty])
  useEffect(() => { if (!dirty) disarm() }, [dirty]) // eslint-disable-line react-hooks/exhaustive-deps

  const save = async (force = false): Promise<boolean> => {
    if (draft === null || !text) return false
    try {
      const t = await filesApi.save(info.path, draft, text.mtime, force)
      setText(t); setDraft(t.text); setConflict(false); setErr(null)
      return true
    } catch (e) {
      if ((e as { status?: number }).status === 409) {
        setConflict(true)
        setErr('这个文件在你打开之后被改过了（多半是 agent 写的）。重新载入会丢掉你这次的修改；照样覆盖会丢掉它的。')
      } else {
        setErr((e as Error).message)
      }
      return false
    }
  }
  const reload = () => {
    setErr(null); setConflict(false)
    void filesApi.text(info.path).then(
      (t) => { setText(t); setDraft(t.text) },
      (e: Error) => setErr(e.message),
    )
  }
  const cancelEdit = () => leave(() => { setDraft(null); setConflict(false); setErr(null) })

  // md 里的相对链接按**这个文件所在目录**解，不按哪个 pane 的 cwd —— 文档里写
  // `./img/a.png` 指的就是它旁边那个
  const openRel = useCallback((p: string) => {
    if (p.startsWith('/') || p.startsWith('~') || !info.parent) return onOpenPath(p)
    const out = info.parent.split('/')
    for (const seg of p.split('/')) {
      if (seg === '..') out.pop()
      else if (seg && seg !== '.') out.push(seg)
    }
    onOpenPath(out.join('/') || '/')
  }, [info.parent, onOpenPath])

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
          leave(onClose)
        }
        // ⌘S / Ctrl+S：编辑时存盘，顺手挡掉浏览器那个「另存网页」
        if (draft !== null && (e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 's') {
          e.preventDefault()
          void save(conflict).then((ok) => { if (ok) toast('已保存') })
        }
      }}
      className="absolute inset-0 z-20 flex flex-col bg-bg outline-none"
      data-testid="file-viewer"
    >
      <div className="flex shrink-0 items-center gap-2 border-b border-line bg-bar px-3 py-2">
        <div className="min-w-0 flex-1">
          <div className="truncate text-sm font-medium tracking-tight">{info.name}</div>
          {/* 完整路径要能看见也能选：把路径拿出去（贴给 agent、敲进终端）靠它和复制按钮 */}
          <div className="truncate font-mono text-[11px] text-muted select-text" title={info.path}>
            {info.path}
            <span className="ml-2 text-faint">{human(text?.bytes ?? info.size)}{info.mime ? ` · ${info.mime}` : ''}</span>
          </div>
          {armed === 'leave' && <div className="truncate text-xs text-bad">有没保存的修改 —— 再点一次放弃</div>}
        </div>
        {draft !== null ? (
          <div className="flex shrink-0 items-center gap-1">
            <Button size="tiny" variant={armed === 'leave' ? 'destructive' : 'default'} onClick={cancelEdit}>
              {armed === 'leave' ? '放弃修改' : '不改了'}
            </Button>
            {conflict ? (
              <>
                <Button size="tiny" onClick={reload}>重新载入</Button>
                <SaveButton size="tiny" variant="danger" onSave={() => save(true)}>照样覆盖</SaveButton>
              </>
            ) : (
              <SaveButton size="tiny" disabled={!dirty} onSave={() => save()}>保存</SaveButton>
            )}
            <Button variant={armed === 'leave' ? 'destructive' : 'ghost'} size="icon" aria-label="关闭" onClick={() => leave(onClose)}>
              <X className="size-4" />
            </Button>
          </div>
        ) : (
        <div className="flex shrink-0 items-center gap-1">
          {info.kind === 'text' && text && !text.truncated && (
            <Button variant="ghost" size="icon" title="编辑（改完点保存，不会自动存）"
              onClick={() => { setDraft(text.text); setErr(null) }}>
              <Pencil className="size-4" />
            </Button>
          )}
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
              title={info.kind === 'image' || info.kind === 'video' ? '在新标签打开（那儿能长按存到相册）' : '下载'}>
              <a href={url} target="_blank" rel="noopener noreferrer">
                {info.kind === 'image' || info.kind === 'video' ? <ExternalLink className="size-4" /> : <Download className="size-4" />}
              </a>
            </Button>
          )}
          <Button variant="ghost" size="icon" aria-label="关闭" onClick={onClose}>
            <X className="size-4" />
          </Button>
        </div>
        )}
      </div>

      <div className={cn('min-h-0 flex-1 overflow-auto overscroll-contain', (info.kind === 'image' || info.kind === 'video') && 'grid place-items-center bg-black/20 p-2', draft !== null && 'flex flex-col')}>
        {err && <Note bad>{err}</Note>}

        {info.kind === 'image' && url && (
          // 默认 contain 铺满可视区，点一下切到原始尺寸（外层容器负责滚动）。
          // 手机上双指缩放照样有用 —— 这个只是省掉「小图被拉伸 / 大图看不清细节」。
          //
          // **SVG 走 `<img>` 是有安全含义的，别改成内联 `<svg>` 或 innerHTML**：
          // `<img>` 里的 SVG 是规范规定的 secure static mode（脚本不跑、外部资源不加载），
          // 而内联进 DOM 的 SVG 是本页面的一部分，agent 写的那段脚本就跑在我们的源上。
          //
          // 尺寸也得分开：SVG 常常不带 width/height（只有 viewBox），那时候 `<img>` 会
          // 退回 300×150 的默认替换元素尺寸 —— 一张图表缩成邮票大。给它撑满容器再
          // contain，有 viewBox 就会按比例放大。
          <img
            src={url}
            alt={info.name}
            onError={() => void renew()}
            onClick={() => setZoom((z) => !z)}
            title={zoom ? '点一下：缩到刚好' : '点一下：看原始尺寸'}
            className={cn(
              'cursor-zoom-in',
              zoom
                ? 'max-w-none cursor-zoom-out'
                : cn('object-contain', info.mime === 'image/svg+xml' ? 'h-full w-full' : 'max-h-full max-w-full'),
            )}
          />
        )}

        {info.kind === 'video' && url && (
          // 走短时签名链接（和图一样），服务端按魔数给真 MIME + 支持 Range —— 拖进度条、
          // iOS 那种分段请求都靠它。**`playsInline` 别去掉**：iPhone 上没它会一点就强制全屏，
          // 退出来整个查看器的状态就丢了。`preload="metadata"`：几百 MB 的录屏别一打开就整份拉
          // （这条是穿隧道走蜂窝网络的），先拿到时长和首帧，点播放才真的读
          <video
            src={url}
            controls
            playsInline
            preload="metadata"
            onError={() => void renew()}
            className="max-h-full max-w-full"
          />
        )}

        {info.kind === 'text' && draft !== null && (
          <textarea
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            autoFocus={!phone}
            spellCheck={false}
            autoCapitalize="off"
            autoCorrect="off"
            aria-label={`编辑 ${info.name}`}
            // 编辑时软折行（和只读那份 <pre> 不一样）：手机上改一段 md，横着滑找光标太难受
            className="block min-h-0 w-full flex-1 resize-none bg-bg p-3 font-mono text-xs/relaxed text-fg outline-none select-text"
          />
        )}

        {info.kind === 'text' && draft === null && !err && (
          text ? (
            <>
              {text.truncated && (
                <Note>文件太大，只显示了前 {human(text.text.length)}（共 {human(text.bytes)}）。整份下载下来看。</Note>
              )}
              {MD.test(info.name) ? (
                <Suspense fallback={<Note>读取中…</Note>}>
                  {/* select-text：外面那层为了终端手势整体禁了选中 */}
                  <div className="mx-auto max-w-3xl px-4 py-4 leading-[1.75] text-fg select-text" style={{ fontSize: `${chatFont}px` }}>
                    <Markdown text={text.text} onPath={openRel} variant="doc" />
                  </div>
                </Suspense>
              ) : (
              /* select-text：外面那层为了终端手势整体禁了选中 */
              <pre className="min-w-full p-3 font-mono text-xs/relaxed whitespace-pre text-fg select-text">{text.text}</pre>
              )}
            </>
          ) : <Note>读取中…</Note>
        )}

        {info.kind === 'binary' && (
          <Note>
            这是二进制文件，页面里没法预览 —— 上面那个下载按钮把它取下来。
            <br />
            （只有认出来的图和视频才会在页面里放：png / jpg / gif / webp / mp4 / mov / webm 按魔数认，SVG 按开头认。
            别的一律当附件下载 —— 从本站的源上渲染一个 agent 写的文件，就等于让它跑在这个页面里。）
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
