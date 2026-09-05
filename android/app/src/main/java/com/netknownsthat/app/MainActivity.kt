package com.netknownsthat.app

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.viewModels
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
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
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import com.netknownsthat.app.net.HubClient
import com.netknownsthat.app.ui.AppViewModelFactory
import com.netknownsthat.app.ui.about.AboutScreen
import com.netknownsthat.app.ui.about.AboutViewModel
import com.netknownsthat.app.ui.hosts.HostListScreen
import com.netknownsthat.app.ui.hosts.HostListViewModel
import com.netknownsthat.app.ui.login.AuthViewModel
import com.netknownsthat.app.ui.login.LoginScreen
import com.netknownsthat.app.ui.theme.NktTheme

private object Routes {
    const val LOGIN = "login"
    const val HOSTS = "hosts"
    const val ABOUT = "about"
    const val HOST_PLACEHOLDER = "host_placeholder"
}

class MainActivity : ComponentActivity() {
    private val app get() = application as NktApplication

    private val factory by lazy { AppViewModelFactory(app.hubClient) }
    private val authViewModel: AuthViewModel by viewModels { factory }
    private val hostListViewModel: HostListViewModel by viewModels { factory }
    private val aboutViewModel: AboutViewModel by viewModels { factory }

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
                onOpenHost = { navController.navigate(Routes.HOST_PLACEHOLDER) },
            )
        }
        composable(Routes.ABOUT) {
            AboutScreen(viewModel = aboutViewModel, onBack = { navController.popBackStack() })
        }
        composable(Routes.HOST_PLACEHOLDER) {
            HostPlaceholderScreen(onBack = { navController.popBackStack() })
        }
    }
}

/**
 * Stands in for the real per-host screens (Overview, Findings, ...) until
 * the plan's phase 2 builds them — proves the login → host-list →
 * host-scoped-call chain works end to end without pretending phase 2 is
 * already done.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun HostPlaceholderScreen(onBack: () -> Unit) {
    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Хост") },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Назад")
                    }
                },
            )
        },
    ) { padding ->
        Box(modifier = Modifier.fillMaxSize().padding(padding).padding(24.dp)) {
            Text(
                text = "Экраны хоста (Обзор, Проблемы, ...) появятся в фазе 2.",
                style = MaterialTheme.typography.bodyLarge,
                modifier = Modifier.align(Alignment.Center),
            )
        }
    }
}
