# Moshi 的 chat 模式是怎么接管 herdr 里 agent 的

> 调研日期 2026-09-18 · 环境：moshi-hook 0.2.35（上游已 0.3.24）/ herdr / Claude Code 2.1.275 / macOS arm64
>
> 原样搬自 `~/dev/longbridge/lb-ops-skill/docs/herdr-streaming.md`（2026-09-21 拷进来）。
> 那边是通用调研，这儿是这个项目要照着做的地基，所以一个字没改。

**为什么这份在 `docs/dev/` 里**：我们要在网页这边做**chat 模式**（一条对话流代替那一屏 TUI），
而它成不成立全看「内容从哪儿来」—— 这份文档把那个问题回答完了。两条结论直接决定实现：
**① 读 agent 自己写在磁盘上的 JSONL，不读屏**（正是 [TUI-VS-GUI.md](TUI-VS-GUI.md) §3 ③ 要的那条
路：读屏错了**不报错**，是这个项目最贵的一条路）；**② 钥匙是 hook payload 里的
`transcript_path`**（§8），拿到它 herdr 就退化成一个只管发键的键盘。

**我们自己的取舍在 §9**（§1–§8 是别人怎么做的，刻意不混在一起写）。§9.1 那张表是
herdr 0.9.0 上和这份调研的三处出入 —— 每一条都让实现变简单了。

## 问题

Moshi 是个手机终端 App，和 herdr 结合得很好：它有 **chat 模式**，能拿到 herdr 里某个 agent 的完整聊天记录并持续推送新内容。但 **herdr 本身没有提供任何聊天层面的 API**。它是怎么做到的？

## 一句话结论

**Moshi 的 chat 模式根本不从 herdr 拿数据。** 聊天内容直接读 Claude Code 自己写在磁盘上的 JSONL transcript，herdr 只负责定位 pane、发按键、以及发键前截屏校验。多路复用器在 Moshi 眼里可替换（tmux / zellij / herdr 三个 adapter 同接口）。

| | 数据来源 | 通道 |
| - | - | - |
| 聊天记录 / 增量推送 | Claude Code 的 `~/.claude/projects/**/*.jsonl` | fsnotify + tail → WebSocket |
| 发消息 / 打断 / 审批 | herdr CLI | `pane send-keys` / `send-text` |
| 审批前确认屏幕状态 | herdr CLI | `pane read`（截屏校验） |

---

## 1. Moshi 是什么

闭源商业 App（App Store / Google Play，订阅制），作者 GitHub ID `rjyo`。本机通过 Homebrew 装的是它的本地守护进程 `moshi-hook`。

```
rjyo/moshi           → 404   源码仓库，不公开
rjyo/moshi-hook      → 404
rjyo/homebrew-moshi  → 200   只有 brew tap 公开
```

tap 里的 formula 由发布脚本生成（`DO NOT EDIT`），内容只是四个预编译 tarball 的地址，**没有 `license` 字段、没有 `head do` 源码构建分支**：

```ruby
url "https://cdn.getmoshi.app/hook/v0.3.24/moshi-hook_Darwin_arm64.tar.gz"
sha256 "82f0fcfd28b00b005115cd27195b518d89a21126af0aee66f03ac4d892ed530c"
```

Go 二进制保留了完整符号表和作者本机源码路径（`/Users/jyo/projects/ai/moshi/app-hook/...`），所以能做到函数级分析。给 opencode 装的那个插件是 TypeScript，**连注释一起原样嵌在二进制里**，可直接抽出：

```bash
strings -a -n 1 "$(which moshi-hook)" | sed -n "$(strings -a -n 1 "$(which moshi-hook)" \
  | grep -n 'import type { Plugin }' | head -1 | cut -d: -f1),+700p"
```

> 仅用于理解机制，不要抄进自己项目。

---

## 2. herdr 确实没有聊天层能力

`herdr api schema --json` 导出的 170 个命令名里，**没有任何 transcript / chat / history / conversation 语义的命令**。最接近的两个是终端快照：

```
herdr agent read <TARGET> --source <visible|recent|recent-unwrapped|detection>
                          --format <text|ansi>
herdr pane  read ...
```

拿到的是**渲染后的屏幕**（可带 ANSI），不是结构化对话。

herdr 连自己的 agent 状态识别都是刮屏来的 —— 本机 `~/.claude/settings.json` 里没有任何 herdr hook，但 `herdr agent get` 依然能报出状态：

```json
{"agent":"claude","agent_status":"working","pane_id":"wA:p1","terminal_id":"term_..."}
```

对应 `herdr agent explain`（"Explain agent detection state"）。

**唯一一个和会话身份有关的接口**，是留给外部程序喂数据的：

```
herdr pane report-agent-session --source <ID> --agent <LABEL> \
      --agent-session-id <ID> --agent-session-path <PATH> <PANE_ID>
```

herdr 愿意**存**这个 session id 和文件路径，但不解析、不暴露内容。`herdr integration install claude` 装的应该就是喂它的 hook。即便装了，读还是得消费方自己读。

---

## 3. Moshi 从哪拿内容

### 3.1 二进制符号

```
internal/cli/hook_claude_transcript.go
internal/gateway/transcripts.go
internal/gateway/transcript_blob.go

cli.collectRecentClaudeTranscriptFiles   cli.parseClaudeTranscriptLine
cli.isClaudeTranscriptPath               cli.transcriptTail
gateway.(*transcriptFile).poll           gateway.(*transcriptFile).load
gateway.(*Server).handleTranscripts      gateway.(*Server).handleTranscriptBlob
gateway.transcriptClientMessage / transcriptServerMessage      ← WebSocket
fsnotify v1.10.1 (backend_kqueue)                              ← 文件监听
```

HTTP 面：`/v1/transcripts/blob`，带自定义头 `X-Moshi-Diff-Control`。

### 3.2 活证据

daemon 没跑、hook 处于 stale 状态时，仍能扫出本机所有 agent 的工作目录 —— 说明它不依赖 hook，直接读文件：

```console
$ moshi-hook cwd-list --json
[{"cwd":"/Users/bysir/dev/longbridge/lb-ops-skill","sources":["claude"],"lastUsed":...},
 {"cwd":"/Users/bysir/golang/bysir/weave","sources":["claude","codex"],...}]
```

### 3.3 session ↔ pane 怎么对上

两边各取一半：

**agent 侧**靠 Claude Code hook payload 自带的字段（二进制里 `hook_event_name` / `session_id` / `transcript_path` / `cwd` / `tool_input` 全在）。`moshi-hook install --target claude` 要装的 9 个 hook：

```
SessionStart, UserPromptSubmit, Stop, SessionEnd, PermissionRequest,
PostToolUse[AskUserQuestion], PostToolUse[ExitPlanMode],
PreToolUse[AskUserQuestion],  PreToolUse[ExitPlanMode]
```

注意 **`PostToolUse` 只挂了两个特定工具，不是全量** —— hook 只用来触发事件和拿 `transcript_path`，不负责搬运内容。

**pane 侧**靠环境变量 + 调 herdr CLI（摘自二进制内嵌的 opencode 插件源码）：

```ts
const herdrSession = process.env.HERDR_ENV === "1" ? process.env.HERDR_SESSION ?? "" : ""
const paneId = process.env.HERDR_PANE_ID ?? herdrFocusedPane(herdrSession)
const paneResult = spawnSync("herdr", ["pane", "get", paneId], { encoding: "utf8", timeout: 300 })
```

现成命令可验证：

```console
$ moshi-hook context
{"kind":"herdr","herdr":{"session":"lb-deva","paneId":"wA:p1","tabId":"wA:t1"},
 "agent":{"name":"claude","source":"herdr","terminalId":"term_..."}}
```

