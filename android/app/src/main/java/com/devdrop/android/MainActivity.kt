package com.devdrop.android

import android.Manifest
import android.content.BroadcastReceiver
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.provider.OpenableColumns
import android.view.View
import android.widget.AdapterView
import android.widget.ArrayAdapter
import android.widget.Button
import android.widget.EditText
import android.widget.Spinner
import android.widget.TextView
import android.widget.Toast
import androidx.activity.result.contract.ActivityResultContracts
import androidx.appcompat.app.AppCompatActivity
import androidx.core.app.ActivityCompat
import androidx.recyclerview.widget.LinearLayoutManager
import androidx.recyclerview.widget.RecyclerView
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import org.json.JSONObject

class MainActivity : AppCompatActivity() {

    private val http = OkHttpClient()

    private lateinit var loginGroup: View
    private lateinit var mainGroup: View
    private lateinit var etServer: EditText
    private lateinit var etEmail: EditText
    private lateinit var etPassword: EditText
    private lateinit var tvStatus: TextView
    private lateinit var tvDevice: TextView
    private lateinit var tvSettingsDevice: TextView
    private lateinit var etServerMain: EditText
    private lateinit var etSend: EditText
    private lateinit var historyList: RecyclerView
    private lateinit var historyAdapter: HistoryAdapter

    private var lastConnected = false
    private var lastDetail = ""

    companion object {
        private const val MAX_FILE_BYTES = 25L * 1024 * 1024
    }

    private val filePicker = registerForActivityResult(ActivityResultContracts.GetContent()) { uri: Uri? ->
        if (uri != null) handlePickedFile(uri)
    }

    private val uiReceiver = object : BroadcastReceiver() {
        override fun onReceive(ctx: Context, intent: Intent) {
            when (intent.action) {
                DevDropService.BC_STATUS -> {
                    lastConnected = intent.getBooleanExtra(DevDropService.EXTRA_CONNECTED, false)
                    lastDetail = intent.getStringExtra(DevDropService.EXTRA_DETAIL) ?: ""
                    renderStatus()
                }
                DevDropService.BC_HISTORY -> refreshHistory()
            }
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_main)

        loginGroup = findViewById(R.id.loginGroup)
        mainGroup = findViewById(R.id.mainGroup)
        etServer = findViewById(R.id.etServer)
        etEmail = findViewById(R.id.etEmail)
        etPassword = findViewById(R.id.etPassword)
        tvStatus = findViewById(R.id.tvStatus)
        tvDevice = findViewById(R.id.tvDevice)
        tvSettingsDevice = findViewById(R.id.tvSettingsDevice)
        etServerMain = findViewById(R.id.etServerMain)
        etSend = findViewById(R.id.etSend)
        historyList = findViewById(R.id.historyList)

        historyAdapter = HistoryAdapter { item -> handleHistoryTap(item) }
        historyList.layoutManager = LinearLayoutManager(this)
        historyList.adapter = historyAdapter

        findViewById<Button>(R.id.btnLogin).setOnClickListener { doAuth(registerFirst = false) }
        findViewById<Button>(R.id.btnRegister).setOnClickListener { doAuth(registerFirst = true) }
        findViewById<Button>(R.id.btnSendClipboard).setOnClickListener { sendCurrentClipboard() }
        findViewById<Button>(R.id.btnDevices).setOnClickListener {
            startActivity(Intent(this, DevicesActivity::class.java))
        }
        findViewById<Button>(R.id.btnSendFile).setOnClickListener {
            try {
                filePicker.launch("*/*")
            } catch (e: Exception) {
                Toast.makeText(this, "No file picker available", Toast.LENGTH_SHORT).show()
            }
        }
        findViewById<Button>(R.id.btnSendText).setOnClickListener {
            val t = etSend.text.toString()
            if (t.isEmpty()) return@setOnClickListener
            DevDropService.pushText(this, t)
            etSend.setText("")
            Toast.makeText(this, "Sending…", Toast.LENGTH_SHORT).show()
        }
        findViewById<Button>(R.id.btnLogout).setOnClickListener {
            DevDropService.stop(this)
            Store.clearCredentials(this)
            showLogin()
        }
        findViewById<Button>(R.id.btnSaveServer).setOnClickListener { saveServerUrl() }

        val ttlLabels = arrayOf("1 minute", "30 minutes", "1 hour", "1 day", "7 days", "30 days")
        val ttlValues = intArrayOf(60, 1800, 3600, 86400, 604800, 2592000)
        val spinner: Spinner = findViewById(R.id.ttlSpinner)
        spinner.adapter = ArrayAdapter(this, android.R.layout.simple_spinner_item, ttlLabels).also {
            it.setDropDownViewResource(android.R.layout.simple_spinner_dropdown_item)
        }
        val cur = Store.getTtlS(this)
        spinner.setSelection(ttlValues.indexOf(cur).takeIf { it >= 0 } ?: 1, false)
        spinner.onItemSelectedListener = object : AdapterView.OnItemSelectedListener {
            override fun onItemSelected(p: AdapterView<*>?, v: View?, pos: Int, id: Long) {
                Store.setTtlS(this@MainActivity, ttlValues[pos])
            }
            override fun onNothingSelected(p: AdapterView<*>?) {}
        }

        etServer.setText(Store.getServerUrl(this))
        if (Store.isLoggedIn(this)) {
            showMain()
            DevDropService.start(this)
        } else {
            showLogin()
        }
        requestNotifPermission()
    }

