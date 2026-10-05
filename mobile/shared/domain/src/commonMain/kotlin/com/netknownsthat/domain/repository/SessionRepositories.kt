package com.netknownsthat.domain.repository

import com.netknownsthat.domain.common.Outcome
import com.netknownsthat.domain.model.Me
import kotlinx.coroutines.flow.StateFlow

/** Interface language of the app; [code] is what the hub is asked to answer in. */
enum class AppLanguage(val code: String) {
    RU("ru"),
    EN("en"),
}

/** The hub connection and the signed-in session. */
interface SessionRepository {
    /** Saved hub address, or null before the first sign-in. */
    val hubUrl: StateFlow<String?>

    /** Fires when the hub says the session is gone (401 on any call but
     * sign-in itself) — the app goes back to the sign-in screen. */
    val sessionExpired: kotlinx.coroutines.flow.Flow<Unit>

    /** Restores the saved hub and cookie; true if a hub is configured. */
    suspend fun restore(): Boolean

    /** Validates and saves a new hub address (a different hub forgets the
     * old session cookie). Fails with BadResponse for an unusable address. */
    suspend fun setHubUrl(raw: String): Outcome<Unit>

    suspend fun login(username: String, password: String): Outcome<Me>
    suspend fun me(): Outcome<Me>
    suspend fun logout()

    /** SHA-256 of the pinned self-signed certificate of the hub, if any. */
    fun pinnedCertificate(): String?

    /** Forgets the pin: the next connection trusts on first use again. */
    fun forgetPinnedCertificate()
}

/** Settings kept on the device. */
interface SettingsRepository {
    /** Chosen interface language; null — follow the device. */
    val language: StateFlow<AppLanguage?>
    suspend fun setLanguage(language: AppLanguage)

    suspend fun betaNoticeHidden(): Boolean
    suspend fun hideBetaNotice()

    val eventNotifications: StateFlow<Boolean>
    suspend fun setEventNotifications(enabled: Boolean)

    /** Newest hub event already notified about; 0 — none yet. */
    suspend fun lastNotifiedEventId(): Long
    suspend fun setLastNotifiedEventId(id: Long)
}

/** The language requests are made in right now — provided by presentation,
 * which owns the interface language; data puts it into X-NKT-Lang. */
fun interface LanguageProvider {
    fun current(): AppLanguage
}
