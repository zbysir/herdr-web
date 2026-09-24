# 把 agent 起的本地服务代理出来（方案，未开发）

> **状态：只有方案，没写代码**（2026-09-24）。搁置的原因是要在域名那一层配不少东西
> （通配 DNS + 通配证书 + 共用 Traefik 上的一条路由），用户觉得眼下不值当。
> 真要做时从这里接着来，别从「路径前缀代理」重新想一遍 —— 那条路下面第 1 节已经排除了。

## 要解决的事

agent 经常起一个开发服务，然后说「打开 http://localhost:5312/?dev 看看」。手机 / 另一台电脑
上 `localhost` 是它自己，点了打不开。想要的是：终端里、chat 里点这种链接，由 herdr-web 把那个
端口代理出来直接看 —— 和「点一条本地文件路径就能看文件」是同一种体验。

## 1. 为什么不能做成 `herdr.bysir.top/_p/5312/...`（路径前缀）

两条，任何一条都够否掉它：

- **安全：被代理的页面绝不能和 herdr-web 同源。** 那是 agent 写的页面、跑着任意 JS。同源的话它
  带着 cookie 就能调 `/api/herdr/say` 往任意 pane 里敲命令 —— 和文件浏览那条「吐内容绝不能是
  `text/html`」（SECURITY.md）是同一类禁令，只是这次是整站。
- **跑不通：** Vite / Next 这类开发服务器的资源几乎全是绝对路径（`/assets/…`、`/@vite/client`、
  `/src/main.ts`），热更新还要一条 WebSocket（`/`、`/_next/webpack-hmr`）。前缀一加全部指到
  herdr-web 的根上。靠改写 HTML 去补是无底洞（JS 里拼出来的路径改写不到）。

sandbox iframe（opaque origin）能解决第一条，解决不了第二条；而且 opaque origin 发出去的请求
带不上 `SameSite=Strict` 的 cookie，代理那一侧还得另找办法认人。

## 2. 方案：每个端口一个子域名（Codespaces 那种）

```
https://5312.p.herdr.bysir.top/  ──VPS Traefik──frp──▶  herdr-web 公网口  ──按 Host 反代──▶  127.0.0.1:5312
```

- **独立 origin**：被代理的页面拿不到主站的 cookie、调不到主站的 API（CSP / 跨源规则天然隔开）。
- **在根路径下代理**：绝对路径、HMR WebSocket 原样能用，不改写任何内容。
- **子域名单独认人**：走局域网直连那套**交接令牌**（`auth.MintHandoff`：一次性、60 秒、兑出来的
  凭据记着 `Parent`，随主站那台设备一起被撤销）。点链接时主站先签一枚，跳到
  `https://5312.p…/?handoff=…`，子域名兑成**只对这个子域名有效**的 cookie（host-only）。
  **别用配对码**（SECURITY.md §11 那条禁令，理由同局域网直连的修正案）。

### herdr-web 这边要做的

1. **按 Host 分流**：`<port>.p.<主域名>` 的请求不进主站路由，进反代（`httputil.ReverseProxy` +
   WebSocket 升级透传）。判据只能是 Host —— 但那一条**只决定「路由到哪」，不决定「放不放行」**：
   放行照旧靠这个子域名自己的 cookie。
2. **端口白名单**：只代理 `127.0.0.1` 的端口，而且只代理**确实有 pane 在上面监听**的（`lsof` / 
   `ss` 按 pane 的进程树找），或者至少排除 herdr-web 自己的几个口和 22 之类的系统口。
   任意端口都放的话，这就是一个带登录的「内网任意端口转发」。
3. **链接改写**：终端（`term/paths.ts` 那套 link provider）和 chat（`lib/mdpaths.ts`）里认出
   `http://localhost:<port>` / `127.0.0.1:<port>` / `0.0.0.0:<port>`，点下去走「签交接令牌 → 跳子域名」。
   本机直连（在跑 herdr-web 的那台机器上开的页面）不改写，原样打开就行。
4. `hostOK` 的白名单要认 `*.p.<主域名>`；子域名上的响应**不加**主站那套 CSP（那是别人的页面）。
5. 配置项：`HERDR_WEB_PROXY_DOMAIN`（比如 `p.herdr.bysir.top`，不配 = 功能关、链接不改写）。

### 域名 / VPS 这边要做的（这就是搁置的原因）

1. DNS：`*.p.herdr.bysir.top` 通配记录指向 VPS。
2. VPS 上共用的那个 Traefik：给这个通配域名签**通配证书**（Let's Encrypt 的通配证书只能走
   **DNS-01**，Traefik 要配 DNS 服务商的 API 凭据）。
3. Traefik 加一条路由：`HostRegexp(^[0-9]+\.p\.herdr\.bysir\.top$)` → 和主域名同一个 frp 端口。
   HTTP/3 已经开在 websecure 入口上（2026-09-24），这条路由自动带上。

## 3. 备选（更省事，但只覆盖一部分场景）

- **只在局域网下用**：直连口本来就能到那台机器，手机直接开 `http://<电脑的局域网 IP>:5312`，
  前提是开发服务器监听 `0.0.0.0` 而不是 `127.0.0.1`（Vite 要 `--host`）。不需要 herdr-web 做
  任何事，但公网下没用。
- **让 agent 自己把服务发到预览环境**：比如 Creght 那种有预览域名的平台，本来就是给人看的。
