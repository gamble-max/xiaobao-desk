package com.xiaobao.desk.core

import org.json.JSONObject
import java.net.DatagramPacket
import java.net.DatagramSocket
import java.net.Inet6Address
import java.net.InetAddress
import java.net.NetworkInterface

data class DiscoveredHost(
    val name: String,
    val address: String,
    val hostId: String,
    val certFingerprint: String
)

object LanDiscovery {
    fun scan(port: Int = 21118, timeoutMs: Int = 900): List<DiscoveredHost> {
        val results = linkedMapOf<String, DiscoveredHost>()
        var socket: DatagramSocket? = null
        try {
            socket = DatagramSocket().apply {
                broadcast = true
                soTimeout = 200
            }
            val payload = """{"type":"xb_discover"}""".toByteArray(Charsets.UTF_8)
            val targets = linkedSetOf<InetAddress>()
            runCatching { targets.add(InetAddress.getByName("255.255.255.255")) }

            val ifaces = NetworkInterface.getNetworkInterfaces()
            while (ifaces != null && ifaces.hasMoreElements()) {
                val nif = ifaces.nextElement()
                if (!nif.isUp || nif.isLoopback) continue
                for (ia in nif.interfaceAddresses) {
                    ia.broadcast?.let { targets.add(it) }
                }
            }

            for (addr in targets) {
                runCatching {
                    socket.send(DatagramPacket(payload, payload.size, addr, port))
                }
            }

            val buf = ByteArray(2048)
            val deadline = System.currentTimeMillis() + timeoutMs
            while (System.currentTimeMillis() < deadline) {
                val pkt = DatagramPacket(buf, buf.size)
                try {
                    socket.receive(pkt)
                } catch (_: Exception) {
                    continue
                }
                val text = String(pkt.data, 0, pkt.length, Charsets.UTF_8).trim()
                val obj = runCatching { JSONObject(text) }.getOrNull() ?: continue
                if (obj.optString("type") != "xb_announce") continue
                val fp = obj.optString("cert_sha256", "").trim()
                if (fp.isEmpty()) continue
                val hostId = obj.optString("host_id", "").trim()
                val name = obj.optString("name", "").trim().ifEmpty { pkt.address.hostAddress ?: "Windows PC" }
                val respPort = obj.optInt("port", port).let { if (it > 0) it else port }
                val rawIp = (pkt.address.hostAddress ?: "").substringBefore('%')
                if (rawIp.isEmpty()) continue
                val formatted = if (pkt.address is Inet6Address || rawIp.contains(':')) {
                    "[$rawIp]:$respPort"
                } else {
                    "$rawIp:$respPort"
                }
                val key = "$fp|$formatted"
                results[key] = DiscoveredHost(
                    name = name,
                    address = formatted,
                    hostId = hostId,
                    certFingerprint = fp
                )
            }
        } catch (_: Exception) {
        } finally {
            runCatching { socket?.close() }
        }
        return results.values.toList()
    }
}
