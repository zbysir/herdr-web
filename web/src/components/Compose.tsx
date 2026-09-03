import { useRef } from 'react'
import { CornerDownLeft } from 'lucide-react'
import { Button } from './ui/button'
import { Textarea } from './ui/textarea'
import { cn } from '@/lib/utils'

/**
 * 语音投稿的发件箱：**一行**输入框 + 一个投稿键，就这两样。
 *
 * 这里必须是一个**真的 textarea**：终端是字节流，xterm.js 的隐藏 textarea 只把按键
 * 转成字节发走、不维护可编辑文本，所以对着网页终端说话只能「说得出、改不了」。有了
 * 真字段，选区 + 输入法提交覆盖选区（textarea 的默认行为）才能实现「框选重说」。
 *
 * ## 为什么只剩一行
 *
 * 它长在终端下面，占的每一像素都是从终端那儿借的。原来这儿是四行 ——「投给哪个 pane」
 * 的下拉、一排按钮（刷新 / 拉回 / 图 / 双向 / 投稿）、一行状态、三行高的输入框 ——
 * 手机竖屏上加起来吃掉小半个屏幕，而那时候屏幕上最该看见的是 agent 正在写什么
 * （用户报的：「整个终端的位置都占用看不到什么东西」）。
 *
 * 去掉的那几件不是删了，是挪到了不占高度的地方：
 *
 *   - **投给谁** —— 一直跟随 herdr 里激活的那个 pane（原来那个下拉的默认值就是它），
 *     换目标去「面板一览」里点一下就是了。框里有自己写的草稿时照旧锁定在当初瞄准的
 *     那个 pane 上（见 useCompose 的 aimed），这条没变。
 *   - **传图** —— 顶栏 / 快捷键条上的「传图」（`img`），传完把路径接到草稿末尾。
 *     这里仍然收**粘贴和拖进来**的图（那两条不占地方，而且能插在光标处）。
 *   - **拉回 / 双向同步** —— 拉回成了一件可以放上顶栏的事（`pull`），双向搬进了
 *     设置 →「终端」。两个都是偶尔用一次的东西，不值得一直占着一行。
 *   - **状态那一行** —— 挪进了 placeholder（框空着的时候才需要知道「这会儿投给谁」）
 *     和 title；出错时框自己变红。
 *
 * **不做自动长高。** 高度一变就是 Dock 变高 → 终端重排 → SIGWINCH + 冻帧（见 CLAUDE.md
 * 「改尺寸会闪一下全黑」），说一句话闪好几次。字多了就在框里上下滚，浏览器自己会把光标
 * 那一行滚出来。
 *
 * 位置和宽度不归它自己管：它是**底部面板**（见 Dock）里的一块，和快捷键条共用一套边框
 * 和左右宽度。
 */
export function Compose({
  text, onChangeText, info, bad, busy, enterSend, onSubmit, onAttach, onRecall,
}: {
  text: string
  onChangeText: (v: string) => void
  /** 这会儿投给谁、远端什么状态。框空时当 placeholder 用 */
  info: string
  bad: boolean
  busy: boolean
  /** 回车就投（设置里那一档）。关着的时候回车是换行，投稿走 ⌘↵ / Ctrl↵ */
  enterSend: boolean
  onSubmit: () => void
  onAttach: (files: FileList | File[], at: () => number) => void
  onRecall: (dir: number) => void
}) {
  const ta = useRef<HTMLTextAreaElement>(null)
  const caret = () => ta.current?.selectionStart ?? text.length

  return (
    <section
      data-testid="compose"
      className={cn('flex items-center gap-1.5 py-1.5 max-phone:py-1', busy && 'pointer-events-none opacity-60')}
      onDragOver={(e) => { if ([...e.dataTransfer.types].includes('Files')) e.preventDefault() }}
      onDrop={(e) => {
        if (![...e.dataTransfer.types].includes('Files')) return
        e.preventDefault()
        onAttach(e.dataTransfer.files, caret)
      }}
    >
      {/* 一行高（h-8 = 快捷键条上一个键的高度）：行高 22px + 上下各 4px + 两道边 = 32px。
          resize-none —— 右下角那个拖角在这个高度上正好压在文字上，而拖高了也只是把终端
          挤掉一截（要挪地方是拖 Dock 的把手，不是拖这个框） */}
      <Textarea
        ref={ta}
        data-testid="compose-text"
        rows={1}
        className={cn(
          'h-8 min-h-0 flex-1 resize-none overflow-y-auto overscroll-contain py-[4px] leading-[22px]',
          // 出错了得看得见（状态那一行没了）。具体哪儿错了在 title 和 placeholder 里
          bad && 'border-bad/70 focus:border-bad',
        )}
        spellCheck={false}
        autoComplete="off"
        value={text}
        title={info}
        // 框空着的时候才谈得上「等会儿投给谁」，所以状态就住在这儿。还没问出来时
        // （刚开页面那一下）退回一句用法
        placeholder={info || (enterSend ? '说话打字，回车投出去' : '说话打字，⌘↵ 投出去')}
        onChange={(e) => onChangeText(e.target.value)}
        onPaste={(e) => {
          const files = [...(e.clipboardData?.files ?? [])]
          if (files.length) { e.preventDefault(); onAttach(files, caret) }
        }}
        onKeyDown={(e) => {
          // Esc 不在这儿处理：它由 App 的 document 级兜底统一转给终端（不管焦点在哪）。
          if (e.key === 'Enter') {
            // **输入法还在拼字**的那一下不算：中文候选词就是按回车上屏的（安卓上很多
            // 输入法只给 keyCode 229，两个判据都要），拦掉的话「回车发送」一开，
            // 选个词就把半句话投出去了
            if (e.nativeEvent.isComposing || e.keyCode === 229) return
            if (e.metaKey || e.ctrlKey) { e.preventDefault(); onSubmit(); return }
            // ⇧↵ 永远是换行 —— 「回车发送」这一档下它是唯一能打出多行的办法
            if (enterSend && !e.shiftKey && !e.altKey) { e.preventDefault(); onSubmit() }
            return
          }
          // 取历史：↑ 只在框空时算（框里有字时它是「把光标挪上一行」），而 ↓ 一路都算 ——
          // 取回一条之后框里就有字了，再要求「空」的话人就卡在历史里出不来
          if (e.key === 'ArrowUp' && !text) { e.preventDefault(); onRecall(1); return }
          if (e.key === 'ArrowDown') { e.preventDefault(); onRecall(-1) }
        }}
      />

      {/* 键盘上按得到，为什么还留这个键：回车发送那一档下它是这个框**唯一**看得出来的
          出口（否则界面上没有任何东西说明这行字会去哪儿），而它只占一格 32px。
          饱和绿是「一屏一个的主操作」那一档，见 CLAUDE.md 配色那节 */}
      <Button
        data-testid="compose-send"
        size="icon"
        variant="primary"
        className="shrink-0"
        aria-label="投稿"
        title={enterSend ? '投稿（回车也行；⇧↵ 换行）' : '投稿 ⌘↵ / Ctrl↵（回车是换行）'}
        onClick={onSubmit}
      >
        <CornerDownLeft className="size-4" />
      </Button>
    </section>
  )
}
