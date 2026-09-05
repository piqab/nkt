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
import androidx.compose.material3.Checkbox
import androidx.compose.material3.FilterChip
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Tab
import androidx.compose.material3.TabRow
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import com.netknownsthat.app.net.model.FirewalldPortSpec
import com.netknownsthat.app.net.model.RuleSpec
import com.netknownsthat.app.status.HealthStatus
import com.netknownsthat.app.status.firewallManagerHealth
import com.netknownsthat.app.ui.theme.StatusDot
import com.netknownsthat.app.ui.theme.statusColor

private val UFW_ACTIONS = listOf(
    "allow" to "Разрешить",
    "deny" to "Запретить",
    "reject" to "Отклонить",
    "limit" to "Ограничить",
)

@Composable
fun FirewallScreen(viewModel: FirewallViewModel) {
    var tab by remember { mutableIntStateOf(0) }

    SectionContent(state = viewModel.state, emptyText = "Данных о firewall нет") { data ->
        val hasUfw = data.state.managers.any { it.name.contains("ufw", ignoreCase = true) }
        val hasFirewalld = data.state.managers.any {
            it.name.contains("firewalld", ignoreCase = true)
        }

        Column {
            TabRow(selectedTabIndex = tab) {
                listOf("Правила", "Добавить", "Состояние", "Порты").forEachIndexed { index, title ->
                    Tab(
                        selected = tab == index,
                        onClick = { tab = index },
                        text = { Text(title) },
                    )
                }
            }
            when (tab) {
                0 -> RulesTab(viewModel, data)
                1 -> AddRuleTab(viewModel, hasUfw, hasFirewalld)
                2 -> StateTab(viewModel, data, hasUfw, hasFirewalld)
                else -> LazyColumn(contentPadding = PaddingValues(16.dp)) {
                    items(data.state.listeners) { ListenerRow(it) }
                }
            }
        }
    }
}

