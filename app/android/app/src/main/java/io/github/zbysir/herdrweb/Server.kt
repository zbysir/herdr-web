package io.github.zbysir.herdrweb

import android.content.Context
import android.net.Uri

/**
 * 「连的是哪台 herdr-web」。App 不带任何人的域名（按上架设计：每个用户连自己托管的那台），
 * 所以第一次打开要人自己填。
 */
object Server {
    private const val PREFS = "server"
    private const val KEY = "origin"

    /**
     * 把人填的东西解析成一个 URL。认两种：光秃秃的地址（`herdr.example.com`、带不带 https 都行），
     * 和终端里那条配对链接（`https://host/?pair=CODE`）—— 后者原样拿去加载一次，服务端的
     * handleRoot 会把 `?pair=` 换成 cookie 再把地址洗干净，App 这边什么都不用做。
     */
    fun parse(input: String): Uri? {
        var s = input.trim()
        if (s.isEmpty()) return null
        if (!s.contains("://")) s = "https://$s"
        val u = Uri.parse(s)
        if (u.scheme != "https" && u.scheme != "http") return null
        if (u.host.isNullOrEmpty()) return null
        return u
    }

    /** `scheme://host[:port]`，默认端口不写 —— 桥的 origin 白名单就是按这个格式比的 */
    fun origin(u: Uri): String {
        val port = if (u.port != -1) ":${u.port}" else ""
        return "${u.scheme}://${u.host}$port"
    }

    fun load(ctx: Context): Uri? =
        ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE).getString(KEY, null)?.let(Uri::parse)

    fun save(ctx: Context, origin: String) {
        ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit().putString(KEY, origin).apply()
    }

    /**
     * 局域网直连那一跳（`useLanDirect`）会把页面切到 `https://192.168.x.x:7790` 这种地址上。
     * 这种地址留在 WebView 里，别的一律交给系统浏览器 —— 不然点一下 agent 输出里的链接，
     * 整个 App 就被换成了别人的网页，返回键还回不来。
     */
    fun isLocalHost(host: String?): Boolean {
        if (host.isNullOrEmpty()) return false
        if (host == "localhost" || host.endsWith(".local")) return true
        val p = host.split('.').mapNotNull { it.toIntOrNull() }
        if (p.size != 4) return false
        return p[0] == 10 ||
            (p[0] == 172 && p[1] in 16..31) ||
            (p[0] == 192 && p[1] == 168) ||
            (p[0] == 100 && p[1] in 64..127) // CGNAT（Tailscale 这类）
    }
}
