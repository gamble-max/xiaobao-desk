package com.xiaobao.desk.ui

import android.annotation.SuppressLint
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.pm.ActivityInfo
import android.hardware.input.InputManager
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.text.Editable
import android.text.TextWatcher
import android.view.Gravity
import android.view.HapticFeedbackConstants
import android.view.InputDevice
import android.view.KeyEvent
import android.view.LayoutInflater
import android.view.MotionEvent
import android.view.SurfaceHolder
import android.view.SurfaceView
import android.view.View
import android.view.ViewConfiguration
import android.view.ViewGroup
import android.view.inputmethod.EditorInfo
import android.view.inputmethod.InputMethodManager
import android.widget.Button
import android.widget.EditText
import android.widget.FrameLayout
import android.widget.ImageButton
import android.widget.LinearLayout
import android.widget.TextView
import android.widget.Toast
import androidx.appcompat.app.AlertDialog
import androidx.core.view.GravityCompat
import androidx.drawerlayout.widget.DrawerLayout
import androidx.fragment.app.Fragment
import androidx.lifecycle.lifecycleScope
import com.google.android.material.materialswitch.MaterialSwitch
import com.xiaobao.desk.R
import com.xiaobao.desk.core.ConnectionState
import com.xiaobao.desk.core.TouchPointer
import com.xiaobao.desk.core.XbConnectionManager
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.launch
import kotlin.math.hypot
import kotlin.math.roundToInt

class StreamingFragment : Fragment(), SurfaceHolder.Callback, InputManager.InputDeviceListener {

    private lateinit var conn: XbConnectionManager
    private lateinit var settings: AppSettings
    private lateinit var drawerLayout: DrawerLayout
    private lateinit var streamRoot: FrameLayout
    private lateinit var surfaceView: SurfaceView
    private lateinit var virtualCursorView: VirtualCursorView
    private lateinit var llVirtualMouseBar: LinearLayout
    private lateinit var etHiddenImeInput: EditText
    private lateinit var overlayEditorView: OverlayEditorView
    private lateinit var overlayToolbar: LinearLayout
    private lateinit var virtualKeyboardView: View
    private lateinit var tvStreamStats: TextView
    private lateinit var btnToggleInputMode: Button
    private lateinit var floatingControlPill: LinearLayout
    private lateinit var btnMiniExpandPill: Button
    private lateinit var switchPreserveCursor: MaterialSwitch

    private var currentVideoW = 1920
    private var currentVideoH = 1080
    private var isVirtualMouseMode = false
    private var autoLockOnDisconnect = false
    private var overlayVisible = false
    private var userExiting = false
    private var reconnectAttempt = 0

    // Direct touch & UU virtual mouse gesture state
    private val mainHandler = Handler(Looper.getMainLooper())
    private var touchSlop = 14f
    private var cursorX = 0.5f
    private var cursorY = 0.5f
    private var lastTouchX = 0f
    private var lastTouchY = 0f
    private var downTouchX = 0f
    private var downTouchY = 0f
    private var downNormX = 0.5f
    private var downNormY = 0.5f
    private var movedBeyondSlop = false
    private var leftDragActive = false
    private var longPressFired = false
    private var multiTouchActive = false
    private var twoFingerMoved = false
    private var lastTwoFingerX = 0f
    private var lastTwoFingerY = 0f
    private var scrollOriginNormX = 0.5f
    private var scrollOriginNormY = 0.5f
    private var nativeTouchActive = false
    private var vMouseLeftHeld = false
    private var lastTapUpTimeMs = 0L
    private var doubleTapDragActive = false
    private var extMouseBtnMask = 0

    private val autoHidePillRunnable = Runnable {
        if (!isAdded || !settings.autoHideToolbar) return@Runnable
        if (overlayEditorView.editMode) return@Runnable
        floatingControlPill.visibility = View.GONE
        btnMiniExpandPill.visibility = View.VISIBLE
    }

    private val longPressRunnable = Runnable {
        if (!multiTouchActive && !movedBeyondSlop) {
            longPressFired = true
            surfaceView.performHapticFeedback(HapticFeedbackConstants.LONG_PRESS)
            if (isVirtualMouseMode) {
                conn.sendMouseClick(cursorX, cursorY, "right")
            } else {
                if (nativeTouchActive) {
                    conn.sendTouch(listOf(TouchPointer(0, "cancel", downNormX, downNormY)))
                    nativeTouchActive = false
                }
                conn.sendMouseClick(downNormX, downNormY, "right")
            }
        }
    }

    // Peripheral detection
    private var inputManager: InputManager? = null
    private var hasExternalKeyboard = false
    private var hasExternalMouse = false
    private var hasExternalGamepad = false

    // Clipboard sync
    private var clipboardManager: ClipboardManager? = null
    private var lastClipText: String = ""
    private var resettingImeText = false
    private val clipListener = ClipboardManager.OnPrimaryClipChangedListener {
        if (!::settings.isInitialized || !settings.clipboardSync) return@OnPrimaryClipChangedListener
        val text = readLocalClipboard()
        if (text.isNotEmpty() && text != lastClipText && text.length <= 32768) {
            lastClipText = text
            conn.sendClipboard(text)
        }
    }

    // Gamepad state
    private var gamepadButtons = 0

    override fun onCreateView(
        inflater: LayoutInflater,
        container: ViewGroup?,
        savedInstanceState: Bundle?
    ): View = inflater.inflate(R.layout.activity_streaming, container, false)

    @SuppressLint("ClickableViewAccessibility")
    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        super.onViewCreated(view, savedInstanceState)
        val main = requireActivity() as MainActivity
        main.requestedOrientation = ActivityInfo.SCREEN_ORIENTATION_SENSOR_LANDSCAPE
        conn = main.connectionManager
        settings = AppSettingsStore(requireContext()).load()
        touchSlop = ViewConfiguration.get(requireContext()).scaledTouchSlop.toFloat().coerceAtLeast(10f)

