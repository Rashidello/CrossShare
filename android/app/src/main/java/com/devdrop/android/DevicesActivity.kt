package com.devdrop.android

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.os.Build
import android.os.Bundle
import android.view.View
import android.widget.TextView
import androidx.appcompat.app.AppCompatActivity
import androidx.recyclerview.widget.LinearLayoutManager
import androidx.recyclerview.widget.RecyclerView
import com.google.android.material.dialog.MaterialAlertDialogBuilder
import org.json.JSONArray
import java.time.Instant

/** Shows the last presence array from the relay. Revoke disconnects a device. */
class DevicesActivity : AppCompatActivity() {

    data class DeviceInfo(
        val id: String,
        val name: String,
        val os: String,
        val online: Boolean,
        val lastSeen: String
    )

    private lateinit var adapter: DeviceAdapter
    private lateinit var tvEmpty: TextView
    private var selfId: String = ""
    private var currentDevices: List<DeviceInfo> = emptyList()

    private val presenceReceiver = object : BroadcastReceiver() {
        override fun onReceive(ctx: Context, intent: Intent) {
            if (intent.action != DevDropService.BC_PRESENCE) return
            selfId = intent.getStringExtra(DevDropService.EXTRA_SELF_ID) ?: selfId
            render(intent.getStringExtra(DevDropService.EXTRA_DEVICES) ?: "[]")
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_devices)

        adapter = DeviceAdapter { d -> confirmDisconnect(d) }
        findViewById<RecyclerView>(R.id.devicesList).apply {
            layoutManager = LinearLayoutManager(this@DevicesActivity)
            adapter = this@DevicesActivity.adapter
        }
        tvEmpty = findViewById(R.id.tvDevicesEmpty)
        findViewById<View>(R.id.btnDevicesBack).setOnClickListener { finish() }
        findViewById<View>(R.id.btnDisconnectOthers).setOnClickListener { confirmDisconnectAll() }
    }

    override fun onResume() {
        super.onResume()
        val f = IntentFilter(DevDropService.BC_PRESENCE)
        if (Build.VERSION.SDK_INT >= 33) {
            registerReceiver(presenceReceiver, f, RECEIVER_NOT_EXPORTED)
        } else {
            @Suppress("UnspecifiedRegisterReceiverFlag")
            registerReceiver(presenceReceiver, f)
        }
        DevDropService.requestPresence(this)
    }

    override fun onPause() {
        super.onPause()
        try { unregisterReceiver(presenceReceiver) } catch (e: Exception) { /* ignore */ }
    }

    private fun render(json: String) {
        val out = ArrayList<DeviceInfo>()
        try {
            val arr = JSONArray(json)
            for (i in 0 until arr.length()) {
                val o = arr.getJSONObject(i)
                out.add(
                    DeviceInfo(
                        id = o.optString("id", ""),
                        name = o.optString("name", ""),
                        os = o.optString("os", ""),
                        online = o.optBoolean("online", false),
                        lastSeen = formatLastSeen(o.optString("last_seen", ""))
                    )
                )
            }
        } catch (e: Exception) { /* ignore -> empty */ }
        // This phone first, then the rest in received order.
        val sorted = out.sortedWith(compareBy { if (it.id.isNotEmpty() && it.id == selfId) 0 else 1 })
        currentDevices = sorted
        adapter.submit(sorted, selfId)
        tvEmpty.visibility = if (sorted.isEmpty()) View.VISIBLE else View.GONE
    }

    private fun formatLastSeen(raw: String): String {
        if (raw.isBlank()) return ""
        return try {
            val instant = Instant.parse(raw)
            val ms = instant.toEpochMilli()
            java.text.SimpleDateFormat("MMM d, HH:mm", java.util.Locale.getDefault())
                .format(java.util.Date(ms))
        } catch (e: Exception) {
            raw
        }
    }

    private fun confirmDisconnect(d: DeviceInfo) {
        val isSelf = d.id.isNotEmpty() && d.id == selfId
        val msg = if (isSelf) {
            "Disconnect \"${d.name}\"? WARNING: this will log out this phone."
        } else {
            "Disconnect \"${d.name}\" (${d.os})?"
        }
        MaterialAlertDialogBuilder(this)
            .setTitle("Disconnect device")
            .setMessage(msg)
            .setNegativeButton("Cancel", null)
            .setPositiveButton("Disconnect") { _, _ ->
                DevDropService.revokeDevice(this, d.id)
            }
            .show()
    }

    private fun confirmDisconnectAll() {
        val others = currentDevices.filter { it.id.isNotEmpty() && it.id != selfId }
        if (others.isEmpty()) {
            MaterialAlertDialogBuilder(this)
                .setTitle("Disconnect all others")
                .setMessage("No other devices to disconnect.")
                .setPositiveButton("OK", null)
                .show()
            return
        }
        MaterialAlertDialogBuilder(this)
            .setTitle("Disconnect all others")
            .setMessage("Disconnect ${others.size} other device(s)? This phone stays connected.")
            .setNegativeButton("Cancel", null)
            .setPositiveButton("Disconnect all") { _, _ ->
                for (d in others) {
                    DevDropService.revokeDevice(this, d.id)
                }
                DevDropService.requestPresence(this)
            }
            .show()
    }
}
