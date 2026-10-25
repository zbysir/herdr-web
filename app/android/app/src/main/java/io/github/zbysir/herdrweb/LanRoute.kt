package io.github.zbysir.herdrweb

import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import android.net.Uri
import android.util.Log
import androidx.core.content.ContextCompat
import androidx.webkit.ProxyConfig
import androidx.webkit.ProxyController
import androidx.webkit.WebViewFeature
import java.io.InputStream
import java.io.OutputStream
import java.net.InetAddress
import java.net.InetSocketAddress
import java.net.ServerSocket
import java.net.Socket
import java.util.concurrent.Executors
import javax.net.ssl.SNIHostName
import javax.net.ssl.SSLSocket
import javax.net.ssl.SSLSocketFactory

/**
 * 局域网换路：**同一个地址**（`https://herdr.bysir.top`），在家时连接从局域网直连口走，在外面
 * 走云端中继。设计在 docs/dev/RELAY.md §4。
 *
 * 网页那条直连（`useLanDirect`）要换 origin（跳到 `https://192.168.x.x:7790`），于是自签证书、
 * 「继续访问」、交接令牌、IP 上用不了 passkey 一串麻烦全是被「换 origin」逼出来的。App 能做的是
 * **决定这个域名连到哪个 IP**：WebView 走一个本机的 CONNECT 代理（`ProxyController`），代理按
 * 探测结果把 `herdr.bysir.top:443` 拨到局域网口或者照常拨域名。TLS 从 WebView 一路到 Mac，
 * **证书由 WebView 自己按域名验**（Mac 的直连口按 SNI 给出同一张真证书，见 tlsgen.BySNI）——
 * 代理只看到密文，验证也不在我们自己写的代码里。
 *
 * 三条别改：
 * 1. **只在探测通过之后才往局域网拨**，而探测是一次**完整的 TLS 握手 + 验域名**，不是 TCP
 *    能连上就算。TCP 通、证书不对（局域网里另一台机器占了那个 IP）时，WebView 那边是一个
 *    证书错误、整屏报错，而不是「退回中继」—— 那正是要避免的。
 * 2. **网络一变先把局域网那条路作废，再重新探**。离开 Wi-Fi 之后还往旧地址拨，每个新连接都要
 *    等一次超时才退回中继，终端重连会慢一大截。
 * 3. **探通局域网的那一刻，把走中继的连接全部掐掉**（`closeRelay`）。只让「新连接走新路」不够：
 *    终端是条长连接，切过去之后它一直挂在中继上；浏览器池子里那几条空闲的 keep-alive 也是，于是
 *    顶栏那颗点一直是蓝的（用户报的「在局域网怎么还是中继」）。而且「先作废、再探」中间有个空档，
 *    恰好在那时重连的终端会落到中继上，等探通时「切过去」那一下已经过了（第一版就是在页面上「切的
 *    那一下重连终端」，撞上的就是这个）。掐掉之后浏览器自己重建、终端的自动重连自己走局域网 ——
 *    条件只看「此刻有没有走中继的连接」，没有时机问题。反方向不用管：离开 Wi-Fi 时局域网那些连接
 *    自己会断。
 */
object LanRoute {
    private const val TAG = "LanRoute"
    /** 局域网 RTT 是个位数毫秒，握手 + 证书验证 1.5 秒绰绰有余；在外面时别让人白等 */
    private const val PROBE_MS = 1500
    private const val LAN_CONNECT_MS = 800

    private val io = Executors.newCachedThreadPool()
    private var proxy: LocalProxy? = null

    /** 当前服务器的域名和端口（`herdr.bysir.top:443`）。只有它走局域网判断，别的照常直连 */
    @Volatile private var host: String? = null
    @Volatile private var port = 443
    /** 页面报来的候选（直连口的 `ip:port`），每次现报 */
    @Volatile private var candidates: List<InetSocketAddress> = emptyList()
    /** 探测通过的那个；null = 走中继（照常拨域名） */
    @Volatile var lan: InetSocketAddress? = null
        private set
    /**
     * 现在**开着的**隧道里，各有几条走局域网 / 走中继。界面上那个「直连 / 中继」标签照它画 ——
     * 不照 [lan]（新连接往哪儿拨）：切到局域网之后终端那条长连接还在中继上，按「新连接」画
     * 就会显示「直连」，而你最在意的终端其实还绕着公网。
     */
    private val openLan = java.util.concurrent.atomic.AtomicInteger()
    private val openRelay = java.util.concurrent.atomic.AtomicInteger()
    /** 变了就回调（任意线程）。MainActivity 转给页面 */
    @Volatile var onChange: (() -> Unit)? = null

