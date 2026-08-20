# herdr-web

浏览器里的终端，用来跑 `herdr`。带主机管理和托管密钥，手机也能用。

> **语音投稿**（平板手写笔说话打字 → 投进 agent pane）已经能用了，见下面「发件箱」一节。设计取舍、herdr socket API 的已验证语义和踩过的坑在 [HANDOFF.md](HANDOFF.md)。

```bash
cd herdr-web
npm install       # 已经装好了，跳过也行
npm start         # 只听 127.0.0.1
npm run lan       # 听 0.0.0.0，顺便在终端画个二维码给手机扫
```

启动后会打印可用地址；`npm run lan` 会按网卡打分，把手机真能连上的那个标成 `← 手机用这个`（机器上一堆 OrbStack / VPN 虚拟网卡都会被压到后面），二维码编的就是它。

token 存在 `~/.herdr-web/token`（0600），**重启不变**，所以手机上存的书签一直有效。想换一把：`rm ~/.herdr-web/token` 再启动。连不上时页面会自己分辨是「后端没起」还是「token 不对」，不用猜。

## 三种连法

| 模式 | 说明 |
|---|---|
| **本机** | 在跑 server 的机器上开一个 `$SHELL -l` 的 PTY，零配置 |
| **SSH + 保存的主机** | 从下拉框选一台，用它绑定的密钥连 |
| **SSH + 临时地址** | 直接敲 `user@host`，用系统 ssh 的默认行为 |

不管哪种，连上之后敲 `herdr`（或点「敲 herdr」）就行。

## 主机与密钥（顶栏 ⚙）

- **主机**：名称 / 主机 / 用户 / 端口 / 密钥 / ProxyJump 跳板 / 是否首次连接自动信任指纹。存在 `~/.herdr-web/hosts.json`（0600）。
- **导入 ssh_config**：把 `~/.ssh/config` 里的非通配 Host 别名一键存成主机。存的就是别名本身，user/port/key 全留空 —— ssh 自己会去读配置，不用在这儿重复填。
- **托管密钥**：
  - 「+ 生成 ed25519」当场生成一把，生成完公钥自动进剪贴板，贴到远端 `authorized_keys` 就能用；
  - 「导入私钥」把已有私钥贴进来，落到 `~/.herdr-web/keys/<name>`（0600）；
  - 每把密钥显示类型、位数、SHA256 指纹，带 passphrase 的会标「已加密」；
  - 「复制公钥」一键拿 `authorized_keys` 那一行。
- **~/.ssh 里现成的密钥**：自动扫出来，主机里选它就是直接 `-i` 引用原路径，不复制不搬家。
- **装公钥**：跑 `ssh-copy-id` 把这台主机绑定的公钥装到远端，远端要的密码直接在网页终端里输。

**不存的东西**：私钥 passphrase、登录密码、sudo 密码一概不存也不经过浏览器。系统 ssh 会在网页终端里当场问你，输完就完事。私钥内容也不会回传给前端 —— 接口只给指纹和公钥。

主机名 / 用户 / 跳板都过白名单正则，密钥名不允许出现路径分隔符，命令行是 argv 数组直接 exec，不过 shell。

## 发件箱（语音投稿）

页面底下那条带 textarea 的就是发件箱，顶栏 `✎` 开关，**默认开着**。在里面说话打字，改完整段投进 herdr 的某个 pane。

为什么要单独一个框而不是直接对着终端说：终端是字节流，没有 selection 语义，输入法只能往里灌字符。「说错的字**框选重说**」需要一个真正的可编辑字段（有文本模型、有选区，选中后输入法提交会覆盖选区）。xterm.js 的隐藏 textarea 不算，它只把按键转成字节发走。

| 操作 | 说明 |
|---|---|
| **目标** | 默认「跟随 herdr 当前 pane」——不用选，投给你此刻在 herdr 里激活的那个。也能在下拉里钉死一个 |
| **投稿** `⌘↵` / `Ctrl↵` | 先清空远端输入行，再整段提交。`Enter` 是换行，不提交 |
| **拉回** | 把远端输入框里已有的内容抓进 textarea 编辑（远端按过 Tab 补全就用它） |
| **自动拉回** | 2s 一拍。切了 pane 自动换成新 pane 的内容；**本地有草稿时绝不覆盖**，只在状态行提示 |
| **`双向`** | 本地改动跟着推回远端输入框（不回车）。默认关，见下面的注意事项 |
| **`📎 图`** | 传图片。手机上点它会给「相机 / 相册」；电脑上截完图直接在框里 `⌘V`，或者把文件拖进来 |
| `↑` | 框空时取回上一条投过的（本地留 30 条） |

