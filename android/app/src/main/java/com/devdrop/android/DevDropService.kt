package com.devdrop.android

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.ClipData
import android.content.ClipboardManager
import android.content.ContentValues
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Build
import android.os.Environment
import android.os.Handler
import android.os.IBinder
import android.os.Looper
import android.provider.MediaStore
import android.util.Base64
import android.util.Log
import android.widget.Toast
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.MediaType.Companion.toMediaTypeOrNull
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import org.json.JSONObject
import java.io.ByteArrayInputStream
import java.io.ByteArrayOutputStream
import java.io.File
import java.util.UUID
import java.util.concurrent.Executors
import java.util.concurrent.ScheduledFuture
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicLong
import java.util.zip.GZIPInputStream

/**
 * Foreground service holding the WebSocket to the DevDrop relay.
 *
 * Sending (phone -> PC): Android 10+ blocks clipboard READS from the
 * background, so the service never reads the clipboard itself. Instead it
 * keeps a persistent notification with a "Send clipboard" action. Tapping it
 * opens [SendClipboardActivity] (a foreground context, where reading is
 * allowed), which hands the text back to [pushText].
 *
 * Receiving (PC -> phone): clipboard WRITES are allowed from the background,
 * so text delivers are applied directly + a notification is shown. File/image
 * delivers are saved to Downloads (or Pictures for images) + notification.
 */
class DevDropService : Service() {

    companion object {
        private const val TAG = "DevDropService"

        const val ACTION_START = "com.devdrop.android.START"
        const val ACTION_PUSH_TEXT = "com.devdrop.android.PUSH_TEXT"
        const val ACTION_PUSH_FILE = "com.devdrop.android.PUSH_FILE"
        const val ACTION_FLUSH = "com.devdrop.android.FLUSH"
        const val ACTION_STOP = "com.devdrop.android.STOP"
        const val ACTION_GET_PRESENCE = "com.devdrop.android.GET_PRESENCE"
        const val ACTION_REVOKE = "com.devdrop.android.REVOKE"
        const val EXTRA_TEXT = "text"
        const val EXTRA_FILE_PATH = "file_path"
        const val EXTRA_FILE_NAME = "file_name"
        const val EXTRA_FILE_MIME = "file_mime"
        const val EXTRA_DEVICE_ID = "device_id"

        /** Broadcasts for UI refresh. */
        const val BC_STATUS = "com.devdrop.android.STATUS"
        const val BC_HISTORY = "com.devdrop.android.HISTORY"
        const val BC_PRESENCE = "com.devdrop.android.PRESENCE"
        const val EXTRA_CONNECTED = "connected"
        const val EXTRA_DETAIL = "detail"
        const val EXTRA_DEVICES = "devices"
        const val EXTRA_SELF_ID = "self_id"

        private const val NOTIF_STATUS = 1
        private const val NOTIF_RECEIVED_BASE = 100
        private const val CH_STATUS = "devdrop_status"
        private const val CH_EVENTS = "devdrop_events"

        const val INLINE_MAX = 1024 * 1024 // 1MB: files above this go via /api/blobs

        /** Single in-memory slot for a file picked while offline. Flushed on WS open. */
        @Volatile var pendingFile: PendingFile? = null

        data class PendingFile(val filename: String, val mime: String, val bytes: ByteArray)

        @Volatile private var running = false

        fun start(ctx: Context) {
            val i = Intent(ctx, DevDropService::class.java).setAction(ACTION_START)
            ctx.startForegroundService(i)
        }

        fun pushText(ctx: Context, text: String) {
            Store.queueOutgoing(ctx, text)
            flush(ctx)
        }

        /**
         * Queue a file for sending. Bytes are staged through a cache file to
         * avoid Binder transaction limits; the service reads + deletes it and
         * either sends immediately or stashes it in [pendingFile] when offline.
         */
        fun pushFile(ctx: Context, filename: String, mime: String, bytes: ByteArray) {
            try {
                val f = File(ctx.cacheDir, "send_${UUID.randomUUID()}.bin")
                f.writeBytes(bytes)
                val i = Intent(ctx, DevDropService::class.java).setAction(ACTION_PUSH_FILE)
                    .putExtra(EXTRA_FILE_PATH, f.absolutePath)
                    .putExtra(EXTRA_FILE_NAME, filename)
                    .putExtra(EXTRA_FILE_MIME, mime)
                try {
                    ctx.startService(i)
                } catch (e: Exception) {
                    pendingFile = PendingFile(filename, mime, bytes)
                    try {
                        f.delete()
                    } catch (ignored: Exception) { /* ignore */ }
                    try {
                        ctx.startForegroundService(i)
                    } catch (e2: Exception) {
                        // Stays in pendingFile; flushed on next connect.
                    }
                }
            } catch (e: Exception) {
                pendingFile = PendingFile(filename, mime, bytes)
            }
        }

        /** Ask the service to flush the outbox (safe to call from background). */
        fun flush(ctx: Context) {
            val i = Intent(ctx, DevDropService::class.java).setAction(ACTION_FLUSH)
            // Service is already foreground; plain startService is fine.
            // Fall back to FGS start if it was killed.
            try {
                ctx.startService(i)
            } catch (e: Exception) {
                try {
                    ctx.startForegroundService(i)
                } catch (e2: Exception) {
                    // Text stays queued; periodic flush picks it up.
                }
            }
        }

        fun requestPresence(ctx: Context) {
            val i = Intent(ctx, DevDropService::class.java).setAction(ACTION_GET_PRESENCE)
            try {
                ctx.startService(i)
            } catch (e: Exception) { /* ignore */ }
        }

        fun revokeDevice(ctx: Context, deviceId: String) {
            val i = Intent(ctx, DevDropService::class.java).setAction(ACTION_REVOKE)
                .putExtra(EXTRA_DEVICE_ID, deviceId)
            try {
                ctx.startService(i)
            } catch (e: Exception) { /* ignore */ }
        }

        fun stop(ctx: Context) {
            ctx.stopService(Intent(ctx, DevDropService::class.java))
        }
    }