    /** 给页面的那份：`{lan, relay, next}`，next = 新连接现在往哪儿拨 */
    fun snapshot(): org.json.JSONObject = org.json.JSONObject()
        .put("t", "route")
        .put("lan", openLan.get())
        .put("relay", openRelay.get())
        .put("next", if (lan != null) "lan" else "relay")
        .put("proxied", host != null)

    internal fun tunnel(h: String, isLan: Boolean, delta: Int) {
        if (!h.equals(host, ignoreCase = true)) return // 不支持反向绕过时别的主机也会进代理，那些不算
        (if (isLan) openLan else openRelay).addAndGet(delta)
        onChange?.invoke()
    }

    /** 每次网络变化 / 新候选都加一，晚到的探测结果对不上就扔掉 */
    @Volatile private var gen = 0
    private var netCb: ConnectivityManager.NetworkCallback? = null
    /**
     * 当前连着的 Wi-Fi。局域网那几条连接（探测、往直连口拨）**绑在它上面**，不跟着系统默认网络走。
     *
     * 为什么：小米这类「双网」机型 Wi-Fi 信号一差（或者它判断 Wi-Fi 上不了外网）就把**默认网络**切到
     * 蜂窝，Wi-Fi 还连着、只是标成 `EXITING`。不指定网络的 socket 全走默认网络，于是人站在家里、
     * Wi-Fi 好好连着，局域网却「探不通」（用户报的「等了 30 秒都没切」，日志里是 ENETUNREACH /
     * 从 10.x 的蜂窝地址去连 192.168.x）。绑在 Wi-Fi 上之后，只要 Wi-Fi 连着，局域网就够得到。
     */
    @Volatile private var wifi: Network? = null
    private var wifiCb: ConnectivityManager.NetworkCallback? = null
    /** 走中继、而 Wi-Fi 连着时，多久再探一次（兜底：有的切换不发任何回调） */
    private const val RETRY_MS = 20_000L
    private val timer = java.util.concurrent.Executors.newSingleThreadScheduledExecutor()

    /** 局域网那条路上的 socket 绑到 Wi-Fi（没 Wi-Fi 就不绑，照默认网络走） */
    internal fun bindLan(s: Socket) {
        val w = wifi ?: return
        try { w.bindSocket(s) } catch (e: Exception) { Log.i(TAG, "绑 Wi-Fi 失败: $e") }
    }

    /**
     * 给这台服务器装上代理，装好再回调 [ready]（这时才能 loadUrl —— 装之前发出去的请求不走代理）。
     * 不支持的 WebView / 裸 IP / http 的地址：不装，直接回调，一切照旧。
     */
    fun install(ctx: Context, server: Uri, ready: () -> Unit) {
        val h = server.host
        if (server.scheme != "https" || h.isNullOrEmpty() || Server.isLocalHost(h) || isIp(h) ||
            !WebViewFeature.isFeatureSupported(WebViewFeature.PROXY_OVERRIDE)
        ) {
            Log.i(TAG, "不装代理：$server（WebView 支持代理：${WebViewFeature.isFeatureSupported(WebViewFeature.PROXY_OVERRIDE)}）")
            clear()
            return ready()
        }
        host = h.lowercase()
        port = if (server.port > 0) server.port else 443
        lan = null
        gen++
        val p = proxy ?: LocalProxy(::route).also { proxy = it; it.start() }
        val b = ProxyConfig.Builder().addProxyRule("127.0.0.1:${p.port}")
        // 「反向绕过」：绕过列表变成**只有这些走代理**。不支持时整个 WebView 都走代理，
        // 代理对别的主机原样直连（route 里只认 host），行为一样，只是多一跳本机转发
        val reverse = WebViewFeature.isFeatureSupported(WebViewFeature.PROXY_OVERRIDE_REVERSE_BYPASS)
        if (reverse) b.addBypassRule(h).setReverseBypassEnabled(true)
        Log.i(TAG, "装代理 127.0.0.1:${p.port}，只代理 $h：$reverse")
        prefs = ctx.getSharedPreferences("lanroute", Context.MODE_PRIVATE)
        // **加载页面之前先拿上次的候选探一次**（最多 PROBE_MS）。不这么做的话：页面先经中继
        // 加载、终端那条 WebSocket 也先在中继上建好，等页面把候选报上来、探通了，新连接才走
        // 局域网 —— 而终端是条长连接，人在家里它也一直绕着公网（顶栏那个点一直是蓝的）。
        val cached = loadCached(h)
        val go = {
            if (cached.isEmpty()) ready()
            else io.execute {
                candidates = cached
                val g = gen
                val hit = cached.firstOrNull { probe(h, it) }
                if (g == gen) {
                    lan = hit
                    if (hit != null) proxy?.closeRelay()
                    Log.i(TAG, if (hit != null) "开页面前探到局域网：$hit" else "开页面前局域网探不通，先走中继")
                }
                ContextCompat.getMainExecutor(ctx).execute { ready() }
            }
        }
        try {
            // 回调要回到主线程：ready 里是 loadUrl
            ProxyController.getInstance().setProxyOverride(b.build(), ContextCompat.getMainExecutor(ctx)) { go() }
        } catch (e: Exception) {
            Log.w(TAG, "装代理失败，照常直连: $e")
            ready()
        }
        watchNetwork(ctx)
    }

