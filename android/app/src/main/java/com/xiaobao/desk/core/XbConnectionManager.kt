package com.xiaobao.desk.core

import android.content.Context
import android.util.Log
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancelChildren
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import org.json.JSONArray
import org.json.JSONObject
import java.io.BufferedInputStream
import java.io.BufferedOutputStream
import java.io.DataInputStream
import java.io.OutputStream
import java.net.Inet6Address
import java.net.InetAddress
import java.net.InetSocketAddress
import java.nio.ByteBuffer
import java.nio.ByteOrder
import java.security.MessageDigest
import java.security.SecureRandom
import java.security.cert.CertificateException
import java.security.cert.X509Certificate
import java.util.UUID
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicReference
import javax.crypto.Mac
import javax.crypto.spec.SecretKeySpec
import javax.net.ssl.SSLContext
import javax.net.ssl.SSLSocket
import javax.net.ssl.TrustManager
import javax.net.ssl.X509TrustManager

class XbConnectionManager(context: Context) {
    private val tag = "XbConnectionManager"
    private val prefs = context.applicationContext.getSharedPreferences("xiaobao_desk_core", Context.MODE_PRIVATE)
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    @OptIn(kotlinx.coroutines.ExperimentalCoroutinesApi::class)
    private val writeScope = CoroutineScope(SupervisorJob() + Dispatchers.IO.limitedParallelism(1))

    val deviceId: String by lazy {
        prefs.getString("device_id", null) ?: UUID.randomUUID().toString().also {
            prefs.edit().putString("device_id", it).apply()
        }
    }

    private val _connectionState = MutableStateFlow(ConnectionState.DISCONNECTED)
    val connectionState: StateFlow<ConnectionState> = _connectionState.asStateFlow()

    private val _connectionPath = MutableStateFlow(ConnectionPath.NONE)
    val connectionPath: StateFlow<ConnectionPath> = _connectionPath.asStateFlow()

    private val _lastError = MutableStateFlow<String?>(null)
    val lastError: StateFlow<String?> = _lastError.asStateFlow()

    private val _stats = MutableStateFlow<StreamStats?>(null)
    val stats: StateFlow<StreamStats?> = _stats.asStateFlow()

    private val _clipboardEvents = MutableSharedFlow<String>(extraBufferCapacity = 16)
    val clipboardEvents: SharedFlow<String> = _clipboardEvents.asSharedFlow()

    private val _imeHintEvents = MutableSharedFlow<Boolean>(extraBufferCapacity = 16)
    val imeHintEvents: SharedFlow<Boolean> = _imeHintEvents.asSharedFlow()

    private val _noticeEvents = MutableSharedFlow<String>(extraBufferCapacity = 16)
    val noticeEvents: SharedFlow<String> = _noticeEvents.asSharedFlow()

    val videoDecoder = HardwareVideoDecoder()
    val audioPlayer = LowLatencyAudioPlayer()
    val terminalClient = TerminalClient { ch, payload -> sendFrame(ch, payload) }
    val fileTransferClient = FileTransferClient { ch, payload -> sendFrame(ch, payload) }

    private val writeLock = Any()
    private var activeSocket: SSLSocket? = null
    private var activeOut: OutputStream? = null
    private var activeIn: DataInputStream? = null
    private var connectJob: Job? = null
    private var currentConfig: ConnectionConfig? = null

    fun connect(config: ConnectionConfig) {
        disconnect()
        currentConfig = config
        _lastError.value = null
        _connectionPath.value = ConnectionPath.NONE
        _connectionState.value = ConnectionState.CONNECTING

        connectJob = scope.launch {
            try {
                establishSession(config)
            } catch (e: Exception) {
                Log.e(tag, "Connection failed", e)
                _lastError.value = e.message ?: "Connection failed"
                _connectionState.value = ConnectionState.ERROR
                closeActiveSocket()
            }
        }
    }

    fun disconnect() {
        connectJob?.cancel()
        connectJob = null
        closeActiveSocket()
        _connectionState.value = ConnectionState.DISCONNECTED
        _connectionPath.value = ConnectionPath.NONE
    }

