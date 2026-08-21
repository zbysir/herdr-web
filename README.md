# herdr-web

<p align="center">
  <img src="assets/logo.png" alt="herdr-web" width="96" />
</p>

浏览器里的终端，用来跑 `herdr`。一个 Go 二进制（前端嵌在里面），手机也能用。

> **语音投稿**（平板手写笔说话打字 → 投进 agent pane）是这个项目的主功能，见下面「发件箱」。三份配套文档：发件箱的设计取舍在 [OUTBOX.md](OUTBOX.md)，抽输入框那套坑在 [COMPOSER.md](COMPOSER.md)，herdr socket API 实测出来的语义在 [HERDR-API.md](HERDR-API.md)。

## 装

```bash
npm install -g @bysir/herdr-web       # 有 node 的话最省事，升级也交给它
herdr-web                             # 只听 127.0.0.1
```

没有 node（服务器上常见）：

```bash
curl -fsSL https://raw.githubusercontent.com/zbysir/herdr-web/master/install.sh | sh
```

装到 `~/.local/bin`。要换地方，变量得给 `sh` 而**不是** `curl`：

```bash
curl -fsSL …/install.sh | HERDR_WEB_INSTALL_DIR=/opt/bin sh    # 对
HERDR_WEB_INSTALL_DIR=/opt/bin curl -fsSL …/install.sh | sh    # 错，变量给了 curl，脚本收不到
```

写错那条**不会报错**，它会安安静静装到默认目录去。同理 `HERDR_WEB_INSTALL_VER=v0.1.0` 装指定版本。

装脚本**强制校验 sha256**，没有 `sha256sum`/`shasum` 就直接拒绝装 —— 这东西后面挂着一个登录 shell。

其它几条路：

```bash
make build && ./herdr-web             # 从源码（前端 → 拷进 internal/webui/dist → go build）
HERDR_WEB_HOST=0.0.0.0 ./herdr-web    # 听局域网，顺便在终端画个二维码给手机扫
```

`go install github.com/zbysir/herdr-web/cmd/herdr-web@latest` 也能装，但**装出来的没有前端** ——
前端产物是 `make build` 生成后 embed 进去的，不入版本库，所以 `go install` 拿不到。那样装出来的
只能配 `--web <目录>` 指一份自己构建的前端，或者干脆只用命令行子命令。要能开页面就走上面那三条。

**Windows 没有原生版**，在 WSL 里装。不是懒：浏览器里那个终端需要一个真 PTY（Go 那边用 `creack/pty`，windows 上是个 `return nil, ErrUnsupported` 的空壳），herdr 自己也走 unix socket。WSL 里就是 linux 版，功能完整；浏览器那端本来就跨平台，Windows 上开 `http://localhost:7788/` 照样用。npm 包在 win32 上会直接打出这段提示而不是装一个跑不起来的东西。

