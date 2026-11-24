package com.xiaobao.desk.ui

import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.TextView
import androidx.fragment.app.Fragment
import com.google.android.material.chip.ChipGroup
import com.google.android.material.materialswitch.MaterialSwitch
import com.google.android.material.slider.Slider
import com.xiaobao.desk.R

class SettingsFragment : Fragment() {

    private lateinit var store: AppSettingsStore

    override fun onCreateView(
        inflater: LayoutInflater,
        container: ViewGroup?,
        savedInstanceState: Bundle?
    ): View = inflater.inflate(R.layout.fragment_settings, container, false)

    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        super.onViewCreated(view, savedInstanceState)
        store = AppSettingsStore(requireContext())
        val s = store.load()

        val swSuper = view.findViewById<MaterialSwitch>(R.id.swDefaultSuperScreen)
        val swVMouse = view.findViewById<MaterialSwitch>(R.id.swDefaultVirtualMouse)
        val swPreserveCursor = view.findViewById<MaterialSwitch>(R.id.swPreserveCursor)
        val swMigrateWindows = view.findViewById<MaterialSwitch>(R.id.swMigrateWindows)
        val swAutoHideToolbar = view.findViewById<MaterialSwitch>(R.id.swAutoHideToolbar)
        val cgSecondaryScale = view.findViewById<ChipGroup>(R.id.cgSecondaryScale)
        val cgFps = view.findViewById<ChipGroup>(R.id.cgSettingsFps)
        val cgCodec = view.findViewById<ChipGroup>(R.id.cgSettingsCodec)
        val sliderBitrate = view.findViewById<Slider>(R.id.sliderSettingsBitrate)
        val tvBitrate = view.findViewById<TextView>(R.id.tvSettingsBitrate)
        val swMute = view.findViewById<MaterialSwitch>(R.id.swMuteHostOnStream)
        val swPrivacy = view.findViewById<MaterialSwitch>(R.id.swPrivacyOnStream)
        val swAutoLock = view.findViewById<MaterialSwitch>(R.id.swAutoLockOnExit)
        val swPeripherals = view.findViewById<MaterialSwitch>(R.id.swAutoPeripherals)
        val swIme = view.findViewById<MaterialSwitch>(R.id.swAutoIme)
        val swClip = view.findViewById<MaterialSwitch>(R.id.swClipboardSync)

        swSuper.isChecked = s.defaultSuperScreen
        swVMouse.isChecked = s.defaultVirtualMouse
        swPreserveCursor.isChecked = s.preserveHostCursor
        swMigrateWindows.isChecked = s.migrateWindowsOnExtended
        swAutoHideToolbar.isChecked = s.autoHideToolbar
        cgSecondaryScale.check(
            when {
                s.secondaryScale <= 1.05f -> R.id.chipScale100
                s.secondaryScale <= 1.17f -> R.id.chipScale115
                s.secondaryScale <= 1.22f -> R.id.chipScale120
                else -> R.id.chipScale125
            }
        )
        cgFps.check(
            when (s.defaultFps) {
                60 -> R.id.chipSetFps60
                144 -> R.id.chipSetFps144
                else -> R.id.chipSetFps120
            }
        )
        cgCodec.check(if (s.defaultCodec == "h264") R.id.chipSetH264 else R.id.chipSetHevc)
        sliderBitrate.value = s.defaultBitrateMbps.toFloat().coerceIn(2f, 50f)
        tvBitrate.text = "${sliderBitrate.value.toInt()} Mbps"
        swMute.isChecked = s.muteHostOnStream
        swPrivacy.isChecked = s.privacyScreenOnStream
        swAutoLock.isChecked = s.autoLockOnDisconnect
        swPeripherals.isChecked = s.autoConnectPeripherals
        swIme.isChecked = s.autoShowIme
        swClip.isChecked = s.clipboardSync

        fun persist() {
            val fps = when (cgFps.checkedChipId) {
                R.id.chipSetFps60 -> 60
                R.id.chipSetFps144 -> 144
                else -> 120
            }
            val scale = when (cgSecondaryScale.checkedChipId) {
                R.id.chipScale100 -> 1.00f
                R.id.chipScale115 -> 1.15f
                R.id.chipScale125 -> 1.25f
                else -> 1.20f
            }
            val codec = if (cgCodec.checkedChipId == R.id.chipSetH264) "h264" else "hevc"
            store.save(
                AppSettings(
                    defaultSuperScreen = swSuper.isChecked,
                    defaultVirtualMouse = swVMouse.isChecked,
                    defaultFps = fps,
                    defaultCodec = codec,
                    defaultBitrateMbps = sliderBitrate.value.toInt(),
                    secondaryScale = scale,
                    preserveHostCursor = swPreserveCursor.isChecked,
                    migrateWindowsOnExtended = swMigrateWindows.isChecked,
                    autoHideToolbar = swAutoHideToolbar.isChecked,
                    muteHostOnStream = swMute.isChecked,
                    privacyScreenOnStream = swPrivacy.isChecked,
                    autoLockOnDisconnect = swAutoLock.isChecked,
                    autoConnectPeripherals = swPeripherals.isChecked,
                    autoShowIme = swIme.isChecked,
                    clipboardSync = swClip.isChecked
                )
            )
        }

        swSuper.setOnCheckedChangeListener { _, _ -> persist() }
        swVMouse.setOnCheckedChangeListener { _, _ -> persist() }
        swPreserveCursor.setOnCheckedChangeListener { _, _ -> persist() }
        swMigrateWindows.setOnCheckedChangeListener { _, _ -> persist() }
        swAutoHideToolbar.setOnCheckedChangeListener { _, _ -> persist() }
        cgSecondaryScale.setOnCheckedStateChangeListener { _, _ -> persist() }
        cgFps.setOnCheckedStateChangeListener { _, _ -> persist() }
        cgCodec.setOnCheckedStateChangeListener { _, _ -> persist() }
        sliderBitrate.addOnChangeListener { _, v, _ ->
            tvBitrate.text = "${v.toInt()} Mbps"
            persist()
        }
        swMute.setOnCheckedChangeListener { _, _ -> persist() }
        swPrivacy.setOnCheckedChangeListener { _, _ -> persist() }
        swAutoLock.setOnCheckedChangeListener { _, _ -> persist() }
        swPeripherals.setOnCheckedChangeListener { _, _ -> persist() }
        swIme.setOnCheckedChangeListener { _, _ -> persist() }
        swClip.setOnCheckedChangeListener { _, _ -> persist() }
    }
}
