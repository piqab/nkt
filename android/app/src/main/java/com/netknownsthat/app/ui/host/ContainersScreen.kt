package com.netknownsthat.app.ui.host

import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.material3.Card
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Tab
import androidx.compose.material3.TabRow
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.netknownsthat.app.net.model.Container

private val LIFECYCLE = listOf("start" to "Пуск", "stop" to "Стоп", "restart" to "Рестарт")

@Composable
fun ContainersScreen(viewModel: ContainersViewModel) {
    var tab by remember { mutableIntStateOf(0) }

    SectionContent(state = viewModel.state, emptyText = "Контейнеры не найдены") { data ->
        // Only tabs with something in them: a host running plain Docker
        // should not be offered three empty tabs for runtimes it lacks.
        val tabs = buildList {
            if (data.docker.containers.isNotEmpty()) add("Docker" to 0)
            if (data.podman.containers.isNotEmpty()) add("Podman" to 1)
            if (data.lxd.instances.isNotEmpty()) add("LXD" to 2)
            if (data.vms.vms.isNotEmpty()) add("ВМ" to 3)
        }
        if (tabs.isEmpty()) {
            Text(
                text = "Ни один контейнерный движок не найден",
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(24.dp),
            )
            return@SectionContent
        }
        val current = tabs.firstOrNull { it.second == tab } ?: tabs.first()

        Column {
            TabRow(selectedTabIndex = tabs.indexOf(current)) {
                tabs.forEach { (title, index) ->
                    Tab(
                        selected = current.second == index,
                        onClick = { tab = index },
                        text = { Text(title) },
                    )
                }
            }
            val enabled = !viewModel.actionInProgress
            when (current.second) {
                0 -> LazyColumn(contentPadding = PaddingValues(16.dp)) {
                    items(data.docker.containers, key = { it.id }) {
                        DockerCard(it, enabled) { action -> viewModel.dockerAction(it.name, action) }
                    }
                }

                1 -> LazyColumn(contentPadding = PaddingValues(16.dp)) {
                    items(data.podman.containers, key = { it.id }) {
                        SimpleRuntimeCard(it.name, it.image, it.status, enabled) { action ->
                            viewModel.podmanAction(it.name, action)
                        }
                    }
                }

                2 -> LazyColumn(contentPadding = PaddingValues(16.dp)) {
                    items(data.lxd.instances, key = { it.name }) {
                        SimpleRuntimeCard(
                            it.name,
                            "${it.type} · ${it.architecture}",
                            it.status + it.ipv4.joinToString("") { ip -> " · $ip" },
                            enabled,
                        ) { action -> viewModel.lxdAction(it.name, action) }
                    }
                }

                3 -> LazyColumn(contentPadding = PaddingValues(16.dp)) {
                    items(data.vms.vms, key = { it.name }) {
                        SimpleRuntimeCard(
                            it.name,
                            "${it.vcpus} vCPU · ${it.memoryKb / 1024} МБ",
                            it.state,
                            enabled,
                        ) { action -> viewModel.vmAction(it.name, action) }
                    }
                }
            }
        }
    }
}

@Composable
private fun DockerCard(container: Container, enabled: Boolean, onAction: (String) -> Unit) {
    Card(modifier = Modifier.fillMaxWidth().padding(bottom = 8.dp)) {
        Column(modifier = Modifier.padding(16.dp)) {
            Row(modifier = Modifier.fillMaxWidth()) {
                Text(
                    text = container.name,
                    style = MaterialTheme.typography.titleSmall,
                    modifier = Modifier.weight(1f),
                )
                Text(
                    text = container.state,
                    style = MaterialTheme.typography.labelMedium,
                    color = if (container.running) MaterialTheme.colorScheme.primary
                    else MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            Text(
                text = container.image,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(top = 4.dp),
            )
            if (container.status.isNotBlank()) {
                Text(text = container.status, style = MaterialTheme.typography.bodySmall)
            }
            if (container.ports.isNotEmpty()) {
                Text(
                    text = container.ports.joinToString(", ") {
                        "${it.hostPort}→${it.containerPort}/${it.protocol}"
                    },
                    style = MaterialTheme.typography.bodySmall,
                    modifier = Modifier.padding(top = 4.dp),
                )
            }
            if (!container.declared && container.project.isBlank()) {
                Text(
                    text = "Запущен вручную, не описан в compose",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.tertiary,
                    modifier = Modifier.padding(top = 4.dp),
                )
            }
            ActionRow(enabled, onAction)
        }
    }
}

@Composable
private fun SimpleRuntimeCard(
    name: String,
    subtitle: String,
    status: String,
    enabled: Boolean,
    onAction: (String) -> Unit,
) {
    Card(modifier = Modifier.fillMaxWidth().padding(bottom = 8.dp)) {
        Column(modifier = Modifier.padding(16.dp)) {
            Text(name, style = MaterialTheme.typography.titleSmall)
            Text(
                text = subtitle,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(top = 4.dp),
            )
            Text(status, style = MaterialTheme.typography.bodySmall)
            ActionRow(enabled, onAction)
        }
    }
}

@Composable
private fun ActionRow(enabled: Boolean, onAction: (String) -> Unit) {
    Row(
        modifier = Modifier
            .horizontalScroll(rememberScrollState())
            .padding(top = 12.dp),
    ) {
        LIFECYCLE.forEach { (action, label) ->
            OutlinedButton(
                onClick = { onAction(action) },
                enabled = enabled,
                modifier = Modifier.padding(end = 8.dp),
            ) { Text(label) }
        }
    }
}
