# 读屏抽输入框

改 `internal/composer/` 之前先读这份。这块的坑有个共同特征：**错了不报错**，只是悄悄多一个字符、
少一段内容、或者把一个界面控件当成用户的文本投出去。

`internal/composer/testdata/` 里是**真机抓屏**，改这块必须跑 `go test ./internal/composer/`。
那批用例是有牙的：把 NBSP 归一去掉 → 3 个挂；把 `38/48/58` 的子参数消费去掉 → 6 个挂。

配套：socket 那层的语义在 [HERDR-API.md](HERDR-API.md)，发件箱怎么用这块在 [OUTBOX.md](OUTBOX.md)。

## 为什么只能读屏

herdr 的 API 里**没有任何「输入框内容」字段**（grep 过 `composer` / `input_line` / `draft` /
`prompt_text`，全 0）。所以「远端输入框里现在有什么」只能靠 `pane.read` 拿整屏 ANSI 自己切。

## 按 agent 分派

herdr 自己就是这么干的 —— 它的 manifest 在
`~/.local/state/herdr/agent-detection/remote/*.toml`，规则引擎的 region 词汇表里本来就有输入框
概念：`prompt_box_body`、`after_last_prompt_marker`、`after_last_horizontal_rule`、
`last_non_empty_above_prompt_box`。

| agent | herdr manifest 用的 region | 屏幕上的实际形状 |
|---|---|---|
| claude | `prompt_box_body` / `after_last_horizontal_rule` | 上下两条纯 `─` 横线夹住，横线是**前景色** `38;2;136;136;136` |
| codex | `after_last_prompt_marker` | 一段**背景色**块 `48;2;49;52;57`，状态栏不在块内 |
| 其余 17 个 | 没有输入区规则，只有 `whole_recent` / `osc_title` / `bottom_non_empty_lines(N)` | 无从照抄，只能嗅探 |

两点注意：

- 这些 region 是引擎内部给规则求值用的，**API 不暴露**，客户端得自己重做一遍。价值在于照抄它的
  per-agent 选择，而不是自己猜 UI 特征。
- codex 那条 `after_last_prompt_marker` 会把状态栏也圈进去 —— herdr 只需要一个区域喂给 regex，
  我们要的是精确内容，所以用背景色块收边更合适。

定位锚点用提示符字形 `[❯›❱]`，**不认裸 `>`**：否则输入内容里以 `>` 开头的 markdown 引用行会被当成
输入框起点。

## 三个静默出错的坑

**Codex 空框里有 dim 占位提示。** 清空之后框里显示 `Run /review on my current changes`，纯文本层面
和真实输入无法区分。判据是它整段套在 `\x1b[2m` 里 —— dim 文字算 chrome 不算内容。两家的真实输入
都不带 dim（实测 Claude Code 的用户输入完全没有 SGR）。

**`38;2;153;153;153` 里那个 `2` 不是 dim。** 按分号朴素切分会读成 dim，而 Claude Code 的横线正好用
这个色，于是整个输入框被判成占位、返回空。必须按 SGR 规则把 `38`/`48`/`58` 后面的 `2;r;g;b` 和
`5;n` 子参数**整段消费掉**再判断。

**Claude Code 的空输入框是 `❯\xa0`（NBSP），不是空格。** 判空之前先把 NBSP 归一成空格。真正吃劲的
不是判空（`rstrip` 顺手就把 NBSP 干掉了），而是**有内容时去掉提示符后那一个分隔空格** —— 不归一就
匹配不上，正文最前面会挂着一个 NBSP 一起发出去。

## 「认不出输入框」和「输入框是空的」必须分开

`composer.Extract` 返回 `(text, ok)`：

- `ok=true, text==""` —— **认出了框，里面是空的**。
- `ok=false` —— **这一屏上没有输入框**（找不到提示符字形 `[❯›❱]`）。

原来 `ok=false` 那条路会退回「屏幕最后一行非空」。那是**猜**：屏幕最后一行往往是状态栏、上一条命令
的输出、或者 agent 画的某个控件，拉回来就是把这行垃圾塞进发件箱，而用户会以为那是远端输入框里的
字。所以现在**认不出就说认不出**，不退回任何一行。

这个区分不只是显示问题，它撑着投稿的安全闸 —— `Clear()` 一旦把 `ok=false` 的 `""` 当成「框已经空
了」，`Say()` 就会往一个不知道是什么的界面里追加整段文本（追加语义见 [OUTBOX.md](OUTBOX.md)）。所以：

| 调用 | `ok=false` 时 |
|---|---|
| `Clear()` | 报 `NoBox` |
| `Say()` | 拒投，**错误信息和「清不空」那条分开** —— 处置不同：一个是按 Esc 收掉控件，一个是先回到输入框 |
| `Draft()` | 报 `skipped: "no-box"` |
| 前端 | 拿到 `noBox` 时**不覆盖本地草稿** —— 那个 `""` 不代表远端是空的 |

这条是踩出来的：验证「拉回」时随手按了个 `↑`，那个 pane 正显示一个提问控件，503 个字符连着一起投
了进去。

## agent 弹对话框的时候

**agent 弹出选择框 / 确认框时，输入框那块区域画的是那个控件。** 抽取会把整个控件当成「输入框内容」
返回（实测拉回来 255 个字符的 checkbox 界面），投出去就是一堆垃圾。

判据**不能用 `agent_status`**（实测同一种对话框，`idle` 和 `blocked` 都出现过，见
[HERDR-API.md](HERDR-API.md)），也不该去猜 UI 特征。可靠信号是**清不空**：对话框开着时 `ctrl+u`
打不掉那块区域，清空循环收敛不到空。所以 `Say()` 在「清不空」时**直接拒绝投递**并报错，而不是硬投
—— 追加语义下硬投就是 `残留 + 新文本`。

## shell pane 是例外

shell pane 抽不出可靠的输入框（提示符五花八门，`➜` 就不在认的字形里），所以那边**不读屏**：
`Clear()` 拍固定次数的 `ctrl+u`（zsh/bash 一次就干掉整个 buffer），投稿照样能用。

代价是 shell pane 上没有「清不空就拒投」这道闸 —— 那条路本来也没有 agent 对话框。
