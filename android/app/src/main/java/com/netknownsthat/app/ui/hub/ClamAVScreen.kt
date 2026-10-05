package com.netknownsthat.app.ui.hub

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
import com.netknownsthat.app.net.model.ClamScan
import com.netknownsthat.app.status.HealthStatus
import com.netknownsthat.app.ui.host.SectionContent
import com.netknownsthat.app.ui.theme.StatusDot
import com.netknownsthat.app.ui.theme.statusColor

private val CLAM_OP = mapOf(
    "install" to "установка",
    "update" to "обновление баз",
    "scan" to "проверка хоста",
    "scan-images" to "проверка образов",
)

/**
 * ClamAV on a host: state of the engine and its signatures, the last host
 * and image scans with what they found, and the long operations — each a
 * host job whose log opens right away (and can be cancelled there).
 * Quarantine stays in the web UI: moving files around is not a phone task.
 */
@Composable
fun ClamAVScreen(viewModel: ClamAVViewModel, jobLog: JobLogViewModel) {
    val openJob: (Long) -> Unit = { id -> if (id > 0) jobLog.show(JobPlace.HOST, id) }
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
                            !st.installed -> "ClamAV не установлен"
                            !st.dbPresent -> "ClamAV ${st.version} — баз сигнатур нет"
                            else -> "ClamAV ${st.version} · базы ${st.dbVersion} от ${st.dbDate}"
                        },
                    )
                }
                if (st.installed) {
                    Text(
                        if (st.freshclamActive) "freshclam обновляет базы сам" else "freshclam не запущен — базы обновляются вручную",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                if (r.running) {
                    Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.padding(top = 8.dp)) {
                        StatusDot(HealthStatus.WARN, busy = true)
                        Text("Идёт ${CLAM_OP[r.op] ?: r.op}", modifier = Modifier.weight(1f))
                        OutlinedButton(onClick = { openJob(r.jobId) }) { Text("Журнал") }
                    }
                }
                val enabled = !r.running
                Row(modifier = Modifier.horizontalScroll(rememberScrollState()).padding(vertical = 8.dp)) {
                    if (!st.installed) {
                        OutlinedButton(onClick = { viewModel.install(openJob) }, enabled = enabled && r.apt) { Text("Установить") }
                    } else {
                        OutlinedButton(onClick = { viewModel.updateDb(openJob) }, enabled = enabled, modifier = Modifier.padding(end = 8.dp)) {
                            Text("Обновить базы")
                        }
                        OutlinedButton(onClick = { viewModel.scanHost(openJob) }, enabled = enabled && st.dbPresent, modifier = Modifier.padding(end = 8.dp)) {
                            Text("Проверить хост")
                        }
                        if (r.images.isNotEmpty()) {
                            OutlinedButton(onClick = { viewModel.scanImages(openJob) }, enabled = enabled && st.dbPresent) {
                                Text("Проверить образы (${r.images.size})")
                            }
                        }
                    }
                }
                if (!st.installed && !r.apt) {
                    Text("Установка отсюда — только на хостах с apt", style = MaterialTheme.typography.bodySmall)
                }
                Text(
                    "Каталоги проверки: ${r.paths.joinToString()}",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            r.hostScan?.let { item { ScanCard("Последняя проверка хоста", it) } }
            r.imageScan?.let { item { ScanCard("Последняя проверка образов", it) } }
        }
    }
}

@Composable
private fun ScanCard(title: String, scan: ClamScan) {
    Card(modifier = Modifier.fillMaxWidth().padding(top = 12.dp)) {
        Column(modifier = Modifier.padding(12.dp)) {
            Text(title, style = MaterialTheme.typography.titleSmall)
            Text(
                "${formatTs(scan.finishedAt)} · файлов ${scan.scanned} · найдено ${scan.hits.size}",
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
