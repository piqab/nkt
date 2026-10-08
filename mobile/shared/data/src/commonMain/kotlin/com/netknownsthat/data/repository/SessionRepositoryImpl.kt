package com.netknownsthat.data.repository

import com.netknownsthat.data.dto.MeDto
import com.netknownsthat.data.local.LocalStore
import com.netknownsthat.data.mapper.toDomain
import com.netknownsthat.data.remote.ApiClient
import com.netknownsthat.domain.common.AppError
import com.netknownsthat.domain.common.Outcome
import com.netknownsthat.domain.common.map
import com.netknownsthat.domain.model.Me
import com.netknownsthat.domain.repository.AppLanguage
import com.netknownsthat.domain.repository.SessionRepository
import com.netknownsthat.domain.repository.SettingsRepository
import com.netknownsthat.domain.repository.UiScale
import io.ktor.http.HttpMethod
import io.ktor.http.Url
import io.ktor.http.parseUrl
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put

class SessionRepositoryImpl(
    private val api: ApiClient,
    private val local: LocalStore,
) : SessionRepository {
    private val _hubUrl = MutableStateFlow<String?>(null)
    override val hubUrl: StateFlow<String?> = _hubUrl.asStateFlow()
    override val sessionExpired: kotlinx.coroutines.flow.Flow<Unit> = api.unauthorized

    // restore() is called from the UI and from the notification worker,
    // possibly at once; the saved state is read only once per process.
    private val restoreLock = Mutex()
    private var restored: Boolean? = null

    override suspend fun restore(): Boolean = restoreLock.withLock {
        restored?.let { return it }
        api.pins.load()
        val saved = local.hubBaseUrl()?.let { parseUrl(it) }
        val ok = if (saved != null) {
            api.setBase(saved)
            api.cookies.restore(saved.host)
            _hubUrl.value = saved.toString()
            true
        } else {
            false
        }
        restored = ok
        ok
    }

    override suspend fun setHubUrl(raw: String): Outcome<Unit> {
        val trimmed = raw.trim()
        val normalized = if ("://" in trimmed) trimmed else "http://$trimmed"
        val url: Url = parseUrl(normalized)?.takeIf { it.host.isNotBlank() }
            ?: return Outcome.Failure(AppError.BadResponse("hub address: $raw"))
        // A session cookie issued by hub A must never be sent to hub B.
        val previous = api.baseUrl
        if (previous != null && previous.host != url.host) api.cookies.clear()
        api.setBase(url)
        local.setHubBaseUrl(url.toString())
        _hubUrl.value = url.toString()
        restoreLock.withLock { restored = true }
        return Outcome.Success(Unit)
    }

    override suspend fun login(username: String, password: String): Outcome<Me> {
        val body = buildJsonObject {
            put("username", username)
            put("password", password)
        }
        return when (val r = api.call(HttpMethod.Post, "/auth/login", body)) {
            is Outcome.Failure -> r
            is Outcome.Success -> me()
        }
    }

    override suspend fun me(): Outcome<Me> = api.get<MeDto>("/auth/me").map { it.toDomain() }

    override suspend fun logout() {
        api.call(HttpMethod.Post, "/auth/logout")
        api.cookies.clear()
    }

    override fun pinnedCertificate(): String? {
        val base = api.baseUrl ?: return null
        return api.pins.pinnedFor("${base.host}:${base.port}")
    }

    override suspend fun webSession(): com.netknownsthat.domain.repository.WebSession? {
        val base = api.baseUrl ?: return null
        val origin = api.url("")?.removeSuffix("/api") ?: return null
        val cookies = api.cookies.get(base).map { io.ktor.http.renderSetCookieHeader(it) }
        if (cookies.isEmpty()) return null
        return com.netknownsthat.domain.repository.WebSession(origin, cookies, pinnedCertificate())
    }

    override fun forgetPinnedCertificate() {
        val base = api.baseUrl ?: return
        api.pins.forget("${base.host}:${base.port}")
    }
}

class SettingsRepositoryImpl(
    private val local: LocalStore,
    private val scope: CoroutineScope,
) : SettingsRepository {
    private val _language = MutableStateFlow<AppLanguage?>(null)
    override val language: StateFlow<AppLanguage?> = _language.asStateFlow()
    private val _scale = MutableStateFlow(UiScale.DEFAULT)
    override val uiScale: StateFlow<UiScale> = _scale.asStateFlow()
    private val _notify = MutableStateFlow(false)
    override val eventNotifications: StateFlow<Boolean> = _notify.asStateFlow()

    /** Completes once the saved values are loaded. */
    val loaded = CompletableDeferred<Unit>()

    init {
        scope.launch {
            _language.value = AppLanguage.entries.firstOrNull { it.code == local.appLang() }
            _scale.value = UiScale.of(local.uiScale())
            _notify.value = local.eventsNotify()
            loaded.complete(Unit)
        }
    }

    override suspend fun setLanguage(language: AppLanguage) {
        _language.value = language
        local.setAppLang(language.code)
    }

    override suspend fun setUiScale(scale: UiScale) {
        _scale.value = scale
        local.setUiScale(scale.percent)
    }

    override suspend fun betaNoticeHidden(): Boolean = local.betaNoticeHidden()
    override suspend fun hideBetaNotice() = local.hideBetaNotice()

    override suspend fun setEventNotifications(enabled: Boolean) {
        _notify.value = enabled
        local.setEventsNotify(enabled)
    }

    override suspend fun lastNotifiedEventId(): Long = local.eventsLastId()
    override suspend fun setLastNotifiedEventId(id: Long) = local.setEventsLastId(id)
}
