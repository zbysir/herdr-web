# herdr-web

浏览器里的终端，用来跑 `herdr`。一个 Go 二进制（前端嵌在里面），手机也能用。

> **语音投稿**（平板手写笔说话打字 → 投进 agent pane）是这个项目的主功能，见下面「发件箱」。设计取舍、herdr socket API 的已验证语义和踩过的坑在 [HANDOFF.md](HANDOFF.md)。

```bash
make build      # 构建前端 → 拷进 internal/webui/dist → go build，出一个 ./herdr-web
./herdr-web     # 只听 127.0.0.1
HERDR_WEB_HOST=0.0.0.0 ./herdr-web    # 听局域网，顺便在终端画个二维码给手机扫
```

启动后会打印可用地址；监听 `0.0.0.0` 时会按网卡打分，把手机真能连上的那个标成 `← 手机用这个`（机器上一堆 OrbStack / VPN 虚拟网卡都会被压到后面），二维码编的就是它。

token 存在 `~/.herdr-web/token`（0600），**重启不变**，所以手机上存的书签一直有效。想换一把：`rm ~/.herdr-web/token` 再启动。连不上时页面会自己分辨是「后端没起」还是「token 不对」，不用猜。

连上之后敲 `herdr`（或点「敲 herdr」）就行。

## 只开本机 shell

要连别的机器就在 herdr 里 ssh —— herdr 自己就能干这事，所以这一层不再实现主机管理和托管私钥（那还得管密钥落盘、`ssh-keygen`、`~/.ssh` 扫描、ssh_config 导入），连带把「浏览器能碰到私钥」这个安全面也一起去掉了。

## 发件箱（语音投稿）

页面底下那条带 textarea 的就是发件箱，顶栏 ✎ 开关，**默认开着**。在里面说话打字，改完整段投进 herdr 的某个 pane。

为什么要单独一个框而不是直接对着终端说：终端是字节流，没有 selection 语义，输入法只能往里灌字符。「说错的字**框选重说**」需要一个真正的可编辑字段（有文本模型、有选区，选中后输入法提交会覆盖选区）。xterm.js 的隐藏 textarea 不算，它只把按键转成字节发走。

| 操作 | 说明 |
|---|---|
| **目标** | 默认「跟随 herdr 当前 pane」——不用选，投给你此刻在 herdr 里激活的那个。也能在下拉里钉死一个 |
| **投稿** `⌘↵` / `Ctrl↵` | 先清空远端输入行，再整段提交。`Enter` 是换行，不提交 |
| **拉回** | 把远端输入框里已有的内容抓进 textarea 编辑（远端按过 Tab 补全就用它） |
| **自动拉回** | 默认 500ms 一拍。切了 pane 自动换成新 pane 的内容；**本地有草稿时绝不覆盖**，只在状态行提示 |
| **双向** | 本地改动跟着推回远端输入框（不回车）。默认关，见下面的注意事项 |
| **图** | 传图片。手机上点它会给「相机 / 相册」；电脑上截完图直接在框里 `⌘V`，或者把文件拖进来 |
| `↑` | 框空时取回上一条投过的（本地留 30 条） |
| `Esc` | **转发给终端**。Esc 在纯 textarea 里没意义，而 agent 那边到处要用它（`/usage` 之类浮层靠它退出）；焦点不动，可以连按 |

**图片是怎么走通的**：herdr 的 socket API 里没有图片概念，能投的只有文本。但 claude 和 codex 都能直接读磁盘上的图片文件（实测两边都描述对了一张 320×200 左红右蓝的测试图，codex 还会打一行 `Viewed Image`）。所以「传图」＝ 存到跑 herdr 那台机器的 `~/.herdr-web/uploads/`，然后把**绝对路径插进提示词**，agent 自己去读文件。路径就在框里，可以随便在前后补话。

手机照片先在浏览器里缩到长边 2400 再传（顺手把 iPhone 的 HEIC 转成 PNG/JPEG，因为 agent 读不了 HEIC）。服务端按**魔数**认类型，只收 png / jpg / gif / webp，改后缀或改 content-type 骗不过去。上限 25 MB。传过的图不会自动删，攒多了自己清 `~/.herdr-web/uploads/`。

状态行会一直显示「这段话现在会投给谁」，`⟳` 表示正在跟随焦点；鼠标悬停能看到当前用的轮询间隔。

