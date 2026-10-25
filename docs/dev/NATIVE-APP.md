# 原生 App：原生壳 + WebView（开发中：Android，1–3 期已完成）

**一句话**：一个很薄的原生壳，里面一个 WebView 加载用户自己那台 herdr-web；界面**整份复用网页**，
壳只做网页做不到的事 —— **推送**、凭据存进系统钥匙串、返回键 / 全屏 / 文件选择这类系统集成。
为什么不用 nsh 这类跨端框架见 [WHY-NOT-NSH.md](WHY-NOT-NSH.md)。

定下来的两件事（2026-10-20）：

- **按上架设计**：App 会发给别人用，每个人连的是**自己托管**的 herdr-web。这条决定了推送
  必须经过一个由我们运营的中转（§4），以及壳里不能写死任何人的域名（§2）。
- **Android 先**，iOS 后。包名 `io.github.zbysir.herdrweb`（和 launchd 那个标签对齐；
  包名里不能有连字符），上架后改不了。
- **要兼顾没有 Google 服务的国内机型**：推送是 FCM + UnifiedPush 两条通道（§4）。

## 1. 不做什么

- **不在壳里画界面**。chat、改动、文件、设置全在网页里，壳里只有「填服务器地址」那一屏。
  网页那边每修一个 bug，App 立刻跟着好，不用发版。
- **不打包前端**。WebView 加载的是服务器上的页面，不是 App 里自带的一份 —— 否则 App 版本和
  服务端版本就要配对，而用户的服务端版本我们管不着。
- **不在 WebView 里做 passkey**（§3）。

## 2. 结构（Android）

```
MainActivity（单 Activity）
 ├─ 没配过服务器 → 设置页：输入地址 / 扫配对页上的二维码
 └─ WebView 加载 https://<用户的服务器>/
      ├─ 导航锁：只有这台服务器的 origin（+ 它报的局域网直连 origin）留在 WebView 里，
      │          别的链接一律交给系统浏览器 / Custom Tabs
      ├─ 桥：WebViewCompat.addWebMessageListener，**只对服务器那个 origin 开**
      └─ 系统集成：返回键、文件选择（传图）、下载、软键盘 adjustResize、edge-to-edge
FirebaseMessagingService → 解密 → 发本地通知（§4）
```

几条要点：

- **桥只给服务器的 origin**。WebView 里渲染的有 agent 的输出（chat 的 Markdown、文件内容），
  那是不可信文本（CLAUDE.md 里 chat 那条），所以用 `addWebMessageListener` 的 origin 白名单，
  **不用 `addJavascriptInterface`**（后者对 WebView 里所有 frame 都可见）。桥上也只放无害的口：
  报平台信息、登记推送、触感反馈、开外部链接 —— 不放读文件、发 Intent 这类。
- **证书**：默认只认系统信任的证书（ACME 签的那种）。`onReceivedSslError` **默认 cancel，绝不
  无条件 proceed** —— 那等于关掉 TLS。自签（`HERDR_WEB_TLS=self`、局域网直连口）以后再做：
  首次连接时给人看 herdr-web 本地 CA 的指纹、确认后钉住那张 CA，只放过它签的叶子证书。
- **网页那边要认出「我在 App 里」**（桥存在即是），然后：
  - 系统通知改走原生推送（`web/src/lib/notify.ts`；Android WebView 里压根没有 `Notification`）；
  - 「呼出键盘自动全屏」停用（和 PWA standalone 同一道闸，`enterFull` 的 `auto`）；
  - 设置里 passkey 相关的入口改成「在浏览器里打开」（§3）。
- **SW 照旧注册**：WebView 支持 Service Worker，现在那个只接管导航、network-first，留着无害。

## 3. 认证

WebView 有自己的 cookie 存储（`CookieManager`，持久化），所以**服务端一行都不用改**就有两条路：

1. **配对码**：WebView 里就是现在那个配对页，输码 / 扫码，服务端照常发 `hw_dev` cookie。
2. **passkey**：WebView 里做不了 —— Android WebView 的 WebAuthn 要靠 Digital Asset Links
   把 App 和域名绑起来（`assetlinks.json`），iOS 的 WKWebView 同理要 Associated Domains，
   而一个上架的通用 App 绑不了每个用户自己的域名。所以 passkey 走 **device flow**
   （`herdr-web connect` 那条，`internal/auth/cliapprove.go`）：App 发起拿到码 → 用
   **Custom Tabs** 打开批准页（真浏览器，passkey 能用）→ 人手输码、刷 passkey → App 轮询领到
   令牌 → 写进 `CookieManager` 当 `hw_dev`（HttpOnly + Secure，只对那个 origin）。
   令牌就是设备凭据本身，cookie 和 Bearer 认的是同一张表，所以塞进 cookie 和配对出来的
   效果完全一样。

