package com.netknownsthat.ui.host

import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.FlowRow
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
import com.netknownsthat.ui.status.HealthStatus
import com.netknownsthat.ui.status.certificateHealth
import com.netknownsthat.ui.theme.StatusDot
import com.netknownsthat.ui.theme.statusColor
import com.netknownsthat.ui.i18n.t

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
    if (viewModel.selfSigned.isNotEmpty()) {
        SelfSignedResultDialog(viewModel.selfSigned) { viewModel.dismissSelfSigned() }
    }

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

    FormCard(t("Выпустить сертификат Let's Encrypt", "Issue a Let's Encrypt certificate")) {
        Text(
            text = t("certbot должен суметь подтвердить владение доменом с этого хоста — ", "certbot must be able to prove domain ownership from this host — ") +
                t("то есть имя уже должно указывать сюда, а 80-й порт быть доступен снаружи.", "so the name must already point here, and port 80 must be reachable from outside."),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        OutlinedTextField(
            value = domains,
            onValueChange = { domains = it },
            label = { Text(t("Домены через запятую", "Domains, comma-separated")) },
            modifier = Modifier.fillMaxWidth().padding(top = 8.dp),
        )
        OutlinedButton(
            onClick = { viewModel.issue(list) },
            enabled = list.isNotEmpty() && !viewModel.jobRunning,
            modifier = Modifier.padding(top = 12.dp),
        ) { Text(t("Выпустить", "Issue")) }
    }
}

@Composable
private fun RenewForm(viewModel: CertificatesViewModel) {
    if (viewModel.lineages.isEmpty()) return

    FormCard(t("Продлить существующий", "Renew existing")) {
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
                        text = if (lineage.known) t("осталось ${lineage.daysLeft} дн.", "${lineage.daysLeft} d left")
                        else t("срок неизвестен — fullchain.pem не прочитан", "expiry unknown — fullchain.pem not read"),
                        style = MaterialTheme.typography.bodySmall,
                        color = statusColor(health),
                    )
                }
                OutlinedButton(
                    onClick = { viewModel.renew(lineage.name) },
                    enabled = !viewModel.jobRunning,
                ) { Text(t("Продлить", "Renew")) }
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

    FormCard(t("Самоподписанный сертификат", "Self-signed certificate")) {
        Text(
            text = t("Браузеры такому не доверяют — это для внутренних адресов и проверок. ", "Browsers do not trust these — they are for internal addresses and tests. ") +
                t("Конфигурацию сервиса он не меняет: выдаёт готовый фрагмент, который можно ", "It does not change the service configuration: it gives a ready snippet that can be ") +
                t("вставить через редактор конфигурации.", "inserted with the configuration editor."),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        OutlinedTextField(
            value = names,
            onValueChange = { names = it },
            label = { Text(t("Имена через запятую", "Names, comma-separated")) },
            modifier = Modifier.fillMaxWidth().padding(top = 8.dp),
        )
        FlowRow(modifier = Modifier.padding(top = 8.dp)) {
            listOf("nginx", "haproxy").forEach { value ->
                FilterChip(
                    selected = service == value,
                    onClick = { service = value },
                    label = { Text(value) },
                    modifier = Modifier.padding(end = 6.dp),
                )
            }
        }
        FlowRow(modifier = Modifier.padding(top = 8.dp)) {
            listOf(2048, 3072, 4096).forEach { value ->
                FilterChip(
                    selected = bits == value,
                    onClick = { bits = value },
                    label = { Text(t("$value бит", "$value bits")) },
                    modifier = Modifier.padding(end = 6.dp),
                )
            }
        }
        OutlinedTextField(
            value = days,
            onValueChange = { new -> days = new.filter { it.isDigit() }.take(3) },
            label = { Text(t("Срок в днях (1…825)", "Validity in days (1…825)")) },
            singleLine = true,
            modifier = Modifier.fillMaxWidth().padding(top = 8.dp),
        )
        OutlinedButton(
            onClick = { viewModel.generateSelfSigned(list, service, bits, days.toIntOrNull() ?: 397) },
            enabled = list.isNotEmpty() && (days.toIntOrNull() ?: 0) in 1..825,
            modifier = Modifier.padding(top = 12.dp),
        ) { Text(t("Создать", "Create")) }
    }
}

@Composable
private fun CombineForm(viewModel: CertificatesViewModel) {
    if (viewModel.lineages.isEmpty()) return
    var lineage by remember { mutableStateOf("") }
    var target by remember { mutableStateOf("") }

    FormCard(t("Собрать PEM для haproxy", "Build a PEM for haproxy")) {
        Text(
            text = t("haproxy ждёт сертификат и ключ одним файлом. certbot здесь не вызывается — ", "haproxy expects the certificate and key in one file. certbot is not called here — ") +
                t("только переупаковывается уже выпущенное.", "only an already issued certificate is repackaged."),
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
                text = t("Перезаписать существующий файл (иначе будет создан новый):", "Overwrite an existing file (otherwise a new one is created):"),
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
        ) { Text(t("Собрать", "Build")) }
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
        title = { Text(if (viewModel.jobRunning) t("Выполняется…", "Running…") else t("Готово", "Done")) },
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
            ) { Text(t("Закрыть", "Close")) }
        },
    )
}

@Composable
private fun SelfSignedResultDialog(
    results: List<com.netknownsthat.domain.model.SelfSignedResult>,
    onDismiss: () -> Unit,
) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = {
            Text(if (results.size > 1) t("Создано сертификатов: ${results.size}", "Certificates created: ${results.size}") else t("Сертификат создан", "Certificate created"))
        },
        text = {
            Column(modifier = Modifier.verticalScroll(rememberScrollState())) {
                results.forEach { result ->
                Text(result.names.joinToString(", "))
                listOfNotNull(
                    result.certPath.takeIf { it.isNotBlank() }?.let { t("Сертификат: $it", "Certificate: $it") },
                    result.keyPath.takeIf { it.isNotBlank() }?.let { t("Ключ: $it", "Key: $it") },
                    result.combinedPath.takeIf { it.isNotBlank() }?.let { "PEM: $it" },
                    result.notAfter.takeIf { it.isNotBlank() }?.let { t("Действует до: $it", "Valid until: $it") },
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
                        text = t("Фрагмент конфигурации — вставьте его через раздел ", "Configuration snippet — insert it via the ") +
                            t("«Конфигурация», приложение не правит конфиги само:", "“Configuration” section, the app does not edit configs itself:"),
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
            }
        },
        confirmButton = { TextButton(onClick = onDismiss) { Text(t("Закрыть", "Close")) } },
    )
}