**图片是怎么走通的**：herdr 的 socket API 里没有图片概念，能投的只有文本。但 claude 和 codex 都能直接读磁盘上的图片文件（实测两边都描述对了一张 320×200 左红右蓝的测试图，codex 还会打一行 `Viewed Image`）。所以「传图」＝ 存到跑 herdr 那台机器的 `~/.herdr-web/uploads/`，然后把**绝对路径插进提示词**，agent 自己去读文件。路径就在框里，可以随便在前后补话。

手机照片先在浏览器里缩到长边 2400 再传（顺手把 iPhone 的 HEIC 转成 PNG/JPEG，因为 agent 读不了 HEIC）。服务端按**魔数**认类型，只收 png / jpg / gif / webp，改后缀或改 content-type 骗不过去。上限 25 MB。传过的图不会自动删，攒多了自己清 `~/.herdr-web/uploads/`。

状态行会一直显示「这段话现在会投给谁」，`⟳` 表示正在跟随焦点；鼠标悬停能看到当前用的轮询间隔。

### 是轮询，不是推送

herdr 有 `events.subscribe` 推送通道，但 agent 一 working 就是刷屏级的量，所以这里用轮询：每 `HERDR_WEB_POLL_MS`（默认 500ms）问一次「焦点在哪个 pane + 那个输入框里是什么」。一拍打 3 次 socket 调用，约 150–320ms。

**切 pane 到 textarea 更新的实测延迟**（同机，8 次取样）：

| 轮询间隔 | 最快 | 中位 | 最慢 |
|---|---|---|---|
| 200ms | 138ms | 318ms | 550ms |
| **500ms（默认）** | ~300ms | ~500ms | ~800ms |
| 1200ms | 408ms | 794ms | 818ms |

地板是一次 sync 的耗时（~150–300ms），因为**每次 herdr 调用有 ~100ms 的硬地板**（原因见 HANDOFF 的「100ms 的坎」），调再快也突破不了。想临时试手感：URL 上加 `?poll=200&push=400`，会覆盖服务端下发的默认值。

几个要知道的：

- **框里一有内容，目标就被钉在当初瞄准的那个 pane 上**，框空了才重新跟随焦点。因为 herdr 会因为 agent 状态变化自己换焦点，不锁定的话「为 A 写的话投进了 B」。
- **`双向` 只对 claude / codex 这种有真输入框的 pane 生效。** 普通 pane 里跑的可能是 vim 或某个选择器，那里的字符是**命令**不是文本。开着 `双向` 的时候也别同时在那个 pane 里手敲字——本地→远端这个方向本质上是在跟字节流抢缓冲区。
- **远端正开着选择框 / 确认框时会拒绝投递**（清不空就不投，否则就是「残留 + 新文本」一起回车）。去那个 pane 按 `Esc` 收掉再投。
- socket 在**跑 herdr server 的那台机器**上。现在只连本机，ssh 模式下 socket 在远端那条路还没接。

`npm test` 跑输入框抽取的回归测试（fixture 是真机抓屏）。

## 手机

xterm.js 的触屏支持基本只有「点一下聚焦隐藏 textarea」，剩下全靠自己补。有程序在收鼠标上报时（herdr 这种），触屏手势整个由本项目接管：

| 手势 | 行为 |
|---|---|
| 单指纵向滑动 | 按行高换算成 SGR 滚轮上报 `CSI < 64/65 ; col ; row M` 发给程序；没开鼠标上报时滚本地 scrollback |
| 单击 | 发 `CSI < 0 ; col ; row M/m` 给程序（点 pane、点 tab 都好使），**不弹系统键盘** |
| 长按 | 什么都不做。既不弹选择气泡也不弹键盘 |
| 双击 | 显示 / 收起系统键盘 |

