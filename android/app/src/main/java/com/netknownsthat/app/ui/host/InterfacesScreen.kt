package com.netknownsthat.app.ui.host

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
import com.netknownsthat.app.net.model.NetworkInterface
import com.netknownsthat.app.status.interfaceHealth
import com.netknownsthat.app.ui.theme.StatusDot
import com.netknownsthat.app.ui.theme.statusColor
import java.util.Locale

@Composable
fun InterfacesScreen(viewModel: InterfacesViewModel) {
    SectionContent(
        state = viewModel.state,
        emptyText = "Интерфейсы не найдены",
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
                text = "Принято ${humanBytes(iface.rxBytes)} · Передано ${humanBytes(iface.txBytes)}",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(top = 8.dp),
            )

            val problems = iface.rxErrors + iface.txErrors + iface.rxDropped + iface.txDropped
            if (problems > 0) {
                Text(
                    text = "Ошибки: ${iface.rxErrors + iface.txErrors} · " +
                        "потери: ${iface.rxDropped + iface.txDropped}",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
            }

            iface.dockerNetwork?.takeIf { it.isNotBlank() }?.let {
                Text(
                    text = "Сеть Docker: $it (контейнеров: ${iface.attachedContainers})",
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
    iface.up && iface.lowerUp -> "есть линк"
    iface.up -> "включён, нет линка"
    else -> "выключен"
}

private fun humanBytes(bytes: Long): String {
    if (bytes < 1024) return "$bytes Б"
    val units = listOf("КБ", "МБ", "ГБ", "ТБ")
    var value = bytes.toDouble() / 1024
    var unit = 0
    while (value >= 1024 && unit < units.lastIndex) {
        value /= 1024
        unit++
    }
    return String.format(Locale.getDefault(), "%.1f %s", value, units[unit])
}
