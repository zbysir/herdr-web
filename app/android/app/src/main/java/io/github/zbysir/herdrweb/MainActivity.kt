package io.github.zbysir.herdrweb

import android.annotation.SuppressLint
import android.content.ActivityNotFoundException
import android.content.Intent
import android.graphics.Color
import android.graphics.drawable.ColorDrawable
import android.net.Uri
import android.net.http.SslError
import android.os.Bundle
import android.os.Message
import android.text.InputType
import android.util.TypedValue
import android.view.Gravity
import android.view.View
import android.view.ViewGroup.LayoutParams.MATCH_PARENT
import android.view.ViewGroup.LayoutParams.WRAP_CONTENT
import android.view.inputmethod.EditorInfo
import android.webkit.CookieManager
import android.webkit.PermissionRequest
import android.webkit.RenderProcessGoneDetail
import android.webkit.SslErrorHandler
import android.webkit.ValueCallback
import android.webkit.WebChromeClient
import android.webkit.WebResourceError
import android.webkit.WebResourceRequest
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.Button
import android.widget.EditText
import android.widget.FrameLayout
import android.widget.LinearLayout
import android.widget.TextView
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.activity.SystemBarStyle
import androidx.activity.addCallback
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.browser.customtabs.CustomTabColorSchemeParams
import androidx.browser.customtabs.CustomTabsIntent
import android.graphics.Typeface
import android.os.Handler
import android.os.Looper
import java.util.concurrent.Executors
import androidx.core.view.ViewCompat
import androidx.core.view.WindowInsetsCompat
import androidx.core.view.WindowInsetsControllerCompat

/**
 * 整个壳就这一个 Activity：没填过服务器 → 填地址那一屏；填过 → 一个 WebView 加载那台
 * herdr-web。界面全在网页里（为什么见 docs/dev/NATIVE-APP.md），这里只做网页做不到的事。
 */
class MainActivity : ComponentActivity() {
    private lateinit var root: FrameLayout
    private val bars by lazy { WindowInsetsControllerCompat(window, root) }

    private var web: WebView? = null
    private var bridge: Bridge? = null
    /** 当前连的那台的 origin（`scheme://host[:port]`） */
    private var origin: String? = null
    /** WebView 的 UA（带 HerdrWebApp 那一截）。登录那几个请求也用它，服务端按它给设备起名 */
    private var webUA: String = ""

    private val main = Handler(Looper.getMainLooper())
    private val io = Executors.newSingleThreadExecutor()
    /** 正在进行的那次 passkey 登录；null = 没有 */
    private var login: LoginRun? = null

    private class LoginRun(val reauth: Boolean) {
        @Volatile var cancelled = false
        var start: Login.Start? = null
        var overlay: View? = null
    }

    private var fileCb: ValueCallback<Array<Uri>>? = null
    private val pickFiles = registerForActivityResult(ActivityResultContracts.StartActivityForResult()) { r ->
        val cb = fileCb ?: return@registerForActivityResult
        fileCb = null
        val data = r.data
        if (r.resultCode != RESULT_OK || data == null) return@registerForActivityResult cb.onReceiveValue(null)
        // 多选时结果在 clipData 里，FileChooserParams.parseResult 只认单个 data
        val clip = data.clipData
        val uris = if (clip != null) Array(clip.itemCount) { clip.getItemAt(it).uri }
        else data.data?.let { arrayOf(it) }
        cb.onReceiveValue(uris)
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge(
            statusBarStyle = SystemBarStyle.dark(Color.TRANSPARENT),
            navigationBarStyle = SystemBarStyle.dark(Color.TRANSPARENT),
        )
        root = FrameLayout(this).apply { setBackgroundColor(getColor(R.color.bg)) }
        setContentView(root)
        // 系统栏 + 输入法 + 刘海一起垫成 padding：键盘一弹 WebView 就变矮，网页那边看到的
        // 是一次普通的视口变化（和 PWA 上 resizes-content 一样），终端按它重排。
        // 不垫 ime 的话 targetSdk 35 起强制 edge-to-edge，adjustResize 不再生效，键盘直接
        // 盖住终端底下那几行和发件箱。
        ViewCompat.setOnApplyWindowInsetsListener(root) { v, ins ->
            val b = ins.getInsets(
                WindowInsetsCompat.Type.systemBars() or WindowInsetsCompat.Type.ime() or
                    WindowInsetsCompat.Type.displayCutout()
            )
            v.setPadding(b.left, b.top, b.right, b.bottom)
            WindowInsetsCompat.CONSUMED
        }
        WebView.setWebContentsDebuggingEnabled(BuildInfo.debuggable(this))
        onBackPressedDispatcher.addCallback(this) { back() }

        val saved = Server.load(this)
        if (saved == null) showSetup(null) else showWeb(saved)
    }

