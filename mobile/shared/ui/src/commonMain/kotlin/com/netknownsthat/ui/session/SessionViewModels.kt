package com.netknownsthat.ui.session

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.netknownsthat.domain.common.AppError
import com.netknownsthat.domain.common.Outcome
import com.netknownsthat.domain.common.getOrNull
import com.netknownsthat.domain.model.HubVersionInfo
import com.netknownsthat.domain.model.HubVulnDBInfo
import com.netknownsthat.domain.model.Me
import com.netknownsthat.domain.repository.AppLanguage
import com.netknownsthat.domain.repository.HubInfoRepository
import com.netknownsthat.domain.repository.SessionRepository
import com.netknownsthat.domain.repository.SettingsRepository
import com.netknownsthat.ui.common.text
import com.netknownsthat.ui.i18n.I18n
import com.netknownsthat.ui.i18n.t
import com.netknownsthat.ui.platform.PlatformServices
import com.netknownsthat.ui.platform.systemLanguage
import kotlinx.coroutines.launch

/** Where the app starts: restoring a previous session, like a reopened
 * browser tab, before showing anything. */
sealed interface Start {
    data object Loading : Start
    data object SignIn : Start
    data object Hosts : Start
}

class AppViewModel(
    private val session: SessionRepository,
    private val settings: SettingsRepository,
    private val platform: PlatformServices,
) : ViewModel() {
    var start by mutableStateOf<Start>(Start.Loading)
        private set
    var showBetaNotice by mutableStateOf(false)
        private set
    var notifyEnabled by mutableStateOf(false)
        private set

    init {
        viewModelScope.launch {
            settings.language.collect { chosen -> I18n.lang = chosen ?: systemLanguage() }
        }
        viewModelScope.launch {
            settings.eventNotifications.collect { notifyEnabled = it }
        }
        viewModelScope.launch {
            // The hub ended the session: back to sign-in (the hub address
            // stays filled in).
            session.sessionExpired.collect { start = Start.SignIn }
        }
        viewModelScope.launch {
            showBetaNotice = !settings.betaNoticeHidden()
            val hasHub = session.restore()
            start = if (hasHub && session.me() is Outcome.Success) Start.Hosts else Start.SignIn
        }
    }

    fun dismissBetaNotice(dontShowAgain: Boolean) {
        showBetaNotice = false
        if (dontShowAgain) viewModelScope.launch { settings.hideBetaNotice() }
    }

    fun setLanguage(language: AppLanguage) {
        I18n.lang = language
        viewModelScope.launch { settings.setLanguage(language) }
    }

    fun setNotifications(enabled: Boolean) {
        notifyEnabled = enabled
        viewModelScope.launch { settings.setEventNotifications(enabled) }
        platform.scheduleEventChecks(enabled)
    }

    /** The shell is rebuilt on every change of [start], so signing in and
     * out always starts from a clean back stack. */
    fun signedIn() {
        start = Start.Hosts
    }

    fun signedOut() {
        start = Start.SignIn
    }
}

data class AuthUiState(
    val hubUrl: String = "",
    val username: String = "",
    val password: String = "",
    val loading: Boolean = false,
    val error: String? = null,
)

class AuthViewModel(private val session: SessionRepository) : ViewModel() {
    var uiState by mutableStateOf(AuthUiState(hubUrl = session.hubUrl.value.orEmpty()))
        private set

    fun onHubUrlChange(value: String) { uiState = uiState.copy(hubUrl = value) }
    fun onUsernameChange(value: String) { uiState = uiState.copy(username = value) }
    fun onPasswordChange(value: String) { uiState = uiState.copy(password = value) }

    fun login(onSuccess: (Me) -> Unit) {
        if (uiState.hubUrl.isBlank() || uiState.username.isBlank() || uiState.password.isBlank()) {
            uiState = uiState.copy(error = t("Заполните адрес хаба, логин и пароль", "Enter the hub address, login and password"))
            return
        }
        viewModelScope.launch {
            uiState = uiState.copy(loading = true, error = null)
            if (session.setHubUrl(uiState.hubUrl) is Outcome.Failure) {
                uiState = uiState.copy(loading = false, error = t("Некорректный адрес хаба", "Invalid hub address"))
                return@launch
            }
            when (val r = session.login(uiState.username, uiState.password)) {
                is Outcome.Success -> {
                    uiState = uiState.copy(loading = false, password = "")
                    onSuccess(r.value)
                }
                // A 401 here is a wrong login or password, not an ended
                // session: the hub's own message says which.
                is Outcome.Failure -> uiState = uiState.copy(
                    loading = false,
                    error = (r.error as? AppError.Unauthorized)?.message ?: r.error.text(),
                )
            }
        }
    }
}

data class AboutUiState(
    val loading: Boolean = true,
    val version: HubVersionInfo? = null,
    val vulnDb: HubVulnDBInfo? = null,
    val certFingerprint: String? = null,
    val error: String? = null,
)

class AboutViewModel(
    private val info: HubInfoRepository,
    private val session: SessionRepository,
    val platform: PlatformServices,
) : ViewModel() {
    var uiState by mutableStateOf(AboutUiState())
        private set

    fun refresh() {
        viewModelScope.launch {
            uiState = uiState.copy(loading = true, error = null)
            val version = info.version()
            val vulnDb = info.vulnDb()
            uiState = uiState.copy(
                loading = false,
                version = version.getOrNull(),
                vulnDb = vulnDb.getOrNull(),
                certFingerprint = session.pinnedCertificate(),
                error = (version as? Outcome.Failure)?.error?.text() ?: (vulnDb as? Outcome.Failure)?.error?.text(),
            )
        }
    }

    fun forgetCert() {
        session.forgetPinnedCertificate()
        uiState = uiState.copy(certFingerprint = null)
    }

    fun logout(onDone: () -> Unit) {
        viewModelScope.launch {
            session.logout()
            onDone()
        }
    }
}
