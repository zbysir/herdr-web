/**
 * 「结尾那个回车要不要拖后发」这条判据的回归测试（`splitEnter`，见 keysend.ts）。
 *
 * 跑法同 paths.test.ts：`node --experimental-strip-types` 直接跑，不引测试框架。
 * 为什么值得钉住：这条判据坏了**两个方向都是静默的** —— 少切一次是「codex 下点
 * `/clear` 只换行、命令不提交」（用户报的那条原样复发），多切一次是把 `alt+enter`
 * 这种一个键的字节拆成「Esc + 200ms + 回车」，意思全变了，而屏幕上都不报错。
 *
 * 跑：`node web/src/term/keysend.test.ts`（`make test` 里有）。
 */
import { splitEnter } from './keysend.ts'

let bad = 0
function eq(name: string, got: unknown, want: unknown) {
  const g = JSON.stringify(got)
  const w = JSON.stringify(want)
  if (g === w) return
  bad++
  console.error(`✗ ${name}\n   got  ${g}\n   want ${w}`)
}

// 要切的：一串字 + 一个回车。这正是 `text:/clear enter` / `"herdr" enter` 的样子
eq('/clear', splitEnter('/clear\r'), ['/clear', '\r'])
eq('herdr', splitEnter('herdr\r'), ['herdr', '\r'])
eq('中文也算字', splitEnter('你好吗\r'), ['你好吗', '\r'])
eq('前面带控制码：只看结尾那几个字', splitEnter('\x15/clear\r'), ['\x15/clear', '\r'])
eq('换行也是回车', splitEnter('/clear\n'), ['/clear', '\n'])

// 不切的
eq('光秃秃一个回车', splitEnter('\r'), null)
eq('alt+enter 是一个键，切开就变意思了', splitEnter('\x1b\r'), null)
eq('esc enter 同理', splitEnter('\x1b\r'), null)
eq('不到 3 个字 codex 不当粘贴', splitEnter('ls\r'), null)
eq('结尾不是回车', splitEnter('/clear'), null)
eq('ctrl+b c', splitEnter('\x02c'), null)
eq('空的', splitEnter(''), null)

if (bad) {
  console.error(`\n${bad} 条没过`)
  process.exit(1)
}
console.log('keysend: 全过')
