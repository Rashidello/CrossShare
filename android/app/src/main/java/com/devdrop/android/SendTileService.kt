package com.devdrop.android

import android.content.Intent
import android.service.quicksettings.TileService

/**
 * Quick Settings tile ("Send clipboard") — a one-tap sender that lives in
 * the notification shade next to Wi-Fi/Bluetooth. Same mechanism as the
 * notification action: it opens [SendClipboardActivity], which is foreground
 * at that moment, so the clipboard read is allowed on Android 10+.
 *
 * Add it once: pull the shade fully down → edit (pencil) → drag
 * "Send clipboard" into the active tiles.
 */
class SendTileService : TileService() {
    override fun onClick() {
        // Clipboard read needs foreground+unlocked, so run after unlock.
        unlockAndRun {
            startActivityAndCollapse(
                Intent(this, SendClipboardActivity::class.java)
                    .addFlags(
                        Intent.FLAG_ACTIVITY_NEW_TASK or
                            Intent.FLAG_ACTIVITY_NO_ANIMATION or
                            Intent.FLAG_ACTIVITY_EXCLUDE_FROM_RECENTS
                    )
            )
        }
    }
}
