package com.netknownsthat.data.remote

import com.netknownsthat.data.local.LocalStore
import io.ktor.client.plugins.cookies.CookiesStorage
import io.ktor.http.Cookie
import io.ktor.http.Url
import io.ktor.http.parseServerSetCookieHeader
import io.ktor.http.renderSetCookieHeader
import io.ktor.util.date.GMTDate
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch

/**
 * The session cookie, kept per hub host and persisted, so a relaunch finds
 * the same session a reopened browser tab would. Stored as
 * "host|Set-Cookie header" — the format the former Android app wrote, so an
 * update stays signed in.
 */
class PersistentCookieStore(
    private val scope: CoroutineScope,
    private val local: LocalStore,
) : CookiesStorage {
    private val byHost = mutableMapOf<String, List<Cookie>>()
    private val lock = Lock()

    override suspend fun get(requestUrl: Url): List<Cookie> {
        val now = GMTDate()
        return lock.withLock { byHost[requestUrl.host].orEmpty() }
            .filter { c -> c.expires == null || c.expires!! > now }
    }

    override suspend fun addCookie(requestUrl: Url, cookie: Cookie) {
        lock.withLock {
            val others = byHost[requestUrl.host].orEmpty().filter { it.name != cookie.name }
            byHost[requestUrl.host] = others + cookie
        }
        persist()
    }

    override fun close() = Unit

    /** Loads the saved cookies of [host] (other hubs' cookies are dropped:
     * a session of hub A must never be sent to hub B). */
    suspend fun restore(host: String) {
        val restored = local.cookies().mapNotNull { raw ->
            val parts = raw.split("|", limit = 2)
            if (parts.size != 2 || parts[0] != host) return@mapNotNull null
            runCatching { parseServerSetCookieHeader(parts[1]) }.getOrNull()
        }
        lock.withLock {
            byHost.clear()
            if (restored.isNotEmpty()) byHost[host] = restored
        }
    }

    fun clear() {
        lock.withLock { byHost.clear() }
        scope.launch { local.clearCookies() }
    }

    private fun persist() {
        val snapshot = lock.withLock {
            byHost.flatMap { (host, cookies) -> cookies.map { "$host|${renderSetCookieHeader(it)}" } }.toSet()
        }
        scope.launch { local.setCookies(snapshot) }
    }
}
