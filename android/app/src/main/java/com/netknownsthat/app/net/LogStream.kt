package com.netknownsthat.app.net

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import com.netknownsthat.app.i18n.t

/**
 * A followed log over a WebSocket.
 *
 * Unlike the terminal (see terminal/TerminalSession.kt) this carries TEXT
 * frames of whole lines — the server batches complete lines and never splits
 * one across frames (handleLogStream), so there is no reassembly to do and
 * filtering can work per line.
 */
class LogStream(
    private val client: OkHttpClient,
    private val url: String,
    private val maxLines: Int = 5000,
) {
    /** Kept as a snapshot list so Compose sees each append. */
    val lines = mutableStateListOf<String>()

    var connected by mutableStateOf(false)
        private set
    var error by mutableStateOf<String?>(null)
        private set

    private var socket: WebSocket? = null
    private val scope = CoroutineScope(Dispatchers.Main.immediate)

    fun connect() {
        close()
        lines.clear()
        error = null

        socket = client.newWebSocket(Request.Builder().url(url).build(), object : WebSocketListener() {
            override fun onOpen(webSocket: WebSocket, response: Response) {
                scope.launch { connected = true }
            }

            override fun onMessage(webSocket: WebSocket, text: String) {
                val incoming = text.split('\n')
                scope.launch {
                    lines.addAll(incoming)
                    // A followed log is unbounded; the process must not grow
                    // with it until it is killed.
                    if (lines.size > maxLines) {
                        lines.removeRange(0, lines.size - maxLines)
                    }
                }
            }

            override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
                scope.launch { connected = false }
            }

            override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                scope.launch {
                    connected = false
                    error = when {
                        response?.code == 400 -> t("Источник не подходит: ${response.message}", "Source not accepted: ${response.message}")
                        response != null -> t("Не удалось подключиться: ${response.code}", "Could not connect: ${response.code}")
                        else -> t.message ?: t("Соединение разорвано", "Connection lost")
                    }
                }
            }
        })
    }

    fun close() {
        socket?.close(1000, null)
        socket = null
        connected = false
    }
}
