package com.netknownsthat.ui.charts

import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Card
import androidx.compose.material3.FilterChip
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.netknownsthat.domain.common.HostTarget
import com.netknownsthat.domain.common.Outcome
import com.netknownsthat.domain.model.AvailabilityBucket
import com.netknownsthat.domain.model.MonSeries
import com.netknownsthat.domain.repository.HostMonitoringRepository
import com.netknownsthat.domain.repository.MonitoringRepository
import com.netknownsthat.ui.common.ChartPoint
import com.netknownsthat.ui.common.ChartSeries
import com.netknownsthat.ui.common.LineChart
import com.netknownsthat.ui.common.SectionContent
import com.netknownsthat.ui.common.SectionViewModel
import com.netknownsthat.ui.common.fmt
import com.netknownsthat.ui.common.humanBytes
import com.netknownsthat.ui.i18n.t
import kotlinx.datetime.TimeZone
import kotlinx.datetime.offsetAt

/*
 * History charts — the same as the web's: a target's availability and
 * latency, a host's CPU, memory, load and disks. Every chart reads out its
 * values under a finger (LineChart).
 */

/** The device's UTC offset in minutes: the hub buckets by local hours. */
private fun tzMinutes(): Int =
    TimeZone.currentSystemDefault().offsetAt(kotlin.time.Clock.System.now()).totalSeconds / 60

/** "2026-10-07T13…" or "2026-10-07 13" → "10-07 13:00"; a day,
 * "2026-10-07" → "10-07" (the web's format). */
private fun shortTime(x: String): String =
    if (x.length > 10) "${x.substring(5, 10)} ${x.substring(11, minOf(13, x.length))}:00" else x.drop(5)

@Composable
private fun RangeChips(ranges: List<String>, current: String, onPick: (String) -> Unit) {
    Row(modifier = Modifier.horizontalScroll(rememberScrollState()).padding(bottom = 8.dp)) {
        ranges.forEach { r ->
            FilterChip(selected = r == current, onClick = { onPick(r) }, label = { Text(rangeLabel(r)) }, modifier = Modifier.padding(end = 6.dp))
        }
    }
}

private fun rangeLabel(r: String) = when (r) {
    "24h" -> t("сутки", "day")
    "7d" -> t("неделя", "week")
    "30d" -> t("месяц", "month")
    "90d" -> t("3 месяца", "3 months")
    "365d" -> t("год", "year")
    else -> r
}

@Composable
private fun ChartCard(title: String, content: @Composable () -> Unit) {
    Card(modifier = Modifier.fillMaxWidth().padding(bottom = 12.dp)) {
        Column(modifier = Modifier.padding(12.dp)) {
            Text(title, style = MaterialTheme.typography.titleSmall, modifier = Modifier.padding(bottom = 4.dp))
            content()
        }
    }
}

// ---- availability target ---------------------------------------------------

class TargetHistoryViewModel(
    private val host: HostTarget,
    private val targetId: Long,
    private val repo: HostMonitoringRepository,
) : SectionViewModel<List<AvailabilityBucket>>() {
    var range by mutableStateOf("7d")
        private set

    override suspend fun fetch(): Outcome<List<AvailabilityBucket>> = repo.targetHistory(host, targetId, range, tzMinutes())

    fun pick(r: String) {
        range = r
        load()
    }
}

