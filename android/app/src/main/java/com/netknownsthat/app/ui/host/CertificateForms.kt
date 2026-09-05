package com.netknownsthat.app.ui.host

import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Card
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.FilterChip
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import com.netknownsthat.app.status.HealthStatus
import com.netknownsthat.app.status.certificateHealth
import com.netknownsthat.app.ui.theme.StatusDot
import com.netknownsthat.app.ui.theme.statusColor

/**
 * The three things this app can do about certificates: issue a real one with
 * certbot, renew one certbot already manages, and generate a self-signed one.
 * Plus repackaging an existing lineage into the single PEM haproxy wants.
 *
 * Issuing and renewing are long jobs on the host (stop services, run certbot,
 * restart), so they report progress from a polled log rather than a spinner
 * that says nothing — see CertificatesViewModel.startJob.
 */
@Composable
fun CertificateFormsTab(viewModel: CertificatesViewModel) {
    if (viewModel.jobRunning || viewModel.jobEvents.isNotEmpty() || viewModel.jobError != null) {
        JobLogDialog(viewModel)
    }
    viewModel.selfSigned?.let { SelfSignedResultDialog(it) { viewModel.dismissSelfSigned() } }

    Column(
        modifier = Modifier
            .verticalScroll(rememberScrollState())
            .padding(16.dp),
    ) {
        IssueForm(viewModel)
        RenewForm(viewModel)
        SelfSignedForm(viewModel)
        CombineForm(viewModel)
    }
}

