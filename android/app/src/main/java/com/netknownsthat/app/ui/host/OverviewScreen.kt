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
import com.netknownsthat.app.i18n.t

/** Severity order the rest of the project uses, worst first. */
private val SEVERITIES = listOf("critical", "high", "medium", "low", "info")

private val SEVERITY_LABELS get() = mapOf(
    "critical" to t("Критичные", "Critical"),
    "high" to t("Высокие", "High"),
    "medium" to t("Средние", "Medium"),
    "low" to t("Низкие", "Low"),
    "info" to t("Информационные", "Info"),
)

private val COUNT_LABELS get() = listOf(
    "endpoints" to t("Точки входа", "Entry points"),
    "endpoints_public" to t("Из них публичных", "Of them public"),
    "upstreams" to t("Апстримы", "Upstreams"),
    "containers" to t("Контейнеры", "Containers"),
    "containers_running" to t("Из них запущено", "Of them running"),
    "networks" to t("Сети", "Networks"),
    "firewall_rules" to t("Правила firewall", "Firewall rules"),
    "listeners" to t("Слушающие сокеты", "Listening sockets"),
    "config_files" to t("Файлы конфигурации", "Configuration files"),
    "certificates" to t("Сертификаты", "Certificates"),
)

@Composable
fun OverviewScreen(viewModel: OverviewViewModel) {
    SectionContent(state = viewModel.state, emptyText = t("Нет данных обзора", "No overview data")) { overview ->
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
    SectionCard(t("Хост", "Host")) {
        LabeledValue(t("Имя", "Name"), overview.host.hostname.ifBlank { "—" })
        LabeledValue(t("ОС", "OS"), overview.host.os.ifBlank { "—" })
        LabeledValue(t("Ядро", "Kernel"), overview.host.kernel.ifBlank { "—" })
        LabeledValue(t("Версия nkt", "nkt version"), overview.version.ifBlank { "—" })
        LabeledValue(t("Проверено", "Checked"), overview.scanned.ifBlank { "—" })
        if (overview.simulated) {
            Text(
                text = t("Демонстрационные данные (режим фикстур)", "Demo data (fixtures mode)"),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.tertiary,
                modifier = Modifier.padding(top = 8.dp),
            )
        }
    }
}

@Composable
private fun FindingsCard(overview: Overview) {
    SectionCard(t("Проблемы", "Problems")) {
        val present = SEVERITIES.filter { (overview.findings[it] ?: 0) > 0 }
        if (present.isEmpty()) {
            Text(
                text = t("Ничего не найдено", "Nothing found"),
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
    SectionCard(t("Сертификаты", "Certificates")) {
        LabeledValue(t("Всего", "Total"), "$total")
        if (expired > 0) {
            Text(
                text = t("Просрочено: $expired", "Expired: $expired"),
                color = MaterialTheme.colorScheme.error,
                style = MaterialTheme.typography.bodyMedium,
            )
        }
        if (expiring > 0) {
            Text(
                text = t("Истекают в ближайшие 30 дней: $expiring", "Expiring in the next 30 days: $expiring"),
                color = MaterialTheme.colorScheme.tertiary,
                style = MaterialTheme.typography.bodyMedium,
            )
        }
        if (soonestDays >= 0) {
            LabeledValue(
                t("Ближайший к истечению", "Closest to expiry"),
                t("$soonestName — через $soonestDays дн.", "$soonestName — in $soonestDays d"),
            )
        }
    }
}

@Composable
private fun AvailabilityCard(targets: Int, up: Int, down: Int, avgUptime: Double) {
    SectionCard(t("Доступность", "Availability")) {
        LabeledValue(t("Целей", "Targets"), "$targets")
        LabeledValue(t("Доступно / недоступно", "Up / down"), "$up / $down")
        LabeledValue(t("Средний аптайм за 24 ч", "Average uptime over 24 h"), "%.1f%%".format(avgUptime))
    }
}

@Composable
private fun CountsCard(overview: Overview) {
    SectionCard(t("Инвентарь", "Inventory")) {
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
