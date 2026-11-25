package com.xiaobao.desk.ui

import android.content.Context
import android.view.View
import android.view.inputmethod.InputMethodManager
import android.widget.Button
import android.widget.HorizontalScrollView
import android.widget.LinearLayout
import com.xiaobao.desk.R

class VirtualKeyboardController(
    private val root: View,
    private val targetSurface: View,
    private val onShowIme: (() -> Unit)? = null,
    private val onKeyEvent: (vk: Int, scan: Int, down: Boolean, ext: Boolean) -> Unit
) {
    private var ctrlActive = false
    private var altActive = false
    private var winActive = false

    init {
        bindTapKey(R.id.keyEsc, 0x1B, 0x01, false)
        bindTapKey(R.id.keyTab, 0x09, 0x0F, false)
        bindTapKey(R.id.keyDel, 0x2E, 0x53, true)
        bindTapKey(R.id.keyBackspace, 0x08, 0x0E, false)
        bindTapKey(R.id.keyEnter, 0x0D, 0x1C, false)
        bindTapKey(R.id.keyLeft, 0x25, 0x4B, true)
        bindTapKey(R.id.keyUp, 0x26, 0x48, true)
        bindTapKey(R.id.keyDown, 0x28, 0x50, true)
        bindTapKey(R.id.keyRight, 0x27, 0x4D, true)

        val btnCtrl = root.findViewById<Button>(R.id.keyCtrl)
        btnCtrl.setOnClickListener {
            ctrlActive = !ctrlActive
            btnCtrl.isSelected = ctrlActive
            onKeyEvent(0xA2, 0x1D, ctrlActive, false)
        }

        val btnAlt = root.findViewById<Button>(R.id.keyAlt)
        btnAlt.setOnClickListener {
            altActive = !altActive
            btnAlt.isSelected = altActive
            onKeyEvent(0xA4, 0x38, altActive, false)
        }

        val btnWin = root.findViewById<Button>(R.id.keyWin)
        btnWin.setOnClickListener {
            winActive = !winActive
            btnWin.isSelected = winActive
            onKeyEvent(0x5B, 0x5B, winActive, true)
        }

        val fnContainer = root.findViewById<HorizontalScrollView>(R.id.rowFnContainer)
        val btnFn = root.findViewById<Button>(R.id.keyFn)
        btnFn.setOnClickListener {
            val show = fnContainer.visibility != View.VISIBLE
            fnContainer.visibility = if (show) View.VISIBLE else View.GONE
            btnFn.isSelected = show
        }

        val fnRow = root.findViewById<LinearLayout>(R.id.rowFnKeys)
        val density = root.resources.displayMetrics.density
        for (i in 1..12) {
            val btn = Button(root.context).apply {
                text = "F$i"
                textSize = 11f
                isAllCaps = false
                setTextColor(root.context.getColor(R.color.xb_text_primary))
                setBackgroundResource(R.drawable.bg_keycap)
                setPadding(0, 0, 0, 0)
                val lp = LinearLayout.LayoutParams((44 * density).toInt(), (34 * density).toInt())
                lp.marginEnd = (4 * density).toInt()
                layoutParams = lp
                val vk = 0x70 + (i - 1)
                val scan = 0x3B + (i - 1)
                setOnClickListener {
                    onKeyEvent(vk, scan, true, false)
                    onKeyEvent(vk, scan, false, false)
                }
            }
            fnRow.addView(btn)
        }

        root.findViewById<Button>(R.id.keyShowIme).setOnClickListener {
            if (onShowIme != null) {
                onShowIme.invoke()
            } else {
                targetSurface.requestFocus()
                val imm = root.context.getSystemService(Context.INPUT_METHOD_SERVICE) as InputMethodManager
                imm.showSoftInput(targetSurface, InputMethodManager.SHOW_FORCED)
            }
        }
    }

    private fun bindTapKey(viewId: Int, vk: Int, scan: Int, ext: Boolean) {
        root.findViewById<Button>(viewId).setOnClickListener {
            onKeyEvent(vk, scan, true, ext)
            onKeyEvent(vk, scan, false, ext)
        }
    }
}
