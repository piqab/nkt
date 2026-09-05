package com.netknownsthat.app.ui.host

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
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.netknownsthat.app.net.model.ServiceUnit

private val ACTION_LABELS = mapOf(
    "start" to "Запустить",
    "stop" to "Остановить",
    "restart" to "Перезапустить",
    "reload" to "Перечитать",
    "enable" to "Включить автозапуск",
    "disable" to "Выключить автозапуск",
)

/** Actions worth a confirmation step: each one interrupts a running service,
 * and on a remote host that is not something to do by a stray tap. */
private val CONFIRM_ACTIONS = setOf("stop", "restart")

@Composable
fun ServicesScreen(viewModel: ServicesViewModel) {
    var pending by remember { mutableStateOf<Pair<ServiceUnit, String>?>(null) }

    pending?.let { (service, action) ->
        AlertDialog(
            onDismissRequest = { pending = null },
            title = { Text(ACTION_LABELS[action] ?: action) },
            text = { Text("${service.name} — выполнить «${ACTION_LABELS[action] ?: action}»?") },
            confirmButton = {
                TextButton(onClick = {
                    viewModel.action(service.name, action)
                    pending = null
                }) { Text("Да") }
            },
            dismissButton = { TextButton(onClick = { pending = null }) { Text("Отмена") } },
        )
    }

    SectionContent(
        state = viewModel.state,
        emptyText = "Сервисы не найдены",
        isEmpty = { it.services.isEmpty() },
    ) { response ->
        LazyColumn(contentPadding = PaddingValues(16.dp)) {
            items(response.services, key = { it.name }) { service ->
                ServiceCard(
                    service = service,
                    // allow_mutations is the host's own switch (a read-only
                    // deployment sets it false); honouring it here keeps the
                    // app from offering buttons the server will refuse.
                    actionsEnabled = response.allowMutations && !viewModel.actionInProgress,
                    onAction = { action ->
                        if (action in CONFIRM_ACTIONS) pending = service to action
                        else viewModel.action(service.name, action)
                    },
                )
            }
        }
    }
}

@Composable
private fun ServiceCard(
    service: ServiceUnit,
    actionsEnabled: Boolean,
    onAction: (String) -> Unit,
) {
    Card(modifier = Modifier.fillMaxWidth().padding(bottom = 8.dp)) {
        Column(modifier = Modifier.padding(16.dp)) {
            Row(modifier = Modifier.fillMaxWidth()) {
                Text(
                    text = service.name,
                    style = MaterialTheme.typography.titleSmall,
                    modifier = Modifier.weight(1f),
                )
                Text(
                    text = service.activeState,
                    style = MaterialTheme.typography.labelMedium,
                    color = when (service.activeState) {
                        "active" -> MaterialTheme.colorScheme.primary
                        "failed" -> MaterialTheme.colorScheme.error
                        else -> MaterialTheme.colorScheme.onSurfaceVariant
                    },
                )
            }
            if (service.description.isNotBlank()) {
                Text(
                    text = service.description,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(top = 4.dp),
                )
            }
            val details = listOfNotNull(
                service.subState.takeIf { it.isNotBlank() },
                service.enabled.takeIf { it.isNotBlank() },
                service.since.takeIf { it.isNotBlank() }?.let { "с $it" },
                service.mainPid.takeIf { it > 0 }?.let { "PID $it" },
            ).joinToString(" · ")
            if (details.isNotEmpty()) {
                Text(
                    text = details,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(top = 4.dp),
                )
            }

            if (service.actions.isNotEmpty()) {
                Row(
                    modifier = Modifier
                        .horizontalScroll(rememberScrollState())
                        .padding(top = 12.dp),
                ) {
                    service.actions.forEach { action ->
                        OutlinedButton(
                            onClick = { onAction(action) },
                            enabled = actionsEnabled,
                            modifier = Modifier.padding(end = 8.dp),
                        ) {
                            Text(ACTION_LABELS[action] ?: action)
                        }
                    }
                }
            }
        }
    }
}
