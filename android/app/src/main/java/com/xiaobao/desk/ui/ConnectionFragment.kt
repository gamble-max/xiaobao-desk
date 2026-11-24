package com.xiaobao.desk.ui

import android.graphics.Color
import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.LinearLayout
import android.widget.TextView
import android.widget.Toast
import androidx.fragment.app.Fragment
import androidx.lifecycle.lifecycleScope
import com.google.android.material.button.MaterialButton
import com.google.android.material.card.MaterialCardView
import com.google.android.material.chip.ChipGroup
import com.google.android.material.dialog.MaterialAlertDialogBuilder
import com.google.android.material.slider.Slider
import com.google.android.material.textfield.TextInputEditText
import com.xiaobao.desk.R
import com.xiaobao.desk.core.ConnectionConfig
import com.xiaobao.desk.core.ConnectionState
import com.xiaobao.desk.core.LanDiscovery
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.util.UUID
import kotlin.math.roundToInt

class ConnectionFragment : Fragment() {

    private lateinit var profileStore: ConnectionProfileStore
    private lateinit var llProfileCards: LinearLayout
    private lateinit var tvEmptyProfiles: TextView
    private lateinit var chipGroupDisplayMode: ChipGroup
    private lateinit var tvDisplayModeHint: TextView
    private lateinit var chipGroupFps: ChipGroup
    private lateinit var chipGroupCodec: ChipGroup
    private lateinit var sliderBitrate: Slider
    private lateinit var tvBitrateValue: TextView
    private lateinit var btnConnect: MaterialButton
    private lateinit var btnDisconnect: MaterialButton
    private lateinit var tvLog: TextView

    private var activeProfile: ConnectionProfile? = null
    private var totpCode: String = ""
    private var awaitingStreamLaunch = false

    override fun onCreateView(
        inflater: LayoutInflater,
        container: ViewGroup?,
        savedInstanceState: Bundle?
    ): View = inflater.inflate(R.layout.fragment_connection, container, false)

    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        super.onViewCreated(view, savedInstanceState)
        profileStore = ConnectionProfileStore(requireContext())

        llProfileCards = view.findViewById(R.id.llProfileCards)
        tvEmptyProfiles = view.findViewById(R.id.tvEmptyProfiles)
        chipGroupDisplayMode = view.findViewById(R.id.chipGroupDisplayMode)
        tvDisplayModeHint = view.findViewById(R.id.tvDisplayModeHint)
        chipGroupFps = view.findViewById(R.id.chipGroupFps)
        chipGroupCodec = view.findViewById(R.id.chipGroupCodec)
        sliderBitrate = view.findViewById(R.id.sliderBitrate)
        tvBitrateValue = view.findViewById(R.id.tvBitrateValue)
        btnConnect = view.findViewById(R.id.btnConnect)
        btnDisconnect = view.findViewById(R.id.btnDisconnect)
        tvLog = view.findViewById(R.id.tvConnectionLog)

        sliderBitrate.addOnChangeListener { _, value, _ ->
            tvBitrateValue.text = "${value.toInt()} Mbps"
        }

        chipGroupDisplayMode.setOnCheckedStateChangeListener { _, _ ->
            updateDisplayModeHint()
        }

        view.findViewById<MaterialButton>(R.id.btnScanLan).setOnClickListener {
            scanLanHosts()
        }
        view.findViewById<MaterialButton>(R.id.btnAddProfile).setOnClickListener {
            showProfileDialog(null)
        }
        btnConnect.setOnClickListener { startConnect(true) }
        btnDisconnect.setOnClickListener {
            val main = requireActivity() as MainActivity
            val state = main.connectionManager.connectionState.value
            if (state == ConnectionState.DISCONNECTED || state == ConnectionState.ERROR) {
                startConnect(false)
            } else {
                awaitingStreamLaunch = false
                main.connectionManager.disconnect()
            }
        }

