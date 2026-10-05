package com.netknownsthat.app.ui.host

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.ui.Modifier
import com.netknownsthat.app.status.HealthStatus
import com.netknownsthat.app.ui.hub.RebootViewModel
import com.netknownsthat.app.ui.theme.statusColor

private val KIND_LABEL = mapOf(
    "docker" to "Docker",
    "podman" to "Podman",
    "lxd" to "LXD",
    "vm" to "ВМ",
    "service" to "служба",
)

/**
 * Reboot with a preview first, like the web UI: what is running now and,
 * more to the point, what will not come back on its own afterwards.
 */
@Composable
fun RebootDialog(hostName: String, viewModel: RebootViewModel, onDismiss: () -> Unit) {
    LaunchedEffect(Unit) { viewModel.loadPreview() }
    val p = viewModel.preview
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("Перезагрузить $hostName?") },
        text = {
            Column(modifier = Modifier.verticalScroll(rememberScrollState())) {
                when {
                    p == null && viewModel.loading -> CircularProgressIndicator()
                    p == null -> Text("Не удалось получить сведения о хосте")
                    else -> {
                        if (p.rebootRequired) {
                            Text("Перезагрузка требуется после обновлений", color = statusColor(HealthStatus.WARN))
                        }
                        val running = p.running.filterValues { it > 0 }
                            .entries.joinToString(", ") { "${KIND_LABEL[it.key] ?: it.key}: ${it.value}" }
                        if (running.isNotEmpty()) Text("Работает сейчас — $running")
                        if (p.noAutostart.isEmpty()) {
                            Text("Всё работающее поднимется само.", color = statusColor(HealthStatus.OK))
                        } else {
                            Text(
                                "Не поднимутся сами (${p.noAutostart.size}):",
                                color = statusColor(HealthStatus.WARN),
                                style = MaterialTheme.typography.titleSmall,
                            )
                            p.noAutostart.forEach {
                                Text(
                                    "${KIND_LABEL[it.kind] ?: it.kind} ${it.name} — ${it.reason}",
                                    style = MaterialTheme.typography.bodySmall,
                                )
                            }
                        }
                        if (p.simulated) {
                            Text("Стенд с фикстурами — перезагрузка не выполняется", style = MaterialTheme.typography.bodySmall)
                        }
                    }
                }
            }
        },
        confirmButton = {
            TextButton(
                enabled = p != null && !viewModel.loading,
                onClick = { viewModel.reboot(onDone = onDismiss) },
            ) { Text("Перезагрузить", color = MaterialTheme.colorScheme.error) }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Отмена") } },
    )
}
