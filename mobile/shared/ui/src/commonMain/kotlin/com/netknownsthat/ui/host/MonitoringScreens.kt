package com.netknownsthat.ui.host

import com.netknownsthat.ui.common.formatTs
import androidx.compose.foundation.layout.Box
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.TextButton
import androidx.compose.material3.IconButton
import androidx.compose.material3.Icon
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.OutlinedTextField
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.rememberScrollState
import androidx.compose.material3.Button
import androidx.compose.material3.FilterChip
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.material3.Card
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.netknownsthat.domain.model.Target
import com.netknownsthat.ui.status.targetHealth
import com.netknownsthat.ui.theme.StatusDot
import com.netknownsthat.ui.theme.statusColor
import com.netknownsthat.domain.model.VulnFinding
import com.netknownsthat.ui.i18n.t
import com.netknownsthat.ui.common.fmt
import com.netknownsthat.ui.common.SectionContent

/** trivy's own scale, worst first. */
private val VULN_SEVERITIES = listOf("CRITICAL", "HIGH", "MEDIUM", "LOW", "UNKNOWN")

@Composable
fun VulnerabilitiesScreen(viewModel: VulnerabilitiesViewModel) {
    val scanning = viewModel.state.data?.scanning == true
    // Polling starts only while a scan is actually running (see the
    // ViewModel) — the loop ends by itself when `scanning` goes false.
    LaunchedEffect(scanning) { if (scanning) viewModel.pollWhileScanning() }

    SectionContent(state = viewModel.state, emptyText = t("Нет данных", "No data")) { response ->
        Column {
            Card(modifier = Modifier.fillMaxWidth().padding(16.dp)) {
                Column(modifier = Modifier.padding(16.dp)) {
                    if (response.scanning) {
                        Text(t("Сканирование…", "Scanning…"), style = MaterialTheme.typography.titleSmall)
                        if (response.progress.isNotBlank()) {
                            Text(
                                text = response.progress,
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                                modifier = Modifier.padding(top = 4.dp),
                            )
                        }
                        LinearProgressIndicator(
                            modifier = Modifier.fillMaxWidth().padding(top = 12.dp),
                        )
                    } else {
                        val scan = response.scan
                        if (scan == null) {
                            Text(t("Хост ещё не сканировался", "The host has not been scanned yet"))
                        } else {
                            Text(
                                text = t("Найдено уязвимостей: ${scan.findings.size}", "Vulnerabilities found: ${scan.findings.size}"),
                                style = MaterialTheme.typography.titleSmall,
                            )
                            if (scan.compared) {
                                Text(
                                    text = t("Новых: ${scan.newCount} · исправлено: ${scan.fixedCount}", "New: ${scan.newCount} · fixed: ${scan.fixedCount}"),
                                    style = MaterialTheme.typography.bodySmall,
                                    modifier = Modifier.padding(top = 4.dp),
                                )
                            }
                            if (scan.scannedAt.isNotBlank()) {
                                Text(
                                    text = t("Проверено: ${formatTs(scan.scannedAt)}", "Checked: ${formatTs(scan.scannedAt)}"),
                                    style = MaterialTheme.typography.bodySmall,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                                )
                            }
                            scan.warnings.forEach {
                                Text(
                                    text = it,
                                    style = MaterialTheme.typography.bodySmall,
                                    color = MaterialTheme.colorScheme.tertiary,
                                )
                            }
                        }
                        response.error?.let {
                            Text(
                                text = it,
                                color = MaterialTheme.colorScheme.error,
                                style = MaterialTheme.typography.bodySmall,
                                modifier = Modifier.padding(top = 4.dp),
                            )
                        }
                        Button(
                            onClick = { viewModel.startScan() },
                            enabled = !viewModel.actionInProgress,
                            modifier = Modifier.padding(top = 12.dp),
                        ) { Text(t("Сканировать", "Scan")) }
                    }
                }
            }

            val findings = response.scan?.findings.orEmpty()
                .sortedBy { VULN_SEVERITIES.indexOf(it.severity).let { i -> if (i < 0) 99 else i } }
            LazyColumn(contentPadding = PaddingValues(horizontal = 16.dp, vertical = 8.dp)) {
                items(findings) { VulnCard(it) }
            }
        }
    }
}