    /** 换服务器 / 回到填地址那一屏：撤掉代理 */
    fun clear() {
        host = null
        lan = null
        candidates = emptyList()
        gen++
        if (proxy != null && WebViewFeature.isFeatureSupported(WebViewFeature.PROXY_OVERRIDE)) {
            try {
                ProxyController.getInstance().clearProxyOverride({ it.run() }) {}
            } catch (_: Exception) {}
        }
    }

    private var prefs: android.content.SharedPreferences? = null

    private fun loadCached(h: String): List<InetSocketAddress> =
        prefs?.getString(h, null)?.split(',')?.mapNotNull { parse(it) } ?: emptyList()

    private fun parse(o: String): InetSocketAddress? {
        val u = Uri.parse(o)
        val h = u.host ?: return null
        if (u.scheme != "https" || u.port <= 0 || !Server.isLocalHost(h)) return null
        return InetSocketAddress.createUnresolved(h, u.port)
    }

    /** 页面报来局域网直连口的地址（`https://192.168.1.5:7790` 这种）。 */
    fun setCandidates(origins: List<String>) {
        host?.let { h -> prefs?.edit()?.putString(h, origins.joinToString(","))?.apply() }
        val cs = origins.mapNotNull { parse(it) }
        // 和手上的一样、而且已经探通了：别作废重探（页面每加载一次都会报一遍，作废的那一瞬
        // 新连接会走中继）
        if (cs == candidates && lan != null) return
        candidates = cs
        Log.i(TAG, "页面报来候选 $origins → $candidates")
        reprobe()
    }

    /** 回前台 / 网络变了：先作废，再探 */
    fun reprobe() {
        val h = host ?: return
        val cs = candidates
        val g = ++gen
        lan = null
        onChange?.invoke()
        if (cs.isEmpty()) return
        io.execute {
            val hit = cs.firstOrNull { probe(h, it) }
            if (g == gen) {
                lan = hit
                if (hit != null) proxy?.closeRelay()
                onChange?.invoke()
                Log.i(TAG, if (hit != null) "走局域网：$h → $hit" else "局域网探不通，走中继")
                // 没探通但 Wi-Fi 连着：过一会儿再探（同一代里才算数，期间有新事件就作废）
                if (hit == null && wifi != null) {
                    timer.schedule({ if (g == gen && lan == null && wifi != null) reprobe() },
                        RETRY_MS, java.util.concurrent.TimeUnit.MILLISECONDS)
                }
            }
        }
    }

