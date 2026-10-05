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
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Card
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.netknownsthat.domain.model.Deployment
import com.netknownsthat.domain.model.Pipeline
import com.netknownsthat.ui.status.HealthStatus
import com.netknownsthat.ui.common.SectionContent
import com.netknownsthat.ui.theme.StatusDot
import com.netknownsthat.ui.theme.statusColor
import com.netknownsthat.ui.i18n.t
import com.netknownsthat.ui.common.formatTs

private val DEPLOY_STATUS get() = mapOf(
    "queued" to t("в очереди", "queued"),
    "running" to t("идёт", "running"),
    "succeeded" to t("выложено", "deployed"),
    "failed" to t("ошибка", "failed"),
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
fun DeploymentsScreen(viewModel: DeploymentsViewModel, onOpenJob: (Long) -> Unit, onOpenPipeline: (Pipeline) -> Unit) {
    var confirm by remember { mutableStateOf<Pipeline?>(null) }
    val openJob: (Long) -> Unit = { id -> if (id > 0) onOpenJob(id) }

    confirm?.let { p ->
        AlertDialog(
            onDismissRequest = { confirm = null },
            title = { Text(t("Выложить ${p.name}?", "Deploy ${p.name}?")) },
            text = { Text(t("Выкладка пойдёт заданием хаба; журнал откроется сразу.", "The deployment runs as a hub job; its log opens right away.")) },
            confirmButton = {
                TextButton(onClick = {
                    viewModel.deploy(p.id, openJob)
                    confirm = null
                }) { Text(t("Выложить", "Deploy")) }
            },
            dismissButton = { TextButton(onClick = { confirm = null }) { Text(t("Отмена", "Cancel")) } },
        )
    }
    SectionContent(
        state = viewModel.state,
        emptyText = t("Конвейеров нет — создаются в веб-интерфейсе", "No pipelines — they are created in the web UI"),
        isEmpty = { it.pipelines.isEmpty() },
    ) { r ->
        LazyColumn(contentPadding = PaddingValues(16.dp)) {
            item {
                Text(
                    t("Выкладка — экспериментальная функция", "Deployments are an experimental feature"),
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
                    onHistory = { onOpenPipeline(p) },
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
                    text = listOfNotNull(p.action.ifBlank { null }, if (p.enabled) null else t("выключен", "disabled")).joinToString(" · "),
                    style = MaterialTheme.typography.labelSmall,
                )
            }
            val last = p.last
            if (last == null) {
                Text(t("Ещё не выкладывался", "Not deployed yet"), style = MaterialTheme.typography.bodySmall)
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
                    t("Старый стек ${l.project} на ${l.host}", "Old stack ${l.project} on ${l.host}") + (if (l.reason.isNotBlank()) " — ${l.reason}" else ""),
                    style = MaterialTheme.typography.bodySmall,
                    color = statusColor(HealthStatus.WARN),
                    modifier = Modifier.padding(top = 4.dp),
                )
                Row {
                    TextButton(onClick = { onLeftover(l.hostId, l.project, false) }, enabled = actionsEnabled) { Text(t("Убрать", "Remove")) }
                    TextButton(onClick = { onLeftover(l.hostId, l.project, true) }, enabled = actionsEnabled) { Text(t("Забыть", "Forget")) }
                }
            }
            Row(modifier = Modifier.horizontalScroll(rememberScrollState()).padding(top = 8.dp)) {
                OutlinedButton(onClick = onDeploy, enabled = p.enabled, modifier = Modifier.padding(end = 8.dp)) {
                    Text(t("Выложить", "Deploy"))
                }
                if (p.action == "compose") {
                    OutlinedButton(onClick = onDryRun, modifier = Modifier.padding(end = 8.dp)) { Text(t("Сухой прогон", "Dry run")) }
                }
                if (last != null && last.jobId > 0) {
                    OutlinedButton(onClick = onLog, modifier = Modifier.padding(end = 8.dp)) { Text(t("Журнал", "Log")) }
                }
                OutlinedButton(onClick = onHistory) { Text(t("История", "History")) }
            }
        }
    }
}

/** A pipeline's page: every deployment with its log and rollback. */
@Composable
fun PipelineScreen(viewModel: PipelineViewModel, onOpenJob: (Long) -> Unit) {
    var rollback by remember { mutableStateOf<Deployment?>(null) }
    LaunchedEffect(viewModel) { viewModel.load() }
    rollback?.let { d ->
        AlertDialog(
            onDismissRequest = { rollback = null },
            text = { Text(t("Выложить заново ${d.summary()}?", "Deploy ${d.summary()} again?")) },
            confirmButton = {
                TextButton(onClick = {
                    viewModel.rollback(d.id) { id -> if (id > 0) onOpenJob(id) }
                    rollback = null
                }) { Text(t("Откатить", "Roll back")) }
            },
            dismissButton = { TextButton(onClick = { rollback = null }) { Text(t("Отмена", "Cancel")) } },
        )
    }
    SectionContent(
        state = viewModel.state,
        emptyText = t("Выкладок ещё не было", "No deployments yet"),
        isEmpty = { it.deployments.isEmpty() },
    ) { r ->
        LazyColumn(contentPadding = PaddingValues(16.dp)) {
            items(r.deployments, key = { it.id }) { d ->
                Card(modifier = Modifier.fillMaxWidth().padding(bottom = 8.dp)) {
                    Column(modifier = Modifier.padding(12.dp)) {
                        Text(
                            "${DEPLOY_STATUS[d.status] ?: d.status} · ${d.summary()}",
                            style = MaterialTheme.typography.bodySmall,
                            color = statusColor(deployHealth(d.status)),
                        )
                        if (d.error.isNotBlank()) {
                            Text(d.error, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
                        }
                        Row {
                            if (d.jobId > 0) TextButton(onClick = { onOpenJob(d.jobId) }) { Text(t("Журнал", "Log")) }
                            if (d.status == "succeeded" && d.commit.isNotBlank()) {
                                TextButton(onClick = { rollback = d }) { Text(t("Откатить сюда", "Roll back to this")) }
                            }
                        }
                    }
                }
            }
        }
    }
}
