# 交接：语音投稿（voice compose）

要做的东西：平板 + 手写笔，按一下说话打字，说错的字**框选重说**改掉，改完把整段投进 herdr 的 agent pane。

herdr-web 是这个功能唯一能落地的地方——原因见下一节。项目本身的说明、环境变量、移动端手势和已处理的坑在 [README.md](README.md)，这份文档只写语音投稿相关的、README 里没有的东西。

## 实现现状（已落地）

下面「原来建议的开发顺序」里的 1–4 全做完了，端到端在真 pane 上验过，而且已经从
Node 全量重写成 **Go 后端 + React 前端，单二进制**（`make build` → `./herdr-web`）。

| 位置 | 内容 |
|---|---|
| `internal/herdr/` | socket 客户端，一次调用一条连接（服务端不支持 pipeline） |
| `internal/composer/` | 按 agent 分派的输入框抽取；`testdata/*.ansi` 是真机抓屏 |
| `internal/outbox/` | 列目标 / 拉回 / 清空 / 投稿 / 推草稿 |
| `internal/softkeys/` | 软键条配置 + 按键谱解析；`data.go` 是从旧 JS 版生成的 |
| `internal/uploads/` | 图片落盘（按魔数认类型），返回给 agent 读的绝对路径 |
| `internal/server/` | HTTP 路由 + PTY/WebSocket + 静态资源 |
| `web/src/term/` | xterm.js 胶水：补协议、触屏手势、重绘看门狗（命令式，不套 React） |
| `web/src/hooks/useCompose.ts` | 发件箱状态机（所有权 / 目标锁定 / 自动拉回 / 双向推送） |

HTTP 口（都要 `?token=`）：`GET /api/state`、`GET|PUT|DELETE /api/softkeys`、
`GET /api/herdr/panes`、`GET /api/herdr/sync`、`GET /api/herdr/pull`、
`POST /api/herdr/say`、`POST /api/herdr/draft`、`POST /api/herdr/upload`（裸字节）。
`target` 省略或传 `__focused` = 投给此刻在 herdr 里激活的那个 pane。

**移植是怎么保证没走样的**：`composer` 和旧 JS 版共用同一批真机抓屏，输出逐字节一致；
`softkeys` 的 51 条预设不是手抄的，是从旧 JS 生成的，`testdata/js-snapshot.json` 存着
当时的快照，测试逐条比对。两边的坑都做过变异测试（去掉 NBSP 归一 / 去掉 SGR 子参数
消费，真机用例立刻挂）。

**去掉的东西**：web 这一层的 ssh 连远端主机（主机管理、托管私钥、`ssh-keygen`、
`~/.ssh` 扫描、ssh_config 导入）。要连别的机器直接在 herdr 里 ssh —— herdr 自己就能干，
没必要在这儿再实现一套，顺带把「浏览器能碰到私钥」这个面也去掉了。

**没做**：ssh 模式下 socket 在远端那条路（见文末「socket 在哪台机器上」），仍然只连本机。
这台机器上 `ssh localhost` 本来就不通，没法验，所以没写。`internal/config` 的
`DefaultSocket()` 是唯一的接入点。

## 为什么必须在网页里加一个真 textarea

终端是字节流，没有 selection 语义，IME 只能往里灌字符。"框选重说"要求一个真正的可编辑字段——有文本模型、有选区，选中后 IME 提交会覆盖选区（这是 EditText / textarea 的默认行为，跟用哪个输入法无关）。

xterm.js 的隐藏 textarea **不算**：它只把按键转成字节流发走，不维护可编辑文本。所以直接对着网页终端说话，只能"说得出、改不了"。

做法是在页面上加一个真的 `<textarea>` 当**发件箱**：在里面说、在里面改，改完整段投出去。

**发件箱，不是镜像。** 原始设计：别做双向同步——两个缓冲区一个字节流，同步永远追不上。发完就清空本地框，每次整段覆盖，不发增量。远端的 Tab 补全和上下键历史属于"直接操作终端"那条通道，跟投稿通道混用必然错位；要历史就在发件箱这侧留（发件箱这侧留了 30 条，框空时按 `↑` 取回）。

