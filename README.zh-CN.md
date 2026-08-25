# herdr-web

<p align="center">
  <img src="assets/logo.png" alt="herdr-web" width="96" />
</p>

<p align="center">
  <a href="README.md">English</a> · <b>简体中文</b>
</p>

浏览器里的终端，用来跑 [`herdr`](https://github.com/zbysir/herdr)。一个 Go 二进制（前端嵌在里面），
手机也能用。

**语音投稿**是这个项目的主功能：在平板上说话打字，说错的字框选重说就改掉，改完整段投进 agent 的
输入行。手机上够用，平板横屏 211 列 —— 那是个工位。

这份文档只讲**怎么装、怎么用、怎么配**。每件事「为什么这么做、踩过什么坑」在
[下面的文档索引](#文档)里，那些才是这个项目真正的家底。

## 装

```bash
npm install -g @bysir/herdr-web       # 有 node 的话最省事，升级也交给它
herdr-web                             # 只听 127.0.0.1
```

没有 node（服务器上常见）：

```bash
curl -fsSL https://raw.githubusercontent.com/zbysir/herdr-web/master/install.sh | sh
```

装到 `~/.local/bin`。要换地方，变量得给 `sh` 而**不是** `curl` —— 写错**不会报错**，
它会安安静静装到默认目录去：

```bash
curl -fsSL …/install.sh | HERDR_WEB_INSTALL_DIR=/opt/bin sh    # 对
HERDR_WEB_INSTALL_DIR=/opt/bin curl -fsSL …/install.sh | sh    # 错
```

同理 `HERDR_WEB_INSTALL_VER=v0.1.0` 装指定版本。装脚本**强制校验 sha256**，
没有 `sha256sum`/`shasum` 就直接拒绝装 —— 这东西后面挂着一个登录 shell。

从源码：

```bash
make build && ./herdr-web             # 前端 → 拷进 internal/webui/dist → go build
```

`go install …/cmd/herdr-web@latest` 也能装，但**装出来的没有前端**（前端产物不入版本库，
`go install` 拿不到），只能配 `--web <目录>` 或者当命令行工具用。

**Windows 没有原生版**，在 WSL 里装：浏览器里那个终端需要一个真 PTY，herdr 自己也走 unix socket。
WSL 里就是 linux 版，功能完整，Windows 上开 `http://localhost:7788/` 照样用。

装成开机自启的常驻服务见[守护进程](#守护进程)，升级见[更新](#更新)。

## 第一次跑

启动后会打印可用地址；监听 `0.0.0.0` 时会按网卡打分，把手机真能连上的那个标成 `← 手机用这个`，
二维码编的就是它。

**一台设备配一次**：启动横幅里有一个一次性配对码（5 分钟过期、用一次就废）和它的二维码，
手机扫一下就进去了 —— 零输入，之后书签里没有任何秘密（凭据在 HttpOnly cookie 里），
换 Wi-Fi、换网段、重启都不用重来。扫码有三条路：相机 App 扫（码里就是带 `?pair=` 的链接）、
配对页里的「用相机扫」（能用才出现 —— 它要 `BarcodeDetector` 和安全上下文里的摄像头），
或者把那 8 位码抄进配对页的框里。

```bash
herdr-web pair          # 出一个新的一次性配对码 + 二维码
herdr-web devices       # 列出已配对设备（标签 / 最后活跃 / 最后 IP / 到期）
herdr-web revoke <id>   # 踢掉某台（all = 全部）；下一个请求立刻 401
herdr-web unlock        # 解开「失败太多」的全局熔断
```

**网页上不出配对码**（连已配对的设备也不行），理由见[安全](#安全)。配对码用完了就回机器前
`herdr-web pair`。

连上之后**自动敲 `herdr`**。想敲别的、或者不想自动敲：`HERDR_WEB_ONCONNECT`（设成空串就留在
shell 里）。

**地址栏里加一段路径就是另一个 herdr session**：`/work` 敲的是 `herdr --session work`，
没有就新建一个；`/scratch` 再来一套，两个书签就是两套工作现场，关掉浏览器再回来还在。
名字只能是 `[A-Za-z0-9._-]`、最长 40，不合法直接报错而不是退回默认 session ——
拿错 socket 投出去的话，话会**静默进另一个 herdr**。

**管理页在 `http://127.0.0.1:<端口+1>/`**：看证书状态、点一下签发/续期、生成 DNS 的 `.env`
片段、出配对码、踢设备。它只绑 loopback、公网上不存在，所以不需要登录 —— 能连上它的东西
已经有你的 shell 了。

**只开本机 shell。** 要连别的机器就在 herdr 里 ssh —— herdr 自己就能干这事，所以这一层不做
主机管理和托管私钥，连带把「浏览器能碰到私钥」这个安全面也去掉了。

## 能干什么

### 发件箱（语音投稿）

页面底下那条带 textarea 的就是发件箱，顶栏 ✎ 开关，**默认开着**。在里面说话打字，改完整段
投进 herdr 的某个 pane。

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

传图不用开发件箱：软键条里配一个 `act:img` 的键，或者**整页粘贴**（剪贴板里是图就直接传）。
路径去哪儿看发件箱开没开 —— 开着接到草稿末尾，没开就直接敲进终端。

→ 为什么要单独一个框、图片怎么走通、双向同步的注意事项、轮询的实测延迟：[OUTBOX.md](docs/dev/OUTBOX.md)

### 软键条

手机没有 Ctrl 键，herdr 的 `ctrl+b` 前缀全靠这条。按键**存在服务端**
（`~/.herdr-web/softkeys.json`），手机 / 平板 / 电脑共用一份定义，在设置 →「软键条」页改。

- 「按键」一栏写**按键谱**，空格分隔可以连发多下 —— `ctrl+b c` 就是前缀加 c，一下点出来。
- 支持 `ctrl+x` `alt+x` `shift+tab`、具名键（`esc tab enter space bs del ins up down left right
  home end pgup pgdn f1-f12`）、原样文本（`text:/new`，带空格要引号）。
- `sticky:ctrl` / `sticky:alt` 是**粘滞**修饰键：点一下亮起，再敲一个字母就发出组合键。
- `act:` 是网页端动作，不发字节：`act:kbd` 呼出键盘、`act:img` 传图、`act:panes` 开面板一览、
  `act:files` 开文件、`act:clip` / `act:paste` 是[手机上的复制粘贴](docs/dev/MOBILE.md#手机上怎么复制--粘贴)。
- 每个键有个**「两下」**勾选框，关 pane / 关标签 / `/clear` 默认就带 —— 键挨得近，误触没法撤销。
- 键宽**按内容自适应**，没有「占几格」那种设置 —— 条是横滑的，滑一下上下两行就对不齐，
  格子对齐压根不成立。（唯一还需要固定格宽的是**弹出组的浮窗**，那儿不滚动。）
- 每个键可以挑一个**内置图标**（四十多个：修饰键 / 编辑键 / 方向 / 面板一览 / 文件 /
  传图 / 放大缩小 …），再选**摆哪儿**：只图标 / 图标+字 / 字+图标 —— `^B 前缀` 这种就该是
  「图标+字」，那个 `B` 不能丢。名字一直留着，指上去能看到。`⌨` 这类字形在很多字体里缺
  （显示成方框）、难看、基线还跟旁边的字母对不齐，图标是 SVG，三个问题一起没了。
- **按键样式**（设置 →「终端」页）：`有底色`（默认）或 `无底色`。后者只剩字和图标，让终端
  多露出来一点；**亮着的键照旧涂满**（粘滞 Ctrl、面板开着那种 —— 不给底就分不出来了）。
- 「载入预设」一下把六十多个键灌进「我的按键」，之后每个都能自己改。

**弹出组**：一个键在条上**只占一格**，点一下在它上面浮出那几个键 —— 方向键就该是这个
（摊在条上要 3×2 六格，手机竖屏上那是半条屏幕）。点「软键条」页上的**「方向键」**一下就摆好。

```
[ Esc ][ ^B ][ 方向 ]…横滑…       点「方向」→      ·  ↑  ·     ← 浮在上面，不占条上的地方
                                                   ←  ↓  →
```

浮窗**盖在终端上，条一点都不重排**。再点一次那个键、或者点到浮窗外面就收起；按里面的键
**不收**（方向键要连着点）。要别的组合，点「弹出组」自己往格子里拖。

**钉住**（每行下面那条「钉住 左 n 右 n」）说的是**头几个 / 尾几个不跟着横滑**：

```
[⌨]│ ^B  Ctrl  Alt  Esc  Tab …横滑… │[↵]
 ↑         只有中间这段跟着滑         ↑
钉左                              钉右
```

手机上条是横滑的，而「呼键盘」「Esc」这种每隔十秒就要按一次的键，滑走了就等于没有。
框里那条竖线就是界线。它是**排布**，所以每套一份（平板和手机各钉各的）。

存的是**个数**不是另一份列表 —— 那一行照旧是完整顺序，所以降级回老版本也不会
「钉住的那几个不见了」。细节见 [MOBILE.md](docs/dev/MOBILE.md#键宽是几格钉住的那几个不跟着滑)。

按键谱在**服务端**解析成字节，写错了保存时就告诉你是第几个键、哪儿不认，不会下发一个点了
没反应的键。

### 面板一览 · 提示

顶栏的 ▦（或软键条上的 `act:panes`，手机上还能点 herdr 自己顶栏那个 `switch`）打开一张 pane
列表：一行一个，**点一下就跳过去并铺满全屏**。上面能筛（tab / 标题 / 路径 / pane id）、能只看
跑着 agent 的。列表自己 4 秒刷一次。

agent 停下来等你回答（或刚跑完），右上角**弹一张卡片，卡上带它说的那段话**，顶栏 ▦ 上点一个
红点。点卡片 = 跳过去。开一次面板一览就算看过，红点才灭。

它是索引，不是第二个界面：点完看的还是同一个 herdr 终端，键盘那套操作一个字都没变。

→ 排序规则、「3 分钟前」那一列、什么时候弹、红点怎么算、系统通知：[MOBILE.md](docs/dev/MOBILE.md)
　那段话是怎么抽出来的：[COMPOSER.md](docs/dev/COMPOSER.md)

### 文件浏览

agent 说「图生成在 `/tmp/plot-3.png`」，**点那行路径就能看**。绝对路径直接开，`./out/a.png`
按那个 pane 的 cwd 解析。顶栏的 📁（或 `act:files`）是兜底：起点是各 pane 的 cwd + 上传目录 +
家目录 + 临时目录，进去之后一路 `..` 能走到 `/`，也能粘一个绝对路径直接开。

图（png / jpg / gif / webp，按魔数认）直接看，文本原样显示，别的只能下载。看完能一键**投给
agent**（把绝对路径插进发件箱）。

**默认没有边界** —— 能打开这个页面的人已经有一个登录 shell，白名单挡不住他、只会天天挡路。
要边界就配 `HERDR_WEB_FILE_ROOTS`（那才是真 jail），整块不要就 `HERDR_WEB_FILES=0`。

→ 短时链接那条路、四条硬规矩（绝不以 `text/html` 吐、SVG 为什么敢渲染）：[SECURITY.md](docs/dev/SECURITY.md)

### 看 diff

`git diff` 在手机的终端里基本读不了：长行不是被切就是横滚，一整行红配一整行绿看不出改了
哪个词，翻页还得靠 pager。顶栏的「改动」（或 `act:diff`）另开一层：

- **先一份清单**：哪些文件改了、各自 `+n −m`、哪些已经 `git add` 过；
- 点进去是**能折行**的补丁（默认折行，右上角那个按钮切换，选择记在这一套排布里），
  配得上对的两行还会**按词高亮**——只有真正变了的那截是深色底；
- **这一次改动的全部文件是一条连续的流**：看着 a 往下滑就到 b，不用退回清单再点一次。
  顶栏那行跟着滚（`第 3 / 19 · 文件名`），点文件之间那条分隔带能把它折起来跳过去。
  没滚到的文件先占个位，滚到跟前才去读（一次读一个，不跟 agent 抢那台机器）；
- 三档：**改动**（工作区相对上次提交，含新建的文件）/ **已暂存** / **上次提交**；
- 在**哪个仓库**不用选：拿各个 pane 的 cwd 去认，按仓库根去重，焦点那个排最前面；
- 顶栏那个按钮上有个**绿点**：有**你还没看过的**改动（不是「有改动」—— 那在一个正干活的
  仓库里永远为真，点就等于一直亮着）。开一次面板就算看过，点灭掉；agent 又动了就再亮。
  它跟着设置里「面板红点」那个开关一起关 —— 关掉之后连轮询都不做。

**只读。** 这儿没有 add / commit / checkout，也不打算有 —— 会改仓库的事在终端里做，
那儿有完整的 git，还看得见输出。边界和文件浏览是同一套（`HERDR_WEB_FILES=0` 时一起关掉，
配了 `HERDR_WEB_FILE_ROOTS` 时 jail 照样管仓库根）；这台机器上没有 git 就不画那个按钮。

### 手机和平板

有程序在收鼠标上报时（herdr 这种），触屏手势整个由本项目接管：

| 手势 | 行为 |
|---|---|
| 单指纵向滑动 | 按行高换算成 SGR 滚轮上报 `CSI < 64/65 ; col ; row M` 发给程序；没开鼠标上报时滚本地 scrollback |
| 单击 | 有鼠标上报时发 `CSI < 0 ; col ; row M/m` 给程序（点 pane、点 tab 都好使）且**不弹系统键盘**，当场就发、没有延迟；没有鼠标上报时聚焦隐藏 textarea（点一下就是想打字）|
| 长按（≈380ms）| **抓住**：按下左键不松手 + `CSI < 32` 上报移动，之后滑动就是拖 —— herdr 的「拖 pane 边框改大小」在手机上靠这条。松手补 `m` 发松开 |

**没有双击。** 双击原来是「呼出 / 收起系统键盘」的入口，去掉了 —— 一个手势赔上了整套手感：
要分清「这是单击」还是「这是双击的第一下」，每次单击都得压着等一个双击窗口（320ms）才敢发出去，
点 pane、点 Claude 里的东西全慢一拍；而只要不等，第一下就**漏进 pane 里那个程序** —— Claude Code
自己有可点的东西（展开一段、**选一个选项**），漏一下就把选项给选了。为一个「呼键盘」的快捷方式
赔上「所有点击都不准、还可能替你选答案」，不值。

键盘现在只走**按钮**：软键条上的 ⌨（`act:kbd`，出厂配置第一个键就是它，手机上软键条默认开着）
和顶栏上那个「系统键盘」（在设置 →「顶栏」里拖上去）。点了就是要键盘 —— 不用猜，也没有延迟。

**发件箱和软键条是同一块底部面板**：两条侧边左右拖改宽度（输入法压住半边屏幕时把整块缩到
剩下的空地上），键那一区上边缘三个把手上下拖改高度、左右拖改边界，任意把手双击复位。
手机竖屏（< 440px）自动换一档：把手全收、面板通屏、软键条一行横滑。**横屏一套、竖屏一套**，
转屏就换成那一份。

**顶栏放哪几个按钮自己拖**，而且**排布按设备类别分套**：手机上排好的六个键不会跟着到桌面，
定义仍然全局共用一份。

顶栏上除了内置按钮，还能放**「我的按键」**里的键（设置 →「顶栏」页第三个筐拖上去）——
画成一个 mono 方块，和图标一眼分得开。存的是**引用**：改一处按键谱两边一起变，在软键条那页
删掉一个，顶栏上也跟着没了。所以 `ctrl+b z` 这种不用等它变成内置按钮，自己配一个拖上来就行。

→ 手势为什么这么分、键盘怎么收、手机上怎么复制粘贴、面板和顶栏的细节：[MOBILE.md](docs/dev/MOBILE.md)

### 设置面板

顶栏最右的 ⚙，分四页：**终端**（字号 / 明暗、kitty 协议 / Option 当 Meta / 选中即复制 /
同步输出、点 herdr 的 switch 开面板一览、面板图标上的红点）、**顶栏**、**软键条**、**设备**。
分页条上面还有一行「这台设备用哪一套排布」。三块浮层（面板一览 / 文件 / 设置）是互斥的。

### 键盘

herdr 的快捷键基本都是 `ctrl+b` 前缀加一个普通键，legacy 编码就能表达。kitty 协议补的是
legacy 表达不了的组合，默认开着（设置 →「终端」里可关）：`Ctrl+Shift+字母`、`Ctrl+数字`、
`Ctrl+Enter` / `Shift+Enter` / `Ctrl+Tab`。

抢不回来的键（浏览器自己吃掉）：macOS 上是 `⌘W` `⌘T` `⌘N` `Ctrl+Tab`；Windows/Linux 上还多
`Ctrl+W` `Ctrl+T` `Ctrl+N` `Ctrl+Shift+I/J/C`。装成 PWA 能拿回一部分。

复制 `⌘C`（或 `Ctrl+Shift+C`）· 粘贴 `⌘V` · 清屏 `⌘K` · `Option` 默认当 Meta。
手机上复制粘贴是另一回事（herdr 复制到的是**跑 herdr 那台机器**的剪贴板），
见 [MOBILE.md](docs/dev/MOBILE.md#手机上怎么复制--粘贴)。

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
| `HERDR_WEB_PORT` | `7788` | 主口。**只服务本地网络**：对端不是本机 / 私网 / 链路本地 / CGNAT 的连接一律 403。要从公网访问就另开一个显式端口，见 `HERDR_WEB_PUBLIC_PORT` |
| `HERDR_WEB_HOST` | `127.0.0.1` | 监听地址，`0.0.0.0` 开局域网 |
| `HERDR_WEB_TOKEN` | 读 `~/.herdr-web/token` | **旧机制**，只够引导一次（换成设备凭据）。新装不再自动生成 |
| `HERDR_WEB_SHELL` | `$SHELL` | PTY 里跑的 shell |
| `HERDR_WEB_ONCONNECT` | `herdr` | 连上就自动往 PTY 里敲这一行（自带回车）。**显式设成空串就不敲**（`HERDR_WEB_ONCONNECT=`）。**地址栏里带 session 的 URL 不看这一项**（`/work` 一律敲 `herdr --session work`，见[第一次跑](#第一次跑)）—— 想固定进某个 session 就把 URL 存书签，别写在这儿 |
| `HERDR_WEB_ONCONNECT_MS` | `250` | 上面那行等多久再敲。等的是「shell 吐出第一批输出之后」再加这么多 —— rc 里动 `stty` 或者补全插件初始化会**静默吞掉**早敲的字符。自动敲的那行没进去就调大它 |
| `HERDR_WEB_DIR` | `~/.herdr-web` | 数据目录，分两层：配置和文件（`softkeys.json` / `tls/` / `uploads/`）在根上，**内部数据**（设备凭据、passkey 公钥）在 `data/` 里 —— 那两个用户不该手改，被改了会在终端告警。**路径别太深**：里面要开一个 unix socket（`ctl.sock`），全长超过 ~100 字节就 bind 不上，子命令会用不了 |
| `HERDR_WEB_FILES` | 开 | `=0` 关掉文件浏览：`/api/files/*` 和 `/_f/` 全部 404，顶栏那个 📁 也不画（点开一片 404 比没有入口更糟） |
| `HERDR_WEB_GIT` | 开 | `=0` 关掉「看 diff」那个面板：`/api/git/*` 全部 404，顶栏那个按钮也不画。**它还压在 `HERDR_WEB_FILES` 底下** —— 一份 diff 就是文件内容，文件浏览关着却还能看 diff 的话，那个开关就是假的。这台机器上没有 `git` 时同样不画 |
| `HERDR_WEB_FILE_ROOTS` | 空 | 逗号分隔的目录，配了就是**真白名单**（jail），只有这几棵树看得到。**空 = 不设边界**，理由见[文件浏览](#文件浏览)。展开 `~`，非绝对路径直接扔掉（相对谁？留着只会让前缀检查在意想不到的地方通过） |

### 发件箱 / 和 herdr 对接

| 变量 | 默认 | 说明 |
|---|---|---|
| `HERDR_WEB_SOCKET` | `$HERDR_SOCKET_PATH` 或 `~/.config/herdr/herdr.sock` | 发件箱连的 herdr socket。**别依赖 `HERDR_SOCKET_PATH`**：`dropEnv` 会把 `HERDR_*` 清掉，而本进程也可能不是从 herdr pane 里起的 |
| `HERDR_WEB_POLL_MS` | `500` | 发件箱多久对一次「焦点在哪 + 输入框里是什么」。下限 200 |
| `HERDR_WEB_PUSH_MS` | `700` | 开着「双向」时，停手多久把草稿推到远端。下限 100 |
| `HERDR_WEB_NOTICE_MS` | `4000` | 提示（右上角弹窗 + 红点）多久问一次「有没有新的」。**`0` = 关掉整套提示**，前端不再轮询。低于 1000 一律按 1000 算 —— 这一拍在服务端只读内存（不打 herdr socket），但提示天生比状态晚 2.5 秒（防抖），问得再勤也快不过那一段 |
| `HERDR_WEB_SETTLE_MS` | `120` | 两次 `pane.read` 之间等多久（对付快照的一帧延迟）。**别调成 0**：herdr 响应有时只要 1-2ms，两次读会落在同一帧上，清空循环会误判成「清不空」。清空那条路自己有 120ms 保底 |

### 暴露 / TLS / 凭据

细节见 [SECURITY.md](docs/dev/SECURITY.md)。

| 变量 | 默认 | 说明 |
|---|---|---|
| `HERDR_WEB_PUBLIC_PORT` | 关 | **要暴露就暴露这个口。** 在 `0.0.0.0:<端口>` 上另起一个监听（和主口同一个 handler），隧道 / 端口转发 / 反代指它，**别指主口**（主口只服务本地网络）。落在这个口上的请求按公网对待：本机免配对、旧 token 的 `loopback` 档都不生效（穿透进来的源地址也是 127.0.0.1，唯一靠得住的判据是「落在哪个监听上」）、限速的「本机永不封」豁免关掉、TLS 变成强制。为什么是多一个口而不是主口上加个开关：开关是**声明**，而声明会漏 —— 在这台机器上写代码的人（尤其是 agent）看到的是 `127.0.0.1:7788`，它没法知道机器上还有一条隧道正把这个口转出去，于是「反正只有本机能连」这个前提下做的每个决定都变成公网上的洞。换成独立端口之后，漏配的表现是隧道那头 connection refused |
| `HERDR_WEB_EXPOSED` | 关 | **老写法，新配置用 `HERDR_WEB_PUBLIC_PORT`。** `=1` 声明**主口本身**能从公网碰到（frp / 端口转发 / 隧道）—— 这件事没法自动测，只能你自己说。声明之后：强制要求 TLS、关掉本机免配对，主口那道「只服务本地网络」的门也跟着让开（既然你说了它是公网口）。留着只为兼容已经这么配的机器 |
| `HERDR_WEB_TLS_CERT` / `_KEY` | 空 | 用指定的证书。自己有域名、DNS-01 签了张真证书就走这条 —— 浏览器零警告、不用装描述文件，最省事 |
| `HERDR_WEB_ACME_DNS` | 空 | 让 herdr-web **自己去签证书**，值是 DNS 服务商：`cloudflare` / `alidns` / `tencentcloud` / `route53` / `digitalocean` / `huaweicloud`。走 DNS-01，所以不需要外网能连进来 —— NAT 后面、甚至域名指到内网地址都能签。**各家 token 怎么拿、要给什么权限：[DNS.md](DNS.md)** |
| `HERDR_WEB_ACME_EMAIL` | 空 | ACME 账号邮箱。可以空着，但那样到期提醒也收不到 |
| `HERDR_WEB_ACME_STAGING` | 关 | `=1` 用 Let's Encrypt 测试环境。**调试时一定先开**：正式环境同一组域名一周只给 5 张证书，试几次就把自己锁一周 |
| `HERDR_WEB_TLS` | 见说明 | `auto` 自签（本地 CA + 397 天叶子，IP 变了自动重签）/ `off` 明文 / `proxy` 前置已经终止了 TLS。默认：暴露或听局域网 → `auto`，纯本机 → `off` |
| `HERDR_WEB_LAN_PORT` | 关 | 在 `0.0.0.0:<端口>` 上**另开一个监听**，自签证书、SAN 跟着当前局域网地址走。这样从隧道进来的页面能嗅探出「其实就在同一个局域网里」并切过去 —— 每个字的往返从两跳公网变成一跳交换机。每台设备有一步手动的、跳不过去：**先开一次它、点「继续访问」**；没点过的话嗅探会在 TLS 握手那里失败，页面就安静留在隧道那条路上。这个口**必须是 TLS**：https 页面对 `http://` 目标的 fetch 算 active mixed content，浏览器一律拦死，明文的口压根探不到。主口本来就在局域网上服务自签 TLS 的话不用配它。**直连那个 origin 上是一份独立凭据**（cookie 是 host-only 的），所以同一台平板在设备面板里会出现两条 —— 切过去那一下会带一个一次性配对码过去，不用你手配 —— 而且那一侧**用不了 passkey**：WebAuthn 的 RPID 只能是域名，裸 IP 不是，装了 CA 也一样。细节见 [DEPLOY.md](DEPLOY.md) |
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

# 3. 走 frp / 隧道暴露到公网：**隧道指公网口，别指主口**（主口只服务本地网络，
#    它上面那些宽松默认都建立在「公网碰不到」上面），PUBLIC_URL 决定二维码编哪个地址
HERDR_WEB_PUBLIC_PORT=17788 HERDR_WEB_TLS=proxy \
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

**DNS provider 的凭据也带 `HERDR_WEB_` 前缀**（`HERDR_WEB_CLOUDFLARE_DNS_API_TOKEN`、`HERDR_WEB_ALICLOUD_ACCESS_KEY` 这些），所以跟着上面那条规则一起抄进去 —— shell 里 export 过就行，不用非走 `--env-file`。前缀不是为了整齐：光秃秃的 `CLOUDFLARE_DNS_API_TOKEN` 前缀和白名单两头都不占，抄不进去，而这个失败要等到第一次签发（或者三个月后第一次续期）才现形。老写法 lego 自己仍然认，但只能靠 `--env-file` 送进服务；两个都给的话带前缀的赢。各家变量名见 [DNS.md](DNS.md)。

`--env-file` 里的 key 是**整份**进去的（还盖过当前环境），文件只在 `install` 那一刻读，之后不再碰。`install` 打出来的那份清单里，凭据只显示星号和长度 —— 那段输出常常就落在一个跑着 agent 的 pane 里。

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

**这个东西等于一个 HTTP 上的 shell**（发件箱那条路即使不开 PTY 也能让 agent 跑命令），所以门是
按这个前提设计的。已经实现的：

- **一台设备配一次。** 一次性配对码换一份 per-device 凭据，放 `HttpOnly; SameSite=Strict` cookie；
  服务端**只存 sha256** —— 这台机器上跑的 agent 天天读不可信内容，凭据文件被 prompt injection
  读走是日常风险。
- **凭据绑设备，不绑 IP。** 换网、换网段都不掉线；按 IP 记信任两头都输。
- **URL 里没有秘密。** `?pair=` 进来就换成 cookie 再 302 洗掉，书签云同步和截图都不是泄露渠道。
- **能撤销。** `herdr-web revoke`，或设置 →「设备」里踢掉，下一个请求立刻 401。
- **配对码只能由坐在机器前的人产生**，网页上任何路径都不出码 —— 那是系统里唯一的带外因子。
- **暴露出去又没 TLS 就拒绝启动。** Host 白名单挡 DNS rebinding，Origin + `SameSite=Strict` +
  自定义头三道挡 CSRF，猜配对码指数退避 + 按 IP 封锁 + 全局熔断。
- **passkey 是第二因子**（服务端只存公钥，泄露也没用）。加完之后换新设备不用回机器前，
  会话凭据的寿命能从三个月压到一天。

→ 威胁模型、每条为什么这么设计、还没做的：[SECURITY.md](docs/dev/SECURITY.md)
　从公网连（frp / 隧道）、TLS 四档：[DEPLOY.md](DEPLOY.md)

## 文档

**这一份和 DEPLOY / DNS 是「使用说明」**；下面前五份在 [`docs/dev/`](docs/dev/README.md) 里，那一层写的是**开发理由**：为什么这么设计、实测出来的语义、会静默出错的坑。

| 要看什么 | 去哪儿 |
|---|---|
| 发件箱：为什么单独一个框、图片怎么走通、轮询实测 | [OUTBOX.md](docs/dev/OUTBOX.md) |
| 读屏：抽输入框、抽提示卡上那段话 | [COMPOSER.md](docs/dev/COMPOSER.md) |
| herdr socket API 实测出来的语义 | [HERDR-API.md](docs/dev/HERDR-API.md) |
| 手机 / 平板那一整套（手势、键盘、面板、顶栏、提示、复制粘贴） | [MOBILE.md](docs/dev/MOBILE.md) |
| 安全设计和威胁模型、文件浏览那条路的规矩 | [SECURITY.md](docs/dev/SECURITY.md) |
| 放在哪儿跑、公网访问、TLS 分档 | [DEPLOY.md](DEPLOY.md) |
| 各家 DNS 的 token 怎么拿、要给什么权限 | [DNS.md](DNS.md) |
| 改代码之前先读（代码结构、发版、配色、会静默出错的坑） | [CLAUDE.md](CLAUDE.md) |

MIT。
