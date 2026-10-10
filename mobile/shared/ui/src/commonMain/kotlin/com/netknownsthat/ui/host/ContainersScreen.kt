package com.netknownsthat.ui.host

import com.netknownsthat.domain.model.OsInfo
import com.netknownsthat.ui.common.OsIcon
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.FlowRow
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
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Tab
import androidx.compose.material3.ScrollableTabRow
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
import com.netknownsthat.domain.model.Container
import com.netknownsthat.domain.model.DockerImage
import com.netknownsthat.ui.status.containerHealth
import com.netknownsthat.ui.status.HealthStatus
import com.netknownsthat.ui.status.instanceHealth
import com.netknownsthat.ui.theme.StatusDot
import com.netknownsthat.ui.theme.statusColor
import com.netknownsthat.ui.i18n.t
import com.netknownsthat.ui.common.SectionContent
import com.netknownsthat.domain.usecase.ContainerRuntimes
import com.netknownsthat.domain.repository.ConsoleKind
import com.netknownsthat.domain.repository.ConsoleTarget

private val LIFECYCLE get() = listOf("start" to t("Пуск", "Start"), "stop" to t("Стоп", "Stop"), "restart" to t("Рестарт", "Restart"))

@Composable
fun ContainersScreen(
    viewModel: ContainersViewModel,
    onOpenContainer: (String) -> Unit,
    onOpenJob: (Long) -> Unit = {},
    onOpenConsole: (ConsoleTarget) -> Unit = {},
    onOpenScreen: (kind: String, name: String, proto: String) -> Unit = { _, _, _ -> },
) {
    var tab by remember { mutableIntStateOf(0) }
    // Docker and Podman ask whom to exec as first, as the web UI does.
    var askUser by remember { mutableStateOf<ConsoleTarget?>(null) }
    askUser?.let { target ->
        ConsoleUserDialog(target, onDismiss = { askUser = null }) { user ->
            askUser = null
            onOpenConsole(target.copy(user = user))
        }
    }

    SectionContent(state = viewModel.state, emptyText = t("Контейнеры не найдены", "No containers found")) { data ->
        // Only tabs with something in them: a host running plain Docker
        // should not be offered three empty tabs for runtimes it lacks.
        val tabs = buildList {
            if (data.docker.containers.isNotEmpty()) add("Docker" to 0)
            if (data.podman.containers.isNotEmpty()) add("Podman" to 1)
            if (data.lxd.instances.isNotEmpty()) add("LXD" to 2)
            if (data.vms.vms.isNotEmpty()) add(t("ВМ", "VMs") to 3)
            if (data.images.images.isNotEmpty()) add(t("Образы", "Images") to 4)
        }
        if (!data.dockerInstalled) {
            Text(
                text = t("Docker на хосте не установлен — установить можно в веб-интерфейсе (раздел «Контейнеры и ВМ»)", "Docker is not installed on the host — it can be installed from the web UI (“Containers and VMs” section)"),
                style = MaterialTheme.typography.bodySmall,
                color = statusColor(HealthStatus.WARN),
                modifier = Modifier.padding(horizontal = 16.dp, vertical = 8.dp),
            )
        }
        if (tabs.isEmpty()) {
            Text(
                text = t("Ни один контейнерный движок не найден", "No container engine found"),
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(24.dp),
            )
            return@SectionContent
        }
        val current = tabs.firstOrNull { it.second == tab } ?: tabs.first()

        Column {
            ScrollableTabRow(selectedTabIndex = tabs.indexOf(current), edgePadding = 0.dp) {
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
                        DockerCard(
                            it, enabled, viewModel.pendingKey == it.name,
                            onInspect = { onOpenContainer(it.name) },
                            onConsole = { askUser = ConsoleTarget(ConsoleKind.DOCKER, it.name) },
                        ) { action ->
                            viewModel.dockerAction(it.name, action)
                        }
                    }
                }

                1 -> LazyColumn(contentPadding = PaddingValues(16.dp)) {
                    items(data.podman.containers, key = { it.id }) {
                        SimpleRuntimeCard(
                            it.name, it.osInfo, it.image, it.status,
                            containerHealth(it.state), enabled,
                            viewModel.pendingKey == it.name,
                            onConsole = { askUser = ConsoleTarget(ConsoleKind.PODMAN, it.name) }.takeIf { _ -> running(it.state) },
                        ) { action -> viewModel.podmanAction(it.name, action) }
                    }
                }

                2 -> LazyColumn(contentPadding = PaddingValues(16.dp)) {
                    items(data.lxd.instances, key = { it.name }) {
                        SimpleRuntimeCard(
                            it.name,
                            it.osInfo,
                            "${it.type} · ${it.architecture}",
                            it.status + it.ipv4.joinToString("") { ip -> " · $ip" },
                            instanceHealth(it.status), enabled,
                            viewModel.pendingKey == it.name,
                            // У Windows текстовой консоли нет — только экран.
                            onConsole = { onOpenConsole(ConsoleTarget(ConsoleKind.LXD, it.name)) }.takeIf { _ -> running(it.status) && it.osInfo?.id != "windows" },
                            // An LXD virtual machine shows its screen over SPICE.
                            onScreen = { onOpenScreen("lxd", it.name, "spice") }
                                .takeIf { _ -> running(it.status) && it.type == "virtual-machine" && screenNameOk(it.name) },
                        ) { action -> viewModel.lxdAction(it.name, action, onOpenJob) }
                    }
                }

                4 -> ImagesTab(viewModel, data, onOpenJob)

                3 -> LazyColumn(contentPadding = PaddingValues(16.dp)) {
                    items(data.vms.vms, key = { it.name }) {
                        SimpleRuntimeCard(
                            it.name,
                            it.osInfo,
                            t("${it.vcpus} vCPU · ${it.memoryKb / 1024} МБ", "${it.vcpus} vCPU · ${it.memoryKb / 1024} MB"),
                            it.state,
                            instanceHealth(it.state), enabled,
                            viewModel.pendingKey == it.name,
                            onConsole = { onOpenConsole(ConsoleTarget(ConsoleKind.VM, it.name)) }.takeIf { _ -> running(it.state) && it.osInfo?.id != "windows" },
                            // VNC when the machine has it (or nothing is known —
                            // an old host), SPICE otherwise.
                            onScreen = screenProto(it.graphics)?.let { proto -> { onOpenScreen("vm", it.name, proto) } }
                                ?.takeIf { _ -> running(it.state) && screenNameOk(it.name) },
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
    onInspect: () -> Unit,
    onConsole: () -> Unit,
    onAction: (String) -> Unit,
) {
    Card(modifier = Modifier.fillMaxWidth().padding(bottom = 8.dp)) {
        Column(modifier = Modifier.padding(16.dp)) {
            Row(
                modifier = Modifier.fillMaxWidth(),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                StatusDot(containerHealth(container.state), busy = busy)
                OsIcon(container.osInfo)
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
                    text = t("Запущен вручную, не описан в compose", "Started by hand, not described in compose"),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.tertiary,
                    modifier = Modifier.padding(top = 4.dp),
                )
            }
            ActionRow(enabled, onAction, onConsole.takeIf { running(container.state) }) {
                TextButton(onClick = onInspect) { Text(t("Инспект", "Inspect")) }
            }
        }
    }
}

@Composable
private fun SimpleRuntimeCard(
    name: String,
    os: OsInfo?,
    subtitle: String,
    status: String,
    health: com.netknownsthat.ui.status.HealthStatus,
    enabled: Boolean,
    busy: Boolean,
    onConsole: (() -> Unit)? = null,
    onScreen: (() -> Unit)? = null,
    onAction: (String) -> Unit,
) {
    Card(modifier = Modifier.fillMaxWidth().padding(bottom = 8.dp)) {
        Column(modifier = Modifier.padding(16.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                StatusDot(health, busy = busy)
                OsIcon(os)
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
            ActionRow(enabled, onAction, onConsole) {
                if (onScreen != null) OutlinedButton(onClick = onScreen, contentPadding = COMPACT_BUTTON) { Text(t("Экран", "Screen")) }
            }
        }
    }
}

@Composable
private fun ActionRow(
    enabled: Boolean,
    onAction: (String) -> Unit,
    onConsole: (() -> Unit)? = null,
    extra: (@Composable () -> Unit)? = null,
) {
    // Wraps instead of scrolling sideways: on a narrow phone the last
    // buttons were cut off with no hint that the row scrolls.
    FlowRow(
        horizontalArrangement = Arrangement.spacedBy(6.dp),
        modifier = Modifier.padding(top = 8.dp),
    ) {
        LIFECYCLE.forEach { (action, label) ->
            OutlinedButton(onClick = { onAction(action) }, enabled = enabled, contentPadding = COMPACT_BUTTON) { Text(label) }
        }
        // Only while it runs: there is nothing to exec into otherwise.
        if (onConsole != null) {
            OutlinedButton(onClick = onConsole, contentPadding = COMPACT_BUTTON) { Text(t("Консоль", "Console")) }
        }
        extra?.invoke()
    }
}

private val COMPACT_BUTTON = PaddingValues(horizontal = 12.dp, vertical = 6.dp)


/** Docker/LXD/libvirt spell "running" differently ("running", "Running"). */
private fun running(state: String) = state.equals("running", ignoreCase = true)

/** Whom to exec as; empty — the image's default user. */
@Composable
private fun ConsoleUserDialog(target: ConsoleTarget, onDismiss: () -> Unit, onOpen: (String) -> Unit) {
    var user by remember { mutableStateOf("") }
    val valid = user.isBlank() || CONSOLE_USER.matches(user.trim())
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(t("Консоль ${target.name}", "Console of ${target.name}")) },
        text = {
            OutlinedTextField(
                value = user,
                onValueChange = { user = it },
                singleLine = true,
                isError = !valid,
                label = { Text(t("Пользователь", "User")) },
                supportingText = { Text(t("Пусто — пользователь образа по умолчанию", "Empty — the image's default user")) },
            )
        },
        confirmButton = { TextButton(onClick = { onOpen(user.trim()) }, enabled = valid) { Text(t("Открыть", "Open")) } },
        dismissButton = { TextButton(onClick = onDismiss) { Text(t("Отмена", "Cancel")) } },
    )
}

/** The server's rule for the exec user (handlers_console.go). */
private val CONSOLE_USER = Regex("^[A-Za-z0-9_][A-Za-z0-9_.-]{0,31}$")

/**
 * Docker images with a multi-select. The two things worth doing to several
 * at once are removing them and saving them to a tar on the host; an image a
 * container is running from is marked, because Docker refuses to remove one
 * without force and saying so first beats offering an action that fails.
 */
@Composable
private fun ImagesTab(viewModel: ContainersViewModel, data: ContainerRuntimes, onOpenJob: (Long) -> Unit) {
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
            title = { Text(t("Удалить выбранные образы?", "Delete the selected images?")) },
            text = {
                Text(
                    if (inUseSelected > 0 && !force)
                        t("Из выбранных $inUseSelected используются запущенными контейнерами — ", "$inUseSelected of the selected are used by running containers — ") +
                            t("Docker откажется их удалять. Включите «принудительно», если это осознанно.", "Docker will refuse to delete them. Turn on “force” if this is intended.")
                    else t("Будет удалено образов: ${selected.size}.", "Images to delete: ${selected.size}.")
                )
            },
            confirmButton = {
                TextButton(onClick = {
                    viewModel.removeImages(selected.map(::refOf), force)
                    picked = emptySet()
                    confirmRemove = false
                }) { Text(t("Удалить", "Delete")) }
            },
            dismissButton = { TextButton(onClick = { confirmRemove = false }) { Text(t("Отмена", "Cancel")) } },
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
                label = { Text(t("принудительно", "force")) },
                modifier = Modifier.padding(end = 8.dp),
            )
            OutlinedButton(
                onClick = { viewModel.saveImages(selected.map(::refOf), onOpenJob) },
                enabled = selected.isNotEmpty() && !viewModel.actionInProgress,
                modifier = Modifier.padding(end = 8.dp),
            ) { Text(t("Сохранить (${selected.size})", "Save (${selected.size})")) }
            OutlinedButton(
                onClick = { confirmRemove = true },
                enabled = selected.isNotEmpty() && !viewModel.actionInProgress,
                modifier = Modifier.padding(end = 8.dp),
            ) { Text(t("Удалить (${selected.size})", "Delete (${selected.size})")) }
            OutlinedButton(
                onClick = { viewModel.pruneImages() },
                enabled = !viewModel.actionInProgress,
            ) { Text(t("Убрать осиротевшие", "Remove dangling")) }
        }

        if (data.images.backupDir.isNotBlank()) {
            Text(
                text = t("Архивы сохраняются в ${data.images.backupDir}", "Archives are saved to ${data.images.backupDir}"),
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
                                text = image.tags.firstOrNull() ?: t("без тега", "untagged"),
                                style = MaterialTheme.typography.titleSmall,
                            )
                            Text(
                                text = t("${image.size / 1024 / 1024} МБ · ", "${image.size / 1024 / 1024} MB · ") +
                                    image.id.removePrefix("sha256:").take(12),
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                            when {
                                image.inUse -> Text(
                                    text = t("используется: ${image.usedBy.joinToString(", ")}", "used by: ${image.usedBy.joinToString(", ")}"),
                                    style = MaterialTheme.typography.bodySmall,
                                    color = statusColor(HealthStatus.OK),
                                )

                                image.dangling -> Text(
                                    text = t("осиротевший", "dangling"),
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
