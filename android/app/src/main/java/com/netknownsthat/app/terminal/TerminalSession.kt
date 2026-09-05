package com.netknownsthat.app.terminal

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
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
import okio.ByteString
import okio.ByteString.Companion.toByteString

/**
 * One PTY over a WebSocket, wired to a [TerminalEmulator].
 *
 * The wire format is the whole protocol (see internal/api/pty_session.go):
 * process output and keystrokes are BINARY frames of raw bytes, and the
 * single TEXT frame is `{"type":"resize","cols":N,"rows":N}`. There is
 * nothing else — no envelope, no acknowledgements.
 *
 * Authentication is the ordinary `nkt_session` cookie: OkHttp's own
 * CookieJar applies to the upgrade request like any other, so sharing the
 * app's configured client (see HubClient.okHttpClient) is what makes the
 * socket authenticate at all.
 */
class TerminalSession(
    private val client: OkHttpClient,
    private val url: String,
    val emulator: TerminalEmulator = TerminalEmulator(),
) {
    enum class State { IDLE, CONNECTING, CONNECTED, CLOSED, FAILED }

    var state by mutableStateOf(State.IDLE)
        private set
    var error by mutableStateOf<String?>(null)
        private set

    /**
     * Bumped whenever the screen changes. Compose has no way to observe
     * mutations inside the emulator's arrays, so this counter is what makes
     * the renderer recompose.
     */
    var revision by mutableIntStateOf(0)
        private set

    private var socket: WebSocket? = null

    // Emulator state is touched only from the main thread: OkHttp delivers
    // callbacks on its own dispatcher, and the parser holds enough mutable
    // state (cursor, wrap, partial UTF-8) that sharing it across threads
    // would corrupt the screen in ways that are miserable to reproduce.
    private val scope = CoroutineScope(Dispatchers.Main.immediate)

    fun connect() {
        if (state == State.CONNECTING || state == State.CONNECTED) return
        state = State.CONNECTING
        error = null
        emulator.reset()
        revision++

        val request = Request.Builder().url(url).build()
        socket = client.newWebSocket(request, object : WebSocketListener() {
            override fun onOpen(webSocket: WebSocket, response: Response) {
                scope.launch {
                    state = State.CONNECTED
                    // The server starts the PTY at a default size, so the
                    // real one has to be announced immediately — otherwise
                    // a full-screen program draws itself for 80x24 and only
                    // corrects itself if something else happens to resize.
                    sendResize(emulator.columns, emulator.rows)
                }
            }

            override fun onMessage(webSocket: WebSocket, bytes: ByteString) {
                val data = bytes.toByteArray()
                scope.launch {
                    emulator.feed(data)
                    revision++
                }
            }

            override fun onMessage(webSocket: WebSocket, text: String) {
                // The server never sends text frames; ignoring one is
                // better than feeding it to the screen as if it were output.
            }

            override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
                scope.launch {
                    state = State.CLOSED
                    revision++
                }
            }

            override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                scope.launch {
                    state = State.FAILED
                    error = when {
                        response?.code == 403 ->
                            "Терминал отключён на этом хосте (NKT_TERMINAL_ENABLED)"

                        response != null -> "Не удалось подключиться: ${response.code}"
                        else -> t.message ?: "Соединение разорвано"
                    }
                    revision++
                }
            }
        })
    }

    /** Text typed by the operator, sent as the raw UTF-8 bytes a keyboard
     * would produce. */
    fun sendText(text: String) {
        send(text.toByteArray(Charsets.UTF_8))
    }

    fun send(bytes: ByteArray) {
        socket?.send(bytes.toByteString())
    }

    fun sendResize(columns: Int, rows: Int) {
        if (columns <= 0 || rows <= 0) return
        emulator.resize(columns, rows)
        revision++
        socket?.send("""{"type":"resize","cols":$columns,"rows":$rows}""")
    }

    fun close() {
        socket?.close(1000, null)
        socket = null
        if (state != State.FAILED) state = State.CLOSED
    }
}

/**
 * Byte sequences for the keys a phone keyboard does not have but a terminal
 * needs constantly. Arrow keys use the "normal" (CSI) forms rather than the
 * application-cursor (SS3) ones — that is what a shell without an active
 * full-screen program expects, and what the web terminal sends too.
 */
object TerminalKeys {
    val ESC = byteArrayOf(0x1B)
    val TAB = byteArrayOf(0x09)
    val ENTER = byteArrayOf(0x0D)
    val BACKSPACE = byteArrayOf(0x7F)
    val UP = "\u001b[A".toByteArray()
    val DOWN = "\u001b[B".toByteArray()
    val RIGHT = "\u001b[C".toByteArray()
    val LEFT = "\u001b[D".toByteArray()
    val HOME = "\u001b[H".toByteArray()
    val END = "\u001b[F".toByteArray()
    val PAGE_UP = "\u001b[5~".toByteArray()
    val PAGE_DOWN = "\u001b[6~".toByteArray()

    /** Ctrl+<letter>: the control code is the letter with its top bits
     * cleared, which is all "control" ever meant on a teletype. */
    fun ctrl(letter: Char): ByteArray {
        val upper = letter.uppercaseChar()
        return byteArrayOf(((upper.code) and 0x1F).toByte())
    }
}