    override fun onResume() {
        super.onResume()
        visible = true
        web?.onResume()
        // 每次回前台都确认一下服务在跑：Android 12+ 不许从后台起前台服务，被系统杀过之后
        // 只有这一刻能再起。已经在跑的话这一下是空操作
        NoticeService.start(this)
        askNotifyOnce()
        bridge?.notifyState(notifyState())
    }

    /**
     * 通知默认开着，但 Android 13+ 的通知权限要运行时问 —— 不问的话装上之后一条都收不到、
     * 也没人告诉你为什么。只问一次（拒了就去设置里那个开关上再点），而且等登录过了再问：
     * 第一屏就弹权限框，人还不知道这个 App 是干嘛的。
     */
    private fun askNotifyOnce() {
        if (android.os.Build.VERSION.SDK_INT < 33 || NoticeService.canNotify(this)) return
        if (!NoticeService.enabled(this)) return
        val o = origin ?: return
        if (cookieToken(o) == null) return
        val prefs = getSharedPreferences("notify", MODE_PRIVATE)
        if (prefs.getBoolean("asked", false)) return
        prefs.edit().putBoolean("asked", true).apply()
        askNotify.launch(android.Manifest.permission.POST_NOTIFICATIONS)
    }

    override fun onPause() {
        visible = false
        // 页面那边会收到 visibilitychange（hidden），终端的「回前台发探针、僵了就重连」照常工作
        web?.onPause()
        CookieManager.getInstance().flush()
        super.onPause()
    }

    override fun onDestroy() {
        dropWeb()
        io.shutdownNow()
        super.onDestroy()
    }

    // ---- 填地址那一屏 ----

    fun showSetup(error: String?) {
        dropWeb()
        root.removeAllViews()
        val pad = dp(24)
        val col = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            gravity = Gravity.CENTER_VERTICAL
            setPadding(pad, pad, pad, pad)
        }
        col.addView(text("连接到你的 herdr-web", 20f, Color.WHITE))
        col.addView(
            text("填服务器地址，或者粘贴终端里打印的配对链接（…/?pair=…）。", 14f, Color.parseColor("#A3A3A3")),
            LinearLayout.LayoutParams(MATCH_PARENT, WRAP_CONTENT).apply { topMargin = dp(8) },
        )
        val input = EditText(this).apply {
            hint = "https://herdr.example.com"
            inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_URI
            imeOptions = EditorInfo.IME_ACTION_GO
            setSingleLine()
            setTextColor(Color.WHITE)
            setHintTextColor(Color.parseColor("#666666"))
            Server.load(this@MainActivity)?.let { setText(it.toString()) }
        }
        col.addView(input, LinearLayout.LayoutParams(MATCH_PARENT, WRAP_CONTENT).apply { topMargin = dp(20) })
        val err = text(error ?: "", 14f, Color.parseColor("#F87171")).apply {
            visibility = if (error == null) View.GONE else View.VISIBLE
        }
        col.addView(err, LinearLayout.LayoutParams(MATCH_PARENT, WRAP_CONTENT).apply { topMargin = dp(8) })
        val go = Button(this).apply { text = "连接" }
        col.addView(go, LinearLayout.LayoutParams(MATCH_PARENT, WRAP_CONTENT).apply { topMargin = dp(16) })

