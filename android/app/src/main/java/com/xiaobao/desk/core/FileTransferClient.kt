package com.xiaobao.desk.core

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.withContext
import org.json.JSONObject
import java.io.File
import java.io.FileInputStream
import java.io.RandomAccessFile
import java.nio.ByteBuffer
import java.nio.ByteOrder
import java.security.MessageDigest
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap

class FileTransferClient(
    private val sendFrame: (channel: Byte, payload: ByteArray) -> Unit
) {
    private val _directoryFlow = MutableStateFlow<Pair<String, List<FileEntry>>>("" to emptyList())
    val directoryFlow: StateFlow<Pair<String, List<FileEntry>>> = _directoryFlow.asStateFlow()

    private val _transferProgressFlow = MutableStateFlow<TransferProgress?>(null)
    val transferProgressFlow: StateFlow<TransferProgress?> = _transferProgressFlow.asStateFlow()

    private val _errorFlow = MutableSharedFlow<String>(extraBufferCapacity = 32)
    val errorFlow: SharedFlow<String> = _errorFlow.asSharedFlow()

    private data class ActiveDownload(
        val id: String,
        val file: File,
        val raf: RandomAccessFile,
        var bytesWritten: Long = 0L,
        var expectedSize: Long = -1L
    )

    private val downloads = ConcurrentHashMap<String, ActiveDownload>()

    fun listDir(path: String) {
        val json = JSONObject().apply {
            put("path", path)
        }.toString().toByteArray(Charsets.UTF_8)
        sendFileFrame(Protocol.FILE_LIST_DIR, json)
    }

    fun downloadFile(remotePath: String, localDest: File): String {
        val id = UUID.randomUUID().toString()
        localDest.parentFile?.mkdirs()
        if (localDest.exists()) localDest.delete()
        val raf = RandomAccessFile(localDest, "rw")
        downloads[id] = ActiveDownload(id = id, file = localDest, raf = raf)
        _transferProgressFlow.value = TransferProgress(
            id = id,
            fileName = localDest.name,
            bytesTransferred = 0L,
            totalBytes = 0L,
            isUpload = false
        )
        val json = JSONObject().apply {
            put("id", id)
            put("path", remotePath)
        }.toString().toByteArray(Charsets.UTF_8)
        sendFileFrame(Protocol.FILE_DOWNLOAD_REQ, json)
        return id
    }

    suspend fun uploadFile(localSrc: File, remotePath: String): String = withContext(Dispatchers.IO) {
        val id = UUID.randomUUID().toString()
        val totalSize = localSrc.length()
        val sha256 = sha256File(localSrc)

        val startJson = JSONObject().apply {
            put("id", id)
            put("path", remotePath)
            put("size", totalSize)
            put("sha256", sha256)
        }.toString().toByteArray(Charsets.UTF_8)
        sendFileFrame(Protocol.FILE_UPLOAD_START, startJson)

        val idBytes = id.toByteArray(Charsets.UTF_8)
        val chunkBuf = ByteArray(64 * 1024)
        var offset = 0L

        FileInputStream(localSrc).use { fis ->
            while (true) {
                val read = fis.read(chunkBuf)
                if (read <= 0) break
                val frame = ByteBuffer.allocate(36 + 8 + read).order(ByteOrder.BIG_ENDIAN)
                frame.put(idBytes, 0, 36)
                frame.putLong(offset)
                frame.put(chunkBuf, 0, read)
                sendFileFrame(Protocol.FILE_CHUNK, frame.array())
                offset += read
                _transferProgressFlow.value = TransferProgress(
                    id = id,
                    fileName = localSrc.name,
                    bytesTransferred = offset,
                    totalBytes = totalSize,
                    isUpload = true
                )
            }
        }

        val doneJson = JSONObject().apply {
            put("id", id)
            put("sha256", sha256)
            put("size", totalSize)
        }.toString().toByteArray(Charsets.UTF_8)
        sendFileFrame(Protocol.FILE_DONE, doneJson)

        _transferProgressFlow.value = TransferProgress(
            id = id,
            fileName = localSrc.name,
            bytesTransferred = totalSize,
            totalBytes = totalSize,
            isUpload = true,
            done = true
        )
        id
    }

    internal fun onFileFrame(subcmd: Byte, payload: ByteArray) {
        when (subcmd) {
            Protocol.FILE_LIST_RESP -> {
                val obj = JSONObject(String(payload, Charsets.UTF_8))
                val path = obj.optString("path", "")
                val arr = obj.optJSONArray("entries")
                val list = mutableListOf<FileEntry>()
                if (arr != null) {
                    for (i in 0 until arr.length()) {
                        val item = arr.getJSONObject(i)
                        list.add(
                            FileEntry(
                                name = item.optString("name"),
                                isDir = item.optBoolean("is_dir", false),
                                size = item.optLong("size", 0L),
                                mtime = item.optLong("mtime", 0L)
                            )
                        )
                    }
                }
                _directoryFlow.value = path to list
            }

            Protocol.FILE_CHUNK -> {
                if (payload.size < 44) return
                val id = String(payload, 0, 36, Charsets.UTF_8)
                val offset = ByteBuffer.wrap(payload, 36, 8).order(ByteOrder.BIG_ENDIAN).long
                val dl = downloads[id] ?: return
                val dataLen = payload.size - 44
                synchronized(dl) {
                    dl.raf.seek(offset)
                    dl.raf.write(payload, 44, dataLen)
                    dl.bytesWritten += dataLen
                }
                _transferProgressFlow.value = TransferProgress(
                    id = id,
                    fileName = dl.file.name,
                    bytesTransferred = dl.bytesWritten,
                    totalBytes = maxOf(dl.expectedSize, dl.bytesWritten),
                    isUpload = false
                )
            }

            Protocol.FILE_DONE -> {
                val obj = JSONObject(String(payload, Charsets.UTF_8))
                val id = obj.optString("id")
                val expectedSha = obj.optString("sha256", "").lowercase()
                val size = obj.optLong("size", 0L)
                val dl = downloads.remove(id) ?: return
                try {
                    dl.raf.close()
                    val actualSha = sha256File(dl.file)
                    val ok = expectedSha.isEmpty() || actualSha == expectedSha
                    _transferProgressFlow.value = TransferProgress(
                        id = id,
                        fileName = dl.file.name,
                        bytesTransferred = dl.file.length(),
                        totalBytes = size,
                        isUpload = false,
                        done = ok,
                        error = if (ok) null else "SHA-256 mismatch"
                    )
                    if (!ok) {
                        _errorFlow.tryEmit("Download SHA-256 mismatch for ${dl.file.name}")
                    }
                } catch (e: Exception) {
                    _errorFlow.tryEmit(e.message ?: "File finalize failed")
                }
            }

            Protocol.FILE_ERR -> {
                val obj = JSONObject(String(payload, Charsets.UTF_8))
                val id = obj.optString("id")
                val err = obj.optString("error", "Unknown file error")
                val dl = downloads.remove(id)
                try {
                    dl?.raf?.close()
                } catch (_: Exception) {
                }
                _transferProgressFlow.value = TransferProgress(
                    id = id,
                    fileName = dl?.file?.name ?: "",
                    bytesTransferred = dl?.bytesWritten ?: 0L,
                    totalBytes = 0L,
                    isUpload = false,
                    done = false,
                    error = err
                )
                _errorFlow.tryEmit(err)
            }
        }
    }

    private fun sendFileFrame(subcmd: Byte, body: ByteArray) {
        val payload = ByteArray(1 + body.size)
        payload[0] = subcmd
        if (body.isNotEmpty()) {
            System.arraycopy(body, 0, payload, 1, body.size)
        }
        sendFrame(Protocol.CHANNEL_FILE, payload)
    }

    private fun sha256File(file: File): String {
        val md = MessageDigest.getInstance("SHA-256")
        val buf = ByteArray(64 * 1024)
        FileInputStream(file).use { fis ->
            while (true) {
                val n = fis.read(buf)
                if (n <= 0) break
                md.update(buf, 0, n)
            }
        }
        return md.digest().joinToString("") { "%02x".format(it) }
    }
}
