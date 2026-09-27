package com.devdrop.android

import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.TextView
import androidx.recyclerview.widget.RecyclerView
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

/** Newest-first history list. Tap an item to copy it back to the clipboard. */
class HistoryAdapter(private val onTap: (Store.HistoryItem) -> Unit) :
    RecyclerView.Adapter<HistoryAdapter.Holder>() {

    private var items: List<Store.HistoryItem> = emptyList()
    private val fmt = SimpleDateFormat("MMM d, HH:mm", Locale.getDefault())

    fun submit(newItems: List<Store.HistoryItem>) {
        items = newItems
        notifyDataSetChanged()
    }

    class Holder(v: View) : RecyclerView.ViewHolder(v) {
        val title: TextView = v.findViewById(R.id.tvTitle)
        val preview: TextView = v.findViewById(R.id.tvPreview)
    }

    override fun onCreateViewHolder(parent: ViewGroup, viewType: Int): Holder {
        val v = LayoutInflater.from(parent.context)
            .inflate(R.layout.item_history, parent, false)
        return Holder(v)
    }

    override fun getItemCount() = items.size

    override fun onBindViewHolder(h: Holder, pos: Int) {
        val item = items[pos]
        val dir = if (item.outgoing) "↑ To PC" else "↓ From ${item.origin}"
        h.title.text = "$dir • ${fmt.format(Date(item.timeMs))}"
        h.preview.text = if (item.kind == "text") {
            if (item.text.length > 140) item.text.take(140) + "…" else item.text
        } else {
            "📄 ${item.filename.ifEmpty { "file" }} (${formatSize(item.size)})"
        }
        h.itemView.setOnClickListener { onTap(item) }
    }

    private fun formatSize(bytes: Long): String {
        if (bytes <= 0) return "0 B"
        val kb = bytes / 1024.0
        if (kb < 1024) return if (bytes < 1024) "$bytes B" else String.format(Locale.getDefault(), "%.1f KB", kb)
        val mb = kb / 1024.0
        return String.format(Locale.getDefault(), "%.1f MB", mb)
    }
}
