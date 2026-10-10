package com.netknownsthat.ui.hosts

import androidx.compose.material3.OutlinedButton
import com.netknownsthat.ui.host.screenNameOk
import com.netknownsthat.ui.host.screenProto
import com.netknownsthat.ui.common.OsIcon
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Info
import androidx.compose.material.icons.automirrored.filled.List
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.filled.Menu
import androidx.compose.material.icons.filled.Notifications
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.FloatingActionButton
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.TextButton
import androidx.compose.material3.Badge
import androidx.compose.material3.BadgedBox
import androidx.compose.material3.Card
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.netknownsthat.domain.model.HubHost
import com.netknownsthat.ui.i18n.t
import com.netknownsthat.ui.common.ScreenActions
import com.netknownsthat.ui.common.SnackbarMessage

/**
 * Same role as Hosts.tsx's host-picker screen — the landing page once
 * logged in. Phase 1: read-only list + open. See HostListViewModel's own
 * doc comment for what's deliberately deferred to a later phase.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun HostListScreen(
    viewModel: HostListViewModel,
    onOpenHost: (HubHost) -> Unit,
    onOpenJob: (Long) -> Unit,
    /** Экран машины через её хост: машина, имя хоста, протокол. */
    onOpenScreen: (HubHost, String, String) -> Unit,
) {
    val state = viewModel.uiState
    var adding by remember { mutableStateOf(false) }
    var groupsOpen by remember { mutableStateOf(false) }
    var deleting by remember { mutableStateOf<HubHost?>(null) }
    var regrouping by remember { mutableStateOf<HubHost?>(null) }
    var noNkt by remember { mutableStateOf<HubHost?>(null) }
    // Экран машины — через её хост (virsh на нём): работает и без nkt
    // внутри, у Windows-машины это единственный вход.
    val screenOf: (HubHost) -> (() -> Unit)? = { host ->
        val parent = state.hosts.firstOrNull { it.id == host.parentId }?.name.orEmpty()
        if (host.parentId != 0L && host.vmState == "running" && screenNameOk(host.name)) {
            screenProto(host.vmGraphics)?.let { proto -> { onOpenScreen(host, parent, proto) } }
        } else {
            null
        }
    }
    noNkt?.let { host ->
        val screen = screenOf(host)
        AlertDialog(
            onDismissRequest = { noNkt = null },
            title = { Text(t("На машине нет nkt", "No nkt on the machine")) },
            text = {
                Text(
                    t(
                        "На ${host.name} nkt не установлен — разделы и терминал недоступны. " +
                            if (screen != null) "Экран открывается через её хост." else "Экран — когда машина запущена.",
                        "nkt is not installed on ${host.name} — sections and the terminal are unavailable. " +
                            if (screen != null) "The screen opens through its host." else "The screen — once the machine is running.",
                    ),
                )
            },
            confirmButton = {
                if (screen != null) {
                    TextButton(onClick = { noNkt = null; screen() }) { Text(t("Экран", "Screen")) }
                }
            },
            dismissButton = { TextButton(onClick = { noNkt = null }) { Text(t("Закрыть", "Close")) } },
        )
    }
    val openJob: (Long) -> Unit = { id -> if (id > 0) onOpenJob(id) }

    SnackbarMessage(viewModel.message) { viewModel.message = null }
    if (adding) {
        AddHostDialog(viewModel.groups, onDismiss = { adding = false }) { form, install ->
            adding = false
            viewModel.addHost(form, install, openJob)
        }
    }
    viewModel.pendingKey?.let { key -> AuthorizedKeyDialog(key) { viewModel.pendingKey = null } }
    viewModel.foreignInstall?.let { (id, text) ->
        AlertDialog(
            onDismissRequest = { viewModel.foreignInstall = null },
            title = { Text(t("На хосте уже есть nkt", "nkt is already on the host")) },
            text = { Text(t("$text\n\nУстановить поверх?", "$text\n\nInstall over it?")) },
            confirmButton = {
                TextButton(onClick = {
                    viewModel.foreignInstall = null
                    viewModel.install(id, true, openJob)
                }) { Text(t("Установить поверх", "Install over it")) }
            },
            dismissButton = { TextButton(onClick = { viewModel.foreignInstall = null }) { Text(t("Отмена", "Cancel")) } },
        )
    }
    deleting?.let { h ->
        DeleteHostDialog(h, onDismiss = { deleting = null }) { purge ->
            deleting = null
            viewModel.delete(h, purge, openJob)
        }
    }
    regrouping?.let { h ->
        SetGroupDialog(h, viewModel.groups, onDismiss = { regrouping = null }) { g ->
            regrouping = null
            viewModel.setGroup(h, g)
        }
    }
    if (groupsOpen) {
        GroupsDialog(
            viewModel.groups,
            onDismiss = { groupsOpen = false },
            onCreate = viewModel::createGroup,
            onRename = viewModel::renameGroup,
            onDelete = viewModel::deleteGroup,
        )
    }

    // Loaded here rather than from the ViewModel's init: the ViewModel is
    // created during composition, before the hub URL has been restored, so an
    // eager fetch reported a configured hub as missing.
    //
    // Returning to this list also means leaving whatever host was open, so
    // the scope goes back to the hub — the same thing the web UI does when
    // its host view closes.
    LaunchedEffect(Unit) {
        viewModel.refresh()
        viewModel.loadGroups()
    }
    ScreenActions {
        IconButton(onClick = viewModel::refresh) {
            Icon(Icons.Default.Refresh, contentDescription = t("Обновить", "Refresh"))
        }
        IconButton(onClick = {
            viewModel.loadGroups()
            groupsOpen = true
        }) {
            Icon(Icons.AutoMirrored.Filled.List, contentDescription = t("Группы хостов", "Host groups"))
        }
    }

    Box(modifier = Modifier.fillMaxSize()) {
        run {
            when {
                state.loading && state.hosts.isEmpty() ->
                    CircularProgressIndicator(modifier = Modifier.align(Alignment.Center))

                state.error != null && state.hosts.isEmpty() ->
                    Text(
                        text = state.error,
                        color = MaterialTheme.colorScheme.error,
                        modifier = Modifier.align(Alignment.Center).padding(24.dp),
                    )

                // Grouped like the web list: named groups in order, hosts
                // without a group last (the hub's own machine first of all).
                else -> LazyColumn(contentPadding = PaddingValues(start = 16.dp, end = 16.dp, top = 8.dp, bottom = 88.dp)) {
                    val byGroup = state.hosts.groupBy { it.group }
                    val order = byGroup.keys.sortedWith(compareBy({ it.isBlank() }, { it }))
                    order.forEach { group ->
                        if (order.size > 1) {
                            item(key = "group:$group") {
                                Text(
                                    text = group.ifBlank { t("Без группы", "No group") },
                                    style = MaterialTheme.typography.titleSmall,
                                    color = MaterialTheme.colorScheme.primary,
                                    modifier = Modifier.padding(top = 8.dp, bottom = 4.dp),
                                )
                            }
                        }
                        items(byGroup.getValue(group), key = { it.id }) { host ->
                            HostRow(
                                host = host,
                                // Машина без своего nkt (Windows или не
                                // поставлен) — разделов нет, только экран.
                                onClick = {
                                    if (host.parentId != 0L && (host.status != "online" || host.osInfo?.id == "windows")) noNkt = host
                                    else onOpenHost(host)
                                },
                                onScreen = screenOf(host),
                                // The hub's own machine has no SSH install to
                                // manage and cannot be deleted.
                                menu = if (host.id == HubHost.LOCAL_HOST_ID) null else HostMenu(
                                    installLabel = when {
                                        host.status == "new" || host.shownVersion.isBlank() -> t("Установить nkt", "Install nkt")
                                        host.outdated -> t("Обновить nkt до ${host.hubVersion}", "Update nkt to ${host.hubVersion}")
                                        else -> t("Переустановить nkt", "Reinstall nkt")
                                    },
                                    onInstall = { viewModel.install(host.id, false, openJob) },
                                    onInstallLog = { viewModel.openInstallLog(host.id, openJob) },
                                    onGroup = { regrouping = host },
                                    onDelete = { deleting = host },
                                ),
                            )
                        }
                    }
                }
            }
        }
        FloatingActionButton(
            onClick = {
                viewModel.loadGroups()
                adding = true
            },
            modifier = Modifier.align(Alignment.BottomEnd).padding(16.dp),
        ) { Icon(Icons.Default.Add, contentDescription = t("Добавить хост", "Add host")) }
    }
}

