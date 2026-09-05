package com.netknownsthat.app.ui.login

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.netknownsthat.app.net.HubClient
import com.netknownsthat.app.net.model.Me
import kotlinx.coroutines.launch

data class AuthUiState(
    val hubUrl: String = "",
    val username: String = "",
    val password: String = "",
    val loading: Boolean = false,
    val error: String? = null,
)

/**
 * Backs the login screen — sets the hub's base URL (persisted from here on,
 * see HubClient.setHubBaseUrl) then logs in. A successful login also
 * fetches /auth/me (see HubClient.login's own doc comment on why that's a
 * second call rather than trusting the login response body's shape).
 */
class AuthViewModel(private val hubClient: HubClient) : ViewModel() {
    var uiState by mutableStateOf(AuthUiState())
        private set

    fun onHubUrlChange(value: String) {
        uiState = uiState.copy(hubUrl = value)
    }

    fun onUsernameChange(value: String) {
        uiState = uiState.copy(username = value)
    }

    fun onPasswordChange(value: String) {
        uiState = uiState.copy(password = value)
    }

    fun login(onSuccess: (Me) -> Unit) {
        if (uiState.hubUrl.isBlank() || uiState.username.isBlank() || uiState.password.isBlank()) {
            uiState = uiState.copy(error = "Заполните адрес хаба, логин и пароль")
            return
        }
        viewModelScope.launch {
            uiState = uiState.copy(loading = true, error = null)

            val urlResult = hubClient.setHubBaseUrl(uiState.hubUrl)
            if (urlResult.isFailure) {
                uiState = uiState.copy(
                    loading = false,
                    error = urlResult.exceptionOrNull()?.message ?: "Некорректный адрес хаба",
                )
                return@launch
            }

            when (val result = hubClient.login(uiState.username, uiState.password)) {
                is HubClient.ApiResult.Success -> {
                    uiState = uiState.copy(loading = false)
                    onSuccess(result.value)
                }
                is HubClient.ApiResult.Failure -> {
                    uiState = uiState.copy(loading = false, error = result.message)
                }
            }
        }
    }
}
