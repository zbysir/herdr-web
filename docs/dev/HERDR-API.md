# herdr socket API：实测出来的语义

动 `internal/herdr/`、`internal/outbox/`、`internal/agentwatch/` 之前先读这份。里面每一条都是在真
pane 上验出来的，**不是从文档抄的** —— herdr 的 schema 只说字段长什么样，不说这些行为。

配套的另两份：抽输入框那套坑在 [COMPOSER.md](COMPOSER.md)，发件箱的设计取舍在 [OUTBOX.md](OUTBOX.md)。

## 怎么连

`$HERDR_SOCKET_PATH`，默认 `~/.config/herdr/herdr.sock`。换行分隔 JSON，请求 `{id, method, params}`。

**别依赖 `HERDR_SOCKET_PATH` 存在**：`internal/server/pty.go` 的 `dropEnv` 会把 `HERDR_*` 从 PTY
环境里清掉（防嵌套启动），而 server 进程自己也可能根本不是从 herdr pane 里起的。解析不到就退回
默认路径 —— `internal/config` 的 `DefaultSocket()` 是唯一接入点。

全量 schema：`herdr api schema --json`。CLI 只暴露了其中一部分（`pane.send_input`、
`events.subscribe` 都没有对应子命令），所以**别拿 `herdr --help` 当 API 清单**。想知道有多少个
method，自己数（数字会随版本变，别记在文档里）：

```sh
herdr api schema --json | python3 -c '
import json,sys,re
d=json.load(sys.stdin); out=set()
def w(o):
  if isinstance(o,dict):
    for k,v in o.items():
      if k=="method" and isinstance(v,dict):
        for kk in ("enum","const"):
          x=v.get(kk)
          out.update([x] if isinstance(x,str) else x or [])
      w(v)
  elif isinstance(o,list):
    for v in o: w(v)
w(d["schemas"]["request"]); print(len(out))'
```

## 已验证的语义

| 事实 | 怎么验的 |
|---|---|
| **一个连接只处理一个请求，不支持 pipeline** | 同连接塞两个请求，只回第一个的响应，第二个直接丢 |
| 清空 + 提交 = 两次连接，**不需要 sleep** | 顺序发送天然有序；实测残留被干净清掉、新文本正常送达 |
| **「亚毫秒往返」只在赢了 accept 竞争时成立** | 见下面「100ms 的坎」 |
| `pane.send_input` 一个请求收 `text` + `keys`，顺序是 **text 先、keys 后** | `text="AAA", keys=["b","c"]` → 行上是 `AAAbc`。所以它适合 text+enter，清空必须单独一个请求 |
| 投 agent 用 `agent.prompt`，别自己拼 enter | 它会按 pane 当前的 bracketed-paste 模式正确编码 Enter |
| `pane.get` 的 `agent` 字段用来分派 | 返回 `"claude"` / `"codex"`，普通 shell pane 没这个字段 |
| `pane.read` 要拿转义序列得 `format:"ansi"` + `strip_ansi:false` | `format:"text"` 即使 `strip_ansi:false` 也不含 ESC。响应嵌在 `result.read.text` |
| **API 里没有任何「输入框内容」字段** | grep 过 `composer` / `input_line` / `draft` / `prompt_text`，全 0。拉取方向只能读屏，见 [COMPOSER.md](COMPOSER.md) |
| `events.subscribe` + `pane.output_matched` 是推送通道 | 带 substring/regex 匹配，事件里给 `matched_line` + 完整 `read` 快照。订阅连接保持打开 |
| `pane.read` 快照有一帧延迟 | 写完立刻读会拿到上一帧内容，要么重读要么等一下。所以 `Pull` / `Sync` 走 `readSettled`（读两次取后一次） |
| `pane.current` 给「此刻激活的 pane」 | 返回和 `pane.get` 同形状的 `pane` 对象，用来做「跟随焦点」 |
| **`agent.prompt` 端到端通了** | 先塞两行残留，再投一段，pane 上收到的就是新文本本身，没有残留前缀 |
| **多行输入在活 pane 上验过** | claude / codex 两家都用 `shift+enter` 换行；抽出来的多行原文一字不差（含以 `>` 开头的续行） |
| **API 里没有任何时间戳** | grep 过整份 schema：`pane.get` / `agent.get` / `agent.list` / 事件负载里都没有时间字段，`agent_session` 也拿不到（实测那几个 claude pane 全是空）。所以「上次动过是几分钟前」只能由收事件的这一侧自己打时间（`internal/agentwatch`） |
| **`agent.list` 的 `state_change_seq` 是全局递增的** | 13 个 agent 的值两两不同、17…438 铺开，顺序和「我今天动过哪些」对得上。所以它 = 「谁最近动过」的可靠排序依据，比自己记的时间可靠（自己记的只覆盖本进程起来之后）。`pane.list` **不带**这个字段，要单独调 `agent.list` |
| **`agent_status` 不能用来判断「正开着对话框」** | 实测一个正在显示选择器的 Claude Code pane，`agent_status` 是 `idle`；另一次同样的对话框又报 `blocked`。两个方向都不可靠 —— 靠它做闸门会静默投错，正确的判据见 [COMPOSER.md](COMPOSER.md) 的「清不空就别投」 |
| **API 里没有图片通道，但 agent 能读磁盘上的图** | 给一张 320×200 左红右蓝中间绿带的 PNG 的**绝对路径**，claude 和 codex 都描述对了（codex 会打一行 `Viewed Image`）。所以传图＝落盘 + 把路径当文本投出去，见 `internal/uploads/` |

