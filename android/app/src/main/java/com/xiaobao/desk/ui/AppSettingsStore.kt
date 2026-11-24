package com.xiaobao.desk.ui

import android.content.Context

data class AppSettings(
    val defaultSuperScreen: Boolean = false,
    val defaultVirtualMouse: Boolean = false,
    val defaultFps: Int = 120,
    val defaultCodec: String = "hevc",
    val defaultBitrateMbps: Int = 12,
    val secondaryScale: Float = 1.20f,
    val preserveHostCursor: Boolean = true,
    val migrateWindowsOnExtended: Boolean = true,
    val autoHideToolbar: Boolean = true,
    val muteHostOnStream: Boolean = true,
    val privacyScreenOnStream: Boolean = false,
    val autoLockOnDisconnect: Boolean = false,
    val autoConnectPeripherals: Boolean = true,
    val autoShowIme: Boolean = true,
    val clipboardSync: Boolean = true
)

class AppSettingsStore(context: Context) {
    private val prefs = context.applicationContext.getSharedPreferences("xb_app_settings", Context.MODE_PRIVATE)

    fun load(): AppSettings = AppSettings(
        defaultSuperScreen = prefs.getBoolean("defaultSuperScreen", false),
        defaultVirtualMouse = prefs.getBoolean("defaultVirtualMouse", false),
        defaultFps = prefs.getInt("defaultFps", 120),
        defaultCodec = prefs.getString("defaultCodec", "hevc") ?: "hevc",
        defaultBitrateMbps = prefs.getInt("defaultBitrateMbps", 12),
        secondaryScale = prefs.getFloat("secondaryScale", 1.20f).coerceIn(1.0f, 1.5f),
        preserveHostCursor = prefs.getBoolean("preserveHostCursor", true),
        migrateWindowsOnExtended = prefs.getBoolean("migrateWindowsOnExtended", true),
        autoHideToolbar = prefs.getBoolean("autoHideToolbar", true),
        muteHostOnStream = prefs.getBoolean("muteHostOnStream", true),
        privacyScreenOnStream = prefs.getBoolean("privacyScreenOnStream", false),
        autoLockOnDisconnect = prefs.getBoolean("autoLockOnDisconnect", false),
        autoConnectPeripherals = prefs.getBoolean("autoConnectPeripherals", true),
        autoShowIme = prefs.getBoolean("autoShowIme", true),
        clipboardSync = prefs.getBoolean("clipboardSync", true)
    )

    fun save(s: AppSettings) {
        prefs.edit()
            .putBoolean("defaultSuperScreen", s.defaultSuperScreen)
            .putBoolean("defaultVirtualMouse", s.defaultVirtualMouse)
            .putInt("defaultFps", s.defaultFps)
            .putString("defaultCodec", s.defaultCodec)
            .putInt("defaultBitrateMbps", s.defaultBitrateMbps)
            .putFloat("secondaryScale", s.secondaryScale)
            .putBoolean("preserveHostCursor", s.preserveHostCursor)
            .putBoolean("migrateWindowsOnExtended", s.migrateWindowsOnExtended)
            .putBoolean("autoHideToolbar", s.autoHideToolbar)
            .putBoolean("muteHostOnStream", s.muteHostOnStream)
            .putBoolean("privacyScreenOnStream", s.privacyScreenOnStream)
            .putBoolean("autoLockOnDisconnect", s.autoLockOnDisconnect)
            .putBoolean("autoConnectPeripherals", s.autoConnectPeripherals)
            .putBoolean("autoShowIme", s.autoShowIme)
            .putBoolean("clipboardSync", s.clipboardSync)
            .apply()
    }
}