### 后来加的：跟随焦点 + 自动拉回 + 可选的双向

需求方要的是「不用选 pane，就用我在 herdr 里激活的那个」和「切 pane 自动拉回，改动双向同步」。落地时拆成三件事，风险差很多：

- **跟随焦点**（默认开）。目标默认是哨兵值 `__focused`，服务端在**按下按钮那一刻**用 `pane.current` 解析。
- **自动拉回**（默认开，500ms 一拍，`HERDR_WEB_POLL_MS` 可调）。只读，安全。带**脏草稿保护**：本地内容和上次对齐的不一样时绝不覆盖，只在状态行提示。
- **本地 → 远端推草稿**（`✎ 双向` 开关，默认关）。这就是原设计反对的那半边，所以加了两道闸：
  - **只推有 `agent` 字段的 pane。** 普通 pane 里跑的可能是 vim 或某个选择器，那里的字符是**命令**不是文本——跟着焦点乱推会直接触发操作。
  - **草稿锁定目标。** 「跟随焦点」不能一路跟到提交那一刻：为 A 写的话，中途 herdr 自己因为 agent 状态变化换了焦点（实测会），投出去就落到 B 了。所以框里一有**自己写的**内容就把目标钉在当初瞄准的 pane 上，框空了才重新跟随（自动拉回来没动过的不算）。这条是踩出来的——没锁定之前，一段写给 claude 的话被投进了 codex。

  推草稿走 `pane.send_input` 只带 text 不带 keys（不回车），推之前照样要先清空。

#### 「所有权」必须单独一个变量，别用文本比较推

这三个 bug 全出在同一个地方，值得单独记一笔。前端一开始只有一个「上次和远端对齐的文本」（旧代码里叫 `cSynced`，现在是 `useCompose` 里的 `synced`），用「文本 !== 它」当「本地有草稿」的判据。它同时在干两件事——**发现远端变了** 和 **保护本地草稿**——于是：

1. **切 pane 内容不更新。** 拿「框里有没有字」当锁定判据时，自动拉回来的内容也会把目标钉死，之后切 pane 永远不刷新。判据得是「这是不是我自己写的」。
2. **开着双向时草稿被覆盖。** 草稿推到远端之后 `synced` 就等于草稿本身，于是草稿看起来「没改过」→ 解锁 → 下一拍被远端内容覆盖。
3. **开双向的瞬间把 A 的内容写进 B。** 勾上开关就推一次，推的却是「自动拉回来的」内容，中间焦点一动就落到别的 pane 里。

现在拆成两个：`synced` 只负责「远端最后一次读到什么」，`own` 只负责「框里是不是用户自己写的」。自动拉回、目标锁定、要不要推草稿，全看 `own`。框空了 `own` 归 false，控制权交回「跟随焦点」。

另外 `Pull` / `Sync` 也必须走**读两次取后一次**（`readSettled`）。单次读会拿到上一帧，表现就是「切了 pane 框里还是旧内容」，更糟的是自动拉回会把这一帧陈旧内容记成「已对齐」，用户接着在陈旧内容上编辑。

原设计那句话仍然成立：**冲突的那一半是本地→远端**。开着 `双向` 的时候别同时在那个 pane 里手敲字。

## 为什么投稿必须"先清空再发"

`agent.prompt` 和 `pane.send_text` 都是**追加**语义，不是"把输入框设为这段文字"。

```
send_text "abc" → send_text "def"   结果：abcdef
```

用户在远端按过 Tab 补全或上下键历史之后，输入行上已经有残留，再投一段进去就变成 `残留 + 新文本` 一起回车。发件箱对此一无所知，用户在本地按删除删的是自己的草稿。

修法：每次投稿前先发 `ctrl+u`。实测在 zsh / Claude Code / Codex 三处都能清空，对空行是 no-op。

> **更正**：原来这里写「无条件打 3 次兼容多行残留」，**不够**。实测 Claude Code 和 Codex 都是 **N 行输入要 2N−1 次** `ctrl+u`（一次删掉本行内容，再一次删掉这个空行），3 次只够两行。5 行残留打 3 次会剩下第 1 行，然后跟新文本一起发出去。
>
> 所以 `Clear()` 不拍固定次数，而是**读一次、按残留行数补够、再读一次确认**，收敛为止（5 行残留一轮搞定）。shell pane 例外：那边抽不出可靠的输入框（提示符五花八门），没法拿「空」当判据，而 zsh/bash 的 `ctrl+u` 一次就干掉整个 buffer，所以那边仍然是固定 3 次 —— 这条路不读屏，所以 shell 照样能投。