`pane.read` 的 `source` 只有 `visible` / `recent` / `recent_unwrapped` / `detection` —— 在 agent pane
上四者返回的都是整屏，**`detection` 并不是裁剪过的区域**，别指望它替你定位输入框。

## 订阅：三个反直觉的地方

| 事实 | 怎么验的 |
|---|---|
| **`pane.agent_status_changed` 会漏，别用它盯状态** | 它必须带 `pane_id`（不带直接报 `missing field pane_id`），于是要给每个 agent pane 订一条、pane 集合一变还得整条连接重订 —— 换来的却是漏事件：同一个五分钟里 `pane.updated` 那条流看到 3 次状态变化，它只来了 1 条（快速来回的 working↔idle 大概被防抖吞了）|
| **盯状态要用全局 `pane.updated`** | 每条都带完整 pane 对象（`terminal_id` / `agent_status` 都在），不用带 pane_id、不用重订。量大（一个 agent 在跑时实测 20 秒 193 条，跟着输出走）但每条只是几百字节，解析可忽略 —— 这就是 herdr 自己界面用的那条流。`internal/agentwatch` 走的是这条 |
| **事件名有两套拼法** | 全局订阅回的是**下划线**（`pane_created` / `pane_updated`），按 pane 订的 `pane.agent_status_changed` 回的是**原样的点号订阅名**，负载形状也不一样（没有 `type` 字段，`pane_id` / `agent_status` 直接摊在 `data` 上）。踩过：按下划线写判断，于是每条真的状态变化都掉进「别的事件」那条分支，时间列一条都没记上，而假 socket 的测试用下划线发、还是绿的。现在 `herdr.Subscribe` 把事件名统一成下划线再交出去，测试也改成两种都发 |
| **订阅一建立，herdr 会补发一个旧的 `pane_created`** | 每次都是同一个 pane、`revision: 0`。原来「收到全局事件就重订阅」，于是每 800ms 重连一次、永远在重连（`Live()` 采样几乎全是 false）。换成只听 `pane.updated` 之后这条自然消失了：别的事件一律忽略、连接一直挂着 |

上面那几条是一路踩出来的，顺序值得记一下：先按「看着最对口」的 `pane.agent_status_changed` 写，
被**事件名的两套拼法**坑了一轮（一条都没记上，而假 socket 的测试是绿的）；改对名字之后又发现
**它本身会漏**（五分钟 3 次变化只来 1 条）；最后换成全局 `pane.updated`，顺带把「补发的
`pane_created` 触发重订、重订又收到补发」那个 800ms 无限重连也一起消掉了。

留下来的那道防仍然要紧：**只有状态和上次记的不一样才算一次变化**。把补发的、重复的事件当变化，
会把所有 pane 的「上次动过」刷成「刚刚」—— 那是**编时间**，比空着糟得多。

## pane.zoom：跳到某个 pane

| 事实 | 怎么验的 |
|---|---|
| **带 `pane_id` 一次跨 workspace + tab + pane** | 对另一个 workspace 里的 pane 发 `mode:"on"`，回 `focus_changed:true`，`pane.current` / `workspace.list` 都跟着切过去了 —— **不用**先 `workspace.focus` 再 `tab.focus`。「面板一览」点一行就是这一个调用 |
| **zoom 是 tab 级的开关，放大的永远是当前焦点 pane** | `pane.layout` 和 `layout.export` 里只有 tab 级的 `zoomed` + `focused_pane_id`，**没有** per-pane 的 zoom 字段；而且 `layout.panes[].rect` 给的是未放大的分屏几何（放大时两个 pane 都还是 120×58），所以「谁被放大了」只能由焦点推 |
| 同 tab 内换 pane 回 `zoom_changed:false` + `reason:"already_zoomed"` 而 `focus_changed:true` | **那不是失败**：放大的对象跟着焦点换了，不需要 off 再 on |
| 单 pane 的 tab 回 `zoomed:false` + `reason:"single_pane"` | 那个 pane 本来就占满整个 tab，焦点已经切过去了。别当失败报错 —— 前端要单独说一句，不然用户以为按钮没生效 |
| `mode` 默认是 `toggle` | 所以「跳到某个 pane 并铺满」必须显式传 `"on"`，不能省。软键条上绑的 zoom 键走的是默认 toggle，那条路只能二选一 |

