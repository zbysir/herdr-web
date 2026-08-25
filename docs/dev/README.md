# 开发文档

**这里面全是「改代码之前要读」的东西**：为什么这么设计、在真机 / 真 pane 上实测出来的语义、
以及一串**会静默出错**的坑。想知道怎么装、怎么用、怎么配，回[上一层](../../README.zh-CN.md)。

根目录只放**使用说明**（README / DEPLOY / DNS），这一层放开发理由 —— 两种读者要的东西
完全不一样：用户要「点哪儿」，改代码的人要「为什么不能改成那样」。混在一份文档里的结果是
两边都读不下去。

| 要动哪块 | 先读 |
|---|---|
| `internal/herdr`、`internal/outbox`、`internal/agentwatch` | [HERDR-API.md](HERDR-API.md) —— socket 的真实语义，每条都是在真 pane 上验的 |
| `internal/composer` | [COMPOSER.md](COMPOSER.md) —— 读屏抽输入框，错了**不报错** |
| `internal/outbox`、`web/src/hooks/useCompose.ts` | [OUTBOX.md](OUTBOX.md) —— 发件箱的设计取舍 |
| `web/src/term/`、触屏那一层、移动端的面板 / 顶栏 / 提示 | [MOBILE.md](MOBILE.md) —— 每一条是拿什么 bug 换来的 |
| 认证、配对、暴露形态、文件浏览 / 看 diff 那条路 | [SECURITY.md](SECURITY.md) —— 分层设计 + 哪些禁令不能绕 |
| **要不要新做一块界面**（还是该扩 herdr 的 TUI） | [TUI-VS-GUI.md](TUI-VS-GUI.md) —— 六条触发条件 + 反向判据 + 动手前的成本清单 |

还有两份在根上，因为它们同时是使用说明：

- [../../DEPLOY.md](../../DEPLOY.md) —— 放在哪儿跑、有哪几档、砍掉什么会怎样
- [../../DNS.md](../../DNS.md) —— 各家 DNS 的 token 怎么拿

以及 [../../CLAUDE.md](../../CLAUDE.md)：给 agent 的项目指令 + 代码结构 + 发版 + 配色 +
已经踩过的坑。**它必须留在根上**（Claude Code 自动加载的是 `./CLAUDE.md`）。
