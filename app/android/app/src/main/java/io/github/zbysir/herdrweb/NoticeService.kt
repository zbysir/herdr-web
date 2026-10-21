package io.github.zbysir.herdrweb

import android.Manifest
import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.content.pm.ServiceInfo
import android.os.Build
import android.os.IBinder
import android.webkit.CookieManager
import android.webkit.WebSettings
import androidx.core.app.NotificationCompat
import androidx.core.app.ServiceCompat
import androidx.core.content.ContextCompat
import org.json.JSONObject
import java.io.IOException
import java.net.HttpURLConnection
import java.net.URL

/**
 * 后台通知：App 不在前台时，agent「等你回答 / 跑完了」也能弹系统通知。
 *
 * **不走 FCM / 中转**（用户选的，见 docs/dev/NATIVE-APP.md §4 的「自用这一档」）：国内机型多半
 * 没有 Google 服务，FCM 在那上面一条都收不到、还不报错。这里是一个前台服务直连用户自己那台
 * herdr-web，挂在 `/api/herdr/notices?wait=50` 上**长轮询**（服务端 agentwatch.WaitNotices）——
 * 一分钟一个请求，提示几乎实时；短轮询的话每几秒发一次请求会让基带一直醒着。
 *
 * 代价写在明处：通知栏常驻一条「在等 agent 的消息」（前台服务的硬要求），国产系统还常要人
 * 手动允许后台运行 / 自启动（设置里那个「允许后台运行」按钮管的是系统那一层的电池优化）。
 *
 * 凭据就是 WebView 里那份 cookie（`hw_dev`），按 Bearer 带上 —— 和 `herdr-web connect`
 * 同一条路，不是 Ambient，不用过 CSRF。每一拍都重新读，所以人在 App 里重新登录 / 重验之后
 * 这边自己就接上了。
 */
