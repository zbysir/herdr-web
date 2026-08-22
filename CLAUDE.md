# herdr-web

浏览器里的 herdr 终端 + **语音投稿**（平板手写笔说话打字、框选重说改字，投进 herdr 的 agent pane）。

一个 Go 二进制，前端（React + Vite + Tailwind）嵌在里面：`make build` → `./herdr-web`。
怎么跑、环境变量、代码结构、移动端手势、已处理的坑：[README.md](README.md)。

动这三块之前先读对应的那份 —— 里面是实测出来的语义和一串**会静默出错**的坑：

| 要动 | 读 |
|---|---|
| herdr socket 调用（`internal/herdr`、`outbox`、`agentwatch`） | [HERDR-API.md](HERDR-API.md) |
| 抽输入框（`internal/composer`） | [COMPOSER.md](COMPOSER.md) |
| 发件箱（`internal/outbox`、`web/src/hooks/useCompose.ts`） | [OUTBOX.md](OUTBOX.md) |

这几条最容易再踩一遍：

- `ctrl+u` 清空是 **2N−1 次**（N 行输入），固定次数只够两行；清不空就别投。
- `HERDR_WEB_SETTLE_MS` **别调成 0**，否则两次 `pane.read` 落在同一帧上，清空循环整体失效。
- 「本地草稿」要用单独的所有权标志判断，别拿文本比较推 —— 开着双向同步时会被自己覆盖。
- 抽输入框有三个坑：dim 占位、`38;2;...` 里的 `2` 不是 dim、空框是 `❯`+NBSP。
  `internal/composer/testdata/` 里是真机抓屏，改这块必须跑 `go test ./internal/composer/`。
- 提示（`internal/agentwatch/notice.go` + `extract.go`、`web/src/hooks/useNotices.ts`）：
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
- 排布分套（`internal/profiles` + `web/src/lib/prefs.ts`）：**定义全局、排布分套**。软键条的
  「我的按键」是所有套共用一份（改一个按键谱两边一起变），`rows`/`bar` 和顶栏 `items` 每套一份。
  由此来的三条：① 删一个定义要把**所有套**条上的引用一起清掉（`Save` 里 prune），读的时候丢掉
  认不出的引用而**不是**整份退回出厂 —— 那会把没坏的一半也抹了；②「恢复默认」只动这一套的条，
  绝不整份恢复出厂（定义全局，那样会把别的套的键抹掉）；③ `softkeys.json` / `topbar.json` 里
  默认那一套要**镜像到顶层老字段**，不然降级回老版本看到的是「配置自己没了」。
  绑定认的是前端生成的 `installId`，**不是 auth 的设备 ID**（本机直连压根没有那个）；
  设备类别只在「第一次来还没绑」时猜一次，**绝不按屏幕宽度自动切**。
  跟着套走的开关 = 设置里「终端」那一整页（白名单在 `profiles.Prefs`，前端那份在
  `lib/prefs.ts`，两边**一字不差、顺序也一样**，有测试盯着）。加一项就是两边各加一行 +
  `applyProfiles` 里刷一下 state。模型是**服务端为准 + localStorage 镜像**：读的地方一律
  照旧读镜像（终端回调里有几处是同步读的），别改成读 state。
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
  `<svg>` 就是同源 XSS。认路径的正则要挡中文标点（`a.png。相对的` 会被吞成一个路径），
  光秃秃的相对路径必须带扩展名（不然 `2026/08/21` 变链接）。都在 README「文件浏览」那节。

## 约定

- 注释和文档写中文，说清**为什么**（尤其是那些反直觉的取舍），别复述代码在干什么。
- 终端那层（`web/src/term/`）是命令式的，别往 React 里搬。
- 命令行用 [cobra](https://github.com/spf13/cobra)，配置用 [viper](https://github.com/spf13/viper) 且**只从环境变量来**（不读配置文件）。加配置项就是 `internal/config/` 里加一行 + README「配置」那节的表格加一行；别新开命令行标志，也别在别处 `os.Getenv`。
- 改完跑 `make test`（Go 测试 + 前端 typecheck）。涉及 herdr 行为的改动要在真 pane 上验一遍。
- 发版：`make release V=vX.Y.Z` 打 tag，GitHub Actions 出 Release + 发 npm（`@bysir/herdr-web`）。
  动之前 `make release-dry` 本地跑一遍。**archive 文件名有三处硬编码**要一起改：
  `.goreleaser.yaml` 的 `name_template`、`internal/selfupdate.AssetName`、`scripts/npm-build.mjs`
  —— 对不上的表现是 `herdr-web update` 下载 404。详见 README 的「发版」。