    private fun watchNetwork(ctx: Context) {
        if (netCb != null) return
        val cm = ctx.getSystemService(ConnectivityManager::class.java) ?: return
        val cb = object : ConnectivityManager.NetworkCallback() {
            override fun onAvailable(network: Network) = reprobe()
            override fun onLost(network: Network) = reprobe()
        }
        try {
            cm.registerDefaultNetworkCallback(cb)
            netCb = cb
        } catch (e: Exception) {
            Log.w(TAG, "盯不了网络变化: $e")
        }
        // 单独盯 Wi-Fi：默认网络不变（一直是蜂窝）时 Wi-Fi 连上 / 断开不会走上面那个回调。
        // 用 requestNetwork 而不是只听：Wi-Fi 被标成 EXITING 之后，没人要它的话系统过一会儿
        // 就把它拆了，而我们正要用它走局域网。不要求能上外网（removeCapability INTERNET）——
        // 小米判它「上不了网」的时候正是我们最需要它的时候。
        val req = android.net.NetworkRequest.Builder()
            .addTransportType(android.net.NetworkCapabilities.TRANSPORT_WIFI)
            .removeCapability(android.net.NetworkCapabilities.NET_CAPABILITY_INTERNET)
            .build()
        val wcb = object : ConnectivityManager.NetworkCallback() {
            override fun onAvailable(network: Network) {
                wifi = network
                Log.i(TAG, "Wi-Fi 连上了")
                reprobe()
            }
            override fun onLost(network: Network) {
                if (wifi == network) wifi = null
                Log.i(TAG, "Wi-Fi 断了")
                reprobe()
            }
        }
        try {
            cm.requestNetwork(req, wcb)
            wifiCb = wcb
        } catch (e: Exception) {
            Log.w(TAG, "盯不了 Wi-Fi: $e")
        }
    }

    /**
     * 握手 + 验域名。`createSocket(raw, host, …)` 会把 host 放进 SNI，Mac 那个口据此给出真证书；
     * `endpointIdentificationAlgorithm = HTTPS` 让 JSSE 自己核证书上的域名（默认的 SSLSocket
     * **不核**，只核链 —— 漏了这一行，任何一张正经证书都能冒充）。
     */
    private fun probe(host: String, addr: InetSocketAddress): Boolean = try {
        Socket().use { raw ->
            bindLan(raw)
            raw.connect(InetSocketAddress(addr.hostString, addr.port), PROBE_MS)
            raw.soTimeout = PROBE_MS
            val s = (SSLSocketFactory.getDefault() as SSLSocketFactory)
                .createSocket(raw, host, addr.port, false) as SSLSocket
            s.sslParameters = s.sslParameters.apply {
                endpointIdentificationAlgorithm = "HTTPS"
                serverNames = listOf(SNIHostName(host))
            }
            s.startHandshake()
            true
        }
    } catch (e: Exception) {
        Log.i(TAG, "探 $addr 不通: $e")
        false
    }

    /** 代理问「这个 CONNECT 该往哪儿拨」：按顺序试，前面的连不上就下一个 */
    private fun route(h: String, p: Int): List<Target> {
        val direct = Target(InetSocketAddress(h, p), 10_000, lan = false)
        val l = lan
        if (l != null && h.equals(host, ignoreCase = true) && p == port) {
            return listOf(Target(InetSocketAddress(l.hostString, l.port), LAN_CONNECT_MS, lan = true), direct)
        }
        return listOf(direct)
    }

    private fun isIp(h: String) = h.contains(':') || h.all { it.isDigit() || it == '.' }
}

/**
 * 本机的 CONNECT 代理，只给这个 App 的 WebView 用。**只做转发，不碰内容、不加凭据**（cookie 在
 * WebView 自己的罐子里）—— 手机上别的 App 连这个口，能做的和它自己直接连网一样，没多拿到什么。
 */
internal class Target(val addr: InetSocketAddress, val timeout: Int, val lan: Boolean)

private class LocalProxy(private val route: (String, Int) -> List<Target>) {
    // **显式 127.0.0.1**，别用 InetAddress.getLoopbackAddress()：Android 上它返回的是 IPv6 的 ::1，
    // 而代理规则写的是 127.0.0.1 —— WebView 报 ERR_PROXY_CONNECTION_FAILED，页面上只是「Failed to
    // fetch」，而从 adb forward 测（连的是 localhost → ::1）一切正常。真机上踩过。
    private val server = ServerSocket(0, 50, InetAddress.getByAddress(byteArrayOf(127, 0, 0, 1)))
    private val pool = Executors.newCachedThreadPool()
    /** 开着的、走中继的上游连接。切到局域网时一起掐掉（见 LanRoute 的第 3 条） */
    private val relayUps: MutableSet<Socket> = java.util.Collections.newSetFromMap(java.util.concurrent.ConcurrentHashMap())

    fun closeRelay() {
        val n = relayUps.size
        relayUps.forEach { runCatching { it.close() } }
        if (n > 0) Log.i("LanRoute", "切到局域网：掐掉 $n 条走中继的连接，让它们重连到局域网")
    }
    val port: Int get() = server.localPort

