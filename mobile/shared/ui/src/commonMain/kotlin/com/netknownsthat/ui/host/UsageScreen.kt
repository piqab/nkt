package com.netknownsthat.ui.host

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.material3.Card
import androidx.compose.material3.Checkbox
import androidx.compose.material3.FilterChip
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.netknownsthat.domain.model.UsageResponse
import com.netknownsthat.domain.model.UsageTopEntry
import com.netknownsthat.ui.common.humanBytes
import com.netknownsthat.ui.i18n.t
import com.netknownsthat.ui.common.fmt
import com.netknownsthat.ui.common.SectionContent
import com.netknownsthat.ui.common.humanBytes
import com.netknownsthat.domain.usecase.USAGE_MAX_SERIES

/** Sources with their labels, in the domain's order. */
private val USAGE_SOURCES: List<Pair<String, String>>
    get() = com.netknownsthat.domain.usecase.USAGE_SOURCE_ORDER.map { id ->
        id to when (id) {
            "docker" -> "Docker"
            "podman" -> "Podman"
            "lxd" -> "LXD"
            "libvirt" -> "Libvirt"
            "k8s" -> "Kubernetes"
            "k8s_node" -> t("Узлы k8s", "k8s nodes")
            else -> t("Хост", "Host")
        }
    }

/** Metrics with their labels — CPU first. */
private val USAGE_METRICS: List<Pair<String, String>>
    get() = listOf(
        "cpu_pct" to "CPU",
        "mem_bytes" to t("Память", "Memory"),
        "net_rx_bytes" to t("Сеть ↓", "Network ↓"),
        "net_tx_bytes" to t("Сеть ↑", "Network ↑"),
    )

/** Distinct series colours that read on both light and dark backgrounds. */
private val SERIES_COLORS = listOf(
    Color(0xFF3B82F6), Color(0xFFF59E0B), Color(0xFF10B981), Color(0xFFEF4444),
    Color(0xFF8B5CF6), Color(0xFF06B6D4), Color(0xFFEC4899), Color(0xFF84CC16),
)

private fun formatValue(metric: String, v: Double): String = when {
    metric == "cpu_pct" -> "${v.fmt(1)}%"
    metric.endsWith("_bytes") -> humanBytes(v)
    else -> v.fmt(0)
}

/** Average per sample for gauges (CPU, memory), the sum for counters. */
private fun UsageTopEntry.shown(metric: String): Double =
    if (metric.startsWith("net_") || samples == 0) total else total / samples

/**
 * «Нагрузка» on a phone: pick a source (container engines, k8s pods, k8s
 * nodes including workers, the host itself) and a metric — CPU first —
 * then tick up to eight subjects to compare on the chart. For k8s pods the
 * node and namespace chips narrow the list, so workers are reachable
 * rather than lumped into «Другое».
 */