    override fun onResume() {
        super.onResume()
        val f = IntentFilter().apply {
            addAction(DevDropService.BC_STATUS)
            addAction(DevDropService.BC_HISTORY)
        }
        if (Build.VERSION.SDK_INT >= 33) {
            registerReceiver(uiReceiver, f, RECEIVER_NOT_EXPORTED)
        } else {
            @Suppress("UnspecifiedRegisterReceiverFlag")
            registerReceiver(uiReceiver, f)
        }
        if (Store.isLoggedIn(this)) refreshHistory()
        renderStatus()
    }

    override fun onPause() {
        super.onPause()
        try { unregisterReceiver(uiReceiver) } catch (e: Exception) { /* ignore */ }
    }

    // ---- UI states ----

    private fun showLogin() {
        loginGroup.visibility = View.VISIBLE
        mainGroup.visibility = View.GONE
    }

    private fun showMain() {
        loginGroup.visibility = View.GONE
        mainGroup.visibility = View.VISIBLE
        tvDevice.text = "Device: ${Store.getDeviceName(this)}\n${Store.getServerUrl(this)}"
        tvSettingsDevice.text = "This phone: ${Store.getDeviceName(this)}"
        etServerMain.setText(Store.getServerUrl(this))
        refreshHistory()
    }

    private fun renderStatus() {
        if (!Store.isLoggedIn(this)) return
        val dot = if (lastConnected) "🟢" else "🔴"
        tvStatus.text = "$dot $lastDetail".trim()
    }

    private fun refreshHistory() {
        historyAdapter.submit(Store.getHistory(this))
        findViewById<TextView>(R.id.tvEmpty).visibility =
            if (historyAdapter.itemCount == 0) View.VISIBLE else View.GONE
    }

    /** Settings card: save a new server address and reconnect immediately. */
    private fun saveServerUrl() {
        val raw = etServerMain.text.toString().trim().trimEnd('/')
        if (raw.isEmpty()) {
            Toast.makeText(this, "Enter a server address", Toast.LENGTH_SHORT).show()
            return
        }
        if (!raw.startsWith("http://") && !raw.startsWith("https://")) {
            Toast.makeText(this, "Server address must start with http:// or https://", Toast.LENGTH_LONG).show()
            return
        }
        Store.setServerUrl(this, raw)
        val saved = Store.getServerUrl(this)
        etServerMain.setText(saved)
        tvDevice.text = "Device: ${Store.getDeviceName(this)}\n$saved"
        Toast.makeText(this, "Server saved — reconnecting…", Toast.LENGTH_SHORT).show()
        // Restart the service connection so the new address takes effect now.
        try {
            DevDropService.stop(this)
        } catch (e: Exception) { /* ignore */ }
        DevDropService.start(this)
    }

    // ---- actions ----

    private fun sendCurrentClipboard() {
        // We are foreground here, so reading is allowed.
        val cm = getSystemService(CLIPBOARD_SERVICE) as ClipboardManager
        val text = try {
            if (!cm.hasPrimaryClip()) null
            else cm.primaryClip
                ?.takeIf { it.itemCount > 0 }
                ?.getItemAt(0)?.coerceToText(this)?.toString()
        } catch (e: Exception) { null }
        if (text.isNullOrBlank()) {
            Toast.makeText(this, "Clipboard is empty or not text", Toast.LENGTH_SHORT).show()
            return
        }
        DevDropService.pushText(this, text)
        Toast.makeText(this, "Sending…", Toast.LENGTH_SHORT).show()
    }

    private fun copyToClipboard(text: String) {
        val cm = getSystemService(CLIPBOARD_SERVICE) as ClipboardManager
        cm.setPrimaryClip(ClipData.newPlainText("CrossShare", text))
        Toast.makeText(this, "Copied", Toast.LENGTH_SHORT).show()
    }

    private fun handleHistoryTap(item: Store.HistoryItem) {
        if (item.kind == "text") {
            copyToClipboard(item.text)
            return
        }
        // File/image row: open via VIEW intent.
        if (item.filepath.isBlank()) {
            Toast.makeText(this, "File not saved on this device", Toast.LENGTH_SHORT).show()
            return
        }
        try {
            val uri = Uri.parse(item.filepath)
            val mime = contentResolver.getType(uri) ?: "*/*"
            val view = Intent(Intent.ACTION_VIEW).apply {
                setDataAndType(uri, mime)
                addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
            }
            startActivity(Intent.createChooser(view, "Open file"))
        } catch (e: Exception) {
            Toast.makeText(this, "Can't open file: ${e.message}", Toast.LENGTH_LONG).show()
        }
    }

