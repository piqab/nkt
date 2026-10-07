package com.netknownsthat.data.local

import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.PreferenceDataStoreFactory
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.booleanPreferencesKey
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.intPreferencesKey
import androidx.datastore.preferences.core.longPreferencesKey
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.core.stringSetPreferencesKey
import kotlinx.coroutines.flow.first
import okio.Path.Companion.toPath

/** Name of the preferences file — the same one the former Android-only app
 * wrote (files/datastore/nkt_settings.preferences_pb), so an update keeps
 * the hub address, session cookie and pinned certificates. */
const val PREFERENCES_FILE = "nkt_settings.preferences_pb"

fun createPreferencesDataStore(path: String): DataStore<Preferences> =
    PreferenceDataStoreFactory.createWithPath(produceFile = { path.toPath() })

/**
 * Everything persisted on the device. Keys are the former app's own, for
 * the same reason as the file name.
 */
class LocalStore(private val store: DataStore<Preferences>) {
    private object Keys {
        val HUB_BASE_URL = stringPreferencesKey("hub_base_url")
        val COOKIES = stringSetPreferencesKey("session_cookies")
        val PINNED_CERTS = stringSetPreferencesKey("pinned_certs")
        val BETA_NOTICE_HIDDEN = booleanPreferencesKey("beta_notice_hidden")
        val APP_LANG = stringPreferencesKey("app_lang")
        val UI_SCALE = intPreferencesKey("ui_scale")
        val EVENTS_NOTIFY = booleanPreferencesKey("events_notify")
        val EVENTS_LAST_ID = longPreferencesKey("events_last_id")
    }

    private suspend fun prefs() = store.data.first()

    suspend fun hubBaseUrl(): String? = prefs()[Keys.HUB_BASE_URL]
    suspend fun setHubBaseUrl(url: String) = store.edit { it[Keys.HUB_BASE_URL] = url }.let { }

    /** "host|Set-Cookie header" per cookie. */
    suspend fun cookies(): Set<String> = prefs()[Keys.COOKIES].orEmpty()
    suspend fun setCookies(cookies: Set<String>) = store.edit { it[Keys.COOKIES] = cookies }.let { }
    suspend fun clearCookies() = store.edit { it.remove(Keys.COOKIES) }.let { }

    /** "host:port=sha256hex" per pinned hub. */
    suspend fun pinnedCerts(): Set<String> = prefs()[Keys.PINNED_CERTS].orEmpty()
    suspend fun setPinnedCerts(entries: Set<String>) = store.edit { it[Keys.PINNED_CERTS] = entries }.let { }

    suspend fun betaNoticeHidden(): Boolean = prefs()[Keys.BETA_NOTICE_HIDDEN] ?: false
    suspend fun hideBetaNotice() = store.edit { it[Keys.BETA_NOTICE_HIDDEN] = true }.let { }

    suspend fun appLang(): String? = prefs()[Keys.APP_LANG]
    suspend fun setAppLang(code: String) = store.edit { it[Keys.APP_LANG] = code }.let { }

    suspend fun uiScale(): Int? = prefs()[Keys.UI_SCALE]
    suspend fun setUiScale(percent: Int) = store.edit { it[Keys.UI_SCALE] = percent }.let { }

    suspend fun eventsNotify(): Boolean = prefs()[Keys.EVENTS_NOTIFY] ?: false
    suspend fun setEventsNotify(on: Boolean) = store.edit { it[Keys.EVENTS_NOTIFY] = on }.let { }

    suspend fun eventsLastId(): Long = prefs()[Keys.EVENTS_LAST_ID] ?: 0L
    suspend fun setEventsLastId(id: Long) = store.edit { it[Keys.EVENTS_LAST_ID] = id }.let { }
}
