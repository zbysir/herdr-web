import { Settings } from 'lucide-react'
import { Button } from './ui/button'
import type { SoftKey } from '@/lib/api'

/**
 * 软键条。按键由 /api/softkeys 下发（存服务端，手机 / 平板 / 电脑共用一份），
 * 点最右边的 ⚙ 在网页上改。
 *
 * 手机没有 Ctrl 键，herdr 的 ctrl+b 前缀全靠这条。
 */
export function Softkeys({
  keys, sticky, kbdUp, onSend, onSticky, onKeyboard, onEdit,
}: {
  keys: SoftKey[]
  sticky: { ctrl: boolean; alt: boolean }
  kbdUp: boolean
  onSend: (bytes: string) => void
  onSticky: (which: 'ctrl' | 'alt') => void
  onKeyboard: () => void
  onEdit: () => void
}) {
  return (
    <nav
      data-testid="softkeys"
      className="flex shrink-0 gap-1.5 border-t border-line bg-bar px-2 py-[7px] select-none"
      style={{ paddingBottom: 'calc(7px + env(safe-area-inset-bottom))' }}
    >
      {/* 按键那一排自己横向滚，⚙ 固定在右边不跟着滚走 */}
      <div className="flex min-w-0 flex-1 gap-1.5 overflow-x-auto [scrollbar-width:none] [&::-webkit-scrollbar]:hidden">
        {keys.map((k, i) => {
          const on = k.sticky ? sticky[k.sticky] : k.act === 'kbd' ? kbdUp : false
          return (
            <Button
              key={i}
              variant="key"
              size="key"
              on={on}
              className={k.wide ? 'min-w-[78px]' : undefined}
              title={k.spec || k.sticky || k.act || ''}
              // 这一个不能顺手 focus 终端，否则没法收起键盘
              onMouseDown={(e) => e.preventDefault()}
              onClick={() => {
                if (k.act === 'kbd') onKeyboard()
                else if (k.sticky) onSticky(k.sticky)
                else if (k.send) onSend(k.send)
              }}
            >
              {k.label}
            </Button>
          )
        })}
      </div>
      <Button data-testid="softkeys-edit" variant="key" size="key" title="配置软键条" onClick={onEdit} onMouseDown={(e) => e.preventDefault()}>
        <Settings className="size-4" />
      </Button>
    </nav>
  )
}
