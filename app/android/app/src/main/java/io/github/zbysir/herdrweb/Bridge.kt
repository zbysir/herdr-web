package io.github.zbysir.herdrweb

import android.os.Handler
import android.os.Looper
import android.webkit.WebView
import androidx.webkit.JavaScriptReplyProxy
import androidx.webkit.WebViewCompat
import androidx.webkit.WebViewFeature
import org.json.JSONObject

/**
 * 网页 ↔ 壳 的那条线。网页那一半在 `web/src/lib/native.ts`，消息格式两边要对得上。
 *
 * **只对服务器那个 origin 开**（`addWebMessageListener` 的白名单），不用
 * `addJavascriptInterface` —— 后者对 WebView 里所有 frame、所有 origin 都可见，而这个页面里
 * 渲染着 agent 的输出（不可信文本）。也只认主 frame 发来的。桥上只放无害的口：报平台、
 * 全屏、配色、开外部链接、返回键 —— 不放读文件、发任意 Intent 这类。
 */
class Bridge(private val act: MainActivity, web: WebView, origin: String) {
    /** 页面发过 hello 才有；换页 / 重载时作废（`reset`），等新页面再来一次 hello */
    private var page: JavaScriptReplyProxy? = null
    private var backCb: ((Boolean) -> Unit)? = null
    private val main = Handler(Looper.getMainLooper())

    val ready get() = page != null

    init {
        if (WebViewFeature.isFeatureSupported(WebViewFeature.WEB_MESSAGE_LISTENER)) {
            WebViewCompat.addWebMessageListener(web, "herdrNative", setOf(origin)) { _, msg, _, isMain, reply ->
                val data = msg.data
                if (isMain && data != null) onMessage(data, reply)
            }
        }
    }

    fun reset() {
        page = null
        backCb?.invoke(false)
        backCb = null
    }

    private fun onMessage(raw: String, reply: JavaScriptReplyProxy) {
        val m = try { JSONObject(raw) } catch (_: Exception) { return }
        when (m.optString("t")) {
            "hello" -> {
                page = reply
                act.hideLoading()
                send(JSONObject().put("t", "hello").put("platform", "android").put("version", BuildInfo.version(act)))
            }
            "back" -> {
                val cb = backCb ?: return
                backCb = null
                cb(m.optBoolean("handled"))
            }
            "fullscreen" -> {
                act.setFullscreen(m.optBoolean("on"))
                send(JSONObject().put("t", "fullscreen").put("on", m.optBoolean("on")))
            }
            "theme" -> act.applyTheme(m.optString("bg"), m.optBoolean("light"))
            "open" -> act.openExternal(m.optString("url"))
            "setup" -> act.showSetup(null)
            "login" -> act.startLogin(m.optBoolean("reauth"))
            "loginCancel" -> act.cancelLogin()
            "notify" -> {
                val op = m.optString("op")
                if (op == "get") send(act.notifyState()) else act.notifyOp(op, m.optBoolean("on"))
            }
        }
    }

    /** 后台通知那一块的状态（开没开 / 有没有通知权限 / 是否免电池优化），设置页照它画 */
    fun notifyState(m: JSONObject) = send(m)

    /** 登录流程的进展：`done` / `error`（带 `msg`）/ `cancel` */
    fun loginState(state: String, msg: String? = null) {
        val m = JSONObject().put("t", "login").put("state", state)
        if (msg != null) m.put("msg", msg)
        send(m)
    }

    /**
     * 返回键先问页面：开着面板 / 查看器就由页面收掉（和 Esc 同一套「谁在上面先关谁」）。
     * 页面没回音（卡住了、或者是个没接桥的旧版服务端）时 400ms 后按「没处理」走，
     * 不能让返回键变成「按了没反应」。
     */
    fun askBack(cb: (Boolean) -> Unit) {
        if (page == null) return cb(false)
        backCb?.invoke(false)
        backCb = cb
        send(JSONObject().put("t", "back"))
        main.postDelayed({
            if (backCb === cb) {
                backCb = null
                cb(false)
            }
        }, 400)
    }

    private fun send(m: JSONObject) {
        try {
            page?.postMessage(m.toString())
        } catch (_: Exception) {
            page = null // 页面已经没了
        }
    }
}
