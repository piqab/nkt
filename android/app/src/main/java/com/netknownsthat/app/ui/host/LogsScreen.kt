package com.netknownsthat.app.ui.host

import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Card
import androidx.compose.material3.Checkbox
import androidx.compose.material3.FilterChip
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.netknownsthat.app.net.model.LogSource
import com.netknownsthat.app.status.HealthStatus
import com.netknownsthat.app.ui.theme.statusColor

/**
 * Watching one log as it is written. Same endpoint as the web page — the
 * detach-into-a-window part has no equivalent here, since Android has no
 * second window to detach into.
 */
@Composable
fun LogsScreen(viewModel: LogsViewModel) {
    var showPicker by remember { mutableStateOf(false) }
    var filter by remember { mutableStateOf("") }
    var highlight by remember { mutableStateOf("") }
    var caseSensitive by remember { mutableStateOf(false) }
    var follow by remember { mutableStateOf(true) }

    LaunchedEffect(Unit) { viewModel.loadSources() }
    DisposableEffect(Unit) { onDispose { viewModel.stop() } }

    if (showPicker) {
        SourcePickerDialog(
            sources = viewModel.sources,
            root = viewModel.root,
            onDismiss = { showPicker = false },
            onPick = { source ->
                showPicker = false
                viewModel.watch(source)
            },
        )
    }

    val stream = viewModel.stream
    // An archive has no stream behind it — it was read once into a snapshot.
    val all: List<String> = if (viewModel.isArchived) viewModel.snapshot
    else stream?.lines ?: emptyList()
    val shown = remember(all.size, filter, caseSensitive) {
        if (filter.isBlank()) all.toList()
        else all.filter {
            if (caseSensitive) it.contains(filter) else it.lowercase().contains(filter.lowercase())
        }
    }

    val listState = rememberLazyListState()
    LaunchedEffect(shown.size, follow) {
        if (follow && shown.isNotEmpty()) listState.scrollToItem(shown.size - 1)
    }

    Column(modifier = Modifier.fillMaxSize()) {
        Card(modifier = Modifier.fillMaxWidth().padding(12.dp)) {
            Column(modifier = Modifier.padding(12.dp)) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(
                        text = viewModel.currentLabel ?: "Источник не выбран",
                        style = MaterialTheme.typography.titleSmall,
                        modifier = Modifier.weight(1f),
                    )
                    Text(
                        text = when {
                            viewModel.isArchived -> "архив"
                            stream?.connected == true -> "поток идёт"
                            else -> "остановлен"
                        },
                        style = MaterialTheme.typography.labelMedium,
                        color = statusColor(
                            if (stream?.connected == true) HealthStatus.OK else HealthStatus.UNKNOWN
                        ),
                    )
                }
                viewModel.snapshotError?.let {
                    Text(
                        text = it,
                        style = MaterialTheme.typography.bodySmall,
                        color = statusColor(HealthStatus.BAD),
                        modifier = Modifier.padding(top = 4.dp),
                    )
                }
                stream?.error?.let {
                    Text(
                        text = it,
                        style = MaterialTheme.typography.bodySmall,
                        color = statusColor(HealthStatus.BAD),
                        modifier = Modifier.padding(top = 4.dp),
                    )
                }

                Row(modifier = Modifier.padding(top = 8.dp)) {
                    OutlinedButton(
                        onClick = { showPicker = true },
                        modifier = Modifier.padding(end = 8.dp),
                    ) { Text("Выбрать") }
                    OutlinedButton(
                        onClick = { if (stream?.connected == true) viewModel.stop() else viewModel.restart() },
                        enabled = viewModel.currentLabel != null,
                    ) {
                        Text(
                            when {
                                viewModel.isArchived -> "Перечитать"
                                stream?.connected == true -> "Остановить"
                                else -> "Смотреть"
                            }
                        )
                    }
                }

                Row(modifier = Modifier.padding(top = 8.dp)) {
                    listOf(500, 1000, 5000).forEach { n ->
                        FilterChip(
                            selected = viewModel.lineCount == n,
                            onClick = { viewModel.setLines(n) },
                            label = { Text("последние $n") },
                            modifier = Modifier.padding(end = 6.dp),
                        )
                    }
                }

                OutlinedTextField(
                    value = filter,
                    onValueChange = { filter = it },
                    label = { Text("Фильтр: только строки с…") },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth().padding(top = 8.dp),
                )
                OutlinedTextField(
                    value = highlight,
                    onValueChange = { highlight = it },
                    label = { Text("Подсветить…") },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth().padding(top = 8.dp),
                )
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Checkbox(checked = caseSensitive, onCheckedChange = { caseSensitive = it })
                    Text("регистр", style = MaterialTheme.typography.bodySmall)
                    Checkbox(checked = follow, onCheckedChange = { follow = it })
                    Text("к концу", style = MaterialTheme.typography.bodySmall)
                }
                Text(
                    text = "показано ${shown.size} из ${all.size}",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }

        LazyColumn(
            state = listState,
            modifier = Modifier.fillMaxSize().padding(horizontal = 12.dp),
        ) {
            items(shown) { line ->
                Text(
                    text = highlighted(line, highlight, caseSensitive),
                    style = MaterialTheme.typography.bodySmall,
                    fontFamily = FontFamily.Monospace,
                    fontSize = 11.sp,
                    softWrap = false,
                    modifier = Modifier.horizontalScroll(rememberScrollState()),
                )
            }
        }
    }
}