**清不空就别投。** 见下面「agent 弹出选择框时」那条。

## herdr socket API（已验证）

`$HERDR_SOCKET_PATH`，默认 `~/.config/herdr/herdr.sock`。换行分隔 JSON，请求 `{id, method, params}`。全量 schema 用 `herdr api schema --json`——208 个 method/event，CLI 只暴露了一部分（`pane.send_input`、`events.subscribe` 都没有对应 CLI 子命令）。

`internal/server/pty.go` 的 `dropEnv` 会把 `HERDR_*` 从 PTY 环境里清掉（防嵌套启动），而 server 进程自己也可能根本不是从 herdr pane 里起的，所以**别依赖 `HERDR_SOCKET_PATH` 存在**，解析不到就退回默认路径。

已验证的语义：

| 事实 | 怎么验的 |
|---|---|
| **一个连接只处理一个请求，不支持 pipeline** | 同连接塞两个请求，只回第一个的响应，第二个直接丢 |
| 清空 + 提交 = 两次连接，**不需要 sleep** | 顺序发送天然有序；实测残留被干净清掉、新文本正常送达 |
| **「亚毫秒往返」只在赢了 accept 竞争时成立** | 见下面「100ms 的坎」——从 node 发请求基本必然踩到 ~106ms |
| `pane.send_input` 一个请求收 `text` + `keys`，顺序是 **text 先、keys 后** | `text="AAA", keys=["b","c"]` → 行上是 `AAAbc`。所以它适合 text+enter，清空必须单独一个请求 |
| 投 agent 用 `agent.prompt`，别自己拼 enter | 它会按 pane 当前的 bracketed-paste 模式正确编码 Enter |
| `pane.get` 的 `agent` 字段用来分派 | 返回 `"claude"` / `"codex"`，普通 shell pane 没这个字段 |
| `pane.read` 要拿转义序列得 `format:"ansi"` + `strip_ansi:false` | `format:"text"` 即使 `strip_ansi:false` 也不含 ESC。响应嵌在 `result.read.text` |
| **API 里没有任何"输入框内容"字段** | grep 过 `composer` / `input_line` / `draft` / `prompt_text`，全 0。拉取方向只能读屏 |
| `events.subscribe` + `pane.output_matched` 是推送通道 | 带 substring/regex 匹配，事件里给 `matched_line` + 完整 `read` 快照。订阅连接保持打开 |
| `pane.read` 快照有一帧延迟 | 写完立刻读会拿到上一帧内容，要么重读要么等一下 |
| `pane.current` 给「此刻激活的 pane」 | 返回和 `pane.get` 同形状的 `pane` 对象，用来做「跟随焦点」 |
| **`agent.prompt` 端到端通了** | 先塞两行残留，再投一段，pane 上收到的就是新文本本身，没有残留前缀 |
| **多行输入在活 pane 上验过了** | 两家都用 `shift+enter` 换行；抽出来的多行原文一字不差（含以 `>` 开头的续行） |
| **`agent_status` 不能用来判断「正开着对话框」** | 实测一个正在显示选择器的 Claude Code pane，`agent_status` 是 `idle`；另一次同样的对话框又报 `blocked`。两个方向都不可靠 |
| **API 里没有图片通道，但 agent 能读磁盘上的图** | 给一张 320×200 左红右蓝中间绿带的 PNG 的**绝对路径**，claude 和 codex 都描述对了（codex 会打一行 `Viewed Image`）。所以传图＝落盘 + 把路径当文本投出去，见 `internal/uploads/` |

`pane.read` 的 source 只有 `visible` / `recent` / `recent_unwrapped` / `detection`——三者在 agent pane 上返回的都是整屏，`detection` 并不是裁剪过的区域。

## 100ms 的坎：请求必须和 connect 同一瞬间发出

