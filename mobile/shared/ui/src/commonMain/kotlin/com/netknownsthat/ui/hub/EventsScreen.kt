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
import androidx.compose.material3.FilterChip
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.netknownsthat.domain.model.HubEvent
import com.netknownsthat.domain.usecase.hostPath
import com.netknownsthat.ui.status.HealthStatus
import com.netknownsthat.ui.common.SectionContent
import com.netknownsthat.ui.theme.StatusDot
import com.netknownsthat.ui.theme.statusColor
import com.netknownsthat.ui.i18n.t
import com.netknownsthat.ui.common.formatTs

/** Same wording as the web UI's events.kind.* labels. */
val EVENT_KIND get() = mapOf(
    "unreachable" to t("не отвечает", "not answering"),
    "recovered" to t("снова отвечает", "answering again"),
    "problems" to t("новые проблемы", "new problems"),
    "resolved" to t("проблемы устранены", "problems resolved"),
    "job-failed" to t("задание не выполнено", "job failed"),
    "bans" to t("новые баны", "new bans"),
    "rebooted" to t("перезагрузился", "rebooted"),
    "forecast" to t("прогноз", "forecast"),
)

fun eventHealth(kind: String): HealthStatus = when (kind) {
    "unreachable", "problems", "job-failed" -> HealthStatus.BAD
    "bans", "forecast", "rebooted" -> HealthStatus.WARN
    "recovered", "resolved" -> HealthStatus.OK
    else -> HealthStatus.UNKNOWN
}

/** Host section path an event leads to (domain's rule). */
fun eventPath(e: HubEvent): String = e.hostPath()

/**
 * The hub's alerts journal: who stopped answering, new problems, bans,
 * failed jobs. «К хосту» opens the host at the section where the event is
 * seen. The switch on top turns on phone notifications for the kinds the
 * hub itself notifies about (see EventsWorker).
 */
@Composable
fun EventsScreen(
    viewModel: EventsViewModel,
    notifyEnabled: Boolean,
    onNotifyChange: (Boolean) -> Unit,
    onOpenHost: (hostId: Long, path: String) -> Unit,
) {
    val requestPermission = com.netknownsthat.ui.platform.rememberNotificationPermission { granted ->
        if (granted) onNotifyChange(true)
    }
    Column {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 4.dp),
        ) {
            Column(modifier = Modifier.weight(1f)) {
                Text(t("Уведомления на телефоне", "Phone notifications"), style = MaterialTheme.typography.bodyMedium)
                Text(
                    t("Проверка раз в 15 минут; виды — как в настройках оповещений хаба", "Checked every 15 minutes; kinds as in the hub's alert settings"),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            Switch(
                checked = notifyEnabled,
                onCheckedChange = { on -> if (on) requestPermission() else onNotifyChange(false) },
            )
        }
        val kinds = viewModel.state.data?.kinds ?: EVENT_KIND.keys.toList()
        Row(
            modifier = Modifier
                .horizontalScroll(rememberScrollState())
                .padding(horizontal = 16.dp, vertical = 4.dp),
        ) {
            FilterChip(
                selected = viewModel.kindFilter == null,
                onClick = {
                    viewModel.kindFilter = null
                    viewModel.load()
                },
                label = { Text(t("Все", "All")) },
                modifier = Modifier.padding(end = 8.dp),
            )
            kinds.forEach { kind ->
                FilterChip(
                    selected = viewModel.kindFilter == kind,
                    onClick = {
                        viewModel.kindFilter = kind
                        viewModel.load()
                    },
                    label = { Text(EVENT_KIND[kind] ?: kind) },
                    modifier = Modifier.padding(end = 8.dp),
                )
            }
        }
        SectionContent(
            state = viewModel.state,
            emptyText = t("Событий нет", "No events"),
            isEmpty = { it.events.isEmpty() },
        ) { response ->
            LazyColumn(contentPadding = PaddingValues(start = 16.dp, end = 16.dp, bottom = 16.dp)) {
                items(response.events, key = { it.id }) { e ->
                    EventCard(e, onOpenHost = { onOpenHost(e.hostId, eventPath(e)) })
                }
            }
        }
    }
}

@Composable
private fun EventCard(e: HubEvent, onOpenHost: () -> Unit) {
    Card(modifier = Modifier.fillMaxWidth().padding(bottom = 8.dp)) {
        Column(modifier = Modifier.padding(12.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                StatusDot(eventHealth(e.kind))
                Text(
                    text = e.hostName.ifBlank { t("хаб", "hub") },
                    style = MaterialTheme.typography.titleSmall,
                    modifier = Modifier.weight(1f),
                )
                Text(
                    text = EVENT_KIND[e.kind] ?: e.kind,
                    style = MaterialTheme.typography.labelMedium,
                    color = statusColor(eventHealth(e.kind)),
                )
            }
            if (e.detail.isNotBlank()) {
                Text(e.detail, style = MaterialTheme.typography.bodySmall, modifier = Modifier.padding(top = 4.dp))
            }
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(
                    text = formatTs(e.ts),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.weight(1f),
                )
                // host_id is zero on events not tied to a host.
                if (e.hostId != 0L) TextButton(onClick = onOpenHost) { Text(t("К хосту", "To host")) }
            }
        }
    }
}
