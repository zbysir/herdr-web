import type { ReactNode } from 'react'
import {
  ArrowBigUp, ArrowDown, ArrowLeft, ArrowRight, ArrowRightToLine, ArrowUp,
  Check, ChevronsDown, ChevronsUp, ChevronUp, CircleStop, ClipboardPaste,
  Command, Copy, CornerDownLeft, Delete, Eraser, LogOut, Maximize2,
  Menu, Minimize2, Minus, Move, Option, Plus, RefreshCw, Redo2, Search, Space,
  Split, Terminal, Trash2, Undo2, X, ZoomIn, ZoomOut,
} from 'lucide-react'

/**
 * 软键上能挑的**内置图标** —— 前端这一半（画什么）。
 *
 * 服务端那一半是白名单（`internal/softkeys/icons.go`），因为它要参与**存盘校验**。
 * 两边靠 id 对上，**顺序也必须一样**，有测试盯着（`softkeys.TestIconsMatchJS`）。
 *
 * 为什么要这一档：`Key.Label` 是自由文本，于是「键盘」这种键只能靠字形（`⌨`）—— 而那些
 * 符号字形在很多字体里压根缺（缺了就是一个方框，index.css 里那串符号字体兜底就是为它加的）、
 * 有的字体里很难看、大小和基线还跟旁边的字母对不齐。图标是 SVG，三个问题一起没了。
 *
 * **Label 仍然是名字**（编辑器里认它、组键靠它、title 里显示它）；Icon 只决定**条上画什么**。
 * 不是二选一：挑了图标，名字还在，只是条上不画字。
 *
 * 那个 Go 测试是拿正则从这个文件里抠 id 的（认「花括号 + id + 引号」这个开头），所以下面
 * 每一条都得让 id 排在最前面、整条写在一行里。
 */
export interface KeyIcon {
  id: string
  /** 选择器里那个格子的 title（说明它一般用来干什么） */
  hint: string
  icon: ReactNode
}

/**
 * 软键上图标的尺寸和描边。
 *
 * 18px + 1.75 描边，不是 15px + lucide 默认的 2 —— **小尺寸上细节会糊**。真机上
 * （截图为证）15px 的 lucide `Keyboard` 内部那八九个小点全糊成一团，看着就是一块脏东西。
 * 尺寸抬一档、线细一档，同一批图标立刻读得出来。
 */
const C = 'size-[18px]'
const SW = 1.75

/**
 * 键盘：**自己画的**，不用 lucide 那个。
 *
 * lucide 的 `Keyboard` 是「外框 + 内部八九个小键」，那个细节量在 18px 上照样糊
 * （用户连着说了两次「太丑」）。这儿只留两个特征：一个圆角外框 + 一条空格键 ——
 * 少即是清楚，小尺寸上一眼认得出是键盘。
 *
 * 别往里加小键点：24 的画布上每个点不到 1px，缩到 18px 就是噪点。
 */
const KeyboardGlyph = ({ className }: { className?: string }) => (
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={SW}
       strokeLinecap="round" strokeLinejoin="round" className={className} aria-hidden>
    <rect x="2.25" y="6.25" width="19.5" height="11.5" rx="2.5" />
    <path d="M8.5 14.25h7" />
  </svg>
)