    private fun closeActiveSocket() {
        synchronized(writeLock) {
            try {
                activeSocket?.close()
            } catch (_: Exception) {
            }
            activeSocket = null
            activeOut = null
            activeIn = null
        }
    }

    private fun expandDirectCandidates(raw: String, defaultPort: Int): List<Pair<String, Int>> {
        if (raw.isBlank()) return emptyList()
        val out = mutableListOf<Pair<String, Int>>()
        val tokens = raw.split(',', ';', ' ', '\n', '\t')
        for (tok in tokens) {
            val ep = parseHostPort(tok, defaultPort) ?: continue
            val (host, port) = ep
            val resolved = runCatching { InetAddress.getAllByName(host) }.getOrNull()
            if (resolved != null && resolved.isNotEmpty()) {
                for (addr in resolved) {
                    val ip = addr.hostAddress?.substringBefore('%')
                    if (!ip.isNullOrBlank()) {
                        out.add(ip to port)
                    }
                }
            } else {
                out.add(host to port)
            }
        }
        return out.distinct()
    }

    private fun establishSession(config: ConnectionConfig) {
        val directCandidates = expandDirectCandidates(config.directHostOverride, config.directPort)

        // 1. Serverless Direct Mode (LAN / IPv6 / DDNS / Tailscale / ZeroTier) when Hub is blank
        if (config.hubHost.isBlank()) {
            if (directCandidates.isEmpty()) {
                throw IllegalStateException("无服务端直连模式下请填写电脑 IP / IPv6 / DDNS / Tailscale 地址")
            }
            _connectionState.value = ConnectionState.AUTHENTICATING
            val directResult = raceDirectEndpoints(directCandidates, config, config.hostCertSha256)
            if (directResult != null) {
                val (sock, inp, out, path) = directResult
                attachAndRunReader(sock, inp, out, path, config)
                return
            }
            throw IllegalStateException("直连失败，请确认电脑端已启动且防火墙放行 TCP 21118 及证书指纹/密码正确")
        }

        // 2. Self-Hosted Hub Mode (with automatic direct fallback if directCandidates are provided)
        val hubSocket: SSLSocket
        val hubIn: DataInputStream
        val hubOut: BufferedOutputStream
        val candidates = directCandidates.toMutableList()
        var resolvedHostId = config.hostId
        var resolvedHostCertSha = config.hostCertSha256

        try {
            hubSocket = openPinnedTlsSocket(config.hubHost, config.hubPort, config.hubCertSha256, 5000)
            hubIn = DataInputStream(BufferedInputStream(hubSocket.inputStream, 64 * 1024))
            hubOut = BufferedOutputStream(hubSocket.outputStream, 64 * 1024)

            _connectionState.value = ConnectionState.AUTHENTICATING
            performAuthHandshake(hubIn, hubOut, config.password, config.totpCode)

            val queryPayload = JSONObject().apply {
                put("type", "peer_query")
                put("host_id", config.hostId)
            }.toString().toByteArray(Charsets.UTF_8)
            Protocol.writeFrame(hubOut, Protocol.CHANNEL_CTRL, queryPayload)

            val deadline = System.currentTimeMillis() + 3000L
            while (System.currentTimeMillis() < deadline) {
                val (ch, payload) = Protocol.readFrame(hubIn)
                if (ch == Protocol.CHANNEL_CTRL) {
                    val obj = JSONObject(String(payload, Charsets.UTF_8))
                    if (obj.optString("type") == "peer_endpoints") {
                        resolvedHostId = obj.optString("host_id", "").ifEmpty { config.hostId }
                        resolvedHostCertSha = obj.optString("cert_sha256", "").ifEmpty { config.hostCertSha256 }
                        val arr = obj.optJSONArray("endpoints") ?: JSONArray()
                        for (i in 0 until arr.length()) {
                            val ep = arr.optString(i)
                            parseHostPort(ep, config.directPort)?.let { candidates.add(it) }
                        }
                        break
                    }
                }
            }
        } catch (hubErr: Exception) {
            if (directCandidates.isNotEmpty()) {
                _connectionState.value = ConnectionState.AUTHENTICATING
                val directResult = raceDirectEndpoints(directCandidates, config, config.hostCertSha256)
                if (directResult != null) {
                    val (sock, inp, out, path) = directResult
                    attachAndRunReader(sock, inp, out, path, config)
                    return
                }
            }
            throw hubErr
        }

        // Race direct P2P endpoints (IPv6 / LAN)
        val directResult = raceDirectEndpoints(candidates.distinct(), config, resolvedHostCertSha)
        if (directResult != null) {
            val (sock, inp, out, path) = directResult
            try {
                hubSocket.close()
            } catch (_: Exception) {
            }
            attachAndRunReader(sock, inp, out, path, config)
            return
        }

        // Fallback to authenticated xb-hub TLS relay
        val bindPayload = JSONObject().apply {
            put("type", "relay_bind")
            put("host_id", resolvedHostId)
        }.toString().toByteArray(Charsets.UTF_8)
        Protocol.writeFrame(hubOut, Protocol.CHANNEL_CTRL, bindPayload)

        while (true) {
            val (ch, payload) = Protocol.readFrame(hubIn)
            if (ch == Protocol.CHANNEL_CTRL) {
                val obj = JSONObject(String(payload, Charsets.UTF_8))
                when (obj.optString("type")) {
                    "relay_ready" -> break
                    "auth_err", "error" -> throw IllegalStateException(obj.optString("reason", obj.optString("error", "Relay failed")))
                }
            }
        }

        performAuthHandshake(hubIn, hubOut, config.password, config.totpCode)
        attachAndRunReader(hubSocket, hubIn, hubOut, ConnectionPath.RELAY_HUB, config)
    }

