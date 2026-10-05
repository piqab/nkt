package com.netknownsthat.app

import android.content.Intent
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.viewModels
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Surface
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.platform.LocalContext
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import com.netknownsthat.app.net.HubClient
import com.netknownsthat.app.ui.AppViewModelFactory
import com.netknownsthat.app.ui.BetaNotice
import kotlinx.coroutines.launch
import com.netknownsthat.app.ui.about.AboutScreen
import com.netknownsthat.app.ui.about.AboutViewModel
import com.netknownsthat.app.ui.host.AuditViewModel
import com.netknownsthat.app.ui.host.AvailabilityViewModel
import com.netknownsthat.app.ui.host.CertificatesViewModel
import com.netknownsthat.app.ui.host.ConfigsViewModel
import com.netknownsthat.app.ui.host.ContainersViewModel
import com.netknownsthat.app.ui.host.FindingsViewModel
import com.netknownsthat.app.ui.host.FirewallViewModel
import com.netknownsthat.app.ui.host.HostScreen
import com.netknownsthat.app.ui.host.HostViewModels
import com.netknownsthat.app.ui.host.InterfacesViewModel
import com.netknownsthat.app.ui.host.LogsViewModel
import com.netknownsthat.app.ui.host.MiscViewModel
import com.netknownsthat.app.ui.host.OverviewViewModel
import com.netknownsthat.app.ui.host.ServicesViewModel
import com.netknownsthat.app.ui.host.TerminalViewModel
import com.netknownsthat.app.ui.host.TopologyViewModel
import com.netknownsthat.app.ui.host.UsageViewModel
import com.netknownsthat.app.ui.host.UsersViewModel
import com.netknownsthat.app.ui.host.VulnerabilitiesViewModel
import com.netknownsthat.app.ui.hosts.HostListScreen
import com.netknownsthat.app.ui.hosts.HostListViewModel
import com.netknownsthat.app.ui.host.HostSection
import com.netknownsthat.app.ui.hub.DeploymentsViewModel
import com.netknownsthat.app.ui.hub.EventsViewModel
import com.netknownsthat.app.ui.hub.EventsWorker
import com.netknownsthat.app.ui.hub.Fail2banViewModel
import com.netknownsthat.app.ui.hub.FleetFail2banViewModel
import com.netknownsthat.app.ui.hub.HostJobsViewModel
import com.netknownsthat.app.ui.hub.HubJobsViewModel
import com.netknownsthat.app.ui.hub.HubScreen
import com.netknownsthat.app.ui.hub.HubSection
import com.netknownsthat.app.ui.hub.HubViewModels
import com.netknownsthat.app.ui.hub.JobLogViewModel
import com.netknownsthat.app.ui.hub.MonitoringViewModel
import com.netknownsthat.app.ui.hub.RebootViewModel
import com.netknownsthat.app.ui.login.AuthViewModel
import com.netknownsthat.app.ui.login.LoginScreen
import com.netknownsthat.app.ui.theme.NktTheme

private object Routes {
    const val LOGIN = "login"
    const val HOSTS = "hosts"
    const val ABOUT = "about"
    const val HOST = "host"
    const val HUB = "hub"
}

/** A jump to a host section requested from outside the UI — a tapped
 * notification. */
private data class HostJump(val hostId: Long, val path: String)

class MainActivity : ComponentActivity() {
    private val app get() = application as NktApplication

