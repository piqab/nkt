package com.netknownsthat.ui.host

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.netknownsthat.domain.common.HostTarget
import com.netknownsthat.domain.common.Outcome
import com.netknownsthat.domain.common.getOrNull
import com.netknownsthat.domain.model.LogSource
import com.netknownsthat.domain.repository.LogsRepository
import com.netknownsthat.domain.repository.StreamEvent
import com.netknownsthat.ui.common.text
import com.netknownsthat.ui.i18n.t
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch

/** A followed log: its lines (bounded) and whether the stream is live. */
class LogFollowState {
    val lines = mutableStateListOf<String>()
    var connected by mutableStateOf(false)
    var error by mutableStateOf<String?>(null)
}

class LogsViewModel(private val host: HostTarget, private val repo: LogsRepository) : ViewModel() {
    var sources by mutableStateOf<List<LogSource>>(emptyList())
        private set
    var root by mutableStateOf("/var/log")
        private set
    var stream by mutableStateOf<LogFollowState?>(null)
        private set
    var currentLabel by mutableStateOf<String?>(null)
        private set
    private var current: LogSource? = null
    var snapshot by mutableStateOf<List<String>>(emptyList())
        private set
    var snapshotError by mutableStateOf<String?>(null)
        private set
    var lineCount by mutableStateOf(500)
        private set
    private var follower: Job? = null

    val isArchived: Boolean get() = current?.archived == true

    fun setLines(n: Int) {
        lineCount = n
        restart()
    }

    fun loadSources() {
        viewModelScope.launch {
            repo.sources(host).getOrNull()?.let { (list, r) ->
                sources = list
                root = r
            }
        }
    }

    fun watch(source: LogSource) {
        current = source
        currentLabel = if (source.kind == "unit") t("журнал: ${source.name}", "log: ${source.name}") else source.name
        restart()
    }

    fun restart() {
        val source = current ?: return
        stop()
        snapshot = emptyList()
        snapshotError = null
        // An archive does not grow: read once (decompressed on the host).
        if (source.archived) {
            viewModelScope.launch {
                when (val r = repo.tail(host, source, lineCount)) {
                    is Outcome.Success -> snapshot = r.value.lines()
                    is Outcome.Failure -> snapshotError = r.error.text()
                }
            }
            return
        }
        val state = LogFollowState()
        stream = state
        follower = viewModelScope.launch {
            repo.follow(host, source, lineCount).events.collect { e ->
                when (e) {
                    StreamEvent.Opened -> state.connected = true
                    is StreamEvent.Text -> {
                        state.lines.addAll(e.text.split('\n'))
                        // A followed log is unbounded; the process must not grow with it.
                        if (state.lines.size > MAX_LINES) state.lines.removeRange(0, state.lines.size - MAX_LINES)
                    }
                    is StreamEvent.Bytes -> state.lines.addAll(e.data.decodeToString().split('\n'))
                    is StreamEvent.Closed -> {
                        state.connected = false
                        state.error = e.error?.text()
                    }
                }
            }
        }
    }

    fun stop() {
        follower?.cancel()
        follower = null
        stream?.connected = false
    }

    private companion object {
        const val MAX_LINES = 5_000
    }
}
