package com.netknownsthat.ui

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.consumeWindowInsets
import androidx.compose.ui.unit.sp
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.LocalContentColor
import androidx.compose.foundation.text.TextAutoSize
import androidx.compose.foundation.text.BasicText
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.filled.List
import androidx.compose.material.icons.filled.Home
import androidx.compose.material.icons.filled.Info
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.filled.Notifications
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.adaptive.currentWindowAdaptiveInfo
import androidx.compose.material3.adaptive.navigationsuite.NavigationSuiteScaffold
import androidx.compose.material3.adaptive.navigationsuite.NavigationSuiteScaffoldDefaults
import androidx.compose.material3.adaptive.navigationsuite.NavigationSuiteType
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.text.style.TextOverflow
import androidx.navigation.NavDestination.Companion.hasRoute
import androidx.navigation.NavHostController
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import androidx.navigation.toRoute
import com.netknownsthat.domain.common.HostTarget
import com.netknownsthat.domain.common.JobOwner
import com.netknownsthat.domain.common.getOrNull
import com.netknownsthat.domain.repository.HostsRepository
import com.netknownsthat.domain.usecase.HostSectionKey
import com.netknownsthat.ui.common.ActionMessages
import com.netknownsthat.ui.common.Chrome
import com.netknownsthat.ui.common.LocalChrome
import com.netknownsthat.ui.common.ScreenActions
import com.netknownsthat.ui.common.SectionViewModel
import com.netknownsthat.ui.host.ConfigFileScreen
import com.netknownsthat.ui.host.ContainerScreen
import com.netknownsthat.ui.host.HostSectionScreen
import com.netknownsthat.ui.host.HostSectionsScreen
import com.netknownsthat.ui.hosts.HostListScreen
import com.netknownsthat.ui.hub.DeploymentsScreen
import com.netknownsthat.ui.hub.EventsScreen
import com.netknownsthat.ui.hub.FleetFail2banScreen
import com.netknownsthat.ui.hub.JobScreen
import com.netknownsthat.ui.hub.JobsScreen
import com.netknownsthat.ui.hub.MonitoringScreen
import com.netknownsthat.ui.hub.MoreScreen
import com.netknownsthat.ui.hub.PipelineScreen
import com.netknownsthat.ui.i18n.I18n
import com.netknownsthat.ui.i18n.t
import com.netknownsthat.ui.login.LoginScreen
import com.netknownsthat.ui.navigation.AboutRoute
import com.netknownsthat.ui.navigation.Breadcrumbs
import com.netknownsthat.ui.navigation.ConfigFileRoute
import com.netknownsthat.ui.navigation.ContainerRoute
import com.netknownsthat.ui.navigation.DeploymentsRoute
import com.netknownsthat.ui.navigation.EventsRoute
import com.netknownsthat.ui.navigation.FleetFail2banRoute
import com.netknownsthat.ui.navigation.HUB_JOBS
import com.netknownsthat.ui.navigation.HostRoute
import com.netknownsthat.ui.navigation.HostSectionRoute
import com.netknownsthat.ui.navigation.HostsRoute
import com.netknownsthat.ui.navigation.HubJobsRoute
import com.netknownsthat.ui.navigation.JobRoute
import com.netknownsthat.ui.navigation.LoginRoute
import com.netknownsthat.ui.navigation.MonitoringRoute
import com.netknownsthat.ui.navigation.MoreRoute
import com.netknownsthat.ui.navigation.PipelineRoute
import com.netknownsthat.ui.navigation.crumbOf
import com.netknownsthat.ui.session.AppViewModel
import com.netknownsthat.ui.session.Start
import com.netknownsthat.ui.theme.NktTheme
import com.netknownsthat.ui.BetaNotice
import com.netknownsthat.ui.about.AboutScreen
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.emptyFlow
import kotlinx.coroutines.launch
import org.koin.compose.koinInject
import org.koin.compose.viewmodel.koinViewModel
import org.koin.core.parameter.parametersOf

/** A jump to a host section from outside the UI — a tapped notification. */
data class HostJump(val hostId: Long, val path: String)

/** The whole app: Android's MainActivity and iOS's view controller show this. */
@Composable
fun NktApp(jumps: Flow<HostJump> = emptyFlow()) {
    NktTheme {
        Surface(modifier = Modifier.fillMaxSize()) {
            val app = koinViewModel<AppViewModel>()
            val chrome = remember { Chrome() }
            if (app.showBetaNotice) BetaNotice(onDismiss = app::dismissBetaNotice)
            CompositionLocalProvider(LocalChrome provides chrome) {
                when (app.start) {
                    Start.Loading -> Box(Modifier.fillMaxSize()) { CircularProgressIndicator(Modifier.align(Alignment.Center)) }
                    Start.SignIn -> Shell(app, chrome, LoginRoute, jumps)
                    Start.Hosts -> Shell(app, chrome, HostsRoute, jumps)
                }
            }
        }
    }
}

