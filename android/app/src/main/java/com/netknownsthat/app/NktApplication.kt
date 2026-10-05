package com.netknownsthat.app

import android.app.Application
import com.netknownsthat.app.data.SettingsStore
import com.netknownsthat.app.net.HubClient
import com.netknownsthat.app.i18n.AppLang
import com.netknownsthat.app.i18n.I18n
import com.netknownsthat.app.ui.hub.EventsWorker
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.SupervisorJob

/**
 * Manual, no-framework dependency setup — deliberately no Hilt/Koin for a
 * phase-1 scaffold this small. [hubClient] is the one thing every screen
 * needs; everything else is constructed straight from it or from a
 * ViewModel.
 */
class NktApplication : Application() {
    /** Outlives any single screen — cookie persistence and any in-flight
     * request should survive a configuration change or a screen closing
     * mid-request. */
    val appScope = CoroutineScope(SupervisorJob())

    lateinit var settingsStore: SettingsStore
        private set
    lateinit var hubClient: HubClient
        private set

    override fun onCreate() {
        super.onCreate()
        settingsStore = SettingsStore(this)
        // Before anything is drawn or requested: the first screen and the
        // first X-NKT-Lang header must already be in the chosen language.
        // A tiny DataStore read, once per process.
        runBlocking { settingsStore.appLang() }?.let { I18n.lang = it }
        hubClient = HubClient(settingsStore, appScope)
        EventsWorker.createChannel(this)
    }

    /** Switches the interface language now and remembers it. */
    fun setLanguage(lang: AppLang) {
        I18n.lang = lang
        appScope.launch { settingsStore.setAppLang(lang) }
    }
}
