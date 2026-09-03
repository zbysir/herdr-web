// 顶栏那几个图标。lucide 里没有完全对应的（原来用的是 ⌨ ✎ ◐ ⌘? A− A+ 这些字形），
// 所以直接从 lucide 挑近似的重导出，名字保持和用途一致。
export { Keyboard, Pencil, Command, AArrowDown, AArrowUp, Image, Maximize, Minimize } from 'lucide-react'
// 顶栏可配置之后多出来的三个（系统键盘 / 取剪贴板 / 粘到终端），见 components/topbarItems.tsx
export { Type as Ime, ClipboardCopy as ClipGet, ClipboardPaste as ClipPut } from 'lucide-react'
// 拉回：把远端输入框里那份**拽下来**。用「向下到线」而不是刷新那两个圈 —— 这件事有方向
// （远端 → 这儿），圈圈看着像「重新读一遍列表」
export { ArrowDownToLine as Pull } from 'lucide-react'
export { LayoutGrid as Panes } from 'lucide-react'
export { FolderOpen as Files } from 'lucide-react'
export { Settings as Gear } from 'lucide-react'
export { Contrast as CircleHalf } from 'lucide-react'
export { Smartphone as Devices } from 'lucide-react'
// 看 diff。挑「文件上带 +/-」这个而不是 GitCompare 那两根箭头：16px 上箭头糊成一团，
// 而 +/- 是 diff 这件事本身的记号
export { FileDiff as Diff } from 'lucide-react'
