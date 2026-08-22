import type { ReactNode } from 'react'
import { AArrowDown, AArrowUp, CircleHalf, ClipGet, ClipPut, Files, Gear, Ime, Image, Keyboard, Maximize, Panes, Pencil } from '@/icons'

/**
 * 顶栏上能放哪些按钮 —— **唯一一份清单**。
 *
 * 顶栏和软键条一样是可配置的：「放哪几个、什么顺序」存在服务端（`~/.herdr-web/topbar.json`，
 * 见 internal/topbar），**按「排布」分套存**（平板八个图标、手机竖屏三个，见
 * internal/profiles），在设置 →「顶栏」页里拖。这里存的是「按钮长什么样」（图标、名字、
 * 一句说明），点了干什么在 App 里（那些动作要用 App 的状态，搬不出来）。
 *
 * 服务端那份白名单（`topbar.Actions`）要和这份**一字不差、顺序也一样**，有测试盯着
 * （internal/topbar 的 `TestActionsMatchJS`）。只在一边加一个按钮的后果很难查：编辑器里
 * 拖得上去、一存就报「不认识的按钮」，或者反过来存得下去但顶栏上画不出来。
 *
 * 那个测试是拿正则从这个文件里抠 id 的（认「花括号 + id + 引号」这个开头），所以下面每一条
 * 都得让 id 排在最前面，别挪到后面去 —— 这段注释本身也因此不敢照抄那个写法。
 *
 * 数组顺序 = 编辑器里「库」的排列顺序（不是顶栏的顺序，那个是配置）。
 */
export type TopbarId =
  | 'panes' | 'files' | 'compose' | 'keys'
  | 'kbd' | 'img' | 'clip' | 'paste'
  | 'font-' | 'font+' | 'theme' | 'full'
  | 'settings'

export interface TopbarItem {
  id: TopbarId
  /** 编辑器里那个小方块上的名字（顶栏上只有图标） */
  label: string
  /** 顶栏上的 title / 编辑器里的一句说明 */
  hint: string
  icon: ReactNode
}

export const TOPBAR_ITEMS: TopbarItem[] = [
  { id: 'panes', label: '面板一览', hint: '跳到某个 pane（顺带全屏）；有 agent 等你时挂角标', icon: <Panes className="size-4" /> },
  { id: 'files', label: '文件', hint: '看 agent 生成的图 / 翻目录', icon: <Files className="size-4" /> },
  { id: 'compose', label: '发件箱', hint: '语音投稿（说话打字 → 投进 agent pane）', icon: <Pencil className="size-4" /> },
  { id: 'keys', label: '软键条', hint: '显示 / 收起软键条（Ctrl / Esc / 方向键）', icon: <Keyboard className="size-4" /> },
  { id: 'kbd', label: '系统键盘', hint: '呼出 / 收起系统输入法（手机上呼键盘只有这条路和软键条上的 ⌨）', icon: <Ime className="size-4" /> },
  { id: 'img', label: '传图', hint: '拍一张 / 从相册选（落盘到 herdr 那台机器，把路径给 agent）', icon: <Image className="size-4" /> },
  { id: 'clip', label: '取剪贴板', hint: '把 herdr 那台机器的剪贴板取到这台设备', icon: <ClipGet className="size-4" /> },
  { id: 'paste', label: '粘到终端', hint: '把这台设备的剪贴板粘进终端', icon: <ClipPut className="size-4" /> },
  { id: 'font-', label: '缩小字号', hint: '终端字号小一号', icon: <AArrowDown className="size-4" /> },
  { id: 'font+', label: '放大字号', hint: '终端字号大一号', icon: <AArrowUp className="size-4" /> },
  { id: 'theme', label: '明暗', hint: '切换亮色 / 暗色', icon: <CircleHalf className="size-4" /> },
  { id: 'full', label: '全屏', hint: '去掉地址栏和工具条，终端多几行', icon: <Maximize className="size-4" /> },
  { id: 'settings', label: '设置', hint: '终端 / 顶栏 / 软键条 / 设备', icon: <Gear className="size-4" /> },
]

export const TOPBAR_BY_ID = new Map(TOPBAR_ITEMS.map((it) => [it.id, it]))

/**
 * 出厂顺序。服务端有同一份（`topbar.Defaults()`）—— 这边这份只在**拿不到配置**时用
 * （后端还没答、或者这个部署的接口挂了），别让顶栏因此空掉。
 */
export const TOPBAR_DEFAULT: TopbarId[] = [
  'panes', 'files', 'compose', 'keys', 'font-', 'font+', 'theme', 'full', 'settings',
]
