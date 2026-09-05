package com.netknownsthat.app.ui.host

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.material3.Card
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import com.netknownsthat.app.net.model.Overview

/** Severity order the rest of the project uses, worst first. */
private val SEVERITIES = listOf("critical", "high", "medium", "low", "info")

private val SEVERITY_LABELS = mapOf(
    "critical" to "Критичные",
    "high" to "Высокие",
    "medium" to "Средние",
    "low" to "Низкие",
    "info" to "Информационные",
)

private val COUNT_LABELS = listOf(
    "endpoints" to "Точки входа",
    "endpoints_public" to "Из них публичных",
    "upstreams" to "Апстримы",
    "containers" to "Контейнеры",
    "containers_running" to "Из них запущено",
    "networks" to "Сети",
    "firewall_rules" to "Правила firewall",
    "listeners" to "Слушающие сокеты",
    "config_files" to "Файлы конфигурации",
    "certificates" to "Сертификаты",
)

@Composable
fun OverviewScreen(viewModel: OverviewViewModel) {
    SectionContent(state = viewModel.state, emptyText = "Нет данных обзора") { overview ->
        LazyColumn(contentPadding = PaddingValues(16.dp)) {
            item { HostCard(overview) }
            item { FindingsCard(overview) }
            overview.certificates?.let { certs ->
                if (certs.total > 0) item { CertificatesCard(certs.total, certs.expired, certs.expiring, certs.soonestDays, certs.soonestName) }
            }
            overview.availability?.let { availability ->
                if (availability.targets > 0) {
                    item {
                        AvailabilityCard(
                            availability.targets,
                            availability.up,
                            availability.down,
                            availability.avgUptime,
                        )
                    }
                }
            }
            item { CountsCard(overview) }
        }
    }
}

@Composable
private fun SectionCard(title: String, content: @Composable () -> Unit) {
    Card(modifier = Modifier.fillMaxWidth().padding(bottom = 16.dp)) {
        Column(modifier = Modifier.padding(16.dp)) {
            Text(title, style = MaterialTheme.typography.titleMedium)
            content()
        }
    }
}

@Composable
private fun HostCard(overview: Overview) {
    SectionCard("Хост") {
        LabeledValue("Имя", overview.host.hostname.ifBlank { "—" })
        LabeledValue("ОС", overview.host.os.ifBlank { "—" })
        LabeledValue("Ядро", overview.host.kernel.ifBlank { "—" })
        LabeledValue("Версия nkt", overview.version.ifBlank { "—" })
        LabeledValue("Проверено", overview.scanned.ifBlank { "—" })
        if (overview.simulated) {
            Text(
                text = "Демонстрационные данные (режим фикстур)",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.tertiary,
                modifier = Modifier.padding(top = 8.dp),
            )
        }
    }
}

@Composable
private fun FindingsCard(overview: Overview) {
    SectionCard("Проблемы") {
        val present = SEVERITIES.filter { (overview.findings[it] ?: 0) > 0 }
        if (present.isEmpty()) {
            Text(
                text = "Ничего не найдено",
                style = MaterialTheme.typography.bodyMedium,
                modifier = Modifier.padding(top = 8.dp),
            )
        } else {
            Row(modifier = Modifier.fillMaxWidth().padding(top = 8.dp)) {
                present.forEach { severity ->
                    Column(modifier = Modifier.weight(1f)) {
                        Text(
                            text = "${overview.findings[severity] ?: 0}",
                            style = MaterialTheme.typography.headlineSmall,
                            color = severityColor(severity),
                            textAlign = TextAlign.Center,
                            modifier = Modifier.fillMaxWidth(),
                        )
                        Text(
                            text = SEVERITY_LABELS[severity] ?: severity,
                            style = MaterialTheme.typography.labelSmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            textAlign = TextAlign.Center,
                            modifier = Modifier.fillMaxWidth(),
                        )
                    }
                }
            }
        }
    }
}

@Composable
private fun CertificatesCard(
    total: Int,
    expired: Int,
    expiring: Int,
    soonestDays: Int,
    soonestName: String,
) {
    SectionCard("Сертификаты") {
        LabeledValue("Всего", "$total")
        if (expired > 0) {
            Text(
                text = "Просрочено: $expired",
                color = MaterialTheme.colorScheme.error,
                style = MaterialTheme.typography.bodyMedium,
            )
        }
        if (expiring > 0) {
            Text(
                text = "Истекают в ближайшие 30 дней: $expiring",
                color = MaterialTheme.colorScheme.tertiary,
                style = MaterialTheme.typography.bodyMedium,
            )
        }
        if (soonestDays >= 0) {
            LabeledValue(
                "Ближайший к истечению",
                "$soonestName — через $soonestDays дн.",
            )
        }
    }
}

@Composable
private fun AvailabilityCard(targets: Int, up: Int, down: Int, avgUptime: Double) {
    SectionCard("Доступность") {
        LabeledValue("Целей", "$targets")
        LabeledValue("Доступно / недоступно", "$up / $down")
        LabeledValue("Средний аптайм за 24 ч", "%.1f%%".format(avgUptime))
    }
}

@Composable
private fun CountsCard(overview: Overview) {
    SectionCard("Инвентарь") {
        COUNT_LABELS.forEach { (key, label) ->
            overview.counts[key]?.let { LabeledValue(label, "$it") }
        }
    }
}

@Composable
private fun severityColor(severity: String) = when (severity) {
    "critical", "high" -> MaterialTheme.colorScheme.error
    "medium" -> MaterialTheme.colorScheme.tertiary
    else -> MaterialTheme.colorScheme.onSurface
}