    fun start() {
        Thread({
            while (true) {
                val c = try { server.accept() } catch (_: Exception) { return@Thread }
                // 兜底：这里抛出去的异常没人接，线程池 / 裸 Thread 上一个未捕获异常就是**整个 App 崩掉**
                // （真机上踩过：从通知点开 → 回前台重探局域网 → closeRelay 掐掉正在建的那条 → 崩）
                pool.execute {
                    try { handle(c) } catch (e: Exception) { Log.d("LanRoute", "代理连接中断：$e") }
                }
            }
        }, "lan-proxy").apply { isDaemon = true }.start()
    }

    private fun handle(c: Socket) {
        c.use {
            c.soTimeout = 15_000
            val input = c.getInputStream()
            val out = c.getOutputStream()
            val head = readHead(input) ?: return
            val line = head.substringBefore("\r\n").split(' ')
            if (line.size < 2 || line[0] != "CONNECT") {
                out.write("HTTP/1.1 405 Method Not Allowed\r\nContent-Length: 0\r\n\r\n".toByteArray())
                return
            }
            val target = line[1]
            val i = target.lastIndexOf(':')
            val h = target.substring(0, maxOf(i, 0)).trim('[', ']')
            val p = target.substring(i + 1).toIntOrNull()
            if (i <= 0 || p == null) {
                out.write("HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\n\r\n".toByteArray())
                return
            }
            val targets = route(h, p)
            val hit = dial(targets) ?: run {
                Log.w("LanRoute", "CONNECT $h:$p 拨不通：$targets")
                out.write("HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n".toByteArray())
                return
            }
            val (up, isLan) = hit
            Log.d("LanRoute", "CONNECT $h:$p → ${up.remoteSocketAddress}${if (isLan) "（局域网）" else ""}")
            LanRoute.tunnel(h, isLan, +1)
            if (!isLan) relayUps.add(up)
            // 拨的那一刻还没有局域网这条路（targets 里压根没有它）、连上时却已经探通了：这条是从
            // 「先作废、再探」那个空档里漏过来的，closeRelay 已经扫过去了，当场补掐。
            // 试过局域网、没拨通才退到中继的那种是正当的退路，不动
            // 补掐之后下面一取 up 的流就抛 Socket is closed —— finally 照样记账，异常由 start 里那层接住
            if (!isLan && targets.none { it.lan } && LanRoute.lan != null) runCatching { up.close() }
            try { up.use {
                // 流**先取出来**：closeRelay 随时可能从别的线程把 up 关掉，关掉之后
                // getInputStream / getOutputStream 直接抛（而不是返回一条读到 EOF 的流）
                val upIn = up.getInputStream()
                val upOut = up.getOutputStream()
                c.soTimeout = 0
                out.write("HTTP/1.1 200 Connection Established\r\n\r\n".toByteArray())
                out.flush()
                val t = Thread { pump(upIn, out); runCatching { c.shutdownOutput() } }
                t.isDaemon = true
                t.start()
                pump(input, upOut)
                runCatching { up.shutdownOutput() }
                t.join()
            } } finally {
                LanRoute.tunnel(h, isLan, -1)
                relayUps.remove(up)
            }
        }
    }

    /** 返回连上的 socket 和「是不是局域网那条」 */
    private fun dial(targets: List<Target>): Pair<Socket, Boolean>? {
        for (t in targets) {
            val s = Socket()
            try {
                if (t.lan) LanRoute.bindLan(s) // 和探测同一个网络，见 LanRoute.wifi
                s.connect(t.addr, t.timeout)
                return s to t.lan
            } catch (_: Exception) {
                s.close()
            }
        }
        return null
    }

    /** 读到 `\r\n\r\n` 为止，一个字节都不多读 —— 后面紧跟着的是 TLS 握手，读多了就丢了 */
    private fun readHead(input: InputStream): String? {
        val b = StringBuilder()
        while (b.length < 8192) {
            val ch = input.read()
            if (ch < 0) return null
            b.append(ch.toChar())
            if (b.endsWith("\r\n\r\n")) return b.toString()
        }
        return null
    }

    private fun pump(from: InputStream, to: OutputStream) {
        val buf = ByteArray(16 * 1024)
        try {
            while (true) {
                val n = from.read(buf)
                if (n < 0) break
                to.write(buf, 0, n)
                to.flush()
            }
        } catch (_: Exception) {}
    }
}
