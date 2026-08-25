import type { ReactNode } from 'react'
import { AArrowDown, AArrowUp, CircleHalf, ClipGet, ClipPut, Files, Gear, Ime, Image, Keyboard, Maximize, Panes, Pencil } from '@/icons'

/**
 * 「这个版本能做哪几件事」—— 前端这一半的**唯一一份清单**（图标、名字、一句说明）。
 *
 * 服务端那一半在 `internal/capability`：它管「能出现在哪些界面上」（顶栏 / 软键条的 act /
 * 是不是浮层 / 删不删得掉），因为那些要参与**存盘校验**。这边管「长什么样」，因为那是
 * React 节点，搬不到 Go 去。两边靠 id 对上，**顺序和 `key` 标记必须一字不差**，有测试盯着
 * （`internal/capability` 的 `TestMatchJS`）。
 *
 * 这三件事以前是三份平行的清单（顶栏目录、softkeys 的 act 联合类型、App 里那个 panel 枚举），
 * 而它们是同一件事的三个切面。合成一份之后：
 *
 *   - 加一件事 = 这儿一行 + Go 那边一行，不用再改四处；
 *   - **`act` 是顶栏 id 的子集这条关系变成了推导出来的类型**（`KeyAct`）—— 原来它只写在
 *     注释里，只在一边加一个 act 的后果是「键点下去什么都不发生，而且不报错」；
 *   - `panel` 那个状态的类型也从这儿推（`PanelId`），不再手写第三份。
 *
 * 点了**干什么**不在这儿，在 App 的 `topbarAct` 里 —— 那些动作要用 App 的状态和 Session，
 * 搬不出来。两边靠 id 对上。
 *
 * 那个 Go 测试是拿正则从这个文件里抠「id + 有没有 key」的（认「花括号 + id + 引号」这个
 * 开头，然后看这一行里有没有 `key: true`），所以**下面每一条都得让 id 排在最前面、整条写在
 * 一行里** —— 这段注释本身也因此不敢照抄那个写法。
 *
 * 数组顺序 = 顶栏编辑器里「库」的排列顺序（不是顶栏的顺序，那个是配置）。
 */
export interface Cap {
  id: string
  /** 能当软键条按键的 `act`（不是所有事都能 —— 见 KeyAct） */
  key?: true
  /** 点开是一块浮层（面板一览 / 文件 / 设置）—— App 的 panel 状态从这儿推 */
  panel?: true
  /** 编辑器里那个小方块上的名字（顶栏上只有图标） */
  label: string
  /** 顶栏上的 title / 编辑器里的一句说明 */
  hint: string
  icon: ReactNode
}

export const CAPS = [
  { id: 'panes', key: true, panel: true, label: '面板一览', hint: '跳到某个 pane（顺带全屏）；有 agent 等你时挂红点', icon: <Panes className="size-4" /> },
  { id: 'files', key: true, panel: true, label: '文件', hint: '看 agent 生成的图 / 翻目录', icon: <Files className="size-4" /> },
  { id: 'compose', label: '发件箱', hint: '语音投稿（说话打字 → 投进 agent pane）', icon: <Pencil className="size-4" /> },
  { id: 'keys', label: '软键条', hint: '显示 / 收起软键条（Ctrl / Esc / 方向键）', icon: <Keyboard className="size-4" /> },
  { id: 'kbd', key: true, label: '系统键盘', hint: '呼出 / 收起系统输入法（手机上呼键盘只有这条路和软键条上的 ⌨）', icon: <Ime className="size-4" /> },
  { id: 'img', key: true, label: '传图', hint: '拍一张 / 从相册选（落盘到 herdr 那台机器，把路径给 agent）', icon: <Image className="size-4" /> },
  { id: 'clip', key: true, label: '取剪贴板', hint: '把 herdr 那台机器的剪贴板取到这台设备', icon: <ClipGet className="size-4" /> },
  { id: 'paste', key: true, label: '粘到终端', hint: '把这台设备的剪贴板粘进终端', icon: <ClipPut className="size-4" /> },
  { id: 'font-', label: '缩小字号', hint: '终端字号小一号', icon: <AArrowDown className="size-4" /> },
  { id: 'font+', label: '放大字号', hint: '终端字号大一号', icon: <AArrowUp className="size-4" /> },
  { id: 'theme', label: '明暗', hint: '切换亮色 / 暗色', icon: <CircleHalf className="size-4" /> },
  { id: 'full', label: '全屏', hint: '去掉地址栏和工具条，终端多几行', icon: <Maximize className="size-4" /> },
  { id: 'settings', panel: true, label: '设置', hint: '终端 / 顶栏 / 软键条 / 设备', icon: <Gear className="size-4" /> },
] as const satisfies readonly Cap[]

/** 全部 id。以前叫 `TopbarId` —— 现在顶栏只是它的一个界面 */
export type CapId = (typeof CAPS)[number]['id']

/**
 * 能当软键条 `act` 的那几个。**从表里推出来的**，不是手写的第二份 ——
 * 只在一边加一个 act 就会在这儿编译不过，而原来那种漏法是完全静默的。
 */
export type KeyAct = Extract<(typeof CAPS)[number], { key: true }>['id']

/** 点开是浮层的那几个（App 的 panel 状态） */
export type PanelId = Extract<(typeof CAPS)[number], { panel: true }>['id']

export const CAP_BY_ID = new Map<CapId, Cap>(CAPS.map((c) => [c.id, c]))

/** 顶栏那份目录（服务端 `capability.TopbarIDs()` 是同一批 id、同一个顺序） */
export const TOPBAR_ITEMS: readonly Cap[] = CAPS

/** 这个 id 认不认（读配置时挡一手：新版本存的配置在旧前端上读到过） */
export const isCapId = (s: string): s is CapId => CAP_BY_ID.has(s as CapId)

/**
 * 出厂顺序。服务端有同一份（`topbar.Defaults()`）—— 这边这份只在**拿不到配置**时用
 * （后端还没答、或者这个部署的接口挂了），别让顶栏因此空掉。
 */
export const TOPBAR_DEFAULT: CapId[] = [
  'panes', 'files', 'compose', 'keys', 'font-', 'font+', 'theme', 'full', 'settings',
]
