package com.netknownsthat.app

import android.app.Application
import com.netknownsthat.app.data.SettingsStore
import com.netknownsthat.app.net.HubClient
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
        hubClient = HubClient(settingsStore, appScope)
    }
}
