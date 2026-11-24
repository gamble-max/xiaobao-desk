package com.xiaobao.desk.ui

import android.content.Context
import android.graphics.Canvas
import android.graphics.Color
import android.graphics.Paint
import android.util.AttributeSet
import android.view.MotionEvent
import android.view.View
import kotlin.math.hypot

class OverlayEditorView @JvmOverloads constructor(
    context: Context,
    attrs: AttributeSet? = null
) : View(context, attrs) {

    interface InputListener {
        fun onOverlayKey(vk: Int, scan: Int, down: Boolean, ext: Boolean)
        fun onEditItemRequested(item: OverlayItem)
    }

    var listener: InputListener? = null
    var editMode: Boolean = false
        set(value) {
            field = value
            activePointers.clear()
            stickKnobOffsets.clear()
            invalidate()
        }

    val items: MutableList<OverlayItem> = mutableListOf()

    private val density = resources.displayMetrics.density
    private val activePointers = mutableMapOf<Int, OverlayItem>()
    private val stickKnobOffsets = mutableMapOf<String, Pair<Float, Float>>()
    private val activeWasdKeys = mutableMapOf<String, MutableSet<Int>>()

    private var dragItem: OverlayItem? = null
    private var resizingItem: OverlayItem? = null
    private var dragStartX = 0f
    private var dragStartY = 0f
    private var itemMoved = false

    private val bgPaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
        color = Color.argb(140, 30, 41, 59)
        style = Paint.Style.FILL
    }
    private val activeBgPaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
        color = Color.argb(180, 56, 189, 248)
        style = Paint.Style.FILL
    }
    private val strokePaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
        color = Color.argb(210, 56, 189, 248)
        style = Paint.Style.STROKE
        strokeWidth = 2f * density
    }
    private val editStrokePaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
        color = Color.argb(240, 251, 191, 36)
        style = Paint.Style.STROKE
        strokeWidth = 2.5f * density
    }
    private val knobPaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
        color = Color.argb(200, 56, 189, 248)
        style = Paint.Style.FILL
    }
    private val textPaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
        color = Color.WHITE
        textSize = 13f * density
        textAlign = Paint.Align.CENTER
    }

    fun setItems(newItems: List<OverlayItem>) {
        items.clear()
        items.addAll(newItems)
        invalidate()
    }

    override fun onDraw(canvas: Canvas) {
        super.onDraw(canvas)
        val w = width.toFloat().coerceAtLeast(1f)
        val h = height.toFloat().coerceAtLeast(1f)

        for (item in items) {
            val cx = item.normX * w
            val cy = item.normY * h
            val r = item.radiusDp * density
            val isPressed = activePointers.values.any { it.id == item.id }

            canvas.drawCircle(cx, cy, r, if (isPressed) activeBgPaint else bgPaint)
            canvas.drawCircle(cx, cy, r, if (editMode) editStrokePaint else strokePaint)

            if (item.type == OverlayItemType.WASD_STICK) {
                val offset = stickKnobOffsets[item.id] ?: (0f to 0f)
                canvas.drawCircle(cx + offset.first, cy + offset.second, r * 0.42f, knobPaint)
            } else {
                val textY = cy - (textPaint.descent() + textPaint.ascent()) / 2f
                canvas.drawText(item.label, cx, textY, textPaint)
            }

            if (editMode) {
                // Draw resize handle at bottom-right of circle
                val hx = cx + r * 0.72f
                val hy = cy + r * 0.72f
                canvas.drawCircle(hx, hy, 8f * density, editStrokePaint)
            }
        }
    }

    override fun onTouchEvent(event: MotionEvent): Boolean {
        if (visibility != VISIBLE) return false
        val w = width.toFloat().coerceAtLeast(1f)
        val h = height.toFloat().coerceAtLeast(1f)

        if (editMode) {
            return handleEditTouch(event, w, h)
        }
        return handlePlayTouch(event, w, h)
    }

    private fun handleEditTouch(event: MotionEvent, w: Float, h: Float): Boolean {
        val x = event.x
        val y = event.y
        when (event.actionMasked) {
            MotionEvent.ACTION_DOWN -> {
                dragStartX = x
                dragStartY = y
                itemMoved = false
                resizingItem = items.lastOrNull { item ->
                    val cx = item.normX * w
                    val cy = item.normY * h
                    val r = item.radiusDp * density
                    hypot(x - (cx + r * 0.72f), y - (cy + r * 0.72f)) <= 18f * density
                }
                if (resizingItem == null) {
                    dragItem = findHitItem(x, y, w, h)
                }
                return resizingItem != null || dragItem != null
            }
            MotionEvent.ACTION_MOVE -> {
                resizingItem?.let { item ->
                    val cx = item.normX * w
                    val cy = item.normY * h
                    val dist = hypot(x - cx, y - cy) / density
                    item.radiusDp = dist.coerceIn(20f, 96f)
                    itemMoved = true
                    invalidate()
                    return true
                }
                dragItem?.let { item ->
                    if (hypot(x - dragStartX, y - dragStartY) > 6f * density) {
                        itemMoved = true
                    }
                    item.normX = (x / w).coerceIn(0.05f, 0.95f)
                    item.normY = (y / h).coerceIn(0.08f, 0.92f)
                    invalidate()
                    return true
                }
            }
            MotionEvent.ACTION_UP, MotionEvent.ACTION_CANCEL -> {
                if (!itemMoved && dragItem != null) {
                    listener?.onEditItemRequested(dragItem!!)
                }
                dragItem = null
                resizingItem = null
                return true
            }
        }
        return false
    }

    private fun handlePlayTouch(event: MotionEvent, w: Float, h: Float): Boolean {
        val actionIndex = event.actionIndex
        val pointerId = event.getPointerId(actionIndex)

        when (event.actionMasked) {
            MotionEvent.ACTION_DOWN, MotionEvent.ACTION_POINTER_DOWN -> {
                val x = event.getX(actionIndex)
                val y = event.getY(actionIndex)
                val hit = findHitItem(x, y, w, h) ?: return false
                activePointers[pointerId] = hit
                if (hit.type == OverlayItemType.KEY_BUTTON) {
                    listener?.onOverlayKey(hit.vk, hit.scan, true, hit.ext)
                } else {
                    updateWasdStick(hit, x, y, w, h)
                }
                invalidate()
                return true
            }
            MotionEvent.ACTION_MOVE -> {
                var handled = false
                for (i in 0 until event.pointerCount) {
                    val pid = event.getPointerId(i)
                    val item = activePointers[pid] ?: continue
                    handled = true
                    if (item.type == OverlayItemType.WASD_STICK) {
                        updateWasdStick(item, event.getX(i), event.getY(i), w, h)
                    }
                }
                if (handled) invalidate()
                return handled
            }
            MotionEvent.ACTION_UP, MotionEvent.ACTION_POINTER_UP, MotionEvent.ACTION_CANCEL -> {
                val item = activePointers.remove(pointerId) ?: return false
                if (item.type == OverlayItemType.KEY_BUTTON) {
                    listener?.onOverlayKey(item.vk, item.scan, false, item.ext)
                } else {
                    releaseWasdStick(item)
                }
                invalidate()
                return true
            }
        }
        return false
    }

    private fun findHitItem(x: Float, y: Float, w: Float, h: Float): OverlayItem? {
        return items.lastOrNull { item ->
            val cx = item.normX * w
            val cy = item.normY * h
            val r = item.radiusDp * density
            hypot(x - cx, y - cy) <= r
        }
    }

    private fun updateWasdStick(item: OverlayItem, x: Float, y: Float, w: Float, h: Float) {
        val cx = item.normX * w
        val cy = item.normY * h
        val maxR = item.radiusDp * density
        var dx = x - cx
        var dy = y - cy
        val dist = hypot(dx, dy)
        if (dist > maxR && dist > 0f) {
            dx = dx / dist * maxR
            dy = dy / dist * maxR
        }
        stickKnobOffsets[item.id] = dx to dy

        val nx = dx / maxR
        val ny = dy / maxR
        val threshold = 0.35f
        val desired = mutableSetOf<Int>()
        if (ny < -threshold) desired.add(0x57) // W
        if (ny > threshold) desired.add(0x53)  // S
        if (nx < -threshold) desired.add(0x41) // A
        if (nx > threshold) desired.add(0x44)  // D

        val current = activeWasdKeys.getOrPut(item.id) { mutableSetOf() }
        for (vk in current - desired) {
            listener?.onOverlayKey(vk, 0, false, false)
        }
        for (vk in desired - current) {
            listener?.onOverlayKey(vk, 0, true, false)
        }
        current.clear()
        current.addAll(desired)
    }

    private fun releaseWasdStick(item: OverlayItem) {
        stickKnobOffsets.remove(item.id)
        val current = activeWasdKeys.remove(item.id) ?: return
        for (vk in current) {
            listener?.onOverlayKey(vk, 0, false, false)
        }
    }
}