### 是轮询，不是推送

herdr 有 `events.subscribe` 推送通道，但 agent 一 working 就是刷屏级的量，所以这里用轮询：每 `HERDR_WEB_POLL_MS`（默认 500ms）问一次「焦点在哪个 pane + 那个输入框里是什么」。

**切 pane 到 textarea 更新的实测延迟**（同机，8 次取样）：

| 轮询间隔 | 最快 | 中位 | 最慢 |
|---|---|---|---|
| 200ms | 138ms | 318ms | 550ms |
| **500ms（默认）** | ~300ms | ~500ms | ~800ms |
| 1200ms | 408ms | 794ms | 818ms |

地板是一次 sync 的耗时，因为 herdr 的每次调用都可能撞上一个 ~100ms 的 tick（原因见 HANDOFF 的「100ms 的坎」）。想临时试手感：URL 上加 `?poll=200&push=400`，会覆盖服务端下发的默认值。

几个要知道的：

- **框里一有你自己写的内容，目标就被钉在当初瞄准的那个 pane 上**，框空了才重新跟随焦点。因为 herdr 会因为 agent 状态变化自己换焦点，不锁定的话「为 A 写的话投进了 B」。自动拉回来还没动过的内容不算草稿，那时候切 pane 照样跟着换。
- **「双向」只对 claude / codex 这种有真输入框的 pane 生效。** 普通 pane 里跑的可能是 vim 或某个选择器，那里的字符是**命令**不是文本。开着的时候也别同时在那个 pane 里手敲字——本地→远端这个方向本质上是在跟字节流抢缓冲区。
- **远端正开着选择框 / 确认框时会拒绝投递**（清不空就不投，否则就是「残留 + 新文本」一起回车）。去那个 pane 按 `Esc` 收掉再投。
- socket 在**跑 herdr server 的那台机器**上。现在只连本机（或 `HERDR_WEB_SOCKET` 指到的路径）。

## 软键条

手机没有 Ctrl 键，herdr 的 `ctrl+b` 前缀全靠这条。按键**存在服务端**（`~/.herdr-web/softkeys.json`），手机 / 平板 / 电脑共用一份，点软键条最右边的 ⚙ 在网页上改。

- 「按键」一栏写**按键谱**，空格分隔可以连发多下 —— `ctrl+b c` 就是 herdr 的前缀加 c，一下点出来。
- 支持 `ctrl+x` `alt+x` `shift+tab`、具名键（`esc tab enter space bs del ins up down left right home end pgup pgdn f1-f12`）、双引号里的原样文本（`"herdr" enter` = 敲 herdr 再回车）。
- 编辑器每行有个「常用…」下拉，51 条预设分 6 组（前缀 / 标签 / Pane / 工作区 / 终端按键 / 文本），按键谱抄的是 `herdr --default-config` 的 `[keys]` 默认值。改过 herdr keybinding 的人手输。
- `Ctrl` / `Alt` 是**粘滞**的：点一下亮起，再敲一个字母就发出对应组合键，然后自动灭掉。手机虚拟键盘的 keydown 不可靠，所以这层是在数据流上做的，不依赖按键事件。写法是 `sticky:ctrl` / `sticky:alt`，呼出键盘写 `act:kbd`。
- 按键谱在**服务端**解析成字节再下发，前端只管照发；写错了保存时会告诉你是第几个按键、哪里不认。

## 手机

xterm.js 的触屏支持基本只有「点一下聚焦隐藏 textarea」，剩下全靠自己补。有程序在收鼠标上报时（herdr 这种），触屏手势整个由本项目接管：

| 手势 | 行为 |
|---|---|
| 单指纵向滑动 | 按行高换算成 SGR 滚轮上报 `CSI < 64/65 ; col ; row M` 发给程序；没开鼠标上报时滚本地 scrollback |
| 单击 | 发 `CSI < 0 ; col ; row M/m` 给程序（点 pane、点 tab 都好使），**不弹系统键盘** |
| 长按 | 什么都不做。既不弹选择气泡也不弹键盘 |
| 双击 | 显示 / 收起系统键盘 |

为什么要这么绕：xterm.js 只把 `wheel` 翻译成鼠标上报、完全不管 touch，所以 herdr 这种「占着备用屏幕（本地没 scrollback 可滚）+ 开了鼠标上报」的程序在手机上两头都不响应，彻底滚不动。而点击和长按又都会落到隐藏 textarea 上，浏览器顺手就把键盘顶出来 —— 在 TUI 里十次里有九次只是想点个 pane，不是想打字。

