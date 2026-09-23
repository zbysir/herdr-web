# herdr-web

浏览器里的 herdr 终端 + **语音投稿**（平板手写笔说话打字、框选重说改字，投进 herdr 的 agent pane）。

一个 Go 二进制，前端（React + Vite + Tailwind）嵌在里面：`make build` → `./herdr-web`。
**文档分两层**：根目录是**使用说明**（怎么装 / 怎么用 / 怎么配 / 放哪儿跑），
[`docs/dev/`](docs/dev/) 是**开发理由**（为什么这么设计、实测出来的语义、会静默出错的坑）。
两种读者要的东西完全不一样，混在一份里两边都读不下去。

- 使用说明：[README.zh-CN.md](README.zh-CN.md) · [DEPLOY.md](DEPLOY.md) · [DNS.md](DNS.md)
- 开发文档索引：[docs/dev/README.md](docs/dev/README.md)
- 代码结构、发版、配色、已经踩过的坑：本文档末尾

动这几块之前先读对应的那份 —— 里面是实测出来的语义和一串**会静默出错**的坑：

| 要动 | 读 |
|---|---|
| herdr socket 调用（`internal/herdr`、`outbox`、`agentwatch`） | [HERDR-API.md](docs/dev/HERDR-API.md) |
| 抽输入框（`internal/composer`） | [COMPOSER.md](docs/dev/COMPOSER.md) |
| 发件箱（`internal/outbox`、`web/src/hooks/useCompose.ts`） | [OUTBOX.md](docs/dev/OUTBOX.md) |
| 触屏 / 移动端的面板、顶栏、提示（`web/src/term/`、`components/`） | [MOBILE.md](docs/dev/MOBILE.md) |
| 认证、配对、暴露形态、文件浏览 / 看 diff 那条路 | [SECURITY.md](docs/dev/SECURITY.md) |
| **chat 模式**（对话流代替那一屏 TUI） | [CHAT.md](docs/dev/CHAT.md) |
| **要不要在网页这边新做一块界面**（还是留在 TUI / 去扩 herdr） | [TUI-VS-GUI.md](docs/dev/TUI-VS-GUI.md) |

## ⚠️ 起服务之前：本机的端口不一定只有本机能连

**这台开发机上跑着一条 frp 隧道**（`~/frp-docker/client/frpc.toml`，frpc 在容器里，frps 在
公网 VPS 上）。它把本机的某个端口转到公网 —— 也就是说，你在终端里看到的
`http://127.0.0.1:<端口>/` 有可能**整个互联网都连得到**，而且：

- **本地一点症状都没有。** 监听地址是 `127.0.0.1`、每个请求的源地址也是 `127.0.0.1`
  （frpc 从本机连过来），从进程里看不出任何区别。
- **`lsof` 也看不出来。** 转发关系在 frpc 的配置里，不在这个进程里。

所以下面这几条是硬规矩，不是建议：

1. **常驻那份服务默认别碰 —— 别替他起、别重启、更别杀。**
   他在平板上说话投稿走的就是这个进程 —— 它一停，人连「给你发下一条消息」的路都没了，
   而你在这边一点症状都看不到。所以编完就说一句「编好了，你重启一下」，别自己去起。
   要在真机上验改动，另起一个调试实例（下面第 3 条），**验完按 pid 杀**（起的时候记下来）：
   `pkill -f herdr-web` 这类按名字匹配的一律不许用 —— 它会把常驻那份一起带走（真踩过一次，
   把用户的投稿路掐了十几分钟）。

   **它现在是 launchd 托管的**（`~/Library/LaunchAgents/io.github.zbysir.herdr-web.plist`，
   `KeepAlive=true`），跑的是 **npm 全局装的那个二进制**，配置在 plist 的
   `EnvironmentVariables` 里（**不读仓库的 `.env`**），日志在 `~/.herdr-web/logs/herdr-web.log`。
   早先是用户自己 `make run` 的，2026-09-20 核实已经不是了。由此来的两条：
   ① **`npm i -g` 会就地换掉正在跑的那个文件**，而进程还用着旧 inode —— 「装完了」不等于
   「跑的是新版」，`herdr-web version` 报的是**文件**的版本，不是那个进程的；
   ② `KeepAlive=true` 意味着 `kill` 掉它 launchd 立刻拉起来。用户**明确要求**更新版本时才重启，
   用 `launchctl kickstart -k gui/$(id -u)/io.github.zbysir.herdr-web`（原地重启，配对设备和
   passkey 都在 `~/.herdr-web/` 里，不会丢）。
2. **不要为了省事关掉鉴权。** 一个「反正只有本机能连，先把鉴权关了调一下」的临时状态，
   在这台机器上等于把一个登录 shell 挂在公网上，只要那段时间有人扫到就完了。
   要免配对就 `HERDR_WEB_TRUST_LOOPBACK=1`（它只在主口生效，公网口不认），
   **别改代码去绕认证**，也别把这个变量写进要提交的 `.env`。
3. **起本地实例要给自己一套独立的端口和目录**，别抢默认口：
   ```bash
   HERDR_WEB_PORT=7811 HERDR_WEB_DIR=/tmp/herdr-web-dev HERDR_WEB_UPDATE_CHECK=false \
   HERDR_WEB_TRUST_LOOPBACK=1 HERDR_WEB_PUBLIC_PORT=0 HERDR_WEB_LAN_PORT=0 \
   HERDR_WEB_TLS=off go run ./cmd/herdr-web
   ```
   （`HERDR_WEB_DIR` 另给一个的理由：设备凭据和锁文件不要和常驻服务那份打架。后面那三个
   得**显式关掉**：`.env` 里配着公网口 27788 和局域网口 7790，而常驻那份正占着它们 ——
   不关的话调试实例在「端口已被占用」上直接起不来，而报错里说的是那两个口，跟你改的东西
   看着毫无关系。）
4. **公网只走公网口。** 主口（`HERDR_WEB_PORT`，默认 7788）在代码里就只服务本地网络：
   对端不是本机 / 私网 / 链路本地 / CGNAT 一律 403（`server.PrivateListener`）。要暴露就
   配 `HERDR_WEB_PUBLIC_PORT`，隧道的 `localPort` 指那个口。**不要把隧道改回指主口**，
   也不要为了「让外面能连上」去掉主口那道检查。
5. **动认证 / 端口 / 暴露形态之前先读**
   [DEPLOY.md](DEPLOY.md) 的「四个口，规则不一样」和
   [SECURITY.md](docs/dev/SECURITY.md) §8「「暴露」从声明改成端口分工」。
   那里写着为什么判据只能是「请求落在哪个监听上」，而不能是源 IP 或 `Host` 头 ——
   那两个在隧道后面都是伪造得出来的。

这几条最容易再踩一遍：

- `ctrl+u` 清空是 **2N−1 次**（N 行输入），固定次数只够两行；清不空就别投。
- `HERDR_WEB_SETTLE_MS` **别调成 0**，否则两次 `pane.read` 落在同一帧上，清空循环整体失效。
- 「本地草稿」要用单独的所有权标志判断，别拿文本比较推 —— 开着双向同步时会被自己覆盖。
- **发件箱是一行**（`web/src/components/Compose.tsx`：输入框 + 一个投稿键，`h-8`，和快捷键条上
  一个键一样高）。原来那四行（投给谁的下拉 / 一排按钮 / 状态行 / 三行 textarea）在手机竖屏上吃掉
  小半个屏幕，而那正是该看 agent 在写什么的时候（用户报的）。去掉的几件挪到了不占高度的地方：
  拉回成了动作库里的一件事（`pull`）、传图用顶栏那个 `img`、双向进了设置 →「终端」、投给谁只剩
  「跟随焦点」、状态进了 placeholder。三条别改回去：① **不自动长高** —— 高度一变就是 Dock 变高 →
  终端重排 → SIGWINCH + 冻帧（下面「改尺寸会闪一下全黑」那条），说一句话闪好几次；② **回车发送
  （`composeEnter`，默认开）必须挡输入法** —— 中文候选词就是按回车上屏的，`isComposing` 和
  `keyCode === 229` 两个判据都要（安卓上不少输入法只给后者），漏了就是「选个词把半句话投出去」；
  ③ **快捷键条 / 顶栏上那个 `↵` 也算回车**（走 `App.sendKeyBytes`：光秃秃一个 `\r` + 焦点在发件箱
  里 + 这一档开着 → 投稿）—— 不接这一条的表现是「点条上的 ↵ 把回车打进了 pane，而自己写的话还在
  框里」。详见 [OUTBOX.md](docs/dev/OUTBOX.md)。
- 抽输入框有三个坑：dim 占位、`38;2;...` 里的 `2` 不是 dim、空框是 `❯`+NBSP。
  `internal/composer/testdata/` 里是真机抓屏，改这块必须跑 `go test ./internal/composer/`。
- 提示（`internal/agentwatch/notice.go` + `extract.go`、`web/src/hooks/useNotices.ts`）：
  右上角那几张**卡默认不弹**（`noticeCard`，设置里开）—— 它是主动打断，盖住的正是 agent 刚写的
  那几行；红点（默认开）、系统通知、轮询各是各的，关卡片一个都不影响。**别把这个开关接到轮询
  或未读上**：红点和面板一览那列「几分钟前」都指着同一拍。下面这些讲的是开着时怎么弹。
  读屏抽话按状态分两套 —— blocked 抽屏幕**底下**那个问题块（`☐` / `╭…╰`），idle/done 抽
  最后一个**不是工具调用**的 `⏺` 块（带 `⎿` 的那种是干活的流水，不是它对你说的话）。
  `internal/agentwatch/testdata/` 是真机抓屏，改这块必须跑 `go test ./internal/agentwatch/`。
  另外**对底那条路不发提示**（herdr 停机后重连会补出一片「刚刚跑完了」，那是编时间）。
  还有两条是端到端才发现的（单测全绿）：herdr **只对看得见的 pane 推 `pane.updated`**，
  背景 workspace 里的 agent 干完活能 40 秒没有事件 —— 所以状态是「3 秒轮一次 `pane.list`
  保底 + 事件加速」；短任务 herdr **不报 `working`**（`idle → done` 直接过去），所以
  `done` / `blocked` 一律弹，别再要求「必须从 working 来」。还有：**投了又按 Esc 取消**在
  herdr 那边就是干净的 `working → idle`，屏幕上也不留「被打断」的记号 —— 只能靠「抽出来的
  话和上次一模一样」认出来（`lastText`），别把这条去重当成可有可无的优化删掉。还有
  **重连时 herdr 会把 pane 当前状态重新推一遍**（事件里没有 `state_change_seq`，只有
  `revision`），只看「和上次记的不一样」会把补推当成刚发生 —— 判据是单独问一次
  `agent.list` 看那个全局计数动没动（`decided`）。以及**同一件事只说一次**：状态识别会抖、
  你打字时屏幕又一直变，所以「同一类提示（等你回答 / 跑完了）在它重新开过工之前不再弹」
  （`lastClass` + `worked`）。