        drawerLayout = view.findViewById(R.id.drawerLayout)
        drawerLayout.setDrawerLockMode(DrawerLayout.LOCK_MODE_LOCKED_CLOSED)
        drawerLayout.addDrawerListener(object : DrawerLayout.SimpleDrawerListener() {
            override fun onDrawerOpened(drawerView: View) {
                drawerLayout.setDrawerLockMode(DrawerLayout.LOCK_MODE_UNLOCKED)
            }

            override fun onDrawerClosed(drawerView: View) {
                drawerLayout.setDrawerLockMode(DrawerLayout.LOCK_MODE_LOCKED_CLOSED)
            }
        })
        streamRoot = view.findViewById(R.id.streamRoot)
        surfaceView = view.findViewById(R.id.streamSurfaceView)
        virtualCursorView = view.findViewById(R.id.virtualCursorView)
        llVirtualMouseBar = view.findViewById(R.id.llVirtualMouseBar)
        etHiddenImeInput = view.findViewById(R.id.etHiddenImeInput)
        overlayEditorView = view.findViewById(R.id.overlayEditorView)
        overlayToolbar = view.findViewById(R.id.overlayEditorToolbar)
        virtualKeyboardView = view.findViewById(R.id.virtualKeyboardInclude)
        tvStreamStats = view.findViewById(R.id.tvStreamStats)
        btnToggleInputMode = view.findViewById(R.id.btnToggleInputMode)
        floatingControlPill = view.findViewById(R.id.floatingControlPill)
        btnMiniExpandPill = view.findViewById(R.id.btnMiniExpandPill)
        switchPreserveCursor = view.findViewById(R.id.switchPreserveCursor)

        isVirtualMouseMode = settings.defaultVirtualMouse
        autoLockOnDisconnect = settings.autoLockOnDisconnect
        applyVirtualMouseUi()

        streamRoot.addOnLayoutChangeListener { _, _, _, _, _, _, _, _, _ ->
            updateSurfaceAspectRatio()
        }
        viewLifecycleOwner.lifecycleScope.launch {
            conn.videoDecoder.videoSizeFlow.collectLatest { (vw, vh) ->
                if (vw > 0 && vh > 0) {
                    currentVideoW = vw
                    currentVideoH = vh
                    updateSurfaceAspectRatio()
                }
            }
        }

        surfaceView.holder.addCallback(this)
        surfaceView.setOnTouchListener { _, event -> handleSurfaceTouch(event) }

        setupHiddenImeInput()
        setupVirtualMouseBar(view)

        VirtualKeyboardController(
            root = virtualKeyboardView,
            targetSurface = etHiddenImeInput,
            onShowIme = { showSoftIme() }
        ) { vk, scan, down, ext ->
            conn.sendKey(vk, scan, down, ext)
        }

        overlayEditorView.setItems(OverlayPresetStore.loadPreset(requireContext()))
        overlayEditorView.listener = object : OverlayEditorView.InputListener {
            override fun onOverlayKey(vk: Int, scan: Int, down: Boolean, ext: Boolean) {
                conn.sendKey(vk, scan, down, ext)
            }

            override fun onEditItemRequested(item: OverlayItem) {
                showEditOverlayItemDialog(item)
            }
        }

        btnToggleInputMode.setOnClickListener {
            isVirtualMouseMode = !isVirtualMouseMode
            applyVirtualMouseUi()
            schedulePillAutoHide()
        }

        view.findViewById<ImageButton>(R.id.btnToggleKeyboard).setOnClickListener {
            val show = virtualKeyboardView.visibility != View.VISIBLE
            virtualKeyboardView.visibility = if (show) View.VISIBLE else View.GONE
            if (show && settings.autoShowIme && !hasExternalKeyboard) {
                showSoftIme()
            }
            schedulePillAutoHide()
        }

        view.findViewById<ImageButton>(R.id.btnToggleOverlay).setOnClickListener {
            overlayVisible = !overlayVisible
            overlayEditorView.editMode = false
            overlayToolbar.visibility = View.GONE
            overlayEditorView.visibility = if (overlayVisible) View.VISIBLE else View.GONE
            schedulePillAutoHide()
        }

        view.findViewById<ImageButton>(R.id.btnOpenQuickDrawer).setOnClickListener {
            drawerLayout.openDrawer(GravityCompat.END)
            schedulePillAutoHide()
        }

        view.findViewById<Button>(R.id.btnCollapsePill).setOnClickListener {
            mainHandler.removeCallbacks(autoHidePillRunnable)
            floatingControlPill.visibility = View.GONE
            btnMiniExpandPill.visibility = View.VISIBLE
        }

        btnMiniExpandPill.setOnClickListener {
            expandFloatingPill()
        }

        schedulePillAutoHide()

        view.findViewById<Button>(R.id.btnAddOverlayKey).setOnClickListener {
            val newItem = OverlayItem(type = OverlayItemType.KEY_BUTTON, label = "Space", normX = 0.5f, normY = 0.5f, vk = 0x20, scan = 0x39)
            overlayEditorView.items.add(newItem)
            overlayEditorView.invalidate()
        }
        view.findViewById<Button>(R.id.btnAddOverlayStick).setOnClickListener {
            val stick = OverlayItem(type = OverlayItemType.WASD_STICK, label = "WASD", normX = 0.2f, normY = 0.7f, radiusDp = 56f)
            overlayEditorView.items.add(stick)
            overlayEditorView.invalidate()
        }
        view.findViewById<Button>(R.id.btnSaveOverlayPreset).setOnClickListener {
            OverlayPresetStore.savePreset(requireContext(), overlayEditorView.items)
            Toast.makeText(requireContext(), "Keymap JSON saved", Toast.LENGTH_SHORT).show()
        }
        view.findViewById<Button>(R.id.btnFinishOverlayEdit).setOnClickListener {
            OverlayPresetStore.savePreset(requireContext(), overlayEditorView.items)
            overlayEditorView.editMode = false
            overlayToolbar.visibility = View.GONE
        }

