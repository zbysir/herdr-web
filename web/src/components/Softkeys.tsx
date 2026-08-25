import { useEffect, useState } from 'react'
import { Button } from './ui/button'
import { NoticeDot } from './ui/dot'
import { KeyGroupPopup } from './KeyGroupPopup'
import type { RowSegments, SoftKey } from '@/lib/api'
import type { KeyAct } from '@/capabilities'
import { usePhone } from '@/hooks/usePhone'
import { useArm } from '@/hooks/useArm'
import { keyStyle } from '@/lib/prefs'
import { keyFace } from '@/keyicons'
import { cn } from '@/lib/utils'

/**
 * 软键条：**只出键本身**，外壳（边框 / 宽度 / 高度 / 把手）归底部面板管（见 Dock）。
 *
 * 按键最多分**两行**（几行是设置），**每行各自横向滚动** —— 手机上一行只放得下四五个键，
 * 而「最常用那几个」和「次常用那几个」各占一行、各滑各的，比十几个键排成一条长龙好找：
 * 手指知道自己在哪一行，滑动也不会把另一行带跑。
 *
 * 收到的是**已经解析好的每行**（`bar`）：同一个键可以在两行里各出现一次，所以这里认
 * 「第几行第几个」，不认「keys 里的第几个」。
 *
 * 每一行切成**钉左 / 跟着滑 / 钉右**三段（`rows`）：钉住那两段不跟着横滑 ——
 * 「呼键盘」「Esc」这种每隔十秒要按一次的键，滑走了就等于没有。滑动只发生在中间那段上。
 *
 * 按键由 /api/softkeys 下发（存服务端；**定义所有设备共用一份，几行 / 哪些键在条上按
 * 「排布」分**，见 internal/profiles），在设置 →「软键条」页改。以前这一条右下角还挂着一个直通那一页的 ⚙，去掉了 —— 顶栏已经有一个设置入口，
 * 而这个位置每换一次朝向都跟键抢地方。
 *
 * 手机没有 Ctrl 键，herdr 的 ctrl+b 前缀全靠这条。
 *
 * 打了 `confirm` 的键要点**两下**（见 hooks/useArm —— 顶栏那边放「我的按键」时共用同一份）。
 */