- 能做哪几件事，**只有一份清单**（`internal/capability` + `web/src/capabilities.tsx`）：以前是
  三份平行的（`topbar.Actions`/`Pinned`、softkeys 的 `acts`、App 里那个 panel 枚举），而它们是
  同一件事的三个切面。「能出现在哪些界面上」是那一行里的字段（`Topbar`/`Key`/`Panel`/`Pinned`），
  加一件事 = Go 一行 + TS 一行。四条：① 两边**顺序和 `key` 标记必须一字不差**，
  `capability.TestMatchJS` 盯着（它连 act 白名单一起盯 —— 那一处原来两边各写一份，对不上时
  是**完全静默**的：键点下去什么都不发生）；② TS 那边 `KeyAct` / `PanelId` 是**从表里 `Extract`
  推出来的**，别再手写第二份联合类型；③ **`act` 必须是能上顶栏的 id 的子集**（测试盯着），
  因为前端拿 act 直接查顶栏那张动作表（`topbarAct`）—— 破了就是「点了没反应且不报错」；
  ④ 顶栏编辑器的「库」= **服务端那份 ∩ 前端画得出的**（读 GET 回来的 `actions`），别只铺前端
  那份 —— 不一致时用户能把一个存不进去的按钮拖上去，一保存报「不认识的按钮」。
  「点了干什么」不在清单里，在 App 的 `topbarAct`（要用 App 的状态和 Session）；快捷键条把它
  整张递进去用（`act={(a) => topbarAct[a]}`），**别再写第二份 act→动作的映射**。
- 键上的内置图标（`internal/softkeys/icons.go` + `web/src/keyicons.tsx`）：`Key.Icon` 决定
  **条上画什么**，`Key.Label` 照旧是**名字**（编辑器认它、组键靠它、title 里显示它）——
  两者不是二选一。为什么要这一档：`⌨` 这类字形在很多字体里压根缺（显示成方框，index.css 里
  那串符号字体兜底就是为它加的）、有的字体里很难看、大小和基线跟旁边字母对不齐，SVG 三个
  问题一起没了。三条：① 两边清单**一字不差、顺序一样**（`TestIconsMatchJS`）；② **存盘严格**
  （不认识的 id 报错）、**读盘丢掉**（当没挑，退回画文字 —— 从新版本降级回来的文件里会有
  不认识的 id，整份退回出厂太贵而 Label 一直在）；③ 出厂那份给 `⌨` / `↵` 配了图标，但
  **Label 一个字没动** —— `sigOf` 认它（「恢复默认」的去重）、快照测试也比它。
- 看 diff（`internal/gitdiff` + `web/src/components/DiffPanel.tsx` / `DiffViewer.tsx`）：这一层
  存在的理由是**终端里那份在手机上读不了**（长行不能折、一整行红配一整行绿看不出改了哪个词、
  翻页要靠 pager），所以那三件事就是它的全部功能，别往里加第四件 —— **它是只读的**，
  没有 add / commit / checkout（会改仓库的事在终端里做）。跑 git 那边八条**静默**出错的写在
  包注释里，最容易再踩的三条：① 用户 `~/.gitconfig` 里 `color.ui = always` 会让每行前面多一串
  ANSI，`+`/`-` 前缀于是认不出来，**整份补丁被解析成一片上下文**（所以每条命令都带
  `-c color.ui=false`）；② `git diff --no-index`（未跟踪的新文件走这条）**有差异时退出码是 1**，
  当成失败的表现是「新建的文件永远打不开」，而且**它那两个参数是任意路径**，必须自己夹在仓库
  根里（`gitdiff.Join`，`TestDiffUntrackedEscape` 盯着）；③ `--no-optional-locks` 不能去掉 ——
  `git status` 默认会写 index、抢 `.git/index.lock`，而对面正有 agent 在同一个仓库里跑 git，
  失败落在**它**头上，这边一点症状都没有。另外 `-z` 的 numstat 里**改名占三条记录**（空路径 +
  旧 + 新），少认一条从那儿开始后面所有数字错位。词高亮是**按位置配对**（第 n 条删配第 n 条加），
  两道门槛在 `pairWords` 里 —— 第一版还要求两边条数相等，正好把最常见的「改两行顺手加一行」
  挡掉了。前端那边：折行是 `diffWrap` 这个 pref（跟着排布走），不折行时行号槽要 `sticky left-0`
  且每行都得有**实底色**（靠 `bg-inherit` 遮住底下滑过去的正文）。
- 补丁页是**一条连续的流**（`DiffViewer`）：一次改动的全部文件顺着滑，不用退回清单再点一次
  （用户报的「多个文件改动没办法一次性看完」）。没滚到的先按 `+n −m` 占个高度，滚到跟前才读，
  **一次只读一个**（对面那台机器上正跑着 agent，别一口气 fork 十几个 git）。四条都是真机上
  踩出来的：① **别把「该读哪几个」和「滚到哪个文件了」建在 IntersectionObserver / rAF 上** ——
  它俩在后台标签页里**完全不跑**（这条坑下面「几个坑」那节早写着，这次是在同一个坑里又验了一遍：
  用浏览器自动化测时那个标签页就是 hidden 的，观察器一次都没回调），改成滚动事件 + 80ms 时间
  节流扫一趟，两件事一次算完；② 那个读队列的 effect **绝不能返回 cleanup 去掐掉在途请求** ——
  它的依赖里有「滚到哪儿了」，每滚一段就重跑一次，写下去的表现是每个请求都在半路被自己人掐掉，
  **一个文件都读不出来**（屏幕上全是「读取中…」）；③ 队列要靠**干完一件自己踢下一件**
  （`pulse`），别指望依赖恰好再变一次 —— 人停在那儿不滚时依赖不会变，队列就停住了；
  ④ 换上真内容时如果那一块**整个在视口上面**，它长高会把正在读的地方往下顶，所以替换前记一下
  位置、替换后把差值补回 `scrollTop`（Safari 不支持 `overflow-anchor`，指望不上浏览器）。
  「在不在上面」要和**滚动容器的上边**比，不是和窗口顶上比 —— 上面还有一条 48px 的顶栏，
  拿 0 当界的话「刚好被顶栏挡住的那一块」会被判成「还在视口里」而不补；那些待补的还得**排成
  队列**（两块内容会落在同一次渲染里，一个格子的话后来的把先来的挤掉，少补一截）。
- 顶栏「改动」上那个**绿点**（`hooks/useGitDirty` + `gitdiff.DirtyStat`）：判据是「有**你还没
  看过的**改动」，不是「有改动」—— 后者在一个正干活的仓库里永远为真，那个点就焊在图标上了
  （提示红点那边是同一条道理）。所以服务端给一个**指纹**（`sig`），前端和本地记的「上次看过的」
  比，开一次面板就算看过。三条：① 指纹里必须带 `git diff --shortstat`——
  `status --porcelain=v2` 里那两个 blob 哈希是 HEAD 和 index 的，agent 往一个**已经改过**的
  文件里再写十行那份输出一个字都不变，只看它的话点永远不会再亮；② 这是**在对面那台机器上按秒
  跑 git**（那台机器正跑着 agent），所以 12 秒一拍、**页面不可见时不问**、服务端还压了 2 秒缓存，
  而且**跟着「红点」那个开关一起关**（不要点的人不该为轮询买单）；③ 颜色是**绿**不是红 ——
  红在这个界面上是「有 agent 在等你」，改动不是坏事；这条路真出问题时是**安静地不画角标**
  （探测失败不弹任何东西）。盯的是**面板里正看的那个仓库**，面板没开过就用焦点 pane 的 cwd
  （服务端自己从任意子目录找到仓库根），而且**换工作空间就作废、退回默认** —— 面板关着的时候
  没人来更新它，不作废的话切到另一个项目上，角标还在说上一个项目（同 `diffRepo` 那条注释）。
  不是「所有仓库一起盯」：一台机器上开着几十个 pane 是常态（实测 34 个不同 cwd），一拍几十次
  git 换一个小点不值当。
