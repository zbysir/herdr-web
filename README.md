# herdr-web

<p align="center">
  <img src="assets/logo.png" alt="herdr-web" width="96" />
</p>

<p align="center">
  <b>简体中文</b> · <a href="README.en.md">English</a>
</p>

浏览器里的终端，用来跑 [`herdr`](https://github.com/zbysir/herdr)。一个 Go 二进制，前端嵌在里面，
手机也能用。

**语音投稿**是主功能：在平板上说话打字，说错的字框选重说就改掉，改完整段投进 agent 的输入行。
手机上够用，平板横屏 211 列 —— 那是个工位。

```
┌─────────────────────────────────────────────┐
│  面板一览  文件  改动  对话   ...   设置    │  顶栏：放哪几个自己拖
├─────────────────────────────────────────────┤
│                                             │
│       herdr 的 pane，和电脑上一模一样       │
│                                             │
├─────────────────────────────────────────────┤
│  说完的一整段话...            [   投稿   ]  │  发件箱：一行，回车就投
│  [键盘][^B][Ctrl][Esc][方向]...横滑...      │  快捷键条：手机没有 Ctrl 键
└─────────────────────────────────────────────┘
```

## 装

```bash
npm install -g @bysir/herdr-web    # 有 node 最省事，升级也交给它
herdr-web                          # 只听 127.0.0.1
```

没有 node（服务器上常见）：

```bash
curl -fsSL https://raw.githubusercontent.com/zbysir/herdr-web/master/install.sh | sh
```

装到 `~/.local/bin`，**强制校验 sha256** —— 这东西后面挂着一个登录 shell。

Windows 没有原生版，在 WSL 里装（浏览器里那个终端要一个真 PTY）。`go install` 也能装，
但**装出来的没有前端**（前端产物不入版本库）。

→ 从源码装、换安装目录、装指定版本：[使用说明 · 装](docs/USAGE.zh-CN.md#装)

## 用

手机 / 平板连进来：

```bash
HERDR_WEB_HOST=0.0.0.0 herdr-web
```

启动横幅里有一个**一次性配对码**和它的二维码，手机扫一下就进去了。**一台设备配一次** ——
凭据在 HttpOnly cookie 里，换 Wi-Fi、换网段、重启都不用重来。连上之后自动敲 `herdr`。

地址栏里加一段路径就是**另一个 herdr session**：`/work` 敲的是 `herdr --session work`，
两个书签就是两套工作现场，关掉浏览器再回来还在。

```bash
herdr-web pair          # 再出一个一次性配对码 + 二维码
herdr-web devices       # 列出已配对设备
herdr-web revoke <id>   # 踢掉某台（all = 全部），下一个请求立刻 401
```

→ 管理页、session 命名规则、连上敲什么：[使用说明 · 第一次跑](docs/USAGE.zh-CN.md#第一次跑)

## 能干什么

| | |
|---|---|
| **发件箱** | 底下那一行输入框，说完一整段投进 agent 的输入行。回车就投，`⇧↵` 换行；截图直接 `⌘V`，手机上拍照 / 录像传过去接成路径 |
| **快捷键条** | 手机没有 Ctrl 键，herdr 的 `ctrl+b` 前缀全靠它。键自己配（按键谱 / 图标 / 弹出组 / 钉住不跟着滑），方向键按住连发 |
| **面板一览** | 一张 pane 列表，点一行跳过去并铺满全屏。agent 停下来等你回答时，顶栏上点一个红点 |
| **对话** | 把 agent 自己写的会话记录读成一条对话流，代替那一屏 TUI —— 手机上读它比读 TUI 舒服得多 |
| **文件** | agent 说「图生成在 `/tmp/plot-3.png`」，点那行路径就能看。图直接看，md 渲染成文档，文本能就地改 |
| **改动** | `git diff` 在手机终端里基本读不了：这儿能折行、按词高亮，一次改动的全部文件是一条连续的流。**只读** |
| **手机和平板** | 触屏手势整套接管（滑动 = 滚轮上报、长按 = 拖 pane 边框），底部面板能挪到右边，横竖屏各一套排布 |
| **装成 app** | PWA：独立窗口、没有地址栏和工具条，白送好几行终端 |

→ 每一件具体怎么用：[使用说明 · 能干什么](docs/USAGE.zh-CN.md#能干什么)

## 配

**配置只有一个来源：环境变量**（没有配置文件，命令行标志只有一个 `--web`）。几套常见的：

```bash
herdr-web                                       # 1. 纯本机（默认），明文 http
HERDR_WEB_HOST=0.0.0.0 herdr-web                # 2. 局域网的手机 / 平板，自签 TLS + 二维码
HERDR_WEB_ONCONNECT= herdr-web                  # 3. 别自动进 herdr，留在 shell 里

# 4. 走 frp / 隧道暴露到公网：隧道指公网口，别指主口
HERDR_WEB_PUBLIC_PORT=17788 HERDR_WEB_TLS=proxy \
HERDR_WEB_PUBLIC_URL=https://herdr.example.com \
HERDR_WEB_HOSTNAME=herdr.example.com herdr-web
```

主口（默认 7788）**只服务本地网络**：对端不是本机 / 私网一律 403。要暴露就另开
`HERDR_WEB_PUBLIC_PORT`，判据是「请求落在哪个监听上」而不是一句声明 ——
在一台挂着隧道的机器上，`127.0.0.1:7788` 有可能整个互联网都连得到，而本地一点症状都没有。

→ 四十多个变量的完整表：[使用说明 · 配置](docs/USAGE.zh-CN.md#配置)

## 常驻 · 更新

```bash
herdr-web service install    # macOS → launchd，Linux → systemd user unit，开机自启
herdr-web update             # 查 + 升（怎么升看当初是怎么装的）
```

配置是 `install` 那一刻从当前 shell 抄进去的，所以改配置 = 换个环境重新 `install`（幂等）。

→ [常驻](docs/USAGE.zh-CN.md#守护进程) · [更新](docs/USAGE.zh-CN.md#更新)

## 安全

这东西等于一个 HTTP 上的 shell，门是按这个前提设计的：

- **一台设备配一次**：一次性配对码换一份 per-device 凭据，服务端**只存 sha256**；
- **凭据绑设备不绑 IP**，换网不掉线；**URL 里没有秘密**（`?pair=` 进来就换成 cookie 再 302 洗掉）；
- **配对码只能由坐在机器前的人产生** —— 网页上任何路径都不出码，那是唯一的带外因子；
- **暴露出去又没 TLS 就拒绝启动**；Host 白名单挡 DNS rebinding，三道挡 CSRF，猜码指数退避 + 封锁；
- **passkey 是第二因子**，服务端只存公钥。

→ 威胁模型、每条为什么这么设计：[SECURITY.md](docs/dev/SECURITY.md)

## 文档

| 要看什么 | 去哪儿 |
|---|---|
| 怎么装、怎么用、每个配置项什么意思 | [docs/USAGE.zh-CN.md](docs/USAGE.zh-CN.md) |
| 放在哪儿跑、公网访问、TLS 四档 | [DEPLOY.md](DEPLOY.md) |
| 各家 DNS 的 token 怎么拿 | [DNS.md](DNS.md) |
| **为什么这么设计**、实测出来的语义、会静默出错的坑 | [docs/dev/](docs/dev/README.md) |
| 改代码之前先读（代码结构、发版、配色） | [CLAUDE.md](CLAUDE.md) |

MIT。
