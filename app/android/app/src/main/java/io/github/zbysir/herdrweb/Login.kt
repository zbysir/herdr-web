package io.github.zbysir.herdrweb

import org.json.JSONObject
import java.io.IOException
import java.net.HttpURLConnection
import java.net.URL

/**
 * passkey 登录 / 到期重验：走服务端现成的 device flow（`herdr-web connect` 用的那条，
 * `internal/server/cliapi.go`）。WebView 里做不了 WebAuthn —— Android WebView 的 WebAuthn
 * 要靠 Digital Asset Links 把 App 绑到域名上，而每个用户连的是自己的域名（NATIVE-APP.md §3）。
 *
 * 所以 passkey 在**浏览器**里刷：App 发起拿到一个显示码 → 浏览器打开批准页、人手输码、刷 passkey
 * → App 轮询领走令牌。这里只管 HTTP，全是阻塞调用，调用方放后台线程。
 *
 * 两条别破：码**不许拼进批准页的 URL**（`/?cli` 只翻页，点开就填好的链接正是钓鱼要的）；
 * 请求**不带 cookie**（HttpURLConnection 没装 CookieHandler）—— 重验靠 Bearer，
 * 那种凭据不是 Ambient，不需要 CSRF 那几道。
 */
class Login(private val origin: String, private val ua: String) {
    class Start(val id: String, val poll: String, val code: String, val url: String)

    sealed class Poll {
        object Pending : Poll()
        object Denied : Poll()
        /** [token] 只有新设备那种才有；重验那种当场续上，凭据还是原来那份 */
        class Approved(val token: String?, val label: String) : Poll()
    }

    open class Failure(msg: String) : Exception(msg)

    /** 连不上（不是服务端说「不行」）。轮询时遇到它要接着等，不能当失败：手机切网、锁屏一下都会撞上 */
    class Offline(msg: String) : Failure(msg)

    /** [bearer] 非空 = 给这台设备重验；空 = 要一台新设备 */
    fun start(bearer: String?): Start {
        val (status, b) = post("/api/auth/cli/start", JSONObject(), bearer)
        when {
            status == 409 && b.optString("reason") == "nopasskey" ->
                throw Failure("这台 herdr-web 还没注册 passkey，只能用配对码")
            status == 404 -> throw Failure("这台 herdr-web 太旧了，不认 passkey 批准，先更新它")
            status != 200 -> throw Failure(b.optString("error").ifEmpty { "发起失败（HTTP $status）" })
        }
        return Start(b.getString("id"), b.getString("poll"), b.getString("code"), b.getString("url"))
    }

    fun poll(s: Start): Poll {
        val (status, b) = post("/api/auth/cli/poll", JSONObject().put("id", s.id).put("poll", s.poll), null)
        if (status != 200) throw Failure(b.optString("error").ifEmpty { "码过期了，再来一次" })
        return when (b.optString("state")) {
            "approved" -> Poll.Approved(b.optString("token").ifEmpty { null }, b.optString("label"))
            "denied" -> Poll.Denied
            else -> Poll.Pending
        }
    }

    private fun post(path: String, body: JSONObject, bearer: String?): Pair<Int, JSONObject> {
        val c = URL(origin + path).openConnection() as HttpURLConnection
        try {
            c.requestMethod = "POST"
            c.connectTimeout = 10_000
            c.readTimeout = 15_000
            c.doOutput = true
            c.setRequestProperty("content-type", "application/json")
            // 和 WebView 同一个 UA：服务端按它给新设备起名（「Android · App」），批准页上也靠它认人
            c.setRequestProperty("User-Agent", ua)
            if (bearer != null) c.setRequestProperty("Authorization", "Bearer $bearer")
            c.outputStream.use { it.write(body.toString().toByteArray()) }
            val status = c.responseCode
            val text = (if (status < 400) c.inputStream else c.errorStream)?.bufferedReader()?.use { it.readText() } ?: ""
            val json = try { JSONObject(text) } catch (_: Exception) { JSONObject() }
            return status to json
        } catch (e: IOException) {
            throw Offline("连不上 $origin：${e.message}")
        } finally {
            c.disconnect()
        }
    }

    companion object {
        /** 和服务端 `auth.CookieName` 一字不差 —— 对不上的表现是「批准了，进去还是配对页」 */
        const val COOKIE = "hw_dev"
    }
}
