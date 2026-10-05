package com.netknownsthat.ui.hub

import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.material3.Card
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import com.netknownsthat.domain.model.ClamScan
import com.netknownsthat.ui.status.HealthStatus
import com.netknownsthat.ui.common.SectionContent
import com.netknownsthat.ui.theme.StatusDot
import com.netknownsthat.ui.theme.statusColor
import com.netknownsthat.ui.i18n.t
import com.netknownsthat.ui.common.formatTs

private val CLAM_OP get() = mapOf(
    "install" to t("установка", "install"),
    "update" to t("обновление баз", "signature update"),
    "scan" to t("проверка хоста", "host scan"),
    "scan-images" to t("проверка образов", "image scan"),
)

/**
 * ClamAV on a host: state of the engine and its signatures, the last host
 * and image scans with what they found, and the long operations — each a
 * host job whose log opens right away (and can be cancelled there).
 * Quarantine stays in the web UI: moving files around is not a phone task.
 */
@Composable
fun ClamAVScreen(viewModel: ClamAVViewModel, onOpenJob: (Long) -> Unit) {
    val openJob: (Long) -> Unit = { id -> if (id > 0) onOpenJob(id) }
    SectionContent(state = viewModel.state) { r ->
        val st = r.status
        LazyColumn(contentPadding = PaddingValues(16.dp)) {
            item {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    StatusDot(
                        when {
                            !st.installed -> HealthStatus.UNKNOWN
                            !st.dbPresent -> HealthStatus.WARN
                            else -> HealthStatus.OK
                        },
                    )
                    Text(
                        when {
                            !st.installed -> t("ClamAV не установлен", "ClamAV is not installed")
                            !st.dbPresent -> t("ClamAV ${st.version} — баз сигнатур нет", "ClamAV ${st.version} — no signature databases")
                            else -> t("ClamAV ${st.version} · базы ${st.dbVersion} от ${st.dbDate}", "ClamAV ${st.version} · databases ${st.dbVersion} of ${st.dbDate}")
                        },
                    )
                }
                if (st.installed) {
                    Text(
                        if (st.freshclamActive) t("freshclam обновляет базы сам", "freshclam updates the databases itself") else t("freshclam не запущен — базы обновляются вручную", "freshclam is not running — databases are updated by hand"),
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                if (r.running) {
                    Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.padding(top = 8.dp)) {
                        StatusDot(HealthStatus.WARN, busy = true)
                        Text(t("Идёт ${CLAM_OP[r.op] ?: r.op}", "Running: ${CLAM_OP[r.op] ?: r.op}"), modifier = Modifier.weight(1f))
                        OutlinedButton(onClick = { openJob(r.jobId) }) { Text(t("Журнал", "Log")) }
                    }
                }
                val enabled = !r.running
                Row(modifier = Modifier.horizontalScroll(rememberScrollState()).padding(vertical = 8.dp)) {
                    if (!st.installed) {
                        OutlinedButton(onClick = { viewModel.install(openJob) }, enabled = enabled && r.apt) { Text(t("Установить", "Install")) }
                    } else {
                        OutlinedButton(onClick = { viewModel.updateDb(openJob) }, enabled = enabled, modifier = Modifier.padding(end = 8.dp)) {
                            Text(t("Обновить базы", "Update databases"))
                        }
                        OutlinedButton(onClick = { viewModel.scanHost(openJob) }, enabled = enabled && st.dbPresent, modifier = Modifier.padding(end = 8.dp)) {
                            Text(t("Проверить хост", "Scan host"))
                        }
                        if (r.images.isNotEmpty()) {
                            OutlinedButton(onClick = { viewModel.scanImages(openJob) }, enabled = enabled && st.dbPresent) {
                                Text(t("Проверить образы (${r.images.size})", "Scan images (${r.images.size})"))
                            }
                        }
                    }
                }
                if (!st.installed && !r.apt) {
                    Text(t("Установка отсюда — только на хостах с apt", "Installing from here works only on hosts with apt"), style = MaterialTheme.typography.bodySmall)
                }
                Text(
                    t("Каталоги проверки: ${r.paths.joinToString()}", "Scanned directories: ${r.paths.joinToString()}"),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            r.hostScan?.let { item { ScanCard(t("Последняя проверка хоста", "Last host scan"), it) } }
            r.imageScan?.let { item { ScanCard(t("Последняя проверка образов", "Last image scan"), it) } }
        }
    }
}

@Composable
private fun ScanCard(title: String, scan: ClamScan) {
    Card(modifier = Modifier.fillMaxWidth().padding(top = 12.dp)) {
        Column(modifier = Modifier.padding(12.dp)) {
            Text(title, style = MaterialTheme.typography.titleSmall)
            Text(
                t("${formatTs(scan.finishedAt)} · файлов ${scan.scanned} · найдено ${scan.hits.size}", "${formatTs(scan.finishedAt)} · files ${scan.scanned} · found ${scan.hits.size}"),
                style = MaterialTheme.typography.bodySmall,
                color = statusColor(if (scan.hits.isEmpty()) HealthStatus.OK else HealthStatus.BAD),
            )
            if (scan.error.isNotBlank()) Text(scan.error, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
            scan.hits.take(50).forEach { h ->
                Text(
                    (if (h.target.isNotBlank()) "${h.target}: " else "") + h.path,
                    fontFamily = FontFamily.Monospace,
                    style = MaterialTheme.typography.bodySmall,
                    modifier = Modifier.padding(top = 4.dp),
                )
                Text(h.signature, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
            }
        }
    }
}
