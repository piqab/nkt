package com.netknownsthat.ui.platform

import android.Manifest
import android.content.pm.PackageManager
import android.os.Build
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.runtime.Composable
import androidx.compose.ui.platform.LocalContext
import androidx.core.content.ContextCompat
import com.netknownsthat.domain.repository.AppLanguage
import java.util.Locale

actual fun systemLanguage(): AppLanguage =
    if (Locale.getDefault().language == "ru") AppLanguage.RU else AppLanguage.EN

@Composable
actual fun rememberNotificationPermission(onResult: (Boolean) -> Unit): () -> Unit {
    val context = LocalContext.current
    val launcher = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission(), onResult)
    return {
        val needs = Build.VERSION.SDK_INT >= 33 &&
            ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        if (needs) launcher.launch(Manifest.permission.POST_NOTIFICATIONS) else onResult(true)
    }
}
