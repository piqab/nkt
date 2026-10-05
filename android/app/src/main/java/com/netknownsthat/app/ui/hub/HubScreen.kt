package com.netknownsthat.app.ui.hub

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Menu
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.DrawerValue
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalDrawerSheet
import androidx.compose.material3.ModalNavigationDrawer
import androidx.compose.material3.NavigationDrawerItem
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.rememberDrawerState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.netknownsthat.app.ui.host.SectionViewModel
import kotlinx.coroutines.launch

/** Hub-level sections — the ones worth having in a pocket. */
enum class HubSection(val title: String) {
    EVENTS("Оповещения"),
    JOBS("Задания хаба"),
    MONITORING("Мониторинг"),
    FAIL2BAN("Fail2ban"),
    DEPLOYMENTS("Выкладка"),
}

class HubViewModels(
    val events: EventsViewModel,
    val jobs: HubJobsViewModel,
    val monitoring: MonitoringViewModel,
    val fail2ban: FleetFail2banViewModel,
    val deployments: DeploymentsViewModel,
    val jobLog: JobLogViewModel,
) {
    fun forSection(section: HubSection): SectionViewModel<*> = when (section) {
        HubSection.EVENTS -> events
        HubSection.JOBS -> jobs
        HubSection.MONITORING -> monitoring
        HubSection.FAIL2BAN -> fail2ban
        HubSection.DEPLOYMENTS -> deployments
    }
}

/**
 * Hub sections behind one drawer, like HostScreen does for a host. The
 * section is owned by the caller so that the host list's bell can open
 * straight onto the alerts.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun HubScreen(
    section: HubSection,
    onSectionChange: (HubSection) -> Unit,
    viewModels: HubViewModels,
    notifyEnabled: Boolean,
    onNotifyChange: (Boolean) -> Unit,
    onOpenHost: (hostId: Long, path: String) -> Unit,
    onBack: () -> Unit,
) {
    val drawerState = rememberDrawerState(DrawerValue.Closed)
    val scope = rememberCoroutineScope()
    val snackbarHostState = remember { SnackbarHostState() }
    val active = viewModels.forSection(section)

    LaunchedEffect(section) { active.load() }
    LaunchedEffect(active.actionMessage) {
        active.actionMessage?.let {
            snackbarHostState.showSnackbar(it)
            active.actionMessage = null
        }
    }
    JobLogDialog(viewModels.jobLog)

    ModalNavigationDrawer(
        drawerState = drawerState,
        drawerContent = {
            ModalDrawerSheet {
                Text("Хаб", style = MaterialTheme.typography.titleMedium, modifier = Modifier.padding(16.dp))
                LazyColumn {
                    items(HubSection.entries) { entry ->
                        NavigationDrawerItem(
                            label = { Text(entry.title) },
                            selected = entry == section,
                            onClick = {
                                onSectionChange(entry)
                                scope.launch { drawerState.close() }
                            },
                            modifier = Modifier.padding(horizontal = 12.dp),
                        )
                    }
                }
            }
        },
    ) {
        Scaffold(
            snackbarHost = { SnackbarHost(snackbarHostState) },
            topBar = {
                TopAppBar(
                    title = { Text(section.title) },
                    navigationIcon = {
                        IconButton(onClick = { scope.launch { drawerState.open() } }) {
                            Icon(Icons.Default.Menu, contentDescription = "Разделы хаба")
                        }
                    },
                    actions = {
                        IconButton(onClick = { active.load() }) {
                            Icon(Icons.Default.Refresh, contentDescription = "Обновить")
                        }
                        IconButton(onClick = onBack) {
                            Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "К списку хостов")
                        }
                    },
                )
            },
        ) { padding ->
            Box(modifier = Modifier.fillMaxSize().padding(padding)) {
                when (section) {
                    HubSection.EVENTS -> EventsScreen(viewModels.events, notifyEnabled, onNotifyChange, onOpenHost)
                    HubSection.JOBS -> JobsScreen(viewModels.jobs, viewModels.jobLog)
                    HubSection.MONITORING -> MonitoringScreen(viewModels.monitoring, onOpenHost)
                    HubSection.FAIL2BAN -> FleetFail2banScreen(viewModels.fail2ban, viewModels.jobLog)
                    HubSection.DEPLOYMENTS -> DeploymentsScreen(viewModels.deployments, viewModels.jobLog)
                }
            }
        }
    }
}