## 100ms 的坎：请求必须和 connect 同一瞬间发出

「unix socket 单次往返亚毫秒」**只在你赢了 accept 竞争的时候成立。**

实测：`connect()` 之后哪怕只隔 **0.5ms** 再 `sendall()`，响应就要 ~106ms 才回来。Python 原型快
（0.1ms）纯粹因为它 connect + sendall 是一串阻塞调用，请求字节在服务端 accept 的那一刻已经躺在
socket 缓冲区里了。隔开一点点就掉到下一个 ~100ms 的 tick 上。

```
connect 后等 0.0ms 再 send ->   0ms      # 字节已在缓冲区，accept 时顺手读掉
connect 后等 0.5ms 再 send -> 106ms      # 错过了，等下一跳
connect 后等 150ms 再 send -> 209ms      # 又量化到 100 的倍数
```

**node 基本必然踩中**：事件循环保证 connect 和 write 之间至少隔一个 turn。试过等 `connect` 事件再
写、`net.connect` 之后立刻 write、`end(payload)` 一把梭 —— 全都是 ~106ms。而 node 客户端打 node
自己写的 unix socket 服务端只要 0–1ms，所以不是 node 的锅，是「请求没赶上 accept」这个交互。

结论和影响：

- 那个 106ms 是**紧凑循环里的共振**，不是地板：每次响应都刚好卡在 tick 上，下一次 connect 就总是
  刚过点。散着打有时只要 1–2ms。
- 一拍 `sync` 打 3 次调用（`pane.current` + 两次 `pane.read`），实测 ~150–320ms。
- **曾经据此推断「readSettled 不用 sleep」，那是错的。** 理由是「每次调用固定 ~106ms，两次读天然
  隔了一个 tick」；但既然 1–2ms 也可能，两次读就会落在同一帧上。实测把 settle 调成 0，清空循环 6 轮
  跑完仍然清不空（整个 `Clear()` 27ms 就返回了，读的全是同一帧陈旧内容），`Say()` 于是一律报
  「清不空」。所以 `HERDR_WEB_SETTLE_MS` 默认 120，而且 `ReadComposer()` 自己有 120ms 保底 ——
  清空那条路的正确性最要紧，不能被一个配置项调坏。

要真正拿到亚毫秒，客户端得在 connect 的同一个系统调用序列里把请求写出去 —— Go 的
`net.DialTimeout` + 立刻 `Write` 也未必赢得了这个竞争（赢不了就只是慢一跳，不影响正确性）。真到了
嫌慢那一步，方向是**减少每拍的调用数**，不是调 sleep。

## 命名 session 各有一个 socket

| 事实 | 怎么验的 |
|---|---|
| **`herdr --session <name>` 是另一个 server，另一个 socket** | `herdr session list --json` 给每个 session 的 `socket_path`：默认是 `~/.config/herdr/herdr.sock`，命名的是 `~/.config/herdr/sessions/<name>/herdr.sock` |
| 从浏览器新建一个 session 是通的 | 网页开 `/{name}` → PTY 里敲 `herdr --session <name>` → `session list` 里那个 session `running`，`pane.list` 只有它自己那一个 pane（默认 session 那边同时是 49 个） |
| socket 路径**从默认 socket 的目录推**，别写死文件名 | `config.SessionSocket()`：`<dir(HERDR_WEB_SOCKET)>/sessions/<name>/<base(HERDR_WEB_SOCKET)>`；`HERDR_WEB_SOCKET` 自己就指在某个 `sessions/<x>/` 里时要先退回上层，否则拼出 `sessions/x/sessions/y/` |

**socket 选错是完全静默的**：拿默认 session 的 socket 去投一个命名 session 里的 pane，
`agent.prompt` 照样成功，只是进了另一个 herdr。所以 herdr-web 那边每个请求都带 `?session=`，
解析不了就报错而**不退回默认 session**（`internal/server/session.go`）。

## socket 在哪台机器上

**socket 在跑 herdr server 的那台机器上**，不一定是跑 herdr-web 的机器。