为什么要这么绕：xterm.js 只把 `wheel` 翻译成鼠标上报、完全不管 touch，所以 herdr 这种「占着备用屏幕（本地没 scrollback 可滚）+ 开了鼠标上报」的程序在手机上两头都不响应，彻底滚不动。而点击和长按又都会落到隐藏 textarea 上，浏览器顺手就把键盘顶出来 —— 在 TUI 里十次里有九次只是想点个 pane，不是想打字。

做法是在 `touchstart` 上 `preventDefault`（仅当有鼠标上报时），一次性掐掉聚焦、长按气泡、双击缩放和浏览器补发的兼容鼠标事件，然后自己在 `touchend` 里按位移和时长把手势分成上面四类。普通 shell 下不接管，浏览器原生行为保留（点一下就能打字、长按还能用系统的粘贴气泡）。

连上时也不再自动抢焦点（触屏设备），否则一连上键盘就顶出来。要打字：双击终端，或点软键条最左边的 `⌨`。键盘状态跟着 textarea 的 focus/blur 走，所以用户自己收起键盘时按钮高亮也会跟着灭。
- 顶栏 `⌨` 开软键盘条：`⌃B 前缀`、`Ctrl`、`Alt`、`Esc`、`Tab`、方向键、`PgUp`、`PgDn`、`⌃C`、`↵`。触屏设备默认自动打开。
- `Ctrl` / `Alt` 是**粘滞**的：点一下亮起，再敲一个字母就发出对应组合键，然后自动灭掉。因为手机虚拟键盘的 keydown 不可靠，这层是在数据流上做的，不依赖按键事件。
- 只点 `⌃B 前缀` 就能进 herdr 的 prefix 模式，手机上没有 Ctrl 键也不影响用。
- 虚拟键盘弹出会触发 `visualViewport` resize → 自动重新 fit 并发 SIGWINCH。
- 横屏比竖屏好用很多（列数够）；字号用顶栏 `A− / A+` 调。

## 实测结论：herdr 在浏览器里是能正常用的

herdr 启动时会请求这些终端能力（用 PTY 抓下来的），对照现在的实现：

| 序列 | 用途 | 状态 |
|---|---|---|
| `CSI ? 1049 h` | 备用屏幕 | xterm.js 原生 |
| `CSI ? 1000/1002/1003 h` + `1006` | 鼠标点击/拖拽/移动 + SGR 坐标 | xterm.js 原生，点击、拖拽、滚轮都通 |
| `CSI ? 2004 h` | 括号粘贴 | xterm.js 原生 |
| `CSI ? 1004 h` | 焦点进出上报 | xterm.js 原生 |
| `CSI ? 2026 h` | 同步输出（防画面撕裂） | xterm.js 6.0 原生 |
| `OSC 8` | 终端超链接 | xterm.js 原生，点击在新标签页打开 |
| `OSC 52` | 程序写系统剪贴板 | ClipboardAddon |
| `OSC 10;? / 11;?` | 查询前景/背景色（判断明暗） | xterm.js 不回，**本项目自己回** |
| `CSI ? 2031 h` | 主题变更通知 | xterm.js 不支持，**本项目自己发** `CSI ? 997 ; 1/2 n` |
| `CSI > 7 u` | kitty 键盘协议 | xterm.js 不支持，**本项目补了消歧子集** |

点右上角 `⌘?` 可以看到当前会话里程序实际请求了哪些能力。

已经端到端验证过的：herdr 界面完整渲染 · `ctrl+b` 前缀模式 · `ctrl+b ?` keybinds 浮层 · 鼠标点击和滚轮 · 改字号触发 SIGWINCH 后整体重排 · 中文粘贴 · 断线重连后回到原 workspace · 用托管密钥 ssh 到远端再跑 herdr（含首次连接的指纹确认提示直接在网页终端里完成）· 软键盘条的 `⌃B` 和粘滞 Ctrl。

## 键盘

herdr 的快捷键基本都是 `ctrl+b` 前缀加一个普通键，legacy 编码就能表达，所以不依赖 kitty 协议。

kitty 协议补的是 legacy 表达不了的组合，默认开着（`⌘?` 面板里可关）：

- `Ctrl+Shift+字母` → `CSI 编码;6u`
- `Ctrl+数字` → `CSI 编码;5u`
- `Ctrl+Enter` / `Shift+Enter` / `Ctrl+Tab`

