import { useEffect, useRef } from 'react'
import { GripHorizontal, Image, RefreshCw } from 'lucide-react'
import { FOLLOW, type Pane } from '@/lib/api'
import { useFloatBox } from '@/hooks/useFloatBox'
import { Button } from './ui/button'
import { Select } from './ui/select'
import { Textarea } from './ui/textarea'
import { Checkbox } from './ui/checkbox'
import { cn } from '@/lib/utils'

/**
 * 语音投稿的发件箱。
 *
 * 这里必须是一个**真的 textarea**：终端是字节流，xterm.js 的隐藏 textarea 只把按键
 * 转成字节发走、不维护可编辑文本，所以对着网页终端说话只能「说得出、改不了」。有了
 * 真字段，选区 + 输入法提交覆盖选区（textarea 的默认行为）才能实现「框选重说」。
 *
 * 默认停靠在底部；抓左上角那个把手一拖就变成浮动面板（位置和大小记在 localStorage），
 * 右下角还能拖大小。这是给平板准备的：输入法弹出来盖住底部时，把面板拖到空地上去。
 */
/**
 * 把手离**屏幕**边至少这么远：安卓手势导航把屏幕左右各约 24dp 划给了侧滑返回 / 前进，
 * 贴着边的把手连 touchstart 都收不到。和软键条里那个常量是同一回事。
 */
const EDGE_SAFE = 28

