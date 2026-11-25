package com.xiaobao.desk.core

import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.asSharedFlow
import org.json.JSONObject

class TerminalClient(
    private val sendFrame: (channel: Byte, payload: ByteArray) -> Unit
) {
    private val _outputFlow = MutableSharedFlow<ByteArray>(extraBufferCapacity = 256)
    val outputFlow: SharedFlow<ByteArray> = _outputFlow.asSharedFlow()

    private val _textFlow = MutableSharedFlow<String>(extraBufferCapacity = 256)
    val textFlow: SharedFlow<String> = _textFlow.asSharedFlow()

    fun open(cols: Int = 120, rows: Int = 36) {
        val json = JSONObject().apply {
            put("cols", cols)
            put("rows", rows)
        }.toString().toByteArray(Charsets.UTF_8)
        sendTermFrame(Protocol.TERM_OPEN, json)
    }

    fun sendStdin(data: ByteArray) {
        if (data.isEmpty()) return
        sendTermFrame(Protocol.TERM_STDIN, data)
    }

    fun sendCommand(text: String) {
        sendStdin(text.toByteArray(Charsets.UTF_8))
    }

    fun resize(cols: Int, rows: Int) {
        val json = JSONObject().apply {
            put("cols", cols)
            put("rows", rows)
        }.toString().toByteArray(Charsets.UTF_8)
        sendTermFrame(Protocol.TERM_RESIZE, json)
    }

    fun close() {
        sendTermFrame(Protocol.TERM_CLOSE, ByteArray(0))
    }

    internal fun onTermFrame(subcmd: Byte, payload: ByteArray) {
        when (subcmd) {
            Protocol.TERM_STDOUT -> {
                _outputFlow.tryEmit(payload)
                _textFlow.tryEmit(String(payload, Charsets.UTF_8))
            }
        }
    }

    private fun sendTermFrame(subcmd: Byte, body: ByteArray) {
        val payload = ByteArray(1 + body.size)
        payload[0] = subcmd
        if (body.isNotEmpty()) {
            System.arraycopy(body, 0, payload, 1, body.size)
        }
        sendFrame(Protocol.CHANNEL_TERM, payload)
    }
}