### 3.4 控制通道

```
tui.(*HerdrAdapter).SendKeys / Capture / Exists
tui.(*TmuxAdapter).  SendKeys / Capture / Exists
tui.(*ZellijAdapter).SendKeys / Capture / Exists
```

一个 adapter 抽象三个实现，接口只有三个方法。`Capture` 不是内容通道，用途是**发键前的安全校验**：

```
Capture → tui.verifyClaudeApprovalScreen   截屏确认审批框真在屏幕上
        → tui.keysForClaudeApproval        再敲对应的键
出错串： "tui bridge: capture failed" / "tui bridge: send keys failed"
```

### 3.5 补漏的脏活

- `cli.runClaudeInterruptWatcher` / `scanClaudeInterruptFiles` / `isClaudeInterruptText` —— 用户按 ESC 打断**不触发任何 hook**，只能扫 JSONL 找 `[Request interrupted by user]` 标记。
- `gateway.screenHasBlockedPrompt` —— 判断是否卡在等输入，这里又退回刮屏。
- 远程审批：`PermissionRequest` hook 阻塞住，等 WebSocket 回传手机上的决定，再输出 hook JSON。相关串：`permission.ask`、`Denied remotely in Moshi`、`Open terminal to answer`。

---

## 4. JSONL 格式

JSON Lines：一行一个完整 JSON 对象，`\n` 分隔，**整个文件不是合法 JSON**（无外层 `[...]`）。

选它而不是 JSON 数组的原因：数组要先写 `[` 最后补 `]`，中途崩掉文件就废了；JSONL 每写完一行就是完整有效数据，**可纯 append、可 tail、可从任意行断点续读**。这正是外部消费的前提。

路径规则：`~/.claude/projects/<cwd 把 / 换成 ->/<session-uuid>.jsonl`

### 真实的一行

```json
{"parentUuid":"0c02adcb-...","isSidechain":false,"promptId":"b8fd6b80-...","type":"user","message":{"role":"user","content":"jsonl 是什么，他是流式的吗，长什么样子"},"uuid":"e86285ca-...","timestamp":"2026-09-18T02:04:10.729Z","permissionMode":"auto","origin":{"kind":"human"},"promptSource":"typed","userType":"external","entrypoint":"cli","cwd":"/Users/bysir/dev/longbridge/lb-ops-skill","sessionId":"a8801680-...","version":"2.1.275","gitBranch":"main"}
```

每行自带完整上下文（`cwd` / `gitBranch` / `version` / `permissionMode` / `origin.kind`），消费方捡起任意一行都能还原环境，不必维护状态。

### 行类型分布（某个 298 行会话的实测）

| type | 条数 | 说明 |
| - | - | - |
| `attachment` | 117 | system-reminder、注入的文件内容 |
| `assistant` | 70 | 模型输出 |
| `user` | 35 | 人类输入 **+ 工具结果** |
| `permission-mode` / `mode` / `last-prompt` / `atis-latch` | 各 14 | 状态快照 |
| `ai-title` | 13 | herdr tab 上显示的标题来源 |
| `file-history-snapshot` | 3 | |

`parentUuid` → `uuid` 串成**树**而非线性列表，用于表达 `/rewind`、分支、sidechain（子 agent）。

### assistant 的拆行规则：一行一个 content block

```
req_011CfA4zF3wF3Z  thinking
req_011CfA4zF3wF3Z  text        ← 同一个 requestId
req_011CfA4zF3wF3Z  tool_use
```

该会话统计：7 个请求各 1 行、24 个各 2 行、7 个各 3 行。**单行只是半个回复，要按 `requestId` 聚合才是一次完整输出。**

---

## 5. 落盘时序实测 —— 本文的核心

### 方法

用 0.15 秒间隔采样 JSONL 的字节数，只在变化时记录；同时把变化量与磁盘上各行的真实字节数对齐。

```bash
prev=0
while :; do
  s=$(stat -f%z "$F")
  [ "$s" != "$prev" ] && { printf '%s %s +%s\n' "$(date +%H:%M:%S.%N|cut -c1-12)" "$s" "$((s-prev))"; prev=$s; }
  perl -e 'select(undef,undef,undef,0.15)'
done
```

### 结果

| 采样时刻（本地） | 增量 | 对应的行（UTC，+8=本地） | 各行真实字节 |
| - | - | - | - |
| 10:15:02.367 | +632 | `attachment` | 632 |
| | | *—— 15.58 秒，零字节写入 ——* | |
| 10:15:17.948 | **+7717** | `thinking` @02:15:02 + `text` @02:15:17 | 4156 + 3561 = **7717** ✓ |
| 10:15:18.438 | +2142 | `attachment` + `system` + `system` | 766+941+435 = **2142** ✓ |

两处加总**精确相等**，无误差。

### 两个结论

**① 不是逐 token。** 那 15.58 秒正在生成正文，磁盘零写入，然后 7717 字节一次落地。

**② 粒度是「一次 API 请求」，不是 content block。**

`thinking` 行的 `timestamp` 是 `02:15:02`，`text` 是 `02:15:17`，相差 15 秒，但**它俩是同一次写入落盘的**。

> **行内 `timestamp` 记的是生成时刻，不是写盘时刻。** Claude Code 攒满整个 API 响应才 flush 一次。任何按 `timestamp` 推断落盘节奏的分析都会错。

---

## 6. 所以「流式」是什么级别

**请求级流式，不是 token 级流式。**

一个 agentic turn 会打很多次 API，每次都 flush 一批：

```
请求1: thinking + tool_use   → flush → 跑命令
请求2: thinking + tool_use   → flush → 跑命令
请求3: thinking + text       → flush
```

手机上就是每隔几秒冒出一块新内容 —— **这与流式的体感没有区别**。

唯一的差距只在**一段连续正文的内部**：终端里逐字长出来，手机上整段砸下来。差距的时长 = 生成那一段的耗时（短回答 3~5 秒无感，长回答十几二十秒有明显空窗）。

真正的 token 流只活在 Claude Code 进程内存和 PTY 渲染里，**从不落盘**，任何基于 JSONL 的消费者都拿不到。要拿到只有两条路：刮屏，或用 Agent SDK 自己当宿主。Moshi 两条都没走。

> 另注：Moshi 还有独立的**真终端模式**（`moshi-hook host setup` = "Easy Pair SSH/Mosh access"，二进制含 `no mosh-server on %s:%d` 等串）。那是真 PTY，逐字流，**完全不碰 JSONL**，本文分析不适用于该模式。

---

## 7. 写消费端的四个坑

1. **工具结果是以 `user` 角色记的**（Anthropic API 约定：`tool_result` 属于 user turn 的 content block）。统计"人类说了几句话"时要再过一层：
   ```
   select(.type=="user" and (.message.content|type=="string"))
   ```
2. **meta 行会重复** —— `last-prompt` / `mode` / `ai-title` 各出现十几次，是不断追加的状态快照。取**最后一条**，别当事件流处理。
3. **别用 `timestamp` 排序** —— `attachment` 行的时间戳常比兄弟行早 1ms（生成早于写入）。**用文件行序**。
4. **按行流式读，别整个 load** —— 单个会话文件可以到 17MB+。

---

## 8. 自己实现的最短路径

1. 装 Claude hook（最少 `SessionStart` + `UserPromptSubmit` + `Stop`），从 payload 取 `session_id` / `transcript_path` / `cwd`
2. 同时读 `HERDR_PANE_ID` / `HERDR_WORKSPACE_ID`，建立 `pane ↔ session ↔ jsonl` 映射
3. fsnotify 监听该 jsonl，增量 parse 推流
4. 发消息用 `herdr agent prompt` 或 `herdr pane send-text`