export function Compose({
  text, onChangeText, panes, sel, onSelect, info, bad, busy, live, onLive,
  onPull, onSubmit, onReload, onAttach, onRecall, pollMs, pushMs, onLayout,
}: {
  text: string
  onChangeText: (v: string) => void
  panes: Pane[]
  sel: string
  onSelect: (v: string) => void
  info: string
  bad: boolean
  busy: boolean
  live: boolean
  onLive: (v: boolean) => void
  onPull: () => void
  onSubmit: () => void
  onReload: () => void
  onAttach: (files: FileList | File[], at: () => number) => void
  onRecall: (dir: number) => void
  pollMs: number
  pushMs: number
  /** 停靠 / 浮动切换时通知外面重排终端（浮动之后面板脱离文档流，终端能多占一块） */
  onLayout: () => void
}) {
  const { ref, box, floating, drag, dock } = useFloatBox<HTMLElement>()
  useEffect(() => { onLayout() }, [floating, onLayout])
  // 面板贴到屏幕边上时，把手和按钮都往里让 —— 那一条是安卓的侧滑区
  const offL = box ? Math.max(0, EDGE_SAFE - box.x) : 0
  const offR = box ? Math.max(0, EDGE_SAFE - (window.innerWidth - (box.x + box.w))) : 0
  const ta = useRef<HTMLTextAreaElement>(null)
  const file = useRef<HTMLInputElement>(null)
  const caret = () => ta.current?.selectionStart ?? text.length

  const agents = panes.filter((p) => p.agent)
  const shells = panes.filter((p) => !p.agent)
  const opt = (p: Pane) => (
    <option key={p.id} value={p.id}>
      {p.agent ? `${p.agent} · ` : ''}{p.workspace}/{p.tab} · {p.id}{p.title ? ` · ${p.title}` : ''}
    </option>
  )

  return (
    <section
      ref={ref as React.Ref<HTMLElement>}
      data-testid="compose"
      className={cn(
        'flex flex-col gap-1.5 border-line bg-bar px-2.5 py-[7px]',
        floating
          ? 'fixed z-20 rounded-[10px] border shadow-[0_12px_44px_rgba(0,0,0,.5)]'
          : 'shrink-0 border-t',
        busy && 'pointer-events-none opacity-60',
      )}
      style={box
        ? { left: box.x, top: box.y, width: box.w, height: box.h, paddingLeft: 10 + offL, paddingRight: 10 + offR }
        : undefined}
      onDragOver={(e) => { if ([...e.dataTransfer.types].includes('Files')) e.preventDefault() }}
      onDrop={(e) => {
        if (![...e.dataTransfer.types].includes('Files')) return
        e.preventDefault()
        onAttach(e.dataTransfer.files, caret)
      }}
    >
      <div className="flex flex-wrap items-center gap-1.5">
        {/* 把手。touch-none 是必须的：不然手指一动浏览器先把它当页面滚动吃掉 */}
        <Button
          size="tiny"
          className="shrink-0 cursor-grab touch-none px-1.5 active:cursor-grabbing"
          title="拖我：把发件箱挪到输入法盖不住的地方（双击放回底部）"
          onPointerDown={(e) => drag(e, 'move')}
          onDoubleClick={dock}
        >
          <GripHorizontal className="size-3.5" />
        </Button>
        {floating && (
          <Button size="tiny" className="shrink-0" title="放回底部" onClick={dock}>停靠</Button>
        )}
        <Select
          data-testid="compose-target"
          className="min-w-0 flex-[0_1_320px] max-md:flex-[0_0_100%]"
          value={sel}
          title="投给哪个 pane。默认跟着 herdr 里激活的那个走"
          onChange={(e) => onSelect(e.target.value)}
        >
          <option value={FOLLOW}>跟随 herdr 当前 pane</option>
          {agents.length > 0 && <optgroup label="Agent pane">{agents.map(opt)}</optgroup>}
          {shells.length > 0 && <optgroup label="Shell pane">{shells.map(opt)}</optgroup>}
        </Select>

        <Button size="tiny" title="刷新 pane 列表" onClick={onReload}><RefreshCw className="size-3" /></Button>
        <Button size="tiny" title="把远端输入框里已有的内容拉进下面的框（远端按过 Tab 补全就用它）" onClick={onPull}>
          拉回
        </Button>
        {/* accept=image/* 在手机上会同时给出「相机」和「相册」 */}
        <Button
          size="tiny"
          title="传图片：存到 herdr 那台机器上，把路径插进提示词（也能直接粘贴或拖进来）"
          onClick={() => file.current?.click()}
        >
          <Image className="size-3" />图
        </Button>
        <input
          ref={file}
          data-testid="compose-file"
          type="file"
          accept="image/*"
          multiple
          hidden
          onChange={(e) => { if (e.target.files) onAttach(e.target.files, caret); e.target.value = '' }}
        />
        <label
          className="flex shrink-0 cursor-pointer items-center gap-1.5 text-[11.5px] text-muted"
          title="本地改动跟着推回远端输入框（不回车）。只对 claude / codex 这种有真输入框的 pane 生效 —— 普通 pane 里跑的可能是 vim，那里的字符是命令不是文本。"
        >
          <Checkbox checked={live} onCheckedChange={(v) => onLive(!!v)} />双向
        </label>

        <span
          className={cn('min-w-0 flex-1 truncate text-[11.5px] max-md:order-9 max-md:flex-[0_0_100%]', bad ? 'text-bad' : 'text-muted')}
          title={`轮询 ${pollMs}ms · 双向防抖 ${pushMs}ms（URL 加 ?poll=&push= 可临时改）`}
        >
          {info}
        </span>

        <Button size="tiny" variant="primary" title="先清空远端输入行，再整段提交" onClick={onSubmit}>
          投稿 ⌘↵
        </Button>
      </div>

      <Textarea
        ref={ta}
        data-testid="compose-text"
        rows={3}
        className={cn('min-h-[62px]', floating && 'min-h-0 flex-1 resize-none')}
        spellCheck={false}
        autoComplete="off"
        value={text}
        placeholder="在这儿说话打字（用输入法的语音键）。说错的字框选重说就改掉。Enter 换行，⌘↵ / Ctrl↵ 投出去；框空时按 ↑ 取回上一条。"
        onChange={(e) => onChangeText(e.target.value)}
        onPaste={(e) => {
          const files = [...(e.clipboardData?.files ?? [])]
          if (files.length) { e.preventDefault(); onAttach(files, caret) }
        }}
        onKeyDown={(e) => {
          // Esc 不在这儿处理：它由 App 的 document 级兜底统一转给终端（不管焦点在哪）。
          // Enter 必须留给换行（语音口述常是多行），提交走 ⌘↵ / Ctrl↵
          if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) { e.preventDefault(); onSubmit(); return }
          if (e.key === 'ArrowUp' && !text) { e.preventDefault(); onRecall(1); return }
          if (e.key === 'ArrowDown') { e.preventDefault(); onRecall(-1) }
        }}
      />

      {/* 改大小：左右两条边 + 右下角。左右都给，是因为平板上换只手拿就得换边拖 */}
      {floating && (
        <>
          <span
            className="absolute top-2 bottom-2 w-4 cursor-ew-resize touch-none rounded-l-[10px] hover:bg-fg/10"
            style={{ left: offL }}
            title="拖左边改宽度（右边不动）"
            onPointerDown={(e) => drag(e, 'w')}
          />
          <span
            className="absolute top-2 bottom-9 w-4 cursor-ew-resize touch-none rounded-r-[10px] hover:bg-fg/10"
            style={{ right: offR }}
            title="拖右边改宽度"
            onPointerDown={(e) => drag(e, 'e')}
          />
          <span
            className="absolute bottom-0 grid size-7 cursor-nwse-resize touch-none place-items-center text-muted select-none"
            style={{ right: offR }}
            title="拖我改大小（宽 + 高）"
            onPointerDown={(e) => drag(e, 'se')}
          >
            ◢
          </span>
        </>
      )}
    </section>
  )
}
