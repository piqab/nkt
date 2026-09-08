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
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Card
import androidx.compose.material3.Checkbox
import androidx.compose.material3.FilterChip
import androidx.compose.material3.TextButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Tab
import androidx.compose.material3.TabRow
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.netknownsthat.app.net.model.Container
import com.netknownsthat.app.net.model.DockerImage
import com.netknownsthat.app.status.containerHealth
import com.netknownsthat.app.status.HealthStatus
import com.netknownsthat.app.status.instanceHealth
import com.netknownsthat.app.ui.theme.StatusDot
import com.netknownsthat.app.ui.theme.statusColor

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
            if (data.images.images.isNotEmpty()) add("Образы" to 4)
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
                        DockerCard(it, enabled, viewModel.pendingKey == it.name) { action ->
                            viewModel.dockerAction(it.name, action)
                        }
                    }
                }

                1 -> LazyColumn(contentPadding = PaddingValues(16.dp)) {
                    items(data.podman.containers, key = { it.id }) {
                        SimpleRuntimeCard(
                            it.name, it.image, it.status,
                            containerHealth(it.state), enabled,
                            viewModel.pendingKey == it.name,
                        ) { action -> viewModel.podmanAction(it.name, action) }
                    }
                }

                2 -> LazyColumn(contentPadding = PaddingValues(16.dp)) {
                    items(data.lxd.instances, key = { it.name }) {
                        SimpleRuntimeCard(
                            it.name,
                            "${it.type} · ${it.architecture}",
                            it.status + it.ipv4.joinToString("") { ip -> " · $ip" },
                            instanceHealth(it.status), enabled,
                            viewModel.pendingKey == it.name,
                        ) { action -> viewModel.lxdAction(it.name, action) }
                    }
                }

                4 -> ImagesTab(viewModel, data)

                3 -> LazyColumn(contentPadding = PaddingValues(16.dp)) {
                    items(data.vms.vms, key = { it.name }) {
                        SimpleRuntimeCard(
                            it.name,
                            "${it.vcpus} vCPU · ${it.memoryKb / 1024} МБ",
                            it.state,
                            instanceHealth(it.state), enabled,
                            viewModel.pendingKey == it.name,
                        ) { action -> viewModel.vmAction(it.name, action) }
                    }
                }
            }
        }
    }
}