    private val factory by lazy { AppViewModelFactory(app.hubClient) }
    private val authViewModel: AuthViewModel by viewModels { factory }
    private val hostListViewModel: HostListViewModel by viewModels { factory }
    private val aboutViewModel: AboutViewModel by viewModels { factory }
    private val overviewViewModel: OverviewViewModel by viewModels { factory }
    private val findingsViewModel: FindingsViewModel by viewModels { factory }
    private val interfacesViewModel: InterfacesViewModel by viewModels { factory }
    private val auditViewModel: AuditViewModel by viewModels { factory }
    private val servicesViewModel: ServicesViewModel by viewModels { factory }
    private val containersViewModel: ContainersViewModel by viewModels { factory }
    private val usersViewModel: UsersViewModel by viewModels { factory }
    private val miscViewModel: MiscViewModel by viewModels { factory }
    private val vulnerabilitiesViewModel: VulnerabilitiesViewModel by viewModels { factory }
    private val availabilityViewModel: AvailabilityViewModel by viewModels { factory }
    private val usageViewModel: UsageViewModel by viewModels { factory }
    private val configsViewModel: ConfigsViewModel by viewModels { factory }
    private val firewallViewModel: FirewallViewModel by viewModels { factory }
    private val certificatesViewModel: CertificatesViewModel by viewModels { factory }
    private val topologyViewModel: TopologyViewModel by viewModels { factory }
    private val terminalViewModel: TerminalViewModel by viewModels { factory }
    private val logsViewModel: LogsViewModel by viewModels { factory }
    private val hostJobsViewModel: HostJobsViewModel by viewModels { factory }
    private val hubJobsViewModel: HubJobsViewModel by viewModels { factory }
    private val jobLogViewModel: JobLogViewModel by viewModels { factory }
    private val eventsViewModel: EventsViewModel by viewModels { factory }
    private val fail2banViewModel: Fail2banViewModel by viewModels { factory }
    private val fleetFail2banViewModel: FleetFail2banViewModel by viewModels { factory }
    private val monitoringViewModel: MonitoringViewModel by viewModels { factory }
    private val deploymentsViewModel: DeploymentsViewModel by viewModels { factory }
    private val rebootViewModel: RebootViewModel by viewModels { factory }

    private val hubViewModels by lazy {
        HubViewModels(
            events = eventsViewModel,
            jobs = hubJobsViewModel,
            monitoring = monitoringViewModel,
            fail2ban = fleetFail2banViewModel,
            deployments = deploymentsViewModel,
            jobLog = jobLogViewModel,
        )
    }

    private var pendingJump by mutableStateOf<HostJump?>(null)

    private fun takeJump(intent: Intent?) {
        val id = intent?.getLongExtra(EventsWorker.EXTRA_HOST_ID, 0L) ?: 0L
        if (id != 0L) pendingJump = HostJump(id, intent?.getStringExtra(EventsWorker.EXTRA_PATH) ?: "/")
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        takeJump(intent)
    }

    private val hostViewModels by lazy {
        HostViewModels(
            overview = overviewViewModel,
            findings = findingsViewModel,
            interfaces = interfacesViewModel,
            audit = auditViewModel,
            services = servicesViewModel,
            containers = containersViewModel,
            users = usersViewModel,
            misc = miscViewModel,
            vulnerabilities = vulnerabilitiesViewModel,
            availability = availabilityViewModel,
            usage = usageViewModel,
            configs = configsViewModel,
            firewall = firewallViewModel,
            certificates = certificatesViewModel,
            topology = topologyViewModel,
            terminal = terminalViewModel,
            logs = logsViewModel,
            jobs = hostJobsViewModel,
            fail2ban = fail2banViewModel,
            jobLog = jobLogViewModel,
            reboot = rebootViewModel,
        )
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        if (savedInstanceState == null) takeJump(intent)
        setContent {
            NktTheme {
                Surface(modifier = Modifier.fillMaxSize()) {
                    NktApp(
                        settingsStore = app.settingsStore,
                        hubClient = app.hubClient,
                        authViewModel = authViewModel,
                        hostListViewModel = hostListViewModel,
                        aboutViewModel = aboutViewModel,
                        hostViewModels = hostViewModels,
                        hubViewModels = hubViewModels,
                        pendingJump = pendingJump,
                        onJumpTaken = { pendingJump = null },
                    )
                }
            }
        }
    }
}

/**
 * Decides the start destination by trying to restore a previous session
 * (persisted hub URL + cookie, see HubClient.bootstrap/PersistentCookieJar)
 * before showing anything — a relaunch should find the same session a
 * browser tab reopened later would, not force a fresh login every time.
 */
