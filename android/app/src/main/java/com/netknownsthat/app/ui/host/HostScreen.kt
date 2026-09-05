package com.netknownsthat.app.ui.host

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Menu
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.DrawerValue
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ModalDrawerSheet
import androidx.compose.material3.ModalNavigationDrawer
import androidx.compose.material3.NavigationDrawerItem
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.rememberDrawerState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.launch

/**
 * Sections of a single host. A drawer rather than a bottom bar on purpose:
 * the web UI's sidebar eventually reaches ~18 entries (the plan's phases 3-8
 * fill in the rest), which a bottom bar cannot hold, and adding one here
 * later would mean rebuilding the navigation instead of extending this list.
 */
enum class HostSection(val title: String) {
    OVERVIEW("Обзор"),
    FINDINGS("Проблемы"),
    INTERFACES("Интерфейсы"),
    AUDIT("Журнал"),
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun HostScreen(
    hostName: String,
    hostId: Long?,
    overviewViewModel: OverviewViewModel,
    findingsViewModel: FindingsViewModel,
    interfacesViewModel: InterfacesViewModel,
    auditViewModel: AuditViewModel,
    onBack: () -> Unit,
) {
    var section by remember { mutableStateOf(HostSection.OVERVIEW) }
    val drawerState = rememberDrawerState(DrawerValue.Closed)
    val scope = rememberCoroutineScope()

    fun reload() = when (section) {
        HostSection.OVERVIEW -> overviewViewModel.load()
        HostSection.FINDINGS -> findingsViewModel.load()
        HostSection.INTERFACES -> interfacesViewModel.load()
        HostSection.AUDIT -> auditViewModel.load()
    }

    // Keyed on the host too, not just the section: these ViewModels live as
    // long as the activity, so returning to the list and opening a different
    // host must refetch rather than show the previous host's data.
    LaunchedEffect(section, hostId) { reload() }

    ModalNavigationDrawer(
        drawerState = drawerState,
        drawerContent = {
            ModalDrawerSheet {
                Text(
                    text = hostName,
                    style = androidx.compose.material3.MaterialTheme.typography.titleMedium,
                    modifier = Modifier.padding(16.dp),
                )
                HostSection.entries.forEach { entry ->
                    NavigationDrawerItem(
                        label = { Text(entry.title) },
                        selected = entry == section,
                        onClick = {
                            section = entry
                            scope.launch { drawerState.close() }
                        },
                        modifier = Modifier.padding(horizontal = 12.dp),
                    )
                }
            }
        },
    ) {
        Scaffold(
            topBar = {
                TopAppBar(
                    title = { Text(section.title) },
                    navigationIcon = {
                        IconButton(onClick = { scope.launch { drawerState.open() } }) {
                            Icon(Icons.Default.Menu, contentDescription = "Разделы")
                        }
                    },
                    actions = {
                        IconButton(onClick = { reload() }) {
                            Icon(Icons.Default.Refresh, contentDescription = "Обновить")
                        }
                        IconButton(onClick = onBack) {
                            Icon(
                                Icons.AutoMirrored.Filled.ArrowBack,
                                contentDescription = "К списку хостов",
                            )
                        }
                    },
                )
            },
        ) { padding ->
            Box(modifier = Modifier.fillMaxSize().padding(padding)) {
                when (section) {
                    HostSection.OVERVIEW -> OverviewScreen(overviewViewModel)
                    HostSection.FINDINGS -> FindingsScreen(findingsViewModel)
                    HostSection.INTERFACES -> InterfacesScreen(interfacesViewModel)
                    HostSection.AUDIT -> AuditScreen(auditViewModel)
                }
            }
        }
    }
}