**杠杆在第 1 步：hook payload 里的 `transcript_path` 是整条链路的钥匙。** 拿到它，多路复用器就退化成一个可有可无的键盘。

---

## 9. 我们自己的实现（herdr-web 的 chat 模式）

上面是**别人怎么做的**，这一节是我们的取舍。代码在 `internal/transcript`（读转录）、
`internal/server/chatapi.go`（一个只读的口）、`web/src/components/ChatPanel.tsx`（那块界面）。

### 9.1 和 §1–§8 的三处出入（herdr 0.9.0 实测，2026-09-21）

调研那会儿是 herdr 早一点的版本，现在这三条变了，而每一条都让实现变简单：

| §8 说的 | 现在的实况 |
|---|---|
| 自己装 claude hook 拿 `transcript_path` | **herdr 自己会装**：`herdr integration install <17 个 agent>`，我们不碰用户的 `~/.claude/settings.json` |
| 自己维护 `pane ↔ session` 映射 | **herdr 存着**：`PaneInfo.agent_session`，`pane.get` / `pane.list` 直接给 |
| 「herdr 愿意存但不暴露」 | 暴露了，但**只给一个 ref**：`{source, agent, kind, value}`，`kind` 只有 `id` / `path` 两档 |

herdr 装的那个 hook 比 Moshi 那 9 个轻得多 —— **只挂一个 `SessionStart`**
（`~/.claude/hooks/herdr-agent-state.sh`，codex 那份在 `~/.codex/`），因为它只要会话身份，
不负责搬运内容。脚本里三道闸：`HERDR_ENV=1` / `HERDR_SOCKET_PATH` / `HERDR_PANE_ID` 都得有
（所以只在 herdr pane 里生效），`agent_id` 非空就退出（**子 agent 不报**，不会把主会话顶掉）。

实测下来有两条要记住：

- **`kind` 实测是 `id` 不是 `path`**，尽管 claude 那个 hook 把 `transcript_path` 一起报了
  （codex 那个压根不报 path，只是要求 payload 里有）。所以 **id → 文件路径这一步必须自己做**。
- **hook 只在 agent 启动那一下报一次。** 装之前已经跑着的 agent 一律没有 `agent_session`
  （实测这台机器上 19 个 agent pane 全是 `null`，字段在响应里直接不出现）。表现是
  「装完了 chat 还是说没有会话」，而人会以为装失败了 —— 界面上必须把「重开一次」说出来。

### 9.2 id → 文件路径：**不照抄目录名变形规则**

claude 的项目目录是 cwd 变形来的（`/Users/bysir/dev/bysir/herdr-web` →
`-Users-bysir-dev-bysir-herdr-web`，点号之类也一起折成 `-`）。照抄这条规则是个静默坑：
规则是别人的、会变，而变形错一个字符的表现是「chat 永远打不开」，报错只说文件不存在。

所以判据是 **session id 全机唯一**：

