plugins {
    id("com.android.application")
}

android {
    namespace = "io.github.zbysir.herdrweb"
    compileSdk = 36

    defaultConfig {
        // 上架后改不了（见 docs/dev/NATIVE-APP.md）
        applicationId = "io.github.zbysir.herdrweb"
        minSdk = 28
        targetSdk = 36
        versionCode = 1
        versionName = "0.1.0"
    }

    buildTypes {
        release {
            isMinifyEnabled = false
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
