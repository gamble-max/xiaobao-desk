package com.xiaobao.desk.core

import java.io.DataInputStream
import java.io.EOFException
import java.io.OutputStream
import java.nio.ByteBuffer
import java.nio.ByteOrder

object Protocol {
    const val CHANNEL_CTRL: Byte = 0x01
    const val CHANNEL_VIDEO: Byte = 0x02
    const val CHANNEL_AUDIO: Byte = 0x03
    const val CHANNEL_INPUT: Byte = 0x04
    const val CHANNEL_TERM: Byte = 0x05
    const val CHANNEL_FILE: Byte = 0x06

    const val VIDEO_FLAG_KEYFRAME: Int = 0x01
    const val VIDEO_FLAG_HEVC: Int = 0x02
    const val VIDEO_FLAG_CODEC_CONFIG: Int = 0x04

    const val TERM_OPEN: Byte = 0x01
    const val TERM_STDIN: Byte = 0x02
    const val TERM_STDOUT: Byte = 0x03
    const val TERM_RESIZE: Byte = 0x04
    const val TERM_CLOSE: Byte = 0x05

    const val FILE_LIST_DIR: Byte = 0x01
    const val FILE_LIST_RESP: Byte = 0x02
    const val FILE_DOWNLOAD_REQ: Byte = 0x03
    const val FILE_UPLOAD_START: Byte = 0x04
    const val FILE_CHUNK: Byte = 0x05
    const val FILE_DONE: Byte = 0x06
    const val FILE_ERR: Byte = 0x07

    const val MAX_FRAME_BYTES = 16 * 1024 * 1024

    fun writeFrame(out: OutputStream, channel: Byte, payload: ByteArray) {
        val header = ByteBuffer.allocate(5).order(ByteOrder.BIG_ENDIAN)
        header.put(channel)
        header.putInt(payload.size)
        out.write(header.array())
        if (payload.isNotEmpty()) {
            out.write(payload)
        }
        out.flush()
    }

    fun readFrame(input: DataInputStream): Pair<Byte, ByteArray> {
        val header = ByteArray(5)
        input.readFully(header)
        val channel = header[0]
        val len = ByteBuffer.wrap(header, 1, 4).order(ByteOrder.BIG_ENDIAN).int
        if (len < 0 || len > MAX_FRAME_BYTES) {
            throw EOFException("Invalid frame length: $len")
        }
        val payload = ByteArray(len)
        if (len > 0) {
            input.readFully(payload)
        }
        return channel to payload
    }
}

enum class ConnectionState {
    DISCONNECTED,
    CONNECTING,
    AUTHENTICATING,
    CONNECTED,
    ERROR
}

enum class ConnectionPath {
    NONE,
    DIRECT_IPV6,
    DIRECT_LAN,
    RELAY_HUB
}

data class ConnectionConfig(
    val hubHost: String = "",
    val hubPort: Int = 21116,
    val hubCertSha256: String = "",
    val hostId: String = "default-host",
    val hostCertSha256: String = "",
    val password: String = "",
    val totpCode: String = "",
    val directHostOverride: String = "",
    val directPort: Int = 21118,
    val width: Int = 1920,
    val height: Int = 1080,
    val fps: Int = 60,
    val bitrateKbps: Int = 12000,
    val codec: String = "hevc",
    val superScreen: Boolean = false,
    val migrateWindows: Boolean = true,
    val startStreamOnConnect: Boolean = true
)

data class TouchPointer(
    val id: Int,
    val action: String, // "down", "move", "up", "cancel"
    val x: Float,
    val y: Float
)

data class StreamStats(
    val fps: Int = 0,
    val bitrateKbps: Int = 0,
    val rttMs: Long = 0,
    val path: ConnectionPath = ConnectionPath.NONE,
    val codec: String = "hevc"
)

data class FileEntry(
    val name: String,
    val isDir: Boolean,
    val size: Long,
    val mtime: Long
)

data class TransferProgress(
    val id: String,
    val fileName: String,
    val bytesTransferred: Long,
    val totalBytes: Long,
    val isUpload: Boolean,
    val done: Boolean = false,
    val error: String? = null
)