        val appDefaults = AppSettingsStore(requireContext()).load()
        val allProfiles = profileStore.loadAll()
        if (activeProfile == null && allProfiles.isNotEmpty()) {
            applyProfile(allProfiles.first())
        } else {
            chipGroupDisplayMode.check(
                if (appDefaults.defaultSuperScreen) R.id.chipModeExtended else R.id.chipModeMirror
            )
            when (appDefaults.defaultFps) {
                60 -> chipGroupFps.check(R.id.chipFps60)
                144 -> chipGroupFps.check(R.id.chipFps144)
                else -> chipGroupFps.check(R.id.chipFps120)
            }
            chipGroupCodec.check(if (appDefaults.defaultCodec == "h264") R.id.chipCodecH264 else R.id.chipCodecHevc)
            sliderBitrate.value = appDefaults.defaultBitrateMbps.toFloat().coerceIn(2f, 50f)
            tvBitrateValue.text = "${sliderBitrate.value.toInt()} Mbps"
            renderProfiles(allProfiles)
        }
        updateDisplayModeHint()
        observeConnection()
    }

    fun reloadAndConnect(profile: ConnectionProfile) {
        if (!isAdded) return
        applyProfile(profile)
        startConnect(true)
    }

    fun reloadProfileOnly(profile: ConnectionProfile) {
        if (!isAdded) return
        applyProfile(profile)
    }

    private fun updateDisplayModeHint() {
        val isExtended = chipGroupDisplayMode.checkedChipId == R.id.chipModeExtended
        tvDisplayModeHint.text = if (isExtended) {
            "按手机触控缩放比例扩展为电脑无线副屏，自动将窗口与焦点移至副屏。"
        } else {
            "保持电脑原屏幕比例居中显示，不拉伸、不改动主屏分辨率。"
        }
    }

    private fun renderProfiles(profiles: List<ConnectionProfile>) {
        llProfileCards.removeAllViews()
        tvEmptyProfiles.visibility = if (profiles.isEmpty()) View.VISIBLE else View.GONE
        val ctx = requireContext()
        val density = resources.displayMetrics.density

        for (p in profiles) {
            val isSelected = p.id == activeProfile?.id
            val card = MaterialCardView(ctx).apply {
                layoutParams = LinearLayout.LayoutParams(
                    LinearLayout.LayoutParams.MATCH_PARENT,
                    LinearLayout.LayoutParams.WRAP_CONTENT
                ).apply {
                    bottomMargin = (8 * density).toInt()
                }
                radius = 12 * density
                setCardBackgroundColor(
                    if (isSelected) ctx.getColor(R.color.xb_bg_surface_elevated)
                    else ctx.getColor(R.color.xb_bg_primary)
                )
                strokeWidth = (if (isSelected) 2 else 1) * density.toInt()
                strokeColor = if (isSelected) ctx.getColor(R.color.xb_primary) else ctx.getColor(R.color.xb_border_subtle)
                setOnClickListener { applyProfile(p) }
            }

            val row = LinearLayout(ctx).apply {
                orientation = LinearLayout.HORIZONTAL
                gravity = android.view.Gravity.CENTER_VERTICAL
                val pad = (12 * density).toInt()
                setPadding(pad, pad, pad, pad)
            }

            val textCol = LinearLayout(ctx).apply {
                orientation = LinearLayout.VERTICAL
                layoutParams = LinearLayout.LayoutParams(0, LinearLayout.LayoutParams.WRAP_CONTENT, 1f)
            }
            val tvTitle = TextView(ctx).apply {
                text = p.name
                textSize = 15f
                setTextColor(ctx.getColor(R.color.xb_text_primary))
                setTypeface(typeface, android.graphics.Typeface.BOLD)
            }
            val modeTag = if (p.hubAddress.isBlank()) "无服务端直连" else "自建 Hub + P2P"
            val tvSub = TextView(ctx).apply {
                text = if (isSelected) "当前已选中 · $modeTag" else "点击选中 · $modeTag"
                textSize = 12f
                setTextColor(if (isSelected) ctx.getColor(R.color.xb_primary) else ctx.getColor(R.color.xb_text_muted))
            }
            textCol.addView(tvTitle)
            textCol.addView(tvSub)

            val btnDetails = MaterialButton(
                ctx,
                null,
                com.google.android.material.R.attr.materialButtonOutlinedStyle
            ).apply {
                text = "详情"
                textSize = 12f
                minWidth = 0
                minimumWidth = 0
                setPadding((12 * density).toInt(), 0, (12 * density).toInt(), 0)
                setOnClickListener { showProfileDialog(p) }
            }

            row.addView(textCol)
            row.addView(btnDetails)
            card.addView(row)
            llProfileCards.addView(card)
        }
    }

    private fun applyProfile(p: ConnectionProfile) {
        activeProfile = p
        chipGroupDisplayMode.check(
            if (p.superScreen) R.id.chipModeExtended else R.id.chipModeMirror
        )
        updateDisplayModeHint()
        when (p.fps) {
            60 -> chipGroupFps.check(R.id.chipFps60)
            144 -> chipGroupFps.check(R.id.chipFps144)
            else -> chipGroupFps.check(R.id.chipFps120)
        }
        if (p.codec == "h264") {
            chipGroupCodec.check(R.id.chipCodecH264)
        } else {
            chipGroupCodec.check(R.id.chipCodecHevc)
        }
        sliderBitrate.value = p.bitrateMbps.toFloat().coerceIn(2f, 50f)
        renderProfiles(profileStore.loadAll())
    }

    private fun scanLanHosts() {
        Toast.makeText(requireContext(), "正在扫描同局域网 / 组网主机...", Toast.LENGTH_SHORT).show()
        viewLifecycleOwner.lifecycleScope.launch(Dispatchers.IO) {
            val hosts = LanDiscovery.scan()
            withContext(Dispatchers.Main) {
                if (!isAdded) return@withContext
                if (hosts.isEmpty()) {
                    Toast.makeText(
                        requireContext(),
                        "未发现局域网主机，请确认同处一个 Wi-Fi/组网且已放行 UDP 21118",
                        Toast.LENGTH_LONG
                    ).show()
                    return@withContext
                }
                val labels = hosts.map { "${it.name} (${it.address})" }.toTypedArray()
                MaterialAlertDialogBuilder(requireContext())
                    .setTitle("发现 ${hosts.size} 台主机")
                    .setItems(labels) { _, idx ->
                        val h = hosts[idx]
                        showProfileDialog(
                            ConnectionProfile(
                                name = h.name,
                                directAddress = h.address,
                                hubAddress = "",
                                hostId = h.hostId,
                                certFingerprint = h.certFingerprint
                            ),
                            isNewPreFilled = true
                        )
                    }
                    .setNegativeButton("取消", null)
                    .show()
            }
        }
    }

    private fun showProfileDialog(existing: ConnectionProfile?, isNewPreFilled: Boolean = false) {
        val dlgView = layoutInflater.inflate(R.layout.dialog_profile_edit, null)
        val etName = dlgView.findViewById<TextInputEditText>(R.id.etDlgProfileName)
        val etDirect = dlgView.findViewById<TextInputEditText>(R.id.etDlgDirectAddress)
        val etHub = dlgView.findViewById<TextInputEditText>(R.id.etDlgHubAddress)
        val etHostId = dlgView.findViewById<TextInputEditText>(R.id.etDlgHostId)
        val etFp = dlgView.findViewById<TextInputEditText>(R.id.etDlgCertFingerprint)
        val etPass = dlgView.findViewById<TextInputEditText>(R.id.etDlgPassword)
        val etTotp = dlgView.findViewById<TextInputEditText>(R.id.etDlgTotp)

        if (existing != null) {
            etName.setText(existing.name)
            etDirect.setText(existing.directAddress)
            etHub.setText(existing.hubAddress)
            etHostId.setText(existing.hostId)
            etFp.setText(existing.certFingerprint)
            etPass.setText(existing.password)
            if (existing.id == activeProfile?.id) {
                etTotp.setText(totpCode)
            }
        }

        val builder = MaterialAlertDialogBuilder(requireContext())
            .setTitle(if (existing == null || isNewPreFilled) "新增设备" else "设备连接详情")
            .setView(dlgView)
            .setPositiveButton("保存") { _, _ ->
                val name = etName.text?.toString()?.trim().orEmpty().ifEmpty { "My PC" }
                val saved = ConnectionProfile(
                    id = if (existing != null && !isNewPreFilled) existing.id else UUID.randomUUID().toString(),
                    name = name,
                    directAddress = etDirect.text?.toString()?.trim().orEmpty(),
                    hubAddress = etHub.text?.toString()?.trim().orEmpty(),
                    hostId = etHostId.text?.toString()?.trim().orEmpty(),
                    certFingerprint = etFp.text?.toString()?.trim().orEmpty(),
                    password = etPass.text?.toString().orEmpty(),
                    superScreen = chipGroupDisplayMode.checkedChipId == R.id.chipModeExtended,
                    fps = selectedFps(),
                    codec = selectedCodec(),
                    bitrateMbps = sliderBitrate.value.toInt()
                )
                totpCode = etTotp.text?.toString()?.trim().orEmpty()
                profileStore.upsert(saved)
                applyProfile(saved)
                Toast.makeText(requireContext(), "已保存设备: $name", Toast.LENGTH_SHORT).show()
            }
            .setNeutralButton("取消", null)

        if (existing != null && !isNewPreFilled) {
            builder.setNegativeButton("删除") { _, _ ->
                val updated = profileStore.deleteById(existing.id)
                if (activeProfile?.id == existing.id) {
                    activeProfile = updated.firstOrNull()
                }
                val current = activeProfile
                if (current != null) {
                    applyProfile(current)
                } else {
                    renderProfiles(updated)
                }
                Toast.makeText(requireContext(), "已删除设备", Toast.LENGTH_SHORT).show()
            }
        }

        builder.show()
    }

    private fun selectedFps(): Int = when (chipGroupFps.checkedChipId) {
        R.id.chipFps60 -> 60
        R.id.chipFps144 -> 144
        else -> 120
    }

    private fun selectedCodec(): String =
        if (chipGroupCodec.checkedChipId == R.id.chipCodecH264) "h264" else "hevc"

    private fun isExtendedMode(): Boolean =
        chipGroupDisplayMode.checkedChipId == R.id.chipModeExtended

    private fun buildConfig(startStream: Boolean = true): ConnectionConfig {
        val p = activeProfile ?: ConnectionProfile(
            name = "Default",
            directAddress = "",
            hubAddress = "",
            hostId = "",
            certFingerprint = ""
        )
        val rawHub = p.hubAddress.trim()
        val hubHost: String
        val hubPort: Int
        if (rawHub.isNotEmpty()) {
            val parts = rawHub.split(":")
            hubHost = parts[0]
            hubPort = parts.getOrNull(1)?.toIntOrNull() ?: 21116
        } else {
            hubHost = ""
            hubPort = 21116
        }
        val fp = p.certFingerprint.trim()
        val dm = resources.displayMetrics
        val landW = maxOf(dm.widthPixels, dm.heightPixels)
        val landH = minOf(dm.widthPixels, dm.heightPixels)
        val appDefaults = AppSettingsStore(requireContext()).load()
        val scale = appDefaults.secondaryScale.coerceIn(1.0f, 1.5f)
        val scaledW = ((landW / scale).roundToInt() and 1.inv()).coerceAtLeast(1280)
        val scaledH = ((landH / scale).roundToInt() and 1.inv()).coerceAtLeast(720)
        val extended = isExtendedMode()
        return ConnectionConfig(
            hubHost = hubHost,
            hubPort = hubPort,
            hubCertSha256 = fp,
            hostId = p.hostId.trim(),
            hostCertSha256 = fp,
            password = p.password,
            totpCode = totpCode,
            directHostOverride = p.directAddress.trim(),
            width = if (extended) scaledW else 0,
            height = if (extended) scaledH else 0,
            fps = selectedFps(),
            bitrateKbps = sliderBitrate.value.toInt() * 1000,
            codec = selectedCodec(),
            superScreen = extended,
            migrateWindows = appDefaults.migrateWindowsOnExtended,
            startStreamOnConnect = startStream
        )
    }

    private fun startConnect(startStream: Boolean) {
        val p = activeProfile
        if (p == null || p.certFingerprint.isBlank() ||
            (p.directAddress.isBlank() && (p.hubAddress.isBlank() || p.hostId.isBlank()))
        ) {
            Toast.makeText(requireContext(), "请先填写直连地址或自建 Hub 信息，并填入证书指纹", Toast.LENGTH_SHORT).show()
            showProfileDialog(p)
            return
        }
        val main = requireActivity() as MainActivity
        val cfg = buildConfig(startStream)
        if (main.connectionManager.connectionState.value == ConnectionState.CONNECTED) {
            if (startStream) {
                main.connectionManager.sendStreamEnable(true)
                main.startStreaming(cfg)
            } else {
                Toast.makeText(requireContext(), "已连接电脑（终端与文件传输已就绪）", Toast.LENGTH_SHORT).show()
            }
            return
        }
        awaitingStreamLaunch = startStream
        tvLog.text = if (startStream) "正在连接 ${p.name} 并开启串流..." else "正在仅连接 ${p.name}..."
        main.connectionManager.connect(cfg)
    }

    private fun observeConnection() {
        val main = requireActivity() as MainActivity
        viewLifecycleOwner.lifecycleScope.launch {
            main.connectionManager.connectionState.collectLatest { state ->
                btnDisconnect.isEnabled = true
                when (state) {
                    ConnectionState.DISCONNECTED -> {
                        btnConnect.text = getString(R.string.btn_connect)
                        btnDisconnect.text = "仅连接（终端 / 文件传输）"
                        tvLog.text = "未连接"
                    }
                    ConnectionState.CONNECTING -> {
                        btnConnect.text = getString(R.string.btn_connect)
                        btnDisconnect.text = getString(R.string.btn_disconnect)
                        tvLog.text = "正在并发探测直连 / 中继通道..."
                    }
                    ConnectionState.AUTHENTICATING -> {
                        btnConnect.text = getString(R.string.btn_connect)
                        btnDisconnect.text = getString(R.string.btn_disconnect)
                        tvLog.text = "正在进行 TLS 1.3 安全认证..."
                    }
                    ConnectionState.CONNECTED -> {
                        val path = main.connectionManager.connectionPath.value
                        btnConnect.text = "进入远程画面"
                        btnDisconnect.text = getString(R.string.btn_disconnect)
                        tvLog.text = "已连接 ($path)"
                        if (awaitingStreamLaunch) {
                            awaitingStreamLaunch = false
                            main.startStreaming(buildConfig(true))
                        }
                    }
                    ConnectionState.ERROR -> {
                        awaitingStreamLaunch = false
                        btnConnect.text = getString(R.string.btn_connect)
                        btnDisconnect.text = "仅连接（终端 / 文件传输）"
                        tvLog.text = "连接失败: ${main.connectionManager.lastError.value ?: "Unknown"}"
                    }
                }
            }
        }
    }
}
