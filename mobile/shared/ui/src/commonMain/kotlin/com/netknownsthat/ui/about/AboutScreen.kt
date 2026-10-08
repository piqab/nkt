package com.netknownsthat.ui.about

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.Card
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import com.netknownsthat.ui.session.AboutViewModel
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import com.netknownsthat.ui.i18n.t

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun AboutScreen(
    viewModel: AboutViewModel,
    onLanguage: (com.netknownsthat.domain.repository.AppLanguage) -> Unit,
    uiScale: com.netknownsthat.domain.repository.UiScale,
    onUiScale: (com.netknownsthat.domain.repository.UiScale) -> Unit,
    onSignedOut: () -> Unit,
) {
    val state = viewModel.uiState

    // See HostListScreen: loading from the ViewModel's init races the hub
    // URL being restored. Reloaded on a language switch: the release notes
    // come from the hub in the interface language.
    LaunchedEffect(com.netknownsthat.ui.i18n.I18n.lang) { viewModel.refresh() }

    run {
        Box(modifier = Modifier.fillMaxSize().verticalScroll(rememberScrollState())) {
            when {
                state.loading ->
                    CircularProgressIndicator(modifier = Modifier.align(Alignment.Center))

                state.error != null ->
                    Text(
                        text = state.error,
                        color = MaterialTheme.colorScheme.error,
                        modifier = Modifier.align(Alignment.Center).padding(24.dp),
                    )

                else -> Column(modifier = Modifier.padding(16.dp)) {
                    Card(modifier = Modifier.fillMaxWidth().padding(bottom = 16.dp)) {
                        Column(modifier = Modifier.padding(16.dp)) {
                            Text(t("Язык", "Language"), style = MaterialTheme.typography.titleMedium)
                            com.netknownsthat.ui.i18n.LanguagePicker(onLanguage, modifier = Modifier.padding(top = 8.dp))
                        }
                    }
                    Card(modifier = Modifier.fillMaxWidth().padding(bottom = 16.dp)) {
                        Column(modifier = Modifier.padding(16.dp)) {
                            Text(t("Масштаб интерфейса", "Interface scale"), style = MaterialTheme.typography.titleMedium)
                            Text(
                                t("Текст и отступы вместе: меньше — больше умещается на экране", "Text and spacing together: smaller fits more on the screen"),
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                            UiScalePicker(uiScale, onUiScale)
                        }
                    }
                    Card(modifier = Modifier.fillMaxWidth().padding(bottom = 16.dp)) {
                        Column(modifier = Modifier.padding(16.dp)) {
                            Text(t("Версия хаба", "Hub version"), style = MaterialTheme.typography.titleMedium)
                            Text(
                                t("Текущая: ${state.version?.current ?: "—"}", "Current: ${state.version?.current ?: "—"}"),
                                modifier = Modifier.padding(top = 8.dp),
                            )
                            state.version?.latest?.let {
                                Text(t("Последняя доступная: $it", "Latest available: $it"))
                            }
                            // The app is released together with nkt, so a
                            // mismatch says which side is behind.
                            Text(
                                t("Приложение: ${viewModel.platform.appVersion}", "App: ${viewModel.platform.appVersion}"),
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                            if (state.version?.updateAvailable == true) {
                                Text(
                                    text = t("Доступно обновление", "Update available"),
                                    color = MaterialTheme.colorScheme.tertiary,
                                )
                                // Что именно несёт новая версия — читается до
                                // обновления, а не после. Текст приходит из
                                // описания релиза на GitHub (WHATSNEW.md):
                                // заметки внутри установленного бинарника
                                // описывали бы уже работающую версию.
                                state.version.notes?.takeIf { it.isNotBlank() }?.let { notes ->
                                    Text(
                                        text = t("Что нового в ${state.version.latest ?: ""}", "What's new in ${state.version.latest ?: ""}").trim(),
                                        style = MaterialTheme.typography.titleSmall,
                                        modifier = Modifier.padding(top = 12.dp),
                                    )
                                    Text(
                                        text = notes,
                                        style = MaterialTheme.typography.bodySmall,
                                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                                        modifier = Modifier
                                            .padding(top = 4.dp)
                                            .heightIn(max = 260.dp)
                                            .verticalScroll(rememberScrollState()),
                                    )
                                }
                            }
                            state.version?.checkError?.let {
                                Text(
                                    text = t("Проверка не удалась: $it", "Check failed: $it"),
                                    color = MaterialTheme.colorScheme.error,
                                )
                            }
                        }
                    }

                    state.certFingerprint?.let { fingerprint ->
                        Card(modifier = Modifier.fillMaxWidth().padding(bottom = 16.dp)) {
                            Column(modifier = Modifier.padding(16.dp)) {
                                Text(
                                    t("Сертификат хаба", "Hub certificate"),
                                    style = MaterialTheme.typography.titleMedium,
                                )
                                Text(
                                    text = t("Самоподписанный, закреплён при первом подключении. ", "Self-signed, pinned on first connection. ") +
                                        t("Сверьте отпечаток с тем, что показывает сам хаб:", "Compare the fingerprint with what the hub itself shows:"),
                                    style = MaterialTheme.typography.bodySmall,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                                    modifier = Modifier.padding(top = 8.dp),
                                )
                                Text(
                                    text = fingerprint.chunked(16).joinToString("\n"),
                                    style = MaterialTheme.typography.bodySmall,
                                    fontFamily = FontFamily.Monospace,
                                    modifier = Modifier.padding(top = 8.dp),
                                )
                                // After a deliberate hub reinstall the old pin
                                // only blocks the new certificate; this lets
                                // the next connection trust on first use again.
                                androidx.compose.material3.TextButton(onClick = viewModel::forgetCert) {
                                    Text(t("Забыть сертификат (после переустановки хаба)", "Forget certificate (after reinstalling the hub)"))
                                }
                            }
                        }
                    }

                    Card(modifier = Modifier.fillMaxWidth()) {
                        Column(modifier = Modifier.padding(16.dp)) {
                            Text(t("База уязвимостей", "Vulnerability database"), style = MaterialTheme.typography.titleMedium)
                            val status = when {
                                state.vulnDb?.refreshing == true -> t("Обновляется…", "Updating…")
                                state.vulnDb?.available == true -> t("Готова", "Ready")
                                else -> t("Ещё не загружена", "Not downloaded yet")
                            }
                            Text(status, modifier = Modifier.padding(top = 8.dp))
                            state.vulnDb?.error?.let {
                                Text(
                                    text = t("Ошибка: $it", "Error: $it"),
                                    color = MaterialTheme.colorScheme.error,
                                )
                            }
                        }
                    }
                    androidx.compose.material3.OutlinedButton(
                        onClick = { viewModel.logout(onSignedOut) },
                        modifier = Modifier.padding(top = 16.dp),
                    ) { Text(t("Выйти", "Sign out")) }
                }
            }
        }
    }
}

/** The ready scales as chips, and a field for any percent 50–100. */
@Composable
private fun UiScalePicker(
    current: com.netknownsthat.domain.repository.UiScale,
    onPick: (com.netknownsthat.domain.repository.UiScale) -> Unit,
) {
    val presets = com.netknownsthat.domain.repository.UiScale.PRESETS
    val range = com.netknownsthat.domain.repository.UiScale.CUSTOM
    androidx.compose.foundation.layout.FlowRow(modifier = Modifier.padding(top = 8.dp)) {
        presets.forEach { s ->
            androidx.compose.material3.FilterChip(
                selected = s == current,
                onClick = { onPick(s) },
                label = { Text("${s.percent}%") },
                modifier = Modifier.padding(end = 6.dp),
            )
        }
        // A typed-in scale is shown as its own chip, selected.
        if (current !in presets) {
            androidx.compose.material3.FilterChip(selected = true, onClick = {}, label = { Text(t("свой: ${current.percent}%", "custom: ${current.percent}%")) })
        }
    }
    var text by androidx.compose.runtime.saveable.rememberSaveable { androidx.compose.runtime.mutableStateOf("") }
    val value = text.trim().toIntOrNull()
    val valid = value != null && value in range
    androidx.compose.foundation.layout.Row(
        verticalAlignment = Alignment.CenterVertically,
        modifier = Modifier.padding(top = 8.dp),
    ) {
        androidx.compose.material3.OutlinedTextField(
            value = text,
            onValueChange = { v -> text = v.filter(Char::isDigit).take(3) },
            singleLine = true,
            isError = text.isNotEmpty() && !valid,
            label = { Text(t("Свой, ${range.first}–${range.last} %", "Custom, ${range.first}–${range.last} %")) },
            keyboardOptions = androidx.compose.foundation.text.KeyboardOptions(keyboardType = androidx.compose.ui.text.input.KeyboardType.Number),
            modifier = Modifier.weight(1f),
        )
        androidx.compose.material3.TextButton(
            onClick = { onPick(com.netknownsthat.domain.repository.UiScale(value!!)); text = "" },
            enabled = valid,
            modifier = Modifier.padding(start = 8.dp),
        ) { Text(t("Применить", "Apply")) }
    }
}