两条别破：① 批准页的码**照旧手输**，不许从 URL 带进去（防钓鱼）；② device flow 的前提是服务端
至少注册过一把 passkey（`start` 在一把都没有时就拒），那种部署只能走配对码。

## 4. 推送

### 4a. 现在实际用的：App 自己长连服务器（2026-10-20 起）

用户手上是没有 Google 服务的机型，选的是这一档：**不经过任何第三方**，App 里一个前台服务
（`NoticeService.kt`）直连用户自己那台 herdr-web，挂在 **`GET /api/herdr/notices?since=&wait=50`**
上长轮询（服务端 `agentwatch.WaitNotices`：没有新提示就挂着，来了一条当场回，最多 50 秒）。
收到「等你回答 / 跑完了」就在本地弹系统通知。

- **为什么长轮询不是短轮询**：手机上每几秒一个请求会让基带一直醒着，电掉得很快；挂着等的话
  一分钟一个请求，提示几乎实时。上限 50 秒是赶在隧道 / 反代常见的 60 秒空闲掐断之前回。
- **第一拍只对底不弹**（`since < 0` 时只取 seq）：服务启动、手机重启、被系统杀了拉起，都会走
  第一拍，不对底的话会把环里的旧提示全弹一遍。
- **App 在前台不弹**（`MainActivity.visible`）：网页自己有提示卡和红点，再弹一条是噪音。
- **凭据就是 WebView 里那份 `hw_dev` cookie**，按 Bearer 带上，每一拍现读 —— 人在 App 里重新
  登录 / 重验之后自己就接上了。401 + `need: passkey` 弹「要你验证一次」，别的 401 弹「登录失效」，
  各弹一次、之后 5 分钟问一次，不刷屏。
- **前台服务类型是 `specialUse`**：`dataSync` 在 Android 15 上一天最多跑 6 小时。
- **常驻那条通知是 agent 状态一览**：前台服务本来就得挂一条通知，让它有用 —— 每个在动的 agent
  一行（等你的排最前，停着的汇成「另有 N 个停着」）。第二条线程挂在 **`GET /api/herdr/agents?ver=&wait=50`**
  上（服务端 `agentwatch.WaitStatus`：任何一个 agent 状态变了就加版本号打铃，含头一回见到；同一个
  状态重推不算），状态一变当场刷新。不复用 `/panes`：那份二十多 KB，而这条是后台走隧道一直在问。
  频道换过两次 id（`service` MIN → `status` LOW → `agents` DEFAULT）：频道级别建好之后 App 改不了。
- **一览是自己画的（RemoteViews），不是 InboxStyle**：用户要「几个在跑就直接列出几个」，一句
  「3 个在跑」得展开才看得到是谁。每条标题最多折两行（要看全）。收起版高度有限（小米上大约四行多字），
  三条两行的放不下、第三条会被拦腰截掉 —— 所以**收起时最多 2 条，多的用「+N」挂在最后一行右边**
  （单独占一行的「还有 N 个」实测也会被截一半）；在通知上往下拖展开看全部。
- **频道级别是 DEFAULT + 静音**：LOW 在小米 HyperOS 上会被自动收进底部「更多通知」，主列表里看不见。
- 挑哪几行、什么颜色、怎么说，常驻通知和桌面小部件**共用 `Agents.kt` 一份**。
- **桌面小部件**（`AgentsWidget.kt`）：和常驻通知同一份数据，后台服务每拿到一份新一览就 `push`
  过去（另存一份，重启 / 新放一个时先画它）。**小部件自己不拉**：系统给的定时最短半小时；所以右上角
  写着更新时间，后台服务没在跑时一眼看得出是旧的。
  **行是布局里写死的六行、每个控件 id 都不一样（`r1_st` … `r6_what`），别改回 `addView`**：
  小米 HyperOS 的桌面是 Flutter **自己重新实现的** RemoteViews 渲染（logcat 里 `flutter_view` /
  `RV_TRACE`），按 view id 认控件；几行 addView 同一个行布局（id 全一样）时它会认错、把旧行的字留着，
  表现是「数量对了、正文没更新」——**完全静默**，通知栏（系统界面画的）同一份数据却是对的。