原来这份文档写「unix socket 单次往返亚毫秒」。**只在你赢了 accept 竞争的时候成立。**

实测：`connect()` 之后哪怕只隔 **0.5ms** 再 `sendall()`，响应就要 ~106ms 才回来。Python 原型快（0.1ms）纯粹因为它 connect + sendall 是一串阻塞调用，请求字节在服务端 accept 的那一刻已经躺在 socket 缓冲区里了。隔开一点点就掉到下一个 ~100ms 的 tick 上。

```
connect 后等 0.0ms 再 send ->   0ms      # 字节已在缓冲区，accept 时顺手读掉
connect 后等 0.5ms 再 send -> 106ms      # 错过了，等下一跳
connect 后等 150ms 再 send -> 209ms      # 又量化到 100 的倍数
```

**node 基本必然踩中**：事件循环保证 connect 和 write 之间至少隔一个 turn。试过等 `connect` 事件再写、`net.connect` 之后立刻 write、`end(payload)` 一把梭 —— 全都是 ~106ms。而 node 客户端打 node 自己写的 unix socket 服务端只要 0–1ms，所以不是 node 的锅，是这个「请求没赶上 accept」的交互。

结论和影响：

- 那个 106ms 是**紧凑循环里的共振**，不是地板：每次响应都刚好卡在 tick 上，下一次 connect 就总是刚过点。散着打有时只要 1–2ms。
- 一拍 `sync` 打 3 次调用（`pane.current` + 两次 `pane.read`），实测 ~150–320ms。
- **曾经据此推断「readSettled 不用 sleep」，那是错的。** 理由是「每次调用固定 ~106ms，两次读天然隔了一个 tick」；但既然 1–2ms 也可能，两次读就会落在同一帧上。实测把 settle 调成 0，清空循环 6 轮跑完仍然清不空（整个 `Clear()` 27ms 就返回了，读的全是同一帧陈旧内容），`Say()` 于是一律报「清不空」。所以 `HERDR_WEB_SETTLE_MS` 默认 120，而且 `ReadComposer()` 自己有 120ms 保底 —— 清空那条路的正确性最要紧，不受配置调低影响。

要真正拿到亚毫秒，客户端得在 connect 的同一个系统调用序列里把请求写出去 —— Go 的 `net.DialTimeout` + 立刻 `Write` 也未必赢得了这个竞争（赢不了就只是慢一跳，不影响正确性）。真到了嫌慢那一步，方向是**减少每拍的调用数**，不是调 sleep。

## 读屏抽输入框：按 agent 分派

herdr 自己就是这么干的。manifest 在 `~/.local/state/herdr/agent-detection/remote/*.toml`，引擎的 region 词汇表里本来就有输入框概念：`prompt_box_body`、`after_last_prompt_marker`、`after_last_horizontal_rule`、`last_non_empty_above_prompt_box`。

| agent | herdr manifest 用的 region | 屏幕上的实际形状 |
|---|---|---|
| claude | `prompt_box_body` / `after_last_horizontal_rule` | 上下两条纯 `─` 横线夹住，横线是**前景色** `38;2;136;136;136` |
| codex | `after_last_prompt_marker` | 一段**背景色**块 `48;2;49;52;57`，状态栏不在块内 |
| 其余 17 个 | 没有输入区规则，只有 `whole_recent` / `osc_title` / `bottom_non_empty_lines(N)` | 无从照抄，只能嗅探 |

两点注意：

- 这些 region 是引擎内部给规则求值用的，**API 不暴露**，客户端得自己重做一遍。价值在于照抄它的 per-agent 选择，而不是猜 UI 特征。
- codex 那条 `after_last_prompt_marker` 会把状态栏也圈进去——herdr 只需要一个区域给 regex 求值，我们要的是精确内容，所以用背景色块收边更合适。

定位锚点用提示符字形 `[❯›❱]`，**不认裸 `>`**，否则输入内容里以 `>` 开头的 markdown 引用行会被当成输入框起点。

## 三个已经踩过的坑

**Codex 空框有 dim 占位提示。** 清空后框里显示 `Run /review on my current changes`，纯文本层面和真实输入无法区分。判据是它整段套在 `\x1b[2m` 里——dim 文字算 chrome 不算内容。两家的真实输入都不带 dim（实测 Claude Code 的用户输入完全没有 SGR）。

