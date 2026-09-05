package com.netknownsthat.app.ui.host

import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.Checkbox
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.netknownsthat.app.net.model.ConfigVersion
import com.netknownsthat.app.net.model.ConfigWriteResult
import com.netknownsthat.app.ui.theme.statusColor
import com.netknownsthat.app.status.HealthStatus

/**
 * Config files: browse, read, edit, and walk back through the host's own
 * version history.
 *
 * Editing from a phone is only reasonable because the host does not take the
 * new content on faith — it validates with the owning service (`nginx -t` and
 * friends) and restores the previous file if validation fails, so a bad edit
 * cannot leave a service unable to start. That result is what this screen
 * shows most prominently after a save.
 */
@Composable
fun ConfigsScreen(viewModel: ConfigsViewModel) {
    val open = viewModel.openFile
    val error = viewModel.openFileError

    viewModel.writeResult?.let { result ->
        WriteResultDialog(result) { viewModel.dismissWriteResult() }
    }
    viewModel.diff?.let { diff ->
        DiffDialog(diff) { viewModel.clearDiff() }
    }

    if (open == null && error == null && !viewModel.openFileLoading) {
        FileList(viewModel)
        return
    }

    Column(modifier = Modifier.fillMaxSize()) {
        Row(
            modifier = Modifier.fillMaxWidth().padding(8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            TextButton(onClick = { viewModel.closeFile() }) { Text("← К списку") }
        }
        when {
            viewModel.openFileLoading ->
                CircularProgressIndicator(modifier = Modifier.padding(24.dp))

            open == null -> Text(
                text = error.orEmpty(),
                color = MaterialTheme.colorScheme.error,
                modifier = Modifier.padding(24.dp),
            )

            else -> FileEditor(viewModel, open.path, open.content, open.sha256, open.editable, error)
        }
    }
}

@Composable
private fun FileList(viewModel: ConfigsViewModel) {
    SectionContent(
        state = viewModel.state,
        emptyText = "Файлы конфигурации не найдены",
        isEmpty = { it.files.isEmpty() },
    ) { response ->
        LazyColumn(contentPadding = PaddingValues(16.dp)) {
            items(response.files, key = { it.path }) { file ->
                Card(
                    onClick = { if (file.readable) viewModel.open(file.path) },
                    modifier = Modifier.fillMaxWidth().padding(bottom = 8.dp),
                ) {
                    Column(modifier = Modifier.padding(16.dp)) {
                        Text(
                            text = file.path,
                            style = MaterialTheme.typography.bodyMedium,
                            fontFamily = FontFamily.Monospace,
                        )
                        Text(
                            text = listOfNotNull(
                                file.service.takeIf { it.isNotBlank() },
                                "${file.size} Б",
                                file.modTime.takeIf { it.isNotBlank() },
                                if (!file.readable) "нет доступа" else null,
                                if (file.readable && !file.editable) "только чтение" else null,
                            ).joinToString(" · "),
                            style = MaterialTheme.typography.bodySmall,
                            color = if (file.readable) MaterialTheme.colorScheme.onSurfaceVariant
                            else MaterialTheme.colorScheme.error,
                            modifier = Modifier.padding(top = 4.dp),
                        )
                    }
                }
            }
        }
    }
}

@Composable
private fun FileEditor(
    viewModel: ConfigsViewModel,
    path: String,
    original: String,
    sha256: String,
    editable: Boolean,
    error: String?,
) {
    var editing by remember(path, sha256) { mutableStateOf(false) }
    var text by remember(path, sha256) { mutableStateOf(original) }
    var note by remember(path, sha256) { mutableStateOf("") }
    var applyAfter by remember(path, sha256) { mutableStateOf(false) }
    var confirmSave by remember { mutableStateOf(false) }
    var showHistory by remember(path) { mutableStateOf(false) }

    LaunchedEffect(path) { viewModel.loadVersions(path) }

    if (confirmSave) {
        AlertDialog(
            onDismissRequest = { confirmSave = false },
            title = { Text("Сохранить файл") },
            text = {
                Text(
                    "$path будет перезаписан." +
                        if (applyAfter) "\n\nСервис будет перечитан после записи." else ""
                )
            },
            confirmButton = {
                TextButton(onClick = {
                    confirmSave = false
                    editing = false
                    viewModel.save(path, text, sha256, note, applyAfter)
                }) { Text("Сохранить") }
            },
            dismissButton = { TextButton(onClick = { confirmSave = false }) { Text("Отмена") } },
        )
    }

    Column(modifier = Modifier.fillMaxSize()) {
        Text(
            text = path,
            style = MaterialTheme.typography.titleSmall,
            fontFamily = FontFamily.Monospace,
            modifier = Modifier.padding(horizontal = 16.dp),
        )
        error?.let {
            Text(
                text = it,
                color = MaterialTheme.colorScheme.error,
                style = MaterialTheme.typography.bodySmall,
                modifier = Modifier.padding(horizontal = 16.dp, vertical = 4.dp),
            )
        }

        Row(
            modifier = Modifier
                .horizontalScroll(rememberScrollState())
                .padding(horizontal = 12.dp, vertical = 8.dp),
        ) {
            if (editable) {
                OutlinedButton(
                    onClick = { editing = !editing; if (!editing) text = original },
                    enabled = !viewModel.saving,
                    modifier = Modifier.padding(end = 8.dp),
                ) { Text(if (editing) "Отменить правку" else "Править") }
            }
            if (editing) {
                Button(
                    onClick = { confirmSave = true },
                    enabled = !viewModel.saving && text != original,
                    modifier = Modifier.padding(end = 8.dp),
                ) { Text("Сохранить") }
            }
            OutlinedButton(
                onClick = { showHistory = !showHistory },
                modifier = Modifier.padding(end = 8.dp),
            ) { Text(if (showHistory) "Скрыть историю" else "История (${viewModel.versions.size})") }
        }

        if (editing) {
            Row(
                modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Checkbox(checked = applyAfter, onCheckedChange = { applyAfter = it })
                Text(
                    // Writing a file and restarting a service are decisions of
                    // different sizes; the second one is opt-in.
                    text = "Перечитать сервис после записи",
                    style = MaterialTheme.typography.bodySmall,
                )
            }
            OutlinedTextField(
                value = note,
                onValueChange = { note = it },
                label = { Text("Комментарий к правке") },
                singleLine = true,
                modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp),
            )
        }

        if (viewModel.saving) {
            CircularProgressIndicator(modifier = Modifier.padding(16.dp))
        }

        if (showHistory) {
            VersionHistory(viewModel, path)
            return@Column
        }

        if (editing) {
            BasicTextField(
                value = text,
                onValueChange = { text = it },
                textStyle = TextStyle(
                    fontFamily = FontFamily.Monospace,
                    fontSize = 12.sp,
                    color = MaterialTheme.colorScheme.onSurface,
                ),
                cursorBrush = SolidColor(MaterialTheme.colorScheme.primary),
                modifier = Modifier
                    .fillMaxSize()
                    .verticalScroll(rememberScrollState())
                    .padding(16.dp),
            )
        } else {
            Text(
                text = original,
                style = MaterialTheme.typography.bodySmall,
                fontFamily = FontFamily.Monospace,
                modifier = Modifier
                    .verticalScroll(rememberScrollState())
                    .horizontalScroll(rememberScrollState())
                    .padding(16.dp),
            )
        }
    }
}