```
claude   先按变形拼一次（命中就一次 stat）→ 拼不中就 glob `~/.claude/projects/*/<id>.jsonl`
codex    session id **就在文件名里** → glob `~/.codex/sessions/*/*/*/rollout-*-<id>.jsonl`
```

实测代价可以忽略（45 个项目目录 / 1061 个 rollout），而且结果带 30 秒缓存 ——
轮询按秒来，而这是**在跑着 agent 的那台机器上**扫目录。

### 9.3 没装 hook 时：**同一个 cwd 有多个 agent pane 就不猜**

退化那条路（按 cwd 找最近那一份）是给「装 hook 之前起来的 agent」留的，但**猜错的代价特别隐蔽**：
显示的是隔壁那个 pane 的对话，而两边都在同一个项目里干活，屏幕上看着完全正常。
一台机器上开着几十个 pane 是常态（实测 54 个 / 19 个带 agent，其中 `herdr-web` 这一个目录下就
有两个 claude pane），所以这不是边缘情况。

判据是「同一个 cwd + 同一个 agent 的 pane 有几个」：>1 就报 `ambiguous` 并**说清为什么不猜**。
`Siblings` 只在没报过会话时才去数（多一次 `pane.list`）—— 装了 hook 的正常情况下一次
`pane.get` 就够。

### 9.4 边界：这一层自己钉，**不借文件浏览那一处**

看 diff 那条路是借 `files.Browser.Check` 当唯一鉴权点（见 `internal/gitdiff`），这儿不能照抄：
转录在 `~/.claude` / `~/.codex` 下，而 `HERDR_WEB_FILE_ROOTS` 一配就是个 jail、那两个目录几乎
肯定不在里面 —— 借它的话「配了 FILE_ROOTS 的部署上 chat 永远打不开」。

换来的义务是自己把路径空间钉死，三条都有测试盯着（`internal/transcript/locate_test.go`）：

1. **id 必须过正则**（只认字母数字和 `-_`，点和斜杠一个都不放）。它要拼进路径和 glob，
   `../../etc/passwd` 一放进去就是任意文件读取，而这个服务是从公网点得到的。
2. **`kind:"path"` 必须落在对应 agent 的根底下**。那个路径是 hook 报上来的、herdr 只转发不
   校验 —— 「相信 herdr」等于「相信任何能连上那个 socket 的东西」。
3. **必须是 `.jsonl`**。少了这条，一个被改过的 hook 报 `~/.claude/.credentials.json` 就能把
   凭据渲染到页面上 —— 那个文件真在 `~/.claude` 底下。

第 2、3 条那个测试里那几个「该被挡掉」的路径**都真建了出来**：不建的话它们是被「文件不存在」
挡掉的，那样的测试在边界检查被删掉之后照样会过（等于没测，验过一遍：拆掉检查测试确实会红）。

### 9.5 读法：尾部开窗口 + 按字节偏移做增量

文件能到 12MB+（上游报过 17MB），所以：

- **首屏从文件尾往前开一个 1MB 的窗口**，只给最后 200 条。窗口起点落在行中间，那半行要丢掉。
- **增量按字节偏移**（响应里的 `next`），下一拍从那儿接着读。
- **`next` 只指向完整行的边界。** 文件正在被写时最后一行可能只落了一半，把它算进 `next` 的话
  下次就从半行中间接着读，**那一条消息从此永远丢了**，而且一个字都不报。
- **「一窗够不够」的门槛不能是 200 条。** 踩过：一份 2.3MB 的转录头一窗出了 181 条，判成不够，
  于是一路翻倍**把整个文件读完** —— 那正是这个窗口要避免的事。门槛改成 30 条（手机上已经是
  好几屏），真出不到 30 条（尾部正好是一大段工具输出）才值得再往前挖一窗。
- 读不出新东西**不是错**：agent 正在想的时候文件就是不动（§5 实测能 15.58 秒零字节）。

**往上翻更早的**（`ReadBefore`）：前端拿上一批的 `start` 当 `before` 再要一段，接在前面。
三条都是真机上抓出来的：

- **`more` / `start` 只能从「整份读」那一次采纳。** 增量那次只读了文件尾新增的一段，对顶端
  一无所知（服务端那条路压根不设这两个字段）。写成每拍都盖的表现是：首屏说了「上面还有」，
  第二拍（3 秒后）把它覆盖成 false，**「看更早的」那个按钮活不过一拍就消失** —— 而这正好
  和「翻不到历史」长得一模一样。
- **偏移只在同一条会话里有意义。** `/clear` 之后 claude 写的是**另一个**文件，而前端手上那个
  `from` 是上一份的字节偏移 —— 套在新文件上就是从中间某处开始读：前面那一截永远读不到，
  一个字都不报。所以请求里带上手上那份的 `sig`，服务端核不过就把偏移丢掉。
- **接上去之前记住位置，接完把长高的那一截补回 `scrollTop`。** 往前面插内容会把正在看的
  那一块往下顶，不补的话手一松屏幕就跳走（Safari 不支持 `overflow-anchor`，指望不上浏览器；
  和 DiffViewer 那条同一个问题）。真机实测：多读 47 条、内容长高 1272px、补 1273px，
  参照块在屏幕上**漂移 0px**。
- 首屏那一批**有意和往前翻的那一批重叠**：截掉的那几条的字节偏移我们并不知道，所以 `start`
  报的是整窗起点，前端按 id 去重。反过来（把 `start` 报成截断处）会**漏掉**中间那几条，
  而且完全看不出来。

### 9.6 渲染：哪些进对话流，哪些不进

**claude** 只认 `user` 和 `assistant` 两种行，其余（`attachment` / `ai-title` /
`permission-mode` 那些状态快照）一律跳过。三条按 §7 来：工具结果是以 `user` 角色记的
（`content` 是**字符串**才是人说的话，是**数组**就是 `tool_result`）、一行只是半个回复
（每个 content block 各占一行）、`isSidechain` 的不进主流（子 agent 的活在主线上只是一条
Task 工具调用）。ESC 打断靠扫 `[Request interrupted by user]` 认（§3.5：那个不触发 hook）。

**codex** 读 `event_msg` / `item_completed`，**不读 `response_item`** —— 后者贴着 API 层，
混着三种读不了的东西：`role:"developer"` 的整段系统提示（实测单行 55KB）、只有
`encrypted_content` 的 reasoning（`summary` 是空数组，拿不到任何可读内容）、以及输入是一段 JS
的 `custom_tool_call`。`item_completed` 给的是归好类的 item（`UserMessage` / `AgentMessage` /
`CommandExecution` / `FileChange` / `ImageView` / `Extension` / `ContextCompaction`），
`command` / `exit_code` 样样现成。两个小坑：`AgentMessage` 里 content 的 type 是大写 `Text`
而 `UserMessage` 里是小写 `text`（**按小写比**，不然 agent 说的话一条都认不出来，完全静默）；
`command` 是数组时前两截是壳（`["/bin/zsh","-lc","真正的命令"]`），直接 join 的话每条命令前面
都顶着一串 `/bin/zsh -lc`。

**工具只送「在干什么 + 成没成」，不送输出。** 一条 `CommandExecution` 的 `aggregated_output`
能有几十 KB，而手机上要看的就是那一行摘要。`ok` 是**三态**：`undefined` = 结果还没落盘
（画「正在跑」）—— 画成成功的话「正在跑」和「跑完了」看着一样。

**这儿刻意不做「连着两条一样就去重」。** `agentwatch` 那边有这么一条，但它的前提是读屏会重复
读到同一屏；转录是纯 append 的事件流，每一行都是一件真发生过的事 —— 而「继续」连说两遍是
再正常不过的用法，去重就是把人真说过的话吞掉。

**机器注入的那几种「块」不是人话，而且原样显示就是一坨标签。** 用户报的是后台任务通知：
`<task-notification><task-id>…</task-id><tool-use-id>…</tool-use-id><output-file>…` 在手机上
占了整屏一个气泡，而人要看的只有里面那句 `summary`。这类块都以 **user 角色**记着，所以会顺着
人话那条路画出来。全机 45 个项目扫过一遍，只有这几种：`task-notification` 168 条、
`local-command-stdout` 27、`bash-input`/`bash-stdout` 各 26（`command-*` 和 `pasted_content`
早就在剥了）。现在的画法：**后台任务 / 命令输出画成一行小字**（`后台任务跑完了：<summary>`），
而 **`bash-input` 照旧是气泡**但显示成 `! 命令` —— 那是人的动作，不是机器的话。

三条：① **判据必须是标签名白名单**，不能写成「以 `<` 开头就算」—— 人话里真有 `<https://…>`
这种写法（实测 2 条），一刀切就把它吃掉了（和 `cmdHead` 那条锚定同一个教训）；
② 形状变了（比如没有 `summary`）**也别退回显示标签**，把标签抹掉当散文读；
③ **剥 ANSI**：斜杠命令的输出里真的带（`/permissions` 回的是 `Approved \x1b[1m…\x1b[22m`），
不剥的话屏幕上是字面的 `[1m`。

**`content` 是数组**不等于**「这是工具结果」。** §7 那条「字符串才是人说的话、数组是
`tool_result`」只对了一半：**人说的话只要带了附件（贴图）就也是数组**
（`[{type:"text",…},{type:"image",…}]`）。只捞 `tool_result` 的后果是**带图的人话一个字都
不进对话流**，而且完全静默 —— 用户报的是「我在电脑上终端发的这条，手机 chat 里看不到」，
而纯文字那几条好端端在（这个「有的行有的不行」正是判据漏了一种形态的样子）。

同一个根因还坑掉了 §3.5 那条**打断记号**：它**只以数组形态出现**（这台机器上 45 个项目的
转录实测「字符串 0 条 / 数组 24 条」），也就是说那行「被打断了」的小字**一次都没出现过**，
判据当初写在了永远匹配不到的那条路上。所以两条路现在共用同一份处理（`claudeSay`）——
各写一份就是这么走偏的。

分得清是因为这三种组合**互斥**（同一批转录实测：`tool_result` 5870 条、只有 `text` 的
14 条全是打断记号、`image,text` 10 条全是带图的人话）。**图片本身不带出去**：claude 那边
一张贴图是几百 KB 的 base64（实测 233KB），而这条路按秒轮询、要过隧道发到手机上；正文里
claude 自己留了 `[Image #10]` 这个记号，所以取文本块就够，真的一个字没打才补一句 `[图片]`。
codex 那边贴图是 `local_image` + **本机路径**（不是 base64），而 `cxText` 本来就是遍历所有
块只挑文本的，所以那边原本就对，只补了「只贴图」的占位。

**剥壳那一类判据必须锚在消息开头。** 人敲 `/clear`，claude 记的是一坨
`<command-name>/clear</command-name>` 标签，所以剥壳这一下是「**整条消息**换成命令名」。
判据不锚的后果不是少显示一点东西，而是**显示出一件没发生过的事**：上下文压缩后那条
`isCompactSummary` 的 summary 里正好写着这串标签（记的就是这个坑本身），于是几万字的 summary
在对话流里变成一个孤零零的 `/clear` 气泡（用户报的）。现在 `cmdHead` 要求整条**以**
`<command-…>` 开头，「抹掉剩下的标签」那个兜底也归在同一道闸后面 —— 人自己引用这串标签时
该原样显示。`TestClaudeCompactBoundary` 盯着，去掉锚定它就红。

**上下文压缩要有个交代，而那份 summary 不算人话。** 压缩在 claude 的转录里留两条：
`type:"system"` + `subtype:"compact_boundary"`（带 `compactMetadata`，`preTokens`/`postTokens`
都在里面），紧跟一条 user 行带 `isCompactSummary: true`。前者出一行小字「上下文被压缩了」
（codex 那边早有这条，claude 这边一直空着，表现是对话流「凭空断了一截」），后者**整条跳掉** ——
它以 user 角色记着，不挡就是对话流里插进一条几万字的「人话」。**判据用那个字段，别嗅正文开头
那句英文**（`This session is being continued…`）：那是宿主的措辞，改了我们就原样把 summary
当人话显示出来。同一行上还有 `isVisibleInTranscriptOnly`，意思一致 —— claude 自己的界面也不把
它摊在对话流里。

### 9.7 读那个口只读，写口只有两个而且形状固定

**没有发送框**：发言用底下那一行发件箱（`agent.prompt`）。另开一条通道就要把 [OUTBOX.md](OUTBOX.md)
里那几个坑再踩一遍 —— 清空要 2N−1 次、`text:…enter` 的回车要隔 200ms、回车发送必须挡输入法。

写口现在有两个，**共同点是「参数不是要发的东西」**：`answer` 收的是**每题选了哪几个选项
（下标）**（按键序列在服务端按下标算，见下面那段），`start` 收的是**白名单里的一个名字**
（claude / codex，命令名在服务端查表）。所以这两个口即使被别的东西调，也只可能发出那两种形状，发不出任意文本 ——
「往任意 pane 发任意文本」那条通道**刻意没有**（真要别的命令，快捷键条上配一个
`text:xxx enter` 的键，那条路本来就是干这个的，还带着回车隔 200ms 那道）。

**`answer` 那套按键协议是量出来的，不是猜的。** 拿一个隔离的 tmux（独立 socket）+ 真
claude 2.1.278 逐键试出来的，和量 codex 那个粘贴判据一个办法。结论：

	单选题            发该选项的**序号**（1-based）→ 选中 + **自动进入下一题**
	多选题            发每个要选的序号 → **切换勾选**，高亮不动、不跳题
	换页              `tab` → 下一个标签；最后一题之后是 Submit 页
	提交              Submit 页上发 `1`（那页第一项就是 `1. Submit answers`）
	例外              **只有一个问题且是单选**时，序号本身就提交了，没有 Submit 页
	                  （多补一个 `1` 会被打进输入框）

这套比原来那串 `↓ ×n + ↵` 强在一处：**数字键和高亮停在哪完全无关**（实测：先按两下 ↓ 把
高亮移到第 3 个，再发 `2`，记下来的是第 2 个）。原来那串假设「高亮停在第一个选项上」，人只要
先在终端里按过方向键就会选错 —— 那正是当初只敢做单题、还要在界面上提醒「别按方向键」的原因。
这个前提没了，所以多题 / 多选能做，界面上那道二次确认也跟着去掉了。

**怎么把这几下发出去，有三条全是静默失效的**（都在真 pane 上量过，对应 `sendOneByOne` 的
①②③）：

① **数字必须走 `keys`，不能走 `text`。** herdr 的 `pane.send_input` 给 `text` 时会**按那个 pane
   当前的 bracketed-paste 状态**编码，而 claude 的 TUI 开着 DEC 2004 —— 于是一个数字被当成
   「粘进来的一段文字」，**选择器压根不理**。这条的误导性极强：herdr 不报错，往 `cat -v`
   那种没开 2004 的 pane 里发看到的又是裸字节（我就是这么先得出「字节没问题」这个错结论的），
   而真实后果是**「点了提交，TUI 里一个选项都没选上，标签却往后翻了」**（用户报的 ——
   因为 `tab` 走的是 `keys`、照样生效，所以「点一次跳一题」）。同一张卡上 A/B：
   `send_input{text:"1"}` **无效**；`send_text{text:"2"}`、`send_keys{["3"]}`、
   `send_input{keys:["4"]}` 三个都有效。**数字本身是合法键名**，所以统一走 `keys`。
② **一个键一次调用。** 一次 `send_keys{["2","3","4"]}` 实测只有**最后一个**生效，另外两个
   静默丢掉。
③ **两下之间要留间隔。** 背靠背三次调用（总共 1ms）只有**第一下**生效；实测 10ms 就够，
   代码里取 80ms 留余量（6 个键也才 0.5 秒，而丢一下就是答错）。

顺带解释了为什么原来那串 `↓↓⏎` 一次发能用、从来没暴露过 ①：那些是转义序列，herdr 按键
编码之后 claude 逐个解析，不走「粘贴」那条路。

两个跟着来的约束：① 序号是**单个数字字符**，所以选项超过 9 个这条路发不了（`pendingAsk` 里
挡着，前端判据要跟它一样，不然是「点了报错」）—— AskUserQuestion 的 schema 上限本来是 4 个，
这只是道保险；② **每题都必须有选择**：缺一题的话 Submit 页会拒，而前面几题的按键已经发出去了,
停在一个半填的选择器上比什么都没发更糟，所以服务端先核完再发、发不出就一个键都不发。
`tab` 走命名键是因为 herdr 对不认识的名字会**报错**（实测 `unsupported key nosuchkey`），
不是静默吞掉。

`start` 是给「焦点落在一个 shell pane 上」那一屏用的（原来只剩一条「回到终端」，用户报的
希望能直接在这儿开）。三条：① **pane 里已经有 agent 就拒**（409 `has_agent`）—— 不拒的后果
不是白开一个，而是把 `claude` 这五个字母当成一句话投进正在跑的那个 agent 的输入框，而前端
手上那份 pane 列表最多 3 秒旧（人可能刚在终端里自己开了）；② **走 `pane.send_input` 不走 PTY**
—— chat 在终端 WebSocket 断着时照旧能用（§9.8 那条状态点），按钮跟着终端连接一起失效说不通；
③ **这儿不需要「回车隔 200ms」** —— 那道是给 codex 的输入框看的（`paste_burst.rs`），而开
agent 敲的对面是 zsh 的提示符，没有那个启发式。界面上还有一条：点下去**绝不能停在
「正在开…」上** —— 失败当场在按钮底下写红字（不发 toast，那个贴在整屏最下沿而眼睛在屏幕正中），
成功也有 25 秒兜底说「敲下去了但还是没检测到」，因为 agent 起不来 / herdr 不报都是真会发生的。

审批**没有**在这儿留入口，尽管 Moshi 有（§3.5）。理由在 [TUI-VS-GUI.md](TUI-VS-GUI.md) §2 ①：
会改状态的事留在终端里，而 Moshi 那条路是「读屏校验 + 发按键」—— 正是这个项目里最贵的那类
判据（错了不报错），而它决定的是「允许/拒绝一次写操作」。

### 9.8 界面上的几条

- **面板不铺满屏**（和 DiffViewer 不一样）：底下那一行发件箱必须还点得到 —— 一边看一边说
  才是这块界面的用法。
- **「看哪个 pane」只给焦点那个工作空间里带 agent 的几个**，按 `workspaceId` 认
  （`workspace` 是标签，两个工作空间同名是常态）。和改动面板同一条教训 —— 那条是用户报的
  bug 换来的：全铺出来的话默认落在哪个上面全看顺序。
- **「焦点在哪」只能有一个来源，而且要跟发件箱同源。** 用户报的是「我在别的终端里用 herdr
  切了 pane，发件箱识别到了，chat 没有，两边对不上，我会发错内容」—— 而这不只是显示不一致：
  投稿跟的是**发件箱**解析出来的焦点，而人读的是 **chat** 那一屏，于是「看着 A 的对话，
  话发给了 B」。成因是两个来源刷新节奏不同：发件箱那条心跳（`/herdr/sync`）**每拍都现问
  herdr**（服务端 `resolve` 走 `pane.current`），而 `panes` 那份列表只在**事件**时重拉
  （开 chat / 开面板 / 自己点 goto），chat 用的正是列表里那个 `focused` 标记 ——
  人在别的终端里切 pane 时一个事件都没有。实测静置时 `/herdr/panes` 是 **0 次**。
  治法是让那一拍**发现换了就把列表一起刷新**（`useCompose` 的 `switched` 分支）：一次真
  切换一次调用，静置照旧 0 次（`/herdr/panes` 要在跑着 agent 那台机器上问几十个 pane，
  实测 55 个，每拍白拉不值当）。两条跟着来的：① **开页那一拍不算切换**（`resolved` 是空串，
  必然满足判据，而那会儿列表刚拉过）；② **那个抢跑提示（`focusHint`）必须有时限** ——
  它原来只在「列表里的焦点 == 提示」时清掉，而 herdr 的焦点落到**第三个** pane 上时那个
  相等永远不成立，于是提示把 chat 永久钉在我们以为的那个 pane 上（同一个「发错内容」）。
  它盖的只是两次往返（实测一百多毫秒），所以 2 秒兜底一到就交还给列表里那个 `focused`。
- **`sig` 变了就把手上那份整份丢掉**（`/clear`、`/resume`、压缩都会换会话）。不丢的话新旧两段
  接在一起，看着像 agent 突然跳回上一个话题。
- **「改了 DOM 再把滚动位置纠正回来」那一类必须放 `useLayoutEffect`**，普通 effect 是绘制
  **之后**才跑的，所以中间那一帧一定看得见。切 pane 时是两帧都坏（用户报的「闪动非常明显」
  「像先滚到顶部再一下子滚到底部」）：① `active` 一变 React 先提交一帧，那时 `msgs` 还是
  **上一个 pane 的对话**（配着新 pane 的头）；② 换上新对话那一帧用的是**旧的 `scrollTop`**。
  两个 effect（换 pane 那个 + 自动贴底那个）都挪进 layout 就没有中间态了。
  量过：拿 MutationObserver 逐批记 `scrollTop`（它在 React 那次同步提交之后的微任务里跑，
  所以「纠正在 layout 里」和「纠正在下一个任务里」分得开）—— 普通 effect 那版 14 批里有
  **1 批**是 `scrollTop=0` 而内容已经是新 pane 的（5511px、离底 4728px，正是那一下「跳到底」），
  改成 layout 之后 15 批 gap 全是 0。顺带一条测试环境的坑：这种验证**在后台标签页里做不了**
  —— rAF 不跑、定时器被节流到 1 秒，而 chat 自己那一拍第一行就是 `if (document.hidden) return`
  （表现是屏幕上写着「这条会话里还没有对话」，看着像 bug），得先把那道闸在测试里顶掉。
- **只有人贴着底时才自动滚**（48px 容差）—— 人往上翻历史时把他拽回底下是最烦的一种
  （和 DiffViewer 的 `selfScroll` 同一条：别跟人抢滚动）。
- **容器一变矮就得重新贴底**（ResizeObserver）。手机上呼输入法是最常见那一下（用户报的
  「弹出输入法会导致自动贴底不生效」）：视口一缩，滚动容器的 `clientHeight` 跟着变小而
  `scrollTop` 不动，于是「原来贴着底」当场变成「离底还有半屏」—— 而那一下**不触发 scroll
  事件**（容器变矮只是把 max scrollTop 变大，浏览器没有理由去夹 scrollTop），光靠 onScroll
  发现不了。盯容器自己的尺寸而不是 `visualViewport`：呼键盘、转屏、快捷键条开合、拖发件箱
  把手在这儿是同一件事。**不做防抖** —— 视口是一格一格变过来的（见 CLAUDE.md），每一格都
  贴一次才跟得住键盘那段动画。
- **人没贴底时，新动态要给个提示**：底下浮一颗药丸，有新的就说「N 条新动态」（品牌色描边），
  没有就只是「回到底部」。点它**瞬时跳**，不用 `smooth`：真机上量过这一跳常常五千多像素，
  smooth 那段动画期间状态对不上账 —— 已经把「贴底了」置上（药丸消失）但中途每次 scroll
  算出来还是「离底很远」（药丸闪回来），期间来新消息还会重新计数。
- **刚投出去的话先本地回显**：投稿 → agent 写进转录 → 我们轮到，中间有几秒空窗，那几秒
  屏幕上一点动静都没有，看着像没投出去（用户报的）。回显只画「投给当前这个 pane」的
  （不然会在 A 的对话里看到投给 B 的话 —— 发件箱默认「跟随焦点」，投到哪儿由服务端解析），
  真的那条从转录里读回来就按**原文**比着撤掉（id 是 agent 生成的，我们没有），
  60 秒还没被顶掉就不再画（一直挂着「投递中」比没有更让人不放心）。
- 思考默认**折起来**（比正文长好几倍），工具占**一行**不占气泡（一次 turn 十几条是常态）。
- **Markdown 用真解析器，按需加载，永远不开原始 HTML**（`components/ChatMarkdown.tsx`）。
  三条：① 转录里那些字是 agent 输出的，在这一层等于**不可信文本**，而这个页面能调
  `/api/herdr/say` —— 所以**不装 `rehype-raw`**，`urlTransform` 只放 `http/https/mailto`
  （`javascript:` 在 `<a href>` 里点一下就是同源脚本执行，而「agent 输出里有条链接」再正常
  不过）。和文件浏览那条「吐内容绝不能是 `text/html`」是同一类规矩；② unified/micromark
  那一套有几十 KB，所以 `lazy()` 按需拉 —— 首屏那几个连接是要紧的（「刷新页面顶部始终闪动」
  那条 bug 就是首屏抢连接抢出来的）；③ **中文里的 `**加粗**` 要靠 `remark-cjk-friendly`**：
  CommonMark 判强调符看左右挨着什么（flanking 规则），而那套规则按西文空格和标点定的，
  星号紧贴中文标点时不算强调符 —— 于是 `**「对话」**` 解析不出来、`**` 原样显示在屏幕上，
  而 agent 写中文时几乎全是这种写法（真机截图里抓到的）。代码块**横向滚不折行**：
  代码的缩进和对齐本身带信息，而正文折行是对的（那是 chat 存在的理由之一），两件事别混。
- **「给终端写的那几件东西」在 chat 模式下一件都不能漏。** chat 是个**模式**，终端那会儿不在
  屏幕上，所以凡是「说终端状态」的界面都要跟着换说法或者不画：左上角那个点（它说的是哪条
  连接）、顶栏那个「连接」按钮、跳转那条 toast、以及**「终端没连上」那整张遮罩**。
  最后这张是漏掉的那件（前三件当初一起改了）—— 它 `z-5`、盖在终端那一层上，而 chat 面板是
  `z-6`，所以竖屏下看不见；**横屏才现形**：那张遮罩是 `grid place-items-center`，内容
  （logo + 标题 + 一段话 + 按钮）比 `<main>` 高时居中会往**上下两头**各溢出一半，而 Dock
  没有 z-index —— 于是那个「连接」按钮画到了发件箱那一行上面（用户报的）。它不只是难看：
  实测那一下**会吃掉本该点到投稿键的点击**（`elementFromPoint` 在那个位置上返回的是那个
  按钮）。两条一起治：① `overlay && !chatOpen`；② 居中改成「装得下就 `my-auto`、装不下就
  自己滚」（`flex justify-center overflow-y-auto` + 子元素 `my-auto`）——
  **别写 `items-center` + `overflow`**，那个组合下溢出的上半截是滚不到的。
  门槛量过：`main` 矮于约 190px 就开始溢出，而手机横屏正好是这个高度（修好之后同样高度上
  那个位置命中的是发件箱，按钮被裁掉，滚到底又能正常点）。
- **`components` 里那几个组件必须在模块级** —— react-markdown 拿它去 `createElement`，
  所以**函数引用一变就是「换了个组件类型」**，React 把那棵子树整个卸掉重挂。写成
  `components={{ a: (p) => …, pre: (p) => … }}` 的话每次渲染都是新函数，而这个面板在 agent
  跑的时候每秒重渲染一次（那个计时），于是**每秒**把所有 `<a>` / `<pre>` / 路径 span 销毁重建，
  里面的 DOM 状态全丢：代码块和表格的横向滚动位置跳回开头、选中的文字被抹掉
  （用户报的「每 3 秒整个重绘，表格滚到最后了又跳回开头」）。真机上量得很干净：那个路径
  span 的节点 4.5 秒内就不是同一个对象了，而默认标签（`table` / `td`）活着 —— 差别正好落在
  「有没有自定义组件」上。`onPath` 是唯一的动态输入，让它走 **context** 而不是闭包，
  组件身份就永远不变（调用方每次传新函数进来也不会重挂）。
  外面再**按 `text` memo 一层**：光固定身份只治了「重挂」，没治「重算」—— 每次渲染
  react-markdown 都要把整段正文重新过一遍 unified，一屏四十个气泡、每秒一遍，手机上是白烧的。
  顺带一条：`node` 要从透传里摘掉（react-markdown 会把 hast 节点一起传进来，spread 到
  `<a>` 上是个非法 DOM 属性）。
- **「在等你答」和「我们能不能代答」是两个判据，别合成一个。** 一键作答只支持单题单选
  （多选是空格勾选、多题要一题一题走，按键序列都不一样），原来那一个值同时承担两件事 ——
  于是多题 / 多选那种被一路当成「不是在等你答」，卡底下那行小字落到最后那句
  「这个问题已经答过了」：agent 明明红着「在等你回答」，屏幕上却说你答过了（用户报的
  「我没有答啊 为什么说我答过了」）。**这个错的方向最糟** —— 人会因此不去答，而对面一直卡着。
  现在分成 `pendingAskID`（最后一条工具调用是提问且没有结果）和 `answerableAskID`
  （其中单题单选那种）：前者决定「这张卡是活的」，后者决定「给不给点」。跟着来一条渲染上的：
  **在等你答但代答不了时选项不压暗** —— 那时候这几个选项正是你要读的东西（你在终端里答、
  照着这儿看），压暗只留给答过的历史（那时候压暗是为了让「选了哪个」一眼跳出来）。
- **「给终端的那些键」在 chat 模式下照旧能用，但发不出去时必须说话。** `/clear`、Esc 打断、
  新标签这些没有在 chat 里另做入口 —— 快捷键条和发件箱在 **Dock 里、在 `<main>` 外面**，
  所以 chat 面板铺满 main 也盖不住它们，那排键一直在（出厂条上就有 Esc；`/clear`、
  `新标签 ctrl+b c` 在编辑器「常用」里，拖上去就行）。**刻意不在 chat 里重做一套**：
  那就是第二份「act → 动作」的映射，而这个项目已经为「散成三份」付过代价（见
  internal/capability 的包注释）。
  但有一条得补：这些键走的是**终端那条 WebSocket**，而 `session.send` 在连接不是 OPEN 时
  **静默丢掉**。平时看得见终端所以无所谓，chat 模式下终端不在屏幕上（断着照旧能看对话），
  于是按 Esc、按 `/clear` 全都「点了没反应且不报错」（用户问「chat 模式下怎么 clear /
  打断」就是撞在这儿）。所以 `sendKeyBytes` 里先看连接状态：没连上就说清
  「发不出去，正在连，连上再按一次」并顺手重连（重连没有代价），**别谎称发出去了** ——
  那一下按键是补不回来的。
- **撤回的消息不能留在对话流里 —— 转录是树，只画当前那条分支。** 人在 TUI 里按 Esc
  **撤回一条还没被回复的消息**时，那条**照旧留在文件里**（`type:"user"` 实实在在写着），
  只是后面的记录不再从它往下挂 —— `parentUuid` → `uuid` 串成的是树（§4），它掉出了当前
  分支。线性地把每行都画出来就是「TUI 里已经撤掉的消息，chat 里还在」（用户报的：投了
  几条「哈哈」、都在 TUI 里撤了，chat 里一直挂着）。`/rewind` 是同一件事。
  **判据不能是「它有没有孩子」**：撤回那条下面仍然挂着自动附件（真机上核过，
  `total_tokens_reminder` 的 parent 就是它）。只能**从最后一条往上走链**（`onBranch`），
  掉出链的才算撤掉。两条保守处理，宁可多留也不错杀：① 链只走到走不动为止（父亲在这一窗
  外面、或者 `parentUuid` 为空 —— 压缩边界那种就是 null），**比链的起点更早的一律保留**；
  ② 没收到 uuid 的记录类型也保留。sidechain **不收进链**（子 agent 是另一条分支，走进去会
  把主线带跑偏）。`TestClaudeDropsRetracted` / `TestClaudeKeepsWholeBranch` 一正一反盯着。

  **光在服务端滤掉不够，还得指名告诉前端「这几条没了」**（`Log.Gone`）：前端只会追加、
  不会删，而被撤的那条早就作为增量送过去了 —— 服务端之后不再送它，屏幕上那条却一直挂着，
  只有整份重读（换会话 / 重开面板）才消失（用户报的第二遍）。这和 `Updates` 是同一个形状、
  同一个理由：**证据出现在后面那一批里**。
  跟着来一条：**增量那一拍要多往前看一段**（`lookBack`，128KB）才判得出来 —— 被撤的那条
  在增量窗口**之前**，只看这一窗的话它压根不在，说不出 id（`TestClaudeGoneInIncremental`
  就是先红在这儿）。那一段**只用来判分支，刻意不把它的消息并进 `Msgs`**：前端拿「增量批次里
  冒出人话」当「投稿落地了」的判据（`dropLanded` 的 fifo），重叠送旧人话会把还没落地的
  回显误撤掉。

  顺带记一条**试过又撤掉的**：先前我以为「撤回的消息留着是对的」，于是加了一行
  「这条还没被处理（按过 Esc…）」去解释它 —— 那是错的（用户指出 TUI 会撤回），而且那行字
  在「连投几条、最后一条还没轮到」这种正常情形下也会冒出来，用户的反馈是「不明所以」。
  **空着比编一句好**；真·被打断（打断在工具调用 / 输出中途）转录里有
  `[Request interrupted by user]`，那条照旧画成一行小字。
- **改了对面状态的动作要立刻补一拍，别等那 3 秒。** 按 Esc 打断、`/clear` 这类键按下去之后，
  对面马上就变了，而 chat 是 3 秒一拍 —— 等下一拍才看到反应就是两三秒的空窗，人会以为
  「没点成功」（用户报的）。所以 `sendKeyBytes` 发完就把一个计数器 +1，chat 那边据此立刻读
  一次（`nudge`），延迟从一拍变成一个来回。**这个计数器不能进轮询那个 effect 的依赖** ——
  那会把整条心跳重建一次（清掉计时器再从头排），而要的只是「额外读一次」。
  一键作答那条早就这么做了（答完 `void tick(active)`），这儿是把同一条规矩铺到所有按键上。
- **「把 TUI 里撤回的消息也藏起来」试了两版、两次都错杀了活消息，已经下线 —— 别再做第三版，
  除非找到 claude 明确写下来的记号。** 撤回一条**还没被回复**的消息时，那条照旧留在文件里
  （`type:"user"` 实实在在写着），只是会话从它父亲那儿另开一支（转录是树，§4）。看着很好认，
  但两次都翻车：
  ① **「不在最后那条的链上」** —— 排队中的消息挂在入队那刻的叶子上、被打断之后后面的记录
     自然绕过它；一条 assistant 带两个工具结果时那两条 `user` 也同父。当场丢用户的消息。
  ② **「同父的几条人话里，有 assistant 后代的那条才是活的」** —— 在全机 12 个会话上验过
     （6 组同父人话，每组恰好 1 条有后代），还把①的那几类错杀写成了用例，全过；
     真机上还是丢了一条用户发出去的消息。
  **取舍摆正**：多显示一条撤回的消息只是难看；**丢掉一条用户真发出去的消息，是让人以为
  发了其实没发** —— 后者严重得多，而这条路上每一版都在拿后者换前者。所以现在**全都画出来**，
  和 TUI 不一致就不一致。
  真要再做，判据必须是**claude 自己写下来的事实**（比如某个 `queue-operation` 的 reason、
  或者一条专门的记录），而不是从树结构反推 —— 结构反推的每一版都在「活着但看起来像撤回」
  这类情形上碎掉。
- **切 pane 成功了不弹提示**（chat 模式）：头上那行 pane 名 + agent 立刻就变了，那本身就是
  反馈，再弹一句「对话已切到 …」是重复，每切一次弹一次很打扰（用户报的）。
  **但「没切到」那种照旧要弹** —— 屏幕上还在老 pane 而提示说成功，是最难查的一种。
- **切 pane 那个「抢跑」有两处竞态，都表现成「人没操作、过一会自己跳回上一个 pane」**
  （用户报的，而且只在网络卡的时候出现）。先量过一遍确认**不是 herdr 在漂**：连着问了
  88 秒 `pane.current`，只在真点的那一下变过。两处：
  ① **goto 是一次独立的 HTTP 请求，而 herdr 的焦点是「最后一跳说了算」。** 网络一卡，
     点 B 再点 A 的两个请求就可能**乱序到达** —— B 后到，焦点最终停在 B；抢跑提示
     `HINT_CAP`（2 秒）一到就交还给「herdr 说焦点在谁」，屏幕于是自己跳过去。
     → **一次只飞一个，中间那些点击互相覆盖**（`useCompose` 的 `jump`：一个在飞的
     promise + 一个待发的最新目标 `pending`，真活儿在 `jump1`）。最后那一跳必然是最后
     一次点击。
     **第一版是串成一条链，比原来的 bug 更糟**：快速点 a-b-a-b-a-b 会攒下六跳，手已经
     停了它还在一个个往下走（用户报的「我都没操作了，他还在 abab」）。切 pane 不是
     「一串要依次执行的动作」，它是**一个当前值** —— 中间那几次点击的唯一意义就是被后面
     那次盖掉。所以最多两跳（在飞的 + 合并后的最新那个）。
  ② **迟到的那次 goto 回来时还在按自己那套收尾** —— B 那一跳的 `await` 是在点 A 之后
     才回来的，它会把 A 的抢跑提示清掉（跳失败那支）或者改成 B（「herdr 说焦点在别处」
     那支），同一个表现。→ `App` 里的 `hintSeq`：不是最后那次点击的，收尾（清提示 /
     改提示 / 弹提示）**全部作废**。判据只能是**次数**，不能是 pane id —— 同一个 pane
     点两下也是两次点击。
  抢跑本身别去掉：它盖住的是 goto + 重拉列表那两次往返，没有它就是「上面整屏已经是 B 的
  对话，下面还写着 A」（同 §9.8 那条口径不一致）。
- **页面不可见就不轮询**，3 秒一拍，自排队的 `setTimeout` 不用 `setInterval`（网络一慢会叠）。
- 读不出来时按**服务端给的 `reason`**（`need_install` / `ambiguous`）说不同的话，
  **不按错误文案 `includes()` 判断** —— 那是最脆的耦合，改一个字就静默失效
  （为此在 `lib/api.ts` 里加了 `ApiError` 把结构化字段带出来）。

### 9.9 关得掉

`HERDR_WEB_CHAT=0` → 口一律 404、`/api/state` 里 `chat: false`、**顶栏那个按钮都不画**
（点开一片报错比没有这个入口更糟，[TUI-VS-GUI.md](TUI-VS-GUI.md) §3 第 5 问）。
这台机器上两个转录根一个都不在（既没装 claude 也没装 codex）时同样为假。

### 9.10 还没做的

- **只支持 claude 和 codex。** herdr 的 integration 有 17 个 agent，每家转录格式都不一样；
  加一家 = `parserFor` 里一行 + 一个 parser + 一份 testdata。
- 图片不显示（`ImageView` / 附件只给路径）—— 想看图走文件浏览那条路。
- 不做 fsnotify 推送，就是轮询。转录的落盘粒度本来就是几秒一块（§6），3 秒一拍看不出差别，
  而推送要在服务端为每个开着的面板留一个 watcher。

## 附录 A：`cctail` —— 跟看任意会话

装在 `~/.local/bin/cctail`（不在本仓库内）。`cctail` 跟当前目录最新会话，`-a` 连历史一起回放，也可传项目目录或 `.jsonl` 路径。

```bash
#!/usr/bin/env bash
set -euo pipefail
FROM_START=0; TARGET=""
for a in "$@"; do case "$a" in -a|--all) FROM_START=1 ;; *) TARGET="$a" ;; esac; done

resolve() {
  local t="${1:-$PWD}"
  [[ "$t" == *.jsonl ]] && { printf '%s\n' "$t"; return; }
  local dir="$HOME/.claude/projects/$(cd "$t" && pwd | sed 's#/#-#g')"
  ls -t "$dir"/*.jsonl 2>/dev/null | head -1
}
F="$(resolve "$TARGET")"

DIM=$(printf '\033[90m'); CYAN=$(printf '\033[1;36m'); GREEN=$(printf '\033[1;32m')
YEL=$(printf '\033[33m');  RST=$(printf '\033[0m')
[[ $FROM_START -eq 1 ]] && START="-n +1" || START="-n 0"

tail -f $START "$F" | jq -r --unbuffered \
  --arg dim "$DIM" --arg cyan "$CYAN" --arg green "$GREEN" --arg yel "$YEL" --arg rst "$RST" '
  (.timestamp[11:19] // "--:--:--") as $t
  | if .type=="user" and (.message.content|type=="string") then
      "\($cyan)\($t)  你\($rst)\n\(.message.content)\n"
    elif .type=="assistant" then
      .message.content[]
      | if .type=="text" then "\($green)\($t)  claude\($rst)\n\(.text)\n"
        elif .type=="thinking" then "\($dim)\($t)  · thinking\($rst)"
        elif .type=="tool_use" then "\($yel)\($t)  ⚙ \(.name)\($rst) \($dim)\((.input|tostring|gsub("\\s+";" "))[0:90])\($rst)"
        else empty end
    else empty end'
```

## 附录 B：复现用的命令清单

```bash
# Moshi 侧
moshi-hook status                    # 配对状态 + 各 agent hook 是否 stale
moshi-hook context                   # 当前 pane 的 herdr/agent 上下文
moshi-hook cwd-list --json           # 扫本机所有 agent transcript
strings -a "$(which moshi-hook)" | grep -E 'internal/(tui|gateway|cli)\.'

# herdr 侧
herdr api schema --json              # 全部 170 个命令
herdr agent get <PANE_ID>            # 无 hook 也能报状态（刮屏）
herdr agent explain <PANE_ID>        # 解释检测依据
herdr pane report-agent-session --help

# JSONL 侧
jq -r '.type' "$F" | sort | uniq -c | sort -rn
jq -r 'select(.type=="assistant") | "\(.requestId)  \(.message.content[0].type)"' "$F"
```

---

## 附录 C：调研过程中的两处自我修正

留档，避免后来者重蹈：

1. **「tui adapter 只有 SendKeys」——错。** 漏搜了 `Capture` / `Exists`。但查清 `Capture` 的调用方（`verifyClaudeApprovalScreen`）后，结论不变：它是发键前的校验，不是内容通道。
2. **「thinking 落盘后隔 28 秒 text 才落盘」——错。** 拿行内 `timestamp` 当了写盘时刻。高频采样证明两条是**同一次写入**下来的，flush 粒度是一次 API 请求。由此也纠正了对"流式"的判断：**请求级流式成立，用户感知到的流式是真的**，之前拿"单次请求内部无写入"去否定整体流式，是用错了刻度。
