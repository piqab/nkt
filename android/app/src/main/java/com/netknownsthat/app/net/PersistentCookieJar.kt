package com.netknownsthat.app.net

import com.netknownsthat.app.data.SettingsStore
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch
import okhttp3.Cookie
import okhttp3.CookieJar
import okhttp3.HttpUrl

/**
 * A CookieJar that behaves like a browser tab's own cookie store — the hub
 * only ever hands out one session cookie (`nkt_session`, see
 * internal/auth/service.go) via a plain Set-Cookie response header, exactly
 * the way the React frontend's `fetch(..., { credentials: 'same-origin' })`
 * relies on. OkHttp does not do this automatically; without an explicit
 * CookieJar, every request would arrive with no cookie at all and the hub
 * would answer 401 regardless of a successful login moments earlier.
 *
 * Persisted to [SettingsStore] on every save so relaunching the app finds
 * the same session instead of forcing a fresh login — call [restore] once
 * at startup, before the first request, to load it back in.
 */
class PersistentCookieJar(
    private val scope: CoroutineScope,
    private val settingsStore: SettingsStore,
) : CookieJar {
    // Keyed by host — this app only ever talks to one hub at a time in the
    // phases implemented so far, but keying by host costs nothing and keeps
    // this correct if that changes later.
    private val cookiesByHost = mutableMapOf<String, List<Cookie>>()

    override fun saveFromResponse(url: HttpUrl, cookies: List<Cookie>) {
        if (cookies.isEmpty()) return
        synchronized(cookiesByHost) { cookiesByHost[url.host] = cookies }
        persist()
    }

    override fun loadForRequest(url: HttpUrl): List<Cookie> =
        synchronized(cookiesByHost) { cookiesByHost[url.host].orEmpty() }

    /** Loads whatever was persisted from a previous run — call before the
     * first request of a fresh process. */
    suspend fun restore(baseUrl: HttpUrl) {
        val restored = settingsStore.savedCookies().mapNotNull { raw ->
            // Cookie.toString() (used in persist() below) produces exactly
            // the Set-Cookie wire format Cookie.parse expects back.
            Cookie.parse(baseUrl, raw)
        }
        if (restored.isNotEmpty()) {
            synchronized(cookiesByHost) { cookiesByHost[baseUrl.host] = restored }
        }
    }

    /** Drops every cookie — called on logout so a stale session cookie
     * never lingers into the next login attempt. */
    fun clear() {
        synchronized(cookiesByHost) { cookiesByHost.clear() }
        scope.launch { settingsStore.clearSession() }
    }

    private fun persist() {
        val snapshot = synchronized(cookiesByHost) {
            cookiesByHost.values.flatten().map { it.toString() }.toSet()
        }
        scope.launch { settingsStore.saveCookies(snapshot) }
    }
}
