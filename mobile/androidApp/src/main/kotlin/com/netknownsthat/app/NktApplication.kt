package com.netknownsthat.app

import android.app.Application
import com.netknownsthat.app.shared.initKoin
import com.netknownsthat.ui.platform.PlatformServices
import org.koin.android.ext.koin.androidContext
import org.koin.dsl.module

/** Starts the Koin graph (shared/app's composition root) with what only the
 * Android app can provide. */
class NktApplication : Application() {
    override fun onCreate() {
        super.onCreate()
        val app = this
        initKoin(module { single<PlatformServices> { AndroidPlatformServices(app) } }) {
            androidContext(app)
        }
        EventsWorker.createChannel(this)
    }
}

private class AndroidPlatformServices(private val app: Application) : PlatformServices {
    override val appVersion: String = BuildConfig.VERSION_NAME
    override fun scheduleEventChecks(enabled: Boolean) = EventsWorker.schedule(app, enabled)
}