    private val executor = Executors.newSingleThreadScheduledExecutor()
    private val http = OkHttpClient.Builder()
        .pingInterval(25, TimeUnit.SECONDS) // protocol-level keepalive (gorilla answers pings)
        .build()

    @Volatile private var ws: WebSocket? = null
    @Volatile private var connected = false
    @Volatile private var detail: String = "Starting…"
    @Volatile private var destroyed = false
    @Volatile private var lastPresenceJson: String = "[]"

    private val backoffMs = AtomicLong(1000)
    private var reconnectTask: ScheduledFuture<*>? = null
    private var flushTask: ScheduledFuture<*>? = null

    private lateinit var clipboard: ClipboardManager
    private var lastSentHash: String? = null
    private var lastAppliedHash: String? = null
    private var notifSeq = 0

    // ---- lifecycle ----

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onCreate() {
        super.onCreate()
        clipboard = getSystemService(CLIPBOARD_SERVICE) as ClipboardManager
        createChannels()
        startForeground(NOTIF_STATUS, statusNotification(detail, promptSend = false))
        clipboard.addPrimaryClipChangedListener(clipListener)
        // Safety net: flush anything queued by the reply receiver / tile
        // even if their startService call was ignored while we were down.
        flushTask = executor.scheduleWithFixedDelay(
            { if (!destroyed) flushOutbox() }, 30, 30, TimeUnit.SECONDS
        )
        running = true
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_PUSH_TEXT -> {
                intent.getStringExtra(EXTRA_TEXT)?.let { Store.queueOutgoing(this, it) }
                executor.execute { flushOutbox() }
            }
            ACTION_PUSH_FILE -> {
                val path = intent.getStringExtra(EXTRA_FILE_PATH)
                val name = intent.getStringExtra(EXTRA_FILE_NAME) ?: "file"
                val mime = intent.getStringExtra(EXTRA_FILE_MIME) ?: "application/octet-stream"
                executor.execute {
                    val bytes = try {
                        if (path.isNullOrEmpty()) null else File(path).readBytes()
                    } catch (e: Exception) { null }
                    try {
                        if (!path.isNullOrEmpty()) File(path).delete()
                    } catch (ignored: Exception) { /* ignore */ }
                    if (bytes == null) {
                        toast("Could not read file")
                        return@execute
                    }
                    handleIncomingFile(name, mime, bytes)
                }
            }
            ACTION_FLUSH -> {
                executor.execute { flushOutbox() }
            }
            ACTION_GET_PRESENCE -> {
                broadcastPresence()
            }
            ACTION_REVOKE -> {
                val target = intent.getStringExtra(EXTRA_DEVICE_ID) ?: return START_STICKY
                executor.execute {
                    try {
                        ws?.send(Proto.revoke(target))
                        toast("Disconnect requested")
                    } catch (e: Exception) {
                        toast("Not connected — cannot revoke now")
                    }
                }
            }
            ACTION_STOP -> {
                stopSelf()
                return START_NOT_STICKY
            }
            else -> { /* ACTION_START or restart */ }
        }
        if (!Store.isLoggedIn(this)) {
            updateDetail("Not logged in")
            stopSelf()
            return START_NOT_STICKY
        }
        ensureConnected()
        return START_STICKY
    }

    override fun onDestroy() {
        destroyed = true
        running = false
        try {
            clipboard.removePrimaryClipChangedListener(clipListener)
        } catch (e: Exception) { /* ignore */ }
        reconnectTask?.cancel(false)
        flushTask?.cancel(false)
        try { ws?.close(1000, "service destroyed") } catch (e: Exception) { /* ignore */ }
        ws = null
        executor.shutdownNow()
        try { http.dispatcher.executorService.shutdown() } catch (e: Exception) { /* ignore */ }
        super.onDestroy()
    }

    // ---- clipboard: send side (prompt only, never read here) ----

    private val clipListener = ClipboardManager.OnPrimaryClipChangedListener {
        // Our own receive-side write: ignore (hash guard also covers this).
        val text: String? = try {
            if (!clipboard.hasPrimaryClip()) null
            else clipboard.primaryClip
                ?.takeIf { it.itemCount > 0 }
                ?.getItemAt(0)
                ?.coerceToText(this)?.toString()
            // NOTE: on Android 10+ this returns null in background — that's
            // expected. We still refresh the prompt so the user can tap Send.
        } catch (e: Exception) { null }

        if (!text.isNullOrEmpty()) {
            val h = Proto.sha256Hex(text.toByteArray(Charsets.UTF_8))
            if (h == lastSentHash || h == lastAppliedHash) return@OnPrimaryClipChangedListener
        }
        refreshStatus(promptSend = true)
    }

    // ---- websocket ----

    private fun ensureConnected() {
        executor.execute {
            if (destroyed || connected || ws != null) return@execute
            connect()
        }
    }

    private fun connect() {
        if (destroyed) return
        val serverUrl = Store.getServerUrl(this)
        if (Store.getDeviceId(this) == null || Store.getToken(this) == null) return
        updateDetail("Connecting…")
        try {
            val req = Request.Builder().url(Proto.wsUrl(serverUrl)).build()
            ws = http.newWebSocket(req, listener)
        } catch (e: Exception) {
            Log.w(TAG, "connect failed: ${e.message}")
            scheduleReconnect()
        }
    }

    private val listener = object : WebSocketListener() {
        override fun onOpen(webSocket: WebSocket, response: Response) {
            backoffMs.set(1000)
            connected = true
            val deviceId = Store.getDeviceId(this@DevDropService) ?: return
            val token = Store.getToken(this@DevDropService) ?: return
            webSocket.send(Proto.hello(deviceId, token, Store.getDeviceName(this@DevDropService), "android"))
            updateDetail("Connected")
            flushOutbox()
            flushPendingFile()
        }

        override fun onMessage(webSocket: WebSocket, text: String) {
            try {
                val msg = JSONObject(text)
                when (msg.optString("type")) {
                    "deliver" -> handleDeliver(msg)
                    "presence" -> handlePresence(msg)
                    "error" -> Log.w(TAG, "server error: ${msg.optString("message")}")
                    else -> { /* ping/unknown: ignore */ }
                }
            } catch (e: Exception) {
                Log.w(TAG, "bad message: ${e.message}")
            }
        }

        override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
            onDropped()
        }

        override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
            Log.w(TAG, "ws failure: ${t.message}")
            onDropped()
        }
    }

    private fun onDropped() {
        ws = null
        if (connected) {
            connected = false
            updateDetail("Reconnecting…")
        }
        scheduleReconnect()
    }

    private fun scheduleReconnect() {
        if (destroyed) return
        reconnectTask?.cancel(false)
        val delay = backoffMs.get().coerceAtMost(30_000)
        backoffMs.set((delay * 2).coerceAtMost(30_000))
        reconnectTask = executor.schedule({
            ws = null
            connect()
        }, delay, TimeUnit.MILLISECONDS)
    }

    // ---- receive side ----

    private fun handleDeliver(msg: JSONObject) {
        val kind = msg.optString("kind", "text").ifEmpty { "text" }
        if (kind == "text") {
            handleTextDeliver(msg)
            return
        }
        if (kind != "file" && kind != "image") return // unknown kind: ignore
        handleFileDeliver(msg, kind)
    }

    private fun handleTextDeliver(msg: JSONObject) {
        val itemId = msg.optString("item_id", UUID.randomUUID().toString())
        val origin = msg.optString("origin_device_name", "PC").ifEmpty { "PC" }
        val live = msg.optBoolean("live", true)
        val text = try {
            Proto.b64decodeUtf8(msg.optString("payload", ""))
        } catch (e: Exception) { return }
        if (text.isEmpty()) return

        // Ack everything so the server clears pending state.
        try { ws?.send(Proto.ack(itemId)) } catch (e: Exception) { /* ignore */ }

        val hash = msg.optString("sha256", "").ifEmpty {
            Proto.sha256Hex(text.toByteArray(Charsets.UTF_8))
        }
        if (hash == lastSentHash) return // our own echo

        Store.addHistory(
            this,
            Store.HistoryItem(itemId, System.currentTimeMillis(), origin, text, outgoing = false)
        )
        sendBroadcast(Intent(BC_HISTORY).setPackage(packageName))

        if (!live) return // backlog: history only, don't hijack clipboard
        lastAppliedHash = hash
        try {
            clipboard.setPrimaryClip(ClipData.newPlainText("CrossShare", text))
        } catch (e: Exception) {
            Log.w(TAG, "clipboard write failed: ${e.message}")
        }
        notifyReceived(origin, text)
    }

    private fun handleFileDeliver(msg: JSONObject, kind: String) {
        val itemId = msg.optString("item_id", UUID.randomUUID().toString())
        val origin = msg.optString("origin_device_name", "PC").ifEmpty { "PC" }
        val live = msg.optBoolean("live", true)
        val filename = msg.optString("filename", "file").ifEmpty { "file" }
        val mime = msg.optString("mime", "application/octet-stream").ifEmpty { "application/octet-stream" }
        val size = msg.optLong("size", 0L)
        val sha = msg.optString("sha256", "")
        val payloadB64 = msg.optString("payload", "")
        val blobId = msg.optString("blob_id", "")

        // Ack everything so the server clears pending state.
        try { ws?.send(Proto.ack(itemId)) } catch (e: Exception) { /* ignore */ }

        // Echo guard (same as text): skip our own send.
        if (sha.isNotEmpty() && sha == lastSentHash) return

        if (!live) {
            // Backlog: history only, no file write, no notification.
            Store.addHistory(
                this,
                Store.HistoryItem(itemId, System.currentTimeMillis(), origin, "", outgoing = false, kind = kind, filename = filename, size = size, filepath = "")
            )
            sendBroadcast(Intent(BC_HISTORY).setPackage(packageName))
            return
        }

        executor.execute {
            try {
                val encoding = msg.optString("encoding", "").trim().lowercase()
                var bytes: ByteArray? = when {
                    payloadB64.isNotEmpty() -> try {
                        Base64.decode(payloadB64, Base64.DEFAULT)
                    } catch (e: Exception) { null }
                    blobId.isNotEmpty() -> downloadBlob(blobId)
                    else -> null
                }
                if (bytes == null || bytes.isEmpty()) {
                    Log.w(TAG, "file deliver had no bytes: $itemId")
                    return@execute
                }
                // Gzip-compressed payloads (inline or blob) must be gunzipped before use.
                if (encoding == "gzip") {
                    bytes = try {
                        gunzip(bytes)
                    } catch (e: Exception) {
                        Log.w(TAG, "gunzip failed for $itemId: ${e.message}")
                        return@execute
                    }
                    if (bytes == null || bytes.isEmpty()) {
                        Log.w(TAG, "gunzip produced no bytes: $itemId")
                        return@execute
                    }
                }
                val hash = sha.ifEmpty { Proto.sha256Hex(bytes) }
                if (hash == lastSentHash) return@execute // our own echo
                val uriStr = saveToMediaStore(filename, mime, bytes) ?: ""
                Store.addHistory(
                    this,
                    Store.HistoryItem(itemId, System.currentTimeMillis(), origin, "", outgoing = false, kind = kind, filename = filename, size = bytes.size.toLong(), filepath = uriStr)
                )
                sendBroadcast(Intent(BC_HISTORY).setPackage(packageName))
                notifyFileReceived(origin, filename, uriStr, mime)
            } catch (e: Exception) {
                Log.w(TAG, "file save failed: ${e.message}")
            }
        }
    }

    private fun handlePresence(msg: JSONObject) {
        try {
            val devices = msg.optJSONArray("devices") ?: return
            lastPresenceJson = devices.toString()
            var online = 0
            for (i in 0 until devices.length()) {
                if (devices.getJSONObject(i).optBoolean("online")) online++
            }
            updateDetail("Connected ($online online)")
            broadcastPresence()
        } catch (e: Exception) { /* ignore */ }
    }

    private fun broadcastPresence() {
        try {
            sendBroadcast(
                Intent(BC_PRESENCE).setPackage(packageName)
                    .putExtra(EXTRA_DEVICES, lastPresenceJson)
                    .putExtra(EXTRA_SELF_ID, Store.getDeviceId(this) ?: "")
            )
        } catch (e: Exception) { /* ignore */ }
    }

    // ---- send side (durable outbox + file send) ----

    /** Send everything queued. Runs on the executor or OkHttp threads. */
    private fun flushOutbox() {
        if (destroyed) return
        val w = ws
        if (!connected || w == null) {
            ensureConnected()
            return
        }
        val queued = Store.takeOutbox(this)
        if (queued.isEmpty()) return
        var sentAny = false
        for (text in queued) {
            try {
                w.send(Proto.pushText(text, Store.getTtlS(this)))
                lastSentHash = Proto.sha256Hex(text.toByteArray(Charsets.UTF_8))
                Store.addHistory(
                    this,
                    Store.HistoryItem(
                        UUID.randomUUID().toString(),
                        System.currentTimeMillis(), "This phone", text, outgoing = true
                    )
                )
                sentAny = true
            } catch (e: Exception) {
                Store.queueOutgoing(this, text) // put back, retry later
                break
            }
        }
        if (sentAny) {
            sendBroadcast(Intent(BC_HISTORY).setPackage(packageName))
            refreshStatus(promptSend = false)
        }
    }

    private fun flushPendingFile() {
        val p = pendingFile ?: return
        pendingFile = null
        handleIncomingFile(p.filename, p.mime, p.bytes)
    }

    /** Runs on the executor. Sends immediately if connected, else stashes. */
    private fun handleIncomingFile(filename: String, mime: String, bytes: ByteArray) {
        val w = ws
        if (destroyed || !connected || w == null) {
            pendingFile = PendingFile(filename, mime, bytes)
            toast("Not connected — will send on reconnect")
            ensureConnected()
            return
        }
        doFileSend(w, filename, mime, bytes)
    }

    private fun doFileSend(w: WebSocket, filename: String, mime: String, bytes: ByteArray) {
        try {
            val ttl = Store.getTtlS(this)
            val json = if (bytes.size <= INLINE_MAX) {
                Proto.pushFileInline(filename, mime, bytes, ttl)
            } else {
                val blobId = uploadBlob(bytes, mime)
                if (blobId == null) {
                    pendingFile = PendingFile(filename, mime, bytes)
                    toast("Blob upload failed — will retry on reconnect")
                    return
                }
                Proto.pushFileBlob(filename, mime, bytes, blobId, ttl)
            }
            w.send(json)
            lastSentHash = Proto.sha256Hex(bytes)
            val kind = Proto.kindForMime(mime)
            Store.addHistory(
                this,
                Store.HistoryItem(UUID.randomUUID().toString(), System.currentTimeMillis(), "This phone", "", outgoing = true, kind = kind, filename = filename, size = bytes.size.toLong(), filepath = "")
            )
            sendBroadcast(Intent(BC_HISTORY).setPackage(packageName))
            refreshStatus(promptSend = false)
            toast("File sent: $filename")
        } catch (e: Exception) {
            Log.w(TAG, "file send failed: ${e.message}")
            pendingFile = PendingFile(filename, mime, bytes)
            toast("Send failed — will retry on reconnect")
        }
    }

    private fun uploadBlob(bytes: ByteArray, mime: String): String? {
        return try {
            val server = Store.getServerUrl(this).trimEnd('/')
            val token = Store.getToken(this) ?: return null
            val mt = mime.ifBlank { "application/octet-stream" }.toMediaTypeOrNull()
                ?: "application/octet-stream".toMediaType()
            val req = Request.Builder()
                .url("$server/api/blobs")
                .header("Authorization", "Bearer $token")
                .post(bytes.toRequestBody(mt))
                .build()
            http.newCall(req).execute().use { resp ->
                if (!resp.isSuccessful) return null
                JSONObject(resp.body?.string() ?: "").optString("blob_id", "").ifEmpty { null }
            }
        } catch (e: Exception) {
            Log.w(TAG, "blob upload failed: ${e.message}")
            null
        }
    }

    private fun downloadBlob(blobId: String): ByteArray? {
        return try {
            val server = Store.getServerUrl(this).trimEnd('/')
            val token = Store.getToken(this) ?: return null
            val req = Request.Builder()
                .url("$server/api/blobs/$blobId")
                .header("Authorization", "Bearer $token")
                .get()
                .build()
            http.newCall(req).execute().use { resp ->
                if (!resp.isSuccessful) null else resp.body?.bytes()
            }
        } catch (e: Exception) {
            Log.w(TAG, "blob download failed: ${e.message}")
            null
        }
    }

    /** Gunzip raw bytes (encoding == "gzip" on push/deliver). Throws on bad data. */
    private fun gunzip(data: ByteArray): ByteArray {
        GZIPInputStream(ByteArrayInputStream(data)).use { gis ->
            val out = ByteArrayOutputStream(data.size.coerceAtLeast(1024))
            val buf = ByteArray(32 * 1024)
            while (true) {
                val n = gis.read(buf)
                if (n < 0) break
                out.write(buf, 0, n)
            }
            return out.toByteArray()
        }
    }

    /**
     * Save bytes into MediaStore Downloads (Download/CrossShare/) on API>=29,
     * images into Pictures/CrossShare/; legacy public dir below API 29.
     * Returns the content/file URI string, or null on failure.
     */
    private fun saveToMediaStore(filename: String, mime: String, bytes: ByteArray): String? {
        val safeName = filename.ifBlank { "file" }.replace('/', '_')
        val effMime = mime.ifBlank { "application/octet-stream" }
        val isImage = effMime.startsWith("image/")
        return try {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                val collection = if (isImage) {
                    MediaStore.Images.Media.getContentUri(MediaStore.VOLUME_EXTERNAL_PRIMARY)
                } else {
                    MediaStore.Downloads.getContentUri(MediaStore.VOLUME_EXTERNAL_PRIMARY)
                }
                val values = ContentValues().apply {
                    put(MediaStore.MediaColumns.DISPLAY_NAME, safeName)
                    put(MediaStore.MediaColumns.MIME_TYPE, effMime)
                    put(MediaStore.MediaColumns.SIZE, bytes.size)
                    if (isImage) {
                        put(MediaStore.Images.Media.RELATIVE_PATH, "Pictures/CrossShare/")
                    } else {
                        put(MediaStore.Downloads.RELATIVE_PATH, "Download/CrossShare/")
                    }
                }
                val resolver = contentResolver
                val uri: Uri = resolver.insert(collection, values) ?: return null
                resolver.openOutputStream(uri)?.use { it.write(bytes) } ?: return null
                uri.toString()
            } else {
                @Suppress("DEPRECATION")
                val base = Environment.getExternalStoragePublicDirectory(
                    if (isImage) Environment.DIRECTORY_PICTURES else Environment.DIRECTORY_DOWNLOADS
                )
                val dir = File(base, "CrossShare")
                if (!dir.exists()) dir.mkdirs()
                var target = File(dir, safeName)
                var n = 1
                while (target.exists()) {
                    val stem = safeName.substringBeforeLast('.', safeName)
                    val ext = safeName.substringAfterLast('.', "")
                    target = File(dir, if (ext.isEmpty() || ext == safeName) "$stem ($n)" else "$stem ($n).$ext")
                    n++
                }
                target.writeBytes(bytes)
                Uri.fromFile(target).toString()
            }
        } catch (e: Exception) {
            Log.w(TAG, "media store save failed: ${e.message}")
            null
        }
    }

    // ---- notifications ----

    private fun createChannels() {
        val nm = getSystemService(NotificationManager::class.java)
        nm.createNotificationChannel(
            NotificationChannel(CH_STATUS, "CrossShare status", NotificationManager.IMPORTANCE_LOW)
        )
        nm.createNotificationChannel(
            NotificationChannel(CH_EVENTS, "CrossShare events", NotificationManager.IMPORTANCE_HIGH)
        )
    }

    private fun statusNotification(detailText: String, promptSend: Boolean): Notification {
        val openApp = PendingIntent.getActivity(
            this, 0, Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )
        val sendIntent = Intent(this, SendClipboardActivity::class.java)
            .addFlags(
                Intent.FLAG_ACTIVITY_NEW_TASK or
                    Intent.FLAG_ACTIVITY_NO_ANIMATION or
                    Intent.FLAG_ACTIVITY_EXCLUDE_FROM_RECENTS
            )
        val send = PendingIntent.getActivity(
            this, 1, sendIntent,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )
        // Inline reply: type a message to the PC right from the shade.
        // RemoteInput requires a MUTABLE PendingIntent so the system can
        // attach the typed text.
        val replyIntent = Intent(this, SendReplyReceiver::class.java)
        val reply = PendingIntent.getBroadcast(
            this, 3, replyIntent,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_MUTABLE
        )
        val remoteInput = android.app.RemoteInput.Builder(SendReplyReceiver.KEY_REPLY)
            .setLabel("Message to PC")
            .build()
        val title = if (promptSend) "Copy detected — send to PC?" else "CrossShare"
        return Notification.Builder(this, CH_STATUS)
            .setSmallIcon(android.R.drawable.ic_dialog_info)
            .setContentTitle(title)
            .setContentText(detailText)
            .setContentIntent(openApp)
            .setOngoing(true)
            .addAction(
                Notification.Action.Builder(
                    null, "Send clipboard",
                    send
                ).build()
            )
            .addAction(
                Notification.Action.Builder(
                    null, "Type message",
                    reply
                ).addRemoteInput(remoteInput).build()
            )
            .build()
    }

    private fun refreshStatus(promptSend: Boolean) {
        try {
            val nm = getSystemService(NotificationManager::class.java)
            nm.notify(NOTIF_STATUS, statusNotification(detail, promptSend))
        } catch (e: Exception) { /* ignore */ }
    }

    private fun updateDetail(d: String) {
        detail = d
        refreshStatus(promptSend = false)
        sendBroadcast(
            Intent(BC_STATUS).setPackage(packageName)
                .putExtra(EXTRA_CONNECTED, connected)
                .putExtra(EXTRA_DETAIL, d)
        )
    }

    private fun notifyReceived(origin: String, text: String) {
        val openApp = PendingIntent.getActivity(
            this, 2, Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )
        val preview = if (text.length > 120) text.take(120) + "…" else text
        val n = Notification.Builder(this, CH_EVENTS)
            .setSmallIcon(android.R.drawable.ic_dialog_info)
            .setContentTitle("From $origin — copied to clipboard")
            .setContentText(preview)
            .setStyle(Notification.BigTextStyle().bigText(text))
            .setContentIntent(openApp)
            .setAutoCancel(true)
            .build()
        try {
            getSystemService(NotificationManager::class.java)
                .notify(NOTIF_RECEIVED_BASE + (notifSeq++ % 20), n)
        } catch (e: Exception) { /* ignore */ }
    }

    private fun notifyFileReceived(origin: String, filename: String, uriStr: String, mime: String) {
        try {
            val builder = Notification.Builder(this, CH_EVENTS)
                .setSmallIcon(android.R.drawable.ic_dialog_info)
                .setContentTitle("File from $origin: $filename")
                .setContentText("Tap to open")
                .setAutoCancel(true)
            if (uriStr.isNotEmpty()) {
                try {
                    val uri = Uri.parse(uriStr)
                    val view = Intent(Intent.ACTION_VIEW).apply {
                        setDataAndType(uri, mime.ifBlank { "*/*" })
                        addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
                    }
                    val pi = PendingIntent.getActivity(
                        this, 200 + (notifSeq % 50), view,
                        PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
                    )
                    builder.setContentIntent(pi)
                } catch (e: Exception) {
                    Log.w(TAG, "file view intent failed: ${e.message}")
                }
            }
            getSystemService(NotificationManager::class.java)
                .notify(NOTIF_RECEIVED_BASE + (notifSeq++ % 20), builder.build())
        } catch (e: Exception) { /* ignore */ }
    }

    private fun toast(msg: String) {
        Handler(Looper.getMainLooper()).post {
            try {
                Toast.makeText(this, msg, Toast.LENGTH_SHORT).show()
            } catch (e: Exception) { /* ignore */ }
        }
    }
}
