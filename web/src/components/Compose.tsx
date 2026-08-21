import { useRef } from 'react'
import { Image, RefreshCw } from 'lucide-react'
import { FOLLOW, type Pane } from '@/lib/api'
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
 * 位置和宽度都不归它自己管：它是**底部面板**（见 Dock）里的一块，和软键条共用一套边框
 * 和左右宽度。以前它能抓着 ⠿ 从底部撕下来变成浮动面板（自己一套位置 / 大小 / 边框），
 * 和底下的软键条叠成错位的两层 —— 现在整块一起挪、一起缩，只调一次。
 *
 * 里面这排控件按**面板宽度**折行（`@max-3xl:`，容器查询），不是按视口宽度：面板缩到
 * 半屏之后视口还是那么宽，按视口算的话这排会挤成一团。
 */
export function Compose({
  text, onChangeText, panes, sel, onSelect, info, bad, busy, live, onLive,
  onPull, onSubmit, onReload, onAttach, onRecall, pollMs, pushMs,
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
}) {
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
      data-testid="compose"
      // 手机竖屏上纵向再挤一点：这一块和软键条加起来占的每一像素都是从终端那儿借的
      className={cn('flex flex-col gap-1.5 py-[7px] max-phone:gap-1 max-phone:py-1', busy && 'pointer-events-none opacity-60')}
      onDragOver={(e) => { if ([...e.dataTransfer.types].includes('Files')) e.preventDefault() }}
      onDrop={(e) => {
        if (![...e.dataTransfer.types].includes('Files')) return
        e.preventDefault()
        onAttach(e.dataTransfer.files, caret)
      }}
    >
      <div className="flex flex-wrap items-center gap-1.5">
        <Select
          data-testid="compose-target"
          className="min-w-0 flex-[0_1_320px] @max-3xl:flex-[0_0_100%]"
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
          className={cn(
            'min-w-0 flex-1 truncate text-[11.5px] @max-3xl:order-9 @max-3xl:flex-[0_0_100%]',
            bad ? 'text-bad' : 'text-muted',
          )}
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
        className="min-h-[62px]"
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
    </section>
  )
}
