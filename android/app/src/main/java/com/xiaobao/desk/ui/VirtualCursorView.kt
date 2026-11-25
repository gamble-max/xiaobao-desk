package com.xiaobao.desk.ui

import android.content.Context
import android.graphics.Canvas
import android.graphics.Color
import android.graphics.Paint
import android.graphics.Path
import android.util.AttributeSet
import android.view.View

class VirtualCursorView @JvmOverloads constructor(
    context: Context,
    attrs: AttributeSet? = null
) : View(context, attrs) {

    private var normX = 0.5f
    private var normY = 0.5f

    private val fillPaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
        color = Color.WHITE
        style = Paint.Style.FILL
    }
    private val strokePaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
        color = Color.BLACK
        style = Paint.Style.STROKE
        strokeWidth = 2.5f * resources.displayMetrics.density
        strokeJoin = Paint.Join.ROUND
    }
    private val path = Path()

    fun setCursorPosition(nx: Float, ny: Float) {
        normX = nx.coerceIn(0f, 1f)
        normY = ny.coerceIn(0f, 1f)
        invalidate()
    }

    override fun onDraw(canvas: Canvas) {
        super.onDraw(canvas)
        if (width <= 0 || height <= 0) return
        val x = normX * width
        val y = normY * height
        val s = resources.displayMetrics.density * 1.15f

        path.reset()
        path.moveTo(x, y)
        path.lineTo(x, y + 18f * s)
        path.lineTo(x + 4.5f * s, y + 13.5f * s)
        path.lineTo(x + 8.5f * s, y + 20f * s)
        path.lineTo(x + 11.5f * s, y + 18.5f * s)
        path.lineTo(x + 7.5f * s, y + 12f * s)
        path.lineTo(x + 13.5f * s, y + 12f * s)
        path.close()

        canvas.drawPath(path, strokePaint)
        canvas.drawPath(path, fillPaint)
    }
}