- **一览的版本号也认标题变化**（`agentwatch.observe`）：每行正文就是 agent 的会话标题，只认状态的话
  数量对、正文一直是旧的。标题是 herdr 去掉转圈字符之后的那份，实测稳定，不会把长轮询白叫醒。
- 代价：通知栏常驻一条（前台服务的硬要求）；国产系统要人手动给「自启动 /
  后台运行」，设置页那块只能替人打开「电池优化」那一层，其余用文字提醒。
- 通知权限（Android 13+）在**登录之后第一次回前台**时问一次，拒了就去设置里那个开关上再点。

设置页那块是 `web/src/components/NativeNotify.tsx`（只在 App 里出现，替换网页的系统通知开关），
桥上的消息是 `notify`（`op`: get / set / battery / test，壳回 `on` / `perm` / `battery`）。

### 4b. 上架之后的设计（未做）

FCM / APNs 的发送凭据属于 **App 的开发者账号**。用户自己托管的服务端没有、也不该有这把钥匙，
所以要一个我们运营的**中转**：

```
herdr-web（用户的机器） ──密文──▶ 推送中转（我们的） ──▶ FCM / APNs ──▶ App 解密 → 通知
```

- **端到端加密，中转只见密文**。App 装好时生成一对密钥（X25519），公钥和推送 token 一起登记给
  **用户自己的** herdr-web；服务端拿公钥加密「哪个 agent、说了什么」；中转只转发；App 在
  `FirebaseMessagingService`（iOS 是 Notification Service Extension）里解密再弹。agent 的话
  不能落到我们的服务器上 —— 那是别人机器上的代码和对话。
- **用 FCM 的 data 消息、高优先级**，不用 notification 消息（后者由系统直接弹，App 没机会解密）。
  正文要压在 4KB 以内。
- **中转无状态、无账号**：收 `{token, 密文}` 就转。防滥用靠「token 只有登记过的那台服务端知道」
  + 按 token / 来源 IP 限速 + 大小上限。中转的代码放在这个仓库（`cmd/herdr-push-relay`），
  地址是个配置项（`HERDR_WEB_PUSH_RELAY`），默认指我们那台，不信任的人可以自己架。
- **服务端那一侧的订阅模型做成通用的**：以后给 PWA 加 Web Push（VAPID）是同一张表、同一个触发点。
- 触发点就是现在的提示（`internal/agentwatch`，「等你回答」/「跑完了」），去重、「同一件事
  只说一次」那几条照旧适用。推送开关按设备记。
- 点通知 → App 打开、跳到那个 pane。

**没有 Google 服务的手机**：FCM 依赖 Google Play 服务，国内大多数 Android 机器没有，
在那上面推送完全收不到、**而且不报错**。所以 Android 上是两条通道：

- 有 GMS → FCM，走我们的中转；
- 没有 GMS → **UnifiedPush**（用户装一个分发器，比如 ntfy）。分发器给的是一个 HTTP 端点，
  服务端**直接往那儿 POST 密文**，不经过我们的中转。加密照做（端点在第三方服务器上）。

App 启动时探测有没有 GMS、有没有装分发器，登记时告诉服务端走哪条；**两条都没有时要在界面上
明说「这台手机收不到推送」**，别让人以为开着。各家厂商通道（小米 / 华为……）工作量大，暂不做。

## 4b. 桥上的消息

网页那一半在 `web/src/lib/native.ts`，壳那一半在 `app/android/.../Bridge.kt`，都是 JSON 字符串、
按 `t` 分派。**两边对不上是完全静默的**（返回键 400ms 后按「没处理」走、全屏点了没反应）。

