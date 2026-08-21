# herdr-web

浏览器里的 herdr 终端 + **语音投稿**（平板手写笔说话打字、框选重说改字，投进 herdr 的 agent pane）。

一个 Go 二进制，前端（React + Vite + Tailwind）嵌在里面：`make build` → `./herdr-web`。
怎么跑、环境变量、代码结构、移动端手势、已处理的坑：[README.md](README.md)。

动 herdr socket 调用、输入框抽取、或发件箱之前先读 [HANDOFF.md](HANDOFF.md)。里面是已验证的
API 语义和一串**会静默出错**的坑，其中这几条最容易再踩一遍：

- `ctrl+u` 清空是 **2N−1 次**（N 行输入），固定次数只够两行；清不空就别投。
- `HERDR_WEB_SETTLE_MS` **别调成 0**，否则两次 `pane.read` 落在同一帧上，清空循环整体失效。
- 「本地草稿」要用单独的所有权标志判断，别拿文本比较推 —— 开着双向同步时会被自己覆盖。
- 抽输入框有三个坑：dim 占位、`38;2;...` 里的 `2` 不是 dim、空框是 `❯`+NBSP。
  `internal/composer/testdata/` 里是真机抓屏，改这块必须跑 `go test ./internal/composer/`。

## 约定

- 注释和文档写中文，说清**为什么**（尤其是那些反直觉的取舍），别复述代码在干什么。
- 终端那层（`web/src/term/`）是命令式的，别往 React 里搬。
- 命令行用 [cobra](https://github.com/spf13/cobra)，配置用 [viper](https://github.com/spf13/viper) 且**只从环境变量来**（不读配置文件）。加配置项就是 `internal/config/` 里加一行 + README「配置」那节的表格加一行；别新开命令行标志，也别在别处 `os.Getenv`。
- 改完跑 `make test`（Go 测试 + 前端 typecheck）。涉及 herdr 行为的改动要在真 pane 上验一遍。
