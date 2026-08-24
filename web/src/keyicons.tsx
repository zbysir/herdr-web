import type { ReactNode } from 'react'
import {
  ArrowBigUp, ArrowDown, ArrowLeft, ArrowRight, ArrowRightToLine, ArrowUp,
  Check, ChevronsDown, ChevronsUp, ChevronUp, CircleStop, CircleX, ClipboardPaste, Contrast,
  Command, Compass, Copy, CornerDownLeft, Delete, Eraser, FolderOpen, Image, LayoutGrid,
  LogOut, Maximize2, Menu, Minimize2, Minus, Option, Pencil, Plus, RefreshCw,
  Redo2, Search, Settings, Space, Split, Terminal, Trash2, Undo2, X, ZoomIn, ZoomOut,
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
 *
 * **`esc` 为什么是 ⊗ 而不是键盘上那个 `⎋`**：`⎋`（缺口圆 + 朝左上的箭头）试了两版，
 * 圆大了两者糊在一起、圆小了整体读成「撤销」那个 ↺ —— 16px 放不下「圆 + 分离的箭头」
 * 这两个元素。⊗ 是「取消」，正好是 Esc 干的事，而且一笔一画都认得出。
 * 门 + 箭头（lucide `LogOut`）不是 Esc，那是「退出 / 登出」，单独留成 `exit`。
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
 * **16px + 1.75 描边。** 三次真机反馈调出来的：
 *
 *   - 15px + lucide 默认的 2 → 细节糊成一团（`Keyboard` 内部那八九个小点）；
 *   - 18px → 和旁边文字键（13px）比明显大一号，整条看着不齐（用户：「方向按键也太大了吧」）；
 *   - 16px + 细一档的线 → 和 13px 的字在视觉上对得上，细节也还在。
 *
 * 尺寸只是一半：**画得太满或太碎都不行**。lucide 里那种「铺满 24 画布的四向箭头」在这个
 * 尺寸上比字重得多，所以键盘和方向这两个都自己画了（见下面），占框七成、用**填充**而不是
 * 细描边 —— 填充缩下去还是实的，1px 的描边缩下去就没了。
 */
const C = 'size-4'
const SW = 1.75

/**
 * 键盘：**自己画的**，不用 lucide 那个。
 *
 * 三条都是真机上试出来的：
 *
 *   - lucide 的 `Keyboard`（外框 + 八九个小键）在这个尺寸上糊成一块脏东西；
 *   - 只画「外框 + 一条空格键」又太空 —— 读起来是「一个框」，压根不像键盘
 *     （用户：「是不是没做键盘 icon?」）；
 *   - 现在是**外框 + 三个键 + 一条空格键**，而那四个内部标记是**填充**的小圆角块，
 *     不是描边。填充缩到 16px 还是实的；同样大小的描边缩下去就剩一层灰。
 *
 * 外框也铺开到 y 5–19（原来 6.25–17.75 太扁，像个横条）。别再往里加第二排小键：
 * 24 的画布上再挤一排，每个块就不到 1px 了。
 */
const KeyboardGlyph = ({ className }: { className?: string }) => (
  <svg viewBox="0 0 24 24" fill="none" className={className} aria-hidden>
    <rect x="1.75" y="5" width="20.5" height="14" rx="2.75"
          stroke="currentColor" strokeWidth={SW} />
    <g fill="currentColor">
      <rect x="4.9" y="8.6" width="3.1" height="1.9" rx="0.95" />
      <rect x="10.45" y="8.6" width="3.1" height="1.9" rx="0.95" />
      <rect x="16" y="8.6" width="3.1" height="1.9" rx="0.95" />
      <rect x="7.5" y="13.1" width="9" height="1.9" rx="0.95" />
    </g>
  </svg>
)

/**
 * 方向（一组方向键）：**一个圆 + 一根指针**（罗盘）。
 *
 * 这个字形改了五版。前四版全在犯同一个错 —— **想在 16px 里画四个东西**：
 *
 *   1. lucide `Move`（四条带箭杆的箭头铺满画布）：细、碎，比旁边 13px 的字重；
 *   2. 四个小三角、离中心 3–5 单位：像一撮不相干的装饰点（散）；
 *   3. 四个三角在中心汇合：连成一块了，但那形状就是 `✦`，读成「闪光」（还和满地的 AI
 *      图标撞脸）；
 *   4. 四个 V 形箭头：round cap 一糊，四个尖端连成一个 `◇` 菱形轮廓。
 *
 * 24 的画布上放四个带方向的标记，两两之间只剩一个描边的间距 —— 缩到 16px 必然粘在一起。
 * 所以换成**一个**形状：圆 + 斜指针，本身就是「方向 / 导航」，而且只有两笔，缩到多小都不糊。
 *
 * 不满意的话别再调这个字形了 —— 那个键的图标是可配的，换成 `up`、或者干脆不用图标
 * （画「方向」两个字）都行。
 */
const DpadGlyph = ({ className }: { className?: string }) => (
  <Compass className={className} strokeWidth={SW} />
)

export const KEY_ICONS = [
  { id: 'ctrl', hint: 'Ctrl（^）', icon: <ChevronUp className={C} strokeWidth={SW} /> },
  { id: 'alt', hint: 'Alt / Option（⌥）', icon: <Option className={C} strokeWidth={SW} /> },
  { id: 'shift', hint: 'Shift（⇧）', icon: <ArrowBigUp className={C} strokeWidth={SW} /> },
  { id: 'cmd', hint: 'Cmd（⌘）', icon: <Command className={C} strokeWidth={SW} /> },
  { id: 'esc', hint: 'Esc / 取消', icon: <CircleX className={C} strokeWidth={SW} /> },
  { id: 'enter', hint: '回车', icon: <CornerDownLeft className={C} strokeWidth={SW} /> },
  { id: 'tab', hint: 'Tab（⇥）', icon: <ArrowRightToLine className={C} strokeWidth={SW} /> },
  { id: 'space', hint: '空格', icon: <Space className={C} strokeWidth={SW} /> },
  { id: 'bs', hint: '退格', icon: <Delete className={C} strokeWidth={SW} /> },
  { id: 'del', hint: '删除', icon: <Eraser className={C} strokeWidth={SW} /> },
  { id: 'up', hint: '上', icon: <ArrowUp className={C} strokeWidth={SW} /> },
  { id: 'down', hint: '下', icon: <ArrowDown className={C} strokeWidth={SW} /> },
  { id: 'left', hint: '左', icon: <ArrowLeft className={C} strokeWidth={SW} /> },
  { id: 'right', hint: '右', icon: <ArrowRight className={C} strokeWidth={SW} /> },
  { id: 'dpad', hint: '方向（四个方向那一组）', icon: <DpadGlyph className={C} /> },
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
  { id: 'panes', hint: '面板一览（act:panes）', icon: <LayoutGrid className={C} strokeWidth={SW} /> },
  { id: 'files', hint: '文件（act:files）', icon: <FolderOpen className={C} strokeWidth={SW} /> },
  { id: 'image', hint: '传图（act:img）', icon: <Image className={C} strokeWidth={SW} /> },
  { id: 'compose', hint: '发件箱', icon: <Pencil className={C} strokeWidth={SW} /> },
  { id: 'settings', hint: '设置', icon: <Settings className={C} strokeWidth={SW} /> },
  { id: 'theme', hint: '明暗', icon: <Contrast className={C} strokeWidth={SW} /> },
  { id: 'exit', hint: '退出 / 断开（门 + 箭头）', icon: <LogOut className={C} strokeWidth={SW} /> },
] as const satisfies readonly KeyIcon[]

export type KeyIconId = (typeof KEY_ICONS)[number]['id']

const BY_ID = new Map<string, KeyIcon>(KEY_ICONS.map((k) => [k.id, k]))

/**
 * 这个键条上画什么。
 *
 * 没挑图标 → 画名字。挑了 → 按 `iconAt` 摆：
 *
 *	only（默认） 只画图标
 *	pre          图标在前、名字在后 —— `[⌃ B]`
 *	post         名字在前、图标在后 —— `[新建 +]`
 *
 * `pre` / `post` 是为 `^B 前缀` 这类键留的：名字里那个 `B` **是有意义的**（换个字母就是
 * 另一个键），而 `^` 那个字形恰恰是丑的那半 —— 只能二选一的话这类键只能忍字形。
 *
 * 间距用 `gap-1` 自己包一层，不吃 Button 的 `gap-1.5`：软键上 6px 太宽，两格宽的键会被
 * 撑到三格。认不出的 icon id 退回只画名字。
 */
export function keyFace(k: { icon?: string; iconAt?: string; label?: string }, fallback = ''): ReactNode {
  const glyph = k.icon ? BY_ID.get(k.icon)?.icon : undefined
  const text = k.label || fallback
  if (!glyph) return text
  if (k.iconAt !== 'pre' && k.iconAt !== 'post') return glyph
  if (!text) return glyph
  return (
    <span className="inline-flex items-center gap-1">
      {k.iconAt === 'pre' ? <>{glyph}{text}</> : <>{text}{glyph}</>}
    </span>
  )
}

export const keyIconHint = (icon?: string) => (icon ? BY_ID.get(icon)?.hint : undefined)