@Composable
private fun DockerCard(
    container: Container,
    enabled: Boolean,
    busy: Boolean,
    onAction: (String) -> Unit,
) {
    Card(modifier = Modifier.fillMaxWidth().padding(bottom = 8.dp)) {
        Column(modifier = Modifier.padding(16.dp)) {
            Row(
                modifier = Modifier.fillMaxWidth(),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                StatusDot(containerHealth(container.state), busy = busy)
                Text(
                    text = container.name,
                    style = MaterialTheme.typography.titleSmall,
                    modifier = Modifier.weight(1f),
                )
                Text(
                    text = if (busy) "…" else container.state,
                    style = MaterialTheme.typography.labelMedium,
                    color = statusColor(containerHealth(container.state)),
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
    health: com.netknownsthat.app.status.HealthStatus,
    enabled: Boolean,
    busy: Boolean,
    onAction: (String) -> Unit,
) {
    Card(modifier = Modifier.fillMaxWidth().padding(bottom = 8.dp)) {
        Column(modifier = Modifier.padding(16.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                StatusDot(health, busy = busy)
                Text(name, style = MaterialTheme.typography.titleSmall)
            }
            Text(
                text = subtitle,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(top = 4.dp),
            )
            Text(
                text = if (busy) "…" else status,
                style = MaterialTheme.typography.bodySmall,
                color = statusColor(health),
            )
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

/**
 * Docker images with a multi-select. The two things worth doing to several
 * at once are removing them and saving them to a tar on the host; an image a
 * container is running from is marked, because Docker refuses to remove one
 * without force and saying so first beats offering an action that fails.
 */
@Composable
private fun ImagesTab(viewModel: ContainersViewModel, data: ContainerRuntimes) {
    var picked by remember { mutableStateOf(setOf<String>()) }
    var force by remember { mutableStateOf(false) }
    var confirmRemove by remember { mutableStateOf(false) }

    // Docker takes a tag or an id; the tag is what an operator recognises.
    fun refOf(image: DockerImage) = image.tags.firstOrNull() ?: image.id
    val selected = data.images.images.filter { picked.contains(it.id) }
    val inUseSelected = selected.count { it.inUse }

    if (confirmRemove) {
        AlertDialog(
            onDismissRequest = { confirmRemove = false },
            title = { Text("Удалить выбранные образы?") },
            text = {
                Text(
                    if (inUseSelected > 0 && !force)
                        "Из выбранных $inUseSelected используются запущенными контейнерами — " +
                            "Docker откажется их удалять. Включите «принудительно», если это осознанно."
                    else "Будет удалено образов: ${selected.size}."
                )
            },
            confirmButton = {
                TextButton(onClick = {
                    viewModel.removeImages(selected.map(::refOf), force)
                    picked = emptySet()
                    confirmRemove = false
                }) { Text("Удалить") }
            },
            dismissButton = { TextButton(onClick = { confirmRemove = false }) { Text("Отмена") } },
        )
    }

    Column {
        Row(
            modifier = Modifier
                .horizontalScroll(rememberScrollState())
                .padding(horizontal = 16.dp, vertical = 8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            FilterChip(
                selected = force,
                onClick = { force = !force },
                label = { Text("принудительно") },
                modifier = Modifier.padding(end = 8.dp),
            )
            OutlinedButton(
                onClick = { viewModel.saveImages(selected.map(::refOf)) },
                enabled = selected.isNotEmpty() && !viewModel.actionInProgress,
                modifier = Modifier.padding(end = 8.dp),
            ) { Text("Сохранить (${selected.size})") }
            OutlinedButton(
                onClick = { confirmRemove = true },
                enabled = selected.isNotEmpty() && !viewModel.actionInProgress,
                modifier = Modifier.padding(end = 8.dp),
            ) { Text("Удалить (${selected.size})") }
            OutlinedButton(
                onClick = { viewModel.pruneImages() },
                enabled = !viewModel.actionInProgress,
            ) { Text("Убрать осиротевшие") }
        }

        if (data.images.backupDir.isNotBlank()) {
            Text(
                text = "Архивы сохраняются в ${data.images.backupDir}",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(horizontal = 16.dp),
            )
        }

        LazyColumn(contentPadding = PaddingValues(16.dp)) {
            items(data.images.images, key = { it.id }) { image ->
                Card(modifier = Modifier.fillMaxWidth().padding(bottom = 8.dp)) {
                    Row(
                        modifier = Modifier.padding(12.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Checkbox(
                            checked = picked.contains(image.id),
                            onCheckedChange = { on ->
                                picked = if (on) picked + image.id else picked - image.id
                            },
                        )
                        Column(modifier = Modifier.weight(1f)) {
                            Text(
                                text = image.tags.firstOrNull() ?: "без тега",
                                style = MaterialTheme.typography.titleSmall,
                            )
                            Text(
                                text = "${image.size / 1024 / 1024} МБ · " +
                                    image.id.removePrefix("sha256:").take(12),
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                            when {
                                image.inUse -> Text(
                                    text = "используется: ${image.usedBy.joinToString(", ")}",
                                    style = MaterialTheme.typography.bodySmall,
                                    color = statusColor(HealthStatus.OK),
                                )

                                image.dangling -> Text(
                                    text = "осиротевший",
                                    style = MaterialTheme.typography.bodySmall,
                                    color = statusColor(HealthStatus.WARN),
                                )
                            }
                        }
                    }
                }
            }
        }
    }
}
