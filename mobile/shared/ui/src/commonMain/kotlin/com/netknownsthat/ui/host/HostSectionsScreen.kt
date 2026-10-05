package com.netknownsthat.ui.host

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.GridItemSpan
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material3.Card
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.netknownsthat.domain.usecase.HostSectionKey
import com.netknownsthat.domain.usecase.HostSectionKey.*
import com.netknownsthat.ui.common.ScreenActions
import com.netknownsthat.ui.common.SnackbarMessage
import com.netknownsthat.ui.hub.RebootViewModel
import com.netknownsthat.ui.i18n.t
import com.netknownsthat.ui.navigation.sectionTitle

/** Sections grouped the way the web UI's menu groups them. */
private val SECTION_GROUPS: List<Pair<String, List<HostSectionKey>>>
    get() = listOf(
        t("Состояние", "State") to listOf(OVERVIEW, FINDINGS, AUDIT),
        t("Консоль", "Console") to listOf(TERMINAL, BTOP, LOGS),
        t("Сервисы и контейнеры", "Services and containers") to listOf(SERVICES, CONTAINERS, JOBS),
        t("Безопасность", "Security") to listOf(VULNERABILITIES, MALWARE, FIREWALL, FAIL2BAN, CERTIFICATES),
        t("Наблюдение", "Monitoring") to listOf(AVAILABILITY, USAGE, TOPOLOGY),
        t("Система", "System") to listOf(CONFIGS, INTERFACES, USERS, MISC),
    )

/**
 * A host: its sections as a grid (two or more columns on wider screens).
 * Each opens its own page; the crumbs and Back lead here again.
 */
@Composable
fun HostSectionsScreen(hostName: String, reboot: RebootViewModel, onOpen: (HostSectionKey) -> Unit) {
    var menu by remember { mutableStateOf(false) }
    var rebootOpen by remember { mutableStateOf(false) }
    if (rebootOpen) RebootDialog(hostName, reboot, onDismiss = { rebootOpen = false })
    SnackbarMessage(reboot.message) { reboot.message = null }
    ScreenActions {
        IconButton(onClick = { menu = true }) { Icon(Icons.Default.MoreVert, contentDescription = t("Ещё", "More")) }
        DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
            DropdownMenuItem(text = { Text(t("Перезагрузить хост…", "Reboot host…")) }, onClick = { menu = false; rebootOpen = true })
        }
    }
    LazyVerticalGrid(columns = GridCells.Adaptive(150.dp), contentPadding = PaddingValues(12.dp)) {
        SECTION_GROUPS.forEach { (group, sections) ->
            item(span = { GridItemSpan(maxLineSpan) }) {
                Text(
                    group,
                    style = MaterialTheme.typography.titleSmall,
                    color = MaterialTheme.colorScheme.primary,
                    modifier = Modifier.padding(start = 4.dp, top = 12.dp, bottom = 4.dp),
                )
            }
            items(sections, key = { it.name }) { s ->
                Card(onClick = { onOpen(s) }, modifier = Modifier.fillMaxWidth().padding(4.dp)) {
                    Column(modifier = Modifier.padding(horizontal = 14.dp, vertical = 16.dp)) {
                        Text(sectionTitle(s), style = MaterialTheme.typography.titleSmall)
                    }
                }
            }
        }
    }
}
