package com.netknownsthat.app.ui.hosts

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.netknownsthat.app.net.HubClient
import com.netknownsthat.app.net.model.HubEventsResponse
import com.netknownsthat.app.net.model.HubHost
import kotlinx.coroutines.launch

data class HostListUiState(
    val loading: Boolean = true,
    val hosts: List<HubHost> = emptyList(),
    val error: String? = null,
    /** Unread hub alerts, for the bell. */
    val unread: Int = 0,
)

/**
 * Phase 1 scope only: list + open. Add/edit/delete/install-log/export-import
 * (the plan's phase 7 — by far the most complex single screen, see the
 * plan's own "Топ-6" ranking) come later, layered onto this same
 * ViewModel/screen rather than a rewrite.
 */
class HostListViewModel(private val hubClient: HubClient) : ViewModel() {
    var uiState by mutableStateOf(HostListUiState())
        private set

    fun refresh() {
        viewModelScope.launch {
            uiState = uiState.copy(loading = true, error = null)
            when (val result = hubClient.get<List<HubHost>>("/hub/hosts")) {
                is HubClient.ApiResult.Success ->
                    uiState = uiState.copy(loading = false, hosts = result.value)
                is HubClient.ApiResult.Failure ->
                    uiState = uiState.copy(loading = false, error = result.message)
            }
            val events = hubClient.get<HubEventsResponse>("/hub/events?limit=1")
            if (events is HubClient.ApiResult.Success) uiState = uiState.copy(unread = events.value.unread)
        }
    }

    /**
     * Selects a host by id for a jump from an alert, a monitoring insight or
     * a notification — the list may not be loaded yet (cold start from a
     * notification), so it is fetched when needed. Null if the host is gone.
     */
    suspend fun selectById(id: Long): HubHost? {
        var host = uiState.hosts.firstOrNull { it.id == id }
        if (host == null) {
            val result = hubClient.get<List<HubHost>>("/hub/hosts")
            if (result is HubClient.ApiResult.Success) {
                uiState = uiState.copy(hosts = result.value)
                host = result.value.firstOrNull { it.id == id }
            }
        }
        host?.let(::select)
        return host
    }

    /** The host whose sections are open, kept here so the host screen can
     * title itself and refetch when the selection changes. */
    var selectedHost by mutableStateOf<HubHost?>(null)
        private set

    /** Selecting a host scopes every subsequent API call to it (see
     * HostScope) — screens from phase 2 onward read hubClient.hostScope to
     * know which host's overview/findings/etc. to show. */
    fun select(host: HubHost) {
        selectedHost = host
        hubClient.hostScope.select(host.id)
    }

    /** Back to hub scope: hub-level screens ("О системе", this list itself)
     * must not inherit the host that happened to be open last. */
    fun deselectHost() {
        selectedHost = null
        hubClient.hostScope.select(null)
    }
}
