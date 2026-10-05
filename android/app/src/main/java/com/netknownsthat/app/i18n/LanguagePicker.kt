package com.netknownsthat.app.i18n

import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.FilterChip
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import com.netknownsthat.app.NktApplication

/** «Русский / English» chips — on the sign-in screen (before any account
 * exists) and in About. The choice is saved and outlives a restart. */
@Composable
fun LanguagePicker(modifier: Modifier = Modifier) {
    val app = LocalContext.current.applicationContext as NktApplication
    Row(modifier = modifier) {
        AppLang.entries.forEach { lang ->
            FilterChip(
                selected = I18n.lang == lang,
                onClick = { app.setLanguage(lang) },
                label = { Text(lang.label) },
                modifier = Modifier.padding(end = 8.dp),
            )
        }
    }
}