    private fun handlePickedFile(uri: Uri) {
        Thread {
            try {
                var filename = "file"
                contentResolver.query(uri, null, null, null, null)?.use { c ->
                    val idx = c.getColumnIndex(OpenableColumns.DISPLAY_NAME)
                    if (idx >= 0 && c.moveToFirst()) {
                        c.getString(idx)?.takeIf { it.isNotBlank() }?.let { filename = it }
                    }
                }
                var mime = contentResolver.getType(uri) ?: "application/octet-stream"
                if (mime.isBlank()) mime = "application/octet-stream"
                // Read with a 25MB cap.
                val bytes = contentResolver.openInputStream(uri)?.use { ins ->
                    val out = java.io.ByteArrayOutputStream()
                    val buf = ByteArray(64 * 1024)
                    var total = 0L
                    while (true) {
                        val n = ins.read(buf)
                        if (n < 0) break
                        total += n
                        if (total > MAX_FILE_BYTES) return@use null
                        out.write(buf, 0, n)
                    }
                    out.toByteArray()
                }
                if (bytes == null) {
                    runOnUiThread {
                        Toast.makeText(this, "File too big (max 25MB)", Toast.LENGTH_LONG).show()
                    }
                    return@Thread
                }
                val name = filename
                val type = mime
                if (!lastConnected) {
                    runOnUiThread {
                        Toast.makeText(this, "Not connected — will send on reconnect", Toast.LENGTH_LONG).show()
                    }
                } else {
                    runOnUiThread {
                        Toast.makeText(this, "Sending $name…", Toast.LENGTH_SHORT).show()
                    }
                }
                // Service sends inline (<=1MB) or via /api/blobs, or stashes
                // in its in-memory pending slot when offline.
                DevDropService.pushFile(this, name, type, bytes)
            } catch (e: Exception) {
                runOnUiThread {
                    Toast.makeText(this, "Could not read file: ${e.message}", Toast.LENGTH_LONG).show()
                }
            }
        }.start()
    }

    private fun requestNotifPermission() {
        if (Build.VERSION.SDK_INT >= 33 &&
            checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        ) {
            ActivityCompat.requestPermissions(
                this, arrayOf(Manifest.permission.POST_NOTIFICATIONS), 1
            )
        }
    }

    // ---- auth (plain background thread + runOnUiThread, no extra deps) ----

    private fun doAuth(registerFirst: Boolean) {
        val server = etServer.text.toString().trim().trimEnd('/')
        val email = etEmail.text.toString().trim()
        val password = etPassword.text.toString()
        if (server.isEmpty() || email.isEmpty() || password.isEmpty()) {
            Toast.makeText(this, "Fill server, email and password", Toast.LENGTH_SHORT).show()
            return
        }
        setBusy(true)
        Thread {
            try {
                if (registerFirst) {
                    val (code, _) = post(
                        "$server/api/register",
                        JSONObject().put("email", email).put("password", password).toString()
                    )
                    if (code != 200 && code != 201) {
                        fail("Register failed (HTTP $code — maybe account exists, try Log in)")
                        return@Thread
                    }
                }
                val deviceName = Build.MODEL?.takeIf { it.isNotBlank() } ?: "Android"
                val (code, body) = post(
                    "$server/api/login",
                    JSONObject()
                        .put("email", email)
                        .put("password", password)
                        .put("device_name", deviceName)
                        .put("device_os", "android")
                        .toString()
                )
                if (code != 200) {
                    fail("Login failed (HTTP $code)")
                    return@Thread
                }
                val obj = JSONObject(body)
                Store.saveLogin(
                    this, server,
                    obj.getString("device_id"), obj.getString("token"), deviceName
                )
                runOnUiThread {
                    setBusy(false)
                    showMain()
                    DevDropService.start(this)
                    Toast.makeText(this, "Logged in", Toast.LENGTH_SHORT).show()
                }
            } catch (e: Exception) {
                fail("Network error: ${e.message}")
            }
        }.start()
    }

    private fun post(url: String, json: String): Pair<Int, String> {
        val req = Request.Builder()
            .url(url)
            .post(json.toRequestBody("application/json".toMediaType()))
            .build()
        http.newCall(req).execute().use { resp ->
            return Pair(resp.code, resp.body?.string() ?: "")
        }
    }

    private fun fail(msg: String) {
        runOnUiThread {
            setBusy(false)
            Toast.makeText(this, msg, Toast.LENGTH_LONG).show()
        }
    }

    private fun setBusy(busy: Boolean) {
        findViewById<Button>(R.id.btnLogin).isEnabled = !busy
        findViewById<Button>(R.id.btnRegister).isEnabled = !busy
    }
}
