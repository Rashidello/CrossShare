package com.devdrop.android

import android.app.RemoteInput
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.os.Bundle
import android.widget.Toast

/**
 * Handles the "Type message" direct-reply action on the persistent
 * notification. The text comes from user input (not the clipboard), so this
 * is fully background-safe on all Android versions — no app is ever opened.
 */
class SendReplyReceiver : BroadcastReceiver() {

    companion object {
        const val KEY_REPLY = "reply_text"
    }

    override fun onReceive(ctx: Context, intent: Intent) {
        val results: Bundle = RemoteInput.getResultsFromIntent(intent) ?: return
        val text = results.getCharSequence(KEY_REPLY)?.toString()?.trim().orEmpty()
        if (text.isEmpty()) return
        if (!Store.isLoggedIn(ctx)) {
            Toast.makeText(ctx, "Log in to CrossShare first", Toast.LENGTH_SHORT).show()
            return
        }
        // Persist first (survives a dead service), then ask the service to flush.
        Store.queueOutgoing(ctx, text)
        try {
            DevDropService.flush(ctx)
        } catch (e: Exception) {
            // Service will flush on its next periodic check / reconnect.
        }
        Toast.makeText(ctx, "Sent to PC", Toast.LENGTH_SHORT).show()
    }
}
