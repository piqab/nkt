package com.netknownsthat.ui.host

import com.netknownsthat.ui.common.humanBytes
import com.netknownsthat.ui.common.formatTs
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
import com.netknownsthat.domain.model.ConfigVersion
import com.netknownsthat.domain.model.ConfigWriteResult
import com.netknownsthat.ui.theme.statusColor
import com.netknownsthat.ui.status.HealthStatus
import com.netknownsthat.ui.i18n.t
import com.netknownsthat.ui.common.SectionContent

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
/** The list of managed config files; a tap opens the file's own page. */
@Composable
fun ConfigsScreen(viewModel: ConfigsViewModel, onOpenFile: (String) -> Unit) {
    FileList(viewModel, onOpenFile)
}

/** One file: view, edit, history, rollback — its own page in the back stack. */
@Composable
fun ConfigFileScreen(viewModel: ConfigsViewModel, path: String) {
    LaunchedEffect(path) {
        viewModel.open(path)
        viewModel.loadVersions(path)
    }
    val open = viewModel.openFile
    val error = viewModel.openFileError

    viewModel.writeResult?.let { result ->
        WriteResultDialog(result) { viewModel.dismissWriteResult() }
    }
    viewModel.diff?.let { diff ->
        DiffDialog(diff) { viewModel.clearDiff() }
    }

    Column(modifier = Modifier.fillMaxSize()) {
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
private fun FileList(viewModel: ConfigsViewModel, onOpenFile: (String) -> Unit) {
    SectionContent(
        state = viewModel.state,
        emptyText = t("Файлы конфигурации не найдены", "No configuration files found"),
        isEmpty = { it.files.isEmpty() },
    ) { response ->
        LazyColumn(contentPadding = PaddingValues(16.dp)) {
            items(response.files, key = { it.path }) { file ->
                Card(
                    onClick = { if (file.readable) onOpenFile(file.path) },
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
                                humanBytes(file.size.toLong()),
                                file.modTime.takeIf { it.isNotBlank() }?.let(::formatTs),
                                if (!file.readable) t("нет доступа", "no access") else null,
                                if (file.readable && !file.editable) t("только чтение", "read-only") else null,
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
            title = { Text(t("Сохранить файл", "Save file")) },
            text = {
                Text(
                    t("$path будет перезаписан.", "$path will be overwritten.") +
                        if (applyAfter) t("\n\nСервис будет перечитан после записи.", "\n\nThe service will be reloaded after writing.") else ""
                )
            },
            confirmButton = {
                TextButton(onClick = {
                    confirmSave = false
                    editing = false
                    viewModel.save(path, text, sha256, note, applyAfter)
                }) { Text(t("Сохранить", "Save")) }
            },
            dismissButton = { TextButton(onClick = { confirmSave = false }) { Text(t("Отмена", "Cancel")) } },
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
                ) { Text(if (editing) t("Отменить правку", "Discard edit") else t("Править", "Edit")) }
            }
            if (editing) {
                Button(
                    onClick = { confirmSave = true },
                    enabled = !viewModel.saving && text != original,
                    modifier = Modifier.padding(end = 8.dp),
                ) { Text(t("Сохранить", "Save")) }
            }
            OutlinedButton(
                onClick = { showHistory = !showHistory },
                modifier = Modifier.padding(end = 8.dp),
            ) { Text(if (showHistory) t("Скрыть историю", "Hide history") else t("История (${viewModel.versions.size})", "History (${viewModel.versions.size})")) }
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
                    text = t("Перечитать сервис после записи", "Reload the service after writing"),
                    style = MaterialTheme.typography.bodySmall,
                )
            }
            OutlinedTextField(
                value = note,
                onValueChange = { note = it },
                label = { Text(t("Комментарий к правке", "Edit comment")) },
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
            title = { Text(t("Откатить к версии #${version.id}", "Roll back to version #${version.id}")) },
            text = {
                Text(
                    t("Текущее содержимое $path будет заменено версией от ${formatTs(version.ts)}. ", "The current content of $path will be replaced by the version from ${formatTs(version.ts)}. ") +
                        t("Оно само сохранится в истории как новая версия, так что откат обратим.", "It is itself saved in history as a new version, so the rollback can be undone.")
                )
            },
            confirmButton = {
                TextButton(onClick = {
                    viewModel.rollback(version.id, path, apply = false)
                    confirmRollback = null
                }) { Text(t("Откатить", "Roll back")) }
            },
            dismissButton = {
                TextButton(onClick = { confirmRollback = null }) { Text(t("Отмена", "Cancel")) }
            },
        )
    }

    if (viewModel.versions.isEmpty()) {
        Text(
            text = t("История пуста", "History is empty"),
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
                            version.ts.takeIf { it.isNotBlank() }?.let(::formatTs),
                            version.author.takeIf { it.isNotBlank() },
                            humanBytes(version.size.toLong()),
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
                        ) { Text(t("Отличия", "Diff")) }
                        OutlinedButton(
                            onClick = { confirmRollback = version },
                            enabled = !viewModel.saving,
                        ) { Text(t("Откатить", "Roll back")) }
                    }
                }
            }
        }
    }
}

private fun actionLabel(action: String): String = when (action) {
    "edit" -> t("правка", "edit")
    "rollback" -> t("откат", "rollback")
    // The state the host recorded before anyone edited the file — it has no
    // author, and calling it an edit would be a lie.
    "observed" -> t("исходное состояние", "original state")
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
        title = { Text(t("Отличия от текущего файла", "Diff against the current file")) },
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
        confirmButton = { TextButton(onClick = onDismiss) { Text(t("Закрыть", "Close")) } },
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
                    result.rolledBack -> t("Изменения отменены", "Changes discarded")
                    result.validated -> t("Сохранено и проверено", "Saved and validated")
                    else -> t("Сохранено", "Saved")
                }
            )
        },
        text = {
            Column(modifier = Modifier.verticalScroll(rememberScrollState())) {
                if (result.message.isNotBlank()) Text(result.message)
                if (result.rolledBack) {
                    Text(
                        text = t("Проверка конфигурации не прошла, поэтому файл возвращён к ", "Configuration validation failed, so the file was restored to ") +
                            t("прежнему содержимому. Сервис не затронут.", "its previous content. The service was not touched."),
                        color = statusColor(HealthStatus.WARN),
                        modifier = Modifier.padding(top = 8.dp),
                    )
                }
                if (result.versionId > 0) {
                    Text(
                        text = t("Версия #${result.versionId}", "Version #${result.versionId}"),
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.padding(top = 8.dp),
                    )
                }
                result.validation?.let { validation ->
                    Text(
                        text = t("Проверка: ${validation.argv.joinToString(" ")}", "Check: ${validation.argv.joinToString(" ")}"),
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
                            text = t("(демонстрационный режим — команда не выполнялась)", "(demo mode — the command was not run)"),
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                }
                if (result.applied) {
                    Text(
                        text = t("Сервис перечитан.", "Service reloaded."),
                        color = statusColor(HealthStatus.OK),
                        modifier = Modifier.padding(top = 8.dp),
                    )
                }
            }
        },
        confirmButton = { TextButton(onClick = onDismiss) { Text(t("Закрыть", "Close")) } },
    )
}