    private data class DirectWinner(
        val socket: SSLSocket,
        val input: DataInputStream,
        val output: OutputStream,
        val path: ConnectionPath
    )

    private fun raceDirectEndpoints(
        candidates: List<Pair<String, Int>>,
        config: ConnectionConfig,
        hostCertSha256: String
    ): DirectWinner? {
        if (candidates.isEmpty()) return null
        val winner = AtomicReference<DirectWinner?>(null)
        val latch = CountDownLatch(candidates.size)

        for ((host, port) in candidates) {
            Thread {
                var sock: SSLSocket? = null
                try {
                    sock = openPinnedTlsSocket(host, port, hostCertSha256, 2500)
                    val inp = DataInputStream(BufferedInputStream(sock.inputStream, 64 * 1024))
                    val out = BufferedOutputStream(sock.outputStream, 64 * 1024)
                    performAuthHandshake(inp, out, config.password, config.totpCode)
                    val isIpv6 = sock.inetAddress is Inet6Address
                    val path = if (isIpv6) ConnectionPath.DIRECT_IPV6 else ConnectionPath.DIRECT_LAN
                    val candidate = DirectWinner(sock, inp, out, path)
                    if (!winner.compareAndSet(null, candidate)) {
                        sock.close()
                    }
                } catch (_: Exception) {
                    try {
                        sock?.close()
                    } catch (_: Exception) {
                    }
                } finally {
                    latch.countDown()
                }
            }.start()
        }

        latch.await(2800L, TimeUnit.MILLISECONDS)
        return winner.get()
    }

    private fun attachAndRunReader(
        socket: SSLSocket,
        inp: DataInputStream,
        out: OutputStream,
        path: ConnectionPath,
        config: ConnectionConfig
    ) {
        synchronized(writeLock) {
            activeSocket = socket
            activeIn = inp
            activeOut = out
        }
        _connectionPath.value = path
        _connectionState.value = ConnectionState.CONNECTED

        // Send initial encoder & display mode configuration
        if (config.startStreamOnConnect) {
            sendStreamEnable(true)
            if (config.superScreen && config.width > 0 && config.height > 0) {
                setSuperScreen(config.width, config.height, config.fps, true, config.migrateWindows)
                setEncoder(config.width, config.height, config.fps, config.bitrateKbps, config.codec)
            } else {
                setSuperScreen(0, 0, 0, false)
                setEncoder(0, 0, config.fps, config.bitrateKbps, config.codec)
            }
        } else {
            sendStreamEnable(false)
        }

        while (_connectionState.value == ConnectionState.CONNECTED) {
            val (channel, payload) = Protocol.readFrame(inp)
            dispatchFrame(channel, payload, path, config.codec)
        }
    }