做法是在 `touchstart` 上 `preventDefault`（仅当有鼠标上报时），一次性掐掉聚焦、长按气泡、双击缩放和浏览器补发的兼容鼠标事件，然后自己在 `touchend` 里按位移和时长把手势分成上面四类。普通 shell 下不接管，浏览器原生行为保留。

连上时也不自动抢焦点（触屏设备），否则一连上键盘就顶出来。要打字：双击终端，或点软键条最左边的 ⌨。键盘状态跟着 textarea 的 focus/blur 走，所以用户自己收起键盘时按钮高亮也会跟着灭。

**虚拟键盘不会再盖住内容**：页面高度跟 `visualViewport` 走，并给 viewport meta 加了 `interactive-widget=resizes-content`。光重排终端不够 —— iOS 的键盘**从不**缩布局视口，`height:100%` 指的是没缩过的那个，键盘会直接盖住软键条和发件箱。

横屏比竖屏好用很多（列数够）；字号用顶栏 `A− / A+` 调。

## 实测结论：herdr 在浏览器里是能正常用的

herdr 启动时会请求这些终端能力（用 PTY 抓下来的），对照现在的实现：

| 序列 | 用途 | 状态 |
|---|---|---|
| `CSI ? 1049 h` | 备用屏幕 | xterm.js 原生 |
| `CSI ? 1000/1002/1003 h` + `1006` | 鼠标点击/拖拽/移动 + SGR 坐标 | xterm.js 原生 |
| `CSI ? 2004 h` | 括号粘贴 | xterm.js 原生 |
| `CSI ? 1004 h` | 焦点进出上报 | xterm.js 原生 |
| `CSI ? 2026 h` | 同步输出（防画面撕裂） | xterm.js 原生，另加了重绘看门狗（见下） |
| `OSC 8` | 终端超链接 | xterm.js 原生，点击在新标签页打开 |
| `OSC 52` | 程序写系统剪贴板 | ClipboardAddon |
| `OSC 10;? / 11;?` | 查询前景/背景色（判断明暗） | xterm.js 不回，**本项目自己回** |
| `CSI ? 2031 h` | 主题变更通知 | xterm.js 不支持，**本项目自己发** `CSI ? 997 ; 1/2 n` |
| `CSI > 7 u` | kitty 键盘协议 | xterm.js 不支持，**本项目补了消歧子集** |

点顶栏最右的 ⌘ 图标可以看到当前会话里程序实际请求了哪些能力，以及那几个开关。

## 键盘

herdr 的快捷键基本都是 `ctrl+b` 前缀加一个普通键，legacy 编码就能表达，所以不依赖 kitty 协议。kitty 协议补的是 legacy 表达不了的组合，默认开着（能力面板里可关）：`Ctrl+Shift+字母` → `CSI 编码;6u`、`Ctrl+数字` → `CSI 编码;5u`、`Ctrl+Enter` / `Shift+Enter` / `Ctrl+Tab`。

抢不回来的键（浏览器自己吃掉）：macOS 上是 `⌘W` `⌘T` `⌘N` `Ctrl+Tab`；Windows/Linux 上还多 `Ctrl+W` `Ctrl+T` `Ctrl+N` `Ctrl+Shift+I/J/C`。真要用这些，把页面装成 PWA 能拿回一部分。

复制 `⌘C`（或 `Ctrl+Shift+C`）· 粘贴 `⌘V` · 清屏 `⌘K` · `Option` 默认当 Meta。

## 代码结构

```
cmd/herdr-web/        main：flag、监听、启动横幅、网卡打分
internal/
  config/             环境变量、路径、落盘 token
  herdr/              herdr socket 客户端（一次调用一条连接）
  composer/           按 agent 分派抽输入框 + testdata 里的真机抓屏
  outbox/             列目标 / 拉回 / 清空 / 投稿 / 推草稿
  softkeys/           软键条配置 + 按键谱解析（data.go 是生成的）
  uploads/            图片落盘（按魔数认类型）
  server/             HTTP 路由 + PTY/WebSocket + 静态资源
  webui/              embed 前端产物（dist 由 make build 拷进来）
  qr/                 启动时在终端画二维码
web/                  Vite + React + TS + Tailwind v4 + shadcn 风格组件
  src/term/           xterm.js 胶水：补协议、触屏手势、重绘看门狗（命令式，不套 React）
  src/hooks/          useCompose（发件箱状态机）、useViewportHeight
reference/            最早的 Python 原型，HANDOFF 里那些「已验证」都是拿它验的
```

