import { useCallback, useEffect, useImperativeHandle, useRef, type Ref } from 'react'
import { CornerDownLeft } from 'lucide-react'
import { Button } from './ui/button'
import { cn } from '@/lib/utils'
import type { UploadResult } from '@/lib/api'

/**
 * 发件箱：**图片 chip 直接住在输入框里**那一版（`contenteditable`）。
 *
 * 纯 `textarea` 那一版在 `Compose.tsx`，设置里能切回去（`composeRich`）。
 *
 * # 为什么这一版存在，以及为什么它有风险
 *
 * 用户要的是三件一起：chip 在输入框**里面**、**共用文字的删除逻辑**（退格像删一个字那样
 * 把它删掉）、**点一下能预览**。`textarea` 三件都给不了 —— 它只装得下纯文本，放不了元素。
 * 所以只能上 `contenteditable`。
 *
 * 风险说清：这个项目的头号功能是**语音投稿**，而 OUTBOX.md 里写着「必须有真 textarea」——
 * 根因是输入法只往可编辑字段里提交文字。`contenteditable` 也是可编辑字段，但**没在那台
 * 手机 + 那套输入法（小米 / 讯飞）上验过**。所以设置里留了一档能切回纯 textarea：万一语音
 * 或候选词在这儿出问题，那是唯一的退路（输入框坏了就没法打字说这件事了）。
 *
 * # 三条实现上的硬规矩
 *
 * ① **这个输入框是「不受控」的。** React 绝不能在每次按键后去写它的 DOM —— 那会把光标和
 *    输入法的合成状态踩掉（中文打字打不出来）。所以 DOM 是内容的**唯一来源**，值是从 DOM
 *    读出来的；外面要改内容（清空、取历史、拉回）只能走 `ref` 上那几个命令式方法。
 *
 * ② **回车那两个判据一个都不能少**（从 textarea 那版原样搬过来）：`isComposing` 和
 *    `keyCode === 229`。中文候选词就是按回车上屏的，而安卓上不少输入法只给后者 ——
 *    漏了就是「选个词把半句话投出去」。
 *
 * ③ **粘贴只收纯文本**。`contenteditable` 默认会把富文本（带样式的 span、整段 HTML）原样
 *    塞进来，那既会弄出一堆看不见的标签，又会让「读出值」变得没法预测。
 *
 * # 值是怎么读出来的
 *
 * 走一遍子节点：文本节点取字、`<br>` 算换行、带 `data-path` 的那种元素取它那条**路径**。
 * 所以「投出去的文本」里 chip 的位置就是它在输入框里的位置 —— 人把它拖到句子中间也对。
 */

/** 外面能对这个输入框下的几个命令（它不受控，见 ①） */
export interface RichHandle {
  /** 读出此刻的内容：说的话 + 图片路径，按它们在框里的先后拼好 */
  value: () => string
  /** 整框换成这段纯文本（清空、取历史、拉回都走这儿） */
  setText: (v: string) => void
  /** 在光标处插一枚图片 chip */
  insertChip: (r: UploadResult) => void
  focus: () => void
}

