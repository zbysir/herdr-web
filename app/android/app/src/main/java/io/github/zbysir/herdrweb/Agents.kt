package io.github.zbysir.herdrweb

import org.json.JSONObject

/**
 * agent 状态一览的「挑哪几行、怎么说」—— 常驻通知（NoticeService）和桌面小部件（AgentsWidget）
 * 共用这一份。两处各写一遍的话，迟早一个把「等你」排最前、另一个不排。
 *
 * 数据是 `/api/herdr/agents` 回的那一份（每个 agent 一行：pane / agent / status / workspace /
 * title / changed）。
 */
object Agents {
    class Summary(
        /** 「1 个等你 · 2 个在跑」/「40 个 agent 都停着」 */
        val title: String,
        /** 要逐行列的那些：等你 → 在跑；一个在动的都没有时是最近停下的 3 个 */
        val shown: List<JSONObject>,
        /** 「另有 37 个停着」这类尾注，没有就是 null */
        val foot: String?,
    )

    /**
     * 只逐行列「等你」和「在跑」的：一台机器上几十个 agent 是常态（实测 40 个），停着的全铺出来，
     * 要看的那几行就被埋了。停着的汇成一句尾注。
     */
    fun summarize(list: List<JSONObject>): Summary {
        val sorted = list.sortedBy { rank(it.optString("status")) }
        val wait = list.count { it.optString("status") == "blocked" }
        val run = list.count { it.optString("status") == "working" }
        val title = when {
            list.isEmpty() -> "没有 agent"
            wait == 0 && run == 0 -> "${list.size} 个 agent 都停着"
            else -> listOfNotNull(
                if (wait > 0) "$wait 个等你" else null,
                if (run > 0) "$run 个在跑" else null,
            ).joinToString(" · ")
        }
        val active = sorted.filter { rank(it.optString("status")) <= 1 }
        val shown = active.ifEmpty { list.sortedByDescending { it.optLong("changed") }.take(3) }
        val idle = list.size - active.size
        val foot = when {
            list.isEmpty() -> null
            active.isEmpty() -> "${list.size} 个都停着"
            idle > 0 -> "另有 $idle 个停着"
            else -> null
        }
        return Summary(title, shown, foot)
    }

    /** 「在跑」只画一个黄点（用户点名的）：它是常态，一屏都是两个字反而看不出谁在等你 */
    fun label(status: String) = when (status) {
        "blocked" -> "等你"
        "working" -> "●"
        "done" -> "跑完"
        "idle" -> "空闲"
        else -> status.ifEmpty { "?" }
    }

    /** 和网页面板一览同一套颜色：等你红、在跑黄、跑完绿、空闲灰（见 index.css 的 bad / warn / brand） */
    fun color(status: String) = when (status) {
        "blocked" -> 0xFFEF4444.toInt()
        "working" -> 0xFFEAB308.toInt()
        "done" -> 0xFF3ECF8E.toInt()
        else -> 0xFF9CA3AF.toInt()
    }

    /** 一行的正文：「项目 · 标题」（没有标题就用 agent 名） */
    fun what(a: JSONObject): String {
        val t = a.optString("title").ifEmpty { a.optString("agent") }
        return listOf(a.optString("workspace"), t).filter { it.isNotEmpty() }.joinToString(" · ")
    }

    /** 纯文字的一行（通知收起时的 contentText、无障碍读的那句） */
    fun line(a: JSONObject) = "${label(a.optString("status"))} ${what(a)}"

    private fun rank(status: String) = when (status) {
        "blocked" -> 0
        "working" -> 1
        "done" -> 2
        else -> 3
    }
}