| `t` | 方向 | 字段 | 干什么 |
|---|---|---|---|
| `hello` | 页 → 壳 → 页 | 回 `platform` / `version` | 页面加载时发一次；壳**收到它才有回信的通道**，换页 / 重载后要重发 |
| `back` | 壳 → 页 → 壳 | 回 `handled` | 返回键：页面按 Esc 那套顺序收最上面一层（`App` 的 `closeTop`）；没东西收就回 `false`，壳把 App 退到后台（**不 finish**，那会连终端一起销毁） |
| `fullscreen` | 页 → 壳 → 页 | `on` | 顶栏全屏键：壳藏 / 显系统栏。不走 Fullscreen API（WebView 里要把整页挪进 `onShowCustomView` 给的那个 View，输入法 inset 跟不过去） |
| `theme` | 页 → 壳 | `bg` / `light` | 明暗切换后同步系统栏底色和图标深浅 |
| `open` | 页 → 壳 | `url` | 交给系统浏览器（只认 http / https / mailto） |
| `setup` | 页 → 壳 | | 回到「填服务器地址」那一屏 |
| `login` | 页 → 壳 → 页 | `reauth`；回 `state`（done / error / cancel）+ `msg` | 配对页上「用 passkey 登录 / 验证」：壳走 device flow（§3），done 之后页面自己翻进去 |
| `notify` | 页 → 壳 → 页 | `op`（get / set / battery / test）+ `on`；回 `on` / `perm` / `battery` | 后台通知那块设置（§4a） |
| `key` | 壳 → 页 | `k`（up / down）、`repeat` | 音量键当 ↑ / ↓（`MainActivity.dispatchKeyEvent`）：页面走快捷键条那个 ↑ 的同一条路（`App.sendKeyBytes`），按住的连发按 `holdRate` 节流。**只在页面 hello 过之后拦**，填地址 / 登录 / 错误页上音量键照常调音量 |
| `volkeys` | 页 → 壳 → 页 | `op`（get / set）+ `on`；回 `on` | 上面那个的开关（默认开），存在壳的 SharedPreferences 里 —— 拦不拦要在 `dispatchKeyEvent` 里同步决定，等不了问页面 |
| `lan` | 页 → 壳 | `origins`（局域网直连口，`https://192.168.x.x:7790`） | 局域网换路的候选（`LanRoute.kt`）：壳按本页域名做 TLS 握手探测，通了就让 WebView 的连接从那儿走，**不换 origin**。网页那边在 App 里不再跑「跳到 IP origin」那一套（RELAY.md §4） |
| `route` | 壳 → 页 | `lan` / `relay`（此刻开着的隧道各几条）、`next`（新连接往哪儿拨）、`proxied` | 顶栏左上角那个状态点的颜色：绿 = 直连、蓝 = 中继（`hooks/useRoute.ts`）。按**开着的**连接算，不按 `next`：终端是条长连接，刚切过去时它还在中继上 |

## 5. 仓库布局

```
app/android/            Gradle 工程（Kotlin），包名待定
cmd/herdr-push-relay/   推送中转
internal/push/          服务端：订阅落盘、加密、往中转发
```

### 怎么编、怎么调

要 Android SDK（`brew install --cask android-commandlinetools`，路径写进 `app/android/local.properties`
的 `sdk.dir`，这个文件不入库）和 **JDK 17–21**（AGP 不认太新的 JDK，机器上默认的 22 / 26 会挂）：

```bash
cd app/android
JAVA_HOME=/opt/homebrew/opt/openjdk@21/libexec/openjdk.jdk/Contents/Home ./gradlew assembleDebug
adb install -r app/build/outputs/apk/debug/app-debug.apk
```

连调试实例（CLAUDE.md 里那套独立端口 + `HERDR_WEB_TLS=off`）用 **`adb reverse tcp:7811 tcp:7811`
再填 `http://localhost:7811`**，别填 `http://10.0.2.2:7811`（模拟器访问宿主的那个地址）：
后者不是 secure context，Service Worker、剪贴板这些当场没有，调出来的不是真实行为。
debug 包才放行明文（`src/debug/AndroidManifest.xml`），release 包只认 https。
`chrome://inspect` 能直接调 WebView 里的页面（debug 包开了 `setWebContentsDebuggingEnabled`）。

## 6. 分期

1. **壳本体**（已完成）：填地址、WebView、配对码登录、导航锁、返回键、文件选择、键盘、网页那边
   认出 App。出 debug APK 侧载。设置里有「换服务器」。
2. **device flow + passkey**（Custom Tabs，已完成）：批准页开在下半屏、码显示在上面；轮询途中断网
   接着等，不当失败。令牌目前就放在 WebView 的 cookie 里，没另做加密存储。
3. **推送**：先做了 §4a 那一档（长连自己的服务器，已完成）；上架用的中转 + FCM / APNs 见 §4b。
4. **发布**：签名、CI 出 APK、上架。
5. **iOS**：WKWebView + APNs + Notification Service Extension，复用 2–4 的服务端部分。