**`38;2;153;153;153` 里有个 `2`。** 按分号朴素切分会读成 dim，而 Claude Code 的横线正好用这个色，整个输入框会被判成占位、返回空。必须按 SGR 规则把 `38`/`48`/`58` 的 `2;r;g;b` 和 `5;n` 子参数整段消费掉再判断。

**Claude Code 的空输入框是 `❯\xa0`（NBSP）**，不是空格。判空之前先把 NBSP 归一成空格。真正吃劲的不是判空（`rstrip` 顺手就把 NBSP 干掉了），而是有内容时「去掉提示符后那一个分隔空格」——不归一就匹配不上，正文最前面会挂着一个 NBSP 一起发出去。把归一去掉，两条真机抓屏的用例立刻挂。

**agent 弹出选择框 / 确认框的时候，输入框那块区域画的是那个控件。** 抽取会把整个控件当成"输入框内容"返回（实测拉回来 255 个字符的 checkbox 界面），投出去就是一堆垃圾。

判据不能用 `agent_status`（见上表，`idle` / `blocked` 都出现过），也不该去猜 UI 特征。可靠信号是**清不空**：对话框开着时 `ctrl+u` 打不掉那块区域，`clearComposer()` 收敛不到空。所以 `Say()` 在「清不空」时直接**拒绝投递**并报错，而不是硬投——追加语义下硬投就是 `残留 + 新文本`。

### 「认不出输入框」和「输入框是空的」必须分开

`composer.Extract` 返回 `(text, ok)`：`ok=true, text==""` 是**认出了框、里面是空的**，`ok=false` 是**这一屏上没有输入框**（找不到提示符字形 `[❯›❱]`）。

原来 `ok=false` 那条路会退回「屏幕最后一行非空」。那是猜的：屏幕最后一行往往是状态栏、上一条命令的输出、或者 agent 画的某个控件，拉回来就是把这行垃圾塞进发件箱，而用户会以为那是远端输入框里的字。所以现在**认不出就说认不出**，不退回任何一行。

这个区分不只是显示问题，它撑着投稿的安全闸：`Clear()` 一旦把 `ok=false` 的 `""` 当成「框已经空了」，`Say()` 就会往一个不知道是什么的界面里追加整段文本。所以 `Clear()` 遇到 `ok=false` 报 `NoBox`，`Say()` 拒投（错误信息和「清不空」那条分开，因为处置不同：一个是按 Esc 收掉控件，一个是先回到输入框），`Draft()` 报 `skipped: "no-box"`。前端拿到 `noBox` 时也**不覆盖本地草稿** —— 那个 `""` 不代表远端是空的。

这条是踩出来的：验证「拉回」时随手按了个 `up`，结果那个 pane 正显示一个提问控件，503 个字符连着一起投了进去。

## socket 在哪台机器上

herdr-web 有两种连法（README 的"三种连法"一节）：本机 PTY，或者 ssh 到远端再跑 herdr。**socket 在跑 herdr server 的那台机器上**，不一定是跑 herdr-web 的机器。

- 本机模式：直连 `~/.config/herdr/herdr.sock`。
- ssh 模式：socket 在远端。要么 `ssh host 'nc -U ~/.config/herdr/herdr.sock'` 把连接代理过去，要么在远端跑个 helper。这条路还没验证过，是设计时要先定的分叉。

## 现成的参考实现

`reference/` 下是 Python 写的原型，上面每一条"已验证"都是拿它们验出来的。移植成 node 时逻辑照搬：

| 文件 | 内容 |
|---|---|
| `reference/herdr_api.py` | `call()` 单请求 + `subscribe()` 生成器 |
| `reference/herdr_composer.py` | 按 agent 分派的输入框抽取，含 dim / SGR / NBSP 处理 |
| `reference/hsay` | 覆盖式投稿：清空 → `agent.prompt` 或 `send_input`+enter |
| `reference/hpull` | 抽输入框内容，另有 `--raw` / `--tail N` |