    private fun dispatchFrame(channel: Byte, payload: ByteArray, path: ConnectionPath, codec: String) {
        when (channel) {
            Protocol.CHANNEL_CTRL -> {
                val obj = JSONObject(String(payload, Charsets.UTF_8))
                when (obj.optString("type")) {
                    "stats" -> {
                        _stats.value = StreamStats(
                            fps = obj.optInt("fps", 0),
                            bitrateKbps = obj.optInt("bitrate_kbps", 0),
                            rttMs = obj.optLong("rtt_ms", 0L),
                            path = path,
                            codec = obj.optString("codec", codec)
                        )
                    }
                    "ping" -> {
                        sendCtrl(JSONObject().put("type", "pong").put("ts", obj.optLong("ts", 0L)))
                    }
                    "clipboard" -> {
                        val txt = obj.optString("text", "")
                        if (txt.isNotEmpty()) _clipboardEvents.tryEmit(txt)
                    }
                    "ime_hint" -> {
                        if (obj.optBoolean("editing", false)) _imeHintEvents.tryEmit(true)
                    }
                    "notice", "error" -> {
                        val msg = obj.optString("message", "")
                        if (msg.isNotEmpty()) _noticeEvents.tryEmit(msg)
                    }
                }
            }

            Protocol.CHANNEL_VIDEO -> {
                if (payload.size < 9) return
                val ptsUs = ByteBuffer.wrap(payload, 0, 8).order(ByteOrder.BIG_ENDIAN).long
                val flags = payload[8].toInt() and 0xFF
                val nal = payload.copyOfRange(9, payload.size)
                videoDecoder.onVideoFrame(ptsUs, flags, nal)
            }

            Protocol.CHANNEL_AUDIO -> {
                if (payload.size < 11) return
                val bb = ByteBuffer.wrap(payload, 0, 11).order(ByteOrder.BIG_ENDIAN)
                val ptsUs = bb.long
                val sampleRateDiv100 = bb.short.toInt() and 0xFFFF
                val channels = bb.get().toInt() and 0xFF
                val pcm = payload.copyOfRange(11, payload.size)
                audioPlayer.onAudioFrame(ptsUs, sampleRateDiv100 * 100, channels, pcm)
            }

            Protocol.CHANNEL_TERM -> {
                if (payload.isEmpty()) return
                val subcmd = payload[0]
                val body = if (payload.size > 1) payload.copyOfRange(1, payload.size) else ByteArray(0)
                terminalClient.onTermFrame(subcmd, body)
            }

            Protocol.CHANNEL_FILE -> {
                if (payload.isEmpty()) return
                val subcmd = payload[0]
                val body = if (payload.size > 1) payload.copyOfRange(1, payload.size) else ByteArray(0)
                fileTransferClient.onFileFrame(subcmd, body)
            }
        }
    }

    private fun performAuthHandshake(
        inp: DataInputStream,
        out: OutputStream,
        password: String,
        totpCode: String
    ) {
        val hello = JSONObject().apply {
            put("type", "hello")
            put("device_id", deviceId)
            put("role", "client")
        }.toString().toByteArray(Charsets.UTF_8)
        Protocol.writeFrame(out, Protocol.CHANNEL_CTRL, hello)

        while (true) {
            val (ch, payload) = Protocol.readFrame(inp)
            if (ch != Protocol.CHANNEL_CTRL) continue
            val obj = JSONObject(String(payload, Charsets.UTF_8))
            when (obj.optString("type")) {
                "challenge" -> {
                    val nonce = obj.getString("nonce")
                    val hmac = hmacSha256Hex(nonce, password)
                    val auth = JSONObject().apply {
                        put("type", "auth")
                        put("device_id", deviceId)
                        put("role", "client")
                        put("hmac", hmac)
                        if (totpCode.isNotBlank()) {
                            put("totp", totpCode.trim())
                        }
                    }.toString().toByteArray(Charsets.UTF_8)
                    Protocol.writeFrame(out, Protocol.CHANNEL_CTRL, auth)
                }
                "auth_ok" -> return
                "auth_err" -> throw IllegalStateException(obj.optString("reason", "Authentication failed"))
            }
        }
    }

