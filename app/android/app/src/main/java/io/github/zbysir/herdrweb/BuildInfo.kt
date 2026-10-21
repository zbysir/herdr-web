package io.github.zbysir.herdrweb

import android.content.Context
import android.content.pm.ApplicationInfo

object BuildInfo {
    fun version(ctx: Context): String =
        ctx.packageManager.getPackageInfo(ctx.packageName, 0).versionName ?: "?"

    fun debuggable(ctx: Context): Boolean =
        ctx.applicationInfo.flags and ApplicationInfo.FLAG_DEBUGGABLE != 0
}
