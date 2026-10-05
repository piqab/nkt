package com.netknownsthat.app.ui.host

import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Card
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Tab
import androidx.compose.material3.TabRow
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import com.netknownsthat.app.net.model.Certificate
import com.netknownsthat.app.status.certificateHealth
import com.netknownsthat.app.status.firewallManagerHealth
import com.netknownsthat.app.ui.theme.StatusDot
import com.netknownsthat.app.ui.theme.statusColor
import com.netknownsthat.app.net.model.Listener
import com.netknownsthat.app.i18n.t

@Composable
fun MiscScreen(viewModel: MiscViewModel) {
    SectionContent(
        state = viewModel.state,
        emptyText = t("Все слушающие сокеты описаны в конфигурации", "All listening sockets are declared in the configuration"),
        isEmpty = { it.listeners.isEmpty() },
    ) { response ->
        LazyColumn(contentPadding = PaddingValues(16.dp)) {
            items(response.listeners) { ListenerRow(it) }
        }
    }
}

@Composable
fun ListenerRow(listener: Listener) {
    Card(modifier = Modifier.fillMaxWidth().padding(bottom = 8.dp)) {
        Column(modifier = Modifier.padding(16.dp)) {
            Text(
                text = "${listener.address}:${listener.port}/${listener.protocol}",
                style = MaterialTheme.typography.titleSmall,
                fontFamily = FontFamily.Monospace,
            )
            Text(
                text = listOfNotNull(
                    listener.process.takeIf { it.isNotBlank() },
                    listener.user.takeIf { it.isNotBlank() },
                    listener.pid.takeIf { it > 0 }?.let { "PID $it" },
                    listener.unit.takeIf { it.isNotBlank() },
                ).joinToString(" · "),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(top = 4.dp),
            )
            if (listener.command.isNotBlank()) {
                Text(
                    text = listener.command,
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

@Composable
fun CertificatesScreen(viewModel: CertificatesViewModel) {
    var tab by remember { mutableIntStateOf(0) }
    LaunchedEffect(Unit) { viewModel.loadForms() }

    Column {
        TabRow(selectedTabIndex = tab) {
            listOf(t("Список", "List"), t("Выпуск", "Issue")).forEachIndexed { index, title ->
                Tab(selected = tab == index, onClick = { tab = index }, text = { Text(title) })
            }
        }
        if (tab == 1) {
            CertificateFormsTab(viewModel)
            return@Column
        }
        CertificatesList(viewModel)
    }
}

@Composable
private fun CertificatesList(viewModel: CertificatesViewModel) {
    SectionContent(
        state = viewModel.state,
        emptyText = t("Сертификаты не найдены", "No certificates found"),
        isEmpty = { it.certificates.isEmpty() },
    ) { response ->
        LazyColumn(contentPadding = PaddingValues(16.dp)) {
            response.summary?.let { summary ->
                item {
                    Card(modifier = Modifier.fillMaxWidth().padding(bottom = 16.dp)) {
                        Column(modifier = Modifier.padding(16.dp)) {
                            LabeledValue(t("Всего", "Total"), "${summary.total}")
                            if (summary.expired > 0) {
                                Text(
                                    text = t("Просрочено: ${summary.expired}", "Expired: ${summary.expired}"),
                                    color = MaterialTheme.colorScheme.error,
                                )
                            }
                            if (summary.expiring > 0) {
                                Text(
                                    text = t("Истекают: ${summary.expiring}", "Expiring: ${summary.expiring}"),
                                    color = MaterialTheme.colorScheme.tertiary,
                                )
                            }
                            if (summary.unmanaged > 0) {
                                Text(
                                    text = t("Без автопродления: ${summary.unmanaged}", "Without auto-renewal: ${summary.unmanaged}"),
                                    color = MaterialTheme.colorScheme.tertiary,
                                )
                            }
                        }
                    }
                }
            }
            items(response.certificates, key = { it.id }) { CertificateCard(it) }
        }
    }
}

@Composable
private fun CertificateCard(cert: Certificate) {
    Card(modifier = Modifier.fillMaxWidth().padding(bottom = 8.dp)) {
        Column(modifier = Modifier.padding(16.dp)) {
            val health = certificateHealth(
                daysLeft = cert.daysLeft,
                automaticRenewal = cert.renewal?.automatic ?: false,
                unreadable = cert.error.isNotBlank(),
            )
            Row(verticalAlignment = Alignment.CenterVertically) {
                StatusDot(health)
                Text(
                    text = cert.names.joinToString(", ").ifBlank { cert.path },
                    style = MaterialTheme.typography.titleSmall,
                )
            }
            Text(
                text = if (cert.daysLeft < 0) t("Просрочен ${-cert.daysLeft} дн. назад", "Expired ${-cert.daysLeft} d ago")
                else t("Осталось ${cert.daysLeft} дн. (до ${cert.notAfter})", "${cert.daysLeft} d left (until ${cert.notAfter})"),
                style = MaterialTheme.typography.bodyMedium,
                color = statusColor(health),
                modifier = Modifier.padding(top = 4.dp),
            )
            Text(
                text = listOfNotNull(
                    cert.service.takeIf { it.isNotBlank() },
                    cert.keyAlgorithm.takeIf { it.isNotBlank() }?.let { "$it ${cert.keyBits}" },
                    if (cert.selfSigned) t("самоподписанный", "self-signed") else null,
                ).joinToString(" · "),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(top = 4.dp),
            )
            cert.renewal?.let { renewal ->
                Text(
                    // The distinction that matters: a certificate nothing
                    // renews is the one that eventually takes a site down.
                    text = if (renewal.automatic) t("Продлевается автоматически (${renewal.tool})", "Renewed automatically (${renewal.tool})")
                    else t("Автопродления нет", "No auto-renewal"),
                    style = MaterialTheme.typography.bodySmall,
                    color = statusColor(
                        if (renewal.automatic) com.netknownsthat.app.status.HealthStatus.OK
                        else com.netknownsthat.app.status.HealthStatus.WARN
                    ),
                    modifier = Modifier.padding(top = 4.dp),
                )
            }
            if (cert.error.isNotBlank()) {
                Text(
                    text = cert.error,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
            }
            Text(
                text = cert.path,
                style = MaterialTheme.typography.bodySmall,
                fontFamily = FontFamily.Monospace,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier
                    .horizontalScroll(rememberScrollState())
                    .padding(top = 4.dp),
            )
        }
    }
}
