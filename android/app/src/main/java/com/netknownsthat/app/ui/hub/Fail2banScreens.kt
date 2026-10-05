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
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.FilterChip
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import com.netknownsthat.app.net.model.Fail2banJail
import com.netknownsthat.app.status.HealthStatus
import com.netknownsthat.app.ui.host.SectionContent
import com.netknownsthat.app.ui.theme.StatusDot
import com.netknownsthat.app.i18n.t

private val BAN_TIMES get() = listOf(3_600L to t("1 час", "1 hour"), 86_400L to t("сутки", "1 day"), 604_800L to t("неделя", "1 week"), 2_592_000L to t("30 дней", "30 days"))

/** Light client-side check only — the server parses and refuses for real
 * (loopback, the hub itself, the caller's own address). */
private val IP_RE = Regex("""^[0-9a-fA-F:.]{2,45}(/\d{1,3})?$""")

/** IP + duration form shared by the host and fleet ban dialogs. */
@Composable
private fun BanDialog(title: String, note: String, onDismiss: () -> Unit, onBan: (String, Long) -> Unit) {
    var ip by remember { mutableStateOf("") }
    var time by remember { mutableLongStateOf(BAN_TIMES[2].first) }
    val valid = IP_RE.matches(ip.trim())
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(title) },
        text = {
            Column {
                OutlinedTextField(
                    value = ip,
                    onValueChange = { ip = it },
                    label = { Text(t("IP-адрес", "IP address")) },
                    singleLine = true,
                    isError = ip.isNotBlank() && !valid,
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri),
                )
                Row(modifier = Modifier.horizontalScroll(rememberScrollState()).padding(top = 8.dp)) {
                    BAN_TIMES.forEach { (secs, label) ->
                        FilterChip(
                            selected = time == secs,
                            onClick = { time = secs },
                            label = { Text(label) },
                            modifier = Modifier.padding(end = 6.dp),
                        )
                    }
                }
                Text(note, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        },
        confirmButton = {
            TextButton(enabled = valid, onClick = { onBan(ip.trim(), time) }) { Text(t("Забанить", "Ban")) }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text(t("Отмена", "Cancel")) } },
    )
}

@Composable
private fun ConfirmDialog(text: String, onDismiss: () -> Unit, onConfirm: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        text = { Text(text) },
        confirmButton = { TextButton(onClick = onConfirm) { Text(t("Да", "Yes")) } },
        dismissButton = { TextButton(onClick = onDismiss) { Text(t("Отмена", "Cancel")) } },
    )
}

/** Host fail2ban: jails with their current bans, manual ban and unban. */
@Composable
fun Fail2banScreen(viewModel: Fail2banViewModel) {
    var banOpen by remember { mutableStateOf(false) }
    var unban by remember { mutableStateOf<Pair<String, String>?>(null) }
    if (banOpen) {
        BanDialog(
            title = t("Забанить адрес", "Ban an address"),
            note = t("В ручной джейл nkt на этом хосте", "Into nkt's manual jail on this host"),
            onDismiss = { banOpen = false },
            onBan = { ip, t ->
                viewModel.ban(ip, t)
                banOpen = false
            },
        )
    }
    unban?.let { (ip, jail) ->
        ConfirmDialog(t("Разбанить $ip в джейле $jail?", "Unban $ip in jail $jail?"), onDismiss = { unban = null }) {
            viewModel.unban(ip, jail)
            unban = null
        }
    }
    SectionContent(state = viewModel.state) { r ->
        val st = r.state
        LazyColumn(contentPadding = PaddingValues(16.dp)) {
            item {
                Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.padding(bottom = 8.dp)) {
                    StatusDot(
                        when {
                            !st.installed -> HealthStatus.UNKNOWN
                            st.running -> HealthStatus.OK
                            else -> HealthStatus.BAD
                        },
                    )
                    Text(
                        text = when {
                            !st.installed -> t("fail2ban не установлен — поставить можно в веб-интерфейсе", "fail2ban is not installed — it can be installed from the web UI")
                            st.running -> t("fail2ban ${st.version} работает", "fail2ban ${st.version} is running")
                            else -> t("fail2ban установлен, но не запущен", "fail2ban is installed but not running")
                        },
                        modifier = Modifier.weight(1f),
                    )
                }
                if (st.installed) {
                    Button(
                        onClick = { banOpen = true },
                        enabled = st.running && r.manualReady && !viewModel.actionInProgress,
                        modifier = Modifier.padding(bottom = 8.dp),
                    ) { Text(t("Забанить IP", "Ban IP")) }
                }
            }
            items(st.jails, key = { it.name }) { jail ->
                JailCard(jail, actionsEnabled = !viewModel.actionInProgress, onUnban = { ip -> unban = ip to jail.name })
            }
        }
    }
}