@Composable
private fun NktApp(
    settingsStore: com.netknownsthat.app.data.SettingsStore,
    hubClient: HubClient,
    authViewModel: AuthViewModel,
    hostListViewModel: HostListViewModel,
    aboutViewModel: AboutViewModel,
    hostViewModels: HostViewModels,
    hubViewModels: HubViewModels,
    pendingJump: HostJump?,
    onJumpTaken: () -> Unit,
) {
    val context = LocalContext.current
    var hubSection by rememberSaveable { mutableStateOf(HubSection.EVENTS) }
    var hostSection by remember { mutableStateOf(HostSection.OVERVIEW) }
    var notifyEnabled by remember { mutableStateOf(false) }
    val navController = rememberNavController()
    var startDestination by remember { mutableStateOf<String?>(null) }
    var showBetaNotice by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()

    if (showBetaNotice) {
        BetaNotice(onDismiss = { dontShowAgain ->
            showBetaNotice = false
            if (dontShowAgain) scope.launch { settingsStore.hideBetaNotice() }
        })
    }

    LaunchedEffect(Unit) {
        showBetaNotice = !settingsStore.betaNoticeHidden()
        notifyEnabled = settingsStore.eventsNotify()
        val hasHub = hubClient.bootstrap()
        startDestination = if (hasHub && hubClient.me() is HubClient.ApiResult.Success) {
            Routes.HOSTS
        } else {
            Routes.LOGIN
        }
    }

    val resolvedStart = startDestination
    if (resolvedStart == null) {
        Box(modifier = Modifier.fillMaxSize()) {
            CircularProgressIndicator(modifier = Modifier.align(Alignment.Center))
        }
        return
    }

    // Opens a host at the section an alert/insight/notification points to.
    val openHostAt: (Long, String) -> Unit = { hostId, path ->
        scope.launch {
            if (hostListViewModel.selectById(hostId) != null) {
                hostSection = HostSection.fromPath(path)
                navController.navigate(Routes.HOST)
            }
        }
    }
    LaunchedEffect(pendingJump, resolvedStart) {
        val jump = pendingJump ?: return@LaunchedEffect
        if (resolvedStart != Routes.HOSTS) return@LaunchedEffect
        onJumpTaken()
        openHostAt(jump.hostId, jump.path)
    }

    NavHost(navController = navController, startDestination = resolvedStart) {
        composable(Routes.LOGIN) {
            LoginScreen(
                viewModel = authViewModel,
                onLoggedIn = {
                    navController.navigate(Routes.HOSTS) {
                        popUpTo(Routes.LOGIN) { inclusive = true }
                    }
                },
            )
        }
        composable(Routes.HOSTS) {
            HostListScreen(
                viewModel = hostListViewModel,
                onOpenAbout = { navController.navigate(Routes.ABOUT) },
                onOpenHost = {
                    hostSection = HostSection.OVERVIEW
                    navController.navigate(Routes.HOST)
                },
                onOpenHub = { navController.navigate(Routes.HUB) },
                onOpenEvents = {
                    hubSection = HubSection.EVENTS
                    navController.navigate(Routes.HUB)
                },
            )
        }
        composable(Routes.HUB) {
            // Hub sections talk to the hub, never to the host opened last —
            // and this runs again on returning here from a host.
            LaunchedEffect(Unit) { hostListViewModel.deselectHost() }
            HubScreen(
                section = hubSection,
                onSectionChange = { hubSection = it },
                viewModels = hubViewModels,
                notifyEnabled = notifyEnabled,
                onNotifyChange = { on ->
                    notifyEnabled = on
                    scope.launch { settingsStore.setEventsNotify(on) }
                    EventsWorker.schedule(context, on)
                },
                onOpenHost = openHostAt,
                onBack = { navController.popBackStack() },
            )
        }
        composable(Routes.ABOUT) {
            AboutScreen(viewModel = aboutViewModel, onBack = { navController.popBackStack() })
        }
        composable(Routes.HOST) {
            val host = hostListViewModel.selectedHost
            HostScreen(
                hostName = host?.name ?: "Хост",
                hostId = host?.id,
                viewModels = hostViewModels,
                onBack = { navController.popBackStack() },
                initialSection = hostSection,
            )
        }
    }
}

