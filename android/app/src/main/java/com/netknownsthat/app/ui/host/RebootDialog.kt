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
import com.netknownsthat.app.i18n.t

private val KIND_LABEL get() = mapOf(
    "docker" to "Docker",
    "podman" to "Podman",
    "lxd" to "LXD",
    "vm" to t("ВМ", "VMs"),
    "service" to t("служба", "service"),
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
        title = { Text(t("Перезагрузить $hostName?", "Reboot $hostName?")) },
        text = {
            Column(modifier = Modifier.verticalScroll(rememberScrollState())) {
                when {
                    p == null && viewModel.loading -> CircularProgressIndicator()
                    p == null -> Text(t("Не удалось получить сведения о хосте", "Could not get the host's details"))
                    else -> {
                        if (p.rebootRequired) {
                            Text(t("Перезагрузка требуется после обновлений", "A reboot is required after updates"), color = statusColor(HealthStatus.WARN))
                        }
                        val running = p.running.filterValues { it > 0 }
                            .entries.joinToString(", ") { "${KIND_LABEL[it.key] ?: it.key}: ${it.value}" }
                        if (running.isNotEmpty()) Text(t("Работает сейчас — $running", "Running now — $running"))
                        if (p.noAutostart.isEmpty()) {
                            Text(t("Всё работающее поднимется само.", "Everything running will come back by itself."), color = statusColor(HealthStatus.OK))
                        } else {
                            Text(
                                t("Не поднимутся сами (${p.noAutostart.size}):", "Will not come back by themselves (${p.noAutostart.size}):"),
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
                            Text(t("Стенд с фикстурами — перезагрузка не выполняется", "Fixtures stand — the reboot is not performed"), style = MaterialTheme.typography.bodySmall)
                        }
                    }
                }
            }
        },
        confirmButton = {
            TextButton(
                enabled = p != null && !viewModel.loading,
                onClick = { viewModel.reboot(onDone = onDismiss) },
            ) { Text(t("Перезагрузить", "Reboot"), color = MaterialTheme.colorScheme.error) }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text(t("Отмена", "Cancel")) } },
    )
}
