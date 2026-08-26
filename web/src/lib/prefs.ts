import { api } from './api'

/**
 * 跟着 profile 走的那几个开关（「排布」的另一半：快捷键条和顶栏是排布，这几个是小设置）。
 *
 * 模型是**服务端为准 + localStorage 镜像**：
 *
 *   - 真相在服务端（换台设备、清了缓存、换个浏览器都还在，见 internal/profiles）；
 *   - 但**每一处读的地方照旧读 localStorage** —— 有几处是在终端那层的回调里同步读的
 *     （App.tsx 里的 kbdFull / noticeOS 都是），拿不到 React state，也等不起一个请求。
 *
 * 所以启动时先把服务端那份盖到镜像上（applyPrefs），之后每次改都是「写镜像 + 推服务端」
 * （pushPref）。镜像还顺手解决了首屏闪一下的问题：字号这种东西要是等请求回来才生效，
 * 终端会先按默认字号画一遍再跳一次。
 *
 * 设置面板「终端」那一页**整页**都在这儿：字号、明暗、五个终端开关、键盘全屏、提示那几个。
 * 通知开关也在 —— 界面上那个勾是「想要」和「给了权限」**与**起来画的，而真弹之前
 * showNotify 还要再问一次权限，所以同步过去不会出现「显示着开着、一条都不弹」。
 *
 * 不在这儿的：kbdFullErr（上次全屏为什么失败，本机诊断）、面板的尺寸位置（还要按横竖屏
 * 各存一份，见 oriented.ts）、提示的未读游标、发件箱瞄准哪个 pane，以及发件箱 / 快捷键条
 * 显不显示 —— 最后这个是随手开关的视图状态，一次会话点十几次，每点一次写一趟服务端不值当。
 *
 * 键名和服务端白名单**一字不差、顺序也一样**，有测试盯着（internal/profiles 的
 * TestPrefsMatchJS）。
 */
export const PREF_KEYS = [
  'fontSize', 'scheme',
  'kitty', 'meta', 'copyOnSelect', 'sync2026', 'switchPanel',
  'kbdFull',
  'noticeDot', 'noticeOS', 'noticeOSFg', 'noticeCardMs',
  'keyStyle', 'popupClear',
  'diffWrap',
] as const
export type PrefKey = (typeof PREF_KEYS)[number]

/**
 * 把服务端那份盖到镜像上。
 *
 * 服务端没给的键**不动**（那是「这一套还没设过这一项」，不是「设成了默认值」）——
 * 清掉的话，用户在这台设备上调好的东西会在第一次同步时凭空退回默认。
 */
export function applyPrefs(prefs?: Record<string, string> | null) {
  if (!prefs) return
  for (const k of PREF_KEYS) {
    const v = prefs[k]
    if (typeof v === 'string' && v !== '') localStorage.setItem(k, v)
  }
}

/**
 * 改一个开关：**先写镜像**（当场生效，刷新也还在），再推服务端。
 *
 * 推不上去只出一条提示，不回滚：这台设备上已经改了，回滚等于「点了一下又跳回去」。
 * fail 不传就不说话（调用方拿不到 toast 的场合）。
 */
export function pushPref(profile: string, k: PrefKey, v: string, fail?: (m: string) => void) {
  localStorage.setItem(k, v)
  void api.put(`/profiles/${encodeURIComponent(profile)}/prefs`, { prefs: { [k]: v } })
    .catch((e: Error) => fail?.(`这台设备上改好了，但没同步到「排布」里：${e.message}`))
}

/** 按键样式：`solid` = 有底色有边（默认），`plain` = 只有字/图标，没底色没边 */
export type KeyStyle = 'solid' | 'plain'

/**
 * 这台设备该用哪种按键样式。**同步读镜像**（见上面那段）—— 快捷键条每渲染一个键都要它，
 * 等一个请求或者绕一圈 React state 都不值当。
 */
export const keyStyle = (): KeyStyle =>
  (localStorage.getItem('keyStyle') === 'plain' ? 'plain' : 'solid')

/**
 * 弹出组浮窗的**透明度**（%，0 = 不透明）。默认 60 —— 浮窗盖在终端上，不透明时那一片
 * 就是个洞，而它常常正好压在 agent 正在写的那几行上；透一点还看得见底下。
 *
 * 加在**整片浮窗**上（CSS `opacity`），不是只调底色：底、边、阴影、键、字一起透。只透
 * 底色的话键还是一块块实的，挡掉的面积没少多少 —— 用户报的就是这一点。
 *
 * 存的是**透明度**（界面上写的也是这个词），CSS 那边要的是不透明度，所以那儿是 `100 - clear`。
 * 别把这两个反过来：60 那一档反过来就是「几乎不透」，和界面上写的正好相反。
 *
 * 只给这几档而不是一个滑块：这是「顺手不顺手」的偏好，不是要精调的参数，滑块在手机上
 * 拖不准（还得给它一个数值显示），几个按钮一眼就挑完了。
 */
export const POPUP_CLEARS = [0, 20, 40, 60] as const
export type PopupClear = (typeof POPUP_CLEARS)[number]

/**
 * 这台设备上浮窗透到什么程度。**同步读镜像**（见上面那段）—— 和 `keyStyle` 一样，
 * 浮窗每次渲染都要它。认不出来的值退回 60（老文件、或者别的设备上更新的版本写进去的）。
 */
export const popupClear = (): PopupClear => {
  const n = Number(localStorage.getItem('popupClear'))
  return (POPUP_CLEARS as readonly number[]).includes(n) ? (n as PopupClear) : 60
}

/**
 * 看 diff 时长行折不折。**默认折**（这个面板本来就是为手机做的：不折的话每行都要横滑，
 * 而那正是终端里那份读不下去的原因）。
 *
 * 桌面和平板横屏上关掉它更好读 —— 那时候屏够宽，不折行才对得齐缩进，一眼看得出层级。
 * 所以它跟着**这一套排布**走（手机一套、电脑一套），不是全局一个值。
 *
 * 和 keyStyle / popupClear 一样**同步读镜像**：查看器每渲染一行都要它。
 */
export const diffWrap = () => localStorage.getItem('diffWrap') !== '0'
