package com.netknownsthat.app.ui.hub

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import com.netknownsthat.app.status.HealthStatus
import com.netknownsthat.app.ui.theme.StatusDot
import com.netknownsthat.app.ui.theme.statusColor
import java.time.OffsetDateTime
import java.time.ZoneId
import java.time.format.DateTimeFormatter

private val TS_FORMAT = DateTimeFormatter.ofPattern("dd.MM HH:mm:ss")

/** Server timestamps are RFC 3339; shown in the phone's own zone. */
fun formatTs(raw: String): String =
    runCatching { OffsetDateTime.parse(raw).atZoneSameInstant(ZoneId.systemDefault()).format(TS_FORMAT) }
        .getOrDefault(raw)

val JOB_STATUS = mapOf(
    "queued" to "в очереди",
    "running" to "выполняется",
    "succeeded" to "выполнено",
    "failed" to "ошибка",
    "canceled" to "отменено",
    "interrupted" to "прервано",
)

fun jobHealth(status: String): HealthStatus = when (status) {
    "succeeded" -> HealthStatus.OK
    "failed", "interrupted" -> HealthStatus.BAD
    "queued", "running" -> HealthStatus.WARN
    else -> HealthStatus.UNKNOWN
}

fun humanBytes(bytes: Double): String {
    val units = listOf("Б", "КБ", "МБ", "ГБ", "ТБ")
    var v = bytes
    var i = 0
    while (v >= 1024 && i < units.lastIndex) {
        v /= 1024
        i++
    }
    return if (i == 0) "${v.toLong()} ${units[i]}" else "%.1f %s".format(v, units[i])
}

/**
 * Full-screen live log of one job — the phone's JobLogModal: status, step,
 * the log tail following new lines, and «Отменить» while the job runs.
 */
@Composable
fun JobLogDialog(viewModel: JobLogViewModel) {
    if (!viewModel.open) return
    val job = viewModel.job
    val listState = rememberLazyListState()
    LaunchedEffect(viewModel.lines.size) {
        if (viewModel.lines.isNotEmpty()) listState.scrollToItem(viewModel.lines.lastIndex)
    }
    Dialog(
        onDismissRequest = viewModel::close,
        properties = DialogProperties(usePlatformDefaultWidth = false),
    ) {
        Surface(modifier = Modifier.fillMaxSize()) {
            Column(modifier = Modifier.fillMaxSize().padding(16.dp)) {
                Text(
                    text = job?.title?.ifBlank { null } ?: "Задание",
                    style = MaterialTheme.typography.titleMedium,
                )
                if (job != null) {
                    Row(modifier = Modifier.padding(top = 4.dp)) {
                        StatusDot(jobHealth(job.status), busy = !job.finished)
                        Text(
                            text = buildString {
                                append(JOB_STATUS[job.status] ?: job.status)
                                if (job.steps > 0) append(" · шаг ${job.step}/${job.steps}")
                                if (job.stepName.isNotBlank()) append(" · ${job.stepName}")
                            },
                            style = MaterialTheme.typography.bodySmall,
                            color = statusColor(jobHealth(job.status)),
                        )
                    }
                    if (!job.finished && job.steps > 0) {
                        LinearProgressIndicator(
                            progress = { job.step.toFloat() / job.steps },
                            modifier = Modifier.fillMaxWidth().padding(top = 8.dp),
                        )
                    }
                    if (job.error.isNotBlank()) {
                        Text(
                            text = job.error,
                            color = MaterialTheme.colorScheme.error,
                            style = MaterialTheme.typography.bodySmall,
                            modifier = Modifier.padding(top = 6.dp),
                        )
                    }
                }
                viewModel.error?.let {
                    Text("Нет связи: $it", color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
                }
                LazyColumn(
                    state = listState,
                    modifier = Modifier.weight(1f).fillMaxWidth().padding(top = 8.dp),
                ) {
                    items(viewModel.lines, key = { it.seq }) { line ->
                        Text(
                            text = line.text,
                            fontFamily = FontFamily.Monospace,
                            fontSize = 11.sp,
                            lineHeight = 14.sp,
                        )
                    }
                }
                Row(
                    modifier = Modifier.fillMaxWidth().padding(top = 8.dp),
                    horizontalArrangement = Arrangement.End,
                ) {
                    if (job != null && !job.finished) {
                        OutlinedButton(onClick = viewModel::cancel, enabled = !viewModel.canceling) {
                            Text("Отменить задание")
                        }
                    }
                    TextButton(onClick = viewModel::close) { Text("Закрыть") }
                }
            }
        }
    }
}
