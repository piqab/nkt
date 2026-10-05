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
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Card
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.netknownsthat.app.net.model.Deployment
import com.netknownsthat.app.net.model.Pipeline
import com.netknownsthat.app.status.HealthStatus
import com.netknownsthat.app.ui.host.SectionContent
import com.netknownsthat.app.ui.theme.StatusDot
import com.netknownsthat.app.ui.theme.statusColor

private val DEPLOY_STATUS = mapOf(
    "queued" to "в очереди",
    "running" to "идёт",
    "succeeded" to "выложено",
    "failed" to "ошибка",
)

private fun deployHealth(status: String?): HealthStatus = when (status) {
    "succeeded" -> HealthStatus.OK
    "failed" -> HealthStatus.BAD
    "queued", "running" -> HealthStatus.WARN
    else -> HealthStatus.UNKNOWN
}

private fun Deployment.summary(): String = listOfNotNull(
    "#$id",
    (tag.ifBlank { null } ?: commit.take(8).ifBlank { null } ?: ref.ifBlank { null }),
    trigger.ifBlank { null },
    author.ifBlank { null },
    formatTs(createdAt),
).joinToString(" · ")

/**
 * Deployments: what each pipeline last did, and the buttons a phone needs —
 * deploy, dry run (compose only), history with rollback, and the old stacks
 * left on a previous host. Editing the description stays in the web UI: it
 * is a YAML editor with a diff, not a phone task.
 */
@Composable
fun DeploymentsScreen(viewModel: DeploymentsViewModel, jobLog: JobLogViewModel) {
    var confirm by remember { mutableStateOf<Pipeline?>(null) }
    val openJob: (Long) -> Unit = { id -> if (id > 0) jobLog.show(JobPlace.HUB, id) }

    confirm?.let { p ->
        AlertDialog(
            onDismissRequest = { confirm = null },
            title = { Text("Выложить ${p.name}?") },
            text = { Text("Выкладка пойдёт заданием хаба; журнал откроется сразу.") },
            confirmButton = {
                TextButton(onClick = {
                    viewModel.deploy(p.id, openJob)
                    confirm = null
                }) { Text("Выложить") }
            },
            dismissButton = { TextButton(onClick = { confirm = null }) { Text("Отмена") } },
        )
    }
    viewModel.history?.let { (id, data) ->
        HistoryDialog(
            pipeline = viewModel.state.data?.pipelines?.firstOrNull { it.id == id },
            deployments = data?.deployments,
            onDismiss = viewModel::closeHistory,
            onLog = openJob,
            onRollback = { d ->
                viewModel.rollback(id, d.id, openJob)
                viewModel.closeHistory()
            },
        )
    }

    SectionContent(
        state = viewModel.state,
        emptyText = "Конвейеров нет — создаются в веб-интерфейсе",
        isEmpty = { it.pipelines.isEmpty() },
    ) { r ->
        LazyColumn(contentPadding = PaddingValues(16.dp)) {
            item {
                Text(
                    "Выкладка — экспериментальная функция",
                    style = MaterialTheme.typography.bodySmall,
                    color = statusColor(HealthStatus.WARN),
                    modifier = Modifier.padding(bottom = 8.dp),
                )
            }
            items(r.pipelines, key = { it.id }) { p ->
                PipelineCard(
                    p = p,
                    actionsEnabled = !viewModel.actionInProgress,
                    onDeploy = { confirm = p },
                    onDryRun = { viewModel.dryRun(p.id, openJob) },
                    onHistory = { viewModel.openHistory(p.id) },
                    onLog = { p.last?.jobId?.let(openJob) },
                    onLeftover = { hostId, project, forget -> viewModel.leftover(p.id, hostId, project, forget) },
                )
            }
        }
    }
}

