package com.netknownsthat.app.data

import android.content.Context
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.core.stringSetPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import kotlinx.coroutines.flow.first

private val Context.dataStore by preferencesDataStore(name = "nkt_settings")

/**
 * Everything that needs to survive a process death/relaunch: which hub this
 * install is pointed at, and its session cookie jar (see
 * net/PersistentCookieJar.kt) — so reopening the app finds the same session
 * a browser tab would, rather than forcing a fresh login every time.
 *
 * Deliberately just one hub connection for now (phase 1's own scope — see
 * the plan's phase 7 for a real multi-hub picker with its own storage).
 */
class SettingsStore(private val context: Context) {
    private object Keys {
        val HUB_BASE_URL = stringPreferencesKey("hub_base_url")
        val COOKIES = stringSetPreferencesKey("session_cookies")
    }

    suspend fun hubBaseUrl(): String? =
        context.dataStore.data.first()[Keys.HUB_BASE_URL]

    suspend fun setHubBaseUrl(url: String) {
        context.dataStore.edit { it[Keys.HUB_BASE_URL] = url }
    }

    /**
     * Raw Set-Cookie-style strings (one per cookie, via okhttp3.Cookie's own
     * toString(), which is already exactly that format and round-trips
     * through Cookie.parse(url, string) — see PersistentCookieJar).
     */
    suspend fun savedCookies(): Set<String> =
        context.dataStore.data.first()[Keys.COOKIES].orEmpty()

    suspend fun saveCookies(cookies: Set<String>) {
        context.dataStore.edit { it[Keys.COOKIES] = cookies }
    }

    suspend fun clearSession() {
        context.dataStore.edit { it.remove(Keys.COOKIES) }
    }
}
