package com.netknownsthat.ui.platform

import androidx.compose.foundation.layout.padding
import androidx.compose.runtime.Composable
import androidx.compose.ui.unit.dp
import com.netknownsthat.domain.repository.AppLanguage
import platform.Foundation.NSLocale
import platform.Foundation.currentLocale
import platform.Foundation.languageCode
import platform.UserNotifications.UNAuthorizationOptionAlert
import platform.UserNotifications.UNAuthorizationOptionBadge
import platform.UserNotifications.UNAuthorizationOptionSound
import platform.UserNotifications.UNUserNotificationCenter
import platform.darwin.dispatch_async
import platform.darwin.dispatch_get_main_queue

actual fun systemLanguage(): AppLanguage =
    if (NSLocale.currentLocale.languageCode == "ru") AppLanguage.RU else AppLanguage.EN

@Composable
actual fun rememberNotificationPermission(onResult: (Boolean) -> Unit): () -> Unit = {
    UNUserNotificationCenter.currentNotificationCenter().requestAuthorizationWithOptions(
        UNAuthorizationOptionAlert or UNAuthorizationOptionSound or UNAuthorizationOptionBadge,
    ) { granted, _ ->
        dispatch_async(dispatch_get_main_queue()) { onResult(granted) }
    }
}

@androidx.compose.runtime.Composable
actual fun HubWebView(session: com.netknownsthat.domain.repository.WebSession, path: String, modifier: androidx.compose.ui.Modifier) {
    // WKWebView with the pinned certificate is still to come on iOS.
    androidx.compose.foundation.layout.Box(modifier) {
        androidx.compose.material3.Text(
            com.netknownsthat.ui.i18n.t(
                "Экран машины в приложении пока только на Android — откройте его в веб-интерфейсе хаба.",
                "The machine screen is Android-only in the app for now — open it in the hub's web interface.",
            ),
            modifier = androidx.compose.ui.Modifier.padding(24.dp),
        )
    }
}
