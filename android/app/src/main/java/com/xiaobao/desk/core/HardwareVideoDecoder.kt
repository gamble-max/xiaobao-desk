package com.xiaobao.desk.core

import android.media.MediaCodec
import android.media.MediaFormat
import android.os.Build
import android.util.Log
import android.view.Surface
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import java.nio.ByteBuffer

class HardwareVideoDecoder {
    private val tag = "HardwareVideoDecoder"
    private val lock = Any()

    private val _videoSizeFlow = MutableStateFlow(1920 to 1080)
    val videoSizeFlow: StateFlow<Pair<Int, Int>> = _videoSizeFlow.asStateFlow()

    private var codec: MediaCodec? = null
    private var surface: Surface? = null
    private var currentMime: String = ""
    private var currentWidth: Int = 1920
    private var currentHeight: Int = 1080
    private var csdBuffer: ByteArray? = null
    private var configured = false

    fun attachSurface(newSurface: Surface?, width: Int = 1920, height: Int = 1080) {
        synchronized(lock) {
            if (surface === newSurface && configured) return
            releaseCodecLocked()
            surface = newSurface
            if (width > 0) currentWidth = width
            if (height > 0) currentHeight = height
            if (newSurface != null && currentMime.isNotEmpty()) {
                initCodecLocked(currentMime, csdBuffer)
            }
        }
    }

    fun detachSurface() {
        synchronized(lock) {
            releaseCodecLocked()
            surface = null
        }
    }

    fun onVideoFrame(ptsUs: Long, flags: Int, nalData: ByteArray) {
        if (nalData.isEmpty()) return
        val isHevc = (flags and Protocol.VIDEO_FLAG_HEVC) != 0
        val isConfig = (flags and Protocol.VIDEO_FLAG_CODEC_CONFIG) != 0
        val isKeyframe = (flags and Protocol.VIDEO_FLAG_KEYFRAME) != 0
        val mime = if (isHevc) MediaFormat.MIMETYPE_VIDEO_HEVC else MediaFormat.MIMETYPE_VIDEO_AVC

        synchronized(lock) {
            if (mime != currentMime) {
                currentMime = mime
                csdBuffer = null
                releaseCodecLocked()
            }

            if (isConfig) {
                csdBuffer = nalData.copyOf()
                if (surface != null && !configured) {
                    initCodecLocked(mime, csdBuffer)
                }
                if (configured) {
                    queueInputLocked(ptsUs, nalData, MediaCodec.BUFFER_FLAG_CODEC_CONFIG)
                    drainOutputLocked()
                }
                return
            }

            if (!configured) {
                if (surface == null) return
                initCodecLocked(mime, csdBuffer)
            }

            val mcFlags = if (isKeyframe) MediaCodec.BUFFER_FLAG_KEY_FRAME else 0
            queueInputLocked(ptsUs, nalData, mcFlags)
            drainOutputLocked()
        }
    }

    private fun initCodecLocked(mime: String, csd: ByteArray?) {
        val targetSurface = surface ?: return
        try {
            val mc = MediaCodec.createDecoderByType(mime)
            val format = MediaFormat.createVideoFormat(mime, currentWidth, currentHeight).apply {
                if (csd != null && csd.isNotEmpty()) {
                    setByteBuffer("csd-0", ByteBuffer.wrap(csd))
                }
                setInteger(MediaFormat.KEY_PRIORITY, 0)
                setInteger(MediaFormat.KEY_OPERATING_RATE, Short.MAX_VALUE.toInt())
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
                    setInteger(MediaFormat.KEY_LOW_LATENCY, 1)
                }
            }
            mc.configure(format, targetSurface, null, 0)
            mc.start()
            codec = mc
            configured = true
        } catch (e: Exception) {
            Log.e(tag, "Failed to init MediaCodec for $mime", e)
            releaseCodecLocked()
        }
    }

    private fun queueInputLocked(ptsUs: Long, data: ByteArray, flags: Int) {
        val mc = codec ?: return
        try {
            val inIdx = mc.dequeueInputBuffer(8000L)
            if (inIdx >= 0) {
                val buf = mc.getInputBuffer(inIdx) ?: return
                buf.clear()
                if (data.size <= buf.capacity()) {
                    buf.put(data)
                    mc.queueInputBuffer(inIdx, 0, data.size, ptsUs, flags)
                } else {
                    mc.queueInputBuffer(inIdx, 0, 0, ptsUs, 0)
                }
            }
        } catch (e: Exception) {
            Log.w(tag, "queueInputBuffer error, resetting decoder", e)
            releaseCodecLocked()
        }
    }

    private fun drainOutputLocked() {
        val mc = codec ?: return
        val info = MediaCodec.BufferInfo()
        try {
            while (true) {
                val outIdx = mc.dequeueOutputBuffer(info, 0L)
                when {
                    outIdx >= 0 -> mc.releaseOutputBuffer(outIdx, true)
                    outIdx == MediaCodec.INFO_OUTPUT_FORMAT_CHANGED -> {
                        val fmt = mc.outputFormat
                        Log.d(tag, "Decoder output format changed: $fmt")
                        val w = if (fmt.containsKey(MediaFormat.KEY_WIDTH)) fmt.getInteger(MediaFormat.KEY_WIDTH) else 0
                        val h = if (fmt.containsKey(MediaFormat.KEY_HEIGHT)) fmt.getInteger(MediaFormat.KEY_HEIGHT) else 0
                        if (w > 0 && h > 0) {
                            _videoSizeFlow.value = w to h
                        }
                    }
                    else -> break
                }
            }
        } catch (e: Exception) {
            Log.w(tag, "drainOutput error", e)
            releaseCodecLocked()
        }
    }

    private fun releaseCodecLocked() {
        configured = false
        try {
            codec?.stop()
        } catch (_: Exception) {
        }
        try {
            codec?.release()
        } catch (_: Exception) {
        }
        codec = null
    }

    fun release() {
        synchronized(lock) {
            releaseCodecLocked()
            surface = null
            csdBuffer = null
        }
    }
}