@Composable
private fun VulnCard(finding: VulnFinding) {
    Card(modifier = Modifier.fillMaxWidth().padding(bottom = 8.dp)) {
        Column(modifier = Modifier.padding(16.dp)) {
            Row(modifier = Modifier.fillMaxWidth()) {
                Text(
                    text = finding.id,
                    style = MaterialTheme.typography.titleSmall,
                    modifier = Modifier.weight(1f),
                )
                Text(
                    text = finding.severity,
                    style = MaterialTheme.typography.labelMedium,
                    color = when (finding.severity) {
                        "CRITICAL", "HIGH" -> MaterialTheme.colorScheme.error
                        "MEDIUM" -> MaterialTheme.colorScheme.tertiary
                        else -> MaterialTheme.colorScheme.onSurfaceVariant
                    },
                )
            }
            if (finding.new) {
                Text(
                    text = t("Новая", "New"),
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.error,
                )
            }
            Text(
                text = "${finding.packageName} ${finding.installedVersion}",
                style = MaterialTheme.typography.bodyMedium,
                modifier = Modifier.padding(top = 4.dp),
            )
            // "No fix yet" is a different situation from "upgrade now", and
            // the Go side keeps them distinct on purpose.
            Text(
                text = if (finding.fixedVersion.isBlank()) t("Исправления пока нет", "No fix yet")
                else t("Исправлено в ${finding.fixedVersion}", "Fixed in ${finding.fixedVersion}"),
                style = MaterialTheme.typography.bodySmall,
                color = if (finding.fixedVersion.isBlank()) MaterialTheme.colorScheme.onSurfaceVariant
                else MaterialTheme.colorScheme.primary,
            )
            if (finding.target.isNotBlank()) {
                Text(
                    text = t("В образе: ${finding.target}", "In image: ${finding.target}"),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            if (finding.title.isNotBlank()) {
                Text(
                    text = finding.title,
                    style = MaterialTheme.typography.bodySmall,
                    modifier = Modifier.padding(top = 4.dp),
                )
            }
        }
    }
}

@Composable
fun AvailabilityScreen(viewModel: AvailabilityViewModel, onHistory: (com.netknownsthat.domain.model.Target) -> Unit = {}) {
    var sourceFilter by remember { mutableStateOf<String?>(null) }
    var adding by remember { mutableStateOf(false) }
    var deleting by remember { mutableStateOf<Target?>(null) }
    // A target of your own — an address no host config mentions.
    com.netknownsthat.ui.common.ScreenActions {
        IconButton(onClick = { adding = true }) {
            Icon(Icons.Default.Add, contentDescription = t("Добавить цель", "Add target"))
        }
    }
    if (adding) AddTargetDialog(onDismiss = { adding = false }) { target ->
        adding = false
        viewModel.add(target)
    }
    deleting?.let { target ->
        AlertDialog(
            onDismissRequest = { deleting = null },
            title = { Text(t("Удалить цель?", "Delete the target?")) },
            text = { Text(target.label) },
            confirmButton = {
                TextButton(onClick = {
                    deleting = null
                    viewModel.delete(target.id)
                }) { Text(t("Удалить", "Delete")) }
            },
            dismissButton = { TextButton(onClick = { deleting = null }) { Text(t("Отмена", "Cancel")) } },
        )
    }
    SectionContent(state = viewModel.state, emptyText = t("Целей нет", "No targets")) { data ->
        // Targets come from several places — web servers, compose, and
        // Kubernetes (ingresses, NodePort/LoadBalancer services, nodes);
        // a chip per source keeps the k8s ones findable on a phone.
        val sources = data.targets.targets.map { it.source }.filter { it.isNotBlank() }.distinct().sorted()
        val visible = data.targets.targets.filter { sourceFilter == null || it.source == sourceFilter }
        LazyColumn(contentPadding = PaddingValues(16.dp)) {
            if (sources.size > 1) {
                item {
                    Row(modifier = Modifier.horizontalScroll(rememberScrollState()).padding(bottom = 8.dp)) {
                        FilterChip(
                            selected = sourceFilter == null,
                            onClick = { sourceFilter = null },
                            label = { Text(t("Все ${data.targets.targets.size}", "All ${data.targets.targets.size}")) },
                            modifier = Modifier.padding(end = 8.dp),
                        )
                        sources.forEach { src ->
                            FilterChip(
                                selected = sourceFilter == src,
                                onClick = { sourceFilter = if (sourceFilter == src) null else src },
                                label = {
                                    Text("${if (src == "kubernetes") "Kubernetes" else src} ${data.targets.targets.count { it.source == src }}")
                                },
                                modifier = Modifier.padding(end = 8.dp),
                            )
                        }
                    }
                }
            }
            if (data.targets.simulated) {
                item {
                    Text(
                        text = t("Демонстрационные данные", "Demo data"),
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.tertiary,
                        modifier = Modifier.padding(bottom = 8.dp),
                    )
                }
            }
            items(visible, key = { it.id }) { target ->
                TargetCard(
                    target,
                    onCheck = { viewModel.check(target.id) },
                    onHistory = { onHistory(target) },
                    onToggle = { viewModel.setEnabled(target.id, !target.enabled) },
                    // Only targets added by hand: the rest come from the
                    // host's own configs and would come back on the next scan.
                    onDelete = if (target.source == "manual") ({ deleting = target }) else null,
                )
            }

            if (data.outages.outages.isNotEmpty()) {
                item {
                    Text(
                        text = t("Недоступность за сутки", "Outages in the last day"),
                        style = MaterialTheme.typography.titleMedium,
                        modifier = Modifier.padding(vertical = 12.dp),
                    )
                }
                items(data.outages.outages) { outage ->
                    Card(modifier = Modifier.fillMaxWidth().padding(bottom = 8.dp)) {
                        Column(modifier = Modifier.padding(16.dp)) {
                            Text(outage.label, style = MaterialTheme.typography.titleSmall)
                            Text(
                                text = t("${outage.start} — ${outage.end.ifBlank { "продолжается" }}", "${outage.start} — ${outage.end.ifBlank { "ongoing" }}"),
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                            if (outage.error.isNotBlank()) {
                                Text(
                                    text = outage.error,
                                    style = MaterialTheme.typography.bodySmall,
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

@Composable
private fun TargetCard(target: Target, onCheck: () -> Unit, onHistory: () -> Unit, onToggle: () -> Unit, onDelete: (() -> Unit)?) {
    var menu by remember { mutableStateOf(false) }
    // A tap opens the history charts, as a row click does in the web.
    Card(onClick = onHistory, modifier = Modifier.fillMaxWidth().padding(bottom = 8.dp)) {
        Column(modifier = Modifier.padding(16.dp)) {
            val health = targetHealth(target.lastOk, target.enabled)
            Row(
                modifier = Modifier.fillMaxWidth(),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                StatusDot(health)
                Text(
                    text = target.label,
                    style = MaterialTheme.typography.titleSmall,
                    modifier = Modifier.weight(1f),
                )

                Text(
                    // last_ok is nullable on the Go side: null means the
                    // target has never been checked, which is not "down".
                    text = when {
                        !target.enabled -> t("проверки выключены", "checks disabled")
                        target.lastOk == true -> t("доступен", "up")
                        target.lastOk == false -> t("недоступен", "down")
                        else -> t("не проверялся", "not checked")
                    },
                    style = MaterialTheme.typography.labelMedium,
                    color = statusColor(health),
                )
                Box {
                    IconButton(onClick = { menu = true }) {
                        Icon(Icons.Default.MoreVert, contentDescription = t("Действия с целью", "Target actions"))
                    }
                    DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                        DropdownMenuItem(text = { Text(t("История и графики", "History and charts")) }, onClick = { menu = false; onHistory() })
                        DropdownMenuItem(text = { Text(t("Проверить сейчас", "Check now")) }, onClick = { menu = false; onCheck() })
                        DropdownMenuItem(
                            text = { Text(if (target.enabled) t("Выключить проверки", "Disable checks") else t("Включить проверки", "Enable checks")) },
                            onClick = { menu = false; onToggle() },
                        )
                        if (onDelete != null) {
                            DropdownMenuItem(text = { Text(t("Удалить", "Delete")) }, onClick = { menu = false; onDelete() })
                        }
                    }
                }
            }
            Text(
                text = listOfNotNull(
                    target.kind.takeIf { it.isNotBlank() },
                    target.host.takeIf { it.isNotBlank() },
                    target.port.takeIf { it > 0 }?.toString(),
                ).joinToString(" · "),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(top = 4.dp),
            )
            if (target.checks24h > 0) {
                Text(
                    text = t("Аптайм 24 ч: ${target.uptime24h.fmt(1)}% · задержка ${target.avgLatency24h.fmt(0)} мс", "Uptime 24 h: ${target.uptime24h.fmt(1)}% · latency ${target.avgLatency24h.fmt(0)} ms"),
                    style = MaterialTheme.typography.bodySmall,
                    modifier = Modifier.padding(top = 4.dp),
                )
            }
            if (target.lastError.isNotBlank()) {
                Text(
                    text = target.lastError,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
            }
        }
    }
}


/** A target of your own: kind, address, port (and path for HTTP), label. */
@Composable
private fun AddTargetDialog(onDismiss: () -> Unit, onAdd: (com.netknownsthat.domain.model.NewTarget) -> Unit) {
    var kind by remember { mutableStateOf("icmp") }
    var host by remember { mutableStateOf("") }
    var port by remember { mutableStateOf("") }
    var path by remember { mutableStateOf("/") }
    var label by remember { mutableStateOf("") }
    val needPort = kind != "icmp"
    val portNum = port.toIntOrNull()
    val ok = host.isNotBlank() && (!needPort || (portNum != null && portNum in 1..65535))
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(t("Своя цель", "Your own target")) },
        text = {
            Column {
                Text(
                    t("Адрес, которого нет в конфигурациях хоста: проверяется с этого хоста.", "An address no host config mentions: checked from this host."),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Row(modifier = Modifier.horizontalScroll(rememberScrollState()).padding(vertical = 8.dp)) {
                    listOf("icmp" to "ping", "tcp" to "TCP", "http" to "HTTP", "https" to "HTTPS").forEach { (id, name) ->
                        FilterChip(
                            selected = kind == id,
                            onClick = {
                                kind = id
                                if (port.isBlank()) port = when (id) { "https" -> "443"; "http" -> "80"; else -> "" }
                            },
                            label = { Text(name) },
                            modifier = Modifier.padding(end = 6.dp),
                        )
                    }
                }
                OutlinedTextField(value = host, onValueChange = { host = it }, label = { Text(t("Адрес", "Address")) }, singleLine = true, modifier = Modifier.fillMaxWidth())
                if (needPort) {
                    OutlinedTextField(value = port, onValueChange = { port = it.filter(Char::isDigit).take(5) }, label = { Text(t("Порт", "Port")) }, singleLine = true, modifier = Modifier.fillMaxWidth())
                }
                if (kind.startsWith("http")) {
                    OutlinedTextField(value = path, onValueChange = { path = it }, label = { Text(t("Путь", "Path")) }, singleLine = true, modifier = Modifier.fillMaxWidth())
                }
                OutlinedTextField(value = label, onValueChange = { label = it }, label = { Text(t("Название (по желанию)", "Label (optional)")) }, singleLine = true, modifier = Modifier.fillMaxWidth())
            }
        },
        confirmButton = {
            TextButton(enabled = ok, onClick = {
                onAdd(com.netknownsthat.domain.model.NewTarget(label = label, kind = kind, host = host, port = if (needPort) portNum ?: 0 else 0, path = path))
            }) { Text(t("Добавить", "Add")) }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text(t("Отмена", "Cancel")) } },
    )
}