- **chat 模式**（`internal/transcript` + `internal/server/chatapi.go` +
  `web/src/components/ChatPanel.tsx`）：内容来自 **agent 自己写在磁盘上的会话记录**，不读屏
  （读屏是这个项目最贵的一条路）。落盘粒度是**一次 API 请求**不是逐 token，所以体感是几秒
  冒出一块 —— 想要逐字的回终端。定位靠 herdr 的 `agent_session`（`herdr integration install
  claude|codex` 装的 hook 报的），八条静默的：① **hook 只在 `SessionStart` 报一次** ——
  装之前已经跑着的 agent 一律没有（实测 19 个 agent pane 全是 `null`），表现是「装完了还说没有
  会话」，界面上必须说「把 agent 重开一次」；② herdr 给的 `kind` **实测是 `id` 不是 `path`**
  （虽然 hook 两个都报了，codex 那个压根不报 path），所以 id → 路径这一步只能自己做；
  ③ **别照抄 claude 那个目录名变形规则**（`/` 和点号都折成 `-`）—— 规则是别人的，错一个字符
  就是「chat 永远打不开」，判据用 session id 全机唯一（拼不中就 glob 一层，45 个目录代价可忽略），
  codex 的 id 就在文件名里；④ **同一个 cwd + 同一个 agent 有多个 pane 就不猜**（`ambiguous`）——
  猜错是「显示的是隔壁那个 pane 的对话」，而两边都在同一个项目里干活，屏幕上看着完全正常
  （`herdr-web` 这个目录下就常开着两个）；⑤ 首屏那个尾部窗口，「够不够」的门槛**不能是 200 条**
  —— 踩过：2.3MB 的转录头一窗出了 181 条判成不够，一路翻倍**把整个文件读完**，正是这个窗口
  要避免的事（门槛是 30 条）；⑥ 增量的 `next` **只能停在整行的边界** —— 文件正在被写时最后一行
  可能只落了一半，算进去的话下次从半行中间接着读，**那一条消息从此永远丢了**且不报错；
  ⑦ codex 读 `event_msg`/`item_completed` **不读 `response_item`**（后者混着 55KB 的 developer
  系统提示和只有 `encrypted_content` 的 reasoning），而 `AgentMessage` 里 content 的 type 是
  大写 `Text`、`UserMessage` 里是小写 `text`（**按小写比**，漏了就是 agent 说的话一条都认不出来）；
  ⑧ **边界这一层自己钉**，不借文件浏览那一处（转录在 `~/.claude` / `~/.codex` 下，`FILE_ROOTS`
  一配就挡在外面）—— 义务是「id 过正则 + path 必须在根底下 + 必须 `.jsonl`」三条，少了最后那条
  一个被改过的 hook 报 `~/.claude/.credentials.json` 就能把凭据渲染到页面上。**只读**：发言走
  现成的发件箱，审批不留入口（会改状态的事留在 TUI）。
  **codex 的 `/clear`**：不新建 rollout、旧文件也不再长，而 herdr 的 `agent_session` 要等
  新会话写盘才更新（hook 挂在 `SessionStart` 上，codex 的 clear 不触发它）—— 那段窗口里
  chat 显示的是**已经作废的上一段对话**（用户报的），而数据层认不出这件事（没有结束标记、
  `~/.codex` 下也没有「当前活跃会话」的记录）。**所以不猜**：状态行上多一句「X 分钟没更新」
  （`Turn.Idle`，服务端算）让人自己判断，说下一句它就跟到新会话上了。
  **替人答那个选择框**（`chatapi.go` 的 `askKeys`）还有一条**完全静默**的：带 `preview` 的题
  在 TUI 里是**左右分栏**，数字键**只把光标移过去**，要再补一下 `enter` 才算选中并跳题；
  不带 preview 的单选题按数字就直接选中 + 跳题。漏了那一下的表现是「界面说提交成功、终端里
  什么反应都没有，停在第一个问题」（用户报的）—— 因为那串键从那一题起全变成在同一题里挪光标，
  一题都没提交。当初量协议用的是「一道多选 + 一道单选」都没有 preview，而 **preview 只在单选
  题上有**，正好和「单选发个序号就完事」撞在一起。判据看屏幕最下那行提示：带 preview 的是
  `↑/↓ to navigate · n to add notes`，普通题是 `Tab/Arrow keys to navigate`。
  界面上还有五条都是真机报出来的：⑨ **`more`/`start` 只能从「整份读」那次采纳** —— 增量那次
  对顶端一无所知，每拍都盖的表现是「看更早的」那个按钮活不过一拍就消失（和「翻不到历史」
  长得一样）；⑩ **偏移要跟着 `sig` 一起核** —— `/clear` 之后是另一个文件，旧偏移套上去就从
  中间某处开始读，前面那截永远读不到；⑪ **往前面插内容要把长高的那截补回 `scrollTop`**
  （Safari 没有 `overflow-anchor`，和 DiffViewer 同一条）；⑫ **容器一变矮要重新贴底**
  （ResizeObserver）—— 手机呼输入法时 `clientHeight` 变小而 `scrollTop` 不动，那一下
  **不触发 scroll 事件**，光靠 onScroll 发现不了（用户报的「弹出输入法会导致自动贴底不生效」）；
  ⑬ **回到底部要瞬时跳不要 smooth** —— 那一跳常常五千多像素，动画期间「贴底了」和 scroll
  事件算出来的对不上账，药丸会闪。还有 **Markdown 永远不开原始 HTML**（agent 输出在这一层
  是不可信文本，而这个页面能调 `/api/herdr/say`），中文加粗靠 `remark-cjk-friendly`
  （CommonMark 的 flanking 规则让紧贴中文标点的 `**` 不算强调符，不接就是满屏星号）。
  详见 [CHAT.md](docs/dev/CHAT.md) §9。
- **文件查看器能改文本 + 渲染 md**（`files.WriteText` + `POST /api/files/save`、`FileViewer.tsx`）：
  只有点「保存」才写盘（**故意不自动保存**，对面的 agent 也在写这些文件），保存带着打开时的
  `mtime`，对不上回 409 让人挑「重新载入 / 照样覆盖」—— 别去掉这道，去掉就是人在手机上改一个字
  把 agent 几分钟的输出整份盖掉、一个字都不报。写法是同目录临时文件 + rename，**先解开符号链接**
  （不然链接本身被换成普通文件），权限位照抄；截断过的（> `MaxText`）不给存。md 用 chat 那个
  `ChatMarkdown` 的 `variant="doc"`（chat 气泡那套标题只大 8%，排整份文档时标题和正文分不开）。
  一条**静默**的：markdown 链接的 href 到手时**已经被 micromark 百分号编码了**（CommonMark 的
  `normalizeUri`），`[稿.md](/Users/x/旁白稿.md)` 进来是 `/Users/x/%E6%97%81…`，不解码就是
  「带中文 / 空格的文件永远找不到」（`localPath`）；正文里拆出来的 `herdr-path:` 那条没被编码，
  别解（会弄坏文件名里真的 `%`）。
- **底部面板能挪到右边**（`Dock` 的 `useDockSide` + 面板上那个按钮，横竖屏各存一份，手机竖屏
  钉在底下、不出按钮）。两条跟着来的：① 面板（`ui/panel.tsx`）的宽度断点看的是**视口**，
  而它压在 `main` 里 —— 所以还夹了一道 `max-w-[calc(100%-20px)]`，不然横屏手机上贴右的面板
  左边溢出屏幕；② 快捷键条放右边时每行要**横滑**（`Softkeys` 的 `slide`），按宽屏那档换行的话
  两排会折成三四排。
- **「我现在在哪个项目」= 焦点 pane 那个工作空间**（`DiffPanel` 的 `dirKey`、`FilesPanel` 的
  `starts`、App 里那个 `diffRepo`）。用户报的是「改动面板打开，看到的是另一个项目的 diff」。
  三处是同一件事：herdr 里同时开着好几个工作空间、几十个 pane 是常态（实测 48 个 pane / 34 个
  不同 cwd），把所有 pane 的 cwd 一锅端去问「哪些是 git 仓库」，默认落在哪个上面全看顺序。
  「同一个工作空间」认的是 **`workspaceId`（herdr 的 `workspace_id`）不是 `workspace`** ——
  后者是给人看的标签，而两个工作空间同名是常态（标签多半就是目录名），按它分组就等于没夹。
  所以**改动面板的候选只有当前工作空间那几个 cwd**（于是下拉框的语义变实了 —— 它出现 = 这个
  工作空间下真有好几个 git 项目，这正是要的「多个项目才给筛选」），**文件面板的起点把当前工作
  空间那几条提到最前面**（第一条是焦点 pane 自己）。但**「当前」那个标只给焦点 pane 那一条** ——
  第一版按工作空间标，于是一个工作空间里有几个 cwd 就亮几个「当前」（用户报的「我正在看一个
  目录的 pane，只应该有一个当前」）：这个标回答「我现在在哪儿」，那是一个 pane 的事，标成一片
  就等于没回答。去重按路径、焦点那个先加，所以同一个目录上开着好几个 pane 时标落在焦点那条上。
  两条**别加回来**：① 「上次看的那个仓库」不再丢进候选里问 ——
  它当初排第二是为了绕服务端 32 个的上限（`/git/repos` 只认前 32 个 `dir=`，排最后会被切掉），
  而夹到一个工作空间之后候选只有几个，留着它的代价正是那个 bug：那个仓库永远在候选里，于是
  「人挑过的那个还在就留着」永远命中，换到别的项目上也不换；② 面板打开时**不从 localStorage
  恢复仓库** —— 同一条毛病，还白跑一次别的仓库的 `git status`（那是在对面那台机器上 fork git）。
- 「落在哪个文件上」这件事**光靠一次 `scrollIntoView` 是落不准的**（用户报的「点第二个文件进去，
  过一会看的却是第一个」），两条一起才行：① **末尾那块空白要按需撑够**（`fitTail`）——
  点最后一个文件时它下面只剩几十像素、滚动条已经到底，浏览器只能把它停在半屏以下，屏幕上大半
  还是上一个文件的尾巴；② **锚要盯到这一阵读完**（`anchor`）—— 只在「它自己读回来」时对齐一次
  是不够的，周围那几块紧接着读回来还会改上面那截的高度，而①那条补偿逐块补总会差一点点
  （估高、亚像素、两头被夹住），差的会攒起来：实测「点第 8 个」稳定落在它上面 300px。
  所以队列里还有它周围没读完的就别松锚，每次内容一变就重新对齐；**人自己一滚就松手**
  （`selfScroll` 分得开「我们自己弄的滚动」和「人在滚」），不然会跟人抢。
  顺带：读队列**从正看着的那个开始由近及远**，不是从上往下 —— 不然点第 8 个文件时，人已经盯着
  第 8 个在等，队列还在从第 1 个慢慢读。
