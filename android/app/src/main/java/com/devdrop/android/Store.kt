package com.devdrop.android

import android.content.Context
import android.content.SharedPreferences
import org.json.JSONArray
import org.json.JSONObject

/** Simple persistent store: credentials + received/sent history. */
object Store {
    private const val PREFS = "devdrop"
    const val HISTORY_MAX = 50

    /** Prefill for quick testing. The tunnel URL changes on every cloudflared restart. */
    const val DEFAULT_SERVER_URL = "https://hoping-sustainability-played-joan.trycloudflare.com"

    data class HistoryItem(
        val id: String,
        val timeMs: Long,
        val origin: String,
        val text: String,
        val outgoing: Boolean,
        val kind: String = "text",
        val filename: String = "",
        val size: Long = 0L,
        val filepath: String = ""
    )

    private fun prefs(ctx: Context): SharedPreferences =
        ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE)

    fun getServerUrl(ctx: Context): String {
        val s = prefs(ctx).getString("server_url", null)
        return if (s.isNullOrBlank()) DEFAULT_SERVER_URL else s
    }

    fun getDeviceId(ctx: Context) = prefs(ctx).getString("device_id", null)
    fun getToken(ctx: Context) = prefs(ctx).getString("token", null)
    fun getDeviceName(ctx: Context) = prefs(ctx).getString("device_name", "Android") ?: "Android"

    /** Retention: how long items wait on the server. Clamped 60s..30d, default 30m. */
    fun getTtlS(ctx: Context): Int {
        val v = prefs(ctx).getInt("ttl_s", 1800)
        return v.coerceIn(60, 2592000)
    }

    fun setTtlS(ctx: Context, v: Int) {
        prefs(ctx).edit().putInt("ttl_s", v.coerceIn(60, 2592000)).apply()
    }

    fun setServerUrl(ctx: Context, url: String) {
        val clean = url.trim().trimEnd('/')
        if (clean.isEmpty()) return
        prefs(ctx).edit().putString("server_url", clean).apply()
    }

    fun isLoggedIn(ctx: Context) =
        !getDeviceId(ctx).isNullOrEmpty() && !getToken(ctx).isNullOrEmpty()

    fun saveLogin(ctx: Context, serverUrl: String, deviceId: String, token: String, deviceName: String) {
        prefs(ctx).edit()
            .putString("server_url", serverUrl.trim().trimEnd('/'))
            .putString("device_id", deviceId)
            .putString("token", token)
            .putString("device_name", deviceName)
            .apply()
    }

    fun clearCredentials(ctx: Context) {
        prefs(ctx).edit()
            .remove("device_id").remove("token").remove("device_name")
            .apply()
    }

    /** Durable outbox: texts waiting to be pushed. Survives service restarts. */
    @Synchronized
    fun queueOutgoing(ctx: Context, text: String) {
        val arr = try {
            JSONArray(prefs(ctx).getString("outbox", "[]") ?: "[]")
        } catch (e: Exception) { JSONArray() }
        arr.put(text)
        while (arr.length() > 20) arr.remove(0)
        prefs(ctx).edit().putString("outbox", arr.toString()).apply()
    }

    /** Atomically take-and-clear the outbox. */
    @Synchronized
    fun takeOutbox(ctx: Context): List<String> {
        val raw = prefs(ctx).getString("outbox", "[]") ?: "[]"
        prefs(ctx).edit().remove("outbox").apply()
        val out = ArrayList<String>()
        try {
            val arr = JSONArray(raw)
            for (i in 0 until arr.length()) {
                arr.optString(i)?.takeIf { it.isNotEmpty() }?.let { out.add(it) }
            }
        } catch (e: Exception) { /* ignore */ }
        return out
    }

    fun addHistory(ctx: Context, item: HistoryItem) {        val arr = try {
            JSONArray(prefs(ctx).getString("history", "[]") ?: "[]")
        } catch (e: Exception) { JSONArray() }
        arr.put(
            JSONObject()
                .put("id", item.id)
                .put("time", item.timeMs)
                .put("origin", item.origin)
                .put("text", item.text)
                .put("out", item.outgoing)
                .put("kind", item.kind)
                .put("filename", item.filename)
                .put("size", item.size)
                .put("filepath", item.filepath)
        )
        while (arr.length() > HISTORY_MAX) arr.remove(0)
        prefs(ctx).edit().putString("history", arr.toString()).apply()
    }

    fun getHistory(ctx: Context): List<HistoryItem> {        val out = ArrayList<HistoryItem>()
        try {
            val arr = JSONArray(prefs(ctx).getString("history", "[]") ?: "[]")
            for (i in 0 until arr.length()) {
                val o = arr.getJSONObject(i)
                out.add(
                    HistoryItem(
                        id = o.optString("id"),
                        timeMs = o.optLong("time"),
                        origin = o.optString("origin"),
                        text = o.optString("text", ""),
                        outgoing = o.optBoolean("out"),
                        kind = o.optString("kind", "text").ifEmpty { "text" },
                        filename = o.optString("filename", ""),
                        size = o.optLong("size", 0L),
                        filepath = o.optString("filepath", o.optString("uri", ""))
                    )
                )
            }
        } catch (e: Exception) { /* corrupted -> empty */ }
        return out.reversed() // newest first
    }
}
