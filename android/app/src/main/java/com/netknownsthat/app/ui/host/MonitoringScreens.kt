package com.netknownsthat.app.ui.host

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.netknownsthat.app.net.model.Target
import com.netknownsthat.app.status.targetHealth
import com.netknownsthat.app.ui.theme.StatusDot
import com.netknownsthat.app.ui.theme.statusColor
import com.netknownsthat.app.net.model.VulnFinding
import java.util.Locale

/** trivy's own scale, worst first. */
private val VULN_SEVERITIES = listOf("CRITICAL", "HIGH", "MEDIUM", "LOW", "UNKNOWN")

@Composable
fun VulnerabilitiesScreen(viewModel: VulnerabilitiesViewModel) {
    val scanning = viewModel.state.data?.scanning == true
    // Polling starts only while a scan is actually running (see the
    // ViewModel) — the loop ends by itself when `scanning` goes false.
    LaunchedEffect(scanning) { if (scanning) viewModel.pollWhileScanning() }

    SectionContent(state = viewModel.state, emptyText = "Нет данных") { response ->
        Column {
            Card(modifier = Modifier.fillMaxWidth().padding(16.dp)) {
                Column(modifier = Modifier.padding(16.dp)) {
                    if (response.scanning) {
                        Text("Сканирование…", style = MaterialTheme.typography.titleSmall)
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
                            Text("Хост ещё не сканировался")
                        } else {
                            Text(
                                text = "Найдено уязвимостей: ${scan.findings.size}",
                                style = MaterialTheme.typography.titleSmall,
                            )
                            if (scan.compared) {
                                Text(
                                    text = "Новых: ${scan.newCount} · исправлено: ${scan.fixedCount}",
                                    style = MaterialTheme.typography.bodySmall,
                                    modifier = Modifier.padding(top = 4.dp),
                                )
                            }
                            if (scan.scannedAt.isNotBlank()) {
                                Text(
                                    text = "Проверено: ${scan.scannedAt}",
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
                        ) { Text("Сканировать") }
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
                    text = "Новая",
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
                text = if (finding.fixedVersion.isBlank()) "Исправления пока нет"
                else "Исправлено в ${finding.fixedVersion}",
                style = MaterialTheme.typography.bodySmall,
                color = if (finding.fixedVersion.isBlank()) MaterialTheme.colorScheme.onSurfaceVariant
                else MaterialTheme.colorScheme.primary,
            )
            if (finding.target.isNotBlank()) {
                Text(
                    text = "В образе: ${finding.target}",
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
fun AvailabilityScreen(viewModel: AvailabilityViewModel) {
    SectionContent(state = viewModel.state, emptyText = "Целей нет") { data ->
        LazyColumn(contentPadding = PaddingValues(16.dp)) {
            if (data.targets.simulated) {
                item {
                    Text(
                        text = "Демонстрационные данные",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.tertiary,
                        modifier = Modifier.padding(bottom = 8.dp),
                    )
                }
            }
            items(data.targets.targets, key = { it.id }) { TargetCard(it) }

            if (data.outages.outages.isNotEmpty()) {
                item {
                    Text(
                        text = "Недоступность за сутки",
                        style = MaterialTheme.typography.titleMedium,
                        modifier = Modifier.padding(vertical = 12.dp),
                    )
                }
                items(data.outages.outages) { outage ->
                    Card(modifier = Modifier.fillMaxWidth().padding(bottom = 8.dp)) {
                        Column(modifier = Modifier.padding(16.dp)) {
                            Text(outage.label, style = MaterialTheme.typography.titleSmall)
                            Text(
                                text = "${outage.start} — ${outage.end.ifBlank { "продолжается" }}",
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
private fun TargetCard(target: Target) {
    Card(modifier = Modifier.fillMaxWidth().padding(bottom = 8.dp)) {
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
                        !target.enabled -> "проверки выключены"
                        target.lastOk == true -> "доступен"
                        target.lastOk == false -> "недоступен"
                        else -> "не проверялся"
                    },
                    style = MaterialTheme.typography.labelMedium,
                    color = statusColor(health),
                )
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
                    text = "Аптайм 24 ч: %.1f%% · задержка %.0f мс".format(
                        Locale.getDefault(), target.uptime24h, target.avgLatency24h,
                    ),
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

@Composable
fun UsageScreen(viewModel: UsageViewModel) {
    SectionContent(state = viewModel.state, emptyText = "Нет данных о нагрузке") { data ->
        LazyColumn(contentPadding = PaddingValues(16.dp)) {
            if (data.top.top.isNotEmpty()) {
                item {
                    Card(modifier = Modifier.fillMaxWidth().padding(bottom = 16.dp)) {
                        Column(modifier = Modifier.padding(16.dp)) {
                            Text(
                                text = "Больше всего: ${data.top.metric}",
                                style = MaterialTheme.typography.titleMedium,
                            )
                            val max = data.top.top.maxOf { it.total }.coerceAtLeast(1.0)
                            data.top.top.forEach { entry ->
                                Column(modifier = Modifier.padding(top = 12.dp)) {
                                    Row(modifier = Modifier.fillMaxWidth()) {
                                        Text(
                                            text = entry.subject,
                                            style = MaterialTheme.typography.bodyMedium,
                                            modifier = Modifier.weight(1f),
                                        )
                                        Text(
                                            text = "%.1f".format(Locale.getDefault(), entry.total),
                                            style = MaterialTheme.typography.bodyMedium,
                                        )
                                    }
                                    // A bar rather than a chart library: one
                                    // proportion per row is all this data is,
                                    // and it stays readable on a phone.
                                    LinearProgressIndicator(
                                        progress = { (entry.total / max).toFloat() },
                                        modifier = Modifier
                                            .fillMaxWidth()
                                            .height(6.dp)
                                            .padding(top = 4.dp),
                                    )
                                }
                            }
                        }
                    }
                }
            }

            if (data.jobs.jobs.isNotEmpty()) {
                item {
                    Text(
                        text = "Фоновые задания",
                        style = MaterialTheme.typography.titleMedium,
                        modifier = Modifier.padding(bottom = 8.dp),
                    )
                }
                items(data.jobs.jobs, key = { it.name }) { job ->
                    Card(modifier = Modifier.fillMaxWidth().padding(bottom = 8.dp)) {
                        Column(modifier = Modifier.padding(16.dp)) {
                            Text(job.name, style = MaterialTheme.typography.titleSmall)
                            Text(
                                text = "Интервал ${job.interval} · запусков ${job.runs}",
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                            if (job.lastRun.isNotBlank()) {
                                Text(
                                    text = "Последний: ${job.lastRun} (${job.durationMs} мс, " +
                                        "${job.lastCount} шт.)",
                                    style = MaterialTheme.typography.bodySmall,
                                )
                            }
                        }
                    }
                }
            }

            if (data.usage.points.isEmpty() && data.top.top.isEmpty()) {
                item {
                    Box(modifier = Modifier.fillMaxWidth().padding(24.dp)) {
                        Text(
                            text = "Метрики не собираются",
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                }
            }
        }
    }
}
