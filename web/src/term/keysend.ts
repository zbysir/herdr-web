/**
 * 快捷键条 / 顶栏上那种「敲一串字 + 一个回车」的键（`text:/clear enter`、
 * `"herdr" enter`），**回车不能和前面那串字挤在一起发**。
 *
 * codex 的输入框里有一道「这是不是粘贴进来的」的判据
 * （codex-rs/tui/src/bottom_pane/paste_burst.rs）：连着 3 个以上字符、每个间隔不到
 * 8ms 就当成粘贴，于是这一阵里的**回车按换行处理**（源码里还写着这个抑制窗口在
 * 最后一个字符之后再延 120ms：PASTE_ENTER_SUPPRESS_WINDOW）。它本来是为了
 * 「粘一段多行文本别粘到一半就提交」，而我们一次性写进 PTY 的 `/clear\r` 正好长成
 * 那个样子：6 个字符零间隔 + 紧跟着的回车。表现就是用户报的**「claude 下好好的，
 * codex 按了只是换了一行，命令没提交」** —— claude code 没有这一道判据，所以同一个
 * 键在它那儿一直是对的，这个坑只在换了 agent 之后才现形。
 *
 * 治法只能是**装得像人在打字**：把结尾那个回车拖后再发。实测（codex 0.155.1，
 * 真跑一个 pty 灌字节）：间隔 10ms 仍然只换行，20ms 起就提交了；取 200ms 是照着
 * 源码里那个 120ms 的窗口留的余量（不同版本、Windows 上的 idle 超时都更长），
 * 人也感觉不出来。
 *
 * 两条别改：
 * ① **等在服务端**（`{t:'i', gap}`，见 internal/server/pty.go）而不是前端 setTimeout：
 *    前端隔开发的两帧走的是同一条 TCP 连接，第一帧卡在重传里时第二帧会跟在它屁股
 *    后面一起到，间隔当场被挤没 —— 而这个间隔恰恰是给对面那个 TUI 看的，隔在
 *    哪一头不是等价的。从平板走隧道进来时这不是小概率。
 * ② **别改成 bracketed paste 包一层**（`\x1b[200~…\x1b[201~`）：那要知道对面那个 pane
 *    此刻开没开 DEC 2004，而快捷键条是往 herdr 的 PTY 里灌原始字节的，这一层看不到
 *    pane 的模式（发件箱那条路能，它走的是 herdr 的 agent.prompt）。猜错了就是把
 *    `[200~` 这几个字面字符打进人家命令行。
 */

/** 结尾那个回车往后拖多久（毫秒） */
export const ENTER_GAP_MS = 200

/** 看得见的字 —— codex 那道判据只数这种，控制码和 ESC 不算 */
const printable = (c: string) => c >= ' ' && c !== '\x7f'

/**
 * 把「一串字 + 结尾那个回车」切成两段；不是这个形状就返回 null（照原样一次发完）。
 *
 * 门槛是**结尾那个回车前面得有 3 个看得见的字**，这条不是随手定的：它对着 codex 那边的
 * `PASTE_BURST_MIN_CHARS = 3` —— 不够 3 个字符压根不会被当成粘贴，也就不用拖。
 * 顺带它还把两种**不能切**的谱挡在外面：`alt+enter`（字节是 `\x1b\r`，切开就成了
 * 「先一个 Esc、200ms 后一个回车」，意思全变了）和 `esc enter` / `ctrl+b c enter`
 * 这种前面只有控制码的。
 */
export function splitEnter(bytes: string): [string, string] | null {
  const m = /^([\s\S]*?)([\r\n]+)$/.exec(bytes)
  if (!m || !m[1]) return null
  const tail = [...m[1]].slice(-3)
  if (tail.length < 3 || !tail.every(printable)) return null
  return [m[1], m[2]]
}
