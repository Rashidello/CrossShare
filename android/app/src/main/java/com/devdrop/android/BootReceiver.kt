package com.devdrop.android

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.os.Build

/** After reboot, resume sync if the user was logged in. */
class BootReceiver : BroadcastReceiver() {
    override fun onReceive(ctx: Context, intent: Intent) {
        if (intent.action != Intent.ACTION_BOOT_COMPLETED) return
        if (!Store.isLoggedIn(ctx)) return
        try {
            DevDropService.start(ctx)
        } catch (e: Exception) {
            // Android 14+ may refuse FGS start from background; the user
            // opening the app once resumes sync. Nothing else to do here.
        }
    }
}
