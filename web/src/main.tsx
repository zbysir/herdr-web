import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import App from './App'
import { installTapRescue } from './lib/tap'
import './index.css'

// 手机上「第一下只收键盘」的兜底。装在这儿而不是 App 里：它是一层跟 React 无关的
// document 监听，装一次就够，别跟着组件挂载 / 卸载走（StrictMode 下 effect 会跑两遍）。
installTapRescue()

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
