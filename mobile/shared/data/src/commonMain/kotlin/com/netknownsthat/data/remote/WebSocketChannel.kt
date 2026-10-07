package com.netknownsthat.data.remote

import com.netknownsthat.domain.common.AppError
import com.netknownsthat.domain.repository.StreamEvent
import com.netknownsthat.domain.repository.TerminalChannel
import io.ktor.client.plugins.websocket.DefaultClientWebSocketSession
import io.ktor.client.plugins.websocket.webSocketSession
import io.ktor.websocket.CloseReason
import io.ktor.websocket.Frame
import io.ktor.websocket.close
import io.ktor.websocket.readText
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.channels.awaitClose
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.callbackFlow
import kotlinx.coroutines.launch

/**
 * One WebSocket to the hub (terminal, followed log, install output).
 * Collecting [events] connects; the session cookie and the language header
 * come with the upgrade request like with any other call. Output frames are
 * binary for PTYs and text for logs; the only control message the server
 * understands is the terminal resize, sent as a text frame.
 */
class WebSocketChannel(
    private val api: ApiClient,
    private val path: String,
) : TerminalChannel {
    private var session: DefaultClientWebSocketSession? = null
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)

    override val events: Flow<StreamEvent> = callbackFlow {
        val url = api.webSocketUrl(path)
        if (url == null) {
            send(StreamEvent.Closed(AppError.NotConfigured))
            close()
            return@callbackFlow
        }
        val job = launch {
            val ws = try {
                api.http.webSocketSession(url)
            } catch (e: CancellationException) {
                throw e
            } catch (e: Throwable) {
                send(StreamEvent.Closed(refusal(e)))
                this@callbackFlow.close()
                return@launch
            }
            session = ws
            send(StreamEvent.Opened)
            try {
                for (frame in ws.incoming) {
                    when (frame) {
                        is Frame.Binary -> send(StreamEvent.Bytes(frame.data))
                        is Frame.Text -> send(StreamEvent.Text(frame.readText()))
                        else -> Unit
                    }
                }
                val reason = runCatching { ws.closeReason.await() }.getOrNull()
                // A close with a reason other than "normal" carries the
                // server's explanation (e.g. "terminal disabled").
                val error = reason?.takeIf { it.code != CloseReason.Codes.NORMAL.code && it.message.isNotBlank() }
                    ?.let { AppError.Server(it.code.toInt(), it.message) }
                send(StreamEvent.Closed(error))
            } catch (e: CancellationException) {
                throw e
            } catch (e: Throwable) {
                send(StreamEvent.Closed(api.networkError(e)))
            }
            session = null
            this@callbackFlow.close()
        }
        awaitClose {
            job.cancel()
            val ws = session
            session = null
            if (ws != null) scope.launch { runCatching { ws.close() } }
        }
    }

    /**
     * Why the upgrade failed. A refused upgrade only says "expected 101,
     * got 403"; the server's reason ("the terminal is disabled", "no such
     * container") is in the body, which the WebSocket client drops. The
     * same path as a plain GET returns it: the handlers check before they
     * upgrade, so nothing is started by asking.
     */
    private suspend fun refusal(e: Throwable): AppError {
        val network = api.networkError(e)
        val probe = api.call(io.ktor.http.HttpMethod.Get, path)
        val error = (probe as? com.netknownsthat.domain.common.Outcome.Failure)?.error
        // 426: the checks passed and the GET reached the upgrade itself —
        // the failure was the connection, not a refusal.
        val refused = (error is AppError.Server && error.status != 426) || error is AppError.Unauthorized
        return if (refused) error!! else network
    }

    override suspend fun send(bytes: ByteArray) {
        runCatching { session?.send(Frame.Binary(true, bytes)) }
    }

    override suspend fun sendText(text: String) {
        runCatching { session?.send(Frame.Text(text)) }
    }

    override suspend fun resize(columns: Int, rows: Int) {
        if (columns <= 0 || rows <= 0) return
        sendText("""{"type":"resize","cols":$columns,"rows":$rows}""")
    }

    override fun close() {
        val ws = session
        session = null
        if (ws != null) scope.launch { runCatching { ws.close() } }
    }
}
