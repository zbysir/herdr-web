package io.github.zbysir.herdrweb

import android.app.PendingIntent
import android.appwidget.AppWidgetManager
import android.appwidget.AppWidgetProvider
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.view.View
import android.widget.RemoteViews
import org.json.JSONArray
import org.json.JSONObject
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

/**
 * 桌面小部件：agent 状态一览。和常驻通知是**同一份数据**（NoticeService 长轮询
 * `/api/herdr/agents` 拿到的，状态一变就 [push] 过来）、同一套挑法（Agents.summarize）。
 *
 * 小部件自己不去拉：系统给小部件的定时刷新最短半小时，靠它一览永远是旧的；再起一条连接
 * 也只是重复后台服务已经在做的事。代价是后台服务没在跑（被系统杀了、通知关了）时一览不动 ——
 * 所以右上角写着「几点几分」，旧了一眼看得出来，不装作是实时的。
 */
class AgentsWidget : AppWidgetProvider() {
    override fun onUpdate(ctx: Context, mgr: AppWidgetManager, ids: IntArray) {
        render(ctx, mgr, ids)
        NoticeService.start(ctx) // 放到桌面上那一下顺手把后台服务拉起来（不让起也没事，下次开 App 会起）
    }

    companion object {
        private const val PREFS = "widget"
        /** 布局里写死的六行：(行, 状态字, 正文)。为什么不 addView 见 widget_agents.xml 的注释 */
        private val ROWS = listOf(
            intArrayOf(R.id.r1, R.id.r1_st, R.id.r1_what),
            intArrayOf(R.id.r2, R.id.r2_st, R.id.r2_what),
            intArrayOf(R.id.r3, R.id.r3_st, R.id.r3_what),
            intArrayOf(R.id.r4, R.id.r4_st, R.id.r4_what),
            intArrayOf(R.id.r5, R.id.r5_st, R.id.r5_what),
            intArrayOf(R.id.r6, R.id.r6_st, R.id.r6_what),
        )
        private val MAX_ROWS = ROWS.size

        /** 后台服务拿到新一览时调：存一份（重启 / 新放一个小部件时先画它），然后刷新所有小部件 */
        fun push(ctx: Context, list: List<JSONObject>) {
            ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit()
                .putString("list", JSONArray(list).toString())
                .putLong("at", System.currentTimeMillis())
                .apply()
            val mgr = AppWidgetManager.getInstance(ctx)
            val ids = mgr.getAppWidgetIds(ComponentName(ctx, AgentsWidget::class.java))
            if (ids.isNotEmpty()) render(ctx, mgr, ids)
        }

        private fun render(ctx: Context, mgr: AppWidgetManager, ids: IntArray) {
            val prefs = ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
            val raw = prefs.getString("list", null)
            val at = prefs.getLong("at", 0)
            val v = RemoteViews(ctx.packageName, R.layout.widget_agents)
            if (raw == null) {
                ROWS.forEach { v.setViewVisibility(it[0], View.GONE) }
                v.setTextViewText(R.id.title, "herdr")
                v.setTextViewText(R.id.foot, "打开一次 App 登录，之后这里显示 agent 状态")
                v.setViewVisibility(R.id.foot, View.VISIBLE)
            } else {
                val arr = JSONArray(raw)
                val sum = Agents.summarize((0 until arr.length()).map { arr.getJSONObject(it) })
                v.setTextViewText(R.id.title, sum.title)
                // 每一行都显式设一遍（用不上的设成 gone）：别指望桌面记得上一次的状态
                for ((i, row) in ROWS.withIndex()) {
                    val a = sum.shown.getOrNull(i)
                    if (a == null) {
                        v.setViewVisibility(row[0], View.GONE)
                        continue
                    }
                    val st = a.optString("status")
                    v.setViewVisibility(row[0], View.VISIBLE)
                    v.setTextViewText(row[1], Agents.label(st))
                    v.setTextColor(row[1], Agents.color(st))
                    v.setTextViewText(row[2], Agents.what(a))
                }
                val foot = listOfNotNull(
                    if (sum.shown.size > MAX_ROWS) "还有 ${sum.shown.size - MAX_ROWS} 个在动" else null,
                    sum.foot,
                ).joinToString("，")
                v.setTextViewText(R.id.foot, foot)
                v.setViewVisibility(R.id.foot, if (foot.isEmpty()) View.GONE else View.VISIBLE)
            }
            v.setTextViewText(
                R.id.`when`,
                if (at > 0) SimpleDateFormat("HH:mm", Locale.getDefault()).format(Date(at)) else "",
            )
            val open = PendingIntent.getActivity(
                ctx, 0,
                Intent(ctx, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
                PendingIntent.FLAG_IMMUTABLE,
            )
            v.setOnClickPendingIntent(R.id.root, open)
            mgr.updateAppWidget(ids, v)
        }
    }
}
