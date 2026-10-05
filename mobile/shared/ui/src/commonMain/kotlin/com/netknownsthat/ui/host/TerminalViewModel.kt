package com.netknownsthat.ui.host

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.netknownsthat.domain.common.AppError
import com.netknownsthat.domain.common.HostTarget
import com.netknownsthat.domain.common.Outcome
import com.netknownsthat.domain.common.getOrNull
import com.netknownsthat.domain.repository.StreamEvent
import com.netknownsthat.domain.repository.TerminalChannel
import com.netknownsthat.domain.repository.TerminalMode
import com.netknownsthat.domain.repository.TerminalRepository
import com.netknownsthat.domain.usecase.OpenTerminalUseCase
import com.netknownsthat.terminal.TerminalEmulator
import com.netknownsthat.ui.common.text
import com.netknownsthat.ui.i18n.t
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

enum class TerminalStatus { CONNECTING, CONNECTED, RECONNECTING, CLOSED, FAILED }

/**
 * A live shell (or btop) on a host.
 *
 * tmux when the host has it: the session then survives a dropped connection,
 * and the view model re-attaches on its own — on a phone a lost network is
 * ordinary. Without tmux a plain shell opens and the screen offers to
 * install it, instead of failing outright (what made the old terminal
 * "sometimes not work").
 *
 * Output is fed into the emulator on the main thread; [revision] bumps on
 * every change, and Compose coalesces bursts into one redraw per frame.
 */
class TerminalViewModel(
    private val host: HostTarget,
    private val btop: Boolean,
    private val openTerminal: OpenTerminalUseCase,
    private val terminal: TerminalRepository,
) : ViewModel() {
    val emulator = TerminalEmulator()

    var revision by mutableIntStateOf(0)
        private set
    var status by mutableStateOf(TerminalStatus.CONNECTING)
        private set
    var error by mutableStateOf<String?>(null)
        private set
    var mode by mutableStateOf<TerminalMode?>(null)
        private set
    var tmuxMissing by mutableStateOf(false)
        private set
    var tmuxMouse by mutableStateOf<Boolean?>(null)
        private set
    var message by mutableStateOf<String?>(null)

    private var channel: TerminalChannel? = null
    private var session: Job? = null
    private var closedByUser = false

    /** Last grid the screen asked for — re-sent on every (re)connect. */
    private var columns = 0
    private var rows = 0

    fun start() {
        closedByUser = false
        session?.cancel()
        session = viewModelScope.launch {
            var attempt = 0
            while (true) {
                status = if (attempt == 0) TerminalStatus.CONNECTING else TerminalStatus.RECONNECTING
                val opened = openTerminal(host, btop)
                mode = opened.mode
                tmuxMissing = opened.tmuxMissing
                channel = opened.channel
                if (opened.mode == TerminalMode.TMUX) refreshMouse()
                var failure: AppError? = null
                var wasOpen = false
                opened.channel.events.collect { e ->
                    when (e) {
                        StreamEvent.Opened -> {
                            wasOpen = true
                            attempt = 0
                            error = null
                            status = TerminalStatus.CONNECTED
                            emulator.reset()
                            revision++
                            // The PTY starts at a default size; full-screen
                            // programs draw for it until told otherwise.
                            if (columns > 0) opened.channel.resize(columns, rows)
                        }
                        is StreamEvent.Bytes -> {
                            emulator.feed(e.data)
                            revision++
                        }
                        is StreamEvent.Text -> Unit
                        is StreamEvent.Closed -> failure = e.error
                    }
                }
                channel = null
                if (closedByUser) return@launch
                // A tmux session outlives the socket: re-attach, a few tries
                // with growing pauses. A plain shell died with the socket.
                val retry = opened.mode == TerminalMode.TMUX && failure !is AppError.Server && attempt < MAX_RETRIES
                if (!retry) {
                    error = failure?.text()
                    status = if (failure != null && !wasOpen) TerminalStatus.FAILED else TerminalStatus.CLOSED
                    revision++
                    return@launch
                }
                attempt++
                status = TerminalStatus.RECONNECTING
                delay(1_000L * attempt)
            }
        }
    }

    fun stop() {
        closedByUser = true
        channel?.close()
        session?.cancel()
        channel = null
    }

    fun resize(columns: Int, rows: Int) {
        if (columns <= 0 || rows <= 0) return
        this.columns = columns
        this.rows = rows
        emulator.resize(columns, rows)
        revision++
        viewModelScope.launch { channel?.resize(columns, rows) }
    }

    fun send(bytes: ByteArray) {
        viewModelScope.launch { channel?.send(bytes) }
    }

    fun sendText(text: String) = send(text.encodeToByteArray())

    /** tmux prefix (Ctrl+B) followed by [key] — what the tmux menu sends. */
    fun tmux(key: String) {
        viewModelScope.launch {
            channel?.send(byteArrayOf(0x02))
            channel?.send(key.encodeToByteArray())
        }
    }

    private fun refreshMouse() {
        viewModelScope.launch { tmuxMouse = terminal.tmuxMouse(host).getOrNull() }
    }

    fun toggleMouse() {
        val on = tmuxMouse ?: return
        viewModelScope.launch {
            when (val r = terminal.setTmuxMouse(host, !on)) {
                is Outcome.Success -> tmuxMouse = r.value
                is Outcome.Failure -> message = r.error.text()
            }
        }
    }

    // ---- tmux install ------------------------------------------------------

    /** Output of the running install, drawn by its own emulator. */
    val installEmulator = TerminalEmulator(columns = 80, rows = 20)
    var installRevision by mutableIntStateOf(0)
        private set
    var installing by mutableStateOf(false)
        private set
    var installResult by mutableStateOf<Boolean?>(null)
        private set

    fun installTmux() {
        if (installing) return
        installing = true
        installResult = null
        installEmulator.reset()
        viewModelScope.launch {
            var failure: AppError? = null
            terminal.installTmux(host).events.collect { e ->
                when (e) {
                    is StreamEvent.Bytes -> { installEmulator.feed(e.data); installRevision++ }
                    is StreamEvent.Closed -> failure = e.error
                    else -> Unit
                }
            }
            // The outcome is not in the stream: the host keeps it (status).
            val ok = failure == null && terminal.tmuxInstallStatus(host).getOrNull()?.succeeded == true
            installResult = ok
            installing = false
            message = if (ok) t("tmux установлен — открываю сессию в tmux", "tmux installed — opening a tmux session")
            else failure?.text() ?: t("Установка tmux не удалась", "tmux installation failed")
            if (ok) {
                stop()
                start()
            }
        }
    }

    override fun onCleared() {
        stop()
    }

    private companion object {
        const val MAX_RETRIES = 5
    }
}