@Composable
fun UsageScreen(viewModel: UsageViewModel) {
    SectionContent(state = viewModel.state, emptyText = t("Нет данных о нагрузке", "No usage data")) { data ->
        val source = viewModel.source
        val metric = viewModel.metric
        LazyColumn(contentPadding = PaddingValues(bottom = 16.dp)) {
            item {
                ChipRow {
                    USAGE_SOURCES.filter { it.first in data.sources }.forEach { (id, label) ->
                        FilterChip(
                            selected = source == id,
                            onClick = { viewModel.choose(source = id) },
                            label = { Text(label) },
                            modifier = Modifier.padding(end = 8.dp),
                        )
                    }
                }
                ChipRow {
                    USAGE_METRICS.filter { source != "host" || !it.first.startsWith("net_") }.forEach { (id, label) ->
                        FilterChip(
                            selected = metric == id,
                            onClick = { viewModel.choose(metric = id) },
                            label = { Text(label) },
                            modifier = Modifier.padding(end = 8.dp),
                        )
                    }
                }
                val cluster = data.cluster
                if (source == "k8s" && cluster != null && cluster.nodes.isNotEmpty()) {
                    ChipRow {
                        FilterChip(
                            selected = viewModel.node == null,
                            onClick = { viewModel.setNode(null) },
                            label = { Text(t("Все узлы", "All nodes")) },
                            modifier = Modifier.padding(end = 8.dp),
                        )
                        cluster.nodes.forEach { n ->
                            FilterChip(
                                selected = viewModel.node == n.name,
                                onClick = { viewModel.setNode(n.name) },
                                label = { Text(n.name + if (n.controlPlane) " (cp)" else "") },
                                modifier = Modifier.padding(end = 8.dp),
                            )
                        }
                    }
                    val namespaces = cluster.pods.keys.map { it.substringBefore('/') }.distinct().sorted()
                    ChipRow {
                        FilterChip(
                            selected = viewModel.namespace == null,
                            onClick = { viewModel.setNamespace(null) },
                            label = { Text(t("Все namespace", "All namespaces")) },
                            modifier = Modifier.padding(end = 8.dp),
                        )
                        namespaces.forEach { ns ->
                            FilterChip(
                                selected = viewModel.namespace == ns,
                                onClick = { viewModel.setNamespace(ns) },
                                label = { Text(ns) },
                                modifier = Modifier.padding(end = 8.dp),
                            )
                        }
                    }
                }
                if (data.series.points.isNotEmpty()) {
                    UsageChart(data.series, data.chartSubjects, metric)
                }
                Text(
                    text = if (viewModel.picked.isEmpty()) t("За сутки · на графике — первые 5; отметьте свои (до $USAGE_MAX_SERIES)", "Last day · the chart shows the top 5; tick your own (up to $USAGE_MAX_SERIES)")
                    else t("Отмечено ${viewModel.picked.size} из $USAGE_MAX_SERIES", "Picked ${viewModel.picked.size} of $USAGE_MAX_SERIES"),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(horizontal = 16.dp, vertical = 4.dp),
                )
                if (data.top.isEmpty()) {
                    Text(t("Данных за сутки нет", "No data for the last day"), modifier = Modifier.padding(16.dp))
                }
            }
            items(data.top, key = { it.subject }) { e ->
                val colorIndex = data.chartSubjects.indexOf(e.subject)
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    modifier = Modifier.fillMaxWidth().padding(horizontal = 8.dp),
                ) {
                    Checkbox(
                        checked = e.subject in viewModel.picked,
                        onCheckedChange = { viewModel.toggle(e.subject) },
                        enabled = e.subject in viewModel.picked || viewModel.picked.size < USAGE_MAX_SERIES,
                    )
                    if (colorIndex >= 0) {
                        Box(
                            modifier = Modifier
                                .padding(end = 6.dp)
                                .size(10.dp)
                                .background(SERIES_COLORS[colorIndex % SERIES_COLORS.size]),
                        )
                    }
                    Column(modifier = Modifier.weight(1f)) {
                        Text(e.subject, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        data.cluster?.pods?.get(e.subject)?.let {
                            Text(t("узел $it", "node $it"), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                        }
                    }
                    Text(formatValue(metric, e.shown(metric)), style = MaterialTheme.typography.bodyMedium)
                }
            }
            if (data.jobs.jobs.isNotEmpty()) {
                item {
                    Text(
                        t("Сбор метрик", "Metric collection"),
                        style = MaterialTheme.typography.titleMedium,
                        modifier = Modifier.padding(start = 16.dp, top = 16.dp, bottom = 8.dp),
                    )
                }
                items(data.jobs.jobs, key = { it.name }) { job ->
                    Card(modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 4.dp)) {
                        Column(modifier = Modifier.padding(12.dp)) {
                            Text(job.name, style = MaterialTheme.typography.titleSmall)
                            Text(
                                t("Интервал ${job.interval} · запусков ${job.runs}", "Interval ${job.interval} · runs ${job.runs}") +
                                    if (job.lastRun.isNotBlank()) t(" · последний ${job.lastRun}", " · last ${job.lastRun}") else "",
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun ChipRow(content: @Composable () -> Unit) {
    Row(
        modifier = Modifier.horizontalScroll(rememberScrollState()).padding(horizontal = 16.dp, vertical = 4.dp),
    ) { content() }
}

/** Plain line chart: one line per subject over the hourly buckets. A chart
 * library would be a heavy dependency for exactly this. */
@Composable
private fun UsageChart(series: UsageResponse, subjects: List<String>, metric: String) {
    val buckets = series.points.map { it.bucket }.distinct().sorted()
    if (buckets.size < 2) return
    val bySubject = series.points.groupBy { it.subject }
    val max = series.points.maxOf { it.value }.coerceAtLeast(1e-9)
    val axis = MaterialTheme.colorScheme.outlineVariant
    Card(modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 8.dp)) {
        Column(modifier = Modifier.padding(12.dp)) {
            Text(t("макс ${formatValue(metric, max)}", "max ${formatValue(metric, max)}"), style = MaterialTheme.typography.labelSmall)
            Canvas(modifier = Modifier.fillMaxWidth().height(160.dp).padding(vertical = 4.dp)) {
                drawLine(axis, Offset(0f, size.height), Offset(size.width, size.height))
                val step = size.width / (buckets.size - 1)
                subjects.forEachIndexed { i, subject ->
                    val values = bySubject[subject]?.associate { it.bucket to it.value } ?: return@forEachIndexed
                    val path = Path()
                    var started = false
                    buckets.forEachIndexed { x, b ->
                        val v = values[b] ?: run { started = false; return@forEachIndexed }
                        val pt = Offset(x * step, size.height - (v / max * size.height).toFloat())
                        if (started) path.lineTo(pt.x, pt.y) else path.moveTo(pt.x, pt.y)
                        started = true
                    }
                    drawPath(path, SERIES_COLORS[i % SERIES_COLORS.size], style = Stroke(width = 2.dp.toPx()))
                }
            }
            Row {
                Text(buckets.first().substringAfter('T'), style = MaterialTheme.typography.labelSmall, modifier = Modifier.weight(1f))
                Text(buckets.last().substringAfter('T'), style = MaterialTheme.typography.labelSmall)
            }
        }
    }
}
