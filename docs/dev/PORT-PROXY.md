# 把 agent 起的本地服务代理出来

> **状态（2026-10-24）：已实现并上线**（`internal/portproxy`，`HERDR_WEB_PORT_DOMAIN`）。
> `http://127.0.0.1:7830` 在 `https://7830.herdr.bysir.top/` 上能看，先过 herdr-web 的认证。
> 下面第 1、2 节是原方案（2026-09-24），思路没变；和原方案不一样的地方、实测踩的坑在最后一节。

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


## 4. 实际做成的样子（2026-10-24）

和第 2 节的差别：

- **子域名是 `<端口>.<主机名>`**（`7830.herdr.bysir.top`），不是 `<端口>.p.<主机名>`：少一层名字，
  而主站那张 cookie 本来就是 host-only 的，子域名拿不到。
- **走中继，证书在主机上签**：DNS 上一条 `*.herdr.bysir.top` 指 VPS；中继按 SNI 认出「第一段是纯数字」
  的子域名就路由给它上一级那台主机（`relay.portParent`）；Traefik 那条透传路由加一个
  `HostSNIRegexp(^[0-9]+\.herdr\.bysir\.top$)`。主机的 ACME 多签一个 `*.<域名>`（`Config.CertDomains`）。
- **认人不用交接令牌**：交接令牌会兑出一台设备，每开一个端口设备表里就多一行。这里换成 portproxy 自己的
  票（60 秒、一次性、绑死端口）→ 子域名上一张**只在内存里**的会话（cookie `__Host-hw_port`），会话只记
  「哪台设备换来的」，**每个请求都回设备表核一遍**（`auth.IdentByID`）：设备撤销、该重验 passkey 了，
  子域名当场一起失效。没登录：子域名 → 主站 `/_port/<端口>` → 主站也没登录就落到登录页带着
  `?port=&to=`，登录完前端自己跳回来（App.tsx）。
- **端口白名单**：1024–65535、排除 herdr-web 自己的几个口（主口 / 管理口 / 直连口）。没收窄到「有 pane 在
  监听的端口」：能走到这一步的人已经过了主站认证，而主站后面就是一个 shell。
- **链接改写**：终端（`term/session.ts` 的 openLink）和 chat（`ChatMarkdown` 的 `<a>`）里
  `http://localhost:<端口>` / `127.0.0.1` / `0.0.0.0` 换成子域名（`lib/portlink.ts`）。在跑 herdr-web
  的那台机器上开的页面不换。

几条会静默出错的：

- **CAA 要放开通配**：主机名上原来有 `issuewild ";"`（不许签通配），签 `*.<域名>` 必须改成
  `issuewild "letsencrypt.org; accounturi=<主机的账户>; validationmethods=dns-01"`。不改的表现是签发失败，
  而服务启动时签不下来会**拒绝启动** —— 所以先在命令行把证书签好再改配置。
- **证书「够不够用」的检查要认通配**：`x509.VerifyHostname` 不收带 `*` 的输入，拿 `*.x` 直接去问永远是
  「不覆盖」，于是每次启动都重签一张（`acme.validFor` 换成拿一个样例子域名去问）。
- **Host 要写成 `localhost:<端口>` 再转**：Vite 5 起会拦不认识的 Host（「Blocked request. This host is not
  allowed」），HMR 的 WebSocket 还核 Origin（我们这个子域名的 Origin 一并换掉）。开发服务器跳转写的是它自己
  眼里的 `http://localhost:<端口>/…`，`Location` 要换回子域名。
- **子域名那张 cookie 绝不转给被代理的服务**（`stripCookie`）：它是一份能进这个子域名的凭据，而那个服务
  是 agent 的。主站那张不用管 —— host-only，压根不会发到子域名上。
- 被代理的页面和主站**同站不同源**（都在 bysir.top 下）：同站请求会带上主站的 SameSite=Strict cookie，
  挡它的是主站那几道 —— 写接口要自定义头（跨源触发 preflight，本服务不答）、`/pty` 核 Origin。
  这两道是**承重的**，别为了别的需求松掉。
