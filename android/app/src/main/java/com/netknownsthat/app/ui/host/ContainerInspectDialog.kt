package com.netknownsthat.app.ui.host

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Surface
import androidx.compose.material3.Tab
import androidx.compose.material3.TabRow
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import com.netknownsthat.app.net.model.ContainerInspect
import com.netknownsthat.app.status.HealthStatus
import com.netknownsthat.app.ui.hub.formatTs
import com.netknownsthat.app.ui.hub.humanBytes
import com.netknownsthat.app.ui.theme.statusColor
import com.netknownsthat.app.i18n.t

private val ORIGIN get() = mapOf(
    "container" to t("контейнер", "container"),
    "override" to t("поверх образа", "over the image"),
    "image" to t("из образа", "from the image"),
)

/**
 * «Инспект» of a Docker container, like the web UI's ContainerInspectModal:
 * environment variables with where each comes from, then image, command,
 * state, ports, mounts and networks. Values are secrets — the host sends
 * them only to an admin who asks, and writes that into its audit log.
 */
@Composable
fun ContainerInspectDialog(name: String, viewModel: ContainersViewModel, onDismiss: () -> Unit) {
    var reveal by remember { mutableStateOf(false) }
    var confirmReveal by remember { mutableStateOf(false) }
    var tab by remember { mutableIntStateOf(0) }
    LaunchedEffect(name, reveal) { viewModel.loadInspect(name, reveal) }
    val d = viewModel.inspect?.takeIf { it.name == name }

    if (confirmReveal) {
        AlertDialog(
            onDismissRequest = { confirmReveal = false },
            text = { Text(t("Показать значения переменных $name? Это пароли и ключи; просмотр запишется в журнал действий хоста.", "Show the values of $name's variables? They are passwords and keys; viewing is written to the host's audit log.")) },
            confirmButton = {
                TextButton(onClick = {
                    reveal = true
                    confirmReveal = false
                }) { Text(t("Показать", "Show")) }
            },
            dismissButton = { TextButton(onClick = { confirmReveal = false }) { Text(t("Отмена", "Cancel")) } },
        )
    }

    Dialog(onDismissRequest = onDismiss, properties = DialogProperties(usePlatformDefaultWidth = false)) {
        Surface(modifier = Modifier.fillMaxSize()) {
            Column(modifier = Modifier.fillMaxSize().padding(16.dp)) {
                Text(t("Инспект $name", "Inspect $name"), style = MaterialTheme.typography.titleMedium)
                viewModel.inspectError?.let { Text(it, color = MaterialTheme.colorScheme.error) }
                if (d == null) {
                    if (viewModel.inspectError == null) CircularProgressIndicator(modifier = Modifier.padding(24.dp))
                } else {
                    TabRow(selectedTabIndex = tab, modifier = Modifier.padding(top = 8.dp)) {
                        listOf(t("Переменные (${d.env.size})", "Variables (${d.env.size})"), t("Основное", "Main"), t("Сеть и тома", "Network and volumes")).forEachIndexed { i, t ->
                            Tab(selected = tab == i, onClick = { tab = i }, text = { Text(t) })
                        }
                    }
                    Column(modifier = Modifier.weight(1f).padding(top = 8.dp)) {
                        when (tab) {
                            0 -> EnvTab(d, onReveal = { confirmReveal = true })
                            1 -> MainTab(d)
                            else -> NetTab(d)
                        }
                    }
                }
                Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = androidx.compose.foundation.layout.Arrangement.End) {
                    TextButton(onClick = onDismiss) { Text(t("Закрыть", "Close")) }
                }
            }
        }
    }
}

@Composable
private fun EnvTab(d: ContainerInspect, onReveal: () -> Unit) {
    LazyColumn {
        item {
            if (!d.revealed) {
                OutlinedButton(onClick = onReveal) { Text(t("Показать значения", "Show values")) }
                Text(
                    t("Значения видит только администратор", "Only an administrator sees the values"),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            } else {
                Text(t("Значения показаны", "Values shown"), color = statusColor(HealthStatus.WARN), style = MaterialTheme.typography.bodySmall)
            }
        }
        items(d.env, key = { it.name }) { e ->
            Column(modifier = Modifier.padding(vertical = 4.dp)) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(e.name, fontFamily = FontFamily.Monospace, modifier = Modifier.weight(1f))
                    Text(
                        ORIGIN[e.origin] ?: e.origin,
                        style = MaterialTheme.typography.labelSmall,
                        color = if (e.origin == "image") MaterialTheme.colorScheme.onSurfaceVariant else MaterialTheme.colorScheme.primary,
                    )
                }
                Text(
                    if (e.masked) "••••••" else e.value,
                    fontFamily = FontFamily.Monospace,
                    style = MaterialTheme.typography.bodySmall,
                )
            }
        }
    }
}

