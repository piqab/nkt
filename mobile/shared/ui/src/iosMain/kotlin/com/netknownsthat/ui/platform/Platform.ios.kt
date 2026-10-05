package com.netknownsthat.ui.platform

import androidx.compose.runtime.Composable
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
