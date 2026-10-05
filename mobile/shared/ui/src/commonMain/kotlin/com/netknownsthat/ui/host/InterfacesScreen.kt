package com.netknownsthat.ui.host

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.Card
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import com.netknownsthat.domain.model.NetworkInterface
import com.netknownsthat.ui.status.interfaceHealth
import com.netknownsthat.ui.theme.StatusDot
import com.netknownsthat.ui.theme.statusColor
import com.netknownsthat.ui.i18n.t
import com.netknownsthat.ui.common.humanBytes
import com.netknownsthat.ui.common.SectionContent

@Composable
fun InterfacesScreen(viewModel: InterfacesViewModel) {
    SectionContent(
        state = viewModel.state,
        emptyText = t("Интерфейсы не найдены", "No interfaces found"),
        isEmpty = { it.interfaces.isEmpty() },
    ) { response ->
        LazyColumn(contentPadding = PaddingValues(16.dp)) {
            items(response.interfaces, key = { it.name }) { InterfaceCard(it) }
        }
    }
}

@Composable
private fun InterfaceCard(iface: NetworkInterface) {
    Card(modifier = Modifier.fillMaxWidth().padding(bottom = 8.dp)) {
        Column(modifier = Modifier.padding(16.dp)) {
            val health = interfaceHealth(iface.up, iface.lowerUp, iface.loopback)
            Row(
                modifier = Modifier.fillMaxWidth(),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                StatusDot(health)
                Text(
                    text = iface.name,
                    style = MaterialTheme.typography.titleSmall,
                    modifier = Modifier.weight(1f),
                )
                Text(
                    text = linkState(iface),
                    style = MaterialTheme.typography.labelMedium,
                    color = statusColor(health),
                )
            }

            if (iface.addresses.isNotEmpty()) {
                Text(
                    text = iface.addresses.joinToString("\n"),
                    style = MaterialTheme.typography.bodyMedium,
                    fontFamily = FontFamily.Monospace,
                    modifier = Modifier.padding(top = 8.dp),
                )
            }

            iface.mac?.takeIf { it.isNotBlank() }?.let {
                Text(
                    text = it,
                    style = MaterialTheme.typography.bodySmall,
                    fontFamily = FontFamily.Monospace,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(top = 4.dp),
                )
            }

            Text(
                text = t("Принято ${humanBytes(iface.rxBytes)} · Передано ${humanBytes(iface.txBytes)}", "Received ${humanBytes(iface.rxBytes)} · Sent ${humanBytes(iface.txBytes)}"),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(top = 8.dp),
            )

            val problems = iface.rxErrors + iface.txErrors + iface.rxDropped + iface.txDropped
            if (problems > 0) {
                Text(
                    text = t("Ошибки: ${iface.rxErrors + iface.txErrors} · ", "Errors: ${iface.rxErrors + iface.txErrors} · ") +
                        t("потери: ${iface.rxDropped + iface.txDropped}", "dropped: ${iface.rxDropped + iface.txDropped}"),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
            }

            iface.dockerNetwork?.takeIf { it.isNotBlank() }?.let {
                Text(
                    text = t("Сеть Docker: $it (контейнеров: ${iface.attachedContainers})", "Docker network: $it (containers: ${iface.attachedContainers})"),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(top = 4.dp),
                )
            }
        }
    }
}

/** Up without carrier is the case worth naming outright — an interface that
 * is switched on but has nothing on the other end looks fine to `ip link`. */
private fun linkState(iface: NetworkInterface): String = when {
    iface.loopback -> "loopback"
    iface.up && iface.lowerUp -> t("есть линк", "link up")
    iface.up -> t("включён, нет линка", "up, no link")
    else -> t("выключен", "disabled")
}

