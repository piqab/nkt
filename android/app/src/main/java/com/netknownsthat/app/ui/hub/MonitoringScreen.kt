package com.netknownsthat.app.ui.hub

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.Card
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.netknownsthat.app.net.model.MonHost
import com.netknownsthat.app.net.model.MonInsight
import com.netknownsthat.app.status.HealthStatus
import com.netknownsthat.app.ui.host.SectionContent
import com.netknownsthat.app.ui.theme.StatusDot
import com.netknownsthat.app.ui.theme.statusColor

private fun pctHealth(pct: Double): HealthStatus = when {
    pct >= 90 -> HealthStatus.BAD
    pct >= 75 -> HealthStatus.WARN
    else -> HealthStatus.OK
}

/**
 * Hub monitoring on a phone: what to look at first (insights — disks filling
 * up, memory leaks, availability drops), then each host's CPU, memory and
 * disks, then the k8s clusters' nodes.
 */
@Composable
fun MonitoringScreen(viewModel: MonitoringViewModel, onOpenHost: (hostId: Long, path: String) -> Unit) {
    SectionContent(state = viewModel.state) { r ->
        LazyColumn(contentPadding = PaddingValues(16.dp)) {
            item {
                Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.padding(bottom = 8.dp)) {
                    Text(
                        text = if (r.collecting) "Идёт сбор…" else "Собрано ${formatTs(r.lastRun)}",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.weight(1f),
                    )
                    OutlinedButton(onClick = viewModel::collect, enabled = !r.collecting && !viewModel.actionInProgress) {
                        Text("Собрать сейчас")
                    }
                }
            }
            if (r.insights.isNotEmpty()) {
                item { Header("Обратить внимание") }
                items(r.insights) { InsightCard(it, onOpenHost) }
            }
            item { Header("Хосты") }
            items(r.hosts, key = { it.id }) { HostLoadCard(it, onOpen = { onOpenHost(it.id, "/usage") }) }
            if (r.clusters.isNotEmpty()) {
                item { Header("Kubernetes") }
                items(r.clusters, key = { it.hostId }) { c ->
                    Card(modifier = Modifier.fillMaxWidth().padding(bottom = 8.dp)) {
                        Column(modifier = Modifier.padding(12.dp)) {
                            Text(c.name, style = MaterialTheme.typography.titleSmall)
                            c.nodes.forEach { n ->
                                Row(verticalAlignment = Alignment.CenterVertically) {
                                    StatusDot(if (n.ready) HealthStatus.OK else HealthStatus.BAD)
                                    Text(
                                        text = "${n.name} · ${n.ip}" + if (n.controlPlane) " · control-plane" else " · worker",
                                        style = MaterialTheme.typography.bodySmall,
                                    )
                                }
                            }
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun Header(text: String) {
    Text(text, style = MaterialTheme.typography.titleMedium, modifier = Modifier.padding(top = 8.dp, bottom = 8.dp))
}

@Composable
private fun InsightCard(i: MonInsight, onOpenHost: (Long, String) -> Unit) {
    val health = if (i.severity == "critical") HealthStatus.BAD else HealthStatus.WARN
    Card(modifier = Modifier.fillMaxWidth().padding(bottom = 8.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.padding(12.dp)) {
            StatusDot(health)
            Text(i.text, style = MaterialTheme.typography.bodySmall, modifier = Modifier.weight(1f))
            if (i.hostId != 0L && i.path.isNotBlank()) {
                TextButton(onClick = { onOpenHost(i.hostId, i.path) }) { Text("К хосту") }
            }
        }
    }
}

@Composable
private fun HostLoadCard(h: MonHost, onOpen: () -> Unit) {
    Card(onClick = onOpen, modifier = Modifier.fillMaxWidth().padding(bottom = 8.dp)) {
        Column(modifier = Modifier.padding(12.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                StatusDot(if (!h.reachable) HealthStatus.BAD else if (h.hasData) HealthStatus.OK else HealthStatus.UNKNOWN)
                Text(h.name, style = MaterialTheme.typography.titleSmall, modifier = Modifier.weight(1f))
                if (h.group.isNotBlank()) Text(h.group, style = MaterialTheme.typography.labelSmall)
            }
            if (h.k8sNode.isNotBlank()) {
                Text(
                    "k8s ${h.k8sRole}: ${h.k8sNode}",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            if (!h.hasData) {
                Text("Данных пока нет", style = MaterialTheme.typography.bodySmall)
                return@Column
            }
            Meter("CPU", h.cpuNow, "сейчас %.0f%% · среднее %.0f%% · макс %.0f%%".format(h.cpuNow, h.cpuAvg, h.cpuMax))
            val memPct = if (h.memTotal > 0) h.memUsed / h.memTotal * 100 else 0.0
            Meter("Память", memPct, "${humanBytes(h.memUsed)} из ${humanBytes(h.memTotal)}")
            h.disks.forEach { d ->
                Meter(
                    d.mount,
                    d.pct,
                    "${humanBytes(d.used)} из ${humanBytes(d.size)}" +
                        (d.etaDays?.let { " · заполнится через %.0f дн.".format(it) } ?: ""),
                )
            }
            Text(
                "load %.1f · нагрузок %d".format(h.loadAvg, h.workloads),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

@Composable
private fun Meter(label: String, pct: Double, detail: String) {
    Column(modifier = Modifier.padding(top = 6.dp)) {
        Row {
            Text(label, style = MaterialTheme.typography.labelMedium, modifier = Modifier.weight(1f))
            Text(
                "%.0f%%".format(pct),
                style = MaterialTheme.typography.labelMedium,
                color = statusColor(pctHealth(pct)),
            )
        }
        LinearProgressIndicator(
            progress = { (pct / 100).toFloat().coerceIn(0f, 1f) },
            color = statusColor(pctHealth(pct)),
            modifier = Modifier.fillMaxWidth().padding(vertical = 2.dp),
        )
        Text(detail, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}