@Composable
private fun VersionHistory(viewModel: ConfigsViewModel, path: String) {
    var confirmRollback by remember { mutableStateOf<ConfigVersion?>(null) }

    confirmRollback?.let { version ->
        AlertDialog(
            onDismissRequest = { confirmRollback = null },
            title = { Text("Откатить к версии #${version.id}") },
            text = {
                Text(
                    "Текущее содержимое $path будет заменено версией от ${version.ts}. " +
                        "Оно само сохранится в истории как новая версия, так что откат обратим."
                )
            },
            confirmButton = {
                TextButton(onClick = {
                    viewModel.rollback(version.id, path, apply = false)
                    confirmRollback = null
                }) { Text("Откатить") }
            },
            dismissButton = {
                TextButton(onClick = { confirmRollback = null }) { Text("Отмена") }
            },
        )
    }

    if (viewModel.versions.isEmpty()) {
        Text(
            text = "История пуста",
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.padding(24.dp),
        )
        return
    }

    LazyColumn(contentPadding = PaddingValues(16.dp)) {
        items(viewModel.versions, key = { it.id }) { version ->
            Card(modifier = Modifier.fillMaxWidth().padding(bottom = 8.dp)) {
                Column(modifier = Modifier.padding(16.dp)) {
                    Text(
                        text = "#${version.id} · ${actionLabel(version.action)}",
                        style = MaterialTheme.typography.titleSmall,
                    )
                    Text(
                        text = listOfNotNull(
                            version.ts.takeIf { it.isNotBlank() },
                            version.author.takeIf { it.isNotBlank() },
                            "${version.size} Б",
                        ).joinToString(" · "),
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    if (version.note.isNotBlank()) {
                        Text(
                            text = version.note,
                            style = MaterialTheme.typography.bodySmall,
                            modifier = Modifier.padding(top = 4.dp),
                        )
                    }
                    Row(modifier = Modifier.padding(top = 12.dp)) {
                        OutlinedButton(
                            onClick = { viewModel.loadDiff(version.id) },
                            modifier = Modifier.padding(end = 8.dp),
                        ) { Text("Отличия") }
                        OutlinedButton(
                            onClick = { confirmRollback = version },
                            enabled = !viewModel.saving,
                        ) { Text("Откатить") }
                    }
                }
            }
        }
    }
}

private fun actionLabel(action: String): String = when (action) {
    "edit" -> "правка"
    "rollback" -> "откат"
    // The state the host recorded before anyone edited the file — it has no
    // author, and calling it an edit would be a lie.
    "observed" -> "исходное состояние"
    else -> action
}

/**
 * A unified diff, coloured the way every diff is. The comparison is against
 * the file as it is now (see ConfigDiffResponse), so this answers "what would
 * change if I rolled back to this".
 */
@Composable
private fun DiffDialog(diff: String, onDismiss: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("Отличия от текущего файла") },
        text = {
            Column(
                modifier = Modifier
                    .verticalScroll(rememberScrollState())
                    .horizontalScroll(rememberScrollState()),
            ) {
                diff.lines().forEach { line ->
                    Text(
                        text = line,
                        style = MaterialTheme.typography.bodySmall,
                        fontFamily = FontFamily.Monospace,
                        color = when {
                            line.startsWith("+++") || line.startsWith("---") ->
                                MaterialTheme.colorScheme.onSurfaceVariant

                            line.startsWith("@@") -> MaterialTheme.colorScheme.primary
                            line.startsWith("+") -> statusColor(HealthStatus.OK)
                            line.startsWith("-") -> statusColor(HealthStatus.BAD)
                            else -> MaterialTheme.colorScheme.onSurface
                        },
                    )
                }
            }
        },
        confirmButton = { TextButton(onClick = onDismiss) { Text("Закрыть") } },
    )
}

