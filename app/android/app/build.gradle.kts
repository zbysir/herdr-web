import java.util.Properties

plugins {
    id("com.android.application")
}

// 正式包的签名：钥匙不进仓库。local.properties（已 gitignore）里给两行：
//   release.storeFile=/Users/you/.android-release/herdr-web.jks
//   release.passFile=/Users/you/.android-release/herdr-web.pass   （库和私钥同一个密码）
// 没配就不签 —— assembleRelease 出一个 unsigned 包，装不上，但不会悄悄拿 debug 钥匙签一个
// 「看着能用、以后却升级不上去」的包。**这把钥匙丢了，已经装上的人就只能卸了重装**（签名对不上
// 不让覆盖安装），所以它和密码文件一定要备份。
val local = Properties().apply {
    rootProject.file("local.properties").takeIf { it.exists() }?.inputStream()?.use { load(it) }
}
//
// CI（release.yml）没有 local.properties，走环境变量：`HERDR_APP_KEYSTORE`（jks 的路径，workflow
// 从 secret 解出来的临时文件）+ `HERDR_APP_KEY_PASS`。本机那份优先。
val releaseStore = (local.getProperty("release.storeFile") ?: System.getenv("HERDR_APP_KEYSTORE"))
    ?.takeIf { it.isNotBlank() }?.let(::file)
val releasePass = local.getProperty("release.passFile")?.let { file(it).readText().trim() }
    ?: System.getenv("HERDR_APP_KEY_PASS")?.takeIf { it.isNotBlank() }

// 版本号跟着发版的 tag 走（`-PappVersion=0.10.0`，release.yml 传），不传就是本机开发那个占位。
// versionCode 必须单调增（系统拿它判「这是不是升级」，小了不让覆盖装），按 主.次.修 各占三位算：
// 0.10.0 → 10000、1.2.3 → 1002003。预发布后缀（-rc1）丢掉 —— 同一个号的正式版会装不上去，
// 不过发版只打正式 tag
val appVersion = (findProperty("appVersion") as String?)?.removePrefix("v")?.substringBefore('-') ?: "0.1.0"
val appCode = appVersion.split('.').map { it.toIntOrNull() ?: 0 }
    .let { (it + listOf(0, 0, 0)).take(3) }
    .let { (ma, mi, pa) -> ma * 1_000_000 + mi * 1_000 + pa }
    .coerceAtLeast(1)

android {
    namespace = "io.github.zbysir.herdrweb"
    compileSdk = 36

    defaultConfig {
        // 上架后改不了（见 docs/dev/NATIVE-APP.md）
        applicationId = "io.github.zbysir.herdrweb"
        minSdk = 28
        targetSdk = 36
        versionCode = appCode
        versionName = appVersion
    }

    signingConfigs {
        if (releaseStore != null && releasePass != null) {
            create("release") {
                storeFile = releaseStore
                storePassword = releasePass
                keyAlias = "herdr-web"
                keyPassword = releasePass
            }
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            signingConfig = signingConfigs.findByName("release")
        }
    }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
}

dependencies {
    implementation("androidx.activity:activity-ktx:1.10.1")
    implementation("androidx.core:core-ktx:1.16.0")
    // addWebMessageListener：桥只对服务器那个 origin 开（见 Bridge.kt）
    implementation("androidx.webkit:webkit:1.14.0")
    // passkey 批准页用 Custom Tabs 打开：真浏览器（passkey 能用、带着浏览器自己的登录态），
    // 而且能只占下半屏，上面还看得见要输的那个码
    implementation("androidx.browser:browser:1.8.0")
}