`make test` 跑 Go 测试 + 前端 typecheck。`make dev` 前端热更新（后端另开一个 `go run ./cmd/herdr-web`，vite 把 `/api` 和 `/pty` 转过去）。

**为什么终端那层不是 React 组件**：它要直接摸 xterm 的 parser、逐字节收 WebSocket、按 rAF 补重绘 —— 套上 React 的渲染周期只会碍事。React 那边只拿一个 ref 挂载它，再订阅几个状态回调。

## 几个坑（已经处理了，记下来免得回头再踩）

- **`HERDR_*` 会让 herdr 拒绝启动**。如果本服务是在 herdr 的 pane 里起的，子进程继承到就会报 `nested herdr is disabled by default`。`internal/server/pty.go` 的 `dropEnv` 把 `HERDR_* / TMUX / ZELLIJ / ITERM_* / CLAUDECODE` 这些痕迹都清了。
- **xterm.js 6.0 会「收下重绘请求但不画」**：DEC 2026 同步输出开着时把范围攒起来等 ESU；绘制在 rAF 里，后台标签页完全不跑。herdr 常驻开着 2026、一帧几 KB 还会被拆成多次 write，攒漏一次屏幕上就留一块空白。缓冲区没坏，所以只补重绘：数据流停下来 180ms 后强制画一次，2026 卡着就自己补个 ESU。频繁出现可以在能力面板里关掉同步输出。
- **herdr 的主题不跟浏览器切换**：`~/.config/herdr/config.toml` 里 `[theme] auto_switch = false`。改成 `true` 之后，网页上切明暗就能直接切 herdr 的配色。
- **别把 `HERDR_WEB_SETTLE_MS` 调成 0**：详见环境变量表。

## 环境变量

| 变量 | 默认 | 说明 |
|---|---|---|
| `HERDR_WEB_PORT` | `7788` | 端口 |
| `HERDR_WEB_HOST` | `127.0.0.1` | 监听地址，`0.0.0.0` 开局域网 |
| `HERDR_WEB_TOKEN` | 读 `~/.herdr-web/token` | 覆盖落盘的 token |
| `HERDR_WEB_SHELL` | `$SHELL` | PTY 里跑的 shell |
| `HERDR_WEB_DIR` | `~/.herdr-web` | token / 软键条 / 上传图片的存放目录 |
| `HERDR_WEB_SOCKET` | `$HERDR_SOCKET_PATH` 或 `~/.config/herdr/herdr.sock` | 发件箱连的 herdr socket。**别依赖 `HERDR_SOCKET_PATH`**：`dropEnv` 会把 `HERDR_*` 清掉，而本进程也可能不是从 herdr pane 里起的 |
| `HERDR_WEB_POLL_MS` | `500` | 发件箱多久对一次「焦点在哪 + 输入框里是什么」。下限 200 |
| `HERDR_WEB_PUSH_MS` | `700` | 开着「双向」时，停手多久把草稿推到远端。下限 100 |
| `HERDR_WEB_SETTLE_MS` | `120` | 两次 `pane.read` 之间等多久（对付快照的一帧延迟）。**别调成 0**：herdr 响应有时只要 1-2ms，两次读会落在同一帧上，清空循环会误判成「清不空」。清空那条路自己有 120ms 保底 |

## 安全

**这个东西等于一个 HTTP 上的 shell**，只有一层 token。默认只听 `127.0.0.1`，够本机自己用。

监听 `0.0.0.0` 之后，**局域网里任何拿到 token 的人都能拿到你的 shell**，而且 http 明文传输、token 就在 URL 里。临时试用可以，别长期这么放着。要长期用（尤其想从外网连）就套一层能做 TLS + 真身份认证的入口：Tailscale Serve、Cloudflare Tunnel、nginx + OIDC 都行。

另外 http 不是安全上下文，`navigator.clipboard` 不可用，所以手机上 `OSC 52` 会失效、`⌘C` 退回 `execCommand('copy')` 兜底。套上 HTTPS 就都正常。
