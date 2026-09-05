package com.netknownsthat.app

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
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import com.netknownsthat.app.net.HubClient
import com.netknownsthat.app.ui.AppViewModelFactory
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
import com.netknownsthat.app.ui.login.AuthViewModel
import com.netknownsthat.app.ui.login.LoginScreen
import com.netknownsthat.app.ui.theme.NktTheme

private object Routes {
    const val LOGIN = "login"
    const val HOSTS = "hosts"
    const val ABOUT = "about"
    const val HOST = "host"
}

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
        )
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContent {
            NktTheme {
                Surface(modifier = Modifier.fillMaxSize()) {
                    NktApp(
                        hubClient = app.hubClient,
                        authViewModel = authViewModel,
                        hostListViewModel = hostListViewModel,
                        aboutViewModel = aboutViewModel,
                        hostViewModels = hostViewModels,
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
    hubClient: HubClient,
    authViewModel: AuthViewModel,
    hostListViewModel: HostListViewModel,
    aboutViewModel: AboutViewModel,
    hostViewModels: HostViewModels,
) {
    val navController = rememberNavController()
    var startDestination by remember { mutableStateOf<String?>(null) }

    LaunchedEffect(Unit) {
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
                onOpenHost = { navController.navigate(Routes.HOST) },
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
            )
        }
    }
}