@Composable
fun TargetHistoryScreen(viewModel: TargetHistoryViewModel) {
    SectionContent(state = viewModel.state) { buckets ->
        Column(modifier = Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(16.dp)) {
            RangeChips(listOf("24h", "7d", "30d"), viewModel.range, viewModel::pick)
            ChartCard(t("Доступность, %", "Availability, %")) {
                LineChart(
                    series = listOf(ChartSeries(t("доступность", "availability"), buckets.map { ChartPoint(it.bucket, it.uptime) })),
                    formatValue = { "${it.fmt(0)}%" },
                    formatX = ::shortTime,
                    yMax = 100.0,
                    area = true,
                )
            }
            ChartCard(t("Задержка", "Latency")) {
                LineChart(
                    series = listOf(
                        ChartSeries(t("средняя", "average"), buckets.map { ChartPoint(it.bucket, it.avgLatencyMs) }),
                        ChartSeries(t("максимальная", "maximum"), buckets.map { ChartPoint(it.bucket, it.maxLatencyMs) }),
                    ),
                    formatValue = { t("${it.fmt(0)} мс", "${it.fmt(0)} ms") },
                    formatX = ::shortTime,
                )
            }
        }
    }
}

// ---- hub monitoring: one host ----------------------------------------------

/** A host's series: CPU, memory and load (source host), and disks. */
data class MonHostCharts(val host: List<MonSeries>, val disk: List<MonSeries>)

class MonHostChartsViewModel(
    private val hostId: Long,
    private val repo: MonitoringRepository,
) : SectionViewModel<MonHostCharts>() {
    var range by mutableStateOf("7d")
        private set

    override suspend fun fetch(): Outcome<MonHostCharts> {
        val host = repo.series(hostId, "host", range)
        if (host is Outcome.Failure) return host
        // Disks are a nicety: without them the CPU and memory still show.
        val disk = (repo.series(hostId, "disk", range) as? Outcome.Success)?.value.orEmpty()
        return Outcome.Success(MonHostCharts((host as Outcome.Success).value, disk))
    }

    fun pick(r: String) {
        range = r
        load()
    }
}

@Composable
fun MonHostChartsScreen(viewModel: MonHostChartsViewModel, memTotal: Double, onUsage: () -> Unit) {
    SectionContent(state = viewModel.state) { data ->
        fun metric(m: String) = data.host.firstOrNull { it.metric == m }?.points.orEmpty()
        fun avgPeak(m: String) = listOf(
            ChartSeries(t("среднее", "average"), metric(m).map { ChartPoint(it.at, it.avg) }),
            ChartSeries(t("пик", "peak"), metric(m).map { ChartPoint(it.at, it.max) }),
        )
        Column(modifier = Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(16.dp)) {
            RangeChips(listOf("24h", "7d", "30d", "90d", "365d"), viewModel.range, viewModel::pick)
            ChartCard("CPU") {
                LineChart(avgPeak("cpu_pct"), formatValue = { "${it.fmt(1)}%" }, formatX = ::shortTime)
            }
            ChartCard(t("Память", "Memory")) {
                LineChart(
                    avgPeak("mem_used_bytes"),
                    formatValue = ::humanBytes,
                    formatX = ::shortTime,
                    reference = memTotal.takeIf { it > 0 },
                )
            }
            ChartCard(t("Нагрузка (load 1 мин)", "Load (1 min)")) {
                LineChart(avgPeak("load1"), formatValue = { it.fmt(2) }, formatX = ::shortTime)
            }
            // Disks of different sizes as a fill percentage: on one byte
            // scale the small ones would lie flat on zero.
            val disks = data.disk.filter { it.metric == "used_bytes" }.map { used ->
                val size = data.disk.firstOrNull { it.subject == used.subject && it.metric == "size_bytes" }
                    ?.points.orEmpty().associate { it.at to it.max }
                ChartSeries(used.subject, used.points.mapNotNull { p -> size[p.at]?.takeIf { it > 0 }?.let { ChartPoint(p.at, p.max / it * 100) } })
            }
            if (disks.isNotEmpty()) {
                ChartCard(t("Диски, % заполнения", "Disks, % full")) {
                    LineChart(disks, formatValue = { "${it.fmt(1)}%" }, formatX = ::shortTime, yMax = 100.0)
                }
            }
            OutlinedButton(onClick = onUsage) { Text(t("Нагрузка хоста по контейнерам →", "Host usage by container →")) }
        }
    }
}
