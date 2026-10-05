package com.netknownsthat.app.ui.hosts

import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Checkbox
import androidx.compose.material3.FilterChip
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import com.netknownsthat.app.net.model.HubHost

private val AUTH_KINDS = listOf(
    "password" to "Пароль",
    "key" to "Свой ключ",
    "generated" to "Ключ хаба",
)

/** Host name: the same characters the hub accepts in names elsewhere —
 * checked here only to catch typos early; the hub validates for real. */
private val NAME_RE = Regex("""^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$""")

/**
 * Adding a host: SSH address and account, then (optionally) installing nkt
 * right away as a hub job. With «Ключ хаба» the hub generates a key pair and
 * shows the public line to put on the host first; nothing is installed until
 * then. The secret (password or private key) goes to the hub in the request
 * body, encrypted at rest there — it is never part of a job.
 */
@Composable
fun AddHostDialog(groups: List<String>, onDismiss: () -> Unit, onAdd: (AddHostForm, Boolean) -> Unit) {
    var form by remember { mutableStateOf(AddHostForm()) }
    var install by remember { mutableStateOf(true) }
    val valid = NAME_RE.matches(form.name.trim()) && form.addr.isNotBlank() && form.user.isNotBlank() &&
        form.port in 1..65535 && (form.authKind == "generated" || form.secret.isNotBlank())
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("Новый хост") },
        text = {
            Column(modifier = Modifier.verticalScroll(rememberScrollState())) {
                OutlinedTextField(
                    value = form.name, onValueChange = { form = form.copy(name = it) },
                    label = { Text("Имя") }, singleLine = true,
                    isError = form.name.isNotBlank() && !NAME_RE.matches(form.name.trim()),
                )
                OutlinedTextField(
                    value = form.addr, onValueChange = { form = form.copy(addr = it) },
                    label = { Text("Адрес (IP или имя)") }, singleLine = true,
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri),
                )
                Row {
                    OutlinedTextField(
                        value = form.user, onValueChange = { form = form.copy(user = it) },
                        label = { Text("Пользователь SSH") }, singleLine = true,
                        modifier = Modifier.weight(1f).padding(end = 8.dp),
                    )
                    OutlinedTextField(
                        value = form.port.toString(),
                        onValueChange = { v -> v.toIntOrNull()?.let { form = form.copy(port = it) } ?: run { if (v.isEmpty()) form = form.copy(port = 0) } },
                        label = { Text("Порт") }, singleLine = true,
                        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number),
                        modifier = Modifier.weight(0.6f),
                    )
                }
                Row(modifier = Modifier.horizontalScroll(rememberScrollState()).padding(top = 8.dp)) {
                    AUTH_KINDS.forEach { (kind, label) ->
                        FilterChip(
                            selected = form.authKind == kind,
                            onClick = { form = form.copy(authKind = kind, secret = "") },
                            label = { Text(label) },
                            modifier = Modifier.padding(end = 6.dp),
                        )
                    }
                }
                when (form.authKind) {
                    "password" -> OutlinedTextField(
                        value = form.secret, onValueChange = { form = form.copy(secret = it) },
                        label = { Text("Пароль") }, singleLine = true,
                        visualTransformation = PasswordVisualTransformation(),
                        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password),
                    )
                    "key" -> OutlinedTextField(
                        value = form.secret, onValueChange = { form = form.copy(secret = it) },
                        label = { Text("Закрытый ключ (PEM)") }, minLines = 3, maxLines = 6,
                    )
                    else -> Text(
                        "Хаб создаст ключ и покажет строку для authorized_keys на хосте",
                        style = MaterialTheme.typography.bodySmall,
                    )
                }
                OutlinedTextField(
                    value = form.group, onValueChange = { form = form.copy(group = it) },
                    label = { Text("Группа (пусто — без группы)") }, singleLine = true,
                )
                if (groups.isNotEmpty()) {
                    Row(modifier = Modifier.horizontalScroll(rememberScrollState())) {
                        groups.forEach { g ->
                            FilterChip(
                                selected = form.group == g,
                                onClick = { form = form.copy(group = if (form.group == g) "" else g) },
                                label = { Text(g) },
                                modifier = Modifier.padding(end = 6.dp),
                            )
                        }
                    }
                }
                CheckRow("Терминал на хосте", form.terminal) { form = form.copy(terminal = it) }
                if (form.authKind != "generated") CheckRow("Сразу установить nkt", install) { install = it }
            }
        },
        confirmButton = { TextButton(enabled = valid, onClick = { onAdd(form, install) }) { Text("Добавить") } },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Отмена") } },
    )
}

@Composable
private fun CheckRow(label: String, checked: Boolean, onChange: (Boolean) -> Unit) {
    Row(verticalAlignment = Alignment.CenterVertically) {
        Checkbox(checked = checked, onCheckedChange = onChange)
        Text(label)
    }
}