- **PWA 的「安装」那一档靠一个带 `fetch` 处理器的 SW 撑着**（`web/public/sw.js` +
  `web/src/lib/sw.ts`）：Chromium 把「安装」和「创建快捷方式」分成两档，后者只是个开浏览器
  的书签。给不给前者要三条一起成立：manifest（一直有）、**证书有效的 https**（自签点过
  「继续访问」不算 —— 局域网直连那条路装不了）、**页面上有一个带 `fetch` 处理器的 SW**。
  原来缺的是第三条，而且缺两层：SW 只在「打开系统通知」那一下才注册（绝大多数人身上没有），
  且它只听 `notificationclick`。四条别改回去：① **进页面就注册**（`registerSWWhenIdle`，
  挂 `load` 之后，别去抢首屏那几个连接）；② `fetch` **只接管导航请求且 network-first** ——
  原来那条「终端页面挂个会拦请求的 SW 只会制造『改了却没生效』」仍然成立，只管导航 + 永远
  先走网络才保住它，别顺手加资源缓存；③ 预缓存的是**独立的 `offline.html`**，不是
  index.html（后者引带哈希的 assets，离线打开只会白屏），而那张页**不能有任何脚本**
  （CSP `default-src 'self'` 会挡掉内联 script 和 `onclick`，表现是「按钮点了没反应」），
  所以「重试」是个 `<a href="/">`；④ maskable 图标是**第三份**（Android 的遮罩只保证中心
  直径 80% 的圆），由 `assets/make-logo.py` 出，别手改 png。**`fetch` 监听器删掉是完全
  静默的**：页面一切正常，只是「安装」那一档当场消失。验的时候记住 `beforeinstallprompt`
  在加载早期就触发，**事后挂 listener 抓不到**，会得出「不可安装」这个错结论。
  还有一条跟着来的：**装成 app 之后「呼出键盘自动全屏」要自己停用**（`enterFull` 的 `auto`
  参数 + `install.isStandalone()`）—— standalone 下地址栏本来就没有，再请求全屏一格都拿不回来，
  赔的却是每弹一次键盘一次重排 + Android 那条系统提示。闸开在 `enterFull` 里而不是三个调用点上：
  那三条路读的东西不一样（两条读 state，`onKeyboardChange` 直接读 localStorage 镜像），拦不齐。
  **顶栏那个全屏按钮不受影响**（standalone 下它还能吃掉状态栏），而设置里那一档必须**显式
  disabled 并说明**，光在底下悄悄不执行就是「开关点了没反应」。
- passkey 那条路上有一条**静默**的：**`NotAllowedError` 不等于「用户取消了」**。WebAuthn
  规范故意让几乎所有失败都报同一个错（不让网页试探「这台设备上有没有某把 passkey」），
  所以「人划掉了」和「浏览器因为这个页面证书被跳过过而不肯做」在错误对象上一模一样 ——
  「取消不弹红字」写成「凡是 NotAllowedError 都吞」的表现就是**点了什么都不发生、一个字
  都不报**（用户报的）。唯一分得开的是**时间**：弹了面板再让人划掉最少大半秒，被策略挡掉
  是当场就回，所以 700ms 内回来的不当取消（`web/src/lib/passkey.ts` 的 `ask()`）。另外
  `HERDR_WEB_TLS=proxy` 时证书是前面那一层的事，前面那层证书不对的话域名 / RP ID / secure
  context 全对也按不动 —— 那时能进的路是配对码。详见 [SECURITY.md](docs/dev/SECURITY.md) §L2(c)。
- 键上的图标（`internal/softkeys/icons.go` ↔ `web/src/keyicons.tsx`，两边一字不差有测试）
  和**按键样式**（`keyStyle` pref，`solid` / `plain`）、**弹窗透明度**（`popupClear` pref，
  存的是**透明度**、0 = 不透明、出厂 60；CSS 那边取 `100 - clear`，反过来就和界面上写的
  正好相反。加在**整片浮窗**上而不是只调底色 —— 只透底色的话键还是一块块实的，挡掉的面积
  没少多少）。三条是真机反馈换来的：
  ① **尺寸 16px + 描边 1.75**：15px 糊（lucide `Keyboard` 内部八九个小点粘成一团）、18px
  又比旁边 13px 的字明显大一号（「方向按键也太大了吧」）；② **键盘和方向自己画**，不用
  lucide 那两个 —— 内部标记用**填充**不用细描边（填充缩到 16px 还是实的，1px 描边缩下去就
  剩一层灰），而且要**占框七成**（第一版画得太收，看着像一撮装饰点）；③ `plain` 那一档
  **只去掉静息态的底和边** —— `on`（粘滞键亮着、面板开着）和二次确认举起来那一下照旧涂满，
  那是「按下去了必须一眼看见」的状态（配色那节），全不给底就分不出哪个亮着。
- 出厂默认条上**方向键只占一格**（`softkeys.DefaultBar` + `defaultArrows`）：它们是「方向」
  那个弹出组的成员。`Defaults()` 是「有哪些定义」，`DefaultBar()` 是「条上放哪几个」——
  两者不再相等，两个建配置的入口（`DefaultConfig` 从零 / `factory` 恢复默认）共用
  `wireDefaults`，各写一遍的话「出厂长什么样」就有两个版本，而「文件坏了」和「恢复默认」
  走的偏偏是不同那一个。另外 `sigOf` 认组时**不带列数** —— 用户把自己那个「方向」组改成
  4 列之后，「恢复默认」不该因为认不出来又补一个同名的进去。
- 弹出组（`Key.Group` + `web/src/components/KeyGroupPopup.tsx`）：一个键在条上**只占一格**，
  点开浮出一小片键（方向键就该是这个 —— 摊在条上要 3×2 六格，手机竖屏上是半条屏幕）。
  五条：① **浮窗是 `fixed` 的，条一点都不重排** —— 内联展开会改 dock 高度，而那会一路触发
  终端重算行列 + SIGWINCH + 冻帧（「改尺寸会闪一下全黑」那条），点一下方向键闪一次屏；
  ② 视口一变（呼输入法、转屏）**重新定位，不关掉** —— 写成「resize 就关」是错的：Android 上
  点一下就可能把输入法顶回来，浮窗刚开就自己关，那台机器上等于没这功能；③ 判「点到外面了」
  只认 `pointerdown`（`click` 在触屏上会丢：浮层在 pointerup 里被卸掉，touch 的 target 钉在
  已脱离文档的元素上，**不冒泡到 document**）；④ **组里不能再放组**（`resolveConfig` 挡，读盘
  丢那一格、存盘报错）；⑤ 它是 Lib 里的**定义**不是 lane 上的排布 —— 所以它就是个普通键，
  能上条、能被钉住、也能上顶栏，而成员键的渲染**复用调用方那一份 `renderKey`**（发字节 /
  粘滞 / act / 两次确认一处都别重写）。
- **粘滞修饰键（Ctrl / Alt）是三档**（`term/session.ts` 的 `StickyMode` + `lib/sticky.ts`）：
  关 → **一次性**（下一个键带上它，发完自动松开）→ **锁住**（一直带着，再点一下关），点一下
  走一档。第三档是用户点名的：`Ctrl+C` 常常要连按好几下打断一个不听话的进程，而一次性那档
  每发一次就松开 —— 手机上等于每按一次 C 都要先去点一次 Ctrl，两个键还离得远。三条：
  ① **默认仍是一次性** —— 绝大多数组合只发一次，自动松开才不会把后面敲的字全变成控制字符
  （「锁住了忘关」是这类键最常见的翻车）；② 锁住的记号是**键底下一条小横杠**（`LOCK_CLS`），
  **不改文字也不改尺寸** —— 键上加字会让按钮变宽，手指底下的键当场挪位置（和「举起来那一下
  不换文字」同一条）；③ 样式和 title **顶栏和快捷键条共用一份**（`lib/sticky.ts`）：顶栏上
  也能放「我的按键」里的 Ctrl，各写一份迟早分家，而这是**状态**不是装饰 —— 分不出「一次性」
  和「锁住」，人就会在锁着的时候接着打字。
- **方向键按住不放会连发**（`web/src/hooks/useHold.ts`，快捷键条和顶栏**共用这一份**，
  和 `useArm` 同理）：按住 400ms 起，**速度是个跟着套走的开关**（`holdRate`，16/8/4/2/1 次每秒，
  默认 16；在 `pointerdown` 里现读镜像，所以改完下一次按住就生效，不用一路传进条和顶栏）。四条：① **一下点照旧走 `click`** ——
  条是横滑的，改成「按下就发」的话每划一下都白发一个方向键（变成滑动时靠 `pointercancel`，
  条短到不滚动时不发，所以还自己量 12px 位移）；② **连发过要吞掉随后那一下 `click`**，
  不然「按住走了 8 格」最后会多走一格；③ **不能连发的键也要挂那几个 handler**（`bind(null)`，
  按下顺手清掉「刚连发过」的标记）—— 只给能连发的挂，会出「按住 ← 在键外面松手、接着点 ↵
  没反应」；④ 有**保险丝**，数的是**下数**不是时长（400 下）—— 按时长卡的话「1 次/秒」那一档
  20 秒就只剩 20 下，而那正是慢档的用法；要它是因为 touch 的 target 在按下那刻就钉死，元素中途
  被卸掉时松手那一下**不冒泡到 document**，收不到就是「键卡住了一直在发」。**能连发的按字节认**
  （`\x1b[A` 那几个 + PgUp/PgDn），不是新加配置项 —— 用户自己配的 `↑` 也该有这一档；代价是
  两边对不上时**完全静默**，`TestHoldKeysMatchJS` 拿服务端那张 `named` 表盯着。回车 / `⌃C` /
  退格有意不连发（多发一次就是多跑一条命令、或者吃掉一整行，终端里没有撤销）。
- **条上「敲一串字 + 一个回车」那种键（`text:/clear enter`、`"herdr" enter`），回车要
  隔 200ms 再发**（`web/src/term/keysend.ts` 的 `splitEnter` → `{t:'i',gap}` → 服务端
  `internal/server/pty.go` 里那一觉）：codex 的输入框有一道「这是不是粘进来的」判据
  （`paste_burst.rs`：连着 3 个以上字符、每个间隔不到 8ms 就算粘贴），于是紧跟着的那个回车
  按**换行**处理 —— 屏幕上 `/clear` 好端端躺在输入框里，命令根本没提交（用户报的）。
  claude code 没有这一道，所以这个坑**只在换了 agent 之后才现形**，而且一个字都不报。
  实测（codex 0.155.1 真跑一个 pty 灌字节）：间隔 10ms 还是只换行，20ms 起才提交；取 200ms
  是照着源码里那个 120ms 的抑制窗口留的余量。三条别改：① 门槛是「回车前面有 **3 个看得见的
  字**」，对着 codex 那个 `PASTE_BURST_MIN_CHARS` —— 顺带它把 `alt+enter` 挡在外面（字节就是
  `\x1b\r`，切开就成了「Esc + 200ms + 回车」，意思全变）；② **等在服务端**，不是前端
  setTimeout：前端背靠背发的两帧走同一条 TCP 连接，第一帧卡在重传里时第二帧跟着一起到，
  间隔当场被挤没，而这个间隔恰恰是给对面那个 TUI 看的；③ 别改成 bracketed paste 包一层 ——
  那得知道对面那个 pane 此刻开没开 DEC 2004，而这一层是往 herdr 的 PTY 里灌原始字节的，
  看不到（发件箱那条路能，它走 herdr 的 `agent.prompt`），猜错就是把 `[200~` 打进人家命令行。
