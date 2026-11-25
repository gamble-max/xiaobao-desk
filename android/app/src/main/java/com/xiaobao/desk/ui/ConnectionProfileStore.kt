package com.xiaobao.desk.ui

import android.content.Context
import org.json.JSONArray
import org.json.JSONObject
import java.util.UUID

data class ConnectionProfile(
    val id: String = UUID.randomUUID().toString(),
    val name: String,
    val hubAddress: String = "",
    val hostId: String = "default-host",
    val certFingerprint: String,
    val password: String = "",
    val hubSecret: String = "",
    val superScreen: Boolean = true,
    val fps: Int = 120,
    val codec: String = "hevc",
    val bitrateMbps: Int = 12
) {
    fun toJson(): JSONObject = JSONObject().apply {
        put("id", id)
        put("name", name)
        put("hubAddress", hubAddress)
        put("hostId", hostId)
        put("certFingerprint", certFingerprint)
        put("password", password)
        put("hubSecret", hubSecret)
        put("superScreen", superScreen)
        put("fps", fps)
        put("codec", codec)
        put("bitrateMbps", bitrateMbps)
    }

    companion object {
        fun fromJson(obj: JSONObject): ConnectionProfile = ConnectionProfile(
            id = obj.optString("id", UUID.randomUUID().toString()),
            name = obj.optString("name", "My PC"),
            hubAddress = obj.optString("hubAddress", obj.optString("serverAddress", "")),
            hostId = obj.optString("hostId", "default-host").ifEmpty { "default-host" },
            certFingerprint = obj.optString("certFingerprint", ""),
            password = obj.optString("password", ""),
            hubSecret = obj.optString("hubSecret", ""),
            superScreen = obj.optBoolean("superScreen", true),
            fps = obj.optInt("fps", 120),
            codec = obj.optString("codec", "hevc"),
            bitrateMbps = obj.optInt("bitrateMbps", 12)
        )
    }
}

class ConnectionProfileStore(context: Context) {
    private val prefs = context.getSharedPreferences("xb_profiles", Context.MODE_PRIVATE)

    fun loadAll(): MutableList<ConnectionProfile> {
        val raw = prefs.getString("profiles_json", null) ?: return mutableListOf()
        val list = mutableListOf<ConnectionProfile>()
        try {
            val arr = JSONArray(raw)
            for (i in 0 until arr.length()) {
                list.add(ConnectionProfile.fromJson(arr.getJSONObject(i)))
            }
        } catch (_: Exception) {
        }
        return list
    }

    fun saveAll(profiles: List<ConnectionProfile>) {
        val arr = JSONArray()
        profiles.forEach { arr.put(it.toJson()) }
        prefs.edit().putString("profiles_json", arr.toString()).apply()
    }

    fun upsert(profile: ConnectionProfile): List<ConnectionProfile> {
        val list = loadAll()
        val idx = list.indexOfFirst { it.id == profile.id || it.name == profile.name }
        if (idx >= 0) {
            list[idx] = profile
        } else {
            list.add(profile)
        }
        saveAll(list)
        return list
    }

    fun deleteById(id: String): List<ConnectionProfile> {
        val list = loadAll().filterNot { it.id == id }
        saveAll(list)
        return list
    }
}