@Composable
private fun RulesTab(viewModel: FirewallViewModel, data: FirewallData) {
    var confirmDelete by remember { mutableStateOf<Pair<Int, String>?>(null) }

    confirmDelete?.let { (number, text) ->
        AlertDialog(
            onDismissRequest = { confirmDelete = null },
            title = { Text("Удалить правило #$number") },
            text = {
                Column {
                    Text(text, fontFamily = FontFamily.Monospace)
                    if (text.contains("22") || text.contains("ssh", ignoreCase = true)) {
                        Text(
                            text = "Похоже, это правило про SSH. Если удалить его и потерять " +
                                "доступ, вернуть его будет нечем.",
                            color = statusColor(HealthStatus.BAD),
                            modifier = Modifier.padding(top = 12.dp),
                        )
                    }
                }
            },
            confirmButton = {
                TextButton(onClick = {
                    viewModel.deleteUfwRule(number, text)
                    confirmDelete = null
                }) { Text("Удалить") }
            },
            dismissButton = { TextButton(onClick = { confirmDelete = null }) { Text("Отмена") } },
        )
    }

    LazyColumn(contentPadding = PaddingValues(16.dp)) {
        if (data.numbered.rules.isNotEmpty()) {
            item { SectionTitle("Правила ufw (нумерованные)") }
            items(data.numbered.rules, key = { it.number }) { rule ->
                Card(modifier = Modifier.fillMaxWidth().padding(bottom = 8.dp)) {
                    Column(modifier = Modifier.padding(16.dp)) {
                        Text("#${rule.number}", style = MaterialTheme.typography.labelMedium)
                        Text(
                            text = rule.text,
                            fontFamily = FontFamily.Monospace,
                            style = MaterialTheme.typography.bodySmall,
                            modifier = Modifier.horizontalScroll(rememberScrollState()),
                        )
                        OutlinedButton(
                            onClick = { confirmDelete = rule.number to rule.text },
                            enabled = !viewModel.actionInProgress,
                            modifier = Modifier.padding(top = 12.dp),
                        ) { Text("Удалить") }
                    }
                }
            }
        }

        if (data.numbered.added.isNotEmpty()) {
            item {
                SectionTitle("Правила ufw, добавленные в конфигурацию")
                Text(
                    // ufw prints no numbered list at all while it is inactive,
                    // so these are the only rules visible in that state.
                    text = "Нумерации нет, пока ufw выключен — удаление идёт по описанию правила.",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(bottom = 8.dp),
                )
            }
            items(data.numbered.added) { rule ->
                Card(modifier = Modifier.fillMaxWidth().padding(bottom = 8.dp)) {
                    Column(modifier = Modifier.padding(16.dp)) {
                        Text(rule.spec, fontFamily = FontFamily.Monospace)
                        Text(
                            text = "${rule.action} · ${rule.protocol} ${rule.port}",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                        OutlinedButton(
                            onClick = {
                                viewModel.deleteUfwRuleBySpec(
                                    RuleSpec(
                                        action = rule.action,
                                        port = rule.port,
                                        protocol = rule.protocol,
                                    )
                                )
                            },
                            enabled = !viewModel.actionInProgress,
                            modifier = Modifier.padding(top = 12.dp),
                        ) { Text("Удалить") }
                    }
                }
            }
        }

        item { SectionTitle("Действующие правила") }
        items(data.state.rules, key = { it.id }) { rule ->
            Card(modifier = Modifier.fillMaxWidth().padding(bottom = 8.dp)) {
                Column(modifier = Modifier.padding(16.dp)) {
                    Text(
                        text = "${rule.backend} ${rule.chain} · ${rule.action}",
                        style = MaterialTheme.typography.labelMedium,
                        color = MaterialTheme.colorScheme.primary,
                    )
                    Text(
                        text = rule.raw,
                        style = MaterialTheme.typography.bodySmall,
                        fontFamily = FontFamily.Monospace,
                        modifier = Modifier
                            .horizontalScroll(rememberScrollState())
                            .padding(top = 4.dp),
                    )
                }
            }
        }
    }
}

@Composable
private fun AddRuleTab(viewModel: FirewallViewModel, hasUfw: Boolean, hasFirewalld: Boolean) {
    LazyColumn(contentPadding = PaddingValues(16.dp)) {
        if (hasUfw) item { UfwForm(viewModel) }
        if (hasFirewalld) item { FirewalldForm(viewModel) }
        if (!hasUfw && !hasFirewalld) {
            item {
                Text(
                    text = "На хосте не найден ни ufw, ни firewalld — добавлять правило нечем.",
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
    }
}

@Composable
private fun UfwForm(viewModel: FirewallViewModel) {
    var action by remember { mutableStateOf("allow") }
    var port by remember { mutableStateOf("") }
    var protocol by remember { mutableStateOf("tcp") }
    var from by remember { mutableStateOf("") }
    var comment by remember { mutableStateOf("") }
    var confirming by remember { mutableStateOf<RuleSpec?>(null) }

    val spec = RuleSpec(
        action = action,
        port = port.toIntOrNull() ?: 0,
        protocol = protocol,
        from = from.trim(),
        comment = comment.trim(),
    )
    val risky = ruleRisksLockout(spec)

    confirming?.let { pending ->
        AlertDialog(
            onDismissRequest = { confirming = null },
            title = { Text(if (risky) "Это может отрезать доступ" else "Добавить правило") },
            text = {
                Column {
                    Text(
                        text = "${pending.action} ${pending.port}/${pending.protocol}" +
                            if (pending.from.isNotBlank()) " от ${pending.from}" else "",
                        fontFamily = FontFamily.Monospace,
                    )
                    if (risky) {
                        Text(
                            text = "Порт ${pending.port} — то, через что вы сейчас управляете " +
                                "хостом. Закрыв его, вы потеряете доступ, и починить это с " +
                                "телефона будет нельзя: понадобится консоль у провайдера.",
                            color = statusColor(HealthStatus.BAD),
                            modifier = Modifier.padding(top = 12.dp),
                        )
                    }
                }
            },
            confirmButton = {
                TextButton(onClick = {
                    viewModel.addUfwRule(pending)
                    confirming = null
                }) { Text(if (risky) "Всё равно добавить" else "Добавить") }
            },
            dismissButton = { TextButton(onClick = { confirming = null }) { Text("Отмена") } },
        )
    }

    Card(modifier = Modifier.fillMaxWidth().padding(bottom = 16.dp)) {
        Column(modifier = Modifier.padding(16.dp)) {
            Text("ufw", style = MaterialTheme.typography.titleMedium)

            Row(
                modifier = Modifier
                    .horizontalScroll(rememberScrollState())
                    .padding(top = 8.dp),
            ) {
                UFW_ACTIONS.forEach { (value, label) ->
                    FilterChip(
                        selected = action == value,
                        onClick = { action = value },
                        label = { Text(label) },
                        modifier = Modifier.padding(end = 6.dp),
                    )
                }
            }

            OutlinedTextField(
                value = port,
                onValueChange = { new -> port = new.filter { it.isDigit() }.take(5) },
                label = { Text("Порт") },
                singleLine = true,
                isError = port.isNotEmpty() && (port.toIntOrNull() ?: 0) !in 1..65535,
                modifier = Modifier.fillMaxWidth().padding(top = 8.dp),
            )

            Row(modifier = Modifier.padding(top = 8.dp)) {
                listOf("tcp", "udp").forEach { value ->
                    FilterChip(
                        selected = protocol == value,
                        onClick = { protocol = value },
                        label = { Text(value) },
                        modifier = Modifier.padding(end = 6.dp),
                    )
                }
            }

            OutlinedTextField(
                value = from,
                onValueChange = { from = it },
                label = { Text("Откуда (IP или CIDR, пусто — отовсюду)") },
                singleLine = true,
                modifier = Modifier.fillMaxWidth().padding(top = 8.dp),
            )
            OutlinedTextField(
                value = comment,
                onValueChange = { comment = it },
                label = { Text("Комментарий") },
                singleLine = true,
                modifier = Modifier.fillMaxWidth().padding(top = 8.dp),
            )

            if (risky) {
                Text(
                    text = "Внимание: порт ${spec.port} используется для доступа к хосту.",
                    color = statusColor(HealthStatus.BAD),
                    style = MaterialTheme.typography.bodySmall,
                    modifier = Modifier.padding(top = 8.dp),
                )
            }

            Row(modifier = Modifier.padding(top = 12.dp)) {
                OutlinedButton(
                    onClick = { confirming = spec },
                    enabled = !viewModel.actionInProgress && spec.port in 1..65535,
                    modifier = Modifier.padding(end = 8.dp),
                ) { Text("Добавить") }
                OutlinedButton(
                    onClick = { viewModel.reloadUfw() },
                    enabled = !viewModel.actionInProgress,
                ) { Text("Перечитать") }
            }
        }
    }
}

@Composable
private fun FirewalldForm(viewModel: FirewallViewModel) {
    var zone by remember { mutableStateOf("public") }
    var port by remember { mutableStateOf("") }
    var protocol by remember { mutableStateOf("tcp") }
    var service by remember { mutableStateOf("") }
    var permanent by remember { mutableStateOf(true) }
    var runtime by remember { mutableStateOf(true) }

    // firewalld takes a port or a named service, never both.
    val byService = service.isNotBlank()
    val spec = FirewalldPortSpec(
        zone = zone.trim(),
        port = if (byService) 0 else port.toIntOrNull() ?: 0,
        protocol = if (byService) "" else protocol,
        service = service.trim(),
        permanent = permanent,
        runtime = runtime,
    )
    val valid = zone.isNotBlank() && (byService || spec.port in 1..65535)

    Card(modifier = Modifier.fillMaxWidth().padding(bottom = 16.dp)) {
        Column(modifier = Modifier.padding(16.dp)) {
            Text("firewalld", style = MaterialTheme.typography.titleMedium)

            OutlinedTextField(
                value = zone,
                onValueChange = { zone = it },
                label = { Text("Зона") },
                singleLine = true,
                modifier = Modifier.fillMaxWidth().padding(top = 8.dp),
            )
            OutlinedTextField(
                value = service,
                onValueChange = { service = it },
                label = { Text("Служба (например ssh) — либо порт ниже") },
                singleLine = true,
                modifier = Modifier.fillMaxWidth().padding(top = 8.dp),
            )
            OutlinedTextField(
                value = port,
                onValueChange = { new -> port = new.filter { it.isDigit() }.take(5) },
                label = { Text("Порт") },
                singleLine = true,
                enabled = !byService,
                modifier = Modifier.fillMaxWidth().padding(top = 8.dp),
            )
            Row(modifier = Modifier.padding(top = 8.dp)) {
                listOf("tcp", "udp").forEach { value ->
                    FilterChip(
                        selected = protocol == value,
                        onClick = { protocol = value },
                        enabled = !byService,
                        label = { Text(value) },
                        modifier = Modifier.padding(end = 6.dp),
                    )
                }
            }

            Row(verticalAlignment = Alignment.CenterVertically) {
                Checkbox(checked = permanent, onCheckedChange = { permanent = it })
                Text("Постоянно", style = MaterialTheme.typography.bodySmall)
                Checkbox(checked = runtime, onCheckedChange = { runtime = it })
                Text("Сейчас", style = MaterialTheme.typography.bodySmall)
            }

            Row(modifier = Modifier.padding(top = 12.dp)) {
                OutlinedButton(
                    onClick = { viewModel.addFirewalldRule(spec) },
                    enabled = !viewModel.actionInProgress && valid,
                    modifier = Modifier.padding(end = 8.dp),
                ) { Text("Разрешить") }
                OutlinedButton(
                    onClick = { viewModel.deleteFirewalldRule(spec) },
                    enabled = !viewModel.actionInProgress && valid,
                    modifier = Modifier.padding(end = 8.dp),
                ) { Text("Убрать") }
                OutlinedButton(
                    onClick = { viewModel.reloadFirewalld() },
                    enabled = !viewModel.actionInProgress,
                ) { Text("Перечитать") }
            }
        }
    }
}

@Composable
private fun StateTab(
    viewModel: FirewallViewModel,
    data: FirewallData,
    hasUfw: Boolean,
    hasFirewalld: Boolean,
) {
    LazyColumn(contentPadding = PaddingValues(16.dp)) {
        items(data.state.managers) { manager ->
            val health = firewallManagerHealth(manager.installed, manager.active)
            Card(modifier = Modifier.fillMaxWidth().padding(bottom = 8.dp)) {
                Column(modifier = Modifier.padding(16.dp)) {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        StatusDot(health)
                        Text(manager.name, style = MaterialTheme.typography.titleSmall)
                    }
                    Text(
                        text = when {
                            !manager.installed -> "не установлен"
                            manager.active -> "включён"
                            else -> "установлен, выключен"
                        },
                        style = MaterialTheme.typography.bodySmall,
                        color = statusColor(health),
                    )
                    if (manager.policy.isNotBlank()) {
                        Text(
                            text = "Политика: ${manager.policy}",
                            style = MaterialTheme.typography.bodySmall,
                        )
                    }
                }
            }
        }
        items(data.state.policies) { policy ->
            Card(modifier = Modifier.fillMaxWidth().padding(bottom = 8.dp)) {
                Column(modifier = Modifier.padding(16.dp)) {
                    Text(
                        text = "${policy.backend} ${policy.table}/${policy.chain}",
                        style = MaterialTheme.typography.bodySmall,
                        fontFamily = FontFamily.Monospace,
                    )
                    Text(
                        text = policy.policy,
                        style = MaterialTheme.typography.labelMedium,
                        color = statusColor(
                            if (policy.policy == "DROP") HealthStatus.OK else HealthStatus.WARN
                        ),
                    )
                }
            }
        }
        if (hasUfw || hasFirewalld) {
            item {
                Row(modifier = Modifier.padding(top = 8.dp)) {
                    if (hasUfw) {
                        OutlinedButton(
                            onClick = { viewModel.reloadUfw() },
                            enabled = !viewModel.actionInProgress,
                            modifier = Modifier.padding(end = 8.dp),
                        ) { Text("Перечитать ufw") }
                    }
                    if (hasFirewalld) {
                        OutlinedButton(
                            onClick = { viewModel.reloadFirewalld() },
                            enabled = !viewModel.actionInProgress,
                        ) { Text("Перечитать firewalld") }
                    }
                }
            }
        }
    }
}

@Composable
private fun SectionTitle(text: String) {
    Text(
        text = text,
        style = MaterialTheme.typography.titleMedium,
        modifier = Modifier.padding(vertical = 8.dp),
    )
}