装成开机自启的常驻服务：[守护进程](#守护进程)。升级：[更新](#更新)。

**配置只有环境变量这一个来源**（没有配置文件，命令行也只有一个 `--web`）：全部清单、怎么设、几套常见配法在下面的[配置](#配置)。子命令 `herdr-web --help`。

启动后会打印可用地址；监听 `0.0.0.0` 时会按网卡打分，把手机真能连上的那个标成 `← 手机用这个`（机器上一堆 OrbStack / VPN 虚拟网卡都会被压到后面），二维码编的就是它。

**一台设备配一次**：启动横幅里有一个一次性配对码（5 分钟过期、用一次就废）和它的二维码，手机扫一下就进去了 —— 零输入，之后书签里没有任何秘密（凭据在 HttpOnly cookie 里），换 Wi-Fi、换网段、重启都不用重来。再配一台就在机器上敲 `herdr-web pair`。

扫码有两条路：

- **相机 App 扫**（到处都能用）：码里就是带 `?pair=` 的链接，扫完直接打开，落地就已经配好。
- **配对页里的「用相机扫」**：开后摄、对准主机屏幕上那个码，认出来自动配对，不用跳转。这个按钮**能用才出现** —— 它依赖 `BarcodeDetector`（系统自带的解码器，省掉几十 KB 的 JS 库；macOS 走 Vision、Android 走 ML Kit，**iOS Safari 和 Linux 上的 Chrome 没有**）和摄像头（只在 https / localhost 这种安全上下文里给，局域网 http 下浏览器直接不给）。两个条件缺一个就不出这个按钮，也不留一个点了报错的入口。

两条都不方便就把那 8 位码抄进配对页的框里，抄够 8 位自动提交。页面内扫出来的内容只取 `pair=` 那一段，走的还是同一个 `POST /auth/pair`，安全模型没变（码照样只有坐在机器前的人能出）。

```bash
herdr-web pair          # 出一个新的一次性配对码 + 二维码
herdr-web devices       # 列出已配对设备（标签 / 最后活跃 / 最后 IP / 到期）
herdr-web revoke <id>   # 踢掉某台（all = 全部）；下一个请求立刻 401
herdr-web unlock        # 解开「失败太多」的全局熔断
```

网页上顶栏最右那个 ⚙ 是**设置面板**，「设备」页里看谁配过对、**登出这台**、踢掉别的。踢人和全部
踢掉都要点两下才生效。**网页上不出配对码**（连已配对的设备也不行），理由见下面「安全」。

**配对码用完了就回机器前** `herdr-web pair`。这不是偷懒 —— 见下。

原来那把永不过期的 `~/.herdr-web/token` 降级成**只能引导**：旧书签第一次打开会自动换成设备凭据、并把 URL 里的 token 抹掉，之后就该 `rm ~/.herdr-web/token`。细节和为什么这么设计看 [SECURITY.md](SECURITY.md)。

连上之后**自动敲 `herdr`**。想敲别的、或者不想自动敲：`HERDR_WEB_ONCONNECT`（设成空串就留在 shell 里）。地址栏里加一段路径（`/work`）就是**另一个 herdr session**，见下面「[一个 URL 一个 session](#一个-url-一个-sessionname)」。顶栏原来那个「敲 herdr」按钮去掉了 —— 自动敲之后它一天用不上一次，而软键条预设里有现成的「敲 herdr」键，要就自己放一个上去。

**管理页在 `http://127.0.0.1:<端口+1>/`**（启动横幅里有）：看证书状态、点一下签发/续期、生成 DNS 的 `.env` 片段、出配对码、踢设备。它**只绑 loopback，公网上不存在**，所以不需要登录 —— 能连上它的东西已经有你的 shell 了。为什么不做成「主服务上一个需要认证的页面」：认证是会失效的控制，「碰不到」是个性质；而且**管理页不能依赖它自己要管的那个证书**（证书一坏就打不开修证书的页面）。

## 一个 URL 一个 session（`/{name}`）

地址栏里加一段就是**另一个 herdr**：

```
https://herdr.bysir.top/          默认 session（老行为，敲的是 `herdr`）
https://herdr.bysir.top/work      敲 `herdr --session work` —— 没有这个 session 就新建一个
https://herdr.bysir.top/scratch   再来一个，和上面那个互不相干
```

`herdr --session <name>` 是 herdr 自己的**命名持久 session**：各有一个 server 进程、各有一套
workspace / tab / pane。所以书签存成 `/work` 和 `/scratch`，两个标签页就是两套工作现场，
关掉浏览器再回来还在（session 是持久的，网页断开只是客户端断开）。

要知道的几条：

- **发件箱和面板一览跟着 URL 走。** 命名 session 有自己的 socket
  （`~/.config/herdr/sessions/<name>/herdr.sock`，`herdr session list --json` 里就是这个），
  所以页面上每个请求都带着 session 名。这是这个功能里唯一真正危险的地方 —— 拿默认 session 的
  socket 去投一个 `/work` 页面上选的 pane，话会**静默进另一个 herdr**，而两边屏幕上都看不出
  异常。服务端因此**不给不合法的名字兜底**（不退回默认 session，直接报错）。
- **名字只能是 `[A-Za-z0-9._-]`、首字符字母数字、最长 40。** 因为它要被拼进一条敲进登录
  shell 的命令行，也要被拼进 socket 路径。不合法时页面上会直接说，而不是给你开一个别的 session。
- **`HERDR_WEB_ONCONNECT` 不参与带 session 的 URL**（包括设成空串「什么都别敲」那种）：
  地址栏里点名要哪个 session，比一个全局默认具体。`/` 还是老规矩。
- **顶栏左边会显示当前 session 名**（设置 →「终端」页底下也有一行，连着那个 session 的 socket
  路径）。默认 session 不显示这个标签 —— 没有标签就是默认那个。
- 一个进程最多同时盯 16 个 session（每个都带一条 agent 状态订阅）。超了会说，重启清空。
- 「添加到主屏幕」存的是 manifest 里的 `start_url`（`/`），所以从主屏图标进的是默认 session。
  要一个直达 `/work` 的图标，用浏览器书签。
- `herdr session list` / `stop` / `delete` 在终端里管这些 session，herdr-web 这边只负责「开/接上」。

## 只开本机 shell

要连别的机器就在 herdr 里 ssh —— herdr 自己就能干这事，所以这一层不再实现主机管理和托管私钥（那还得管密钥落盘、`ssh-keygen`、`~/.ssh` 扫描、ssh_config 导入），连带把「浏览器能碰到私钥」这个安全面也一起去掉了。

## 发件箱（语音投稿）

页面底下那条带 textarea 的就是发件箱，顶栏 ✎ 开关，**默认开着**。在里面说话打字，改完整段投进 herdr 的某个 pane。它和软键条装在**同一块底部面板**里（一套边框、一个宽度，怎么拖见下面「底部面板」那节）。

为什么要单独一个框而不是直接对着终端说：终端是字节流，没有 selection 语义，输入法只能往里灌字符。「说错的字**框选重说**」需要一个真正的可编辑字段（有文本模型、有选区，选中后输入法提交会覆盖选区）。xterm.js 的隐藏 textarea 不算，它只把按键转成字节发走。

| 操作 | 说明 |
|---|---|
| **目标** | 默认「跟随 herdr 当前 pane」——不用选，投给你此刻在 herdr 里激活的那个。也能在下拉里钉死一个 |
| **投稿** `⌘↵` / `Ctrl↵` | 先清空远端输入行，再整段提交。`Enter` 是换行，不提交 |
| **拉回** | 把远端输入框里已有的内容抓进 textarea 编辑（远端按过 Tab 补全就用它） |
| **自动拉回** | 默认 500ms 一拍。切了 pane 自动换成新 pane 的内容；**本地有草稿时绝不覆盖**，只在状态行提示 |
| **双向** | 本地改动跟着推回远端输入框（不回车）。默认关，见下面的注意事项 |
| **图** | 传图片，路径插在**光标处**。手机上点它会给「相机 / 相册」；电脑上截完图直接在框里 `⌘V`，或者把文件拖进来。不开发件箱也能传 —— 软键条里配个 `act:img` 或者整页粘贴 |
| `↑` | 框空时取回上一条投过的（本地留 30 条） |
| `Esc` | **转发给终端**。Esc 在纯 textarea 里没意义，而 agent 那边到处要用它（`/usage` 之类浮层靠它退出）；焦点不动，可以连按 |

**图片是怎么走通的**：herdr 的 socket API 里没有图片概念，能投的只有文本。但 claude 和 codex 都能直接读磁盘上的图片文件（实测两边都描述对了一张 320×200 左红右蓝的测试图，codex 还会打一行 `Viewed Image`）。所以「传图」＝ 存到跑 herdr 那台机器的 `~/.herdr-web/uploads/`，然后把**绝对路径**当文本给出去，agent 自己去读文件。

**传图不用开发件箱**：软键条里配一个 `act:img` 的键就行（设置 →「软键条」页，预设「网页端动作」里有现成的 🖼 传图，位置和标签随便改），而且**整页都能粘贴**（`⌘V` / 长按粘贴，剪贴板里是图就直接传）。路径去哪儿看发件箱开没开 —— 开着就接到草稿末尾（还能接着说话再投），没开就**直接敲进终端**，等于替你把路径打进当前 pane 的输入行。多数时候只是想把刚截的图丢给 agent，为这个专门开一次发件箱不值当。

入口放在软键条而不是顶栏：顶栏在平板上已经挤了八个按钮，而软键条本来就是「自己配一排常用动作」的地方 —— 要不要这个键、放第几个、叫什么，都归用户。

粘贴走的是挂在 window 上的**捕获阶段**监听：得抢在 xterm 那个隐藏 textarea 之前，否则剪贴板里只有图片时它会往终端里粘一段空文本。落在发件箱 textarea 里的粘贴放过去，交给它自己处理（那儿能插在光标处）。

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

地板是一次 sync 的耗时，因为 herdr 的每次调用都可能撞上一个 ~100ms 的 tick（原因见 [HERDR-API.md](HERDR-API.md) 的「100ms 的坎」）。想临时试手感：URL 上加 `?poll=200&push=400`，会覆盖服务端下发的默认值。

几个要知道的：

- **框里一有你自己写的内容，目标就被钉在当初瞄准的那个 pane 上**，框空了才重新跟随焦点。因为 herdr 会因为 agent 状态变化自己换焦点，不锁定的话「为 A 写的话投进了 B」。自动拉回来还没动过的内容不算草稿，那时候切 pane 照样跟着换。
- **「双向」只对 claude / codex 这种有真输入框的 pane 生效。** 普通 pane 里跑的可能是 vim 或某个选择器，那里的字符是**命令**不是文本。开着的时候也别同时在那个 pane 里手敲字——本地→远端这个方向本质上是在跟字节流抢缓冲区。
- **远端正开着选择框 / 确认框时会拒绝投递**（清不空就不投，否则就是「残留 + 新文本」一起回车）。去那个 pane 按 `Esc` 收掉再投。
- **agent pane 上认不出输入框时也不投**（屏幕上正开着分页器 / 编辑器 / 某个全屏控件）。这时候「拉回」也不会往框里塞东西 —— 认不出就是认不出，不会退回屏幕最后一行。shell pane 天生读不到输入行，那边不受影响，投稿照常。
- socket 在**跑 herdr server 的那台机器**上。现在只连本机（或 `HERDR_WEB_SOCKET` 指到的路径）。

## 面板一览（手机上换 pane 的那条路）

顶栏的 ▦（或者软键条上配一个 `act:panes`）打开一张 pane 列表：按 workspace 分组，一行一个
pane，**点一下就跳过去并铺满全屏**。列表上方能筛（tab 名 / 标题 / 路径 / pane id）、能只看
跑着 agent 的、能关掉「全屏」（那时候点一行是「切焦点 + 退出放大」）。

**为什么需要它。** 软键条发的是**按键**，而按键只能表达**相对**导航：下一个 tab、往右切一格。
「让 `w5:p3` 全屏」这句话用按键说不出来，只能拆成一串相对动作走过去 —— 而中间每一步的屏幕，
正好都是「未放大的多 pane 布局」那个在手机上根本读不了的状态。本机实测的规模是 48 个 pane /
38 个 tab / 4 个 workspace，一趟是 workspace → tab → pane → zoom 四段盲走。

herdr 的 socket 这层是**按 pane_id 寻址**的：`pane.zoom` 带上 `pane_id` 就能一次跨
workspace + tab + pane（焦点连着 workspace 和 tab 一起切过去，不用先 `workspace.focus`
再 `tab.focus`，实测确认）。所以界面上就是点一行。

**它是索引，不是第二个界面。** 点完之后看的还是同一个 herdr 终端 —— 网页接的是整个 TUI，
herdr 那边一切焦点，画面自己就跟过来了；键盘那套操作一个字都没变，Mac 上照旧敲键盘。这条是
刻意的：手机端「只接管一个 pane + 做一套图形界面管面板」的做法能做得更花，代价是**两套使用
习惯**和第二个真相源。所以这个面板只做「去哪儿」，不做增删改。

### 排序（可切换）

顶上那个按钮点一下换一种，记在本地：

| 排序 | 规则 |
|---|---|
| **优先级**（默认）| 按「多想让你看一眼」分档：**等你回答 > 跑完了 > 在跑 > 闲着 > 非 agent**，同一档里按最近动过排 |
| **分组** | 按 workspace 分组，组里是 tab / pane 的原顺序 —— 和你在 herdr 里看到的一样 |

状态点的颜色：**红 = 等你，绿 = 跑完了，黄 = 在跑**，闲着是灰点。「在跑」用黄不用绿，和 herdr
自己 agents 栏那个黄点一致；绿留给「跑完了」（通用约定，一眼知道是好事）。只有闲着不给颜色 ——
一列点全是彩的就没有重点了。

`在跑` 单独一档。一开始把它和 `闲着` 合成了一档（理由是两个都不需要你，谁在前面没有客观
答案，交给「刚动过」去定），实际用起来不对：黄点那个正在跑的会被十几个闲着的埋掉，而列表里
最想一眼看到的恰恰是它。`等你` / `完成` 两档在行上额外挂一个小标签，别的状态只有那个点的
颜色 —— 每行都塞标签就没有重点了。

**同一档里认 `state_change_seq`**（herdr 里的全局递增计数，每次 agent 状态变化推高一格），
不认时间。因为 herdr 的 API 里**一个时间戳都没有**：`agent.list` 只给这个计数，事件里也不带
时间。计数一直是对的，所以排序一直是对的。

### 「3 分钟前」那一列

时间是 **herdr-web 自己盯着状态变化打的**（`internal/agentwatch`：订一条
`pane.agent_status_changed`，收到就记 `time.Now()`）。所以：

- **第一次跑的时候这一列是空的**，随着一次次状态变化填回来。空着是实话 —— 那个变化发生在
  开始盯之前，没法知道是什么时候，编一个时间比空着糟得多。列表底下会说明这一句。
- **按 `terminal_id` 存盘**（`~/.herdr-web/agent-seen.json`），所以 herdr-web 自己重启
  （升级、改配置）不丢时间。不能按 `pane_id` 存：那是 herdr 里的位置编号，pane 一开一关就
  重新分配给别人了，会张冠李戴。herdr 重启之后终端 id 全是新的，旧记录自然对不上 —— 存盘时
  只写这会儿还在的终端，文件自己就不会长胖。
- **状态不存盘**，只存时间。存了状态的话，重启后拿旧状态一比就会把「停机期间变的」记成
  「刚刚变的」。
- 订阅没连上（herdr server 没在跑）时，列表底下会说一句，免得空着的时间列看着像坏了。
- 显示用 `3m` `2h` `4d` 这种紧凑写法（不到 45 秒算「刚刚」），完整时间在 title 里。手机上这一列
  只有几十像素，「3 分钟前」四个字放不下。

几个细节：

- **pane id 在手机上也照样显示**。一个 tab 里分了两个 pane 时，两行的 tab 标签和 cwd
  一模一样（实拍见过），id 是唯一分得开的东西。
- 第二行给的是 **agent 自己写的会话标题**（Claude Code 那个「图片识别」之类），没有才退回
  cwd —— shell pane 的标题只是 `user@host:path`，不如路径。
- **跳完不自动聚焦终端**（手机上那一下会把系统键盘顶出来，而刚跳过去多半是要看）；宽屏上顺手聚上。
- 投稿目标不用管：默认那条「跟随 herdr 当前 pane」自己就跟过去了。本地有草稿时目标仍然锁在
  原来那个 pane 上 —— 为 A 写的话不该因为你去 B 看了一眼就投给 B。
- 「全屏」开关记在本地（`localStorage`）。手机上默认开：多 pane 平铺读不了，去了不放大等于没去。
- `zoomed` 是**整个 tab** 的状态，不是某个 pane 的（herdr 放大的永远是当前焦点 pane）。
  所以「这个 tab 只有一个 pane」会回 `zoomed:false`，那不是失败，界面上单独说一句。

## 设置面板

顶栏最右的 ⚙ 是全部设置，分三页：**终端**（字号 / 明暗、kitty 协议 / Option 当 Meta / 选中即复制 / 同步输出，加一行后端环境）、**软键条**（下面那节）、**设备**（谁配过对、登出、踢人）。

「终端」页顶上那排**字号 / 明暗**和顶栏里那三个图标是同一套动作（不是另一份状态）：手机竖屏的顶栏放不下它们，那一档只能从这儿调。

以前这是三个各自为政的小面板，顶栏为此挂了三个图标 —— 平板上顶栏本来就挤，而且「设置」被切成三块，找起来全靠记哪个图标是哪个。**软键条右下角原来还有一个直通「软键条」页的 ⚙，去掉了**：它常驻在键那一排的右边跟键抢地方（竖屏尤其明显），而改按键是一次调完的事，绕一下顶栏不算负担。

## 软键条

手机没有 Ctrl 键，herdr 的 `ctrl+b` 前缀全靠这条。按键**存在服务端**（`~/.herdr-web/softkeys.json`），手机 / 平板 / 电脑共用一份，在设置 →「软键条」页改。

**一行还是两行是个设置**（存服务端，跟着配置走），不是靠「第二行空不空」猜 —— 空的第二行和「我只要一行」是两件事。两行**各自横向滚动**：手机上一行放得下四五个键，把常用的放第一行、次常用的放第二行，比十几个键排成一条长龙好找 —— 手指知道自己在哪一行，滑一行也不会把另一行带跑。切回一行时第二行的键会**并到第一行末尾**（服务端也是这么算的；「存着但不显示」是最烦人的一种状态）。

编辑器分两层，落盘也是两层（`{rows, keys, bar}`）：

- **`keys` = 「我的按键」**：所有按键的**定义**，每个带一个 `id`。新增、改名字 / 按键谱 / 宽 / 两下、彻底删掉都在这儿。
- **`bar` = 软键条**：每行一串 **id**，指向「我的按键」里的定义。

条上存 id 而不是整份定义，为的是「条上的键是从我的按键里**选**出来的」，不是搬过去的：

- 同一个键**能在两行各放一个**（Esc 第一行来一个、第二行也来一个），拖上去库里那个不动；
- 改一处定义，条上所有引用一起变；
- ✕ 只是去掉一个引用，定义还在库里，随时再拖上去。删定义会把条上的引用一起清掉（顺手 toast 说清了几处 —— 不然就是「保存完少了个键」）。

**内置预设不单独占一片**：六十多个键铺出去比整页都长，而且看着像能编辑其实不能。现在只有一个「载入预设」按钮，一下把预设全灌进「我的按键」（按「名字 + 干什么」去重），之后每个都能自己改。所以「我的按键」的上限（120）比条上能放的多得多 —— 定义只占一行 JSON，不占屏幕。

老配置（「排第几行」长在按键上的 `row` / `off`）第一次读到时自动迁成这两层，已经调好的软键条不会因为升级白丢。

拖动是**按住 250ms 才算拿起**（触屏；鼠标走 6px 就算拖）。这一页要能上下滚，而键本身就是拖动的把手 —— 手指落在键上往下划，是滚页面还是拖这个键，只能靠「有没有按住」区分。给键写死 `touch-action: none` 页面就滚不动了（键铺满整页），`pan-y` 又会把「往下拖到第二行」吃成滚动。拿起来之后在 `touchmove` 上 `preventDefault` 挡住滚动 —— 手指在按住期间没动过，浏览器还没开始滚，这时候拦得住。

- 「按键」一栏写**按键谱**，空格分隔可以连发多下 —— `ctrl+b c` 就是 herdr 的前缀加 c，一下点出来。
- 支持 `ctrl+x` `alt+x` `shift+tab`、具名键（`esc tab enter space bs del ins up down left right home end pgup pgdn f1-f12`）、原样文本。
- 原样文本两种写法等价：`"herdr" enter` 和 `text:/new enter`（`text:` 是给平板手输准备的 —— 编辑器里本来就有 `sticky:` / `act:` 前缀，找引号反而麻烦；带空格的仍要引号：`text:"git status"`）。
- 预设分 8 组（前缀 / 标签 / Pane / 工作区 / 终端按键 / 文本 / Claude 命令 / 网页端动作），「载入预设」一下全进「我的按键」。herdr 那几组抄的是 `herdr --default-config` 的 `[keys]` 默认值，改过 keybinding 的人自己改；「Claude 命令」是 `/new` `/clear` `/compact` `/usage` `/context` `/model` `/resume` `/cost`，都带回车，一下点完。
- 每个键有个**「两下」**勾选框：勾上的键要点两次才真发出去 —— 第一下只是举起来（键变红，文字不变，免得按键变宽把手指底下的键挪走），3 秒不点、或者点了别的键就放下。软键条上键挨得近，关 pane / 关标签这种误触没法撤销。预设里 `关 pane` `关标签` `关工作区` `断开` `/clear` 默认就带。
- `Ctrl` / `Alt` 是**粘滞**的：点一下亮起，再敲一个字母就发出对应组合键，然后自动灭掉。手机虚拟键盘的 keydown 不可靠，所以这层是在数据流上做的，不依赖按键事件。写法是 `sticky:ctrl` / `sticky:alt`。
- `act:` 是**网页端自己处理**的动作，不发任何字节：`act:kbd` 呼出 / 收起系统键盘，`act:img` 传图（弹相机 / 相册，路径按「发件箱开没开」决定去草稿还是直接敲进终端），`act:panes` 开「面板一览」（上面那节），`act:clip` 把机器上的剪贴板取到手机剪贴板，`act:paste` 把手机剪贴板粘进终端（后两个见「[手机上怎么复制 / 粘贴](#手机上怎么复制--粘贴)」——**手机上这两条只能是点出来的**，浏览器不给定时器碰剪贴板）。服务端只认白名单里这几个，写错了保存时就报错，不会下发一个点了没反应的键。
  `act:panes` 放在软键条上是有讲究的：手机上键盘一弹起来顶栏整段就收掉了，那时候顶栏那个入口点不到，而软键条正好在拇指底下。
- 按键谱在**服务端**解析成字节再下发，前端只管照发；写错了保存时会告诉你是第几个按键、哪里不认。回包里 `send` 是**解析好的字节**、`spec` 是你写的谱 —— 编辑器回传时两个字段都在，服务端**认 `spec`**。拿 `send` 当谱重解一次的话，Tab 的 `"\t"` 去掉空白就是空串，报「按键谱是空的」而用户什么都没改（踩过）。

## 手机

xterm.js 的触屏支持基本只有「点一下聚焦隐藏 textarea」，剩下全靠自己补。有程序在收鼠标上报时（herdr 这种），触屏手势整个由本项目接管：

| 手势 | 行为 |
|---|---|
| 单指纵向滑动 | 按行高换算成 SGR 滚轮上报 `CSI < 64/65 ; col ; row M` 发给程序；没开鼠标上报时滚本地 scrollback |
| 单击 | 有鼠标上报时发 `CSI < 0 ; col ; row M/m` 给程序（点 pane、点 tab 都好使）且**不弹系统键盘**；没有鼠标上报时聚焦隐藏 textarea（点一下就是想打字）|
| 长按（≈380ms）| **抓住**：按下左键不松手 + `CSI < 32` 上报移动，之后滑动就是拖 —— herdr 的「拖 pane 边框改大小」在手机上靠这条。松手补 `m` 发松开 |
| 双击 | 显示 / 收起系统键盘 |

抓取**只认长按**。曾经试过「手指落在框线附近就立刻抓」，翻车了：agent 自己画的框（Claude Code 每个 pane 一个圆角框）竖边同样贯穿整屏，从字符层面和 herdr 的 pane 边框分不出来，于是在框边上一划就变成往 agent 里拖鼠标 —— 手指想滚屏，屏幕上却在选文字。**滑动永远是滑动**，换挡要先按住。

按住之后按**像素吸附**到附近的贯穿线（`SNAP_PX = 24`，约一根手指的落点误差），不是按格数。这条也是踩出来的：原来只允许差一格，而平板上 211 列宽的屏幕一格才 ~6px，手指偏十几个 px 就把 press 落进 pane 里，agent 收到一次拖动、屏幕上什么也没发生 —— 表现就是「手机上根本拖不动 pane」。

只认**贯穿的长线**（框线字符占了这一列 / 这一行 70% 以上、至少 6 格），agent 画的短横线（消息分隔、「2 new messages」那种）都挡在外面。挡不住的是 agent 的外框竖边，但既然只是「按下点挪最多 24px」，猜错了也就是这一次拖动落在框边上。代价：2×2 布局里那条横向分界只占半屏宽，吸不到，得按准一点。

长按期间允许手指飘 16px（`HOLD_SLOP`）。原来是 8px，太苛刻 —— 按住不动的手指本来就会飘十几个 px，一飘长按就被撤销，表现是「长按没反应」。没开鼠标上报的普通 shell 下不抓取（那儿拖一下没有意义），长按仍然是「什么都不做」。

端到端验过：在一个独立的 herdr session 里竖分屏，长按分界线**右边 3 格**再拖，分界从第 45/46 列移到 40/41 列；正好在贯穿竖线上竖划，发出去的仍然只有滚轮上报。

### 手机上怎么复制 / 粘贴

先说结论：手机上要**两个软键**（设置 →「软键条」→ 载入预设，「网页端动作」组里的
**📋 取** 和 **📥 粘**，拖到条上）。原因是下面两条，一条比一条反直觉。

**第一条：herdr 复制到的是「跑 herdr 那台机器」的剪贴板，不是手机的。**

手机上长按拖选（那一下会被翻成鼠标拖动发给 herdr，配上 herdr 自己的 `copy_on_select` 就是
一次复制），herdr 弹「copied 84 chars to clipboard」——**那 84 个字进的是 Mac 的剪贴板**
（`pbpaste` 读出来一字不差），浏览器一无所知，手机上哪儿都粘不出来。看着像「复制失败」，
其实是复制成功了、只是落在另一台设备上。

所以有了 **📋 取**（`act:clip`）：点一下，服务端读机器上的剪贴板（`pbpaste` /
`wl-paste` / `xclip`，见 `internal/clip`）交给网页，网页写进手机剪贴板。之后在手机上随便哪儿
长按粘贴都是它。

反方向是 **📥 粘**（`act:paste`）：点一下读手机剪贴板，按**括号粘贴**送进终端（多行不会被
当成一行一次回车）。触屏上没法 `⌘V`，也没法长按呼出终端的粘贴菜单（单指手势被接管了），
这个键是唯一的入口。

**为什么不能做成一个「自动同步」**：浏览器只在**用户手势**里给读写剪贴板，定时器里偷偷做
一律被拒 —— 而且是静默的。所以两个方向各要一次点击，这一下是浏览器的硬要求，不是没做。

触屏上**选不了字**（单指手势整个被上面那套接管了），所以另一条复制路径是 **herdr 自己的
COPY 模式**：`ctrl+b` 前缀进去，`hjkl` 选、`y` 复制。它走 **OSC 52** —— 终端里的程序把文本推给
网页，网页再写系统剪贴板。

**但浏览器不一定让写，而且以前是静默失败的。** 两条限制叠在一起：`navigator.clipboard` 只在
安全上下文里存在（局域网 http 上压根没这个对象），而且手机浏览器要求这一次写发生在**用户
手势**里。COPY 模式和「选中即复制」的触发点都不是点击，于是在手机上被拒 —— 屏幕上选区好好的、
一句提示都没有，剪贴板里还是上一次的东西。

现在写不进去就在底部出一条**「点一下复制」**：那一下点击本身就是手势，按一下就进剪贴板。
连 `execCommand` 也被拒的话，它把文本摊在一个**已经全选好**的框里，长按 → 「拷贝」。

还有一个**桌面上也会踩**的坑：**标签页不可见时 Chrome 让 `writeText` 那个 promise 永远挂着**
（实测 26 秒既不 resolve 也不 reject，剪贴板也确实没变）。光 `await` 的话「写不进去」永远发现
不了，所以那一步有 1.2 秒上限，超时就当失败、往后面两条路走。

两条相关的：

- **「选中即复制」是鼠标那一档的设置**，触屏上没有选区，手机上开它不起作用。
- 想在电脑上看那条提示长什么样：URL 上加 **`?nocopy=1`**，两条写剪贴板的路都当成失败
  （和 `?poll=` / `?push=` 一样是调试参数）。

为什么要这么绕：xterm.js 只把 `wheel` 翻译成鼠标上报、完全不管 touch，所以 herdr 这种「占着备用屏幕（本地没 scrollback 可滚）+ 开了鼠标上报」的程序在手机上两头都不响应，彻底滚不动。而点击和长按又都会落到隐藏 textarea 上，浏览器顺手就把键盘顶出来 —— 在 TUI 里十次里有九次只是想点个 pane，不是想打字。

做法是在 `touchstart` 上**无条件** `preventDefault`（单指手势），一次性掐掉聚焦、长按气泡、双击缩放和浏览器补发的兼容鼠标事件，然后自己在 `touchend` 里按位移和时长把手势分成上面几类。

**为什么连「没有鼠标上报」的时候也要吃掉**：不吃的话浏览器会补发兼容鼠标事件，xterm 当成「按下 + 拖选」——手指一滑变成选中文字、终端一动不动（还没 attach herdr、或者 pane 里跑着不收鼠标的程序时必现）。触屏上想滚屏远比想选字常见，所以这里选滚屏。代价：触屏不再能拖选（要复制用桌面鼠标，或者 herdr 自己的 COPY 模式），点一下也不再由浏览器顺手聚焦 textarea，改成自己在 `touchend` 里 focus。

**手势时长用事件自带的时间戳**（`e.timeStamp`），不是处理函数里的 `Date.now()`。终端忙着重绘时定时器和事件派发都会被推后，实测一次 60ms 的点击在处理函数里量出来是 994ms，于是被「超过 500ms 算长按，什么都不做」挡掉 —— 表现就是「输出一多点哪儿都没反应」。事件时间戳是事件产生时打的，不受处理延迟影响。

**换 pane 不靠盲敲**：顶栏 ▦ 或软键条上的 `act:panes` 开「面板一览」，点一行直接跳过去并铺满
（见上面那节）。按键那条通道只能表达「下一个 tab」这种相对导航，而中间每一步的屏幕正好都是
手机上读不了的那个平铺状态。

连上时也不自动抢焦点（触屏设备），否则一连上键盘就顶出来。要打字：双击终端，或点软键条最左边的 ⌨。键盘状态跟着 textarea 的 focus/blur 走，所以用户自己收起键盘时按钮高亮也会跟着灭。

**顶栏在手机上收成一行**：状态只留那个彩点（完整文字进 `title`）、连上之后不显示「连接」、字号 `A−/A+` 和明暗 `◐` 挪进设置 →「终端」页。七个图标在 393px 上排不下，折成两行就白吃掉 ~36px（约三行终端），而这三个都是一次调完的东西。

**键盘一弹起来，顶栏整条收掉**，只留 8px 的一条缝（点一下放回来）。那一刻可见高度只剩 ~430px，而顶栏里的东西那时候一个都用不上 —— 正在打字的人要的是软键条和发件箱，「连接」是连之前的事。收起是临时的：手动点开只管这一次，键盘一收就自动恢复常态，不留状态（否则下次打字时顶栏在不在全靠碰）。

判断键盘弹没弹用的是 **visualViewport 被压掉多少**（`hooks/useKeyboardUp.ts`，阈值 0.8），不是「xterm 那个隐藏 textarea 聚焦了没有」：这个项目最常见的姿势是**在发件箱里口述**，那时候焦点在发件箱上，终端那边一无所知，而键盘照样占掉半个屏幕。两个信号一起用（对着终端打字时前者不动、后者认得出来）。认不出来也不影响正确性 —— 那儿就退化成「顶栏不收」。

**虚拟键盘不会再盖住内容**：页面高度跟 `visualViewport` 走，并给 viewport meta 加了 `interactive-widget=resizes-content`。光重排终端不够 —— iOS 的键盘**从不**缩布局视口，`height:100%` 指的是没缩过的那个，键盘会直接盖住软键条和发件箱。

**但这套不是每个浏览器都认**（实测某些国产浏览器里页面高度纹丝不动，发件箱照样被埋一半），所以底下那块能用手拖，不依赖浏览器行为。

### 底部面板

发件箱和软键条是**同一块面板**（`web/src/components/Dock.tsx`）：一套边框、一个宽度，一次调完。

以前它们各管各的 —— 发件箱能抓着 ⠿ 从底部「撕」下来变成浮动面板（自己一套位置 / 大小 / 边框），软键条自己一套左右留白和高度。两块叠在一起就是两条边框、两种宽度、两套把手，看着像错位的两层，而「把底下这一坨从输入法上挪开」得分别调两次。现在整块一起缩：

- **左 / 右两条侧边**左右拖，改那一边的边界（横向改宽度）。输入法连着它那圈工具条常常压住半边屏幕，把整块面板缩到剩下的空地上，比整条通屏挤在键盘底下有用。
- **键那一区上边缘的三个把手是两轴的**：上下拖 = 软键条多高（**最多半屏**，放不下的部分上下滚），左右拖 = 左边那个动左边界、右边那个动右边界、中间那个整条平移（宽度不变）。每个方向都有 3px 死区，只想横着拖不会顺手把高度锁成定高。
- 软键条按键**换行**排（不再是一条横向滚动的长龙）。没拖过高度是自动的、封顶两排（不留空白）；拖过之后是定高 —— 用户明确要求「更高」就别自作聪明缩回内容高度。任意把手**双击复位**（宽度和高度一起）。
- 面板里的东西按**面板自己的宽度**折行（`@container` + `@max-3xl:`），不是按视口宽度：缩到半屏之后视口还是那么宽，按视口算的话发件箱那排控件会挤成一团。
- 左右留白换了个存储键（`dockInset`，旧的 `softkeysInset` 不再读）：以前那份只缩键那一条，现在缩的是整块，语义不一样 —— 直接套过来的话升级后一开页面会发现发件箱莫名其妙只剩半屏宽。

**手机竖屏（< 440px）是另一档**：一整套把手都不出，面板通屏，软键条**一行横滑**、键小一号（字号 13→11.5px，高 35→28px）。

- 那么窄的屏上把手是负收益：三条把手加起来 24px 高（约两行终端），而左右本来就没有空地可让 —— 那一档的输入法是整条压在底下的，不是压半边。
- 键**换行**更亏：每多一排就少一排终端。横滑只花一次划的功夫，而且常用的那几个键本来就在最前面（顺序你自己排）。要两行就在设置里明确开两行（见「软键条」那节），每行各自横滑。
- 断点写在两处，改一个就得改另一个：`index.css` 的 `--breakpoint-phone`（给 Tailwind 的 `max-phone:` 变体）和 `hooks/usePhone.ts` 的 `PHONE_MAX`（给行内 style 和「要不要渲染把手」——这两样 CSS 盖不掉）。
- 宽度一过线（转横屏、平板、桌面）把手和存着的那份尺寸自己就回来了，两档互不影响。

**浮动发件箱去掉了。** 想把它挪出输入法的话现在只有「整块横向缩」这一条路（这也是实际在用的那条：那台平板的输入法压的是半边屏幕）。真需要竖着挪，再把 `useFloatBox` 那套加回来 —— 但别再让它自己长一套边框。

**把手不能贴着屏幕边**（`EDGE_SAFE = 14`）。安卓手势导航把屏幕左右各一条划给了侧滑返回 / 前进，那一条系统先吃，网页连 `touchstart` 都收不到 —— 贴边的把手就是拖不动（实测）。所以把手离屏幕边不足这么多时往里让，面板里的东西跟着让出同样的内边距，免得控件钻到把手底下；面板自己已经缩进来了就不用让。数值是真机上试的：按系统标称的 24dp 让，让出来那条太宽、看着像错位，而实际有效的侧滑判定比标称窄。哪天侧边把手又拖不动了，先怀疑这个数。

**横屏一套、竖屏一套**（`web/src/lib/oriented.ts`）。同一块面板在两种朝向下想要的尺寸位置根本不是一回事，共用一份的话每次转屏都得重摆，还会把另一份覆盖掉。所以底部面板的高度和左右留白按朝向分开存，转屏就换成那一份再收边（**读**那一份，而不是把手上这份挪一挪）。朝向按宽高比判断，不用 `screen.orientation` —— 桌面窗口、平板分屏下宽高比才是真正决定布局的东西。老版本存的那份（不带朝向后缀）会在第一次读到时迁到当前朝向名下，已经调好的设置不会因为升级丢掉。

横屏比竖屏好用很多（列数够）；字号用顶栏 `A− / A+` 调；顶栏还有个**全屏**按钮，地址栏和工具条一去终端能多好几行。iOS Safari 不给网页全屏（只有视频能），那边点了会提示改用「添加到主屏幕」，从主屏打开一样没有地址栏。

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

那几个开关在顶栏最右的 ⚙ →「终端」页里。**「程序请求的终端能力」那张列表去掉了**：它是当初补协议时的调试视图，日常没人看（能力本身还照样记着，`DEC 2031` 的主题通知要用）。

## 键盘

herdr 的快捷键基本都是 `ctrl+b` 前缀加一个普通键，legacy 编码就能表达，所以不依赖 kitty 协议。kitty 协议补的是 legacy 表达不了的组合，默认开着（设置 →「终端」里可关）：`Ctrl+Shift+字母` → `CSI 编码;6u`、`Ctrl+数字` → `CSI 编码;5u`、`Ctrl+Enter` / `Shift+Enter` / `Ctrl+Tab`。

**每个 herdr session 有自己的 socket**：默认 session 是 `~/.config/herdr/herdr.sock`，`herdr --session x` 是 `~/.config/herdr/sessions/x/herdr.sock`。发件箱连的是 `HERDR_WEB_SOCKET`「那一个」，所以要对着非默认 session 用发件箱，得把这个变量指过去。

**`Esc` 也在里面，而且是最要紧的一个**：程序声明 kitty 的 disambiguate flag（`CSI > 1 u`，herdr 和 Claude Code 都会）之后，Esc 必须编成 `CSI 27 u`。bare `0x1b` 是**所有**转义序列的前缀，程序收到它没法立刻判断这是一次真实的 Esc 还是一段序列的开头，只能等超时或者丢掉 —— 表现就是「网页上按 Esc 没反应」，`/usage` 之类的浮层退不出来。软键条上的 `Esc` 和发件箱里转发的 Esc 走同一套编码（服务端解析出来的字节不知道 kitty 开没开，所以孤立的 ESC 到前端会按当前模式重编）。

抢不回来的键（浏览器自己吃掉）：macOS 上是 `⌘W` `⌘T` `⌘N` `Ctrl+Tab`；Windows/Linux 上还多 `Ctrl+W` `Ctrl+T` `Ctrl+N` `Ctrl+Shift+I/J/C`。真要用这些，把页面装成 PWA 能拿回一部分。

复制 `⌘C`（或 `Ctrl+Shift+C`）· 粘贴 `⌘V` · 清屏 `⌘K` · `Option` 默认当 Meta。

## 代码结构

```
cmd/herdr-web/        main：flag、子命令、监听、启动横幅、网卡打分
internal/
  config/             环境变量（viper，只认 env）、路径、部署形态（TLS 档位 / 暴露声明 / 白名单）
  auth/               配对码 + 设备凭据（只存哈希）+ 限速封锁（gate.go）
  acme/               DNS-01 自动签发和续期（只 import 用到的 provider，见包注释）
  tlsgen/             本地 CA + 短期叶子证书 / 指定的真证书，都带热重载
  ctl/                ~/.herdr-web/ctl.sock：子命令和跑着的服务之间的通道
  herdr/              herdr socket 客户端（一次调用一条连接）
  composer/           按 agent 分派抽输入框 + testdata 里的真机抓屏
  outbox/             列目标 / 拉回 / 清空 / 投稿 / 推草稿
  softkeys/           软键条配置 + 按键谱解析（data.go 是从旧 JS 版生成的，不是手抄的；
                      testdata/js-snapshot.json 存着当时的快照，测试比对前 6 组）
  uploads/            图片落盘（按魔数认类型）
  clip/               读这台机器的剪贴板（pbpaste / wl-paste / xclip）—— herdr 的复制
                      落在**跑 herdr 那台机器**上，手机要拿到只能由这一侧读出来
  server/             HTTP 路由 + PTY/WebSocket + 静态资源
                      guard.go 是门卫（Host 白名单 / Origin / 安全响应头）
                      authapi.go 是配对和设备管理的口
                      session.go 是「一个 URL 一个 herdr session」的分派（每个 session
                      一个 socket、一份发件箱、一条状态订阅）
  webui/              embed 前端产物（dist 由 make build 拷进来）
  qr/                 启动时在终端画二维码
  version/            版本号的唯一出处（goreleaser 用 ldflags 注进来）
  selfupdate/         查 GitHub Releases + 缓存 + 下载校验 + 原地换二进制
  service/            装成 launchd / systemd 常驻服务（plist / unit 生成 + 环境快照）
assets/               图标（herdr 的羊关在浏览器窗口里）。**别手改 svg**，
                      改 assets/make-logo.py 再跑一遍 —— 羊的剪影是从 herdr 复用的
                      一条 1800+ 字符描图路径，而同一份图形要出圆角版 / 方角版 / 三种 png
web/                  Vite + React + TS + Tailwind v4 + shadcn 风格组件
  public/             图标和 manifest（Vite 原样拷进 dist，走 / 根路径）
  src/term/           xterm.js 胶水：补协议、触屏手势、重绘看门狗（命令式，不套 React）
  src/hooks/          useCompose（发件箱状态机）、useViewportHeight
  src/components/     Dock.tsx 是底部面板的外壳（发件箱 + 软键条共用的边框 / 宽度 / 高度）
                      Pairing.tsx 是配对页（没配对时只渲染它）
                      SettingsPanel.tsx 是设置面板，软键条编辑器和设备管理是它的两页
                      QrScan.tsx 是配对页里的扫码器（BarcodeDetector + 后摄）
reference/            最早的 Python 原型，那三份文档里的「已验证」都是拿它验的
npm/herdr-web/        npm 根包 @bysir/herdr-web：一个 JS 壳，按平台找二进制
scripts/npm-*.mjs     把 goreleaser 产物摊成 npm 包 / 按顺序发布
install.sh            没有 node 时的装法（下载 + 强制校验 sha256）
.goreleaser.yaml      交叉编译 + archive + checksums（只出 darwin / linux）
.github/workflows/    ci.yml 每次推都跑；release.yml 打 tag 就发 GitHub + npm
```

命令行是 [cobra](https://github.com/spf13/cobra)（`cmd/herdr-web/main.go`）：根命令起服务，`pair` / `devices` / `revoke` / `unlock` / `version` / `update` / `service` 是子命令，`--help` 和补全脚本白送。**标志只有一个** `-w, --web`（开发时指前端目录），别的配置一律环境变量 —— 同一个设置两个入口就得规定谁盖谁，不值当。

`make test` 跑 Go 测试 + 前端 typecheck。`make dev` 前端热更新（后端另开一个 `go run ./cmd/herdr-web`，vite 把 `/api` 和 `/pty` 转过去）。

### 发版

```bash
make release-dry        # 本地把整条链跑一遍：交叉编译 → archive → npm 包 → npm publish --dry-run
make release V=v0.1.0   # 打 tag 并推上去，剩下的 GitHub Actions 干
```

推上 tag 之后 `release.yml` 会：`make test` → goreleaser（交叉编译 4 个平台、出 archive 和 `checksums.txt`、建 GitHub Release）→ 把 archive 摊成 npm 包 → **先发 4 个平台子包、最后发根包**。顺序反了会有一段时间 `npm install` 装出一个没有二进制的壳。

**Release 建好了但 npm 那步挂了**（发过一次，见下）用同一个 workflow 补发，不重新编译：

```bash
gh workflow run release.yml -f tag=v0.1.0
```

它会去下已经发出去的那批 archive 再打包，所以补发的二进制和 Release 里的**逐字节相同**。

**发布 workflow 只能有一个，别再拆出去。** npm 的 Trusted Publisher（OIDC）一个包只能绑一个
workflow 文件名，绑的就是 `release.yml`；再开一个会发包的 workflow，从它发就对不上 OIDC。

需要一个仓库 secret：`NPM_TOKEN`（**Automation** 类型 —— 另外两档在开了 2FA 的账号上发包会要交互式
验证码，CI 里没人输）。配了 Trusted Publisher 之后可以去掉它，但**先发一版确认 OIDC 真的生效**再删。

Trusted Publisher 是**按包**配的，5 个包（根包 + 4 个平台子包）每个都要配一遍，都填 `release.yml`、
Environment name **留空**（我们的 workflow 没声明 environment，填了任何值 OIDC 都会对不上）。
少配一个的表现是下次发版在「发 npm」那步中途失败。

**tag 要推到装着 `release.yml` 的那个远端**，也就是 GitHub。这个仓库有两个远端（`origin`
是自建 git，`github` 才是 GitHub），所以 `make release` **不写死 origin** —— 它按 push URL 里的
`github.com` 认，认不出来就拒绝发版。推错远端是最难查的一种：tag 打上去了、命令也成功了，
Actions 那边一直没动静，而「没动静」和「还在排队」长得一模一样。要覆盖：
`make release V=vX.Y.Z RELEASE_REMOTE=xxx`。

三处名字必须对得上，改一个就要改另外两个：`.goreleaser.yaml` 的 `name_template`、`internal/selfupdate.AssetName`（自更新下载）、`scripts/npm-build.mjs`。对不上的表现是 `herdr-web update` 下载 404。

`make release-dry` 跑完会**把工作区还回去**：`npm-build.mjs` 把版本号写进入库的
`npm/herdr-web/package.json`（干跑时是 `0.1.1-next` 这种快照号）。不还的话紧接着
`make release` 会说「工作区不干净」而你什么都没改，或者那个 `-next` 版本号被顺手提交进去。

发版路上踩过、已经修掉的三个（都是**静默**失败）：

- `web/tsconfig.tsbuildinfo` 曾经入库。它是 `tsc -b` 的增量缓存，`make test` 每跑一次就改写它，
  紧接着 goreleaser 判定 `git is in a dirty state` 直接拒绝发版。构建缓存一律不入库。
- `make web` 里那句 `rm -rf $(WEBDIST)` 会删掉入库的 `internal/webui/dist/.gitkeep`。那个文件是
  承重的：空目录上 `go:embed all:dist` 报 `cannot embed directory dist: contains no embeddable
  files`，新 clone 连 `go build` 都过不了。所以 `web` 和 `clean` 两个目标都会把它写回来。
- 首发之后有几分钟，npm 的 packument 读路径还没物化（`version` 端点和 search 都查得到，packument
  却 404）。这时候 `npm i` 拿到 404 会**静默跳过** optional 依赖，装出一个没有二进制的壳。
  等几分钟重装就好，壳里那段报错会提示重装。

**为什么终端那层不是 React 组件**：它要直接摸 xterm 的 parser、逐字节收 WebSocket、按 rAF 补重绘 —— 套上 React 的渲染周期只会碍事。React 那边只拿一个 ref 挂载它，再订阅几个状态回调。

### 配色（改界面之前先看这段）

token 全定在 `web/src/index.css` 的 `@theme` 里（暗亮各一份），组件里**不写具体颜色**，只用这些名字：

- 灰阶四档：`bg`（画布 / 终端）→ `bar`（顶栏、底部面板、浮层）→ `ctl`（控件）→ `ctl-hi`（控件 hover）；
  分隔线 `line` / `line-hi`；文字 `fg` / `muted` / `faint`。全是 S=0 的**纯灰** —— 原来那套偏蓝的板岩灰
  和终端里的彩色输出叠在一起会显脏。
- 绿只当强调色：`brand` 给文字 / 图标 / 描边，`brand-bg` + `brand-line` + `brand-fg` 是主按钮那一套填充。
  **打开 / 选中态是「淡绿底 + 绿边 + 绿字」，不是整块涂满** —— 顶栏上五六个图标可能同时是打开的，
  涂满的话整条栏全是色块，什么都不突出。饱和填充只留给一屏一个的主操作（投稿 / 保存 / 配对）和粘滞
  修饰键那种「按下去了必须一眼看见」的状态。
- 圆角两档：控件 `rounded-md`（6px）、浮层 `rounded-card`（12px）。字号：正文 13px，次要一律 `text-xs`，
  别再写 `text-[11.5px]` 这种一次性数值。
- 终端只有**灰阶和光标**跟着 token 走（`src/term/themes.ts`）：底色 = `bg`、光标 = 品牌绿、选区是半透明的绿。
  红黄蓝品青那六个色相一个都没动 —— 那是别人程序的输出颜色，diff 的红绿、agent 的高亮全靠它们。
- `accent` 是旧名字（原来那个亮蓝），现在留成 `brand` 的别名防止漏改，新代码别用它。

## 几个坑（已经处理了，记下来免得回头再踩）

- **WebSocket 不能并发写，写崩了是整个进程一起死。** gorilla/websocket 撞上并发写会
  `panic: concurrent write to websocket connection`，而这个 panic 发生在 handler 自己起的
  goroutine 里 —— net/http 只兜得住 handler 本身那一层，所以**进程直接退出，所有人的终端
  一起断**。一条 PTY 连接上有三个写者：PTY 数据、25 秒一次的 ping、退出时的 exit + close。
  线上炸过一次，是 ping 正好撞上一批二进制帧（和「开了几个浏览器」无关，每条连接各有自己的
  conn；但连接越多、重连越频繁越容易撞）。现在全部收口到 `wsWriter`，`ws_test.go` 里那个
  并发测试去掉锁就会复现同一条 panic。顺带两件：写入加了 10 秒超时（手机断网时 TCP 缓冲
  填满会让 `WriteMessage` 一直阻塞、把锁也占着，那样 PTY 读循环都推不动了），ping 的
  goroutine 改成 select 到 done 上（`Ticker.Stop()` 不关 channel，光 Stop 那个 goroutine
  会永远卡在接收上，连着 conn 一起泄漏 —— 手机频繁重连时一条一个地攒）。

- **`HERDR_*` 会让 herdr 拒绝启动**。如果本服务是在 herdr 的 pane 里起的，子进程继承到就会报 `nested herdr is disabled by default`。`internal/server/pty.go` 的 `dropEnv` 把 `HERDR_* / TMUX / ZELLIJ / ITERM_* / CLAUDECODE` 这些痕迹都清了。
- **xterm.js 6.0 会「收下重绘请求但不画」**：DEC 2026 同步输出开着时把范围攒起来等 ESU；绘制在 rAF 里，后台标签页完全不跑。herdr 常驻开着 2026、一帧几 KB 还会被拆成多次 write，攒漏一次屏幕上就留一块空白。缓冲区没坏，所以只补重绘：数据流停下来 180ms 后强制画一次，2026 卡着就自己补个 ESU。频繁出现可以在设置 →「终端」里关掉同步输出。
- **改尺寸会闪一下全黑，要拿「冻帧」盖住**。呼输入法（`visualViewport` 一变就重排）时最明显。原因是叠起来的：xterm 的 WebGL 渲染器一改 `canvas.width` 绘制缓冲就清空、`FitAddon.fit()` 在 resize 前还主动 `renderService.clear()` 一次，而重画最快也要等下一个 rAF（2026 同步输出开着时得等 ESU）；herdr 收到 SIGWINCH 之后自己又清屏重绘一遍，加起来几十毫秒。xterm 没有同步重绘的口子，所以延迟一个都去不掉 —— 改尺寸之前把 `.xterm-screen` 里那几层 canvas 合成一张图铺在终端上，等新画面画上（`onRender`）再多留 120ms 淡出。两个前提：WebGL 要开 `preserveDrawingBuffer`（合成完不丢缓冲，否则 `drawImage` 拿到的是空图），以及**快照读不出东西时要放弃冻帧**（后台标签页 rAF 不跑、画布压根没画过，糊一张空图上去比闪一下更糟）。另外行列数没变就不碰 xterm：键盘动画期间 `visualViewport` 会连着报好几次，白 resize 一次就白闪一次。
- **herdr 的主题不跟浏览器切换**：`~/.config/herdr/config.toml` 里 `[theme] auto_switch = false`。改成 `true` 之后，网页上切明暗就能直接切 herdr 的配色。
- **别把 `HERDR_WEB_SETTLE_MS` 调成 0**：详见「配置」那节。
- **重连必须先把终端复位**。一条 WebSocket 对应一个 PTY，断开时服务端就把 PTY 杀了，所以每次「连接」都是一个**全新的登录 shell**；但 xterm 实例是复用的，上一次 herdr 打开的私有模式还留在里面。表现是重连之后屏幕不但没好，还往命令行里灌乱码：鼠标移动上报（1003+1006）还开着，指针 / 手写笔一动就发 `ESC [ < 35;120;36 M`，zsh 的 ZLE 把认不出的 `ESC [ <` 前缀吃掉、余下的自插进命令行，于是屏幕上是 `35;120;36M35;115;37M…`（实测复现过：`➜  ~ 35;16;5M35;26;8M`）。kitty 键盘协议的 flags 同理留着，Esc 会被编成 `CSI 27 u`，新 shell 里显示 `[27u`。`connect()` 现在先 `term.reset()` 再连，顺手清掉我们自己攒的 kitty flags / 能力清单 / 粘滞修饰键。
- **「连接」按钮随时能按，所以连之前要自己收掉旧连接**。不收：服务端会再起一个登录 shell，两个 shell 的输出往同一个 xterm 里灌，屏幕当场花掉，而且旧 PTY 只要连接还在就一直活着。旧连接的回调也要一起摘掉 —— close 是异步的，旧连接的 `onclose` 会把新连接的状态改成「已断开」。

## 配置

**配置只有一个来源：环境变量。** 没有配置文件，命令行也只有一个 `--web`（开发时指前端目录）。用 [viper](https://github.com/spf13/viper) 收口在 `internal/config/`（`SetEnvPrefix("HERDR_WEB")` + `AutomaticEnv()`），配置项和变量名一一对应。

不读配置文件是故意的：这个口后面是一个登录 shell，「现在生效的到底是哪份配置」必须一眼看得见 —— 环境变量在 `ps` / systemd unit / launchd plist 里都是明摆着的，再多一个「某个目录下可能还有个 yaml」，出事时先得花半天确认哪份生效。同理也不做「命令行标志盖过环境变量」那一套：一个设置两个入口，就得规定谁盖谁。

### 怎么设

```bash
# 试一下：写在命令前面，只对这一次生效
HERDR_WEB_PORT=8000 HERDR_WEB_ONCONNECT= ./herdr-web

# 常驻：写进 ~/.zshrc（自己在终端里手起的时候）
export HERDR_WEB_HOST=0.0.0.0
export HERDR_WEB_TLS=auto

# 常驻：launchd（macOS）在 plist 的 EnvironmentVariables 里；
# systemd 在 unit 的 Environment= / EnvironmentFile= 里
```

三条规则，都和「猜」有关：

- **显式设成空串算数。** `HERDR_WEB_ONCONNECT=` 就是「连上什么都不敲」，不会退回默认值 `herdr`。有默认值的开关全靠这条才关得掉。
- **整数写错了当没设**（退回默认值），而不是静默变成 0；低于下限的夹到下限。`HERDR_WEB_DEVICE_TTL_DAYS=9O`（字母 O）不会把设备凭据变成「永不过期」。
- **开关认 `1` / `true`**（大小写随意），别的都算关。

改完重启进程才生效，配置只在启动时读一次。想确认读到了什么，看启动横幅：shell、数据目录、herdr socket、TLS 档位、已配对设备数都印在上面。

### 基本

| 变量 | 默认 | 说明 |
|---|---|---|
| `HERDR_WEB_PORT` | `7788` | 端口 |
| `HERDR_WEB_HOST` | `127.0.0.1` | 监听地址，`0.0.0.0` 开局域网 |
| `HERDR_WEB_TOKEN` | 读 `~/.herdr-web/token` | **旧机制**，只够引导一次（换成设备凭据）。新装不再自动生成 |
| `HERDR_WEB_SHELL` | `$SHELL` | PTY 里跑的 shell |
| `HERDR_WEB_ONCONNECT` | `herdr` | 连上就自动往 PTY 里敲这一行（自带回车）。**显式设成空串就不敲**（`HERDR_WEB_ONCONNECT=`）。**地址栏里带 session 的 URL 不看这一项**（`/work` 一律敲 `herdr --session work`，见「[一个 URL 一个 session](#一个-url-一个-sessionname)」）—— 想固定进某个 session 就把 URL 存书签，别写在这儿 |
| `HERDR_WEB_ONCONNECT_MS` | `250` | 上面那行等多久再敲。等的是「shell 吐出第一批输出之后」再加这么多 —— rc 里动 `stty` 或者补全插件初始化会**静默吞掉**早敲的字符。自动敲的那行没进去就调大它 |
| `HERDR_WEB_DIR` | `~/.herdr-web` | 数据目录，分两层：配置和文件（`softkeys.json` / `tls/` / `uploads/`）在根上，**内部数据**（设备凭据、passkey 公钥）在 `data/` 里 —— 那两个用户不该手改，被改了会在终端告警。**路径别太深**：里面要开一个 unix socket（`ctl.sock`），全长超过 ~100 字节就 bind 不上，子命令会用不了 |

### 发件箱 / 和 herdr 对接

| 变量 | 默认 | 说明 |
|---|---|---|
| `HERDR_WEB_SOCKET` | `$HERDR_SOCKET_PATH` 或 `~/.config/herdr/herdr.sock` | 发件箱连的 herdr socket。**别依赖 `HERDR_SOCKET_PATH`**：`dropEnv` 会把 `HERDR_*` 清掉，而本进程也可能不是从 herdr pane 里起的 |
| `HERDR_WEB_POLL_MS` | `500` | 发件箱多久对一次「焦点在哪 + 输入框里是什么」。下限 200 |
| `HERDR_WEB_PUSH_MS` | `700` | 开着「双向」时，停手多久把草稿推到远端。下限 100 |
| `HERDR_WEB_SETTLE_MS` | `120` | 两次 `pane.read` 之间等多久（对付快照的一帧延迟）。**别调成 0**：herdr 响应有时只要 1-2ms，两次读会落在同一帧上，清空循环会误判成「清不空」。清空那条路自己有 120ms 保底 |

### 暴露 / TLS / 凭据

细节见 [SECURITY.md](SECURITY.md)。

| 变量 | 默认 | 说明 |
|---|---|---|
| `HERDR_WEB_EXPOSED` | 关 | `=1` **声明这个口能从公网碰到**（frp / 端口转发 / 隧道）。走 frp 时本进程往往只监听 127.0.0.1，「监听地址是不是本机」这个判据完全失效，没法自动测，只能你自己说。声明之后：强制要求 TLS、关掉本机免配对 |
| `HERDR_WEB_TLS_CERT` / `_KEY` | 空 | 用指定的证书。自己有域名、DNS-01 签了张真证书就走这条 —— 浏览器零警告、不用装描述文件，最省事 |
| `HERDR_WEB_ACME_DNS` | 空 | 让 herdr-web **自己去签证书**，值是 DNS 服务商：`cloudflare` / `alidns` / `tencentcloud` / `route53` / `digitalocean` / `huaweicloud`。走 DNS-01，所以不需要外网能连进来 —— NAT 后面、甚至域名指到内网地址都能签。**各家 token 怎么拿、要给什么权限：[DNS.md](DNS.md)** |
| `HERDR_WEB_ACME_EMAIL` | 空 | ACME 账号邮箱。可以空着，但那样到期提醒也收不到 |
| `HERDR_WEB_ACME_STAGING` | 关 | `=1` 用 Let's Encrypt 测试环境。**调试时一定先开**：正式环境同一组域名一周只给 5 张证书，试几次就把自己锁一周 |
| `HERDR_WEB_TLS` | 见说明 | `auto` 自签（本地 CA + 397 天叶子，IP 变了自动重签）/ `off` 明文 / `proxy` 前置已经终止了 TLS。默认：暴露或听局域网 → `auto`，纯本机 → `off` |
| `HERDR_WEB_HOSTNAME` | 空 | 允许出现在 `Host` 头里的域名，逗号分隔。**IP 一律放行，域名必须在名单里** —— 这是 DNS rebinding 的唯一防线，不在名单里直接 421 |
| `HERDR_WEB_PUBLIC_URL` | 空 | 你**实际访问**的地址（`https://herdr.example.com:17788`）。frp 的公网端口和本地端口经常不是一个，不给就横幅上的二维码是废的。里面的域名自动进白名单 |
| `HERDR_WEB_DEVICE_TTL_DAYS` | `90` | 设备凭据多久不活跃就失效（每次用都续期）。`0` = **永不过期** |
| `HERDR_WEB_RPID` | 推导 | passkey 绑定的域名。默认取 `HERDR_WEB_HOSTNAME` 的第一个，纯本机时是 `localhost`。**裸 IP 不是合法值**，那种部署用不了 passkey |
| `HERDR_WEB_REAUTH_HOURS` | `24` | 注册过 passkey 之后，一份会话凭据在「上次生物验证」之后还能用多久。`0` = 不要求重验（passkey 只当登录/换设备的入口）。**一把 passkey 都没注册时这条完全不生效** |
| `HERDR_WEB_LEGACY_TOKEN` | `on` | `on` / `loopback`（旧 token 只在本机有效）/ `off`。迁移完建议直接删掉 token 文件 |
| `HERDR_WEB_TRUST_LOOPBACK` | 关 | `=1` 让来自 127.0.0.1 的请求免配对。**套 frp / 反代时千万别开** —— 那时候公网请求的源地址就是 127.0.0.1，等于谁都是「本机」。开着时还额外要求 `Host` 也是 loopback 字面量 |
| `HERDR_WEB_TRUST_PROXY` | 关 | `=1` 才读 `X-Forwarded-For`。没有可信前置时开着，攻击者自带一个头就能伪造源 IP、把按 IP 的限速绕干净 |
| `HERDR_WEB_INSECURE` | 关 | `=1` 允许「暴露出去但没有 TLS」。除了临时调试没有正当用途 |
| `HERDR_WEB_UPDATE_CHECK` | 开 | `=0` 关掉自动查更新。关掉之后这个进程**不会有任何出站请求** —— 内网机器不该主动连外网那类环境的硬要求。只是不自动查，手动 `herdr-web update --check` 照样能查 |

### 排查

| 变量 | 默认 | 说明 |
|---|---|---|
| `HERDR_WEB_DEBUG_INPUT` | 关 | `=1` 时把写进 PTY 的每一批字节 hex 打到日志（包括自动敲的那行，前缀 `onconnect`）。排「某个键到底发出去了什么」只能靠它 —— 猜是猜不出来的 |

### 不带 `HERDR_WEB_` 前缀、但会被读到的

| 变量 | 什么时候用 |
|---|---|
| `SHELL` | `HERDR_WEB_SHELL` 没给时，PTY 里跑的就是它（再没有就 `/bin/zsh`） |
| `HERDR_SOCKET_PATH` | `HERDR_WEB_SOCKET` 没给时的 herdr socket 兜底。**别指望它在**：`dropEnv` 会把 `HERDR_*` 从子进程里清掉（防嵌套启动），而本进程也可能不是从 herdr pane 里起的 |

### 几套常见的配法

```bash
# 1. 纯本机（默认）：明文 http，loopback 上本来就是 secure context
./herdr-web

# 2. 局域网里的手机 / 平板：自签 TLS，扫横幅上的二维码配对
HERDR_WEB_HOST=0.0.0.0 ./herdr-web

# 3. 走 frp / 隧道暴露到公网：EXPOSED 必须自己声明（进程只监听 127.0.0.1，
#    它自己看不出来外面有没有人能碰到），PUBLIC_URL 决定二维码编的是哪个地址
HERDR_WEB_EXPOSED=1 HERDR_WEB_TLS=proxy \
HERDR_WEB_PUBLIC_URL=https://herdr.example.com \
HERDR_WEB_HOSTNAME=herdr.example.com ./herdr-web

# 4. 自己有域名 + 真证书（浏览器零警告，最省事）
HERDR_WEB_HOST=0.0.0.0 HERDR_WEB_HOSTNAME=herdr.example.com \
HERDR_WEB_TLS_CERT=/etc/ssl/herdr/fullchain.pem \
HERDR_WEB_TLS_KEY=/etc/ssl/herdr/privkey.pem ./herdr-web

# 5. 不想一连上就进 herdr（留在 shell 里）
HERDR_WEB_ONCONNECT= ./herdr-web
```

## 守护进程

装成 user 级常驻服务，开机自启：

```bash
herdr-web service install     # macOS → launchd LaunchAgent；Linux → systemd user unit
herdr-web service status      # 装没装、跑没跑、PID、日志在哪
herdr-web service logs        # tail -f 日志
herdr-web service restart     # 换过二进制之后要这一步
herdr-web service uninstall   # 停掉并删掉（数据和日志不动）
```

**配置是装的那一刻从当前 shell 抄进去的。** 所以顺序是「先把环境配对，再 install」；改了配置要重新 `install`（幂等，就是覆盖 + 重启）。想从文件读：

```bash
herdr-web service install --env-file .env
```

抄进去的是所有 `HERDR_WEB_*`，加上 `PATH` / `SHELL` / `HOME` / `USER` / `LOGNAME` / `LANG` / `LC_ALL` / `TERM` / `HERDR_SOCKET_PATH`。`install` 会把这份清单全打出来 —— 以后「这台机器上服务到底在用哪套配置」只能靠 plist / unit 回答，装的时候看一眼最省事。

**签证书那条路（C / D 档）必须用 `--env-file`。** DNS provider 的凭据（`CLOUDFLARE_DNS_API_TOKEN`、`ALICLOUD_ACCESS_KEY` 这些）既不带 `HERDR_WEB_` 前缀、也不在上面那张白名单里，所以**从 shell 抄不进去**：你在 `.zshrc` 里 export 得再对，装出来的服务照样签不出证书，而且要等到第一次签发才炸。`--env-file` 里的 key 是**整份**进去的（还盖过当前环境），这是唯一能把 token 交给服务的路。文件只在 `install` 那一刻读，之后不再碰。

plist / unit 是 **0600** 的 —— 里面就是这份环境变量的明文。

**抄 `PATH` 是必须的，这是装成服务后最常见的故障**：launchd 给的默认 `PATH` 只有 `/usr/bin:/bin:/usr/sbin:/sbin`，于是 `HERDR_WEB_ONCONNECT=herdr` 变成 `herdr: command not found`，而页面上只看到一个空 shell，完全看不出为什么。

为什么是 user 级不是系统级：这个进程会开一个**你的** shell。跑成 root 的系统服务意味着浏览器里那个终端是 root 的，权限一步到位放到最大，而且 `~/.herdr-web`、`~/.config/herdr/herdr.sock` 这些路径全指到别人家去了。

两个平台各自的坑：

| | 文件 | 注意 |
|---|---|---|
| macOS | `~/Library/LaunchAgents/io.github.zbysir.herdr-web.plist` | LaunchAgent 是**登录时**起，不是开机时起。开了自动登录的机器上二者等价；没开的话得登录一次。真要「无人登录也起」只能用 `/Library/LaunchDaemons` 里的系统级 daemon，但那样 shell 就是 root 的，这个项目不做。 |
| Linux | `~/.config/systemd/user/herdr-web.service` | `install` 会顺手 `loginctl enable-linger`。**不开 linger 的话，你 ssh 退出登录之后服务会被停掉** —— 对一台要随时能连进去的机器来说那等于没常驻。失败会提示你手动 `sudo loginctl enable-linger $USER`。 |

日志两个平台都在 `~/.herdr-web/logs/herdr-web.log`（故意统一，文档和 `service logs` 只有一套说法）。Linux 上 `journalctl --user -u herdr-web` 一样能看。

`service status` 显示「装了但没在跑」就是**起来就崩**，原因只在日志里 —— launchd 和 systemd 都会按几秒的退避一直重试，不看日志会以为它在跑。

Windows / 没有 systemd 的 Linux（容器、WSL1）会明确告诉你用不了以及该怎么办，不会装一个跑不起来的东西。WSL2 里加 `[boot] systemd=true` 到 `/etc/wsl.conf` 然后 `wsl --shutdown` 重开就能用。

## 更新

```bash
herdr-web update            # 查 + 升
herdr-web update --check    # 只查，不动
herdr-web update --restart  # 升完顺手重启服务
herdr-web version           # 当前版本 + 当初是怎么装的
```

**怎么升取决于当初怎么装的**，`update` 自己判断（看可执行文件路径，symlink 会先解开）：

| 装法 | 升级动作 |
|---|---|
| npm | 调 `npm install -g @bysir/herdr-web@latest` |
| homebrew | 调 `brew upgrade herdr-web` |
| `go install` | 调 `go install …@latest` |
| release archive / install.sh | **本程序自己来**：下载 → 校验 sha256 → 同目录写临时文件 → `rename` 原子替换 |

包管理器装的不自己动文件，是因为去改 `node_modules` / `Cellar` 里的东西，下次那个包管理器一升级就盖回去了，白忙一场。

自己换的那条路有三个点是刻意的：**先校验再落地**（`checksums.txt` 对不上就整个放弃）、**临时文件必须同目录**（跨目录 `rename` 会 EXDEV）、**不删旧的**（unix 上 rename 覆盖一个正在运行的可执行文件是允许的，老 inode 还被进程持着，所以当前进程能安全跑到自己退出）。

**换完文件不等于换了正在跑的那个进程。** 重启才生效，而重启会掐掉所有正在用的终端会话 —— 所以这一步默认不做，`--restart` 才做。

新版本提示出现在三个地方：

- **启动横幅**最后一行（用缓存，不在启动路径上发请求 —— 网络慢的时候那会变成「启动卡十秒」）；
- **管理页**最上面横一条，带当前版本、该敲哪条命令、更新说明链接；
- 服务在跑的时候，后台每天查一次，发现新版本往**日志**里写一行（同一个版本只提一次，不会天天刷）。

查更新走 GitHub Releases 的匿名接口，结果缓存在 `~/.herdr-web/update.json`（落盘的，所以频繁重启不会变成每次都查；查失败也记时间戳，网络不通的机器不会每次启动都撞一次超时）。`HERDR_WEB_UPDATE_CHECK=0` 彻底关掉自动查 —— 关掉之后这个进程不会有任何出站请求。本地构建（`version` 显示 `dev`）不查也不提示。

## 安全

**这个东西等于一个 HTTP 上的 shell**（发件箱那条路即使不开 PTY 也能让 agent 跑命令），所以门是按这个前提设计的。设计文档和威胁模型在 [SECURITY.md](SECURITY.md)，这里只写现在**已经实现**的：

- **一台设备配一次**。一次性配对码（40 位、5 分钟、用一次就废，只在内存里）换一份 per-device 凭据，放 `HttpOnly; SameSite=Strict` cookie。服务端 `~/.herdr-web/devices.json` 里**只存 sha256** —— 这台机器上跑的 agent 天天读不可信内容，凭据文件被 prompt injection 读走在这儿是日常风险，不是理论风险。
- **凭据绑设备，不绑 IP。** 按 IP 记住信任两头都输：DHCP 会把你批准过的地址分给别人（客人连一下 Wi-Fi 就进你的 shell），而你自己换个 Wi-Fi 就要重新配对。
- **URL 里没有秘密。** `?pair=` / 旧 `?token=` 进来就换成 cookie 再 302 洗掉，所以浏览器历史、书签云同步、截图都不再是泄露渠道。
- **能撤销。** 命令行 `herdr-web devices` / `revoke`，网页上是设置 →「设备」里的「登出」/「踢掉」，下一个请求立刻 401。
- **配对码只能由坐在机器前的人产生**（`herdr-web pair` 或启动横幅），网页上任何路径都不出码，连已配对的设备也不行。两个理由：① 码创造的是一份**不随创造者一起被撤销**的独立凭据 —— 手机被人拿去一次、他配一台自己的进来，你之后把手机踢掉，他那台还在，等于绕过撤销做了持久化；② 码是打在终端里的，而那个终端往往是个 herdr pane，同 session 的 agent 能 `pane.read` 读到它 —— 要是外面的人能远程触发「打一个码」，「触发打印 + 被注入的 agent 读走」就是一条完整的远程配对链，人根本不用碰机器。在 L2 的第二因子做出来之前，「能读到那个终端」是系统里**唯一的带外因子**，不能动。
- **暴露出去又没 TLS 就拒绝启动**（以前只打一行警告，警告没人看）。自签走本地 CA + 397 天叶子，IP 变了自动重签、但设备信任的是 CA，所以不用重新点「继续访问」。
- **Host 白名单**挡 DNS rebinding（IP 一律放行，域名必须在 `HERDR_WEB_HOSTNAME` 里，否则 421）、**Origin 校验** + `SameSite=Strict` + 一个自定义头三道挡 CSRF、`/pty` 上没有 Origin 的 cookie 请求直接拒。
- **限速和封锁**：猜配对码前两次免罚，之后指数退避；15 分钟里 10 次封该 IP 15 分钟（重犯翻倍，上限 24 小时），换源 IP 的分布式尝试会触发全局熔断（只拒新配对，不动已有会话），终端上打告警。只数「猜短凭据」的失败 —— cookie 认不出来不算，不然刚 revoke 一台旧手机就把自己封了。**本机默认永不封**（否则解锁的入口也在门后面），但**声明了 `EXPOSED` 之后这个豁免自动关掉** —— 见下面 frp 那节，穿透进来的源 IP 全是 127.0.0.1，留着它整层限速就是空转。
- 安全响应头（CSP / nosniff / no-referrer / DENY）、PTY 并发上限 8、OSC 8 链接只放 `http/https/mailto`（终端上显示什么是程序说了算的）。
- **故意不发 HSTS**：自签证书配上 HSTS 会把「继续访问」那个口也焊死，而且清不掉。

- **passkey**（第二因子）。设置 → 设备 → passkey → 添加。服务端**只存公钥**，所以凭据文件被同机 agent 读走也没用（TOTP 的共享密钥做不到这一点，这是选它的主要原因）。加完之后：换新设备不用回机器前（同步的 passkey 在你所有设备上都有），而且会话凭据的寿命可以从三个月压到一天。要求用域名访问 —— 裸 IP 不是合法的 WebAuthn 标识。

还没做（按值排序）：审计日志、令牌滚动轮换 + 重用检测、`panic` 一键断开。

### 从公网连（frp / 隧道）

> 完整的公网访问方案、分档简化路线、以及实际部署时踩的运维坑：[DEPLOY.md](DEPLOY.md)。

推荐 **frp 的 `type = tcp` + herdr-web 自己拿真证书**：TLS 端到端，frps 那台 VPS 上只看得到密文。用 frp 的 https 模式就是在 VPS 上解密，那台机器能看到你的整个终端画面。

```bash
HERDR_WEB_EXPOSED=1 HERDR_WEB_TLS_CERT=~/certs/herdr.example.com/fullchain.pem HERDR_WEB_TLS_KEY=~/certs/herdr.example.com/privkey.pem HERDR_WEB_HOSTNAME=herdr.example.com HERDR_WEB_PUBLIC_URL=https://herdr.example.com:17788 ./herdr-web
```

证书用 DNS-01 签（`lego` / `certbot` 都行）：**不需要公网可达，只要能改一条 TXT 记录**，所以把 A 记录指到内网地址也能拿到浏览器默认信任的证书 —— 手机上零警告、是 secure context（剪贴板和 `OSC 52` 跟着正常）、passkey 的域名门槛也一起解决了。

⚠️ **`HERDR_WEB_EXPOSED=1` 必须自己声明**：走 frp 时 frpc 从本机连过来，herdr-web 看到的监听地址是 `127.0.0.1`、每个请求的源地址也是 `127.0.0.1`，「是不是本机」这个判据彻底失效。没声明的话「暴露了却检测不到」和「本机免配对把公网请求也放进来」这两个洞会同时开着。前者靠这个变量兜，后者已经改成**默认关**了。

⚠️ **frp 的 tcp 模式拿不到客户端真实 IP**：所有请求在 herdr-web 眼里都来自 `127.0.0.1`（frpc 在容器里时也一样）。这有两个后果：

1. 按 IP 的限速会把所有人算成同一个。封锁只挡新配对、不碰已有会话，所以最坏是「十五分钟内配不了新设备」，`herdr-web unlock` 解开。
2. **「本机永不封」这个豁免必须关掉**，否则整层限速是空转 —— 看起来配了，一次都不生效。`HERDR_WEB_EXPOSED=1` 会自动关掉它，这也是这个变量必须声明的另一个理由。

想要真实 IP 就在 frpc 上开 `transport.proxyProtocolVersion = "v2"`（herdr-web 还得会解 PROXY 协议头，现在不会），或者用 http 模式让 frps 加 `X-Forwarded-For`（那时候才配 `HERDR_WEB_TRUST_PROXY=1`）。**没有可信前置千万别开那个变量** —— 攻击者自带一个头就能伪造源 IP。

### 别的

http 不是安全上下文，`navigator.clipboard` 不可用，所以手机上 `OSC 52` 会失效、`⌘C` 退回 `execCommand('copy')` 兜底。上了 HTTPS 就都正常。

cookie **不区分端口**：同一台主机上另一个端口的 web 服务也会收到这个 cookie（`HttpOnly` 只挡 JS 读，挡不住浏览器发）。没有解法，别在同一台机器上跑不可信的 web 服务。

**配对码打在终端里，如果那是个 herdr pane，同 session 的别的 agent 能用 `pane.read` 读到它**（这个项目自己的发件箱就是这么读 pane 的）。窗口是 5 分钟 + 一次性，而且只有你主动出码时才存在（正因为这条，远程触发出码的口子被去掉了）；再不放心就在 herdr 之外的终端里 `herdr-web pair`。
