package com.netknownsthat.ui.platform

import androidx.compose.runtime.Composable
import com.netknownsthat.domain.repository.AppLanguage

/** What presentation needs from the platform app (androidApp, iosApp),
 * provided through Koin. */
interface PlatformServices {
    /** versionName / CFBundleShortVersionString — shown in About. */
    val appVersion: String

    /** Periodic checks of the hub's alerts for phone notifications:
     * WorkManager on Android, background app refresh on iOS. */
    fun scheduleEventChecks(enabled: Boolean)
}

/** The device language, when it is one of ours; English otherwise. */
expect fun systemLanguage(): AppLanguage

/**
 * Asks for permission to post notifications (Android 13+, iOS); returns a
 * launcher. [onResult] gets true when notifications may be shown.
 */
@Composable
expect fun rememberNotificationPermission(onResult: (Boolean) -> Unit): () -> Unit

/**
 * A hub page in an embedded browser, signed in as [session] — the VM screen
 * (noVNC / spice-html5), which a native client would have to reimplement.
 * Only pages of the hub itself are opened; a self-signed hub certificate is
 * accepted only if it is the one pinned at sign-in.
 */
@Composable
expect fun HubWebView(session: com.netknownsthat.domain.repository.WebSession, path: String, modifier: androidx.compose.ui.Modifier)
