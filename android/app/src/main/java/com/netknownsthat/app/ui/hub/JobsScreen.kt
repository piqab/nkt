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
import androidx.compose.material3.FilterChip
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.netknownsthat.app.net.model.JobRecord
import com.netknownsthat.app.ui.host.SectionContent
import com.netknownsthat.app.ui.theme.StatusDot
import com.netknownsthat.app.ui.theme.statusColor
import com.netknownsthat.app.i18n.t

private val STATUS_FILTERS get() = listOf(
    "" to t("Все", "All"),
    "queued,running" to t("Идут", "Running"),
    "failed,interrupted" to t("С ошибкой", "Failed"),
    "succeeded" to t("Выполнены", "Succeeded"),
)

/**
 * Background jobs of a host or of the hub — installs, scans, deployments,
 * fleet bans. Tapping one opens its live log; a running one can be
 * cancelled right from the list.
 */
@Composable
fun JobsScreen(viewModel: JobsViewModel, jobLog: JobLogViewModel) {
    Column {
        Row(
            modifier = Modifier
                .horizontalScroll(rememberScrollState())
                .padding(horizontal = 16.dp, vertical = 8.dp),
        ) {
            STATUS_FILTERS.forEach { (value, label) ->
                FilterChip(
                    selected = viewModel.statusFilter == value,
                    onClick = {
                        viewModel.statusFilter = value
                        viewModel.load()
                    },
                    label = { Text(label) },
                    modifier = Modifier.padding(end = 8.dp),
                )
            }
        }
        SectionContent(
            state = viewModel.state,
            emptyText = t("Заданий нет", "No jobs"),
            isEmpty = { it.jobs.isEmpty() },
        ) { response ->
            LazyColumn(contentPadding = PaddingValues(start = 16.dp, end = 16.dp, bottom = 16.dp)) {
                if (response.active > 0) {
                    item {
                        Text(
                            text = t("Выполняется: ${response.active}", "Running: ${response.active}"),
                            style = MaterialTheme.typography.labelMedium,
                            modifier = Modifier.padding(bottom = 8.dp),
                        )
                    }
                }
                items(response.jobs, key = { it.id }) { job ->
                    JobCard(
                        job = job,
                        onOpen = { jobLog.show(viewModel.place, job.id) },
                        onCancel = { viewModel.cancel(job.id) },
                        actionsEnabled = !viewModel.actionInProgress,
                    )
                }
            }
        }
    }
}

@Composable
private fun JobCard(job: JobRecord, onOpen: () -> Unit, onCancel: () -> Unit, actionsEnabled: Boolean) {
    Card(onClick = onOpen, modifier = Modifier.fillMaxWidth().padding(bottom = 8.dp)) {
        Column(modifier = Modifier.padding(12.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                StatusDot(jobHealth(job.status), busy = job.status == "running")
                Text(
                    text = job.title.ifBlank { job.kind },
                    style = MaterialTheme.typography.titleSmall,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f),
                )
                Text(
                    text = JOB_STATUS[job.status] ?: job.status,
                    style = MaterialTheme.typography.labelMedium,
                    color = statusColor(jobHealth(job.status)),
                )
            }
            Text(
                text = listOfNotNull(
                    "#${job.id}",
                    job.kind,
                    job.author.ifBlank { null },
                    formatTs(job.createdAt),
                    if (job.steps > 0 && !job.finished) t("шаг ${job.step}/${job.steps}", "step ${job.step}/${job.steps}") else null,
                ).joinToString(" · "),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(top = 4.dp),
            )
            if (job.error.isNotBlank()) {
                Text(
                    text = job.error,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                    maxLines = 3,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.padding(top = 4.dp),
                )
            }
            if (!job.finished) {
                TextButton(onClick = onCancel, enabled = actionsEnabled) { Text(t("Отменить", "Cancel")) }
            }
        }
    }
}
