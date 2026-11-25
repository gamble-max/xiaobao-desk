package com.xiaobao.desk.ui

import android.content.Intent
import android.os.Bundle
import android.view.KeyEvent
import android.view.MotionEvent
import android.view.View
import android.widget.LinearLayout
import android.widget.TextView
import android.widget.Toast
import androidx.appcompat.app.AppCompatActivity
import androidx.core.view.WindowCompat
import androidx.core.view.WindowInsetsCompat
import androidx.core.view.WindowInsetsControllerCompat
import androidx.fragment.app.Fragment
import androidx.lifecycle.lifecycleScope
import com.google.android.material.bottomnavigation.BottomNavigationView
import com.xiaobao.desk.R
import com.xiaobao.desk.core.ConnectionConfig
import com.xiaobao.desk.core.ConnectionPath
import com.xiaobao.desk.core.ConnectionState
import com.xiaobao.desk.core.XbConnectionManager
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONObject
import java.net.URL
import javax.net.ssl.HttpsURLConnection

class MainActivity : AppCompatActivity() {

    lateinit var connectionManager: XbConnectionManager
        private set

    var activeConfig: ConnectionConfig = ConnectionConfig()
        private set

    private lateinit var bottomNav: BottomNavigationView
    private lateinit var tvStatusBadge: TextView
    private lateinit var viewStatusDot: View
    private lateinit var topBar: LinearLayout