        val submit = {
            val u = Server.parse(input.text.toString())
            when {
                u == null -> {
                    err.text = "认不出这个地址"
                    err.visibility = View.VISIBLE
                }
                // release 包在网络层就拦明文（没开 usesCleartextTraffic），这里先说清楚，
                // 别让人看着一个「连不上」去猜
                u.scheme == "http" && !BuildInfo.debuggable(this) && !Server.isLocalHost(u.host) -> {
                    err.text = "要用 https 地址"
                    err.visibility = View.VISIBLE
                }
                else -> {
                    val o = Server.origin(u)
                    Server.save(this, o)
                    showWeb(Uri.parse(o), u)
                }
            }
        }
        go.setOnClickListener { submit() }
        input.setOnEditorActionListener { _, id, _ ->
            if (id == EditorInfo.IME_ACTION_GO) { submit(); true } else false
        }
        root.addView(col, FrameLayout.LayoutParams(MATCH_PARENT, MATCH_PARENT))
    }

    // ---- WebView ----

    /** [start] 是第一次要打开的完整地址（配对链接带着 `?pair=`），默认就是 origin 本身 */
    @SuppressLint("SetJavaScriptEnabled")
    private fun showWeb(server: Uri, start: Uri = server) {
        dropWeb()
        root.removeAllViews()
        val o = Server.origin(server)
        origin = o
        val w = WebView(this)
        w.setBackgroundColor(getColor(R.color.bg))
        w.settings.apply {
            javaScriptEnabled = true
            domStorageEnabled = true
            allowFileAccess = false
            allowContentAccess = false
            mixedContentMode = WebSettings.MIXED_CONTENT_NEVER_ALLOW
            // 系统字体放大会按比例改网页字号 —— 终端的行列数是按字宽量出来的，放大后整屏对不齐
            textZoom = 100
            // target=_blank / window.open 走 onCreateWindow，在那儿转给系统浏览器
            setSupportMultipleWindows(true)
            javaScriptCanOpenWindowsAutomatically = true
            // 服务端的设备列表按 UA 起名，带上这一截才分得出「App」和「手机浏览器」
            userAgentString = "$userAgentString HerdrWebApp/${BuildInfo.version(this@MainActivity)}"
            webUA = userAgentString
        }
        CookieManager.getInstance().apply {
            setAcceptCookie(true)
            setAcceptThirdPartyCookies(w, false)
        }
        w.webViewClient = Client()
        w.webChromeClient = Chrome()
        w.setDownloadListener { url, _, _, _, _ -> openExternal(url) }
        web = w
        bridge = Bridge(this, w, o)
        root.addView(w, FrameLayout.LayoutParams(MATCH_PARENT, MATCH_PARENT))
        showLoading(server.host ?: o)
        w.loadUrl(start.toString())
    }

    // ---- 加载中 ----

    private var loading: View? = null
    private var loadingBar: android.widget.ProgressBar? = null

    /**
     * 页面画出来之前盖一层「正在连接」。不盖的话那段时间只有 WebView 的深底色 —— 隧道窄的时候
     * 第一次打开要下载将近 1MB 前端（实测 frp 那条二三十秒），人对着一块黑屏只会以为卡死了
     * （用户报的「输完地址就黑屏」）。页面一 hello（Bridge）或 onPageFinished 就淡出。
     */
    private fun showLoading(host: String) {
        hideLoading(fade = false)
        val box = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            gravity = Gravity.CENTER
            setBackgroundColor(getColor(R.color.bg))
            isClickable = true
        }
        val logo = android.widget.ImageView(this).apply { setImageResource(R.drawable.ic_launcher_art) }
        box.addView(logo, LinearLayout.LayoutParams(dp(72), dp(72)))
        val spin = android.widget.ProgressBar(this).apply {
            isIndeterminate = true
            indeterminateTintList = android.content.res.ColorStateList.valueOf(getColor(R.color.brand))
        }
        box.addView(spin, LinearLayout.LayoutParams(dp(36), dp(36)).apply { topMargin = dp(28) })
        box.addView(
            text("正在连接 $host…", 14f, Color.parseColor("#A3A3A3")).apply { gravity = Gravity.CENTER },
            LinearLayout.LayoutParams(WRAP_CONTENT, WRAP_CONTENT).apply { topMargin = dp(16) },
        )
        // 真实进度（WebChromeClient.onProgressChanged），细细一条，让人看见「在动」
        val bar = android.widget.ProgressBar(this, null, android.R.attr.progressBarStyleHorizontal).apply {
            max = 100
            progressTintList = android.content.res.ColorStateList.valueOf(getColor(R.color.brand))
        }
        box.addView(bar, LinearLayout.LayoutParams(dp(160), dp(4)).apply { topMargin = dp(14) })
        val slow = text(
            "网络比较慢。第一次打开要下载一次界面（之后有缓存，会快很多）。",
            12f, Color.parseColor("#737373"),
        ).apply {
            gravity = Gravity.CENTER
            visibility = View.INVISIBLE
        }
        box.addView(slow, LinearLayout.LayoutParams(dp(260), WRAP_CONTENT).apply { topMargin = dp(16) })
        main.postDelayed({ if (loading === box) slow.visibility = View.VISIBLE }, 6_000)
        root.addView(box, FrameLayout.LayoutParams(MATCH_PARENT, MATCH_PARENT))
        loading = box
        loadingBar = bar
    }

    fun hideLoading(fade: Boolean = true) {
        val v = loading ?: return
        loading = null
        loadingBar = null
        if (!fade) return root.removeView(v)
        v.animate().alpha(0f).setDuration(180).withEndAction { root.removeView(v) }.start()
    }

    private fun dropWeb() {
        hideLoading(fade = false)
        login?.let { endLogin(it, "cancel", null) }
        bridge?.reset()
        bridge = null
        web?.let {
            root.removeView(it)
            it.destroy()
        }
        web = null
    }

    private fun showError(msg: String) {
        hideLoading(fade = false)
        val o = origin ?: return showSetup(msg)
        val pad = dp(24)
        val box = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            gravity = Gravity.CENTER
            setPadding(pad, pad, pad, pad)
            setBackgroundColor(getColor(R.color.bg))
            isClickable = true // 挡住底下那个 WebView
        }
        box.addView(text(msg, 15f, Color.WHITE).apply { gravity = Gravity.CENTER })
        val retry = Button(this).apply { text = "重试" }
        val change = Button(this).apply { text = "换服务器" }
        box.addView(retry, LinearLayout.LayoutParams(WRAP_CONTENT, WRAP_CONTENT).apply { topMargin = dp(20) })
        box.addView(change, LinearLayout.LayoutParams(WRAP_CONTENT, WRAP_CONTENT).apply { topMargin = dp(8) })
        retry.setOnClickListener { showWeb(Uri.parse(o)) }
        change.setOnClickListener { showSetup(null) }
        root.addView(box, FrameLayout.LayoutParams(MATCH_PARENT, MATCH_PARENT))
    }

    private inner class Client : WebViewClient() {
        override fun shouldOverrideUrlLoading(view: WebView, req: WebResourceRequest): Boolean {
            val u = req.url
            return when (u.scheme) {
                "http", "https" -> {
                    if (Server.origin(u) == origin || Server.isLocalHost(u.host)) false
                    else { openExternal(u.toString()); true }
                }
                "about", "blob", "data" -> false
                else -> { openExternal(u.toString()); true } // mailto: / tel: / intent: ……
            }
        }

        override fun onPageFinished(view: WebView, url: String?) {
            hideLoading() // 兜底：页面里没接桥的旧版服务端不会 hello
        }

        override fun onPageStarted(view: WebView, url: String?, favicon: android.graphics.Bitmap?) {
            bridge?.reset() // 新页面要重新 hello，旧的回信通道已经作废
        }

        override fun onReceivedError(view: WebView, req: WebResourceRequest, err: WebResourceError) {
            if (req.isForMainFrame) showError("连不上 $origin\n${err.description}")
        }

        /**
         * **默认拒，绝不无条件 proceed** —— 那等于把 TLS 整个关掉，而这个 WebView 里跑着一个
         * 登录 shell。自签证书（`HERDR_WEB_TLS=self`、局域网直连口）要等「钉住本地 CA」做完
         * 才放行（NATIVE-APP.md §2）。子资源上的证书错（局域网嗅探那一下）照样拒，但不弹整屏
         * —— 嗅探失败本来就会安静地留在公网那条路上。
         */
        override fun onReceivedSslError(view: WebView, handler: SslErrorHandler, error: SslError) {
            handler.cancel()
            val u = Uri.parse(error.url)
            if (Server.origin(u) == origin) showError("$origin 的证书不被这台手机信任。\nApp 只认正式签发的证书（比如 ACME 签的）。")
        }

        /** 系统内存吃紧时会杀掉渲染进程；不接这个，App 会跟着一起崩 */
        override fun onRenderProcessGone(view: WebView, detail: RenderProcessGoneDetail): Boolean {
            val o = origin ?: return true
            if (view === web) showWeb(Uri.parse(o))
            return true
        }
    }

    private inner class Chrome : WebChromeClient() {
        override fun onProgressChanged(view: WebView, newProgress: Int) {
            loadingBar?.progress = newProgress
        }

        override fun onShowFileChooser(
            view: WebView, cb: ValueCallback<Array<Uri>>, params: FileChooserParams,
        ): Boolean {
            fileCb?.onReceiveValue(null)
            fileCb = cb
            val intent = params.createIntent()
            if (params.mode == FileChooserParams.MODE_OPEN_MULTIPLE) intent.putExtra(Intent.EXTRA_ALLOW_MULTIPLE, true)
            return try {
                pickFiles.launch(intent)
                true
            } catch (_: ActivityNotFoundException) {
                fileCb = null
                cb.onReceiveValue(null)
                true
            }
        }

        /**
         * 新窗口（`target=_blank`、`window.open`）：拿一个临时 WebView 接住它要去的地址，
         * 转给系统浏览器。文件查看器「新标签打开」那条是 `/_f/` 签名链接，不靠 cookie，
         * 在浏览器里一样打得开。
         */
        override fun onCreateWindow(view: WebView, isDialog: Boolean, isUserGesture: Boolean, resultMsg: Message): Boolean {
            val tmp = WebView(this@MainActivity)
            tmp.webViewClient = object : WebViewClient() {
                override fun shouldOverrideUrlLoading(v: WebView, r: WebResourceRequest): Boolean {
                    openExternal(r.url.toString())
                    v.destroy()
                    return true
                }
            }
            (resultMsg.obj as WebView.WebViewTransport).webView = tmp
            resultMsg.sendToTarget()
            return true
        }

        // 相机 / 麦克风暂时不给（语音投稿走的是输入法，不是 getUserMedia）
        override fun onPermissionRequest(request: PermissionRequest) = request.deny()
    }

    // ---- passkey 登录（device flow，见 Login.kt）----

    /**
     * 配对页上点「用 passkey 登录 / 验证」进来。[reauth] = 这台设备配过对、只是该重验了：
     * 带着现有凭据（Bearer）去发起，批准之后服务端当场续上，cookie 不用换。
     */
    fun startLogin(reauth: Boolean) {
        val o = origin ?: return
        if (login != null) return
        val run = LoginRun(reauth)
        login = run
        val bearer = if (reauth) cookieToken(o) else null
        io.execute {
            val api = Login(o, webUA)
            try {
                val st = try {
                    api.start(bearer)
                } catch (e: Login.Failure) {
                    // 凭据已经不认了（被撤销 / 过期）：重验不成，退成要一台新设备
                    // 只是连不上的话别退：那会白要一台新设备，把还有效的这份凭据换掉
                    if (bearer == null || e is Login.Offline) throw e
                    api.start(null)
                }
                if (run.cancelled) return@execute
                main.post {
                    run.start = st
                    showLoginOverlay(run, st)
                    // 批准页里一点输入框键盘就弹起来，半屏会被顶成整屏、把上面的码盖住（真机上
                    // Edge 就是这样）。所以先替人复制好，那边直接粘贴。这不是「把码带进批准页」：
                    // 码还是要人自己粘进去、自己看清发起方再刷 passkey
                    copyCode(st.code, false)
                    openApprove(st.url)
                }
                // 码 5 分钟过期（auth.CLITTL），过了服务端会说「没有这个请求」；这里再兜一个上限，
                // 一直连不上时别无限等下去
                val deadline = System.currentTimeMillis() + 6 * 60_000
                while (!run.cancelled) {
                    Thread.sleep(2000)
                    if (run.cancelled) return@execute
                    val p = try {
                        api.poll(st)
                    } catch (e: Login.Offline) {
                        if (System.currentTimeMillis() > deadline) throw e
                        continue
                    }
                    when (p) {
                        is Login.Poll.Pending -> continue
                        is Login.Poll.Denied -> {
                            main.post { endLogin(run, "error", "在浏览器里被拒绝了") }
                            return@execute
                        }
                        is Login.Poll.Approved -> {
                            main.post { approved(run, o, p.token) }
                            return@execute
                        }
                    }
                }
            } catch (e: Login.Failure) {
                main.post { endLogin(run, "error", e.message) }
            } catch (_: InterruptedException) {
            }
        }
    }

    fun cancelLogin() {
        login?.let { endLogin(it, "cancel", null) }
    }

    private fun approved(run: LoginRun, o: String, token: String?) {
        if (run.cancelled) return
        if (token == null) return endLogin(run, "done", null) // 重验：凭据没换，服务端已经续上了
        // 新设备：令牌就是设备凭据本身，和配对出来的那份 cookie 认的是同一张表。
        // 属性照抄服务端的 IssueCookie（HttpOnly + SameSite=Strict + https 时 Secure），
        // 寿命给满 400 天 —— 实际有效期服务端说了算，whoami 每次还会重新下发一遍
        val secure = if (o.startsWith("https://")) "; Secure" else ""
        val cm = CookieManager.getInstance()
        cm.setCookie(o, "${Login.COOKIE}=$token; Path=/; Max-Age=${400 * 24 * 3600}; HttpOnly; SameSite=Strict$secure") {
            cm.flush()
            endLogin(run, "done", null)
            NoticeService.start(this) // 有凭据了，后台通知可以接上了
            askNotifyOnce()
        }
    }

    /** 收尾：摘掉浮层、关掉批准页、告诉页面结果。done 之后页面自己翻进去（Pairing 的 onDone） */
    private fun endLogin(run: LoginRun, state: String, msg: String?) {
        if (login !== run) return
        run.cancelled = true
        login = null
        run.overlay?.let { root.removeView(it) }
        finishActivity(REQ_APPROVE) // 批准页还开着的话一起关掉
        bridge?.loginState(state, msg)
    }

    private fun cookieToken(o: String): String? =
        CookieManager.getInstance().getCookie(o)
            ?.split(';')
            ?.map { it.trim() }
            ?.firstOrNull { it.startsWith("${Login.COOKIE}=") }
            ?.substringAfter('=')
            ?.ifEmpty { null }

    /**
     * 批准页用 Custom Tabs 开、**只占下面一部分**（partial custom tab）：人要照着上面浮层里的码
     * 往下面那页里输。必须走 startActivityForResult 才给半屏（直接 startActivity 是整屏），
     * 也正好让 [endLogin] 能用 finishActivity 把它关掉。浏览器不支持半屏时退成整屏 ——
     * 那时码要先记住，所以浮层上留着「重新打开」。
     */
    @Suppress("DEPRECATION")
    private fun openApprove(url: String) {
        val bg = getColor(R.color.bg)
        val tab = CustomTabsIntent.Builder()
            .setInitialActivityHeightPx((resources.displayMetrics.heightPixels * 0.68).toInt())
            .setDefaultColorSchemeParams(CustomTabColorSchemeParams.Builder().setToolbarColor(bg).build())
            .setShareState(CustomTabsIntent.SHARE_STATE_OFF)
            .build()
        tab.intent.data = Uri.parse(url)
        try {
            startActivityForResult(tab.intent, REQ_APPROVE)
        } catch (_: ActivityNotFoundException) {
            login?.let { endLogin(it, "error", "这台手机上没有浏览器，打不开批准页") }
        }
    }

    private fun copyCode(code: String, toast: Boolean) {
        val cm = getSystemService(android.content.ClipboardManager::class.java)
        cm.setPrimaryClip(android.content.ClipData.newPlainText("herdr 批准码", code))
        // Android 13+ 系统自己会弹「已复制」，别再叠一条
        if (toast && android.os.Build.VERSION.SDK_INT < 33) Toast.makeText(this, "已复制", Toast.LENGTH_SHORT).show()
    }

    private fun showLoginOverlay(run: LoginRun, st: Login.Start) {
        val pad = dp(20)
        val box = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(pad, pad, pad, pad)
            setBackgroundColor(getColor(R.color.bg))
            isClickable = true
        }
        box.addView(text(if (run.reauth) "在浏览器里验证这台设备" else "在浏览器里批准这台设备", 18f, Color.WHITE))
        box.addView(
            text("在下面打开的批准页里输入这个码，看清发起方是「Android · App」，再刷一次 passkey。", 14f, Color.parseColor("#A3A3A3")),
            LinearLayout.LayoutParams(MATCH_PARENT, WRAP_CONTENT).apply { topMargin = dp(6) },
        )
        val shown = if (st.code.length == 8) st.code.substring(0, 4) + "-" + st.code.substring(4) else st.code
        box.addView(
            text(shown, 34f, getColor(R.color.brand)).apply {
                typeface = Typeface.MONOSPACE
                letterSpacing = 0.08f
                setOnClickListener { copyCode(shown, true) }
            },
            LinearLayout.LayoutParams(WRAP_CONTENT, WRAP_CONTENT).apply { topMargin = dp(14) },
        )
        box.addView(
            text("点一下码就复制；打开批准页时已经替你复制好了，在那边粘贴就行。", 12f, Color.parseColor("#737373")),
            LinearLayout.LayoutParams(MATCH_PARENT, WRAP_CONTENT).apply { topMargin = dp(4) },
        )
        val row = LinearLayout(this).apply { orientation = LinearLayout.HORIZONTAL }
        val reopen = Button(this).apply { text = "打开批准页" }
        val cancel = Button(this).apply { text = "取消" }
        row.addView(reopen)
        row.addView(cancel, LinearLayout.LayoutParams(WRAP_CONTENT, WRAP_CONTENT).apply { marginStart = dp(8) })
        box.addView(row, LinearLayout.LayoutParams(WRAP_CONTENT, WRAP_CONTENT).apply { topMargin = dp(14) })
        reopen.setOnClickListener { openApprove(st.url) }
        cancel.setOnClickListener { endLogin(run, "cancel", null) }
        // 只占上面一条，下面留给半屏的批准页
        root.addView(box, FrameLayout.LayoutParams(MATCH_PARENT, WRAP_CONTENT, Gravity.TOP))
        run.overlay = box
    }

    // ---- 桥调过来的 ----

    private fun back() {
        val b = bridge
        if (b == null || !b.ready) return fallbackBack()
        b.askBack { handled -> if (!handled) fallbackBack() }
    }

    /** 页面没东西可收：退到后台（不 finish —— 那会把 WebView 连同终端一起销毁） */
    private fun fallbackBack() {
        moveTaskToBack(true)
    }

    fun setFullscreen(on: Boolean) {
        if (on) {
            bars.systemBarsBehavior = WindowInsetsControllerCompat.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE
            bars.hide(WindowInsetsCompat.Type.systemBars())
        } else {
            bars.show(WindowInsetsCompat.Type.systemBars())
        }
    }

    /** 页面换了明暗 / 主题：系统栏那一条的底色和图标深浅跟着走，不然亮色主题顶上一条黑 */
    fun applyTheme(bg: String, light: Boolean) {
        val c = try { Color.parseColor(bg) } catch (_: Exception) { return }
        root.setBackgroundColor(c)
        window.setBackgroundDrawable(ColorDrawable(c))
        bars.isAppearanceLightStatusBars = light
        bars.isAppearanceLightNavigationBars = light
    }

    fun openExternal(url: String) {
        val u = Uri.parse(url)
        if (u.scheme != "http" && u.scheme != "https" && u.scheme != "mailto") return
        try {
            startActivity(Intent(Intent.ACTION_VIEW, u).addCategory(Intent.CATEGORY_BROWSABLE))
        } catch (_: ActivityNotFoundException) {
            Toast.makeText(this, "没有能打开这个链接的应用", Toast.LENGTH_SHORT).show()
        }
    }

    // ---- 小工具 ----

    private fun dp(v: Int) = TypedValue.applyDimension(TypedValue.COMPLEX_UNIT_DIP, v.toFloat(), resources.displayMetrics).toInt()

    private fun text(s: String, sp: Float, color: Int) = TextView(this).apply {
        text = s
        textSize = sp
        setTextColor(color)
    }

    // ---- 后台通知（NoticeService）----

    private val askNotify = registerForActivityResult(ActivityResultContracts.RequestPermission()) {
        bridge?.notifyState(notifyState())
    }

    /** 设置里那几个按钮：开关 / 允许后台运行 / 试一下 / 问状态 */
    fun notifyOp(op: String, on: Boolean) {
        when (op) {
            "set" -> {
                NoticeService.setEnabled(this, on)
                // 打开时顺手要通知权限（Android 13+ 要运行时申请；这一下是人点的，合规）
                if (on && !NoticeService.canNotify(this) && android.os.Build.VERSION.SDK_INT >= 33) {
                    askNotify.launch(android.Manifest.permission.POST_NOTIFICATIONS)
                }
            }
            "battery" -> {
                // 国产系统杀后台最狠的一层是电池优化；这个系统对话框只管这一层，
                // 「自启动 / 后台运行」那几个开关各家藏在不同地方，界面上用文字提醒
                try {
                    startActivity(
                        Intent(android.provider.Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS)
                            .setData(Uri.parse("package:$packageName")),
                    )
                } catch (_: ActivityNotFoundException) {
                    startActivity(Intent(android.provider.Settings.ACTION_IGNORE_BATTERY_OPTIMIZATION_SETTINGS))
                }
            }
            "test" -> {
                if (!NoticeService.canNotify(this) && android.os.Build.VERSION.SDK_INT >= 33) {
                    askNotify.launch(android.Manifest.permission.POST_NOTIFICATIONS)
                } else {
                    NoticeService.test(this)
                }
            }
        }
        bridge?.notifyState(notifyState())
    }

    fun notifyState(): org.json.JSONObject {
        val pm = getSystemService(android.os.PowerManager::class.java)
        return org.json.JSONObject()
            .put("t", "notify")
            .put("on", NoticeService.enabled(this))
            .put("perm", NoticeService.canNotify(this))
            .put("battery", pm.isIgnoringBatteryOptimizations(packageName))
    }

    companion object {
        private const val REQ_APPROVE = 41

        /** App 在不在前台。在的话后台服务不弹系统通知 —— 网页自己有提示卡和红点，再弹一条是噪音 */
        @Volatile var visible = false
    }
}