export const KEY_ICONS = [
  { id: 'ctrl', hint: 'Ctrl（^）', icon: <ChevronUp className={C} strokeWidth={SW} /> },
  { id: 'alt', hint: 'Alt / Option（⌥）', icon: <Option className={C} strokeWidth={SW} /> },
  { id: 'shift', hint: 'Shift（⇧）', icon: <ArrowBigUp className={C} strokeWidth={SW} /> },
  { id: 'cmd', hint: 'Cmd（⌘）', icon: <Command className={C} strokeWidth={SW} /> },
  { id: 'esc', hint: 'Esc / 退出', icon: <LogOut className={C} strokeWidth={SW} /> },
  { id: 'enter', hint: '回车', icon: <CornerDownLeft className={C} strokeWidth={SW} /> },
  { id: 'tab', hint: 'Tab（⇥）', icon: <ArrowRightToLine className={C} strokeWidth={SW} /> },
  { id: 'space', hint: '空格', icon: <Space className={C} strokeWidth={SW} /> },
  { id: 'bs', hint: '退格', icon: <Delete className={C} strokeWidth={SW} /> },
  { id: 'del', hint: '删除', icon: <Eraser className={C} strokeWidth={SW} /> },
  { id: 'up', hint: '上', icon: <ArrowUp className={C} strokeWidth={SW} /> },
  { id: 'down', hint: '下', icon: <ArrowDown className={C} strokeWidth={SW} /> },
  { id: 'left', hint: '左', icon: <ArrowLeft className={C} strokeWidth={SW} /> },
  { id: 'right', hint: '右', icon: <ArrowRight className={C} strokeWidth={SW} /> },
  { id: 'dpad', hint: '方向（四个方向那一组）', icon: <Move className={C} strokeWidth={SW} /> },
  { id: 'pgup', hint: '上一页', icon: <ChevronsUp className={C} strokeWidth={SW} /> },
  { id: 'pgdn', hint: '下一页', icon: <ChevronsDown className={C} strokeWidth={SW} /> },
  { id: 'keyboard', hint: '键盘', icon: <KeyboardGlyph className={C} /> },
  { id: 'terminal', hint: '终端', icon: <Terminal className={C} strokeWidth={SW} /> },
  { id: 'close', hint: '关闭', icon: <X className={C} strokeWidth={SW} /> },
  { id: 'stop', hint: '中断（^C）', icon: <CircleStop className={C} strokeWidth={SW} /> },
  { id: 'check', hint: '确认', icon: <Check className={C} strokeWidth={SW} /> },
  { id: 'trash', hint: '删掉', icon: <Trash2 className={C} strokeWidth={SW} /> },
  { id: 'copy', hint: '复制', icon: <Copy className={C} strokeWidth={SW} /> },
  { id: 'paste', hint: '粘贴', icon: <ClipboardPaste className={C} strokeWidth={SW} /> },
  { id: 'search', hint: '搜索', icon: <Search className={C} strokeWidth={SW} /> },
  { id: 'refresh', hint: '刷新', icon: <RefreshCw className={C} strokeWidth={SW} /> },
  { id: 'undo', hint: '撤销', icon: <Undo2 className={C} strokeWidth={SW} /> },
  { id: 'redo', hint: '重做', icon: <Redo2 className={C} strokeWidth={SW} /> },
  { id: 'plus', hint: '加', icon: <Plus className={C} strokeWidth={SW} /> },
  { id: 'minus', hint: '减', icon: <Minus className={C} strokeWidth={SW} /> },
  { id: 'zoom-in', hint: '放大', icon: <ZoomIn className={C} strokeWidth={SW} /> },
  { id: 'zoom-out', hint: '缩小', icon: <ZoomOut className={C} strokeWidth={SW} /> },
  { id: 'max', hint: '全屏 / 放大 pane', icon: <Maximize2 className={C} strokeWidth={SW} /> },
  { id: 'min', hint: '还原', icon: <Minimize2 className={C} strokeWidth={SW} /> },
  { id: 'split', hint: '分屏', icon: <Split className={C} strokeWidth={SW} /> },
  { id: 'menu', hint: '菜单', icon: <Menu className={C} strokeWidth={SW} /> },
] as const satisfies readonly KeyIcon[]

export type KeyIconId = (typeof KEY_ICONS)[number]['id']

const BY_ID = new Map<string, KeyIcon>(KEY_ICONS.map((k) => [k.id, k]))

/** 这个键条上画什么：挑了图标就画图标，没挑就画名字。认不出的 id 退回名字 */
export const keyFace = (icon: string | undefined, label: string): ReactNode =>
  (icon && BY_ID.get(icon)?.icon) || label

export const keyIconHint = (icon?: string) => (icon ? BY_ID.get(icon)?.hint : undefined)