    private var isStreaming = false

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_main)

        connectionManager = XbConnectionManager(applicationContext)

        bottomNav = findViewById(R.id.bottomNav)
        tvStatusBadge = findViewById(R.id.tvConnectionBadge)
        viewStatusDot = findViewById(R.id.viewStatusDot)
        topBar = tvStatusBadge.parent as LinearLayout
        findViewById<TextView>(R.id.tvAppTitle).text = "XiaobaoDesk v1.5.0"

        if (savedInstanceState == null) {
            showTabFragment(ConnectionFragment())
        }

        bottomNav.setOnItemSelectedListener { item ->
            when (item.itemId) {
                R.id.nav_connect -> showTabFragment(ConnectionFragment())
                R.id.nav_terminal -> showTabFragment(TerminalFragment())
                R.id.nav_files -> showTabFragment(FileTransferFragment())
                R.id.nav_settings -> showTabFragment(SettingsFragment())
                else -> return@setOnItemSelectedListener false
            }
            true
        }

        lifecycleScope.launch {
            connectionManager.connectionState.collectLatest { state ->
                updateStatusBadge(state, connectionManager.connectionPath.value)
            }
        }
        lifecycleScope.launch {
            connectionManager.connectionPath.collectLatest { path ->
                updateStatusBadge(connectionManager.connectionState.value, path)
            }
        }

        handleImportIntent(intent)
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
        handleImportIntent(intent)
    }

    private fun handleImportIntent(intent: Intent?) {
        val uri = intent?.data ?: return
        if (uri.scheme != "xiaobaodesk" || uri.host != "import") return
        intent.data = null

        val inlineData = uri.getQueryParameter("data")?.trim().orEmpty()
        if (inlineData.isNotEmpty()) {
            try {
                val rawBytes = android.util.Base64.decode(
                    inlineData,
                    android.util.Base64.URL_SAFE or android.util.Base64.NO_WRAP or android.util.Base64.NO_PADDING
                )
                val profile = ConnectionProfile.fromJson(JSONObject(String(rawBytes, Charsets.UTF_8)))
                ConnectionProfileStore(applicationContext).upsert(profile)
                Toast.makeText(this, "已离线导入配置: ${profile.name}", Toast.LENGTH_LONG).show()
                if (isStreaming) {
                    exitStreaming()
                }
                bottomNav.selectedItemId = R.id.nav_connect
                showTabFragment(ConnectionFragment())
                supportFragmentManager.executePendingTransactions()
                val frag = supportFragmentManager.findFragmentById(R.id.fragmentContainer) as? ConnectionFragment
                frag?.reloadProfileOnly(profile)
            } catch (e: Exception) {
                Toast.makeText(this, "离线配置解析失败: ${e.message ?: "格式错误"}", Toast.LENGTH_LONG).show()
            }
            return
        }

        val claimUrl = uri.getQueryParameter("claim")?.trim().orEmpty()
        if (!claimUrl.startsWith("https://")) {
            Toast.makeText(this, "无效的导入链接", Toast.LENGTH_SHORT).show()
            return
        }

        Toast.makeText(this, "正在获取一次性配置...", Toast.LENGTH_SHORT).show()
        lifecycleScope.launch(Dispatchers.IO) {
            try {
                val conn = (URL(claimUrl).openConnection() as HttpsURLConnection).apply {
                    requestMethod = "GET"
                    connectTimeout = 8000
                    readTimeout = 8000
                    useCaches = false
                }
                val code = conn.responseCode
                if (code == 410) {
                    withContext(Dispatchers.Main) {
                        Toast.makeText(this@MainActivity, "该一次性导入链接已失效（已被使用）", Toast.LENGTH_LONG).show()
                    }
                    return@launch
                }
                if (code != 200) {
                    withContext(Dispatchers.Main) {
                        Toast.makeText(this@MainActivity, "导入失败: HTTP $code", Toast.LENGTH_LONG).show()
                    }
                    return@launch
                }
                val body = conn.inputStream.bufferedReader(Charsets.UTF_8).use { it.readText() }
                conn.disconnect()

                val profile = ConnectionProfile.fromJson(JSONObject(body))
                ConnectionProfileStore(applicationContext).upsert(profile)

                withContext(Dispatchers.Main) {
                    Toast.makeText(this@MainActivity, "配置已自动导入！请选择镜像或无线副屏模式后点击连接", Toast.LENGTH_LONG).show()
                    if (isStreaming) {
                        exitStreaming()
                    }
                    bottomNav.selectedItemId = R.id.nav_connect
                    showTabFragment(ConnectionFragment())
                    supportFragmentManager.executePendingTransactions()
                    val frag = supportFragmentManager.findFragmentById(R.id.fragmentContainer) as? ConnectionFragment
                    frag?.reloadProfileOnly(profile)
                }
            } catch (e: Exception) {
                withContext(Dispatchers.Main) {
                    Toast.makeText(this@MainActivity, "导入出错: ${e.message ?: "网络异常"}", Toast.LENGTH_LONG).show()
                }
            }
        }
    }

    private fun updateStatusBadge(state: ConnectionState, path: ConnectionPath) {
        val pathTag = when (path) {
            ConnectionPath.DIRECT_IPV6 -> " [IPv6]"
            ConnectionPath.DIRECT_LAN -> " [LAN]"
            ConnectionPath.RELAY_HUB -> " [Relay]"
            ConnectionPath.NONE -> ""
        }
        when (state) {
            ConnectionState.DISCONNECTED -> {
                tvStatusBadge.text = getString(R.string.status_disconnected)
                viewStatusDot.background.setTint(getColor(R.color.xb_text_muted))
            }
            ConnectionState.CONNECTING, ConnectionState.AUTHENTICATING -> {
                tvStatusBadge.text = getString(R.string.status_connecting)
                viewStatusDot.background.setTint(getColor(R.color.xb_warning))
            }
            ConnectionState.CONNECTED -> {
                tvStatusBadge.text = getString(R.string.status_connected) + pathTag
                viewStatusDot.background.setTint(getColor(R.color.xb_success))
            }
            ConnectionState.ERROR -> {
                tvStatusBadge.text = "Error"
                viewStatusDot.background.setTint(getColor(R.color.xb_error))
            }
        }
    }

    private fun showTabFragment(fragment: Fragment) {
        supportFragmentManager.beginTransaction()
            .replace(R.id.fragmentContainer, fragment)
            .commit()
    }

    fun startStreaming(config: ConnectionConfig) {
        activeConfig = config
        isStreaming = true
        topBar.visibility = View.GONE
        bottomNav.visibility = View.GONE
        setFullscreen(true)

        supportFragmentManager.beginTransaction()
            .replace(R.id.fragmentContainer, StreamingFragment(), "STREAMING")
            .addToBackStack("STREAMING")
            .commit()
    }

    fun exitStreaming() {
        if (!isStreaming) return
        isStreaming = false
        setFullscreen(false)
        topBar.visibility = View.VISIBLE
        bottomNav.visibility = View.VISIBLE
        if (supportFragmentManager.backStackEntryCount > 0) {
            supportFragmentManager.popBackStack()
        }
    }

    private fun setFullscreen(enable: Boolean) {
        val controller = WindowCompat.getInsetsController(window, window.decorView)
        if (enable) {
            controller.systemBarsBehavior = WindowInsetsControllerCompat.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE
            controller.hide(WindowInsetsCompat.Type.systemBars())
        } else {
            controller.show(WindowInsetsCompat.Type.systemBars())
        }
    }

    override fun dispatchKeyEvent(event: KeyEvent): Boolean {
        val streamFrag = supportFragmentManager.findFragmentByTag("STREAMING") as? StreamingFragment
        if (isStreaming && streamFrag != null && streamFrag.handleHardwareKeyEvent(event)) {
            return true
        }
        return super.dispatchKeyEvent(event)
    }

    override fun dispatchGenericMotionEvent(ev: MotionEvent): Boolean {
        val streamFrag = supportFragmentManager.findFragmentByTag("STREAMING") as? StreamingFragment
        if (isStreaming && streamFrag != null && streamFrag.handleGenericMotion(ev)) {
            return true
        }
        return super.dispatchGenericMotionEvent(ev)
    }

    override fun onBackPressed() {
        if (isStreaming) {
            exitStreaming()
            return
        }
        super.onBackPressed()
    }

    override fun onDestroy() {
        super.onDestroy()
        if (isFinishing) {
            connectionManager.release()
        }
    }
}