class NoticeService : Service() {
    @Volatile private var stop = false
    private var worker: Thread? = null
    private var watcher: Thread? = null
    /**
     * 最后一次画好的那条常驻通知。App 每次回前台都会再 startService 一次（见 MainActivity
     * 的 onResume），onStartCommand 里的 startForeground 要用**它**，不能每次都用「正在连接」
     * 那条 —— 不然一回前台一览就被盖回去，而一览只在状态变了时才重画，就一直停在那句话上
     * （真机上撞到的）。
     */
    @Volatile private var lastStatus: Notification? = null

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        channels(this)
        val type = if (Build.VERSION.SDK_INT >= 34) ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE else 0
        ServiceCompat.startForeground(this, ID_SERVICE, lastStatus ?: statusNote(null), type)
        if (worker == null) {
            stop = false
            worker = Thread(::loop, "herdr-notices").apply { isDaemon = true; start() }
            watcher = Thread(::statusLoop, "herdr-status").apply { isDaemon = true; start() }
        }
        return START_STICKY // 被系统杀了还会被拉起来
    }

    override fun onDestroy() {
        stop = true
        worker?.interrupt()
        worker = null
        watcher?.interrupt()
        watcher = null
        super.onDestroy()
    }

    private fun loop() {
        // since < 0 = 还没对过底：第一拍只取当前 seq、**不弹**，不然一开服务就把环里的旧提示
        // 全弹一遍（重启手机、App 被杀后拉起都会走这里）
        var since = -1L
        var backoff = 2_000L
        var authWarned = false
        while (!stop) {
            val origin = Server.load(this)?.let(Server::origin)
            if (origin == null) { stopSelf(); return }
            val token = token(origin)
            if (token == null) { nap(30_000); continue } // 还没登录：安静等着，别弹「失效了」
            try {
                val q = if (since < 0) "" else "?since=$since&wait=50"
                val (status, body) = get("$origin/api/herdr/notices$q", token)
                if (status == 401 || status == 403) {
                    if (!authWarned) authNote(JSONObject(body.ifEmpty { "{}" }).optString("need") == "passkey")
                    authWarned = true
                    nap(5 * 60_000)
                    continue
                }
                if (status != 200) throw IOException("HTTP $status")
                if (authWarned) {
                    authWarned = false
                    nm().cancel(ID_AUTH)
                }
                val j = JSONObject(body)
                val seq = j.optLong("seq")
                val list = j.optJSONArray("notices")
                if (since >= 0 && list != null && !MainActivity.visible) {
                    for (i in 0 until list.length()) show(list.getJSONObject(i))
                }
                // 服务端重启过：seq 从头数了，比我们手上的还小。不改回去的话要等它数过旧值才有新的
                since = seq
                backoff = 2_000
            } catch (_: InterruptedException) {
                return
            } catch (e: Exception) {
                // 断网 / 切网 / 服务端重启：退避重连，别把错误弹给人 —— 回来了自己就接上
                android.util.Log.w(TAG, "提示这一拍失败，${backoff}ms 后重试", e)
                nap(backoff)
                backoff = (backoff * 2).coerceAtMost(60_000)
            }
        }
    }

    /**
     * 常驻通知里的「agent 状态一览」：挂在 `/api/herdr/agents?ver=&wait=50` 上（服务端
     * agentwatch.WaitStatus），任何一个 agent 状态一变就当场回，刷新一下常驻那条通知。
     *
     * 那条通知本来就得在（前台服务的硬要求），让它显示有用的东西，而不是一句白占位置的
     * 「在等 agent 的消息」（用户要的「在通知栏里看到正在运行的 agent 的状态一览」）。
     * 和提示那条循环分开跑：一个挂着等提示、一个挂着等状态，谁也不耽误谁。
     */
    private fun statusLoop() {
        var ver = 0L
        var backoff = 2_000L
        while (!stop) {
            val origin = Server.load(this)?.let(Server::origin) ?: return
            val token = token(origin)
            if (token == null) { nap(30_000); continue }
            try {
                val t0 = System.currentTimeMillis()
                val (status, body) = get("$origin/api/herdr/agents?ver=$ver&wait=50", token)
                android.util.Log.i(TAG, "一览：HTTP $status，等了 ${System.currentTimeMillis() - t0}ms（ver=$ver）")
                if (status == 401 || status == 403) {
                    // 登录失效的提醒归提示那条循环发，这边只把一览换成一句实话
                    postStatus(statusNote(null, "登录失效了，打开 App 重新登录"))
                    nap(5 * 60_000)
                    continue
                }
                if (status != 200) throw IOException("HTTP $status")
                val j = JSONObject(body)
                ver = j.optLong("ver")
                val arr = j.optJSONArray("agents") ?: org.json.JSONArray()
                val list = (0 until arr.length()).map { arr.getJSONObject(it) }
                android.util.Log.i(TAG, "一览：ver→$ver，在动 ${list.count { it.optString("status") in setOf("working", "blocked") }} 个")
                postStatus(statusNote(list))
                AgentsWidget.push(this, list) // 桌面小部件跟着同一份数据刷新
                backoff = 2_000
            } catch (_: InterruptedException) {
                return
            } catch (e: Exception) {
                android.util.Log.w(TAG, "状态一览这一拍失败，${backoff}ms 后重试", e)
                nap(backoff)
                backoff = (backoff * 2).coerceAtMost(60_000)
            }
        }
    }

    private fun postStatus(n: Notification) {
        lastStatus = n
        nm().notify(ID_SERVICE, n)
    }

    private fun nap(ms: Long) {
        try { Thread.sleep(ms) } catch (_: InterruptedException) { stop = true }
    }

    private fun get(url: String, token: String): Pair<Int, String> {
        val c = URL(url).openConnection() as HttpURLConnection
        try {
            c.connectTimeout = 15_000
            c.readTimeout = 70_000 // 服务端最多挂 50 秒，留余量
            c.setRequestProperty("Authorization", "Bearer $token")
            c.setRequestProperty("User-Agent", ua(this))
            val status = c.responseCode
            val text = (if (status < 400) c.inputStream else c.errorStream)?.bufferedReader()?.use { it.readText() } ?: ""
            return status to text
        } finally {
            c.disconnect()
        }
    }

    /** 一条提示一条通知；同一个 pane 的新提示**替换**旧的（id 按 pane 算），不在通知栏里攒一串 */
    private fun show(n: JSONObject) {
        if (!canNotify(this)) return
        val wait = n.optString("status") == "blocked"
        val agent = n.optString("agent").ifEmpty { "agent" }
        val head = if (wait) "$agent 在等你回答" else "已完成"
        val title = n.optString("title").let { if (it.isEmpty()) head else "$head · $it" }
        val text = n.optString("text").ifEmpty { if (wait) "回来看一眼" else "这一轮结束了" }
        val pane = n.optString("pane")
        val note = NotificationCompat.Builder(this, CH_AGENT)
            .setSmallIcon(R.drawable.ic_notify)
            .setContentTitle(title)
            .setContentText(text)
            .setStyle(NotificationCompat.BigTextStyle().bigText(text))
            .setCategory(NotificationCompat.CATEGORY_MESSAGE)
            .setPriority(NotificationCompat.PRIORITY_HIGH)
            .setWhen(n.optLong("at").takeIf { it > 0 } ?: System.currentTimeMillis())
            .setShowWhen(true)
            .setAutoCancel(true)
            .setContentIntent(openApp(this, pane))
            .build()
        nm().notify(ID_AGENT_BASE + (pane.hashCode() and 0xffff), note)
    }

    private fun authNote(passkey: Boolean) {
        if (!canNotify(this)) return
        val note = NotificationCompat.Builder(this, CH_AGENT)
            .setSmallIcon(R.drawable.ic_notify)
            .setContentTitle(if (passkey) "herdr 要你验证一次" else "herdr 的登录失效了")
            .setContentText(if (passkey) "打开 App 用 passkey 验证，验完接着收通知" else "打开 App 重新登录，之后才收得到通知")
            .setAutoCancel(true)
            .setContentIntent(openApp(this, null))
            .build()
        nm().notify(ID_AUTH, note)
    }

    /**
     * 常驻那条：标题是「几个等你 · 几个在跑」，展开是每个 agent 一行，**等你的排最前**
     * （那是要你动手的），然后在跑的、跑完的、空闲的。[list] 为 null = 还没拿到（刚起 / 连不上）。
     */
    private fun statusNote(list: List<JSONObject>?, note: String? = null): Notification {
        val b = NotificationCompat.Builder(this, CH_STATUS)
            .setSmallIcon(R.drawable.ic_notify)
            .setOngoing(true)
            .setOnlyAlertOnce(true) // 一览每次刷新都不响不震
            .setSilent(true)
            .setShowWhen(false)
            .setPriority(NotificationCompat.PRIORITY_DEFAULT)
            .setContentIntent(openApp(this, null))
        if (list == null) {
            return b.setContentTitle(note ?: "正在连接 ${Server.load(this)?.host ?: ""}…").build()
        }
        val sum = Agents.summarize(list)
        /*
         * 自己画（RemoteViews），不用 InboxStyle：用户要的是「几个在跑就直接列出几个」，
         * 一句「3 个在跑」得下拉展开才看得到是谁。每条标题最多折两行（用户要看全）。
         * 收起那一版高度有限（真机小米上大约四行多字），三条两行的放不下、第三条会被拦腰
         * 截掉 —— 所以收起时最多 2 条，多出来的用一行小字说「还有几个」；展开版全列。
         */
        val small = rows(sum.shown.take(2), null, more = sum.shown.size - 2)
        val big = rows(sum.shown.take(MAX_LINES), listOfNotNull(
            if (sum.shown.size > MAX_LINES) "还有 ${sum.shown.size - MAX_LINES} 个在动" else null,
            sum.foot,
        ).joinToString("，").ifEmpty { null })
        return b.setContentTitle(sum.title)
            .setContentText(sum.shown.firstOrNull()?.let(Agents::line) ?: (sum.foot ?: ""))
            .setStyle(NotificationCompat.DecoratedCustomViewStyle())
            .setCustomContentView(small)
            .setCustomBigContentView(big)
            .build()
    }

    /** [more] > 0 时在最后一行右边挂一个「+N」（收起版用：放不下的那几个，下拉能看到） */
    private fun rows(items: List<JSONObject>, foot: String?, more: Int = 0): android.widget.RemoteViews {
        val v = android.widget.RemoteViews(packageName, R.layout.notif_status)
        v.removeAllViews(R.id.rows)
        for ((i, a) in items.withIndex()) {
            val r = android.widget.RemoteViews(packageName, R.layout.notif_row)
            val st = a.optString("status")
            r.setTextViewText(R.id.st, Agents.label(st))
            r.setTextColor(R.id.st, Agents.color(st))
            r.setTextViewText(R.id.what, Agents.what(a))
            if (more > 0 && i == items.lastIndex) {
                r.setTextViewText(R.id.tail, "+$more")
                r.setViewVisibility(R.id.tail, android.view.View.VISIBLE)
            }
            v.addView(R.id.rows, r)
        }
        if (foot != null) {
            v.setTextViewText(R.id.foot, foot)
            v.setViewVisibility(R.id.foot, android.view.View.VISIBLE)
        }
        return v
    }

    private fun nm() = getSystemService(NotificationManager::class.java)

    companion object {
        private const val TAG = "herdr"
        private const val CH_AGENT = "agent"
        // 常驻那条的频道。原来叫 service、级别 MIN（只是个占位）；改成状态一览之后要看得见，
        // 级别得是 LOW —— 而频道的级别建好之后 App 改不了，所以换了个新 id、旧的删掉
        // 第二次换：LOW 在小米 HyperOS 上会被自动收进底部「更多通知」，主列表里看不见
        //（真机上撞到的）。DEFAULT + 频道静音 + setSilent，看得见、不响不震
        private const val CH_STATUS = "agents"
        private const val MAX_LINES = 7
        private const val ID_SERVICE = 1
        private const val ID_AUTH = 2
        private const val ID_AGENT_BASE = 1000
        private const val PREFS = "notify"

        fun enabled(ctx: Context) = ctx.getSharedPreferences(PREFS, MODE_PRIVATE).getBoolean("on", true)

        fun setEnabled(ctx: Context, on: Boolean) {
            ctx.getSharedPreferences(PREFS, MODE_PRIVATE).edit().putBoolean("on", on).apply()
            if (on) start(ctx) else ctx.stopService(Intent(ctx, NoticeService::class.java))
        }

        /** 开着、而且配过服务器才起。没给通知权限也照起：权限随时可能补上，到时候不用重启 App */
        fun start(ctx: Context) {
            if (!enabled(ctx) || Server.load(ctx) == null) return
            try {
                ContextCompat.startForegroundService(ctx, Intent(ctx, NoticeService::class.java))
            } catch (_: Exception) {
                // Android 12+ 不许从后台起前台服务；App 下次回到前台时会再起
            }
        }

        fun canNotify(ctx: Context) = Build.VERSION.SDK_INT < 33 ||
            ContextCompat.checkSelfPermission(ctx, Manifest.permission.POST_NOTIFICATIONS) == PackageManager.PERMISSION_GRANTED

        fun channels(ctx: Context) {
            val nm = ctx.getSystemService(NotificationManager::class.java)
            nm.createNotificationChannel(
                NotificationChannel(CH_AGENT, "agent 的消息", NotificationManager.IMPORTANCE_HIGH).apply {
                    description = "agent 等你回答、跑完了"
                },
            )
            // 常驻那一条：agent 状态一览。LOW = 看得见但不响不震
            nm.deleteNotificationChannel("service")
            nm.deleteNotificationChannel("status")
            nm.createNotificationChannel(
                NotificationChannel(CH_STATUS, "agent 状态一览", NotificationManager.IMPORTANCE_DEFAULT).apply {
                    description = "常驻通知栏：哪几个 agent 在跑、哪几个在等你"
                    setShowBadge(false)
                    setSound(null, null)
                    enableVibration(false)
                },
            )
        }

        /** 测试通知（设置里「试一下」）：不看开关、不看在不在前台，当场弹一条 */
        fun test(ctx: Context) {
            channels(ctx)
            if (!canNotify(ctx)) return
            val note = NotificationCompat.Builder(ctx, CH_AGENT)
                .setSmallIcon(R.drawable.ic_notify)
                .setContentTitle("herdr 通知试一下")
                .setContentText("看到这条就说明通知是通的")
                .setAutoCancel(true)
                .setContentIntent(openApp(ctx, null))
                .build()
            ctx.getSystemService(NotificationManager::class.java).notify(ID_AUTH + 1, note)
        }

        private fun openApp(ctx: Context, pane: String?): PendingIntent {
            val i = Intent(ctx, MainActivity::class.java)
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_SINGLE_TOP)
            if (pane != null) i.putExtra("pane", pane)
            return PendingIntent.getActivity(
                ctx, pane?.hashCode() ?: 0, i,
                PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
            )
        }

        private fun token(origin: String): String? =
            CookieManager.getInstance().getCookie(origin)
                ?.split(';')
                ?.map { it.trim() }
                ?.firstOrNull { it.startsWith("${Login.COOKIE}=") }
                ?.substringAfter('=')
                ?.ifEmpty { null }

        /** 和 WebView 同一个 UA（设备列表里还是「Android · App」那一台） */
        fun ua(ctx: Context) = "${WebSettings.getDefaultUserAgent(ctx)} HerdrWebApp/${BuildInfo.version(ctx)}"
    }
}
