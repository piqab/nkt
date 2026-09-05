package com.netknownsthat.app.ui.hosts

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Info
import androidx.compose.material.icons.filled.Refresh
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
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.netknownsthat.app.net.model.HubHost

/**
 * Same role as Hosts.tsx's host-picker screen — the landing page once
 * logged in. Phase 1: read-only list + open. See HostListViewModel's own
 * doc comment for what's deliberately deferred to a later phase.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun HostListScreen(
    viewModel: HostListViewModel,
    onOpenAbout: () -> Unit,
    onOpenHost: (HubHost) -> Unit,
) {
    val state = viewModel.uiState

    // Loaded here rather than from the ViewModel's init: the ViewModel is
    // created during composition, before the hub URL has been restored, so an
    // eager fetch reported a configured hub as missing.
    //
    // Returning to this list also means leaving whatever host was open, so
    // the scope goes back to the hub — the same thing the web UI does when
    // its host view closes.
    LaunchedEffect(Unit) {
        viewModel.deselectHost()
        viewModel.refresh()
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Хосты") },
                actions = {
                    IconButton(onClick = viewModel::refresh) {
                        Icon(Icons.Default.Refresh, contentDescription = "Обновить")
                    }
                    IconButton(onClick = onOpenAbout) {
                        Icon(Icons.Default.Info, contentDescription = "О системе")
                    }
                },
            )
        },
    ) { padding ->
        Box(modifier = Modifier.fillMaxSize().padding(padding)) {
            when {
                state.loading && state.hosts.isEmpty() ->
                    CircularProgressIndicator(modifier = Modifier.align(Alignment.Center))

                state.error != null && state.hosts.isEmpty() ->
                    Text(
                        text = state.error,
                        color = MaterialTheme.colorScheme.error,
                        modifier = Modifier.align(Alignment.Center).padding(24.dp),
                    )

                else -> LazyColumn(contentPadding = PaddingValues(16.dp)) {
                    items(state.hosts, key = { it.id }) { host ->
                        HostRow(host = host, onClick = {
                            viewModel.select(host)
                            onOpenHost(host)
                        })
                    }
                }
            }
        }
    }
}

@Composable
private fun HostRow(host: HubHost, onClick: () -> Unit) {
    Card(
        onClick = onClick,
        modifier = Modifier
            .fillMaxWidth()
            .padding(bottom = 8.dp),
    ) {
        Column(modifier = Modifier.padding(16.dp)) {
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceBetween,
            ) {
                Text(host.name, style = MaterialTheme.typography.titleMedium)
                Text(host.status, style = MaterialTheme.typography.labelMedium)
            }
            Text(
                text = host.addr,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            val findingsTotal = host.findings?.values?.sum() ?: 0
            if (findingsTotal > 0) {
                Text(
                    text = "Проблем: $findingsTotal",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
            }
            if (host.reachable == false) {
                Text(
                    text = "Недоступен",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
            }
        }
    }
}
