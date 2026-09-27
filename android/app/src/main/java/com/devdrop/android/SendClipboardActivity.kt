package com.devdrop.android

import android.app.Activity
import android.content.ClipData
import android.content.ClipboardManager
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.widget.Toast

/**
 * Transparent activity launched from the "Send clipboard" notification action.
 * Exists for one reason: Android 10+ only lets a FOREGROUND app read the
 * clipboard. This activity is foreground while it runs, so the read succeeds.
 * It immediately hands the text to [DevDropService] and finishes — no UI.
 *
 * IMPORTANT: the read must happen after the window gains focus
 * ([onWindowFocusChanged]), NOT in [onCreate] — before focus is granted the
 * system still reports the clipboard as empty.
 */
class SendClipboardActivity : Activity() {

    private val handler = Handler(Looper.getMainLooper())
    private var handled = false

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        // Never show anything: no layout, no transition. The user only sees a toast.
        overridePendingTransition(0, 0)
        // Fallback in case the focus callback never fires: try anyway shortly.
        handler.postDelayed({ doSendOnce() }, 600)
    }

    override fun onWindowFocusChanged(hasFocus: Boolean) {
        super.onWindowFocusChanged(hasFocus)
        if (hasFocus) doSendOnce()
    }

    private fun doSendOnce() {
        if (handled) return
        handled = true
        val text = readClipboardText()
        if (text.isNullOrBlank()) {
            // One last retry — focus sometimes lands a beat after the callback.
            handler.postDelayed({
                val retry = readClipboardText()
                if (retry.isNullOrBlank()) {
                    Toast.makeText(this, "Clipboard is empty or not text", Toast.LENGTH_SHORT).show()
                } else {
                    send(retry)
                }
                finishAndRemoveTask()
                overridePendingTransition(0, 0)
            }, 400)
            return
        }
        send(text)
        finishAndRemoveTask()
        overridePendingTransition(0, 0)
    }

    private fun send(text: String) {
        if (!Store.isLoggedIn(this)) {
            Toast.makeText(this, "Log in to CrossShare first", Toast.LENGTH_SHORT).show()
            return
        }
        DevDropService.pushText(this, text)
        Toast.makeText(this, "Sent to PC", Toast.LENGTH_SHORT).show()
    }

    private fun readClipboardText(): String? {
        return try {
            val cm = getSystemService(CLIPBOARD_SERVICE) as ClipboardManager
            if (!cm.hasPrimaryClip()) return null
            val clip: ClipData = cm.primaryClip ?: return null
            if (clip.itemCount == 0) return null
            clip.getItemAt(0)?.coerceToText(this)?.toString()
        } catch (e: Exception) {
            null
        }
    }

    override fun onDestroy() {
        handler.removeCallbacksAndMessages(null)
        super.onDestroy()
    }
}