/** Marks every occurrence of [needle]; plain text when there is nothing to mark. */
@Composable
private fun highlighted(line: String, needle: String, caseSensitive: Boolean) =
    buildAnnotatedString {
        if (needle.isBlank()) {
            append(line)
            return@buildAnnotatedString
        }
        val haystack = if (caseSensitive) line else line.lowercase()
        val term = if (caseSensitive) needle else needle.lowercase()
        var from = 0
        while (true) {
            val at = haystack.indexOf(term, from)
            if (at < 0) break
            append(line.substring(from, at))
            withStyle(SpanStyle(background = statusColor(HealthStatus.WARN))) {
                append(line.substring(at, at + term.length))
            }
            from = at + term.length
        }
        append(line.substring(from))
    }

@Composable
private fun SourcePickerDialog(
    sources: List<LogSource>,
    root: String,
    onDismiss: () -> Unit,
    onPick: (LogSource) -> Unit,
) {
    var custom by remember { mutableStateOf("") }
    var search by remember { mutableStateOf("") }
    var showArchived by remember { mutableStateOf(false) }
    val visible = sources.filter {
        it.name.contains(search, ignoreCase = true) && (showArchived || !it.archived)
    }

    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("Источник") },
        text = {
            Column {
                OutlinedTextField(
                    value = search,
                    onValueChange = { search = it },
                    label = { Text("Поиск") },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                )
                OutlinedTextField(
                    value = custom,
                    onValueChange = { custom = it },
                    label = { Text("Свой путь внутри $root") },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth().padding(top = 8.dp),
                )
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Checkbox(checked = showArchived, onCheckedChange = { showArchived = it })
                    // Rotated generations are hidden by default: the list is
                    // roughly twice as long with them, and they are only
                    // wanted when looking further back than today.
                    Text("показывать архивные", style = MaterialTheme.typography.bodySmall)
                }
                if (custom.isNotBlank()) {
                    OutlinedButton(
                        onClick = { onPick(LogSource(kind = "file", name = custom.trim())) },
                        modifier = Modifier.padding(top = 8.dp),
                    ) { Text("Смотреть этот файл") }
                }
                LazyColumn(modifier = Modifier.padding(top = 8.dp)) {
                    items(visible) { source ->
                        FilterChip(
                            selected = false,
                            onClick = { onPick(source) },
                            label = {
                                Text(
                                    text = when {
                                        source.kind == "unit" -> "журнал: ${source.name}"
                                        source.compressed -> "${source.name} (сжат)"
                                        source.archived -> "${source.name} (архив)"
                                        else -> source.name
                                    },
                                )
                            },
                            modifier = Modifier.padding(bottom = 4.dp),
                        )
                    }
                }
            }
        },
        confirmButton = { TextButton(onClick = onDismiss) { Text("Закрыть") } },
    )
}
