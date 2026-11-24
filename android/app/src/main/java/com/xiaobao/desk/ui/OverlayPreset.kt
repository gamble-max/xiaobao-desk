package com.xiaobao.desk.ui

import android.content.Context
import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.util.UUID

enum class OverlayItemType {
    KEY_BUTTON,
    WASD_STICK
}

data class OverlayItem(
    val id: String = UUID.randomUUID().toString(),
    val type: OverlayItemType,
    var label: String,
    var normX: Float,
    var normY: Float,
    var radiusDp: Float = 32f,
    var vk: Int = 0x20,
    var scan: Int = 0x39,
    var ext: Boolean = false
) {
    fun toJson(): JSONObject = JSONObject().apply {
        put("id", id)
        put("type", type.name)
        put("label", label)
        put("normX", normX.toDouble())
        put("normY", normY.toDouble())
        put("radiusDp", radiusDp.toDouble())
        put("vk", vk)
        put("scan", scan)
        put("ext", ext)
    }

    companion object {
        fun fromJson(obj: JSONObject): OverlayItem = OverlayItem(
            id = obj.optString("id", UUID.randomUUID().toString()),
            type = try {
                OverlayItemType.valueOf(obj.optString("type", "KEY_BUTTON"))
            } catch (_: Exception) {
                OverlayItemType.KEY_BUTTON
            },
            label = obj.optString("label", "Key"),
            normX = obj.optDouble("normX", 0.5).toFloat(),
            normY = obj.optDouble("normY", 0.5).toFloat(),
            radiusDp = obj.optDouble("radiusDp", 32.0).toFloat(),
            vk = obj.optInt("vk", 0x20),
            scan = obj.optInt("scan", 0x39),
            ext = obj.optBoolean("ext", false)
        )
    }
}

object OverlayPresetStore {
    private const val DEFAULT_FILE = "default_overlay.json"

    fun savePreset(context: Context, items: List<OverlayItem>, fileName: String = DEFAULT_FILE) {
        val dir = File(context.filesDir, "overlay_presets").apply { mkdirs() }
        val arr = JSONArray()
        items.forEach { arr.put(it.toJson()) }
        File(dir, fileName).writeText(arr.toString(2))
    }

    fun loadPreset(context: Context, fileName: String = DEFAULT_FILE): MutableList<OverlayItem> {
        val file = File(File(context.filesDir, "overlay_presets"), fileName)
        if (!file.exists()) {
            return mutableListOf(
                OverlayItem(type = OverlayItemType.WASD_STICK, label = "WASD", normX = 0.16f, normY = 0.72f, radiusDp = 56f),
                OverlayItem(type = OverlayItemType.KEY_BUTTON, label = "Space", normX = 0.84f, normY = 0.76f, radiusDp = 34f, vk = 0x20, scan = 0x39),
                OverlayItem(type = OverlayItemType.KEY_BUTTON, label = "E", normX = 0.76f, normY = 0.62f, radiusDp = 28f, vk = 0x45, scan = 0x12)
            )
        }
        val list = mutableListOf<OverlayItem>()
        try {
            val arr = JSONArray(file.readText())
            for (i in 0 until arr.length()) {
                list.add(OverlayItem.fromJson(arr.getJSONObject(i)))
            }
        } catch (_: Exception) {
        }
        return list
    }
}