`hsay` 和 `hpull` 由 `~/.local/bin/` 里的 symlink 指到这里，所以在终端里能直接敲，改 repo 里的文件即时生效。两个脚本按 `os.path.realpath(__file__)` 找同目录的模块，通过 symlink 调用也能 import——挪动这些文件时保持四个文件同目录。

### 验证状态

Python 原型当初过了：codex 空框（有占位）→ 空、codex 有内容 → 原文、claude 空框 → 空、claude 有内容 → 原文（含中文标点）、shell pane → 提示符行（**Go 版在这条上故意不一致**：`➜` 不是认的提示符字形，现在报「认不出输入框」，见上面那节）。合成用例也过：两种 UI 的多行输入（含以 `>` 开头的续行不误判）、纯 dim 占位、真实内容带 `38;2` 前景色的解析陷阱、未知 agent 走嗅探。

**移植之后补上的**（`npm test`，18 个用例全过）：

- JS 版和 Python 版在 7 张真机抓屏上**逐字节一致**（claude 空/有内容/多行、codex 空/有内容/多行、shell 空/有内容）。
- codex 那批 fixture 是**真的 codex** 抓的，不再是合成的。
- 当初「没过」的两条都过了：`agent.prompt` 端到端（先塞残留再投，收到的没有残留前缀）、多行输入在活 pane 上造出来验过。
- 两个坑做了变异测试：去掉 NBSP 归一 → 3 个用例挂；去掉 `38/48/58` 子参数消费 → 6 个用例挂。都是有牙的用例，不是摆设。

UI 侧用 CDP 驱真 Chrome 验过：输入法提交（`Input.insertText`）、**框选重说**（选中「try catch」再提交一次，被覆盖成新词）、`⌘↵` 投稿、投完清空、`↑` 取历史、自动拉回、脏草稿不被覆盖、焦点漂移下草稿锁定不投错 pane。

**仍然没验**：ssh 模式下 socket 在远端那条路（没写）。

## 原来建议的开发顺序（1–4 已完成，见文首「实现现状」）

1. **`lib/herdr-api.js`**——socket 客户端。完成判据：从 node 里跑通 `pane.get` / `pane.read`(ansi) / `pane.send_input` / `agent.prompt` 四个调用，每个都拿到非错误响应。
2. **`lib/composer.js`**——输入框抽取 + 单元测试。完成判据：上面"验证状态"那批真实和合成用例全部复现通过。
3. **前端发件箱 UI**——textarea + 目标 pane 选择 + 投递按钮，挂在现有软键条旁边（README 的移动端一节说明了软键条的既有结构和粘滞修饰键的做法）。完成判据：平板上用输入法语音键说一句、框选一个词重说改掉、投出去，agent 收到的文本和 textarea 里一字不差。
4. **拉回按钮**——读远端输入框塞进 textarea。完成判据：远端按过 Tab 补全后，点一下能把补全结果拉进 textarea 编辑，再覆盖式投回去。
5. **（可选）推送同步**——订阅 `pane.output_matched`。注意 agent 处于 `working` 时这个订阅是刷屏级的，按 `agent_status` 开关。

长文本还有个更稳的路子：把文本写进临时文件，只投一句"读 /tmp/xxx.md 并执行"。这样远端输入框里永远只有一行短命令，残留和 bracketed-paste 的边界情况全绕开。

## 平板侧两个待定事实

这两条是设备上要确认的，不是代码问题：

- **笔的按键能不能自定义。** 小米焦点触控笔的按键被系统占了（虚拟激光笔 / 圈选 / 远程拍照），Pro 直接取消实体键改手势。Android 层面笔键是 `KEYCODE_STYLUS_BUTTON_PRIMARY/SECONDARY`，只有笔在该 App 窗口上悬停或触碰时才收得到，**没有全局热键 API**。要在设置→触控笔里确认有没有"快捷启动应用"。没有的话，用页面上一个大按钮代替——笔尖点一下，人机工学几乎一样，而且 100% 可靠。
- **语音来源。** 国行 HyperOS 没有 GMS，`termux-speech-to-text` 那条路基本废掉，只能靠输入法（小米 / 讯飞）的语音键。这也正是"必须有真 textarea"的根因：输入法只往可编辑字段里提交文字。
