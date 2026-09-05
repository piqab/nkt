package com.netknownsthat.app.ui.hosts

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.netknownsthat.app.net.HubClient
import com.netknownsthat.app.net.model.HubHost
import kotlinx.coroutines.launch

data class HostListUiState(
    val loading: Boolean = true,
    val hosts: List<HubHost> = emptyList(),
    val error: String? = null,
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

    init {
        refresh()
    }

    fun refresh() {
        viewModelScope.launch {
            uiState = uiState.copy(loading = true, error = null)
            when (val result = hubClient.get<List<HubHost>>("/hub/hosts")) {
                is HubClient.ApiResult.Success ->
                    uiState = uiState.copy(loading = false, hosts = result.value)
                is HubClient.ApiResult.Failure ->
                    uiState = uiState.copy(loading = false, error = result.message)
            }
        }
    }

    /** Selecting a host scopes every subsequent API call to it (see
     * HostScope) — screens from phase 2 onward read hubClient.hostScope to
     * know which host's overview/findings/etc. to show. */
    fun select(host: HubHost) {
        hubClient.hostScope.select(host.id)
    }
}