        setupQuickDrawer(view)
        setupPeripheralsAndClipboard()
        observeStatsAndEvents()
    }

    private fun schedulePillAutoHide() {
        mainHandler.removeCallbacks(autoHidePillRunnable)
        if (::settings.isInitialized && settings.autoHideToolbar && floatingControlPill.visibility == View.VISIBLE) {
            mainHandler.postDelayed(autoHidePillRunnable, 4000L)
        }
    }

    private fun expandFloatingPill() {
        btnMiniExpandPill.visibility = View.GONE
        floatingControlPill.visibility = View.VISIBLE
        schedulePillAutoHide()
    }

    private fun applyVirtualMouseUi() {
        btnToggleInputMode.text = if (isVirtualMouseMode) "虚拟鼠标" else "触屏手势"
        virtualCursorView.visibility = if (isVirtualMouseMode) View.VISIBLE else View.GONE
        llVirtualMouseBar.visibility = if (isVirtualMouseMode) View.VISIBLE else View.GONE
        virtualCursorView.setCursorPosition(cursorX, cursorY)
    }

    @SuppressLint("ClickableViewAccessibility")
    private fun setupVirtualMouseBar(root: View) {
        val btnLeft = root.findViewById<Button>(R.id.btnVMouseLeft)
        btnLeft.setOnTouchListener { v, ev ->
            when (ev.actionMasked) {
                MotionEvent.ACTION_DOWN -> {
                    v.isPressed = true
                    vMouseLeftHeld = true
                    conn.sendMouseButton("left", true, cursorX, cursorY)
                    true
                }
                MotionEvent.ACTION_UP, MotionEvent.ACTION_CANCEL -> {
                    v.isPressed = false
                    vMouseLeftHeld = false
                    conn.sendMouseButton("left", false, cursorX, cursorY)
                    true
                }
                else -> false
            }
        }
        root.findViewById<Button>(R.id.btnVMouseWheelUp).setOnClickListener {
            conn.sendMouseWheel(0, 120, cursorX, cursorY)
        }
        root.findViewById<Button>(R.id.btnVMouseWheelDown).setOnClickListener {
            conn.sendMouseWheel(0, -120, cursorX, cursorY)
        }
        root.findViewById<Button>(R.id.btnVMouseRight).setOnClickListener {
            conn.sendMouseClick(cursorX, cursorY, "right")
        }
    }

    private fun setupHiddenImeInput() {
        resettingImeText = true
        etHiddenImeInput.setText(" ")
        etHiddenImeInput.setSelection(1)
        resettingImeText = false

        etHiddenImeInput.addTextChangedListener(object : TextWatcher {
            override fun beforeTextChanged(s: CharSequence?, start: Int, count: Int, after: Int) {}
            override fun onTextChanged(s: CharSequence?, start: Int, before: Int, count: Int) {}
            override fun afterTextChanged(s: Editable?) {
                if (resettingImeText) return
                val str = s?.toString() ?: ""
                if (str.isEmpty()) {
                    // Backspace deleted sentinel space
                    conn.sendKey(0x08, 0x0E, true, false)
                    conn.sendKey(0x08, 0x0E, false, false)
                } else {
                    val committed = if (str.startsWith(" ")) str.substring(1) else str
                    if (committed.isNotEmpty()) {
                        conn.sendText(committed)
                    }
                }
                resettingImeText = true
                etHiddenImeInput.setText(" ")
                etHiddenImeInput.setSelection(1)
                resettingImeText = false
            }
        })

        etHiddenImeInput.setOnEditorActionListener { _, actionId, _ ->
            if (actionId == EditorInfo.IME_ACTION_DONE || actionId == EditorInfo.IME_ACTION_SEND || actionId == EditorInfo.IME_ACTION_GO) {
                conn.sendKey(0x0D, 0x1C, true, false)
                conn.sendKey(0x0D, 0x1C, false, false)
                true
            } else false
        }
    }

    private fun showSoftIme() {
        etHiddenImeInput.requestFocus()
        val imm = requireContext().getSystemService(Context.INPUT_METHOD_SERVICE) as InputMethodManager
        imm.showSoftInput(etHiddenImeInput, InputMethodManager.SHOW_IMPLICIT)
    }

    private fun setupPeripheralsAndClipboard() {
        inputManager = requireContext().getSystemService(Context.INPUT_SERVICE) as? InputManager
        inputManager?.registerInputDeviceListener(this, mainHandler)
        scanExternalPeripherals(showToast = true)

        clipboardManager = requireContext().getSystemService(Context.CLIPBOARD_SERVICE) as? ClipboardManager
        clipboardManager?.addPrimaryClipChangedListener(clipListener)
        if (settings.clipboardSync) {
            val initial = readLocalClipboard()
            if (initial.isNotEmpty() && initial.length <= 32768) {
                lastClipText = initial
                conn.sendClipboard(initial)
            }
        }
    }

    private fun readLocalClipboard(): String {
        val cm = clipboardManager ?: return ""
        val clip = cm.primaryClip ?: return ""
        if (clip.itemCount <= 0) return ""
        return clip.getItemAt(0).coerceToText(requireContext())?.toString().orEmpty()
    }

    private fun scanExternalPeripherals(showToast: Boolean) {
        if (!settings.autoConnectPeripherals) return
        val im = inputManager ?: return
        var kb = false
        var mouse = false
        var pad = false
        for (id in im.inputDeviceIds) {
            val dev = im.getInputDevice(id) ?: continue
            if (dev.isVirtual) continue
            val src = dev.sources
            val isExt = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) dev.isExternal else true
            if (!isExt) continue
            if ((src and InputDevice.SOURCE_KEYBOARD) == InputDevice.SOURCE_KEYBOARD &&
                dev.keyboardType == InputDevice.KEYBOARD_TYPE_ALPHABETIC
            ) {
                kb = true
            }
            if ((src and InputDevice.SOURCE_MOUSE) == InputDevice.SOURCE_MOUSE) {
                mouse = true
            }
            if ((src and InputDevice.SOURCE_GAMEPAD) == InputDevice.SOURCE_GAMEPAD ||
                (src and InputDevice.SOURCE_JOYSTICK) == InputDevice.SOURCE_JOYSTICK
            ) {
                pad = true
            }
        }
        hasExternalKeyboard = kb
        hasExternalMouse = mouse
        hasExternalGamepad = pad
        if (showToast && (kb || mouse || pad)) {
            val list = mutableListOf<String>()
            if (kb) list.add("外接键盘")
            if (mouse) list.add("外接鼠标")
            if (pad) list.add("手柄")
            Toast.makeText(requireContext(), "已自动接入${list.joinToString(" / ")}", Toast.LENGTH_SHORT).show()
        }
    }

    override fun onInputDeviceAdded(deviceId: Int) {
        if (isAdded) scanExternalPeripherals(showToast = true)
    }

    override fun onInputDeviceRemoved(deviceId: Int) {
        if (isAdded) scanExternalPeripherals(showToast = false)
    }

    override fun onInputDeviceChanged(deviceId: Int) {
        if (isAdded) scanExternalPeripherals(showToast = false)
    }

    private fun setupQuickDrawer(root: View) {
        val main = requireActivity() as MainActivity
        val switchViewOnly = root.findViewById<MaterialSwitch>(R.id.switchViewOnly)
        val switchMuteHost = root.findViewById<MaterialSwitch>(R.id.switchMuteHost)
        val switchSuperScreen = root.findViewById<MaterialSwitch>(R.id.switchDrawerSuperScreen)
        val switchPrivacyScreen = root.findViewById<MaterialSwitch>(R.id.switchPrivacyScreen)
        val switchAutoLock = root.findViewById<MaterialSwitch>(R.id.switchAutoLock)

        switchSuperScreen.isChecked = main.activeConfig.superScreen
        switchMuteHost.isChecked = settings.muteHostOnStream
        switchPrivacyScreen.isChecked = settings.privacyScreenOnStream
        switchPreserveCursor.isChecked = settings.preserveHostCursor
        switchAutoLock.isChecked = autoLockOnDisconnect

        // Apply default host mute, cursor preservation & optional privacy screen on stream entry; ensure input is unlocked
        conn.setViewOnly(false)
        conn.sendPreserveCursor(settings.preserveHostCursor)
        conn.setMuteHost(settings.muteHostOnStream)
        if (settings.privacyScreenOnStream) {
            conn.sendPrivacyScreen(true)
        }

        switchViewOnly.setOnCheckedChangeListener { _, checked -> conn.setViewOnly(checked) }
        switchMuteHost.setOnCheckedChangeListener { _, checked -> conn.setMuteHost(checked) }
        switchPrivacyScreen.setOnCheckedChangeListener { _, checked -> conn.sendPrivacyScreen(checked) }
        switchPreserveCursor.setOnCheckedChangeListener { _, checked -> conn.sendPreserveCursor(checked) }
        switchSuperScreen.setOnCheckedChangeListener { _, checked ->
            if (checked) {
                val (w, h, hz) = getScaledSecondaryMode()
                conn.setSuperScreen(w, h, hz, true, settings.migrateWindowsOnExtended)
                conn.setEncoder(w, h, main.activeConfig.fps, main.activeConfig.bitrateKbps, main.activeConfig.codec)
            } else {
                conn.setSuperScreen(0, 0, 0, false, false)
                conn.setEncoder(0, 0, main.activeConfig.fps, main.activeConfig.bitrateKbps, main.activeConfig.codec)
            }
        }
        switchAutoLock.setOnCheckedChangeListener { _, checked ->
            autoLockOnDisconnect = checked
        }

        root.findViewById<Button>(R.id.btnMigrateWindows).setOnClickListener {
            conn.sendHostAction("migrate_windows")
            drawerLayout.closeDrawer(GravityCompat.END)
        }

        root.findViewById<Button>(R.id.btnShowDesktop).setOnClickListener {
            conn.sendHostAction("show_desktop")
            drawerLayout.closeDrawer(GravityCompat.END)
        }
        root.findViewById<Button>(R.id.btnTaskView).setOnClickListener {
            conn.sendHostAction("task_view")
            drawerLayout.closeDrawer(GravityCompat.END)
        }
        root.findViewById<Button>(R.id.btnSwitchMonitor).setOnClickListener {
            conn.sendHostAction("switch_monitor")
            drawerLayout.closeDrawer(GravityCompat.END)
        }
        root.findViewById<Button>(R.id.btnLockHostNow).setOnClickListener {
            conn.sendHostAction("lock")
            drawerLayout.closeDrawer(GravityCompat.END)
        }
        root.findViewById<Button>(R.id.btnEditOverlayFromDrawer).setOnClickListener {
            drawerLayout.closeDrawer(GravityCompat.END)
            overlayVisible = true
            overlayEditorView.visibility = View.VISIBLE
            overlayEditorView.editMode = true
            overlayToolbar.visibility = View.VISIBLE
        }
        root.findViewById<Button>(R.id.btnRemoteReboot).setOnClickListener {
            confirmHostPowerAction("Remote Reboot", "Reboot the Windows host PC?", "reboot")
        }
        root.findViewById<Button>(R.id.btnRemoteShutdown).setOnClickListener {
            confirmHostPowerAction("Remote Shutdown", "Shut down the Windows host PC?", "shutdown")
        }
        root.findViewById<Button>(R.id.btnExitStream).setOnClickListener {
            userExiting = true
            drawerLayout.closeDrawer(GravityCompat.END)
            conn.sendPrivacyScreen(false)
            conn.setMuteHost(false)
            conn.sendStreamEnable(false)
            if (autoLockOnDisconnect) {
                conn.sendHostAction("lock")
            }
            main.exitStreaming()
        }
    }

    private fun confirmHostPowerAction(title: String, msg: String, cmd: String) {
        AlertDialog.Builder(requireContext())
            .setTitle(title)
            .setMessage(msg)
            .setPositiveButton("Confirm") { _, _ -> conn.sendHostAction(cmd) }
            .setNegativeButton("Cancel", null)
            .show()
    }

    private fun showEditOverlayItemDialog(item: OverlayItem) {
        val labels = KeyCodeMapper.PRESET_KEYS.map { it.first }.toTypedArray()
        AlertDialog.Builder(requireContext())
            .setTitle("Configure ${item.label}")
            .setItems(labels) { _, which ->
                val (label, mapping) = KeyCodeMapper.PRESET_KEYS[which]
                item.label = label
                item.vk = mapping.vk
                item.scan = mapping.scan
                item.ext = mapping.ext
                overlayEditorView.invalidate()
            }
            .setNeutralButton("Delete") { _, _ ->
                overlayEditorView.items.removeAll { it.id == item.id }
                overlayEditorView.invalidate()
            }
            .setNegativeButton("Cancel", null)
            .show()
    }

    private fun updateSurfaceAspectRatio() {
        val rootW = streamRoot.width
        val rootH = streamRoot.height
        if (rootW <= 0 || rootH <= 0 || currentVideoW <= 0 || currentVideoH <= 0) return

        val videoAspect = currentVideoW.toFloat() / currentVideoH.toFloat()
        val rootAspect = rootW.toFloat() / rootH.toFloat()
        val targetW: Int
        val targetH: Int
        if (rootAspect > videoAspect) {
            targetH = rootH
            targetW = (rootH * videoAspect).roundToInt().coerceAtLeast(1)
        } else {
            targetW = rootW
            targetH = (rootW / videoAspect).roundToInt().coerceAtLeast(1)
        }

        val lp = surfaceView.layoutParams as? FrameLayout.LayoutParams
        if (lp == null || lp.width != targetW || lp.height != targetH || lp.gravity != Gravity.CENTER) {
            surfaceView.layoutParams = FrameLayout.LayoutParams(targetW, targetH, Gravity.CENTER)
            virtualCursorView.layoutParams = FrameLayout.LayoutParams(targetW, targetH, Gravity.CENTER)
        }
    }

    private fun getNativeDisplayMode(): Triple<Int, Int, Int> {
        val display = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            requireActivity().display
        } else {
            @Suppress("DEPRECATION")
            requireActivity().windowManager.defaultDisplay
        }
        val mode = display?.mode
        val w = mode?.physicalWidth ?: resources.displayMetrics.widthPixels
        val h = mode?.physicalHeight ?: resources.displayMetrics.heightPixels
        val hz = mode?.refreshRate?.roundToInt() ?: 60
        return Triple(maxOf(w, h), minOf(w, h), hz)
    }

    private fun getScaledSecondaryMode(): Triple<Int, Int, Int> {
        val (nw, nh, hz) = getNativeDisplayMode()
        val scale = if (::settings.isInitialized) settings.secondaryScale.coerceIn(1.0f, 1.5f) else 1.20f
        val sw = ((nw / scale).roundToInt() and 1.inv()).coerceAtLeast(1280)
        val sh = ((nh / scale).roundToInt() and 1.inv()).coerceAtLeast(720)
        return Triple(sw, sh, hz)
    }

    override fun surfaceCreated(holder: SurfaceHolder) {
        val main = requireActivity() as MainActivity
        val cfg = main.activeConfig
        val (nativeW, nativeH, nativeHz) = getNativeDisplayMode()
        conn.videoDecoder.attachSurface(holder.surface, nativeW, nativeH)

        conn.sendStreamEnable(true)
        conn.setViewOnly(false)
        conn.sendPreserveCursor(if (::switchPreserveCursor.isInitialized) switchPreserveCursor.isChecked else settings.preserveHostCursor)
        if (cfg.superScreen) {
            val (sw, sh, _) = getScaledSecondaryMode()
            conn.setSuperScreen(sw, sh, nativeHz, true, settings.migrateWindowsOnExtended)
            conn.setEncoder(sw, sh, cfg.fps, cfg.bitrateKbps, cfg.codec)
        } else {
            conn.setSuperScreen(0, 0, 0, false, false)
            conn.setEncoder(0, 0, cfg.fps, cfg.bitrateKbps, cfg.codec)
        }
    }

    override fun surfaceChanged(holder: SurfaceHolder, format: Int, width: Int, height: Int) {
        conn.videoDecoder.attachSurface(holder.surface, width, height)
    }

    override fun surfaceDestroyed(holder: SurfaceHolder) {
        conn.videoDecoder.detachSurface()
    }

    private fun syncExternalMouseButtons(newMask: Int) {
        val changed = extMouseBtnMask xor newMask
        if ((changed and MotionEvent.BUTTON_PRIMARY) != 0) {
            conn.sendMouseButton("left", (newMask and MotionEvent.BUTTON_PRIMARY) != 0)
        }
        if ((changed and MotionEvent.BUTTON_SECONDARY) != 0) {
            conn.sendMouseButton("right", (newMask and MotionEvent.BUTTON_SECONDARY) != 0)
        }
        if ((changed and MotionEvent.BUTTON_TERTIARY) != 0) {
            conn.sendMouseButton("middle", (newMask and MotionEvent.BUTTON_TERTIARY) != 0)
        }
        extMouseBtnMask = newMask
    }

    private fun handleSurfaceTouch(event: MotionEvent): Boolean {
        val w = surfaceView.width.toFloat().coerceAtLeast(1f)
        val h = surfaceView.height.toFloat().coerceAtLeast(1f)
        val nx = (event.x / w).coerceIn(0f, 1f)
        val ny = (event.y / h).coerceIn(0f, 1f)

        // External Bluetooth/USB Mouse direct passthrough
        if (settings.autoConnectPeripherals && event.getToolType(0) == MotionEvent.TOOL_TYPE_MOUSE) {
            cursorX = nx
            cursorY = ny
            virtualCursorView.setCursorPosition(nx, ny)
            conn.sendMouseMove(nx, ny)
            syncExternalMouseButtons(event.buttonState)
            return true
        }

        // Stylus passthrough
        if (event.getToolType(0) == MotionEvent.TOOL_TYPE_STYLUS) {
            conn.sendMouseMove(nx, ny)
            val isSecondary = (event.buttonState and MotionEvent.BUTTON_STYLUS_PRIMARY) != 0
            when (event.actionMasked) {
                MotionEvent.ACTION_DOWN -> conn.sendMouseButton(if (isSecondary) "right" else "left", true)
                MotionEvent.ACTION_UP, MotionEvent.ACTION_CANCEL -> {
                    conn.sendMouseButton("left", false)
                    conn.sendMouseButton("right", false)
                }
            }
            return true
        }

        if (isVirtualMouseMode) {
            return handleVirtualMouseTouch(event, w, h)
        }

        // Direct Touch Gesture Mode (Native Windows Touch hides cursor during touch & restores on physical mouse; 2-Finger Slide=Scroll from midpoint)
        when (event.actionMasked) {
            MotionEvent.ACTION_DOWN -> {
                mainHandler.removeCallbacks(longPressRunnable)
                downTouchX = event.x
                downTouchY = event.y
                downNormX = nx
                downNormY = ny
                movedBeyondSlop = false
                leftDragActive = false
                longPressFired = false
                multiTouchActive = false
                twoFingerMoved = false
                nativeTouchActive = true
                conn.sendTouch(listOf(TouchPointer(0, "down", nx, ny)))
                mainHandler.postDelayed(longPressRunnable, 420L)
            }
            MotionEvent.ACTION_POINTER_DOWN -> {
                mainHandler.removeCallbacks(longPressRunnable)
                if (nativeTouchActive) {
                    conn.sendTouch(listOf(TouchPointer(0, "cancel", nx, ny)))
                    nativeTouchActive = false
                }
                if (leftDragActive) {
                    conn.sendMouseButton("left", false, nx, ny)
                    leftDragActive = false
                }
                multiTouchActive = true
                twoFingerMoved = false
                if (event.pointerCount >= 2) {
                    lastTwoFingerX = (event.getX(0) + event.getX(1)) / 2f
                    lastTwoFingerY = (event.getY(0) + event.getY(1)) / 2f
                    scrollOriginNormX = (lastTwoFingerX / w).coerceIn(0f, 1f)
                    scrollOriginNormY = (lastTwoFingerY / h).coerceIn(0f, 1f)
                }
            }
            MotionEvent.ACTION_MOVE -> {
                if (event.pointerCount >= 2) {
                    val avgX = (event.getX(0) + event.getX(1)) / 2f
                    val avgY = (event.getY(0) + event.getY(1)) / 2f
                    if (hypot(avgX - lastTwoFingerX, avgY - lastTwoFingerY) > 6f) {
                        twoFingerMoved = true
                    }
                    val dx = ((avgX - lastTwoFingerX) * 2.2f).roundToInt()
                    val dy = ((avgY - lastTwoFingerY) * 2.8f).roundToInt()
                    if (dx != 0 || dy != 0) {
                        conn.sendMouseWheel(dx, dy, scrollOriginNormX, scrollOriginNormY)
                        lastTwoFingerX = avgX
                        lastTwoFingerY = avgY
                    }
                } else if (event.pointerCount == 1 && !multiTouchActive && !longPressFired) {
                    if (!movedBeyondSlop && hypot(event.x - downTouchX, event.y - downTouchY) > touchSlop) {
                        movedBeyondSlop = true
                        mainHandler.removeCallbacks(longPressRunnable)
                    }
                    if (nativeTouchActive) {
                        conn.sendTouch(listOf(TouchPointer(0, "move", nx, ny)))
                    }
                }
            }
            MotionEvent.ACTION_POINTER_UP -> {
                if (event.pointerCount == 2 && multiTouchActive && !twoFingerMoved) {
                    conn.sendMouseClick(scrollOriginNormX, scrollOriginNormY, "right")
                }
            }
            MotionEvent.ACTION_UP, MotionEvent.ACTION_CANCEL -> {
                mainHandler.removeCallbacks(longPressRunnable)
                if (nativeTouchActive) {
                    val act = if (event.actionMasked == MotionEvent.ACTION_UP && !multiTouchActive && !longPressFired) "up" else "cancel"
                    conn.sendTouch(listOf(TouchPointer(0, act, nx, ny)))
                    nativeTouchActive = false
                }
                multiTouchActive = false
            }
        }
        return true
    }

    private fun handleVirtualMouseTouch(event: MotionEvent, w: Float, h: Float): Boolean {
        val preserveCursor = if (::switchPreserveCursor.isInitialized) switchPreserveCursor.isChecked else true
        when (event.actionMasked) {
            MotionEvent.ACTION_DOWN -> {
                mainHandler.removeCallbacks(longPressRunnable)
                lastTouchX = event.x
                lastTouchY = event.y
                downTouchX = event.x
                downTouchY = event.y
                movedBeyondSlop = false
                longPressFired = false
                multiTouchActive = false
                twoFingerMoved = false
                val now = System.currentTimeMillis()
                if (now - lastTapUpTimeMs < 260L) {
                    doubleTapDragActive = true
                    conn.sendMouseButton("left", true, cursorX, cursorY)
                } else {
                    doubleTapDragActive = false
                    mainHandler.postDelayed(longPressRunnable, 420L)
                }
            }
            MotionEvent.ACTION_POINTER_DOWN -> {
                mainHandler.removeCallbacks(longPressRunnable)
                multiTouchActive = true
                twoFingerMoved = false
                if (event.pointerCount >= 2) {
                    lastTwoFingerX = (event.getX(0) + event.getX(1)) / 2f
                    lastTwoFingerY = (event.getY(0) + event.getY(1)) / 2f
                    scrollOriginNormX = (lastTwoFingerX / w).coerceIn(0f, 1f)
                    scrollOriginNormY = (lastTwoFingerY / h).coerceIn(0f, 1f)
                }
            }
            MotionEvent.ACTION_MOVE -> {
                if (event.pointerCount >= 2) {
                    val avgX = (event.getX(0) + event.getX(1)) / 2f
                    val avgY = (event.getY(0) + event.getY(1)) / 2f
                    if (hypot(avgX - lastTwoFingerX, avgY - lastTwoFingerY) > 6f) {
                        twoFingerMoved = true
                    }
                    val dx = ((avgX - lastTwoFingerX) * 2.2f).roundToInt()
                    val dy = ((avgY - lastTwoFingerY) * 2.8f).roundToInt()
                    if (dx != 0 || dy != 0) {
                        conn.sendMouseWheel(dx, dy, scrollOriginNormX, scrollOriginNormY)
                        lastTwoFingerX = avgX
                        lastTwoFingerY = avgY
                    }
                } else if (event.pointerCount == 1 && !multiTouchActive && !longPressFired) {
                    if (hypot(event.x - downTouchX, event.y - downTouchY) > touchSlop) {
                        movedBeyondSlop = true
                        mainHandler.removeCallbacks(longPressRunnable)
                    }
                    val dx = (event.x - lastTouchX) / w
                    val dy = (event.y - lastTouchY) / h
                    cursorX = (cursorX + dx * 1.4f).coerceIn(0f, 1f)
                    cursorY = (cursorY + dy * 1.4f).coerceIn(0f, 1f)
                    virtualCursorView.setCursorPosition(cursorX, cursorY)
                    if (vMouseLeftHeld || doubleTapDragActive || !preserveCursor) {
                        conn.sendMouseMove(cursorX, cursorY)
                    }
                    lastTouchX = event.x
                    lastTouchY = event.y
                }
            }
            MotionEvent.ACTION_POINTER_UP -> {
                if (event.pointerCount == 2 && multiTouchActive && !twoFingerMoved) {
                    conn.sendMouseClick(cursorX, cursorY, "right")
                }
            }
            MotionEvent.ACTION_UP, MotionEvent.ACTION_CANCEL -> {
                mainHandler.removeCallbacks(longPressRunnable)
                if (doubleTapDragActive) {
                    conn.sendMouseButton("left", false, cursorX, cursorY)
                    doubleTapDragActive = false
                    lastTapUpTimeMs = 0L
                } else if (event.actionMasked == MotionEvent.ACTION_UP &&
                    !multiTouchActive && !longPressFired && !movedBeyondSlop
                ) {
                    conn.sendMouseClick(cursorX, cursorY, "left")
                    lastTapUpTimeMs = System.currentTimeMillis()
                }
                multiTouchActive = false
            }
        }
        return true
    }

    fun handleHardwareKeyEvent(event: KeyEvent): Boolean {
        if (!settings.autoConnectPeripherals) return false
        val isGamepad = (event.source and InputDevice.SOURCE_GAMEPAD) == InputDevice.SOURCE_GAMEPAD ||
            (event.source and InputDevice.SOURCE_JOYSTICK) == InputDevice.SOURCE_JOYSTICK

        if (isGamepad) {
            val mask = gamepadButtonMask(event.keyCode)
            if (mask != 0) {
                gamepadButtons = if (event.action == KeyEvent.ACTION_DOWN) {
                    gamepadButtons or mask
                } else {
                    gamepadButtons and mask.inv()
                }
                conn.sendGamepad(gamepadButtons, 0, 0, 0, 0, 0, 0)
                return true
            }
        }

        val mapping = KeyCodeMapper.fromAndroidKeyCode(event.keyCode) ?: return false
        val down = event.action == KeyEvent.ACTION_DOWN
        if (event.action == KeyEvent.ACTION_DOWN || event.action == KeyEvent.ACTION_UP) {
            conn.sendKey(mapping.vk, mapping.scan, down, mapping.ext)
            return true
        }
        return false
    }

    fun handleGenericMotion(event: MotionEvent): Boolean {
        if (!settings.autoConnectPeripherals) return false
        if ((event.source and InputDevice.SOURCE_MOUSE) == InputDevice.SOURCE_MOUSE) {
            val w = surfaceView.width.toFloat().coerceAtLeast(1f)
            val h = surfaceView.height.toFloat().coerceAtLeast(1f)
            val loc = IntArray(2)
            surfaceView.getLocationOnScreen(loc)
            val nx = ((event.rawX - loc[0]) / w).coerceIn(0f, 1f)
            val ny = ((event.rawY - loc[1]) / h).coerceIn(0f, 1f)
            when (event.actionMasked) {
                MotionEvent.ACTION_HOVER_MOVE -> {
                    cursorX = nx
                    cursorY = ny
                    virtualCursorView.setCursorPosition(nx, ny)
                    conn.sendMouseMove(nx, ny)
                    return true
                }
                MotionEvent.ACTION_SCROLL -> {
                    val vScroll = (event.getAxisValue(MotionEvent.AXIS_VSCROLL) * 120f).roundToInt()
                    val hScroll = (event.getAxisValue(MotionEvent.AXIS_HSCROLL) * 120f).roundToInt()
                    if (vScroll != 0 || hScroll != 0) {
                        conn.sendMouseWheel(hScroll, vScroll)
                    }
                    return true
                }
                MotionEvent.ACTION_BUTTON_PRESS, MotionEvent.ACTION_BUTTON_RELEASE -> {
                    conn.sendMouseMove(nx, ny)
                    syncExternalMouseButtons(event.buttonState)
                    return true
                }
            }
        }

        if ((event.source and InputDevice.SOURCE_JOYSTICK) == InputDevice.SOURCE_JOYSTICK &&
            event.action == MotionEvent.ACTION_MOVE
        ) {
            val lx = (event.getAxisValue(MotionEvent.AXIS_X) * 32767f).roundToInt().coerceIn(-32768, 32767)
            val ly = (event.getAxisValue(MotionEvent.AXIS_Y) * -32767f).roundToInt().coerceIn(-32768, 32767)
            val rx = (event.getAxisValue(MotionEvent.AXIS_Z) * 32767f).roundToInt().coerceIn(-32768, 32767)
            val ry = (event.getAxisValue(MotionEvent.AXIS_RZ) * -32767f).roundToInt().coerceIn(-32768, 32767)
            val lt = (event.getAxisValue(MotionEvent.AXIS_LTRIGGER) * 255f).roundToInt().coerceIn(0, 255)
            val rt = (event.getAxisValue(MotionEvent.AXIS_RTRIGGER) * 255f).roundToInt().coerceIn(0, 255)
            conn.sendGamepad(gamepadButtons, lx, ly, rx, ry, lt, rt)
            return true
        }
        return false
    }

    private fun gamepadButtonMask(keyCode: Int): Int = when (keyCode) {
        KeyEvent.KEYCODE_BUTTON_A -> 0x1000
        KeyEvent.KEYCODE_BUTTON_B -> 0x2000
        KeyEvent.KEYCODE_BUTTON_X -> 0x4000
        KeyEvent.KEYCODE_BUTTON_Y -> 0x8000
        KeyEvent.KEYCODE_BUTTON_L1 -> 0x0100
        KeyEvent.KEYCODE_BUTTON_R1 -> 0x0200
        KeyEvent.KEYCODE_BUTTON_START -> 0x0010
        KeyEvent.KEYCODE_BUTTON_SELECT -> 0x0020
        KeyEvent.KEYCODE_BUTTON_THUMBL -> 0x0040
        KeyEvent.KEYCODE_BUTTON_THUMBR -> 0x0080
        else -> 0
    }

    private fun observeStatsAndEvents() {
        val main = requireActivity() as MainActivity
        viewLifecycleOwner.lifecycleScope.launch {
            conn.stats.collectLatest { st ->
                if (st != null) {
                    tvStreamStats.text = "${st.fps} FPS | ${st.rttMs}ms | ${st.codec.uppercase()}"
                }
            }
        }
        viewLifecycleOwner.lifecycleScope.launch {
            conn.clipboardEvents.collect { remoteText ->
                if (settings.clipboardSync && remoteText.isNotEmpty() && remoteText != lastClipText) {
                    lastClipText = remoteText
                    clipboardManager?.setPrimaryClip(ClipData.newPlainText("XiaobaoDesk", remoteText))
                }
            }
        }
        viewLifecycleOwner.lifecycleScope.launch {
            conn.imeHintEvents.collect { editing ->
                if (editing && settings.autoShowIme && !hasExternalKeyboard) {
                    showSoftIme()
                }
            }
        }
        viewLifecycleOwner.lifecycleScope.launch {
            conn.noticeEvents.collect { msg ->
                Toast.makeText(requireContext(), msg, Toast.LENGTH_SHORT).show()
            }
        }
        viewLifecycleOwner.lifecycleScope.launch {
            conn.connectionState.collectLatest { state ->
                when (state) {
                    ConnectionState.CONNECTED -> {
                        if (reconnectAttempt > 0) {
                            reconnectAttempt = 0
                            val cfg = main.activeConfig
                            val (_, _, nativeHz) = getNativeDisplayMode()
                            conn.sendStreamEnable(true)
                            conn.setViewOnly(false)
                            conn.sendPreserveCursor(if (::switchPreserveCursor.isInitialized) switchPreserveCursor.isChecked else settings.preserveHostCursor)
                            if (cfg.superScreen) {
                                val (sw, sh, _) = getScaledSecondaryMode()
                                conn.setSuperScreen(sw, sh, nativeHz, true, settings.migrateWindowsOnExtended)
                                conn.setEncoder(sw, sh, cfg.fps, cfg.bitrateKbps, cfg.codec)
                            } else {
                                conn.setSuperScreen(0, 0, 0, false, false)
                                conn.setEncoder(0, 0, cfg.fps, cfg.bitrateKbps, cfg.codec)
                            }
                        }
                    }
                    ConnectionState.ERROR, ConnectionState.DISCONNECTED -> {
                        if (!userExiting && reconnectAttempt < 3) {
                            reconnectAttempt++
                            tvStreamStats.text = "断线重连中 ($reconnectAttempt/3)..."
                            delay(2000L)
                            if (!userExiting && isAdded) {
                                conn.connect(main.activeConfig)
                            }
                        }
                    }
                    else -> {}
                }
            }
        }
    }

    override fun onDestroyView() {
        mainHandler.removeCallbacks(autoHidePillRunnable)
        mainHandler.removeCallbacks(longPressRunnable)
        inputManager?.unregisterInputDeviceListener(this)
        clipboardManager?.removePrimaryClipChangedListener(clipListener)
        conn.sendPrivacyScreen(false)
        conn.setMuteHost(false)
        super.onDestroyView()
        activity?.requestedOrientation = ActivityInfo.SCREEN_ORIENTATION_UNSPECIFIED
        if (autoLockOnDisconnect) {
            conn.sendHostAction("lock")
        }
    }
}
