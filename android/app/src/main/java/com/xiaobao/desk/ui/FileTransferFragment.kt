package com.xiaobao.desk.ui

import android.os.Bundle
import android.os.Environment
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.Button
import android.widget.TextView
import android.widget.Toast
import androidx.fragment.app.Fragment
import androidx.lifecycle.lifecycleScope
import androidx.recyclerview.widget.LinearLayoutManager
import androidx.recyclerview.widget.RecyclerView
import com.xiaobao.desk.R
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.launch
import java.io.File

class FileTransferFragment : Fragment() {

    private lateinit var localAdapter: FileEntryAdapter
    private lateinit var remoteAdapter: FileEntryAdapter
    private lateinit var tvLocalPath: TextView
    private lateinit var tvRemotePath: TextView
    private lateinit var tvTransferStatus: TextView

    private var currentLocalDir: File = File("/sdcard")
    private var currentRemotePath: String = "C:\\"

    override fun onCreateView(
        inflater: LayoutInflater,
        container: ViewGroup?,
        savedInstanceState: Bundle?
    ): View = inflater.inflate(R.layout.fragment_file_transfer, container, false)

    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        super.onViewCreated(view, savedInstanceState)
        val conn = (requireActivity() as MainActivity).connectionManager

        tvLocalPath = view.findViewById(R.id.tvLocalPath)
        tvRemotePath = view.findViewById(R.id.tvRemotePath)
        tvTransferStatus = view.findViewById(R.id.tvTransferStatus)

        val rvLocal = view.findViewById<RecyclerView>(R.id.rvLocalFiles)
        val rvRemote = view.findViewById<RecyclerView>(R.id.rvRemoteFiles)

        localAdapter = FileEntryAdapter { entry ->
            if (entry.isDir) {
                loadLocalDir(File(entry.fullPath))
            }
        }
        remoteAdapter = FileEntryAdapter { entry ->
            if (entry.isDir) {
                currentRemotePath = entry.fullPath
                conn.fileTransferClient.listDir(currentRemotePath)
            }
        }

        rvLocal.layoutManager = LinearLayoutManager(requireContext())
        rvLocal.adapter = localAdapter
        rvRemote.layoutManager = LinearLayoutManager(requireContext())
        rvRemote.adapter = remoteAdapter

        val defaultLocal = Environment.getExternalStorageDirectory()
        currentLocalDir = if (defaultLocal.canRead()) defaultLocal else requireContext().filesDir
        loadLocalDir(currentLocalDir)

        view.findViewById<Button>(R.id.btnLocalUp).setOnClickListener {
            currentLocalDir.parentFile?.let { loadLocalDir(it) }
        }
        view.findViewById<Button>(R.id.btnRemoteUp).setOnClickListener {
            val parent = parentWindowsPath(currentRemotePath)
            currentRemotePath = parent
            conn.fileTransferClient.listDir(parent)
        }
        view.findViewById<Button>(R.id.btnRemoteRefresh).setOnClickListener {
            conn.fileTransferClient.listDir(currentRemotePath)
        }

        view.findViewById<Button>(R.id.btnUploadFile).setOnClickListener {
            val sel = localAdapter.getSelected()
            if (sel == null || sel.isDir) {
                Toast.makeText(requireContext(), "Select a local file first", Toast.LENGTH_SHORT).show()
                return@setOnClickListener
            }
            val remoteDest = joinWindowsPath(currentRemotePath, sel.name)
            viewLifecycleOwner.lifecycleScope.launch {
                conn.fileTransferClient.uploadFile(File(sel.fullPath), remoteDest)
            }
        }

        view.findViewById<Button>(R.id.btnDownloadFile).setOnClickListener {
            val sel = remoteAdapter.getSelected()
            if (sel == null || sel.isDir) {
                Toast.makeText(requireContext(), "Select a remote file first", Toast.LENGTH_SHORT).show()
                return@setOnClickListener
            }
            val localDest = File(currentLocalDir, sel.name)
            conn.fileTransferClient.downloadFile(sel.fullPath, localDest)
        }

        viewLifecycleOwner.lifecycleScope.launch {
            conn.fileTransferClient.directoryFlow.collectLatest { (path, entries) ->
                if (path.isNotEmpty()) {
                    currentRemotePath = path
                    tvRemotePath.text = path
                }
                remoteAdapter.submit(
                    entries.map {
                        UiFileEntry(
                            name = it.name,
                            isDir = it.isDir,
                            size = it.size,
                            fullPath = joinWindowsPath(currentRemotePath, it.name)
                        )
                    }
                )
            }
        }

        viewLifecycleOwner.lifecycleScope.launch {
            conn.fileTransferClient.transferProgressFlow.collectLatest { prog ->
                if (prog != null) {
                    val dirLabel = if (prog.isUpload) "Uploading" else "Downloading"
                    val pct = if (prog.totalBytes > 0) (prog.bytesTransferred * 100 / prog.totalBytes) else 0
                    tvTransferStatus.text = when {
                        prog.error != null -> "Error: ${prog.error}"
                        prog.done -> "Done: ${prog.fileName} (${prog.totalBytes} B)"
                        else -> "$dirLabel ${prog.fileName}: $pct% (${prog.bytesTransferred}/${prog.totalBytes})"
                    }
                    if (prog.done && !prog.isUpload) {
                        loadLocalDir(currentLocalDir)
                    }
                }
            }
        }

        viewLifecycleOwner.lifecycleScope.launch {
            conn.fileTransferClient.errorFlow.collectLatest { err ->
                tvTransferStatus.text = "Transfer error: $err"
            }
        }
    }

    private fun loadLocalDir(dir: File) {
        currentLocalDir = dir
        tvLocalPath.text = dir.absolutePath
        val files = dir.listFiles()?.map {
            UiFileEntry(name = it.name, isDir = it.isDirectory, size = it.length(), fullPath = it.absolutePath)
        } ?: emptyList()
        localAdapter.submit(files)
    }

    private fun joinWindowsPath(base: String, child: String): String {
        val clean = base.trimEnd('\\', '/')
        return if (clean.endsWith(":")) "$clean\\$child" else "$clean\\$child"
    }

    private fun parentWindowsPath(path: String): String {
        val clean = path.trimEnd('\\', '/')
        val idx = clean.lastIndexOf('\\')
        return if (idx <= 2) {
            if (clean.length >= 2 && clean[1] == ':') "${clean.substring(0, 2)}\\" else "C:\\"
        } else {
            clean.substring(0, idx)
        }
    }
}