@Composable
private fun PipelineCard(
    p: Pipeline,
    actionsEnabled: Boolean,
    onDeploy: () -> Unit,
    onDryRun: () -> Unit,
    onHistory: () -> Unit,
    onLog: () -> Unit,
    onLeftover: (Long, String, Boolean) -> Unit,
) {
    Card(modifier = Modifier.fillMaxWidth().padding(bottom = 8.dp)) {
        Column(modifier = Modifier.padding(12.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                StatusDot(deployHealth(p.last?.status), busy = p.last?.status == "running")
                Text(p.name, style = MaterialTheme.typography.titleSmall, modifier = Modifier.weight(1f))
                Text(
                    text = listOfNotNull(p.action.ifBlank { null }, if (p.enabled) null else "выключен").joinToString(" · "),
                    style = MaterialTheme.typography.labelSmall,
                )
            }
            val last = p.last
            if (last == null) {
                Text("Ещё не выкладывался", style = MaterialTheme.typography.bodySmall)
            } else {
                Text(
                    "${DEPLOY_STATUS[last.status] ?: last.status} · ${last.summary()}",
                    style = MaterialTheme.typography.bodySmall,
                    color = statusColor(deployHealth(last.status)),
                )
                if (last.error.isNotBlank()) {
                    Text(last.error, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
                }
            }
            p.leftovers.forEach { l ->
                Text(
                    "Старый стек ${l.project} на ${l.host}" + (if (l.reason.isNotBlank()) " — ${l.reason}" else ""),
                    style = MaterialTheme.typography.bodySmall,
                    color = statusColor(HealthStatus.WARN),
                    modifier = Modifier.padding(top = 4.dp),
                )
                Row {
                    TextButton(onClick = { onLeftover(l.hostId, l.project, false) }, enabled = actionsEnabled) { Text("Убрать") }
                    TextButton(onClick = { onLeftover(l.hostId, l.project, true) }, enabled = actionsEnabled) { Text("Забыть") }
                }
            }
            Row(modifier = Modifier.horizontalScroll(rememberScrollState()).padding(top = 8.dp)) {
                OutlinedButton(onClick = onDeploy, enabled = p.enabled, modifier = Modifier.padding(end = 8.dp)) {
                    Text("Выложить")
                }
                if (p.action == "compose") {
                    OutlinedButton(onClick = onDryRun, modifier = Modifier.padding(end = 8.dp)) { Text("Сухой прогон") }
                }
                if (last != null && last.jobId > 0) {
                    OutlinedButton(onClick = onLog, modifier = Modifier.padding(end = 8.dp)) { Text("Журнал") }
                }
                OutlinedButton(onClick = onHistory) { Text("История") }
            }
        }
    }
}

@Composable
private fun HistoryDialog(
    pipeline: Pipeline?,
    deployments: List<Deployment>?,
    onDismiss: () -> Unit,
    onLog: (Long) -> Unit,
    onRollback: (Deployment) -> Unit,
) {
    var rollback by remember { mutableStateOf<Deployment?>(null) }
    rollback?.let { d ->
        AlertDialog(
            onDismissRequest = { rollback = null },
            text = { Text("Выложить заново ${d.summary()}?") },
            confirmButton = {
                TextButton(onClick = {
                    onRollback(d)
                    rollback = null
                }) { Text("Откатить") }
            },
            dismissButton = { TextButton(onClick = { rollback = null }) { Text("Отмена") } },
        )
    }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("История ${pipeline?.name ?: ""}") },
        text = {
            when {
                deployments == null -> CircularProgressIndicator()
                deployments.isEmpty() -> Text("Выкладок ещё не было")
                else -> LazyColumn {
                    items(deployments, key = { it.id }) { d ->
                        Column(modifier = Modifier.padding(bottom = 8.dp)) {
                            Text(
                                "${DEPLOY_STATUS[d.status] ?: d.status} · ${d.summary()}",
                                style = MaterialTheme.typography.bodySmall,
                                color = statusColor(deployHealth(d.status)),
                            )
                            if (d.error.isNotBlank()) {
                                Text(d.error, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
                            }
                            Row {
                                if (d.jobId > 0) TextButton(onClick = { onLog(d.jobId) }) { Text("Журнал") }
                                if (d.status == "succeeded" && d.commit.isNotBlank()) {
                                    TextButton(onClick = { rollback = d }) { Text("Откатить сюда") }
                                }
                            }
                        }
                    }
                }
            }
        },
        confirmButton = { TextButton(onClick = onDismiss) { Text("Закрыть") } },
    )
}