- 快捷键条的宽度和钉住（`internal/softkeys`）：**宽度按内容自适应，没有「占几格」这回事。**
  这条是走过弯路才想清的 —— 以前有 `Span`（1..3 格）+ `Wide`，理由写的是「整数格才谈得上
  跨行对齐」，可同一份注释里还写着「两行各自横滑」，两条直接矛盾（滑一下就不齐了）。当时是
  拿「固定块」（不滚动的对齐网格）圆的，固定块删掉之后这个理由就空了，于是整套 span 一起
  删掉。**格子只在不滚动的地方有意义** —— 现在只剩弹出组的浮窗，`--sk-w` 因此只有两个用途：
  可点的最小宽 + 浮窗网格的列宽。老文件里的 `span` / `wide` 读进来忽略，也不再写出去。
  「按内容」这件事得靠**每个键 `shrink-0`** 托着：键上那个 `min-w:--sk-w` 会顶掉 flex 的
  自动最小尺寸（只有 `min-width:auto` 才是「不小于内容」），漏了就是条挤不下时每个键都被
  压到 36px，而字是 nowrap 的 —— `/clear` 这种长键的字漏到边框外面（浮窗那边同理，列宽
  要 `minmax(var(--sk-w), auto)`）。
  **钉住**（`Pin{Left,Right}`）是「这一行头几个 / 尾几个不跟着横滑」。三条：① 存的是**个数**
  不是另一份列表，`Bar` 照旧是那一行的**完整顺序** —— 只认 Bar 的老版本读出来是同样那些键
  （只是全都跟着滑），不会「钉住的那几个不见了」；② 代价是个数会随行长失效（别的设备删了
  定义 → 行变短），所以**读的时候夹住**（`resolvePin`：非负、`Left+Right ≤` 行长、超了左边
  优先）——**不报错**，那不是谁的 bug；③ 渲染上一行是三段 flex，钉住那两段 `shrink-0`。
- 排布分套（`internal/profiles` + `web/src/lib/prefs.ts`）：**定义全局、排布分套**。快捷键条的
  「我的按键」是所有套共用一份（改一个按键谱两边一起变），`rows`/`bar` 和顶栏 `items` 每套一份。
  由此来的三条：① 删一个定义要把**所有套**条上的引用一起清掉（`Save` 里 prune），读的时候丢掉
  认不出的引用而**不是**整份退回出厂 —— 那会把没坏的一半也抹了；②「恢复默认」只动这一套的条，
  绝不整份恢复出厂（定义全局，那样会把别的套的键抹掉）；③ `softkeys.json` / `topbar.json` 里
  默认那一套要**镜像到顶层老字段**，不然降级回老版本看到的是「配置自己没了」。
  **「我的按键」现在有两个界面**：顶栏 `items` 里除了内置按钮的 id 还能放 `key:<定义ID>`
  （`topbar.KeyPrefix`）—— 动作库只有一份，「顶栏上能不能加个 ctrl+b z」不用每次动白名单。
  由此三条：① 存盘严格（`Store.Keys` 钩子核定义在不在）、**读盘不核** —— 核的话一次
  softkeys.json 读失败就能把人家配好的键从顶栏抹掉，认不出的引用交给前端渲染时丢；
  ② 所以①里那条 prune 也要管顶栏（`topbar.PruneKeys`，挂在**快捷键条那个口**上，因为两个包
  互不 import，线接在 `server.New`），漏了就是顶栏上一个画不出来的幽灵项占着名额；
  ③ `softkeys` 的 `act` 白名单（kbd/img/panes/files/clip/paste）是 `TopbarId` 的**子集**，
  所以顶栏上那种键的 act 直接走 `topbarAct`，别写第二份映射。
  绑定认的是前端生成的 `installId`，**不是 auth 的设备 ID**（本机直连压根没有那个）；
  设备类别只在「第一次来还没绑」时猜一次，**绝不按屏幕宽度自动切**。
  **排布在本地还留一份镜像**（`web/src/lib/layoutcache.ts`）：那两个 GET 排在
  `whoami → state → profiles/hello` 后面，第一帧只能画前端那份出厂顺序，响应回来整条顶栏
  跳一下 —— 用户报的「刷新页面顶部始终闪动」。三条：① 它**只当初值用**，服务端那份回来照旧
  整份盖上去（一样的话渲染出的 DOM 也一样，屏幕上什么都不动），别把任何判断建在镜像上；
  ② 按 `installId` 存，对不上就整份作废 —— 局域网直连那一跳会采纳另一个 install，那时镜像
  里是**别的浏览器**那套排布；③ 只存渲染要的（`lib`/`bar`/`pin`/顶栏那串 id），`presets`
  那几十 KB 不进去（编辑器打开时本来就要现拉一次）。
  跟着套走的开关 = 设置里「终端」那一整页（白名单在 `profiles.Prefs`，前端那份在
  `lib/prefs.ts`，两边**一字不差、顺序也一样**，有测试盯着）。加一项就是两边各加一行 +
  `applyProfiles` 里刷一下 state。模型是**服务端为准 + localStorage 镜像**：读的地方一律
  照旧读镜像（终端回调里有几处是同步读的），别改成读 state。
- 局域网直连（`internal/lan`、`internal/server/lanapi.go`、`web/src/hooks/useLanDirect.ts`）：
  从隧道进来的页面嗅探「能不能直连」再切过去。四条都是**静默出错**的：① 局域网那个口必须是
  **TLS** —— https 页面对 `http://` 目标的 fetch 算 active mixed content，浏览器无条件拦死
  （`no-cors` 也拦），明文口连嗅探都发不出去；② 那些 origin 必须进 **CSP 的 `connect-src`**，
  不放行的话是被**自己的 CSP** 挡掉，而控制台里那条错和「连不上」长得一样；③ 嗅探只能用
  `mode:'no-cors'`（普通 fetch 会因为没有 CORS 头 reject，于是把「通」也当成「不通」），它能
  分清「有响应」和「连不上 / 证书不认」，而这正好是要的全部信息；④ 候选**每次现报**，别缓存 ——
  内网 IP 会变，而且虚拟网卡（`Addr.Virtual`）要滤掉，手机碰不到 bridge/utun 上那些地址。
  凭据那一侧还有两条：⑤ 换 origin 要带凭据过去，用的是**交接令牌**（`auth.MintHandoff`）
  而**不是配对码** —— 配对码「只有坐在机器前的人能出」是写进 SECURITY.md 的性质（理由是
  它创造一份不随创造者被撤销的凭据），图省事调 `MintCode()` 就是把那条禁令本身实现了一遍；
  ⑥ 判「是不是从直连口进来的」只能按**请求落在哪个监听上**算（`server.FromLan`），**不能看
  `Host`**（`hostOK` 对 IP 一律放行，公网那条路伪造一个内网 Host 就绕过去了），而且那个口
  必须自己拒掉非本地对端（`lan.PeerIsLocal`）—— 「绑通配地址只有局域网碰得到」是拓扑假设，
  Go 对 `0.0.0.0` 开的是**双栈**套接字，有全局 IPv6 的机器上那个口公网可能直接可达。
  另外「探不通」有两种原因，**必须分开报**：在外面（安静走公网）和地址变了（旧 origin 上那一下
  「继续访问」作废了，要提示人去新地址再点一次）—— 混成一种的话这条路会永久静默失效且查不出原因。
- 触屏那一层（`web/src/term/touch.ts`、`web/src/lib/tap.ts`、面板里的行）有两条**方法上**的
  硬规矩，都是用真机报的 bug 换来的：
  ① **别用合成事件验「浏览器补发的兼容鼠标事件」那一类问题** —— 合成的 `dispatchEvent` 不会派生
  兼容事件，手动补一个 `touchend` 就正好把 bug 藏住（真机上那一下恰恰是唯一必然丢失的事件：
  浮层在 `pointerup` 里被卸掉，touch 事件的 target 钉在已脱离文档的元素上，于是**不冒泡到
  document**）。要判「手指刚在别处抬起」只能认 `pointerup`。
  ② **改这层之前先读 `web/node_modules/@xterm/xterm/src/` 里的真实监听**，别推断。已经吃过的两条：
  `.terminal` 上的 mousedown 是**无条件** `preventDefault() + focus()`（在外面 preventDefault
  拦不住聚焦，只能在 document 捕获段 `stopPropagation`）；同一串鼠标事件还会按 SGR **上报给
  herdr**，于是一次幻影点击能把 herdr 的焦点拽走。
