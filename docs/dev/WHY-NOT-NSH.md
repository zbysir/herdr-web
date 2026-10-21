# 为什么原生 App 不用 nsh

**结论：原生 App 走「原生壳 + WebView」**（壳负责推送、令牌存放、系统集成，界面整份复用现在的
网页）。**不用 [nsh](https://nsh.dev/zh-CN/docs/comparison/)**，也不走「WebView 跑终端 + nsh 画其余界面」
那条中间路线。

> 评估时间 2026-10-20，对着的是 nsh **v0.1.1**（仓库 2026-10-14 才建）。下面关于 nsh 能力的
> 每一条都是那个版本的文档写的；它变了，结论可能跟着变 —— 文末列了「什么时候该重新评估」。

## 0. nsh 是什么

Longbridge 出的跨端框架（Apache-2.0）：模块写成一个 TSX 文件，运行时是 Rust（QuickJS 跑模块
自己的 JS），**每个平台用自己的原生控件画**（UIKit / Android View / AppKit / GTK 4 / Win32），
不带浏览器引擎。两种用法：嵌进已有的原生 App 当一个视图（`NshAppView` / `NshView`），或者
`nsh package` 直接打出 `.ipa` / `.apk`（官方说这条主要用来试用和做小工具）。它设计时瞄准的是
「**AI 生成的界面**」：签名包、每个视图一个沙箱、能力按视图授予。

## 1. 卡住的是终端，而终端是这个项目的核心

herdr-web 的前端里，**只有终端用 WebSocket**（`web/src/term/session.ts` 连 `/pty`），其余全是
HTTP + 轮询。所以 chat / 改动 / 文件 / 面板一览 / 发件箱搬到 nsh 上都没障碍 —— 问题全在终端：

- **nsh 没有 WebSocket API**（networking 那页原话：「没有 XMLHttpRequest，也没有 WebSocket API」），
  只有 `fetch`。
- **Canvas 画不了终端**：每次 `draw` 整份重新录制、没有像素读回，文档里也没有键盘输入。
  一屏几千个字符、每帧都在变，靠它自己画不现实。
- **能接原生终端控件，但只能在自己的宿主 App 里**：nsh 允许注册任意 `UIView` / `android.view.View`
  当组件，可 `nsh package` 打出来的 App **不注册任何组件**（文档原话：渲染成空视图并记一条
  「未知名称」日志）。要接 SwiftTerm / Termux 的 terminal-view，就得自己写 Swift / Kotlin 宿主。

## 2. 两条用得上 nsh 的路，都不划算

### 2a. WebView 跑终端 + nsh 画其余界面 —— 两头吃亏

这是 nsh 能直接做到的（它自带 WebView 组件）。但 CLAUDE.md 里记着的那些 WebView 坑，**最重、
最难修的几条都在终端上**：Android 上点一下就把输入法顶回来、呼出输入法时重排闪黑（靠冻帧遮挡）、
WebGL 字形纹理拿错、后台 rAF 不跑导致画面停在息屏前那一帧。终端留在 WebView 里，这些一条都
不会消失。

而 chat / 改动 / 文件那几页的坑轻得多，大多已经修好了。用 nsh 重写它们 = 花大力气改善次要的
部分，最主要的问题原封不动，**还多维护一套 UI**（网页那份不会消失，桌面浏览器还要用）。

### 2b. 原生终端 + nsh 画其余界面 —— 能把 WebView 整个拿掉，但代价太大

这是唯一能从根上解决上面那串坑的组合。代价：

1. **终端集成从一份变成三份**：iOS（SwiftTerm）、Android（Termux terminal-view）、网页（xterm.js）。
   Rust 那条路省不了多少 —— nsh 用 Rust 注册组件的平台里没有 Android，绘制和输入法也还得逐平台接。
2. **herdr 依赖的终端协议，两个库不一定都支持**：kitty 键盘协议、DEC 2026 同步输出、SGR 鼠标
   上报、OSC 52 剪贴板。少一项，herdr 里对应的功能就**静默**失效（鼠标点不动 pane、Esc 被编错……）。
3. **Android 上终端里直接打中文是风险**：Termux 的 View 自己实现输入法接口，拼音组字时的显示
   一直不算好。
4. **`web/src/term/` 攒下的逻辑要在原生端重写或放弃**：最大的一块是 `paths.ts`（终端里的路径可点、
   折行拼接、按分栏切内容带 —— 每条都是真机调出来的），还有断线重连、回前台发探针认「僵」连接、
   重连前复位终端模式、PTY 起来后补发行列数。

## 3. 和 nsh 无关、但做原生时都要面对的约束

这几条换任何方案都在，记在这儿是因为当初评估 nsh 时一起查清的：

- **认证走 Bearer 令牌**：原生那一侧没有浏览器的 cookie jar，复用 `herdr-web connect` 那条
  device flow（`internal/auth/cliapprove.go`）拿令牌。批准页可以用 `ASWebAuthenticationSession` /
  Custom Tabs 打开（真浏览器，passkey 能用），但**码照旧手输，不许从 URL 带进去**。
- **原生 passkey 不适合自托管**：iOS 要在 entitlement 里写死 `webcredentials:<域名>` 并在那个域名
  上放 `apple-app-site-association`，Android 要 `assetlinks.json` + 服务端认 `android:apk-key-hash:`
  这种 origin。一个上架的通用 App 列不出每个用户自己的域名。
- **自签证书**：nsh 只用系统 TLS、没有自定义信任，局域网直连那条自签 TLS 的路在它上面走不通
  （原生壳 + WebView 可以在证书质询回调里自己决定）。

## 4. 什么时候该重新评估

满足下面任意一条，就值得回来重看 2b：

- WebView 的终端坑重新成了主要痛点（修不动、或者用户反复报），而网页那边已经没有招了；
- nsh 补上了 WebSocket，或者有了能在 `nsh package` 里用的终端组件；
- 实测过 SwiftTerm 和 Termux terminal-view 对第 2b 节第 2 条那几个协议的支持，且 Android 中文
  输入法可接受。

真要走 2b，顺序是：先只做 iOS 最小验证（SwiftTerm + Bearer 连 `/pty` 跑 herdr，过一遍鼠标 /
2026 / kitty 键盘 / 中文输入法 / 滚动选字 / 锁屏重连），再用同一份清单验 Android。

另外有一个**更贴近 nsh 本意**的方向没被否掉：让 agent 下发签名过的 TSX 界面（比如替代
`chatapi.go` 里按键模拟答 TUI 选择框那套），在手机上原生渲染。但那是「在网页这边新做一块界面」，
先过 [TUI-VS-GUI.md](TUI-VS-GUI.md) 的判据。