/** Web's hosts.sudo* wording, shortened for a phone. */
private val SUDO_LABEL get() = mapOf(
    "root" to "sudo: root",
    "nopasswd" to t("sudo без пароля — опасно", "passwordless sudo — dangerous"),
    "password_required" to t("sudo с паролем", "sudo with password"),
    "narrow" to t("узкий sudo", "narrow sudo"),
)

/** Management actions of a host row (admin). */
private class HostMenu(
    val installLabel: String,
    val onInstall: () -> Unit,
    val onInstallLog: () -> Unit,
    val onGroup: () -> Unit,
    val onDelete: () -> Unit,
)

@Composable
private fun HostRow(host: HubHost, onClick: () -> Unit, onScreen: (() -> Unit)?, menu: HostMenu?) {
    var open by remember { mutableStateOf(false) }
    Card(
        onClick = onClick,
        modifier = Modifier
            .fillMaxWidth()
            .padding(bottom = 8.dp),
    ) {
        // Tight on purpose: a list of hosts is scanned, and a full-size
        // menu button made every card half empty.
        Column(modifier = Modifier.padding(start = 14.dp, end = if (menu != null) 4.dp else 14.dp, top = 8.dp, bottom = 10.dp)) {
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceBetween,
                verticalAlignment = Alignment.CenterVertically,
            ) {
                OsIcon(host.osInfo, size = 18.dp)
                Text(host.name, style = MaterialTheme.typography.titleMedium, modifier = Modifier.weight(1f))
                Text(host.status, style = MaterialTheme.typography.labelMedium)
                if (menu != null) {
                    Box {
                        IconButton(onClick = { open = true }, modifier = Modifier.size(36.dp)) {
                            Icon(Icons.Default.MoreVert, contentDescription = t("Действия с хостом", "Host actions"))
                        }
                        DropdownMenu(expanded = open, onDismissRequest = { open = false }) {
                            DropdownMenuItem(text = { Text(menu.installLabel) }, onClick = { open = false; menu.onInstall() })
                            DropdownMenuItem(text = { Text(t("Журнал установки", "Install log")) }, onClick = { open = false; menu.onInstallLog() })
                            DropdownMenuItem(text = { Text(t("Группа…", "Group…")) }, onClick = { open = false; menu.onGroup() })
                            DropdownMenuItem(text = { Text(t("Удалить…", "Delete…")) }, onClick = { open = false; menu.onDelete() })
                        }
                    }
                }
            }
            if (host.outdated) {
                Text(
                    text = t("nkt ${host.shownVersion} — хаб ${host.hubVersion}", "nkt ${host.shownVersion} — hub ${host.hubVersion}"),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.tertiary,
                )
            }
            Text(
                text = host.addr,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            val tags = listOfNotNull(
                host.group.ifBlank { null },
                host.k8sRole.ifBlank { null }?.let { "k8s $it" },
                SUDO_LABEL[host.sudoStatus.orEmpty()],
                if (host.installActive) t("идёт установка", "installing") else null,
            )
            if (tags.isNotEmpty()) {
                Text(
                    text = tags.joinToString(" · "),
                    style = MaterialTheme.typography.bodySmall,
                    // sudo without a password is the one worth a colour:
                    // whoever logs in as that user is root at once.
                    color = if (host.sudoStatus == "nopasswd") MaterialTheme.colorScheme.error
                    else MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            val findingsTotal = host.findings?.values?.sum() ?: 0
            if (findingsTotal > 0) {
                Text(
                    text = t("Проблем: $findingsTotal", "Problems: $findingsTotal"),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
            }
            if (host.reachable == false) {
                Text(
                    text = t("Недоступен", "Unreachable"),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
            }
            if (onScreen != null) {
                OutlinedButton(onClick = onScreen, modifier = Modifier.padding(top = 4.dp)) { Text(t("Экран", "Screen")) }
            }
        }
    }
}