抢不回来的键（浏览器自己吃掉，改不了）：macOS 上是 `⌘W` `⌘T` `⌘N` `Ctrl+Tab`；Windows/Linux 上还多 `Ctrl+W` `Ctrl+T` `Ctrl+N` `Ctrl+Shift+I/J/C`。真要用这些，把页面装成 PWA（Chrome「安装到程序坞」）能拿回一部分。

复制 `⌘C`（或 `Ctrl+Shift+C`）· 粘贴 `⌘V` · 清屏 `⌘K` · `Option` 默认当 Meta（`alt+1`、`alt+g` 这类快捷键要靠它）。

## 几个坑（已经处理了，记下来免得回头再踩）

- **`HERDR_ENV` 会让 herdr 拒绝启动**。如果 server 本身是在 herdr 的 pane 里起的，子进程继承到 `HERDR_*` 就会报 `nested herdr is disabled by default`。`server.js` 的 `DROP_ENV` 把 `HERDR_* / TMUX / ZELLIJ / ITERM_* / CLAUDECODE` 这些痕迹都清了。
- **node-pty 的 prebuild 会丢 `spawn-helper` 的可执行位**，症状是 `posix_spawnp failed`。启动时会自动 `chmod +x`。
- **herdr 的主题不跟浏览器切换**：`~/.config/herdr/config.toml` 里 `[theme] auto_switch = false`。改成 `true` 之后，网页右上角的 ◐ 就能直接切 herdr 的配色（通知已经发过去了）。
- **`ssh localhost` 走不通**：本机 sshd 只开了 publickey，而 `~/.ssh/authorized_keys` 里没有自己的 key。要连本机就执行一次
  `cat ~/.ssh/id_rsa.pub >> ~/.ssh/authorized_keys`。连远端主机不受影响。
- **窄屏顶栏用 `flex: 0 0 100%` 而不是 `width: 100%`**：`flex: 1` 的 `flex-basis: 0` 会让 `width` 失效，手机上整条顶栏会挤成一行。

## 环境变量

| 变量 | 默认 | 说明 |
|---|---|---|
| `HERDR_WEB_PORT` | `7788` | 端口 |
| `HERDR_WEB_HOST` | `127.0.0.1` | 监听地址，`0.0.0.0` 开局域网 |
| `HERDR_WEB_TOKEN` | 读 `~/.herdr-web/token` | 覆盖落盘的 token |
| `HERDR_WEB_SHELL` | `$SHELL` | 本机模式用的 shell |
| `HERDR_WEB_SSH` | `/usr/bin/ssh` | ssh 客户端路径 |
| `HERDR_WEB_DIR` | `~/.herdr-web` | 主机和密钥的存放目录 |
| `HERDR_WEB_SOCKET` | `$HERDR_SOCKET_PATH` 或 `~/.config/herdr/herdr.sock` | 发件箱连的 herdr socket。**别依赖 `HERDR_SOCKET_PATH`**：`DROP_ENV` 会把 `HERDR_*` 清掉，而 server 自己也可能不是从 herdr pane 里起的 |
| `HERDR_WEB_POLL_MS` | `500` | 发件箱多久对一次「焦点在哪 + 输入框里是什么」。下限 200 |
| `HERDR_WEB_PUSH_MS` | `700` | 开着「双向」时，停手多久把草稿推到远端。下限 100 |
| `HERDR_WEB_SETTLE_MS` | `0` | 两次 `pane.read` 之间额外等多久。默认 0 就够（每次调用本来就隔了一个服务端 tick），一般不用动 |

## 安全

**这个 demo 等于一个 HTTP 上的 shell**，只有一层 token。默认只听 `127.0.0.1`，够本机自己用。

`npm run lan` 之后，**局域网里任何拿到 token 的人都能拿到你的 shell**，而且 http 明文传输、token 就在 URL 里。临时试用可以，别长期这么放着。要长期用（尤其想从外网连）就套一层能做 TLS + 真身份认证的入口：Tailscale Serve、Cloudflare Tunnel、nginx + OIDC 都行。

另外 http 不是安全上下文，`navigator.clipboard` 不可用，所以手机上 `OSC 52` 会失效、`⌘C` 退回 `execCommand('copy')` 兜底。套上 HTTPS 就都正常。
