package com.netknownsthat.ui.hub

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
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.netknownsthat.ui.i18n.t
import com.netknownsthat.ui.status.HealthStatus
import com.netknownsthat.ui.theme.StatusDot
import com.netknownsthat.ui.theme.statusColor

val JOB_STATUS: Map<String, String>
    get() = mapOf(
        "queued" to t("в очереди", "queued"),
        "running" to t("выполняется", "running"),
        "succeeded" to t("выполнено", "succeeded"),
        "failed" to t("ошибка", "failed"),
        "canceled" to t("отменено", "cancelled"),
        "interrupted" to t("прервано", "interrupted"),
    )

fun jobHealth(status: String): HealthStatus = when (status) {
    "succeeded" -> HealthStatus.OK
    "failed", "interrupted" -> HealthStatus.BAD
    "queued", "running" -> HealthStatus.WARN
    else -> HealthStatus.UNKNOWN
}

/**
 * A job's page — the phone's JobLogModal: status, step, the log tail
 * following new lines, and «Отменить задание» while it runs. Any screen that
 * starts a job navigates here, so Back returns to where it was started.
 */
@Composable
fun JobScreen(viewModel: JobLogViewModel) {
    LaunchedEffect(viewModel) { viewModel.start() }
    val job = viewModel.job
    val listState = rememberLazyListState()
    LaunchedEffect(viewModel.lines.size) {
        if (viewModel.lines.isNotEmpty()) listState.scrollToItem(viewModel.lines.lastIndex)
    }
    Column(modifier = Modifier.fillMaxSize().padding(16.dp)) {
        Text(job?.title?.ifBlank { null } ?: t("Задание", "Job"), style = MaterialTheme.typography.titleMedium)
        if (job != null) {
            Row(modifier = Modifier.padding(top = 4.dp)) {
                StatusDot(jobHealth(job.status), busy = !job.finished)
                Text(
                    buildString {
                        append(JOB_STATUS[job.status] ?: job.status)
                        if (job.steps > 0) append(t(" · шаг ${job.step}/${job.steps}", " · step ${job.step}/${job.steps}"))
                        if (job.stepName.isNotBlank()) append(" · ${job.stepName}")
                    },
                    style = MaterialTheme.typography.bodySmall,
                    color = statusColor(jobHealth(job.status)),
                )
            }
            if (!job.finished && job.steps > 0) {
                LinearProgressIndicator(progress = { job.step.toFloat() / job.steps }, modifier = Modifier.fillMaxWidth().padding(top = 8.dp))
            }
            if (job.error.isNotBlank()) {
                Text(job.error, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall, modifier = Modifier.padding(top = 6.dp))
            }
        }
        viewModel.error?.let {
            Text(t("Нет связи: $it", "No connection: $it"), color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
        }
        LazyColumn(state = listState, modifier = Modifier.weight(1f).fillMaxWidth().padding(top = 8.dp)) {
            items(viewModel.lines, key = { it.seq }) { line ->
                Text(line.text, fontFamily = FontFamily.Monospace, fontSize = 11.sp, lineHeight = 14.sp)
            }
        }
        if (job != null && !job.finished) {
            Row(modifier = Modifier.fillMaxWidth().padding(top = 8.dp), horizontalArrangement = Arrangement.End) {
                OutlinedButton(onClick = viewModel::cancel, enabled = !viewModel.canceling) { Text(t("Отменить задание", "Cancel job")) }
            }
        }
    }
}
