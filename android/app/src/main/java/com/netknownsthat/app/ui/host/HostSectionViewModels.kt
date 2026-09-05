package com.netknownsthat.app.ui.host

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.netknownsthat.app.net.HubClient
import com.netknownsthat.app.net.model.AuditResponse
import com.netknownsthat.app.net.model.FindingsResponse
import com.netknownsthat.app.net.model.InterfacesResponse
import com.netknownsthat.app.net.model.Overview
import kotlinx.coroutines.launch

/** What every read-only section needs and nothing more. */
data class SectionState<T>(
    val loading: Boolean = false,
    val data: T? = null,
    val error: String? = null,
)

/**
 * Shared plumbing for the phase-2 sections: they differ only in which path
 * they read and what type comes back, so the fetch-and-store dance lives
 * here once.
 *
 * Nothing loads in `init` on purpose. These ViewModels are created with the
 * activity, before any host has been picked, so an eager load would fire
 * with no host scope set (see HostScope) and read the wrong thing entirely.
 * The screens call [load] from a LaunchedEffect keyed on the selected host,
 * which also gets a fresh fetch when the operator switches hosts.
 */
abstract class SectionViewModel<T>(protected val hubClient: HubClient) : ViewModel() {
    var state by mutableStateOf(SectionState<T>())
        private set

    protected abstract suspend fun fetch(): HubClient.ApiResult<T>

    fun load() {
        viewModelScope.launch {
            state = state.copy(loading = true, error = null)
            state = when (val result = fetch()) {
                is HubClient.ApiResult.Success -> state.copy(loading = false, data = result.value)
                is HubClient.ApiResult.Failure -> state.copy(loading = false, error = result.message)
            }
        }
    }
}

class OverviewViewModel(hubClient: HubClient) : SectionViewModel<Overview>(hubClient) {
    override suspend fun fetch() = hubClient.get<Overview>("/overview")
}

class FindingsViewModel(hubClient: HubClient) : SectionViewModel<FindingsResponse>(hubClient) {
    override suspend fun fetch() = hubClient.get<FindingsResponse>("/findings")
}

class InterfacesViewModel(hubClient: HubClient) : SectionViewModel<InterfacesResponse>(hubClient) {
    override suspend fun fetch() = hubClient.get<InterfacesResponse>("/interfaces")
}

class AuditViewModel(hubClient: HubClient) : SectionViewModel<AuditResponse>(hubClient) {
    override suspend fun fetch() = hubClient.get<AuditResponse>("/audit")
}