private data class TopItem(val route: Any, val label: () -> String, val icon: ImageVector, val matches: (androidx.navigation.NavDestination) -> Boolean)

private val TOP_ITEMS = listOf(
    TopItem(HostsRoute, { t("Хосты", "Hosts") }, Icons.Default.Home) { it.hasRoute<HostsRoute>() },
    TopItem(EventsRoute, { t("Оповещения", "Alerts") }, Icons.Default.Notifications) { it.hasRoute<EventsRoute>() },
    TopItem(MonitoringRoute, { t("Мониторинг", "Monitoring") }, Icons.Default.Info) { it.hasRoute<MonitoringRoute>() },
    TopItem(HubJobsRoute, { t("Задания", "Jobs") }, Icons.AutoMirrored.Filled.List) { it.hasRoute<HubJobsRoute>() },
    TopItem(MoreRoute, { t("Ещё", "More") }, Icons.Default.MoreVert) { it.hasRoute<MoreRoute>() },
)

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun Shell(app: AppViewModel, chrome: Chrome, start: Any, jumps: Flow<HostJump>) {
    val nav = rememberNavController()
    val stack by nav.currentBackStack.collectAsState()
    val current = stack.lastOrNull()
    val signedIn = current != null && !current.destination.hasRoute<LoginRoute>()
    // The top-level tab the current page belongs to: the last one in the stack.
    val selectedTop = stack.asReversed().firstNotNullOfOrNull { e -> TOP_ITEMS.firstOrNull { it.matches(e.destination) } }
    val hosts = koinInject<HostsRepository>()
    val scope = rememberCoroutineScope()

    val openHostAt: (Long, String) -> Unit = { hostId, path ->
        scope.launch {
            val name = hosts.hosts().getOrNull()?.firstOrNull { it.id == hostId }?.name ?: "#$hostId"
            nav.navigate(HostRoute(hostId, name))
            nav.navigate(HostSectionRoute(hostId, name, HostSectionKey.fromPath(path).name))
        }
    }
    LaunchedEffect(signedIn) {
        if (signedIn) jumps.collect { openHostAt(it.hostId, it.path) }
    }

    val layout = when {
        !signedIn || chrome.fullScreen -> NavigationSuiteType.None
        else -> NavigationSuiteScaffoldDefaults.calculateFromAdaptiveInfo(currentWindowAdaptiveInfo())
    }
    NavigationSuiteScaffold(
        layoutType = layout,
        navigationSuiteItems = {
            TOP_ITEMS.forEach { item ->
                item(
                    selected = selectedTop == item,
                    onClick = {
                        nav.navigate(item.route) {
                            popUpTo(HostsRoute) { saveState = true }
                            launchSingleTop = true
                            restoreState = true
                        }
                    },
                    icon = { Icon(item.icon, contentDescription = null) },
                    // One line, shrinking if needed: "Оповещения" broke into
                    // two lines on a phone-width bar.
                    label = {
                        BasicText(
                            item.label(),
                            maxLines = 1,
                            style = MaterialTheme.typography.labelMedium.copy(color = LocalContentColor.current),
                            autoSize = TextAutoSize.StepBased(minFontSize = 9.sp, maxFontSize = MaterialTheme.typography.labelMedium.fontSize),
                        )
                    },
                )
            }
        },
    ) {
        Scaffold(
            snackbarHost = { SnackbarHost(chrome.snackbar) },
            topBar = {
                if (signedIn && !chrome.fullScreen) {
                    Column {
                        TopAppBar(
                            title = { Text(current?.let { crumbOf(it) }.orEmpty(), maxLines = 1, overflow = TextOverflow.Ellipsis) },
                            navigationIcon = {
                                if (nav.previousBackStackEntry != null) {
                                    IconButton(onClick = { nav.popBackStack() }) {
                                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = t("Назад", "Back"))
                                    }
                                }
                            },
                            actions = {
                                chrome.actionEntries.forEach { it.content(this) }
                                // Background operations of the hub and every
                                // host — any job, wherever it was started.
                                com.netknownsthat.ui.hub.ActiveJobsButton(koinViewModel()) { a ->
                                    val hostId = if (a.hostId < 0) HUB_JOBS else a.hostId
                                    nav.navigate(JobRoute(hostId, a.hostName, a.job.id))
                                }
                            },
                        )
                        Breadcrumbs(nav)
                    }
                }
            },
        ) { padding ->
            // The app draws edge to edge, so the keyboard does not shrink the
            // window by itself: every screen — forms and the terminal alike —
            // is padded for it here.
            Box(Modifier.fillMaxSize().padding(padding).consumeWindowInsets(padding).imePadding()) {
                Routes(nav, app, start, openHostAt)
            }
        }
    }
}

/** A section screen's common duties: load on entry, refresh in the top
 * bar, action results in the snackbar. */