@Composable
private fun IssueForm(viewModel: CertificatesViewModel) {
    var domains by remember { mutableStateOf("") }
    val list = domains.split(',', ' ', '\n').map { it.trim() }.filter { it.isNotEmpty() }

    FormCard("Выпустить сертификат Let's Encrypt") {
        Text(
            text = "certbot должен суметь подтвердить владение доменом с этого хоста — " +
                "то есть имя уже должно указывать сюда, а 80-й порт быть доступен снаружи.",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        OutlinedTextField(
            value = domains,
            onValueChange = { domains = it },
            label = { Text("Домены через запятую") },
            modifier = Modifier.fillMaxWidth().padding(top = 8.dp),
        )
        OutlinedButton(
            onClick = { viewModel.issue(list) },
            enabled = list.isNotEmpty() && !viewModel.jobRunning,
            modifier = Modifier.padding(top = 12.dp),
        ) { Text("Выпустить") }
    }
}

@Composable
private fun RenewForm(viewModel: CertificatesViewModel) {
    if (viewModel.lineages.isEmpty()) return

    FormCard("Продлить существующий") {
        viewModel.lineages.forEach { lineage ->
            val health = certificateHealth(
                daysLeft = lineage.daysLeft,
                automaticRenewal = true,
                unreadable = !lineage.known,
            )
            Row(modifier = Modifier.fillMaxWidth().padding(top = 8.dp)) {
                Column(modifier = Modifier.weight(1f)) {
                    Row {
                        StatusDot(health)
                        Text(
                            // certbot names IDN lineages in punycode; the
                            // readable form is what the operator recognises.
                            text = lineage.nameUnicode.ifBlank { lineage.name },
                            style = MaterialTheme.typography.bodyMedium,
                        )
                    }
                    Text(
                        text = if (lineage.known) "осталось ${lineage.daysLeft} дн."
                        else "срок неизвестен — fullchain.pem не прочитан",
                        style = MaterialTheme.typography.bodySmall,
                        color = statusColor(health),
                    )
                }
                OutlinedButton(
                    onClick = { viewModel.renew(lineage.name) },
                    enabled = !viewModel.jobRunning,
                ) { Text("Продлить") }
            }
        }
    }
}

@Composable
private fun SelfSignedForm(viewModel: CertificatesViewModel) {
    var names by remember { mutableStateOf("") }
    var service by remember { mutableStateOf("nginx") }
    var bits by remember { mutableStateOf(2048) }
    var days by remember { mutableStateOf("397") }
    val list = names.split(',', ' ', '\n').map { it.trim() }.filter { it.isNotEmpty() }

    FormCard("Самоподписанный сертификат") {
        Text(
            text = "Браузеры такому не доверяют — это для внутренних адресов и проверок. " +
                "Конфигурацию сервиса он не меняет: выдаёт готовый фрагмент, который можно " +
                "вставить через редактор конфигурации.",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        OutlinedTextField(
            value = names,
            onValueChange = { names = it },
            label = { Text("Имена через запятую") },
            modifier = Modifier.fillMaxWidth().padding(top = 8.dp),
        )
        Row(modifier = Modifier.padding(top = 8.dp)) {
            listOf("nginx", "haproxy").forEach { value ->
                FilterChip(
                    selected = service == value,
                    onClick = { service = value },
                    label = { Text(value) },
                    modifier = Modifier.padding(end = 6.dp),
                )
            }
        }
        Row(modifier = Modifier.padding(top = 8.dp)) {
            listOf(2048, 3072, 4096).forEach { value ->
                FilterChip(
                    selected = bits == value,
                    onClick = { bits = value },
                    label = { Text("$value бит") },
                    modifier = Modifier.padding(end = 6.dp),
                )
            }
        }
        OutlinedTextField(
            value = days,
            onValueChange = { new -> days = new.filter { it.isDigit() }.take(3) },
            label = { Text("Срок в днях (1…825)") },
            singleLine = true,
            modifier = Modifier.fillMaxWidth().padding(top = 8.dp),
        )
        OutlinedButton(
            onClick = { viewModel.generateSelfSigned(list, service, bits, days.toIntOrNull() ?: 397) },
            enabled = list.isNotEmpty() && (days.toIntOrNull() ?: 0) in 1..825,
            modifier = Modifier.padding(top = 12.dp),
        ) { Text("Создать") }
    }
}

@Composable
private fun CombineForm(viewModel: CertificatesViewModel) {
    if (viewModel.lineages.isEmpty()) return
    var lineage by remember { mutableStateOf("") }
    var target by remember { mutableStateOf("") }

    FormCard("Собрать PEM для haproxy") {
        Text(
            text = "haproxy ждёт сертификат и ключ одним файлом. certbot здесь не вызывается — " +
                "только переупаковывается уже выпущенное.",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Row(
            modifier = Modifier
                .horizontalScroll(rememberScrollState())
                .padding(top = 8.dp),
        ) {
            viewModel.lineages.forEach { item ->
                FilterChip(
                    selected = lineage == item.name,
                    onClick = { lineage = item.name },
                    label = { Text(item.nameUnicode.ifBlank { item.name }) },
                    modifier = Modifier.padding(end = 6.dp),
                )
            }
        }
        if (viewModel.haproxyPaths.isNotEmpty()) {
            Text(
                text = "Перезаписать существующий файл (иначе будет создан новый):",
                style = MaterialTheme.typography.bodySmall,
                modifier = Modifier.padding(top = 8.dp),
            )
            viewModel.haproxyPaths.forEach { path ->
                FilterChip(
                    selected = target == path,
                    onClick = { target = if (target == path) "" else path },
                    label = { Text(path, fontFamily = FontFamily.Monospace) },
                    modifier = Modifier.padding(top = 4.dp),
                )
            }
        }
        OutlinedButton(
            onClick = { viewModel.combine(lineage, target) },
            enabled = lineage.isNotEmpty() && !viewModel.actionInProgress,
            modifier = Modifier.padding(top = 12.dp),
        ) { Text("Собрать") }
    }
}

@Composable
private fun FormCard(title: String, content: @Composable () -> Unit) {
    Card(modifier = Modifier.fillMaxWidth().padding(bottom = 16.dp)) {
        Column(modifier = Modifier.padding(16.dp)) {
            Text(title, style = MaterialTheme.typography.titleMedium)
            content()
        }
    }
}

@Composable
private fun JobLogDialog(viewModel: CertificatesViewModel) {
    AlertDialog(
        onDismissRequest = { if (!viewModel.jobRunning) viewModel.dismissJob() },
        title = { Text(if (viewModel.jobRunning) "Выполняется…" else "Готово") },
        text = {
            Column(modifier = Modifier.verticalScroll(rememberScrollState())) {
                if (viewModel.jobRunning) {
                    CircularProgressIndicator(modifier = Modifier.padding(bottom = 12.dp))
                }
                viewModel.jobEvents.forEach { event ->
                    Text(
                        text = event.text,
                        style = MaterialTheme.typography.bodySmall,
                        fontFamily = FontFamily.Monospace,
                    )
                }
                viewModel.jobError?.takeIf { it.isNotBlank() }?.let {
                    Text(
                        text = it,
                        color = statusColor(HealthStatus.BAD),
                        modifier = Modifier.padding(top = 8.dp),
                    )
                }
            }
        },
        confirmButton = {
            TextButton(
                onClick = { viewModel.dismissJob() },
                // Closing mid-run would leave the poll going with nothing
                // showing it; certbot takes minutes and the log is the only
                // sign of progress.
                enabled = !viewModel.jobRunning,
            ) { Text("Закрыть") }
        },
    )
}

@Composable
private fun SelfSignedResultDialog(
    result: com.netknownsthat.app.net.model.SelfSignedResult,
    onDismiss: () -> Unit,
) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("Сертификат создан") },
        text = {
            Column(modifier = Modifier.verticalScroll(rememberScrollState())) {
                Text(result.names.joinToString(", "))
                listOfNotNull(
                    result.certPath.takeIf { it.isNotBlank() }?.let { "Сертификат: $it" },
                    result.keyPath.takeIf { it.isNotBlank() }?.let { "Ключ: $it" },
                    result.combinedPath.takeIf { it.isNotBlank() }?.let { "PEM: $it" },
                    result.notAfter.takeIf { it.isNotBlank() }?.let { "Действует до: $it" },
                ).forEach {
                    Text(
                        text = it,
                        style = MaterialTheme.typography.bodySmall,
                        fontFamily = FontFamily.Monospace,
                        modifier = Modifier.padding(top = 4.dp),
                    )
                }
                if (result.snippet.isNotBlank()) {
                    Text(
                        text = "Фрагмент конфигурации — вставьте его через раздел " +
                            "«Конфигурация», приложение не правит конфиги само:",
                        style = MaterialTheme.typography.bodySmall,
                        modifier = Modifier.padding(top = 12.dp),
                    )
                    Text(
                        text = result.snippet,
                        style = MaterialTheme.typography.bodySmall,
                        fontFamily = FontFamily.Monospace,
                        modifier = Modifier
                            .horizontalScroll(rememberScrollState())
                            .padding(top = 4.dp),
                    )
                }
            }
        },
        confirmButton = { TextButton(onClick = onDismiss) { Text("Закрыть") } },
    )
}
