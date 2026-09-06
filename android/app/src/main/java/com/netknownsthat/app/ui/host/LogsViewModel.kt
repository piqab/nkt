package com.netknownsthat.app.ui.host

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.netknownsthat.app.net.HubClient
import com.netknownsthat.app.net.LogStream
import com.netknownsthat.app.net.model.LogSource
import com.netknownsthat.app.net.model.LogSourcesResponse
import com.netknownsthat.app.net.model.LogTailResponse
import kotlinx.coroutines.launch
import java.net.URLEncoder

/**
 * Owns the log list and the one open stream. Not a SectionViewModel: there
 * is nothing to "refresh" — the socket either carries the log or it does not.
 */
class LogsViewModel(private val hubClient: HubClient) : ViewModel() {
    var sources by mutableStateOf<List<LogSource>>(emptyList())
        private set
    var root by mutableStateOf("/var/log")
        private set
    var stream by mutableStateOf<LogStream?>(null)
        private set
    var currentLabel by mutableStateOf<String?>(null)
        private set

    private var current: LogSource? = null

    /** Lines held for an archive, which has no stream behind it. */
    var snapshot by mutableStateOf<List<String>>(emptyList())
        private set
    var snapshotError by mutableStateOf<String?>(null)
        private set
    var lineCount by mutableStateOf(500)

    val isArchived: Boolean get() = current?.archived == true

    fun setLines(n: Int) {
        lineCount = n
        restart()
    }

    fun loadSources() {
        viewModelScope.launch {
            when (val result = hubClient.get<LogSourcesResponse>("/logs/sources")) {
                is HubClient.ApiResult.Success -> {
                    sources = result.value.sources
                    root = result.value.root
                }

                is HubClient.ApiResult.Failure -> Unit
            }
        }
    }

    fun watch(source: LogSource) {
        current = source
        currentLabel = if (source.kind == "unit") "журнал: ${source.name}" else source.name
        restart()
    }

    fun restart() {
        val source = current ?: return
        stop()
        snapshot = emptyList()
        snapshotError = null

        val query = when (source.kind) {
            "unit" -> "unit=" + URLEncoder.encode(source.name, "UTF-8")
            else -> "path=" + URLEncoder.encode(source.name, "UTF-8")
        }

        // An archive does not grow, so there is nothing to follow: it is read
        // once instead, decompressed on the host if it needs to be.
        if (source.archived) {
            stream = null
            viewModelScope.launch {
                when (val result = hubClient.get<LogTailResponse>("/logs/tail?$query&lines=$lineCount")) {
                    is HubClient.ApiResult.Success ->
                        snapshot = result.value.output.lines()

                    is HubClient.ApiResult.Failure -> snapshotError = result.message
                }
            }
            return
        }

        val url = hubClient.webSocketUrl("/logs/ws?$query&lines=$lineCount") ?: return
        stream = LogStream(hubClient.okHttpClient(), url).also { it.connect() }
    }

    fun stop() {
        stream?.close()
    }

    override fun onCleared() {
        stop()
    }
}
