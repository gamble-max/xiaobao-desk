package com.xiaobao.desk.core

import android.media.AudioAttributes
import android.media.AudioFormat
import android.media.AudioTrack
import android.util.Log

class LowLatencyAudioPlayer {
    private val tag = "LowLatencyAudioPlayer"
    private val lock = Any()

    private var audioTrack: AudioTrack? = null
    private var currentSampleRate = 0
    private var currentChannels = 0

    @Volatile
    var muted: Boolean = false
        set(value) {
            field = value
            synchronized(lock) {
                audioTrack?.setVolume(if (value) 0f else 1f)
            }
        }

    fun onAudioFrame(@Suppress("UNUSED_PARAMETER") ptsUs: Long, sampleRate: Int, channels: Int, pcmData: ByteArray) {
        if (pcmData.isEmpty()) return
        val rate = if (sampleRate > 0) sampleRate else 48000
        val ch = if (channels in 1..2) channels else 2

        synchronized(lock) {
            if (audioTrack == null || currentSampleRate != rate || currentChannels != ch) {
                initTrackLocked(rate, ch)
            }
            val track = audioTrack ?: return
            if (track.playState != AudioTrack.PLAYSTATE_PLAYING) {
                try {
                    track.play()
                } catch (e: Exception) {
                    Log.w(tag, "AudioTrack play failed", e)
                    return
                }
            }
            if (!muted) {
                track.write(pcmData, 0, pcmData.size, AudioTrack.WRITE_NON_BLOCKING)
            }
        }
    }

    private fun initTrackLocked(sampleRate: Int, channels: Int) {
        releaseLocked()
        val channelMask = if (channels == 1) AudioFormat.CHANNEL_OUT_MONO else AudioFormat.CHANNEL_OUT_STEREO
        val minBuf = AudioTrack.getMinBufferSize(sampleRate, channelMask, AudioFormat.ENCODING_PCM_16BIT)
        val bufSize = maxOf(minBuf, sampleRate * channels * 2 / 25) // ~40ms buffer

        try {
            val track = AudioTrack.Builder()
                .setAudioAttributes(
                    AudioAttributes.Builder()
                        .setUsage(AudioAttributes.USAGE_GAME)
                        .setContentType(AudioAttributes.CONTENT_TYPE_MUSIC)
                        .build()
                )
                .setAudioFormat(
                    AudioFormat.Builder()
                        .setSampleRate(sampleRate)
                        .setChannelMask(channelMask)
                        .setEncoding(AudioFormat.ENCODING_PCM_16BIT)
                        .build()
                )
                .setBufferSizeInBytes(bufSize)
                .setTransferMode(AudioTrack.MODE_STREAM)
                .setPerformanceMode(AudioTrack.PERFORMANCE_MODE_LOW_LATENCY)
                .build()
            track.setVolume(if (muted) 0f else 1f)
            track.play()
            audioTrack = track
            currentSampleRate = sampleRate
            currentChannels = channels
        } catch (e: Exception) {
            Log.e(tag, "Failed to initialize AudioTrack ($sampleRate Hz, $channels ch)", e)
        }
    }

    private fun releaseLocked() {
        try {
            audioTrack?.stop()
        } catch (_: Exception) {
        }
        try {
            audioTrack?.release()
        } catch (_: Exception) {
        }
        audioTrack = null
        currentSampleRate = 0
        currentChannels = 0
    }

    fun release() {
        synchronized(lock) {
            releaseLocked()
        }
    }
}
