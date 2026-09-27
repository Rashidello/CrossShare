package com.devdrop.android

import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.Button
import android.widget.TextView
import androidx.recyclerview.widget.RecyclerView

/** Row in the Devices screen. Disconnect button delegates to the activity. */
class DeviceAdapter(
    private val onDisconnect: (DevicesActivity.DeviceInfo) -> Unit
) : RecyclerView.Adapter<DeviceAdapter.Holder>() {

    private var items: List<DevicesActivity.DeviceInfo> = emptyList()
    private var selfId: String = ""

    fun submit(newItems: List<DevicesActivity.DeviceInfo>, self: String) {
        items = newItems
        selfId = self
        notifyDataSetChanged()
    }

    class Holder(v: View) : RecyclerView.ViewHolder(v) {
        val dot: TextView = v.findViewById(R.id.tvDot)
        val name: TextView = v.findViewById(R.id.tvName)
        val meta: TextView = v.findViewById(R.id.tvMeta)
        val badge: TextView = v.findViewById(R.id.tvBadge)
        val btnDisconnect: Button = v.findViewById(R.id.btnDisconnect)
    }

    override fun onCreateViewHolder(parent: ViewGroup, viewType: Int): Holder {
        val v = LayoutInflater.from(parent.context)
            .inflate(R.layout.item_device, parent, false)
        return Holder(v)
    }

    override fun getItemCount() = items.size

    override fun onBindViewHolder(h: Holder, pos: Int) {
        val d = items[pos]
        h.dot.text = if (d.online) "●" else "○"
        h.dot.setTextColor(
            if (d.online) 0xFF2E7D32.toInt() else 0xFF9E9E9E.toInt()
        )
        h.name.text = d.name.ifEmpty { "(unnamed)" }
        val seen = if (d.online) "online now" else d.lastSeen.ifEmpty { "offline" }
        h.meta.text = "${d.os.ifEmpty { "?" }} • $seen"
        val isSelf = d.id.isNotEmpty() && d.id == selfId
        h.badge.visibility = if (isSelf) View.VISIBLE else View.GONE
        h.btnDisconnect.setOnClickListener { onDisconnect(d) }
    }
}