@Composable
fun <VM : SectionViewModel<*>> Section(viewModel: VM, content: @Composable (VM) -> Unit) {
    // Server texts (problems, errors, journals) come in the interface
    // language, so a language switch reloads them too.
    LaunchedEffect(viewModel, I18n.lang) { viewModel.load() }
    ActionMessages(viewModel)
    ScreenActions {
        IconButton(onClick = { viewModel.load() }) { Icon(Icons.Default.Refresh, contentDescription = t("Обновить", "Refresh")) }
    }
    content(viewModel)
}

@Composable
private fun Routes(nav: NavHostController, app: AppViewModel, start: Any, openHostAt: (Long, String) -> Unit) {
    val openHubJob: (Long) -> Unit = { id -> nav.navigate(JobRoute(HUB_JOBS, "", id)) }
    NavHost(navController = nav, startDestination = start) {
        composable<LoginRoute> {
            LoginScreen(
                viewModel = koinViewModel(),
                onLoggedIn = { app.signedIn() },
                onLanguage = app::setLanguage,
            )
        }
        composable<HostsRoute> {
            HostListScreen(
                viewModel = koinViewModel(),
                onOpenHost = { nav.navigate(HostRoute(it.id, it.name)) },
                onOpenJob = openHubJob,
            )
        }
        composable<EventsRoute> {
            Section(koinViewModel<com.netknownsthat.ui.hub.EventsViewModel>()) { vm ->
                EventsScreen(vm, app.notifyEnabled, app::setNotifications, openHostAt)
            }
        }
        composable<MonitoringRoute> {
            Section(koinViewModel<com.netknownsthat.ui.hub.MonitoringViewModel>()) { vm -> MonitoringScreen(vm, openHostAt) }
        }
        composable<HubJobsRoute> {
            Section(koinViewModel<com.netknownsthat.ui.hub.JobsViewModel> { parametersOf(JobOwner.Hub) }) { vm -> JobsScreen(vm, openHubJob) }
        }
        composable<MoreRoute> {
            MoreScreen(
                onFail2ban = { nav.navigate(FleetFail2banRoute) },
                onDeployments = { nav.navigate(DeploymentsRoute) },
                onAbout = { nav.navigate(AboutRoute) },
            )
        }
        composable<FleetFail2banRoute> {
            Section(koinViewModel<com.netknownsthat.ui.hub.FleetFail2banViewModel>()) { vm -> FleetFail2banScreen(vm, openHubJob) }
        }
        composable<DeploymentsRoute> {
            Section(koinViewModel<com.netknownsthat.ui.hub.DeploymentsViewModel>()) { vm ->
                DeploymentsScreen(vm, openHubJob) { p -> nav.navigate(PipelineRoute(p.id, p.name)) }
            }
        }
        composable<PipelineRoute> { entry ->
            val r = entry.toRoute<PipelineRoute>()
            Section(koinViewModel<com.netknownsthat.ui.hub.PipelineViewModel> { parametersOf(r.pipelineId) }) { vm -> PipelineScreen(vm, openHubJob) }
        }
        composable<AboutRoute> {
            AboutScreen(
                viewModel = koinViewModel(),
                onLanguage = app::setLanguage,
                onSignedOut = { app.signedOut() },
            )
        }
        composable<HostRoute> { entry ->
            val r = entry.toRoute<HostRoute>()
            HostSectionsScreen(
                hostName = r.hostName,
                reboot = koinViewModel { parametersOf(HostTarget(r.hostId)) },
                onOpen = { nav.navigate(HostSectionRoute(r.hostId, r.hostName, it.name)) },
            )
        }
        composable<HostSectionRoute> { entry ->
            val r = entry.toRoute<HostSectionRoute>()
            HostSectionScreen(
                host = HostTarget(r.hostId),
                section = HostSectionKey.valueOf(r.section),
                openSection = { nav.navigate(HostSectionRoute(r.hostId, r.hostName, it.name)) },
                openContainer = { nav.navigate(ContainerRoute(r.hostId, r.hostName, it)) },
                openConfigFile = { nav.navigate(ConfigFileRoute(r.hostId, r.hostName, it)) },
                openJob = { nav.navigate(JobRoute(r.hostId, r.hostName, it)) },
                openHostAt = openHostAt,
            )
        }
        composable<ContainerRoute> { entry ->
            val r = entry.toRoute<ContainerRoute>()
            ContainerScreen(r.container, koinViewModel { parametersOf(HostTarget(r.hostId)) })
        }
        composable<ConfigFileRoute> { entry ->
            val r = entry.toRoute<ConfigFileRoute>()
            ConfigFileScreen(koinViewModel { parametersOf(HostTarget(r.hostId)) }, r.path)
        }
        composable<JobRoute> { entry ->
            val r = entry.toRoute<JobRoute>()
            val owner = if (r.hostId == HUB_JOBS) JobOwner.Hub else JobOwner.Host(HostTarget(r.hostId))
            JobScreen(koinViewModel { parametersOf(owner, r.jobId) })
        }
    }
}
