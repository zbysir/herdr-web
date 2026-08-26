import { useEffect, useRef, useState } from 'react'

/**
 * 举起来的键多久自动放下（ms）。
 * 太短来不及看第二眼，太长就会忘了自己举过 —— 回头随手一点反而正好点实。
 */
export const CONFIRM_MS = 3000

/**
 * 打了 `confirm` 的键要点**两下**：第一下只是举起来（变红），第二下才真发出去。
 * 键挨得近，关 pane / 关标签这种误触一下就没了，而 herdr 那边没有撤销。
 *
 * 快捷键条和顶栏**共用这一份**：同一个定义现在能同时出现在两个界面上（顶栏放
 * `key:<定义ID>`，见 internal/topbar），两处各写一遍计时器就是「同一个键两种手感」的来路。
 *
 * `at` 是调用方自己定的坐标 —— 快捷键条用「第几行第几个」（同一个定义两行各放一个是合法的），
 * 顶栏用那一项的 id。举起来的必须是**手指点的那一个**，不是「那个定义」。
 *
 * `reset` 变了就放下：按键在编辑器里存了一版之后，那个坐标现在可能已经是别的键了，
 * 接着点就点错了东西。
 */
export function useArm(reset?: unknown) {
  const [armed, setArmed] = useState<string | null>(null)
  const timer = useRef<number | undefined>(undefined)

  const disarm = () => {
    clearTimeout(timer.current)
    setArmed(null)
  }

  useEffect(() => () => clearTimeout(timer.current), [])
  useEffect(disarm, [reset])

  /**
   * 点了一下：返回「这一下算不算真的发出去」。false = 只是举起来了。
   *
   * 点**别的**键等于把举着的那个放下，但这一下照样算数 —— 举着的那个和手指此刻点的
   * 不是一回事，让它把一次正常的按键吞掉是错的。
   */
  const tap = (at: string, confirm?: boolean) => {
    if (confirm && armed !== at) {
      clearTimeout(timer.current)
      setArmed(at)
      timer.current = window.setTimeout(() => setArmed(null), CONFIRM_MS)
      return false
    }
    disarm()
    return true
  }

  return { armed, tap, disarm }
}
