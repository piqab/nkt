package com.netknownsthat.app.ui

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Checkbox
import androidx.compose.material3.MaterialTheme
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
import kotlinx.coroutines.delay

/**
 * Shown once at startup: this app can restart services, rewrite configs and
 * close firewall ports on real machines, and it has not been through the kind
 * of use that earns trust yet.
 *
 * It dismisses itself after a couple of seconds so it does not become a tap
 * everyone learns to make without reading — but the checkbox is the operator's
 * to set, and once set the notice never returns.
 */
@Composable
fun BetaNotice(
    onDismiss: (dontShowAgain: Boolean) -> Unit,
) {
    var dontShowAgain by remember { mutableStateOf(false) }

    // The auto-dismiss must not fire while the checkbox is being considered:
    // closing the dialog out from under a half-made decision would drop it.
    LaunchedEffect(dontShowAgain) {
        if (!dontShowAgain) {
            delay(2500)
            onDismiss(false)
        }
    }

    AlertDialog(
        onDismissRequest = { onDismiss(dontShowAgain) },
        title = { Text("Бета-версия") },
        text = {
            Column {
                Text(
                    text = "Приложение управляет настоящими хостами: перезапускает сервисы, " +
                        "переписывает конфигурацию, меняет правила firewall. Оно ещё не " +
                        "обкатано — проверяйте, что делаете, особенно на боевых машинах.",
                    style = MaterialTheme.typography.bodyMedium,
                )
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    modifier = Modifier.padding(top = 16.dp),
                ) {
                    Checkbox(
                        checked = dontShowAgain,
                        onCheckedChange = { dontShowAgain = it },
                    )
                    Text("Больше не показывать", style = MaterialTheme.typography.bodyMedium)
                }
            }
        },
        confirmButton = {
            TextButton(onClick = { onDismiss(dontShowAgain) }) { Text("Понятно") }
        },
    )
}
