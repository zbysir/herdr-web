import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import App from './App'
import { installTapRescue } from './lib/tap'
import { registerSWWhenIdle } from './lib/sw'
// 只为副作用：它在模块顶层挂 beforeinstallprompt。那个事件在页面加载早期只触发一次，
// 等 React 组件挂载再挂 listener 就抓不到了（见 lib/install.ts）。
import './lib/install'
import './index.css'

// 手机上「第一下只收键盘」的兜底。装在这儿而不是 App 里：它是一层跟 React 无关的
// document 监听，装一次就够，别跟着组件挂载 / 卸载走（StrictMode 下 effect 会跑两遍）。
installTapRescue()

// service worker：系统通知要它，浏览器判「这个站点能不能装成 PWA」也要它（见 lib/sw.ts）。
// 同样装在这儿而不是 App 里 —— 全局只该做一次，别跟着 StrictMode 的双跑走。
registerSWWhenIdle()

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
