# herdr-web

浏览器里的终端，用来跑 `herdr`。带主机管理和托管密钥，手机也能用。

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

## 安全

**这个 demo 等于一个 HTTP 上的 shell**，只有一层 token。默认只听 `127.0.0.1`，够本机自己用。

`npm run lan` 之后，**局域网里任何拿到 token 的人都能拿到你的 shell**，而且 http 明文传输、token 就在 URL 里。临时试用可以，别长期这么放着。要长期用（尤其想从外网连）就套一层能做 TLS + 真身份认证的入口：Tailscale Serve、Cloudflare Tunnel、nginx + OIDC 都行。

另外 http 不是安全上下文，`navigator.clipboard` 不可用，所以手机上 `OSC 52` 会失效、`⌘C` 退回 `execCommand('copy')` 兜底。套上 HTTPS 就都正常。
