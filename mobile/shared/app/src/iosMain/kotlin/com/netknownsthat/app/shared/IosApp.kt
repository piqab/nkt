package com.netknownsthat.app.shared

import androidx.compose.ui.window.ComposeUIViewController
import com.netknownsthat.domain.usecase.CheckNewEventsUseCase
import com.netknownsthat.domain.usecase.hostPath
import com.netknownsthat.ui.HostJump
import com.netknownsthat.ui.NktApp
import com.netknownsthat.ui.platform.PlatformServices
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.MainScope
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.launch
import org.koin.core.component.KoinComponent
import org.koin.core.component.inject
import org.koin.dsl.module
import platform.BackgroundTasks.BGAppRefreshTaskRequest
import platform.BackgroundTasks.BGTaskScheduler
import platform.Foundation.NSBundle
import platform.Foundation.NSDate
import platform.Foundation.dateWithTimeIntervalSinceNow
import platform.UIKit.UIViewController
import platform.UserNotifications.UNMutableNotificationContent
import platform.UserNotifications.UNNotificationRequest
import platform.UserNotifications.UNUserNotificationCenter

/** Identifier of the background refresh task (also in Info.plist,
 * BGTaskSchedulerPermittedIdentifiers). */
const val EVENTS_TASK_ID = "com.netknownsthat.app.events"

private val jumps = MutableSharedFlow<HostJump>(extraBufferCapacity = 4)

private class IosPlatformServices : PlatformServices {
    override val appVersion: String =
        NSBundle.mainBundle.objectForInfoDictionaryKey("CFBundleShortVersionString") as? String ?: "?"

    override fun scheduleEventChecks(enabled: Boolean) {
        if (enabled) scheduleNextEventCheck() else BGTaskScheduler.sharedScheduler.cancelTaskRequestWithIdentifier(EVENTS_TASK_ID)
    }
}

/** Called once from the Swift app at launch. */
fun startApp() {
    initKoin(module { single<PlatformServices> { IosPlatformServices() } })
}

/** The whole UI, for SwiftUI's UIViewControllerRepresentable. */
fun MainViewController(): UIViewController = ComposeUIViewController { NktApp(jumps) }

/** A tapped notification: open the host at the event's section. */
fun openHost(hostId: Long, path: String) {
    jumps.tryEmit(HostJump(hostId, path))
}

/** Asks iOS for the next background refresh (it decides when — 15 minutes
 * is only the earliest). */
fun scheduleNextEventCheck() {
    val request = BGAppRefreshTaskRequest(EVENTS_TASK_ID)
    request.earliestBeginDate = NSDate.dateWithTimeIntervalSinceNow(15.0 * 60)
    runCatching { BGTaskScheduler.sharedScheduler.submitTaskRequest(request, null) }
}

/**
 * The background refresh: new hub events → local notifications. Same rule
 * as on Android (CheckNewEventsUseCase); [done] is called when finished so
 * Swift can complete the BGTask.
 */
object EventChecks : KoinComponent {
    private val check: CheckNewEventsUseCase by inject()

    fun run(done: () -> Unit) {
        CoroutineScope(Dispatchers.Default).launch {
            val events = runCatching { check() }.getOrDefault(emptyList())
            MainScope().launch {
                events.forEach { e ->
                    val content = UNMutableNotificationContent()
                    content.setTitle(if (e.hostName.isNotBlank()) "${e.hostName}: ${e.kind}" else e.kind)
                    content.setBody(e.detail)
                    content.setUserInfo(mapOf("host_id" to e.hostId, "path" to e.hostPath()))
                    val request = UNNotificationRequest.requestWithIdentifier("event-${e.id}", content, null)
                    UNUserNotificationCenter.currentNotificationCenter().addNotificationRequest(request, null)
                }
                scheduleNextEventCheck()
                done()
            }
        }
    }
}

