package com.xiaobao.desk.ui

import android.content.Context
import android.util.Base64
import com.xiaobao.desk.core.XbConnectionManager
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
    val superScreen: Boolean = false,
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
        const val MAX_IMPORT_PAYLOAD_BYTES = 16 * 1024
        private val SHA256_HEX_REGEX = Regex("^[0-9a-f]{64}$")

        fun normalizeFingerprint(raw: String): String =
            raw.replace(":", "").replace(" ", "").trim().lowercase()

        fun isValidSha256Fingerprint(raw: String): Boolean =
            normalizeFingerprint(raw).matches(SHA256_HEX_REGEX)

        fun fromImportDataParam(encodedData: String): ConnectionProfile {
            val trimmed = encodedData.trim()
            require(trimmed.isNotEmpty()) { "缺少导入数据 (data)" }
            require(trimmed.length <= MAX_IMPORT_PAYLOAD_BYTES * 2) {
                "导入配置数据过大（超过 16KB 限制）"
            }
            val rawBytes = Base64.decode(
                trimmed,
                Base64.URL_SAFE or Base64.NO_WRAP or Base64.NO_PADDING
            )
            require(rawBytes.size <= MAX_IMPORT_PAYLOAD_BYTES) {
                "导入配置数据过大（超过 16KB 限制）"
            }
            return parseAndValidateImportJson(String(rawBytes, Charsets.UTF_8))
        }

        fun parseAndValidateImportJson(rawJson: String): ConnectionProfile {
            require(rawJson.toByteArray(Charsets.UTF_8).size <= MAX_IMPORT_PAYLOAD_BYTES) {
                "导入配置数据过大（超过 16KB 限制）"
            }
            val obj = JSONObject(rawJson)
            val hubAddress = obj.optString("hubAddress", obj.optString("serverAddress", "")).trim()
            require(hubAddress.isNotEmpty()) { "缺少必填字段 hubAddress" }
            val parsedEndpoint = XbConnectionManager.parseHostPort(hubAddress, 21116)
            require(parsedEndpoint != null && parsedEndpoint.first.isNotBlank() && parsedEndpoint.second in 1..65535) {
                "服务端地址格式无效: $hubAddress"
            }

            val normalizedFp = normalizeFingerprint(obj.optString("certFingerprint", ""))
            require(normalizedFp.matches(SHA256_HEX_REGEX)) {
                "证书指纹 (certFingerprint) 格式无效，需为 64 位十六进制 SHA-256"
            }

            val rawCodec = obj.optString("codec", "hevc").trim().lowercase()
            val validCodec = if (rawCodec == "h264") "h264" else "hevc"

            return ConnectionProfile(
                id = obj.optString("id", "").trim().ifEmpty { UUID.randomUUID().toString() },
                name = obj.optString("name", "My PC").trim().ifEmpty { "My PC" },
                hubAddress = hubAddress,
                hostId = obj.optString("hostId", "default-host").trim().ifEmpty { "default-host" },
                certFingerprint = normalizedFp,
                password = obj.optString("password", ""),
                hubSecret = obj.optString("hubSecret", ""),
                superScreen = obj.optBoolean("superScreen", false),
                fps = obj.optInt("fps", 60).coerceIn(30, 144),
                codec = validCodec,
                bitrateMbps = obj.optInt("bitrateMbps", 12).coerceIn(2, 50)
            )
        }

        fun fromJson(obj: JSONObject): ConnectionProfile = ConnectionProfile(
            id = obj.optString("id", UUID.randomUUID().toString()),
            name = obj.optString("name", "My PC"),
            hubAddress = obj.optString("hubAddress", obj.optString("serverAddress", "")),
            hostId = obj.optString("hostId", "default-host").ifEmpty { "default-host" },
            certFingerprint = obj.optString("certFingerprint", ""),
            password = obj.optString("password", ""),
            hubSecret = obj.optString("hubSecret", ""),
            superScreen = obj.optBoolean("superScreen", false),
            fps = obj.optInt("fps", 120).coerceIn(30, 144),
            codec = if (obj.optString("codec", "hevc").lowercase() == "h264") "h264" else "hevc",
            bitrateMbps = obj.optInt("bitrateMbps", 12).coerceIn(2, 50)
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
