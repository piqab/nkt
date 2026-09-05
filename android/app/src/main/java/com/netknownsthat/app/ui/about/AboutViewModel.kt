package com.netknownsthat.app.ui.about

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.netknownsthat.app.net.HubClient
import com.netknownsthat.app.net.model.HubVersionInfo
import com.netknownsthat.app.net.model.HubVulnDBInfo
import kotlinx.coroutines.launch

data class AboutUiState(
    val loading: Boolean = true,
    val version: HubVersionInfo? = null,
    val vulnDb: HubVulnDBInfo? = null,
    val error: String? = null,
)

/**
 * Mirrors web/src/pages/About.tsx's own two cards — hub version/update
 * check and the centralized vulnerability-DB status. No "Обновить"/
 * "Обновить сейчас" buttons in this phase: replacing the hub's own running
 * binary from a phone isn't a meaningful action the way it is from the
 * machine sitting next to the hub, and the vuln-DB refresh is a
 * long-running background job better triggered from wherever the hub
 * itself is administered. Both are read-only status views here — add the
 * actions later if that judgment turns out wrong in practice.
 */
class AboutViewModel(private val hubClient: HubClient) : ViewModel() {
    var uiState by mutableStateOf(AboutUiState())
        private set

    init {
        refresh()
    }

    fun refresh() {
        viewModelScope.launch {
            uiState = uiState.copy(loading = true, error = null)
            val versionResult = hubClient.get<HubVersionInfo>("/hub/version")
            val vulnDbResult = hubClient.get<HubVulnDBInfo>("/hub/vulndb")

            val error = (versionResult as? HubClient.ApiResult.Failure)?.message
                ?: (vulnDbResult as? HubClient.ApiResult.Failure)?.message

            uiState = uiState.copy(
                loading = false,
                version = (versionResult as? HubClient.ApiResult.Success)?.value,
                vulnDb = (vulnDbResult as? HubClient.ApiResult.Success)?.value,
                error = error,
            )
        }
    }
}