export function Softkeys({
  rows, sticky, act, onSend, onSticky,
}: {
  /** 每行三段（已按 id 解析好、个数也夹过）。一到两行。见 lib/api.ts 的 resolveRows */
  rows: RowSegments[]
  sticky: { ctrl: boolean; alt: boolean }
  onSend: (bytes: string) => void
  onSticky: (which: 'ctrl' | 'alt') => void
  /**
   * `act:` 那一档的键**点了干什么 / 亮不亮 / 挂不挂红点 / 这个部署有没有这项** ——
   * 直接把顶栏那张动作表（App 的 `topbarAct`）递进来。
   *
   * 以前这儿是六个 prop（onKeyboard / onImage / onPanes / onFiles / onClip / onPaste）加一个
   * `notice`，也就是**同一份 act→动作的映射写了两遍**（顶栏一份、这儿一份）。合成一份之后
   * 白拿到三件原来没有的：
   *
   *   - `act:panes` / `act:files` 的键会跟着面板亮起来（原来只有 `act:kbd` 会）；
   *   - 红点不再只挂在 panes 上，谁有就挂谁（原来是一个专门的 `notice` prop）；
   *   - 服务端关掉文件浏览时（`HERDR_WEB_FILES=0`）`act:files` 的键**直接不画** ——
   *     原来是画出来点了没反应。
   *
   * 顺带一个行为变化：`panes` / `files` 现在**点第二下收起面板**（原来只开不关）——
   * 那就是顶栏那两个按钮的语义，两处不一样才是怪事。
   */
  act: (id: KeyAct) => { run: () => void; on?: boolean; dot?: boolean; hide?: boolean } | undefined
}) {
  const phone = usePhone()
  // 按键样式跟着 profile 走（设置 →「终端」）。**同步读镜像** —— 每个键都要它，
  // 绕一圈 React state 不值当，见 lib/prefs.ts
  const kv = keyStyle() === 'plain' ? 'keyPlain' : 'key'

  // 举着的那个键，坐标是「第几行第几个」：同一个定义可能在两行各有一个，举起来的必须是
  // **手指点的那个**。bar 变了（编辑器里存了一版）就放下，见 useArm。
  const { armed, tap } = useArm(rows)

  /**
   * 开着的那个**弹出组**（坐标 + 它那个键的 DOM，浮窗贴着它摆）。
   * 一次只开一个：两片浮窗同时飘着分不清哪个是哪个的。
   */
  const [open, setOpen] = useState<{ at: string; el: HTMLElement } | null>(null)
  // 按键改了（在编辑器里存了一版）就关掉：那个坐标现在可能已经是别的键了，
  // 浮窗还挂在旧位置上，点下去就点错了东西。和 useArm 里那条 reset 同一个道理
  useEffect(() => setOpen(null), [rows])


  /**
   * 按坐标找回那个组键。坐标是 `"行:位置"`（条上）或 `"p:第几格"`（固定块里）——
   * 用坐标而不是定义 ID：同一个组键可以在界面上出现好几次，开着的必须是**手指点的那一个**。
   * 坐标是「第几行:哪一段(l/m/r):第几个」。
   */
  const groupAt = (at: string): SoftKey | undefined => {
    const [ri, which, i] = at.split(':')
    const r = rows[Number(ri)]
    if (!r) return undefined
    return (which === 'l' ? r.left : which === 'r' ? r.right : r.scroll)[Number(i)]
  }

  /**
   * 一个键。`at` 是它在界面上的坐标（条上是「第几行第几个」，固定块里是「第几格」）——
   * 二次确认举起来的必须是**手指点的那一个**，同一个定义可能在界面上出现好几次。
   *
   */
  const renderKey = (k: SoftKey, at: string) => {
    const a = k.act ? act(k.act) : undefined
    const isGroup = !!k.members
    // 这个部署没有这项（比如服务端关掉了文件浏览）：整个键不画。
    // 画出来点了没反应比没有这个键更糟
    if (a?.hide) return null
    const on = k.sticky ? sticky[k.sticky] : !!a?.on
    const up = armed === at   // 举起来了，等第二下
    return (
      <Button
        key={at}
        data-testid={up ? 'softkey-armed' : undefined}
        variant={kv}
        size="key"
        on={isGroup ? open?.at === at : on}
        // 举起来只换颜色，**不换文字**：改字会让按键变宽，手指底下的键
        // 当场挪位置，第二下就点到隔壁去了。
        className={cn(
          // **一律不许被压窄**：键上有个显式的 `min-w`（--sk-w，够手指点的下限），而显式
          // min-width 会顶掉 flex 的自动最小尺寸（`min-width:auto` 才是「不小于内容」）——
          // 于是这一段挤不下时每个键都被压到 36px，键上的字又是 nowrap 的，`/clear` 这种
          // 长键的字就直接漏到边框外面去（用户报的）。条本来就是横滑的，压窄没有任何好处。
          'relative shrink-0',
          up && 'border-bad bg-bad text-white hover:border-bad hover:bg-bad',
        )}
        title={up ? '再点一次才真的发出去'
          : isGroup ? `${k.label}：点开一小片键（浮在上面，不占条上的地方）`
            // 挑了图标之后条上不画字了，名字得进 title —— 否则那个键是什么全靠猜
            : `${k.icon ? `${k.label} —— ` : ''}${k.spec || k.sticky || k.act || ''}${k.confirm ? '（要点两下）' : ''}`}
        // 这一个不能顺手 focus 终端，否则没法收起键盘
        onMouseDown={(e) => e.preventDefault()}
        onClick={(e) => {
          if (!tap(at, k.confirm)) return   // 这一下只是举起来
          // 弹出组：点一下开 / 再点一下关。**浮窗不占条上的地方**（见 KeyGroupPopup）
          if (isGroup) {
            const el = e.currentTarget as HTMLElement
            setOpen((cur) => (cur?.at === at ? null : { at, el }))
            return
          }
          if (a) a.run()
          else if (k.sticky) onSticky(k.sticky)
          else if (k.send) onSend(k.send)
        }}
      >
        {keyFace(k)}
        {/* 顶栏那个 ▦ 上已经有一个了，这儿还要一个是因为**手机上键盘一弹起来顶栏整条就收掉**
            （见 App 里的 barHidden）——而那正是你在跟 agent 说话、最该知道「另一个在等你」的时候。
            长什么样在 ui/dot.tsx（那儿写着它为什么不能探出键的框：这一行也是横滑的） */}
        {!!a?.dot && <NoticeDot />}
      </Button>
    )
  }

  /**
   * 一行里的一段。钉住那两段 `shrink-0`（条挤它不动，这才叫「钉住」），
   * **滑动只发生在中间那段上**。
   */
  const seg = (ri: number, which: 'l' | 'm' | 'r', keys: SoftKey[]) => keys.length > 0 && (
    <div
      key={which}
      data-testid={`softkeys-${ri + 1}-${which}`}
      className={cn(
        'flex gap-1.5',
        which === 'm'
          ? cn('min-w-0 flex-1 overscroll-contain [scrollbar-width:none] [&::-webkit-scrollbar]:hidden',
            // 手机：这一段不换行、自己横滑（滚动条不出，会盖住键）
            // 宽屏：照旧换行排，放不下的部分由外面那层上下滚
            phone ? 'flex-nowrap overflow-x-auto' : 'flex-wrap content-start')
          : 'shrink-0',
      )}
    >
      {keys.map((k, i) => renderKey(k, `${ri}:${which}:${i}`))}
    </div>
  )

  const rowEls = rows.map((r, ri) => (r.left.length + r.scroll.length + r.right.length) > 0 && (
    <div key={ri} data-testid={`softkeys-row-${ri + 1}`} className="flex min-w-0 items-start gap-1.5">
      {seg(ri, 'l', r.left)}
      {seg(ri, 'm', r.scroll)}
      {seg(ri, 'r', r.right)}
    </div>
  ))

  // 开着的那个弹出组。**浮窗是 fixed 的**，所以挂在哪一级都不影响布局 —— 挂在最外面
  // 是为了让钉住那两段和滑动那段里的组键走同一条路
  const popup = (() => {
    if (!open) return null
    const src = groupAt(open.at)
    if (!src?.members || !src.group) return null
    return (
      <KeyGroupPopup
        cols={src.group.cols}
        members={src.members}
        anchor={open.el}
        onClose={() => setOpen(null)}
        // 成员键用**同一份** renderKey：发字节 / 粘滞 / act / 两次确认一处都不用重写
        renderKey={(k, at) => renderKey(k, `${open.at}/${at}`)}
      />
    )
  })()

  return <>{rowEls}{popup}</>
}
