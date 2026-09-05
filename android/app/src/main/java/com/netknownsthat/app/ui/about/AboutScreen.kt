package com.netknownsthat.app.ui.about

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
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
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun AboutScreen(
    viewModel: AboutViewModel,
    onBack: () -> Unit,
) {
    val state = viewModel.uiState

    // See HostListScreen: loading from the ViewModel's init races the hub
    // URL being restored.
    LaunchedEffect(Unit) { viewModel.refresh() }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("О системе") },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Назад")
                    }
                },
            )
        },
    ) { padding ->
        Box(modifier = Modifier.fillMaxSize().padding(padding)) {
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
                            Text("Версия хаба", style = MaterialTheme.typography.titleMedium)
                            Text(
                                "Текущая: ${state.version?.current ?: "—"}",
                                modifier = Modifier.padding(top = 8.dp),
                            )
                            state.version?.latest?.let {
                                Text("Последняя доступная: $it")
                            }
                            if (state.version?.updateAvailable == true) {
                                Text(
                                    text = "Доступно обновление",
                                    color = MaterialTheme.colorScheme.tertiary,
                                )
                            }
                            state.version?.checkError?.let {
                                Text(
                                    text = "Проверка не удалась: $it",
                                    color = MaterialTheme.colorScheme.error,
                                )
                            }
                        }
                    }

                    state.certFingerprint?.let { fingerprint ->
                        Card(modifier = Modifier.fillMaxWidth().padding(bottom = 16.dp)) {
                            Column(modifier = Modifier.padding(16.dp)) {
                                Text(
                                    "Сертификат хаба",
                                    style = MaterialTheme.typography.titleMedium,
                                )
                                Text(
                                    text = "Самоподписанный, закреплён при первом подключении. " +
                                        "Сверьте отпечаток с тем, что показывает сам хаб:",
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
                            }
                        }
                    }

                    Card(modifier = Modifier.fillMaxWidth()) {
                        Column(modifier = Modifier.padding(16.dp)) {
                            Text("База уязвимостей", style = MaterialTheme.typography.titleMedium)
                            val status = when {
                                state.vulnDb?.refreshing == true -> "Обновляется…"
                                state.vulnDb?.available == true -> "Готова"
                                else -> "Ещё не загружена"
                            }
                            Text(status, modifier = Modifier.padding(top = 8.dp))
                            state.vulnDb?.error?.let {
                                Text(
                                    text = "Ошибка: $it",
                                    color = MaterialTheme.colorScheme.error,
                                )
                            }
                        }
                    }
                }
            }
        }
    }
}