/**
 * What the host made of the write. The validation outcome is the point: a
 * rolled-back write means the file on disk is unchanged and the service was
 * never at risk, which is very different news from a plain failure.
 */
@Composable
private fun WriteResultDialog(result: ConfigWriteResult, onDismiss: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = {
            Text(
                when {
                    result.rolledBack -> "Изменения отменены"
                    result.validated -> "Сохранено и проверено"
                    else -> "Сохранено"
                }
            )
        },
        text = {
            Column(modifier = Modifier.verticalScroll(rememberScrollState())) {
                if (result.message.isNotBlank()) Text(result.message)
                if (result.rolledBack) {
                    Text(
                        text = "Проверка конфигурации не прошла, поэтому файл возвращён к " +
                            "прежнему содержимому. Сервис не затронут.",
                        color = statusColor(HealthStatus.WARN),
                        modifier = Modifier.padding(top = 8.dp),
                    )
                }
                if (result.versionId > 0) {
                    Text(
                        text = "Версия #${result.versionId}",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.padding(top = 8.dp),
                    )
                }
                result.validation?.let { validation ->
                    Text(
                        text = "Проверка: ${validation.argv.joinToString(" ")}",
                        style = MaterialTheme.typography.bodySmall,
                        fontFamily = FontFamily.Monospace,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.padding(top = 8.dp),
                    )
                    val output = (validation.stderr + validation.stdout).trim()
                    if (output.isNotEmpty()) {
                        Text(
                            text = output,
                            style = MaterialTheme.typography.bodySmall,
                            fontFamily = FontFamily.Monospace,
                            color = if (validation.exitCode == 0) MaterialTheme.colorScheme.onSurface
                            else statusColor(HealthStatus.BAD),
                        )
                    }
                    if (validation.simulated) {
                        Text(
                            text = "(демонстрационный режим — команда не выполнялась)",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                }
                if (result.applied) {
                    Text(
                        text = "Сервис перечитан.",
                        color = statusColor(HealthStatus.OK),
                        modifier = Modifier.padding(top = 8.dp),
                    )
                }
            }
        },
        confirmButton = { TextButton(onClick = onDismiss) { Text("Закрыть") } },
    )
}
