package com.devdrop.android

import android.util.Base64
import org.json.JSONObject
import java.security.MessageDigest
import java.time.Instant
import java.util.UUID

/** Protocol helpers mirroring the Go agent (agent/types.go, agent/clipboard.go). */
object Proto {
    fun sha256Hex(data: ByteArray): String {
        val md = MessageDigest.getInstance("SHA-256")
        return md.digest(data).joinToString("") { "%02x".format(it) }
    }

    fun b64encodeUtf8(text: String): String =
        Base64.encodeToString(text.toByteArray(Charsets.UTF_8), Base64.NO_WRAP)

    fun b64decodeUtf8(b64: String): String =
        String(Base64.decode(b64, Base64.DEFAULT), Charsets.UTF_8)

    fun nowRfc3339(): String = Instant.now().toString()

    /** https://host -> wss://host/ws, http://host -> ws://host/ws */
    fun wsUrl(serverUrl: String): String {
        var u = serverUrl.trim().trimEnd('/')
        u = when {
            u.startsWith("https://") -> "wss://" + u.removePrefix("https://")
            u.startsWith("http://") -> "ws://" + u.removePrefix("http://")
            else -> u
        }
        return "$u/ws"
    }

    fun hello(deviceId: String, token: String, name: String, os: String): String =
        JSONObject()
            .put("type", "hello")
            .put("device_id", deviceId)
            .put("token", token)
            .put("name", name)
            .put("os", os)
            .toString()

    fun pushText(text: String, ttlS: Int = 1800): String {
        val bytes = text.toByteArray(Charsets.UTF_8)
        return JSONObject()
            .put("type", "push")
            .put("item_id", UUID.randomUUID().toString())
            .put("kind", "text")
            .put("mime", "text/plain")
            .put("size", bytes.size)
            .put("sha256", sha256Hex(bytes))
            .put("ttl_s", ttlS)
            .put("targets", "all")
            .put("payload", b64encodeUtf8(text))
            .put("created_at", nowRfc3339())
            .toString()
    }

    fun ack(itemId: String): String =
        JSONObject()
            .put("type", "ack")
            .put("item_id", itemId)
            .put("state", "received")
            .toString()

    fun revoke(deviceId: String): String =
        JSONObject()
            .put("type", "revoke")
            .put("device_id", deviceId)
            .toString()

    fun kindForMime(mime: String): String =
        if (mime.startsWith("image/")) "image" else "file"

    fun b64encodeBytes(bytes: ByteArray): String =
        Base64.encodeToString(bytes, Base64.NO_WRAP)

    /** Files <=1MB go inline as base64. */
    fun pushFileInline(filename: String, mime: String, bytes: ByteArray, ttlS: Int = 1800): String =
        JSONObject()
            .put("type", "push")
            .put("item_id", UUID.randomUUID().toString())
            .put("kind", kindForMime(mime))
            .put("mime", mime.ifBlank { "application/octet-stream" })
            .put("filename", filename)
            .put("size", bytes.size)
            .put("sha256", sha256Hex(bytes))
            .put("ttl_s", ttlS)
            .put("targets", "all")
            .put("payload", b64encodeBytes(bytes))
            .put("created_at", nowRfc3339())
            .toString()

    /** Larger files: bytes already uploaded via POST /api/blobs. */
    fun pushFileBlob(filename: String, mime: String, bytes: ByteArray, blobId: String, ttlS: Int = 1800): String =
        JSONObject()
            .put("type", "push")
            .put("item_id", UUID.randomUUID().toString())
            .put("kind", kindForMime(mime))
            .put("mime", mime.ifBlank { "application/octet-stream" })
            .put("filename", filename)
            .put("size", bytes.size)
            .put("sha256", sha256Hex(bytes))
            .put("ttl_s", ttlS)
            .put("targets", "all")
            .put("blob_id", blobId)
            .put("created_at", nowRfc3339())
            .toString()
}