@Composable
private fun JailCard(jail: Fail2banJail, actionsEnabled: Boolean, onUnban: (String) -> Unit) {
    Card(modifier = Modifier.fillMaxWidth().padding(bottom = 8.dp)) {
        Column(modifier = Modifier.padding(12.dp)) {
            Text(jail.name, style = MaterialTheme.typography.titleSmall)
            Text(
                text = t("забанено ${jail.banned} (всего ${jail.totalBanned}) · неудачных ${jail.failed} (${jail.totalFailed}) · ", "banned ${jail.banned} (total ${jail.totalBanned}) · failed ${jail.failed} (${jail.totalFailed}) · ") +
                    t("maxretry ${jail.maxRetry} · бан ${jail.banTime} с", "maxretry ${jail.maxRetry} · ban ${jail.banTime} s"),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            jail.bans.forEach { ban ->
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Column(modifier = Modifier.weight(1f)) {
                        Text(ban.ip, fontFamily = FontFamily.Monospace)
                        if (ban.until.isNotBlank()) {
                            Text(
                                t("до ${formatTs(ban.until)}", "until ${formatTs(ban.until)}"),
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                    }
                    TextButton(onClick = { onUnban(ban.ip) }, enabled = actionsEnabled) { Text(t("Разбанить", "Unban")) }
                }
            }
        }
    }
}

/** Fleet fail2ban on the hub: every banned address and where; ban or unban
 * on every host at once (a hub job — its log opens right away). */
@Composable
fun FleetFail2banScreen(viewModel: FleetFail2banViewModel, jobLog: JobLogViewModel) {
    var banOpen by remember { mutableStateOf(false) }
    var unban by remember { mutableStateOf<String?>(null) }
    val openJob: (Long) -> Unit = { id ->
        jobLog.show(JobPlace.HUB, id)
        viewModel.load()
    }
    if (banOpen) {
        BanDialog(
            title = t("Забанить на всех хостах", "Ban on all hosts"),
            note = t("На каждом хосте, где есть fail2ban; идёт заданием хаба", "On every host with fail2ban; runs as a hub job"),
            onDismiss = { banOpen = false },
            onBan = { ip, t ->
                viewModel.fleet("ban", listOf(ip), t, emptyList(), openJob)
                banOpen = false
            },
        )
    }
    unban?.let { ip ->
        ConfirmDialog(t("Разбанить $ip на всех хостах?", "Unban $ip on all hosts?"), onDismiss = { unban = null }) {
            viewModel.fleet("unban", listOf(ip), 0, emptyList(), openJob)
            unban = null
        }
    }
    SectionContent(state = viewModel.state) { r ->
        LazyColumn(contentPadding = PaddingValues(16.dp)) {
            item {
                Button(onClick = { banOpen = true }, modifier = Modifier.padding(bottom = 8.dp)) {
                    Text(t("Забанить на всех", "Ban everywhere"))
                }
                Text(
                    text = r.hosts.joinToString(" · ") { h ->
                        when {
                            !h.installed -> t("${h.name}: нет", "${h.name}: none")
                            !h.running -> t("${h.name}: остановлен", "${h.name}: stopped")
                            else -> "${h.name}: ${h.banned}"
                        }
                    },
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(bottom = 8.dp),
                )
                if (r.ips.isEmpty()) Text(t("Забаненных адресов нет", "No banned addresses"))
            }
            items(r.ips, key = { it.ip }) { item ->
                Card(modifier = Modifier.fillMaxWidth().padding(bottom = 8.dp)) {
                    Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.padding(12.dp)) {
                        Column(modifier = Modifier.weight(1f)) {
                            Text(item.ip, fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.titleSmall)
                            Text(
                                text = item.hosts.joinToString(" · ") { "${it.name} (${it.jails.joinToString()})" },
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                        TextButton(onClick = { unban = item.ip }) { Text(t("Разбанить везде", "Unban everywhere")) }
                    }
                }
            }
        }
    }
}