@Composable
private fun MainTab(d: ContainerInspect) {
    LazyColumn {
        item {
            if (d.imageOutdated) {
                Text(
                    t("Тег ${d.image} теперь указывает на другой образ — контейнер не пересоздан", "Tag ${d.image} now points to another image — the container was not recreated"),
                    color = statusColor(HealthStatus.WARN),
                    style = MaterialTheme.typography.bodySmall,
                )
            }
            LabeledValue(t("Образ", "Image"), d.image)
            LabeledValue(t("ID образа", "Image ID"), d.imageId.take(19))
            LabeledValue(
                t("Состояние", "State"),
                d.state + (if (d.health.isNotBlank()) " · ${d.health}" else "") + (if (d.state != "running") " · exit ${d.exitCode}" else ""),
            )
            LabeledValue(t("Создан", "Created"), formatTs(d.created))
            if (!d.startedAt.startsWith("0001")) LabeledValue(t("Запущен", "Started"), formatTs(d.startedAt))
            LabeledValue(t("Перезапуск", "Restart"), t("${d.restartPolicy.ifBlank { "no" }} · перезапусков ${d.restartCount}", "${d.restartPolicy.ifBlank { "no" }} · restarts ${d.restartCount}"))
            val cmd = (d.entrypoint.orEmpty() + d.cmd.orEmpty()).joinToString(" ")
            if (cmd.isNotBlank()) LabeledValue(t("Команда", "Command"), cmd)
            if (d.user.isNotBlank()) LabeledValue(t("Пользователь", "User"), d.user)
            if (d.workingDir.isNotBlank()) LabeledValue(t("Рабочий каталог", "Working directory"), d.workingDir)
            val limits = listOfNotNull(
                d.memoryLimit.takeIf { it > 0 }?.let { t("память ${humanBytes(it.toDouble())}", "memory ${humanBytes(it.toDouble())}") },
                d.nanoCpus.takeIf { it > 0 }?.let { "CPU ${it / 1e9}" },
            ).joinToString(" · ")
            if (limits.isNotEmpty()) LabeledValue(t("Ограничения", "Limits"), limits)
            if (d.composeProject.isNotBlank()) LabeledValue("Compose", "${d.composeProject} · ${d.composeService}")
            LabeledValue("ID", d.id.take(12))
        }
    }
}

@Composable
private fun NetTab(d: ContainerInspect) {
    LazyColumn {
        item { Text(t("Порты", "Ports"), style = MaterialTheme.typography.titleSmall) }
        if (d.ports.isEmpty()) item { Text("—") }
        items(d.ports) { p ->
            Text(
                (if (p.hostPort.isNotBlank()) "${p.hostIp.ifBlank { "0.0.0.0" }}:${p.hostPort} → " else "") + p.container,
                fontFamily = FontFamily.Monospace,
                style = MaterialTheme.typography.bodySmall,
            )
        }
        item { Text(t("Тома", "Volumes"), style = MaterialTheme.typography.titleSmall, modifier = Modifier.padding(top = 8.dp)) }
        if (d.mounts.isEmpty()) item { Text("—") }
        items(d.mounts) { m ->
            Text(
                "${m.type}: ${m.name.ifBlank { m.source }} → ${m.destination}" + if (m.rw) "" else " (ro)",
                fontFamily = FontFamily.Monospace,
                style = MaterialTheme.typography.bodySmall,
            )
        }
        item { Text(t("Сети", "Networks"), style = MaterialTheme.typography.titleSmall, modifier = Modifier.padding(top = 8.dp)) }
        items(d.networks) { n ->
            Text(
                n.name + (if (n.ip.isNotBlank()) " · ${n.ip}" else "") + (if (n.aliases.isNotEmpty()) " · ${n.aliases.joinToString()}" else ""),
                style = MaterialTheme.typography.bodySmall,
            )
        }
    }
}