    private fun openPinnedTlsSocket(
        host: String,
        port: Int,
        expectedSha256: String,
        timeoutMs: Int
    ): SSLSocket {
        val normalizedPin = expectedSha256.replace(":", "").replace(" ", "").lowercase()
        val trustManager = object : X509TrustManager {
            override fun checkClientTrusted(chain: Array<out X509Certificate>?, authType: String?) {}
            override fun checkServerTrusted(chain: Array<out X509Certificate>?, authType: String?) {
                if (chain.isNullOrEmpty()) throw CertificateException("Empty server certificate chain")
                if (normalizedPin.isNotEmpty()) {
                    val digest = MessageDigest.getInstance("SHA-256").digest(chain[0].encoded)
                    val actual = digest.joinToString("") { "%02x".format(it) }
                    if (actual != normalizedPin) {
                        throw CertificateException("TLS certificate SHA-256 mismatch")
                    }
                }
            }
            override fun getAcceptedIssuers(): Array<X509Certificate> = emptyArray()
        }

        val sslCtx = SSLContext.getInstance("TLS")
        sslCtx.init(null, arrayOf<TrustManager>(trustManager), SecureRandom())
        val socket = sslCtx.socketFactory.createSocket() as SSLSocket
        socket.connect(InetSocketAddress(InetAddress.getByName(host), port), timeoutMs)
        socket.soTimeout = 0
        socket.tcpNoDelay = true
        val supported = socket.supportedProtocols.filter { it == "TLSv1.3" || it == "TLSv1.2" }.toTypedArray()
        if (supported.isNotEmpty()) {
            socket.enabledProtocols = supported
        }
        socket.startHandshake()
        return socket
    }

    private fun hmacSha256Hex(nonce: String, secret: String): String {
        val mac = Mac.getInstance("HmacSHA256")
        mac.init(SecretKeySpec(secret.toByteArray(Charsets.UTF_8), "HmacSHA256"))
        return mac.doFinal(nonce.toByteArray(Charsets.UTF_8)).joinToString("") { "%02x".format(it) }
    }

    private fun parseHostPort(endpoint: String, defaultPort: Int): Pair<String, Int>? {
        val trimmed = endpoint.trim()
        if (trimmed.isEmpty()) return null
        return if (trimmed.startsWith("[")) {
            val endBracket = trimmed.indexOf(']')
            if (endBracket < 0) return null
            val host = trimmed.substring(1, endBracket)
            val port = if (endBracket + 1 < trimmed.length && trimmed[endBracket + 1] == ':') {
                trimmed.substring(endBracket + 2).toIntOrNull() ?: defaultPort
            } else defaultPort
            host to port
        } else {
            val colon = trimmed.lastIndexOf(':')
            if (colon > 0 && trimmed.indexOf(':') == colon) {
                val host = trimmed.substring(0, colon)
                val port = trimmed.substring(colon + 1).toIntOrNull() ?: defaultPort
                host to port
            } else {
                trimmed to defaultPort
            }
        }
    }

    fun sendFrame(channel: Byte, payload: ByteArray) {
        writeScope.launch {
            synchronized(writeLock) {
                val out = activeOut ?: return@synchronized
                try {
                    Protocol.writeFrame(out, channel, payload)
                } catch (e: Exception) {
                    Log.w(tag, "sendFrame failed", e)
                }
            }
        }
    }

    fun sendCtrl(json: JSONObject) {
        sendFrame(Protocol.CHANNEL_CTRL, json.toString().toByteArray(Charsets.UTF_8))
    }

    fun sendInput(json: JSONObject) {
        sendFrame(Protocol.CHANNEL_INPUT, json.toString().toByteArray(Charsets.UTF_8))
    }

    fun sendMouseMove(x: Float, y: Float) {
        sendInput(JSONObject().apply {
            put("type", "mouse_move")
            put("x", x.coerceIn(0f, 1f).toDouble())
            put("y", y.coerceIn(0f, 1f).toDouble())
        })
    }

    fun sendMouseButton(button: String, down: Boolean, x: Float? = null, y: Float? = null) {
        sendInput(JSONObject().apply {
            put("type", "mouse_btn")
            put("button", button)
            put("down", down)
            if (x != null && y != null) {
                put("x", x.coerceIn(0f, 1f).toDouble())
                put("y", y.coerceIn(0f, 1f).toDouble())
            }
        })
    }