- **本机模式**（现在唯一实现的）：直连 `~/.config/herdr/herdr.sock`。
- **ssh 模式**：socket 在远端。要么 `ssh host 'nc -U ~/.config/herdr/herdr.sock'` 把连接代理过去，
  要么在远端跑个 helper。**这条没写也没验** —— 这台机器上 `ssh localhost` 本来就不通，没法验。
  真要做，唯一的接入点是 `internal/config` 的 `DefaultSocket()`。

（herdr-web 这一层**不做** ssh 连远端：要连别的机器直接在 herdr 里 ssh，herdr 自己就能干。见
README 的「只开本机 shell」。）

## 这些结论是怎么验出来的

`reference/` 下是 Python 原型，上面每一条「已验证」都是拿它们跑出来的：

| 文件 | 内容 |
|---|---|
| `reference/herdr_api.py` | `call()` 单请求 + `subscribe()` 生成器 |
| `reference/herdr_composer.py` | 按 agent 分派的输入框抽取，含 dim / SGR / NBSP 处理 |
| `reference/hsay` | 覆盖式投稿：清空 → `agent.prompt` 或 `send_input`+enter |
| `reference/hpull` | 抽输入框内容，另有 `--raw` / `--tail N` |

`hsay` / `hpull` 由 `~/.local/bin/` 里的 symlink 指到这里，所以在终端里能直接敲，改 repo 里的文件
即时生效。两个脚本按 `os.path.realpath(__file__)` 找同目录的模块，通过 symlink 调用也能 import
—— **挪动这些文件时保持四个同目录**。

要再验一条新事实，最快的路子就是照 `herdr_api.py` 写十行 Python 打一次 socket，别先写 Go。

---

## 点一行要「一下就跳」

（原来在 README「面板一览」那节。这是 `pane.zoom` 按 pane_id 寻址的实测结论。）

#### 点一行要「一下就跳」

手机上键盘弹着的时候点列表里一行，原来是**第一下只收键盘、第二下才跳**（真机实拍）。两个原因叠在一起：

- 开这个面板的三条路都刻意**没让浏览器改焦点** —— 软键条在 mousedown 上 `preventDefault`，触屏那层把
  `touchstart` 整个吃掉（不然手指一划就变成拖选，见 [MOBILE.md](MOBILE.md#触屏手势)）。于是面板浮出来的时候，
  发件箱 / 终端那个输入框还聚着，键盘还占着半个屏。
- 那一下点击于是**先**把焦点带走：`--vvh` 跟着 visualViewport 一变，面板整个重排，手指底下那一行已经
  挪了位置 —— 浏览器把 click 派给了别人，或者干脆不派。

三头都堵上了：

- **开面板时主动把焦点从输入框上摘掉**（Web 上没有「收键盘」这个 API，blur 就是收键盘）。文件浏览
  那两块（目录面板 / 查看器）主入口也是终端里点一下，同样在打开时先收。
- **筛选框在触屏上不自动聚焦** —— 判据从「手机竖屏（< 440px）」改成「有没有实体指针」。平板上原来照样
  自动聚焦，等于刚收起来又弹回去。
- **行的收工点从 `click` 挪到 `pointerup`**：touch / 笔的 pointer 事件有隐式捕获，pointerdown 落在
  哪一行、pointerup 就还在那一行，这中间布局怎么动都不影响。鼠标和键盘还是走 click（桌面上 click 从不
  丢），手指滑超过 10px 当成在滚列表、不算点。行这条**必须自己认元素**，光靠下面那个兜底不够：
  丢了 click 是一种，**派给隔壁那一行**是另一种，跳错 pane 比没反应糟得多。
- **手指按下那一刻就把 pane id 记住**，抬起时用记下的那个，不用当前渲染的 `p.id`：列表 4 秒重拉一次，
  React 重渲染之后同一个 DOM 元素上挂的已经是另一个 pane 的回调了 —— 那就成了「跳到你没点的那个」。
- **顺序是实时的**：4 秒一拍重拉之后跟着重排，谁刚开工 / 刚等你就当场浮上来。中间有一版是「面板开着
  就把顺序冻住」，为的是治「你看着第三行、手指落下去时那一行已经换人了」；**撤掉了** —— 实际用起来
  是排序像坏的：一个在跑的 pane 明明 1 分钟前动过，却排在几个闲了两小时的下面（因为它是在面板打开
  之后才开工的）。这个面板的价值就是「谁在等我」，**停住的排名比抽走一行更糟**（需求方定的）。
  「点错 pane」那条不靠冻结兜，靠上一条：按下那一刻就把 id 记住了。

别处的按钮（各个面板的 ✕、开关、分页）走的是全局那道兜底，见
[MOBILE.md 的「第一下只收键盘」](MOBILE.md#第一下只收键盘)。