- 文件浏览（`internal/files`、`web/src/term/paths.ts`）：吐内容那条路**绝不能是
  `text/html`** —— 同源 HTML 就是一个能调 `/api/herdr/say` 的跳板。**SVG 能 inline 是
  因为走 `<img>`**（规范的 secure static mode）+ 顶层打开有 CSP `sandbox`，换成内联
  `<svg>` 就是同源 XSS。认路径的正则要挡中文标点（`a.png。相对的` 会被吞成一个路径）和
  pane 的竖线（`/tmp/a.pn│`），光秃秃的相对路径必须带扩展名（不然 `2026/08/21` 变链接）。
  还有一条只在窄屏上现形：**判「路径折在两行上」不能只看 xterm 的 `isWrapped`** —— herdr
  的 pane 是绝对定位重画的，那个标志永远是 false，于是手机上被 agent 折断的路径只认出前
  半截。判据是「顶到内容区右边界」（Ink 的 `hard` 折行正好切在列宽上），而**「顶到」要容
  四格** —— TUI 自己那一层比 pane 窄几列，**每个 TUI 让的还不一样多**（实测：Ink 的 42 列
  pane 差两格；codex/ratatui 的 51 列 pane 差**四**格 —— 它留了滚动条那一列，列表块又有右
  留白）。少容一格的后果**不是少一条链接，而是一条指错地方的链接**：续行
  `assets/chapter-7/x.png` 自己就是一条合法的相对路径，于是按 cwd 解出
  `…/recursion-game/assets/…`（真路径是 `…/art-assets/…`），下划线看着完全正常、点下去报
  「找不到」。放宽靠另外三条判据兜底（词得真装不下、结尾不能已经是完整文件名、带的边界）。容了这两格就得再加一条：结尾那个词
  已经像个完整文件名（`…/a.png`）时不拼，否则「正好差一两格填满这行的路径」会把下一行的头
  一个词粘上来，把一条好链接弄坏。还有一条：判「被切开的那个词有多宽」时**边界只能是
  空格**，宽字符的第二格（空串）不算 —— wrap-ansi 切词就是 `split(' ')`，中文里一个空格
  都没有，所以「这一批全上线了,预览无报错。https://…」整条是一个词（超过行宽才被切开的）。
  把那一格当空格的话量出来的词只剩 `https://p54f`，「两截还不到一行宽」成立、判成正常断句
  —— 表现是**中文后面紧跟的链接永远只认出前半截**，而 agent 说话就是这个样子的。
  还有一条只在**分栏**时现形：折行判据里的「右边界」必须是**这条内容带**的右边界（`Band`，
  按竖直分隔线切；`provideLinks` 对一行上每条带各算一遍）。拿「整行最右那个非空格」去推的话，
  平板上左右两个 pane 时那个字在**另一个 pane** 里，量出来的边界离本带的字几十列远，
  「顶到右边界」永远不成立 —— **分栏时压根不拼**，URL 点开只有 `https://p`。
  它上一版是「有分隔线就整个放弃拼行」（怕把另一半 pane 的字拼进来），按带读之后那个取舍
  不存在了：本带拼得回来，另一半各算各的。
  还有一条同族的：判「结尾那个词是不是已经是个完整文件名」（`FINISHED`，容了两格之后才需要
  的那道）之前**要先剥掉 scheme** —— `https://` 里那两个斜杠不是路径分隔符，它后面跟的是
  主机名，而主机名里的点是标签分隔。不剥的话 `…preview.creght.cn/#diff` 被切在 `.creg` 上
  时，一截被切开的主机名被判成「已经完整、别拼」，表现是**点开只有半个域名**；而剥的那个
  正则**不能锚在词首**（`/^.*[A-Za-z][\w+.-]*:\/\//`）—— 「词」是按空格切出来的，而 agent
  说话时 URL 前面常常直接粘着中文和标点（`更大胆:https://…`，中间一个空格都没有），锚在
  词首就等于没剥，同一条毛病原样复发（用户报的「点开只有半截」）；而它只在
  「靠 slack 才算顶到边界」时才生效，所以换个前缀或 pane 宽度就好了（「有时候能点有时候
  不能」）。同一条还有个例外要留着：**上一行在同一列上也断着时，`FINISHED` 那道不算** ——
  那一列就是这个 TUI 的硬折宽度，「凑巧差几格填满」这个前提当场没了。少了它，被切在
  **扩展名当中**的 URL（`…__contact_qr.pn` ＋ `g`）会停在断点上，点开少最后一个字符
  （用户报的；`.pn` 一样满足「点后面 1–5 位字母数字」）。
  还有一条是**打开**那一侧的：路径尾巴上常挂着**行号锚点** —— markdown 链接里是
  `…VIDEO.md#L21`（agent 在 chat 里最爱写这种）、编译错误里是 `main.go:42:7`，而那几位
  不是文件名的一部分，stat 一定找不到（用户报的「这个文件是存在的，只是它有 # 号」）。
  剥在 `App.openPath`（终端 / chat / 改动三个入口都汇到那儿），**顺序是「先按原样问一次，
  找不到再剥」**：`notes#1.md` 是合法文件名，先剥的话那种文件永远打不开，而且报的错和
  这条一模一样。
  几何是量出来的，所以钉了 `web/src/term/paths.test.ts`（`node` 直接跑那个 .ts，
  在 `make test` 里）。
  都在 [SECURITY.md](docs/dev/SECURITY.md) 和 [MOBILE.md](docs/dev/MOBILE.md)。

## 约定

