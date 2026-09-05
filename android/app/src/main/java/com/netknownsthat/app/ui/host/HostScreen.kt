package com.netknownsthat.app.ui.host

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
    TERMINAL("Терминал"),
    BTOP("Монитор (btop)"),
    SERVICES("Сервисы"),
    CONTAINERS("Контейнеры"),
    VULNERABILITIES("Уязвимости"),
    AVAILABILITY("Доступность"),
    USAGE("Нагрузка"),
    CONFIGS("Конфигурация"),
    FIREWALL("Firewall"),
    CERTIFICATES("Сертификаты"),
    INTERFACES("Интерфейсы"),
    MISC("Разное"),
    TOPOLOGY("Карта"),
    USERS("Пользователи"),
    AUDIT("Журнал"),
}

/** Every ViewModel the host screen's sections need, passed as one bundle so
 * adding a section does not mean threading another parameter through the
 * navigation graph. */
class HostViewModels(
    val overview: OverviewViewModel,
    val findings: FindingsViewModel,
    val interfaces: InterfacesViewModel,
    val audit: AuditViewModel,
    val services: ServicesViewModel,
    val containers: ContainersViewModel,
    val users: UsersViewModel,
    val misc: MiscViewModel,
    val vulnerabilities: VulnerabilitiesViewModel,
    val availability: AvailabilityViewModel,
    val usage: UsageViewModel,
    val configs: ConfigsViewModel,
    val firewall: FirewallViewModel,
    val certificates: CertificatesViewModel,
    val topology: TopologyViewModel,
    val terminal: TerminalViewModel,
) {
    /** Null for the live sections: a terminal has nothing to load or
     * refresh, so the generic fetch/refresh plumbing does not apply. */
    fun forSection(section: HostSection): SectionViewModel<*>? = when (section) {
        HostSection.TERMINAL, HostSection.BTOP -> null
        HostSection.OVERVIEW -> overview
        HostSection.FINDINGS -> findings
        HostSection.SERVICES -> services
        HostSection.CONTAINERS -> containers
        HostSection.VULNERABILITIES -> vulnerabilities
        HostSection.AVAILABILITY -> availability
        HostSection.USAGE -> usage
        HostSection.CONFIGS -> configs
        HostSection.FIREWALL -> firewall
        HostSection.CERTIFICATES -> certificates
        HostSection.INTERFACES -> interfaces
        HostSection.MISC -> misc
        HostSection.TOPOLOGY -> topology
        HostSection.USERS -> users
        HostSection.AUDIT -> audit
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun HostScreen(
    hostName: String,
    hostId: Long?,
    viewModels: HostViewModels,
    onBack: () -> Unit,
) {
    var section by remember { mutableStateOf(HostSection.OVERVIEW) }
    val drawerState = rememberDrawerState(DrawerValue.Closed)
    val scope = rememberCoroutineScope()
    val snackbarHostState = remember { SnackbarHostState() }
    val active = viewModels.forSection(section)

    // Keyed on the host too, not just the section: these ViewModels live as
    // long as the activity, so returning to the list and opening a different
    // host must refetch rather than show the previous host's data.
    LaunchedEffect(section, hostId) { active?.load() }

    // Action results surface as a snackbar wherever they happen, so no
    // screen has to grow its own reporting.
    LaunchedEffect(active?.actionMessage) {
        active?.actionMessage?.let {
            snackbarHostState.showSnackbar(it)
            active.actionMessage = null
        }
    }

    ModalNavigationDrawer(
        drawerState = drawerState,
        drawerContent = {
            ModalDrawerSheet {
                Text(
                    text = hostName,
                    style = androidx.compose.material3.MaterialTheme.typography.titleMedium,
                    modifier = Modifier.padding(16.dp),
                )
                // Scrollable: fifteen sections do not fit on a phone, and a
                // silently clipped list would hide the last few entirely.
                LazyColumn {
                    items(HostSection.entries) { entry ->
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
                            Icon(Icons.Default.Menu, contentDescription = "Разделы")
                        }
                    },
                    actions = {
                        if (active != null) {
                            IconButton(onClick = { active.load() }) {
                                Icon(Icons.Default.Refresh, contentDescription = "Обновить")
                            }
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
                    HostSection.OVERVIEW -> OverviewScreen(viewModels.overview)
                    HostSection.FINDINGS -> FindingsScreen(viewModels.findings)
                    HostSection.TERMINAL -> TerminalScreen(viewModels.terminal, btop = false)
                    HostSection.BTOP -> TerminalScreen(viewModels.terminal, btop = true)
                    HostSection.SERVICES -> ServicesScreen(viewModels.services)
                    HostSection.CONTAINERS -> ContainersScreen(viewModels.containers)
                    HostSection.VULNERABILITIES -> VulnerabilitiesScreen(viewModels.vulnerabilities)
                    HostSection.AVAILABILITY -> AvailabilityScreen(viewModels.availability)
                    HostSection.USAGE -> UsageScreen(viewModels.usage)
                    HostSection.CONFIGS -> ConfigsScreen(viewModels.configs)
                    HostSection.FIREWALL -> FirewallScreen(viewModels.firewall)
                    HostSection.CERTIFICATES -> CertificatesScreen(viewModels.certificates)
                    HostSection.INTERFACES -> InterfacesScreen(viewModels.interfaces)
                    HostSection.MISC -> MiscScreen(viewModels.misc)
                    HostSection.TOPOLOGY -> TopologyScreen(viewModels.topology)
                    HostSection.USERS -> UsersScreen(viewModels.users)
                    HostSection.AUDIT -> AuditScreen(viewModels.audit)
                }
            }
        }
    }
}
