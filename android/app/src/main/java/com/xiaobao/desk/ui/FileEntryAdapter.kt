package com.xiaobao.desk.ui

import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.ImageView
import android.widget.TextView
import androidx.recyclerview.widget.RecyclerView
import com.xiaobao.desk.R
import java.util.Locale

data class UiFileEntry(
    val name: String,
    val isDir: Boolean,
    val size: Long,
    val fullPath: String
)

class FileEntryAdapter(
    private val onClick: (UiFileEntry) -> Unit
) : RecyclerView.Adapter<FileEntryAdapter.Holder>() {

    private val items = mutableListOf<UiFileEntry>()
    var selectedPath: String? = null
        private set

    fun submit(newItems: List<UiFileEntry>) {
        items.clear()
        items.addAll(newItems.sortedWith(compareByDescending<UiFileEntry> { it.isDir }.thenBy { it.name.lowercase(Locale.ROOT) }))
        selectedPath = null
        notifyDataSetChanged()
    }

    fun getSelected(): UiFileEntry? = items.firstOrNull { it.fullPath == selectedPath }

    override fun onCreateViewHolder(parent: ViewGroup, viewType: Int): Holder {
        val v = LayoutInflater.from(parent.context).inflate(R.layout.item_file_entry, parent, false)
        return Holder(v)
    }

    override fun getItemCount(): Int = items.size

    override fun onBindViewHolder(holder: Holder, position: Int) {
        val item = items[position]
        holder.ivIcon.setImageResource(if (item.isDir) R.drawable.ic_folder else R.drawable.ic_file)
        holder.tvName.text = item.name
        holder.tvSize.text = if (item.isDir) "<DIR>" else formatBytes(item.size)
        val isSelected = item.fullPath == selectedPath
        holder.itemView.setBackgroundColor(
            if (isSelected) 0x3338BDF8 else 0x00000000
        )
        holder.itemView.setOnClickListener {
            selectedPath = item.fullPath
            notifyDataSetChanged()
            onClick(item)
        }
    }

    class Holder(view: View) : RecyclerView.ViewHolder(view) {
        val ivIcon: ImageView = view.findViewById(R.id.ivFileIcon)
        val tvName: TextView = view.findViewById(R.id.tvFileName)
        val tvSize: TextView = view.findViewById(R.id.tvFileSize)
    }

    private fun formatBytes(bytes: Long): String {
        if (bytes < 1024) return "$bytes B"
        val kb = bytes / 1024.0
        if (kb < 1024) return String.format(Locale.US, "%.1f KB", kb)
        val mb = kb / 1024.0
        if (mb < 1024) return String.format(Locale.US, "%.1f MB", mb)
        return String.format(Locale.US, "%.2f GB", mb / 1024.0)
    }
}