export function ComposeRich({
  ref, text, onChangeText, info, bad, busy, enterSend, onSubmit, onAttach, onRecall, onPreview,
}: {
  ref?: Ref<RichHandle>
  /** 只当**外面**改内容时的信号用（清空 / 取历史 / 拉回），不是每次按键都写回来 —— 见 ① */
  text: string
  onChangeText: (v: string) => void
  info: string
  bad: boolean
  busy: boolean
  enterSend: boolean
  onSubmit: () => void
  onAttach: (files: FileList | File[]) => void
  onRecall: (dir: number) => void
  /** 点了一枚 chip：开图（走 App 那条 openPath，和终端里点路径同一个动作） */
  onPreview?: (path: string) => void
}) {
  const box = useRef<HTMLDivElement>(null)
  /** 上一次**我们自己**吐出去的纯文本。外面传进来的 text 和它不一样才算「外面改了」 */
  const mine = useRef('')

  /** 读出「说的话 + 路径」（chip 按它在框里的位置就地展开） */
  const read = useCallback((): { body: string; said: string } => {
    const el = box.current
    if (!el) return { body: '', said: '' }
    let body = ''
    let said = ''
    const walk = (n: Node) => {
      if (n.nodeType === Node.TEXT_NODE) {
        body += n.nodeValue ?? ''
        said += n.nodeValue ?? ''
        return
      }
      if (!(n instanceof HTMLElement)) return
      if (n.tagName === 'BR') {
        body += '\n'
        said += '\n'
        return
      }
      const p = n.dataset.path
      if (p) {
        // 路径两边各留一个空格：投出去是一行字，路径和话之间不能粘在一起
        body += (body && !/\s$/.test(body) ? ' ' : '') + p + ' '
        return
      }
      n.childNodes.forEach(walk)
    }
    el.childNodes.forEach(walk)
    // 路径两边各留一个空格，而人自己也可能打了空格 —— 合起来就成了两个（实测
    // `看这张 /path/x.png  里的药丸`）。**只并空格和制表符，别碰换行**：多行是有意义的。
    return { body: body.replace(/[ \t]{2,}/g, ' ').trim(), said }
  }, [])

  const emit = useCallback(() => {
    const { said } = read()
    mine.current = said
    onChangeText(said)
  }, [read, onChangeText])

  /** 把一段纯文本整框换掉。**只在外面主动要求时调**（见 ①） */
  const setText = useCallback((v: string) => {
    const el = box.current
    if (!el) return
    el.textContent = v
    mine.current = v
    // 光标放到最后 —— 取历史 / 拉回之后人下一步就是接着写
    const r = document.createRange()
    r.selectNodeContents(el)
    r.collapse(false)
    const sel = getSelection()
    sel?.removeAllRanges()
    sel?.addRange(r)
  }, [])

  const insertChip = useCallback((r: UploadResult) => {
    const el = box.current
    if (!el) return
    const chip = document.createElement('span')
    // **contentEditable=false 是「退格能整块删掉」的关键**：浏览器把它当一个不可分的
    // 原子，退格一下整枚消失 —— 这正是用户要的「共用文字的删除逻辑」。
    chip.contentEditable = 'false'
    chip.dataset.path = r.path
    chip.title = `${r.name} · 点一下预览`
    chip.className = 'mx-0.5 inline-flex max-w-[9rem] items-center gap-1 truncate rounded '
      + 'border border-brand/40 bg-brand/12 px-1 align-[-2px] text-[11.5px] text-brand'
    chip.textContent = `🖼 ${r.name}`

    const sel = getSelection()
    const at = sel && sel.rangeCount ? sel.getRangeAt(0) : null
    if (at && el.contains(at.commonAncestorContainer)) {
      at.deleteContents()
      at.insertNode(chip)
      at.setStartAfter(chip)
      at.collapse(true)
      sel!.removeAllRanges()
      sel!.addRange(at)
    } else {
      el.appendChild(chip)
    }
    el.focus()
    emit()
  }, [emit])

  useImperativeHandle(ref, () => ({
    value: () => read().body,
    setText,
    insertChip,
    focus: () => box.current?.focus(),
  }), [read, setText, insertChip])

  /**
   * 外面把文本改掉了（投完清空、取历史、拉回）才写 DOM。
   *
   * **判据是「和我们自己上次吐出去的不一样」** —— 拿 `text` 直接当受控值写回去的话，
   * 每敲一个字都会重写 DOM，光标和输入法的合成状态当场没了。
   */
  useEffect(() => {
    if (text === mine.current) return
    setText(text)
  }, [text, setText])

  return (
    <section
      data-testid="compose"
      className={cn('flex items-center gap-1.5 py-1.5 max-phone:py-1', busy && 'pointer-events-none opacity-60')}
      onDragOver={(e) => { if ([...e.dataTransfer.types].includes('Files')) e.preventDefault() }}
      onDrop={(e) => {
        if (![...e.dataTransfer.types].includes('Files')) return
        e.preventDefault()
        onAttach(e.dataTransfer.files)
      }}
    >
      <div
        ref={box}
        data-testid="compose-text"
        contentEditable
        suppressContentEditableWarning
        role="textbox"
        aria-multiline="true"
        title={info}
        // 框空着时那句话：CSS 里靠 :empty 画（见 index.css 的 compose-ph）
        data-ph={info || (enterSend ? '说话打字，回车投出去' : '说话打字，⌘↵ 投出去')}
        className={cn(
          'compose-ph h-8 min-h-0 flex-1 overflow-y-auto overscroll-contain whitespace-pre-wrap break-words',
          'rounded-md border border-line bg-ctl px-2 py-[4px] text-[13px] leading-[22px] text-fg outline-none',
          'focus:border-brand/70',
          bad && 'border-bad/70 focus:border-bad',
        )}
        spellCheck={false}
        onInput={emit}
        // chip 上点一下 = 预览。用 pointerup 而不是 click：触屏上 click 会丢
        // （见 lib/tap.ts 那段 —— 那条兜底认的是 [role=button]，这儿是输入框里的元素，够不着）
        onPointerUp={(e) => {
          const el = (e.target as HTMLElement).closest('[data-path]') as HTMLElement | null
          if (!el?.dataset.path) return
          e.preventDefault()
          onPreview?.(el.dataset.path)
        }}
        onPaste={(e) => {
          const files = [...(e.clipboardData?.files ?? [])]
          if (files.length) { e.preventDefault(); onAttach(files); return }
          // **只收纯文本**：contenteditable 默认会把整段 HTML 塞进来（见 ③）
          e.preventDefault()
          const t = e.clipboardData?.getData('text/plain') ?? ''
          if (t) document.execCommand('insertText', false, t)
        }}
        onKeyDown={(e) => {
          // Esc 不在这儿处理：它由 App 的 document 级兜底统一转给终端（不管焦点在哪）。
          if (e.key === 'Enter') {
            // **输入法还在拼字**的那一下不算 —— 两个判据都要（安卓上不少输入法只给 229）。
            // 漏了就是「选个词把半句话投出去」（见 ②）
            if (e.nativeEvent.isComposing || e.keyCode === 229) return
            if (e.metaKey || e.ctrlKey) { e.preventDefault(); onSubmit(); return }
            if (enterSend && !e.shiftKey && !e.altKey) { e.preventDefault(); onSubmit(); return }
            // 换行自己插一个 `<br>`：contenteditable 默认会造 `<div>`，那样读值时
            // 行的边界就靠猜了（而 read() 只认 br）
            e.preventDefault()
            document.execCommand('insertLineBreak')
            emit()
            return
          }
          // 取历史：↑ 只在框空时算，↓ 一路都算（和 textarea 那版一致）
          if (e.key === 'ArrowUp' && !text) { e.preventDefault(); onRecall(1); return }
          if (e.key === 'ArrowDown') { e.preventDefault(); onRecall(-1) }
        }}
      />

      <Button
        variant="primary"
        size="icon"
        className="size-8 shrink-0"
        title={enterSend ? '投稿（回车也行）' : '投稿（⌘↵ / Ctrl↵）'}
        aria-label="投稿"
        onMouseDown={(e) => e.preventDefault()} // 别把输入框的焦点抢走（键盘会收起来）
        onClick={onSubmit}
      >
        <CornerDownLeft className="size-4" />
      </Button>
    </section>
  )
}