    fun sendMouseClick(x: Float, y: Float, button: String = "left") {
        sendInput(JSONObject().apply {
            put("type", "mouse_click")
            put("x", x.coerceIn(0f, 1f).toDouble())
            put("y", y.coerceIn(0f, 1f).toDouble())
            put("button", button)
        })
    }

    fun sendMouseWheel(dx: Int, dy: Int, x: Float? = null, y: Float? = null) {
        sendInput(JSONObject().apply {
            put("type", "mouse_wheel")
            put("dx", dx)
            put("dy", dy)
            if (x != null && y != null) {
                put("x", x.coerceIn(0f, 1f).toDouble())
                put("y", y.coerceIn(0f, 1f).toDouble())
            }
        })
    }

    fun sendKey(vk: Int, scan: Int, down: Boolean, ext: Boolean = false) {
        sendInput(JSONObject().apply {
            put("type", "key")
            put("vk", vk)
            put("scan", scan)
            put("down", down)
            put("ext", ext)
        })
    }

    fun sendTouch(pointers: List<TouchPointer>) {
        val arr = JSONArray()
        for (p in pointers.take(10)) {
            arr.put(JSONObject().apply {
                put("id", p.id)
                put("action", p.action)
                put("x", p.x.coerceIn(0f, 1f).toDouble())
                put("y", p.y.coerceIn(0f, 1f).toDouble())
            })
        }
        sendInput(JSONObject().apply {
            put("type", "touch")
            put("pointers", arr)
        })
    }

    fun sendGamepad(buttons: Int, lx: Int, ly: Int, rx: Int, ry: Int, lt: Int, rt: Int) {
        sendInput(JSONObject().apply {
            put("type", "gamepad")
            put("buttons", buttons)
            put("lx", lx)
            put("ly", ly)
            put("rx", rx)
            put("ry", ry)
            put("lt", lt)
            put("rt", rt)
        })
    }

    fun sendHostAction(cmd: String) {
        sendCtrl(JSONObject().apply {
            put("type", "desktop_action")
            put("cmd", cmd)
        })
    }

    fun sendStreamEnable(enable: Boolean) {
        sendCtrl(JSONObject().apply {
            put("type", "stream_enable")
            put("enable", enable)
        })
    }

    fun sendPrivacyScreen(enable: Boolean) {
        sendCtrl(JSONObject().apply {
            put("type", "privacy_screen")
            put("enable", enable)
        })
    }

    fun sendClipboard(text: String) {
        sendCtrl(JSONObject().apply {
            put("type", "clipboard")
            put("text", text)
        })
    }

    fun sendText(text: String) {
        sendInput(JSONObject().apply {
            put("type", "text")
            put("text", text)
        })
    }

    fun setMuteHost(enable: Boolean) {
        sendCtrl(JSONObject().apply {
            put("type", "mute_host")
            put("enable", enable)
        })
    }

    fun setViewOnly(enable: Boolean) {
        sendCtrl(JSONObject().apply {
            put("type", "view_only")
            put("enable", enable)
        })
    }

    fun sendPreserveCursor(enable: Boolean) {
        sendCtrl(JSONObject().apply {
            put("type", "preserve_cursor")
            put("enable", enable)
        })
    }

    fun setSuperScreen(width: Int, height: Int, hz: Int, enable: Boolean, migrateWindows: Boolean = true) {
        sendCtrl(JSONObject().apply {
            put("type", "set_superscreen")
            put("width", width)
            put("height", height)
            put("hz", hz)
            put("enable", enable)
            put("migrate_windows", migrateWindows)
        })
    }

    fun setEncoder(width: Int, height: Int, fps: Int, bitrateKbps: Int, codec: String) {
        sendCtrl(JSONObject().apply {
            put("type", "set_encoder")
            put("width", width)
            put("height", height)
            put("fps", fps)
            put("bitrate_kbps", bitrateKbps)
            put("codec", codec)
        })
    }

    fun release() {
        disconnect()
        videoDecoder.release()
        audioPlayer.release()
        scope.coroutineContext.cancelChildren()
        writeScope.coroutineContext.cancelChildren()
    }
}