- 注释和文档写中文，说清**为什么**（尤其是那些反直觉的取舍），别复述代码在干什么。
- 终端那层（`web/src/term/`）是命令式的，别往 React 里搬。
- 命令行用 [cobra](https://github.com/spf13/cobra)，配置用 [viper](https://github.com/spf13/viper) 且**只从环境变量来**（不读配置文件）。加配置项就是 `internal/config/` 里加一行 + README「配置」那节的表格加一行；别新开命令行标志，也别在别处 `os.Getenv`。
- **所有环境变量都带 `HERDR_WEB_` 前缀，云厂商的 DNS 凭据也不例外**（`HERDR_WEB_CLOUDFLARE_DNS_API_TOKEN`
  这种；lego 读的是光秃秃的名字，进它之前在 `internal/acme/env.go` 里按命名空间脱前缀）。
  前缀不是为了整齐：`service install` 抄进 plist / unit 的就是「所有 `HERDR_WEB_*` + 一张短
  白名单」，光秃秃的名字两头都不占，抄不进去 —— 而这个失败要等到第一次签发（或者三个月后
  第一次续期）才现形。由此来的两条：① 加一家 provider 是**三处**（`envHint` / `newDNS` /
  `dnsNamespaces`），少了最后那个的表现是「带前缀的凭据被当成没配」；② 凭据现在跟着前缀一起
  被 `install` 打到终端上了，所以那段输出里凭据只印星号和长度（`acme.SecretEnv`）—— 那行
  命令常常就敲在一个跑着 agent 的 pane 里。
- 改完跑 `make test`（Go 测试 + 前端 typecheck）。涉及 herdr 行为的改动要在真 pane 上验一遍。
- 发版：`make release V=vX.Y.Z` 打 tag，GitHub Actions 出 Release + 发 npm（`@bysir/herdr-web`）。
  动之前 `make release-dry` 本地跑一遍。**archive 文件名有三处硬编码**要一起改：
  `.goreleaser.yaml` 的 `name_template`、`internal/selfupdate.AssetName`、`scripts/npm-build.mjs`
  —— 对不上的表现是 `herdr-web update` 下载 404。详见下面「发版」。

## 代码结构 / 发版 / 配色 / 已经踩过的坑

（原来在 README 里。README 现在只讲怎么装、怎么用、怎么配；下面这些是**改代码之前**要读的。）

### 代码结构

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
  agentwatch/         盯 agent 状态变化：打时间戳（面板一览的「几分钟前」）+ 攒提示
                      （notice.go 防抖 / extract.go 读屏抽话，testdata 是真机抓屏）
  outbox/             列目标 / 拉回 / 清空 / 投稿 / 推草稿
  softkeys/           快捷键条配置 + 按键谱解析（data.go 是从旧 JS 版生成的，不是手抄的；
                      testdata/js-snapshot.json 存着当时的快照，测试比对前 6 组）
  capability/         **能做哪几件事的唯一一份清单**（id + 能出现在哪些界面上）。顶栏的白名单、
                      快捷键条的 act 白名单、前端那份按钮目录都从它来 —— 为什么合成一份、
                      散着会怎么静默出错，在包注释里
  topbar/             顶栏放哪几个按钮（一串 id + 白名单）。和快捷键条**分两个文件两个口**：
                      混在一起就得处理「只改一半」的偏更新语义，那是静默丢配置的来路
  profiles/           「这台设备用哪一套排布」：名册 + 每个浏览器绑在哪一套 + 跟着套走的
                      那几个开关。定义全局、排布分套 —— 为什么这么切在包注释里
  uploads/            图片 / 视频落盘（按魔数认类型，**边读边写**进 `.part` 再改名 ——
                      视频几百 MB 别整份读进内存，断在一半也不留一个看着正常的半截文件）。
                      上限按类型定（图 25 MB / 视频 512 MB）。视频的魔数认在 files.VideoType，
                      和看文件那条路**共用一份**（HEIC / AVIF 和 mp4 是同一个 ftyp 容器，
                      得先按主品牌排掉照片）。agent **不直接看视频**，要自己跑 ffmpeg 抽帧
  files/              文件浏览：起点 / 列目录 / 按魔数认类型 / 短时签名链接（sign.go）。
                      默认不设边界，配了 FILE_ROOTS 才是 jail —— 为什么、以及那四条
                      「绝不 text/html」的硬规矩，都在包注释里
  gitdiff/            看 diff：跑 git（status / diff）+ 把补丁解析成结构 + 按词高亮
                      （parse.go）。**只读**，边界借文件浏览那一处（Files.Check）——
                      跑 git 的那八条「静默出错」在包注释里
  transcript/         chat 模式的数据源：读 **agent 自己写在磁盘上的会话记录**
                      （claude 的 JSONL / codex 的 rollout）读成一条对话流。定位靠 herdr 的
                      `agent_session`（`herdr integration install` 装的 hook 报的），
                      id → 路径这一步自己做。**边界自己钉**（不借文件浏览那一处，理由见
                      包注释）。为什么不读屏、两家格式的坑，在 docs/dev/CHAT.md
  clip/               读这台机器的剪贴板（pbpaste / wl-paste / xclip）—— herdr 的复制
                      落在**跑 herdr 那台机器**上，手机要拿到只能由这一侧读出来
  server/             HTTP 路由 + PTY/WebSocket + 静态资源
                      guard.go 是门卫（Host 白名单 / Origin / 安全响应头）
                      authapi.go 是配对和设备管理的口
                      session.go 是「一个 URL 一个 herdr session」的分派（每个 session
                      一个 socket、一份发件箱、一条状态订阅）
                      filesapi.go 是文件浏览的口 + /_f/ 那条**不带 cookie**的吐字节路
                      chatapi.go 是 chat 模式那个**只读**的口（发言走现成的发件箱，
                      审批一律留在终端）
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
  src/capabilities.tsx  **能做哪几件事**那份清单的前端一半（图标 / 名字 / 一句说明 + 从表里
                      推出来的 KeyAct / PanelId）。服务端那一半在 internal/capability
  src/term/           xterm.js 胶水：补协议、触屏手势、重绘看门狗（命令式，不套 React）
                      paths.ts 把终端里的文件路径变成可点的链接（折行拼回 —— xterm 折的
                      和 TUI 自己折的两套、中文标点和竖线当终止符、截断过的不给链接 ——
                      每条都是实测踩出来的）
                      mobilebar.ts 认 herdr 移动端顶栏那个 switch 按钮（按背景色摊色块），
                      给「点它开我们的面板一览」当判据
  src/hooks/          useCompose（发件箱状态机）、useNotices（提示轮询 + 红点）、
                      useViewportHeight
  src/lib/            api.ts（fetch + CSRF，还有认「这是哪台设备」的 installId）、
                      prefs.ts（跟着套走的那几个开关：服务端为准 + localStorage 镜像，
                      因为有几处读是同步的）、chipdrag.ts（「库在下、栏在上、拖进去」那套
                      手势，顶栏和快捷键条两个编辑器共用）、oriented.ts（横竖屏各存一份本地
                      几何）、tap.ts（丢了 click 就补一个）
  src/components/     Dock.tsx 是底部面板的外壳（发件箱 + 快捷键条共用的边框 / 宽度 / 高度）
                      Notices.tsx 是右上角那几张提示卡
                      FilesPanel.tsx 是文件浏览（起点列表：当前工作空间排最前、标「当前」
                      + 目录 + 粘路径的框）
                      FileViewer.tsx 是看一个文件（图 / 文本），铺满整屏
                      ChatPanel.tsx 是 chat 模式（对话流代替那一屏 TUI）：**不铺满屏**
                      （底下那行发件箱要还点得到）、候选只给当前工作空间、sig 变了整份丢掉
                      DiffPanel.tsx 是改动清单（仓库从**当前工作空间**那几个 pane 的 cwd 猜），
                      DiffViewer.tsx 是补丁页：**全部文件一条连续的流**（滚到跟前才读、
                      折行 / 词高亮 / 段头粘顶），铺满整屏
                      Pairing.tsx 是配对页（没配对时只渲染它）
                      SettingsPanel.tsx 是设置面板，顶栏 / 快捷键条 / 设备是它的三页，
                      ProfilePicker.tsx 是分页条上面那行「这台设备用哪一套排布」
                      TopbarPanel.tsx 是顶栏编辑器（三个筐：顶栏 / 内置按钮 / 我的按键），
                      「有哪些内置按钮」在 `src/capabilities.tsx`（那是**全部能做的事**那一份，
                      服务端 internal/capability 要和它一致，有测试盯着）；「我的按键」那一档
                      是 `key:<定义ID>` 引用
                      QrScan.tsx 是配对页里的扫码器（BarcodeDetector + 后摄）
docs/dev/             **开发理由**：为什么这么设计、实测出来的语义、会静默出错的坑
                      （HERDR-API / COMPOSER / OUTBOX / MOBILE / SECURITY，索引在 README.md）。
                      根目录只留使用说明 —— 两种读者要的东西不一样
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

**tag 要推到装着 `release.yml` 的那个远端**，也就是 GitHub。这个仓库的远端**不叫 origin**
（只有一个 `github`；早先还有一个指向自建 git 的 `origin`，已经删掉了），所以 `make release`
**不写死 origin** —— 它按 push URL 里的 `github.com` 认，认不出来就拒绝发版。推错远端是最难查的一种：tag 打上去了、命令也成功了，
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
- **`index.css` 里的全局样式必须包在 `@layer` 里**（踩过一次，**完全静默**）：按 CSS
  Cascade Layers 的规则，**未分层的样式压过所有 layer**，而 Tailwind v4 的工具类在
  `@layer utilities` 里 —— 于是一条 `* { scrollbar-width: thin }` 就能把
  `[scrollbar-width:none]`（「这一块别画滚动条」）整个盖掉，而 F12 里两条规则都看得见、
  类也在元素上，看着就是「这个工具类在这儿不生效」。滚动条那段现在收在 `@layer base` 里。
  用户报的是「快捷键条下面那条滚动条怎么关都还在」—— 认它的判据是那条横线**从滑动段的
  第一个键开始**（钉住的那几个不在滚动容器里，底下是干净的）。
- `accent` 是旧名字（原来那个亮蓝），现在留成 `brand` 的别名防止漏改，新代码别用它。
- **主题色是可选的**（`brand` 这个 pref，跟着排布走，入口在设置 →「终端」）：内置六套
  （绿 / 蓝 / 紫 / 青 / 品红 / 石墨），换的只有 `brand` 那四个 token，灰阶和 `ok`/`bad`/`warn`
  一个都不动。四条别改回去：① **内置色里不给红橙黄** —— 面板一览一行里同框出现「等你」（红）、
  「在跑」（黄点）和「当前 / 完成」（brand），主题色落进那一段三个当场分不出，而这正是
  `--color-warn` 那条注释记着的翻车；② 色值**只有 CSS 一份出处**（`--sw-<id>` +
  `:root[data-brand=<id>]`），终端的光标和选区是 `term/themes.ts` 的 `termTheme` 现读
  `--color-brand` 拿的 —— 别在 TS 里抄第二份（6 套 × 明暗 = 24 个值，抄漏了是「界面紫了、
  光标还是绿的」）；③ 由此来一条**顺序**规矩：先把 `.light` 类和 `data-brand` 落到 `<html>`
  上，**再**叫终端去读（App 那两个 effect 就是这个顺序，原来那两个是反的），反了会一直错到
  下次切换；④ TS 那份清单（`lib/prefs.ts` 的 `BRANDS`）和 CSS 里的规则**对不上是完全静默的**
  （色块画成一个洞 / 点了没反应），`web/src/lib/brand.test.ts` 盯着（在 `make test` 里）。
  两条已知的不跟着走：不支持 `color-mix` 的老浏览器上，`bg-brand/12` 这类**半透明**的档会
  停在绿（Tailwind 编的那份写死兜底）；logo 和离线页那点绿是**品牌标识**，有意不换。

### 几个坑（已经处理了，记下来免得回头再踩）

- **Android 上「tap 手势本身就会把输入法顶回来」，`preventDefault` 拦不住。** Chromium 处理完一个
  GestureTap 之后**无条件**调 `ShowVirtualKeyboard()`（`WebFrameWidgetImpl::DidHandleGestureEvent`，
  不看事件被没被取消），浏览器那侧只判「此刻聚焦的元素可不可编辑」。而**用返回键 / 系统手势收键盘
  不会 blur 页面元素** —— 终端那个隐藏 textarea 还聚着焦，于是点任何一个「刻意不改焦点」的按钮
  （顶栏那排、快捷键条、键盘收起时顶栏留的那条 8px 缝）都会把 IME 重新弹出来。两条治法都在用：
  ① 那条缝**原生吃掉 touchstart**（React 的 `onTouchStart` 是 passive 的，`preventDefault` 不生效；
  吃 touchend 也不行 —— 「touchend 被 handled + Android」本身是弹键盘的另一个触发条件），
  touchstart 一被取消，Chromium 会丢掉整条手势序列，连 click 都没有，所以展开动作在那儿自己做；
  ② 视口长回去（键盘没了）而焦点还在输入框上时**主动 blur**，让状态和事实一致 —— 顶栏自己就回来了，
  而且没有可编辑元素可弹。②的判据只在「这个浏览器确实会因为键盘压缩视口」时才生效（有的浏览器
  纹丝不动，见上面「手机」那节）。
  另外**「键盘收没收」这个判据不能拿 `window.innerHeight` 当基准**：viewport meta 里的
  `interactive-widget=resizes-content` 会让布局视口跟着键盘一起缩，比值恒等于 1、信号恒 false ——
  iOS 上照旧对，只在安卓上现形（②那条补救等不到触发，⌨ 一直亮着、要点两下才弹键盘）。
  基准是「这个朝向上见过的最高那次」（`useKeyboardUp`）。
- **后台标签页里量不出「打开态 / 高亮」这类样式。** `document.hidden` 为真时 Chrome 不渲染，
  CSS transition 永远停在 `currentTime: 0`，于是 `getComputedStyle(el).backgroundColor` 返回的是
  **过渡前**那个颜色 —— 而元素上的 class 明明是对的。用浏览器自动化验样式时会得出「`bg-brand`
  没生效」这种结论，而且**新建一个同 class 的探针会报出正确的终值**（它没有过渡要跑），于是
  看着像「同样的 class 在这个位置就是不生效」，能查很久（查过两次）。判据：先看 `document.hidden`
  和 `requestAnimationFrame` 还跑不跑；要量就先塞一条 `*{transition:none!important}`，
  或者干脆只断言 class。
- **触屏上「点了没反应 / 键盘自己弹出来 / 焦点被拽走」这一类，别用合成事件验。** 这条是三轮没修掉
  同一个 bug 换来的：`dispatchEvent` 派出去的合成事件**不会派生兼容鼠标事件**，于是我手动补了一个
  `touchend` —— 而真机上那一下恰恰是唯一必然丢失的事件（浮层在 `pointerup` 的处理里被卸掉，而 touch
  事件的 target 在 `touchstart` 就钉死了，元素脱离文档之后事件仍派给它、**不再冒泡到 document**）。
  测试里闸门永远是开的，看着「修好了」，真机上等于没装。**判「手指刚刚在别处抬起」只能认
  `pointerup`**：它的传播路径在派发开始时就算好，处理器里卸掉浮层也不影响它走完。
- **改触屏 / 鼠标那一层之前，先读 `web/node_modules/@xterm/xterm/src/` 里的真实监听，别推断。**
  已经吃过两条：`.terminal` 上的 mousedown 是**无条件** `preventDefault() + focus()`（所以「在外面
  preventDefault 拦住聚焦」这条路根本走不通，只能在 document 捕获段 `stopPropagation`）；而同一串
  鼠标事件还会按 SGR **上报给 herdr** —— 一次幻影点击就能把 herdr 的焦点拽到手指底下那个 pane，
  甚至点开它自己的 `switch` 面板盖住目标。一个洞两个后果，只修其中一个会一直觉得「没修好」。
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
  **回到前台那一下要自己补一次重绘**（`session.ts` 的 `wake`）：不可见时 rAF 不跑、一帧都没画，
  回来时连接多半还是好的（探针有回音）于是什么都不做，而补画那条路是**收到新字节之后**静默
  180ms 才跑 —— 「agent 停下来等你回答」正好是对面一个字节都不发的状态，画面就焊在息屏前
  那一帧上。用户报的是「点了提交终端里什么都没反应，还停留在第一个问题」：**按键一直是通的、
  投出去的选择也真到了**（chat 里看得见结果），丢的只有画面，所以这条**完全静默**。
- **滚几屏之后零星几个字变成别的字形**（用户报的，真机确认修好了）：不是花屏也不是缺字，
  是字形**拿错了纹理** —— 截图里 `creght-eval` 画成 `≯reghɪ-ev≜l`、`main` 成了 `mai↗`，
  **英文也中招**。出处在 `@xterm/addon-webgl` 的 `TextureAtlas._createNewPage()`：页数涨到
  `min(32, gl.MAX_TEXTURE_IMAGE_UNITS)`（手机 GPU 上常见 16）时它把 4 页合成一大页，然后
  **平移所有 glyph 的页索引**，错一个那个字就从别处取纹理。**只有中文用户看得见**：一页
  512×512，而手机上（DPR≈2.75、字号 13）一个汉字的格子约 40×40 设备像素 —— 一页只放得下
  一百多个汉字，一屏中文就要一两页，滚几屏必然顶到上限；英文那几十个字形一辈子填不满一页。
  治法是**不让它合并**（`session.ts` 装 WebglAddon 那段）：数着当前页数，到 8 页就自己
  `clearTextureAtlas()`。三条别改：① 数的是**现在有几页**（`onAdd/onRemoveTextureAtlasCanvas`
  一起听），不是「新建过几次」—— 清纹理**不删页**，按新建次数数的话页数照旧涨到上限；
  ② **异步清**（`setTimeout 0`），那个回调是在「正往新页里写字形」的中途 fire 的；
  ③ 阈值留余量（8 对 16），卡着上限清等于赌谁先到。
- **改尺寸会闪一下全黑，要拿「冻帧」盖住**。呼输入法（`visualViewport` 一变就重排）时最明显。原因是叠起来的：xterm 的 WebGL 渲染器一改 `canvas.width` 绘制缓冲就清空、`FitAddon.fit()` 在 resize 前还主动 `renderService.clear()` 一次（所以现在不走 fit：自己量完直接 `term.resize`，少一次清屏），而重画最快也要等下一个 rAF（2026 同步输出开着时得等 ESU）；herdr 收到 SIGWINCH 之后自己又清屏重绘一遍，加起来几十毫秒。xterm 没有同步重绘的口子，所以延迟一个都去不掉 —— 改尺寸之前把 `.xterm-screen` 里那几层 canvas 合成一张图铺在终端上，等新画面画上（`onRender`）再多留 120ms 淡出。两个前提：WebGL 要开 `preserveDrawingBuffer`（合成完不丢缓冲，否则 `drawImage` 拿到的是空图），以及**快照读不出东西时要放弃冻帧**（后台标签页 rAF 不跑、画布压根没画过，糊一张空图上去比闪一下更糟）。另外行列数没变就不碰 xterm：键盘动画期间 `visualViewport` 会连着报好几次，白 resize 一次就白闪一次。
- **「呼一次输入法」不是一下，所以重排要等整段停下来**（`session.ts` 的 `relayout` / `settle`）。用户报的是「呼键盘重绘很多次，期望只有 1 次」。视口是**一格一格**变过来的：iOS 每帧一个 `visualViewport.resize`，安卓分几段、每段之间能静止 200ms，中间还夹着「顶栏收掉」和进全屏那两下 —— 而每一下都够触发一次 resize，一次 resize 就是一次清画布 + SIGWINCH + herdr 清屏重画。原来那个固定 80ms 防抖只挡得住密到 80ms 以内的，隔得开一点就漏成两次、三次。现在分档：**视口自己在动**的那几下（呼输入法 / 转屏 / 进出全屏，`relayout(true)`）在防抖之外再压一个 260ms 的地板，别的（面板开合、拖发件箱、改字号）照旧 90ms —— 单纯把防抖调长的话，一步到位的变化会跟着白等。代价是键盘升起的那 ~300–500ms 里终端还是老行数（底下几行被键盘盖着），换的是只闪一次。两条别拆掉：① 视口有可能**一直**在动（iOS 地址栏来回弹），所以有 900ms 的兜底上限，不然永远不重排；② 那个上限只对视口档生效 —— 拖发件箱把手是连着几秒的 resize 事件，给它加上限就是「拖着不放每 0.9 秒闪一下」。
- **resize 之后的冻帧要等 herdr 重画到了才撤**，别跟着 xterm 自己那次 `onRender`。xterm 因为 resize 画的那一帧是「旧内容按新宽度重排」，而 herdr 的 pane 是绝对定位重画的、压根不按行流重排，那一帧看着是花的 —— 跟着它撤帧的话屏幕上是「花一下 → herdr 的 SIGWINCH 重画又正一下」**两次**变化，这就是「呼一次键盘闪两回」里的第二回。判据是「resize 之后收到过字节」（`freezeAwait`）。**兜底照旧是 `THAW_CAP`**：对面也可能一个字节都不回（底下就是个闲着的 shell 提示符），不能糊着一张旧图不放。
- **「点了没反应」多半是反馈离手指太远。** toast 挂在终端那一层、底部居中（`z-20`，面板是 `z-10`，所以它确实画在上面），而面板里的保存按钮在**顶上**那一行 —— 桌面上面板靠右、提示在整屏正下方；手机上面板几乎铺满，提示压在最底那条缝里。人的眼睛在刚点的按钮上，于是「保存了跟没保存一样」（用户报的）。所以**存这类动作的反馈放在按钮自己身上**（`web/src/components/ui/savebutton.tsx`：✔ 已保存 / ✕ 没存上），toast 只留给「顺手做完、本来就看得见结果」的事（恢复默认、复制、换套）。三条别拆掉：① **失败也要举一下 ✕** —— 只做成功态的话，失败又退回一个一模一样的「保存」，「点了没反应」原样回来了；② 举着的那一下按钮是 `disabled`（挡连点），但必须 `disabled:opacity-100` —— 按钮基类自带 `disabled:opacity-45`，正好把唯一要人看见的这一下压暗；③ **失败的原因还是留在面板里那行红字上**，别塞进按钮（服务端会指出是第几个按键、哪里不认，按钮只回答「这一下算不算」），所以约定 `onSave` 成了 `true`、没成 `false`，别把异常抛给按钮。
- **herdr 的主题不跟浏览器切换**：`~/.config/herdr/config.toml` 里 `[theme] auto_switch = false`。改成 `true` 之后，网页上切明暗就能直接切 herdr 的配色。
- **别把 `HERDR_WEB_SETTLE_MS` 调成 0**：详见「配置」那节。
- **锁屏断连没法「修」，只能自己连回来。** 手机 / 平板锁屏时系统把页面挂起，WebSocket 跟着断 —— 页面里没有任何开关能留住它（Web Lock、keepalive 都不管这事）。以前解锁回来就是一句「已断开」加一次手点，而重连本来**没有任何代价**：一条 WebSocket 一个 PTY，重连拿到的是新的登录 shell，但 herdr 的 pane 都活在 herdr server 里，`herdr` 一敲就 attach 回去，屏幕和断开前一样。所以现在断了自己连（`web/src/term/session.ts` 的 `retry` / `wake`）：退避 0.4→8 秒最多 8 次，**页面不可见时压根不试**（iOS 后台定时器基本不跑，就算连上了也马上被系统再掐掉，还白起一个登录 shell），等回到前台 / 网络回来那一下再从最短那档重新数；连不上就把原来那套诊断（后端没在跑 / 凭据没了 / 反代没转发 Upgrade）摊在遮罩上。**凭据被撤销那种不重试** —— 重连一万次也一样，而每次 `term.reset()` 还会把真正的原因刷掉。另外 `connect()` 里往 sessionStorage 记了一笔「这个标签页连过」：iOS 锁屏久了 Safari 会把整个页面丢掉重载，回来时靠这个直接连上，而不是又停在「点连接」那一屏（sessionStorage 只对这个标签页有效，新开一个还是要手点）。
- **锁屏回来的 WebSocket 常常是「僵」的**：`readyState` 还是 OPEN、`send()` 也不报错，但对面早就没了 —— 这时候只看 readyState 会以为连着，敲什么都没反应。协议层的 ping/pong 是浏览器自己处理的，网页里读不到（拿它判断这条路不存在），所以在应用层补了一帧：回到前台时发 `{"t":"p"}`，3 秒内没有任何回音就当断了，收掉重连。服务端的回音在 `internal/server/pty.go` 的 `case "p"`，**丢了这一行的表现是「每次解锁都白重连一次」**，屏幕上看不出异常 —— 所以 `TestPTYAnswersProbe` 是端到端拨一条真连接来验的。
- **重连必须先把终端复位**。一条 WebSocket 对应一个 PTY，断开时服务端就把 PTY 杀了，所以每次「连接」都是一个**全新的登录 shell**；但 xterm 实例是复用的，上一次 herdr 打开的私有模式还留在里面。表现是重连之后屏幕不但没好，还往命令行里灌乱码：鼠标移动上报（1003+1006）还开着，指针 / 手写笔一动就发 `ESC [ < 35;120;36 M`，zsh 的 ZLE 把认不出的 `ESC [ <` 前缀吃掉、余下的自插进命令行，于是屏幕上是 `35;120;36M35;115;37M…`（实测复现过：`➜  ~ 35;16;5M35;26;8M`）。kitty 键盘协议的 flags 同理留着，Esc 会被编成 `CSI 27 u`，新 shell 里显示 `[27u`。`connect()` 现在先 `term.reset()` 再连，顺手清掉我们自己攒的 kitty flags / 能力清单 / 粘滞修饰键。
- **「连接」按钮随时能按，所以连之前要自己收掉旧连接**。不收：服务端会再起一个登录 shell，两个 shell 的输出往同一个 xterm 里灌，屏幕当场花掉，而且旧 PTY 只要连接还在就一直活着。旧连接的回调也要一起摘掉 —— close 是异步的，旧连接的 `onclose` 会把新连接的状态改成「已断开」。