/** The public key line of a «Ключ хаба» host, to copy onto the host. */
@Composable
fun AuthorizedKeyDialog(key: String, onDismiss: () -> Unit) {
    val clipboard = LocalClipboardManager.current
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("Ключ хаба") },
        text = {
            Column {
                Text(
                    "Добавьте строку в ~/.ssh/authorized_keys пользователя SSH на хосте, затем «Установить nkt» в меню хоста.",
                    style = MaterialTheme.typography.bodySmall,
                )
                SelectionContainer {
                    Text(key, fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.bodySmall, modifier = Modifier.padding(top = 8.dp))
                }
            }
        },
        confirmButton = {
            TextButton(onClick = { clipboard.setText(AnnotatedString(key)) }) { Text("Копировать") }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Готово") } },
    )
}

/** Delete: only the hub's record, or also nkt on the host. */
@Composable
fun DeleteHostDialog(host: HubHost, onDismiss: () -> Unit, onDelete: (purge: Boolean) -> Unit) {
    var purge by remember { mutableStateOf(false) }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("Удалить ${host.name}?") },
        text = {
            Column {
                CheckRow("Убрать nkt и с самого хоста", purge) { purge = it }
                Text(
                    if (purge) "Служба, данные, правило sudo и ключ хаба будут удалены с хоста, вход по паролю — возвращён. Запись в хабе удаляется в любом случае, даже если хост не ответит."
                    else "Удаляется только запись в хабе; nkt на хосте продолжит работать.",
                    style = MaterialTheme.typography.bodySmall,
                )
            }
        },
        confirmButton = {
            TextButton(onClick = { onDelete(purge) }) { Text("Удалить", color = MaterialTheme.colorScheme.error) }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Отмена") } },
    )
}

/** Move a host to a group (or out of any). */
@Composable
fun SetGroupDialog(host: HubHost, groups: List<String>, onDismiss: () -> Unit, onSet: (String) -> Unit) {
    var group by remember { mutableStateOf(host.group) }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("Группа ${host.name}") },
        text = {
            Column {
                OutlinedTextField(value = group, onValueChange = { group = it }, label = { Text("Группа") }, singleLine = true)
                Row(modifier = Modifier.horizontalScroll(rememberScrollState()).padding(top = 8.dp)) {
                    FilterChip(selected = group.isBlank(), onClick = { group = "" }, label = { Text("Без группы") }, modifier = Modifier.padding(end = 6.dp))
                    groups.forEach { g ->
                        FilterChip(selected = group == g, onClick = { group = g }, label = { Text(g) }, modifier = Modifier.padding(end = 6.dp))
                    }
                }
                Text(
                    "Перенос в группу ничего на хосте не меняет — профиль группы к нему не применяется.",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(top = 8.dp),
                )
            }
        },
        confirmButton = { TextButton(onClick = { onSet(group) }) { Text("Сохранить") } },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Отмена") } },
    )
}

/** Groups: create, rename, delete (its hosts move to «Без группы»). */
@Composable
fun GroupsDialog(
    groups: List<String>,
    onDismiss: () -> Unit,
    onCreate: (String) -> Unit,
    onRename: (String, String) -> Unit,
    onDelete: (String) -> Unit,
) {
    var newName by remember { mutableStateOf("") }
    var renaming by remember { mutableStateOf<String?>(null) }
    var renameTo by remember { mutableStateOf("") }
    var deleting by remember { mutableStateOf<String?>(null) }
    deleting?.let { g ->
        AlertDialog(
            onDismissRequest = { deleting = null },
            text = { Text("Удалить группу «$g»? Её хосты останутся — без группы.") },
            confirmButton = {
                TextButton(onClick = {
                    onDelete(g)
                    deleting = null
                }) { Text("Удалить") }
            },
            dismissButton = { TextButton(onClick = { deleting = null }) { Text("Отмена") } },
        )
    }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("Группы хостов") },
        text = {
            Column(modifier = Modifier.verticalScroll(rememberScrollState())) {
                if (groups.isEmpty()) Text("Групп пока нет")
                groups.forEach { g ->
                    if (renaming == g) {
                        Row(verticalAlignment = Alignment.CenterVertically) {
                            OutlinedTextField(value = renameTo, onValueChange = { renameTo = it }, singleLine = true, modifier = Modifier.weight(1f))
                            TextButton(enabled = renameTo.isNotBlank() && renameTo != g, onClick = {
                                onRename(g, renameTo)
                                renaming = null
                            }) { Text("OK") }
                        }
                    } else {
                        Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.fillMaxWidth()) {
                            Text(g, modifier = Modifier.weight(1f))
                            TextButton(onClick = {
                                renaming = g
                                renameTo = g
                            }) { Text("Переименовать") }
                            TextButton(onClick = { deleting = g }) { Text("Удалить") }
                        }
                    }
                }
                Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.padding(top = 8.dp)) {
                    OutlinedTextField(
                        value = newName, onValueChange = { newName = it },
                        label = { Text("Новая группа") }, singleLine = true, modifier = Modifier.weight(1f),
                    )
                    TextButton(enabled = newName.isNotBlank() && newName.trim() !in groups, onClick = {
                        onCreate(newName)
                        newName = ""
                    }) { Text("Создать") }
                }
            }
        },
        confirmButton = { TextButton(onClick = onDismiss) { Text("Закрыть") } },
    )
}
