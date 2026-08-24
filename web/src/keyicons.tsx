import type { ReactNode } from 'react'
import {
  ArrowBigUp, ArrowDown, ArrowLeft, ArrowRight, ArrowRightToLine, ArrowUp,
  Check, ChevronsDown, ChevronsUp, ChevronUp, CircleStop, ClipboardPaste,
  Command, Copy, CornerDownLeft, Delete, Eraser, Keyboard, LogOut, Maximize2,
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

const C = 'size-[15px]' // 比顶栏那些（size-4）小半格：软键上字号本来就小一档

export const KEY_ICONS = [
  { id: 'ctrl', hint: 'Ctrl（^）', icon: <ChevronUp className={C} /> },
  { id: 'alt', hint: 'Alt / Option（⌥）', icon: <Option className={C} /> },
  { id: 'shift', hint: 'Shift（⇧）', icon: <ArrowBigUp className={C} /> },
  { id: 'cmd', hint: 'Cmd（⌘）', icon: <Command className={C} /> },
  { id: 'esc', hint: 'Esc / 退出', icon: <LogOut className={C} /> },
  { id: 'enter', hint: '回车', icon: <CornerDownLeft className={C} /> },
  { id: 'tab', hint: 'Tab（⇥）', icon: <ArrowRightToLine className={C} /> },
  { id: 'space', hint: '空格', icon: <Space className={C} /> },
  { id: 'bs', hint: '退格', icon: <Delete className={C} /> },
  { id: 'del', hint: '删除', icon: <Eraser className={C} /> },
  { id: 'up', hint: '上', icon: <ArrowUp className={C} /> },
  { id: 'down', hint: '下', icon: <ArrowDown className={C} /> },
  { id: 'left', hint: '左', icon: <ArrowLeft className={C} /> },
  { id: 'right', hint: '右', icon: <ArrowRight className={C} /> },
  { id: 'dpad', hint: '方向（四个方向那一组）', icon: <Move className={C} /> },
  { id: 'pgup', hint: '上一页', icon: <ChevronsUp className={C} /> },
  { id: 'pgdn', hint: '下一页', icon: <ChevronsDown className={C} /> },
  { id: 'keyboard', hint: '键盘', icon: <Keyboard className={C} /> },
  { id: 'terminal', hint: '终端', icon: <Terminal className={C} /> },
  { id: 'close', hint: '关闭', icon: <X className={C} /> },
  { id: 'stop', hint: '中断（^C）', icon: <CircleStop className={C} /> },
  { id: 'check', hint: '确认', icon: <Check className={C} /> },
  { id: 'trash', hint: '删掉', icon: <Trash2 className={C} /> },
  { id: 'copy', hint: '复制', icon: <Copy className={C} /> },
  { id: 'paste', hint: '粘贴', icon: <ClipboardPaste className={C} /> },
  { id: 'search', hint: '搜索', icon: <Search className={C} /> },
  { id: 'refresh', hint: '刷新', icon: <RefreshCw className={C} /> },
  { id: 'undo', hint: '撤销', icon: <Undo2 className={C} /> },
  { id: 'redo', hint: '重做', icon: <Redo2 className={C} /> },
  { id: 'plus', hint: '加', icon: <Plus className={C} /> },
  { id: 'minus', hint: '减', icon: <Minus className={C} /> },
  { id: 'zoom-in', hint: '放大', icon: <ZoomIn className={C} /> },
  { id: 'zoom-out', hint: '缩小', icon: <ZoomOut className={C} /> },
  { id: 'max', hint: '全屏 / 放大 pane', icon: <Maximize2 className={C} /> },
  { id: 'min', hint: '还原', icon: <Minimize2 className={C} /> },
  { id: 'split', hint: '分屏', icon: <Split className={C} /> },
  { id: 'menu', hint: '菜单', icon: <Menu className={C} /> },
] as const satisfies readonly KeyIcon[]

export type KeyIconId = (typeof KEY_ICONS)[number]['id']

const BY_ID = new Map<string, KeyIcon>(KEY_ICONS.map((k) => [k.id, k]))

/** 这个键条上画什么：挑了图标就画图标，没挑就画名字。认不出的 id 退回名字 */
export const keyFace = (icon: string | undefined, label: string): ReactNode =>
  (icon && BY_ID.get(icon)?.icon) || label

export const keyIconHint = (icon?: string) => (icon ? BY_ID.get(icon)?.hint : undefined)
