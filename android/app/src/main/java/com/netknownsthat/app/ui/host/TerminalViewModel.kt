package com.netknownsthat.app.ui.host

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.lifecycle.ViewModel
import com.netknownsthat.app.net.HubClient
import com.netknownsthat.app.terminal.TerminalSession

/**
 * Owns the live PTY sessions. Separate from SectionViewModel because a
 * terminal has nothing to fetch and nothing to refresh — it is a socket,
 * not a document.
 */
class TerminalViewModel(private val hubClient: HubClient) : ViewModel() {
    var session by mutableStateOf<TerminalSession?>(null)
        private set

    /**
     * [tmux] asks the host to wrap the shell in tmux, which is what makes a
     * session survive the socket dropping — worth defaulting to on a phone,
     * where losing the network mid-command is ordinary rather than
     * exceptional. btop ignores it: it is its own program, not a shell.
     */
    fun start(btop: Boolean, tmux: Boolean = true) {
        stop()
        val path = when {
            btop -> "/terminal/btop/ws"
            tmux -> "/terminal/ws?tmux=1"
            else -> "/terminal/ws"
        }
        val url = hubClient.webSocketUrl(path) ?: return
        session = TerminalSession(hubClient.okHttpClient(), url).also { it.connect() }
    }

    fun stop() {
        session?.close()
        session = null
    }

    override fun onCleared() {
        stop()
    }
}
