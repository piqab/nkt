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
import com.netknownsthat.app.i18n.t

private val AUTH_KINDS get() = listOf(
    "password" to t("Пароль", "Password"),
    "key" to t("Свой ключ", "Own key"),
    "generated" to t("Ключ хаба", "Hub key"),
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
        title = { Text(t("Новый хост", "New host")) },
        text = {
            Column(modifier = Modifier.verticalScroll(rememberScrollState())) {
                OutlinedTextField(
                    value = form.name, onValueChange = { form = form.copy(name = it) },
                    label = { Text(t("Имя", "Name")) }, singleLine = true,
                    isError = form.name.isNotBlank() && !NAME_RE.matches(form.name.trim()),
                )
                OutlinedTextField(
                    value = form.addr, onValueChange = { form = form.copy(addr = it) },
                    label = { Text(t("Адрес (IP или имя)", "Address (IP or name)")) }, singleLine = true,
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri),
                )
                Row {
                    OutlinedTextField(
                        value = form.user, onValueChange = { form = form.copy(user = it) },
                        label = { Text(t("Пользователь SSH", "SSH user")) }, singleLine = true,
                        modifier = Modifier.weight(1f).padding(end = 8.dp),
                    )
                    OutlinedTextField(
                        value = form.port.toString(),
                        onValueChange = { v -> v.toIntOrNull()?.let { form = form.copy(port = it) } ?: run { if (v.isEmpty()) form = form.copy(port = 0) } },
                        label = { Text(t("Порт", "Port")) }, singleLine = true,
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
                        label = { Text(t("Пароль", "Password")) }, singleLine = true,
                        visualTransformation = PasswordVisualTransformation(),
                        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password),
                    )
                    "key" -> OutlinedTextField(
                        value = form.secret, onValueChange = { form = form.copy(secret = it) },
                        label = { Text(t("Закрытый ключ (PEM)", "Private key (PEM)")) }, minLines = 3, maxLines = 6,
                    )
                    else -> Text(
                        t("Хаб создаст ключ и покажет строку для authorized_keys на хосте", "The hub will create a key and show the line for authorized_keys on the host"),
                        style = MaterialTheme.typography.bodySmall,
                    )
                }
                OutlinedTextField(
                    value = form.group, onValueChange = { form = form.copy(group = it) },
                    label = { Text(t("Группа (пусто — без группы)", "Group (empty — no group)")) }, singleLine = true,
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
                CheckRow(t("Терминал на хосте", "Terminal on the host"), form.terminal) { form = form.copy(terminal = it) }
                if (form.authKind != "generated") CheckRow(t("Сразу установить nkt", "Install nkt right away"), install) { install = it }
            }
        },
        confirmButton = { TextButton(enabled = valid, onClick = { onAdd(form, install) }) { Text(t("Добавить", "Add")) } },
        dismissButton = { TextButton(onClick = onDismiss) { Text(t("Отмена", "Cancel")) } },
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
        title = { Text(t("Ключ хаба", "Hub key")) },
        text = {
            Column {
                Text(
                    t("Добавьте строку в ~/.ssh/authorized_keys пользователя SSH на хосте, затем «Установить nkt» в меню хоста.", "Add the line to ~/.ssh/authorized_keys of the SSH user on the host, then “Install nkt” in the host menu."),
                    style = MaterialTheme.typography.bodySmall,
                )
                SelectionContainer {
                    Text(key, fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.bodySmall, modifier = Modifier.padding(top = 8.dp))
                }
            }
        },
        confirmButton = {
            TextButton(onClick = { clipboard.setText(AnnotatedString(key)) }) { Text(t("Копировать", "Copy")) }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text(t("Готово", "Done")) } },
    )
}

/** Delete: only the hub's record, or also nkt on the host. */
@Composable
fun DeleteHostDialog(host: HubHost, onDismiss: () -> Unit, onDelete: (purge: Boolean) -> Unit) {
    var purge by remember { mutableStateOf(false) }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(t("Удалить ${host.name}?", "Delete ${host.name}?")) },
        text = {
            Column {
                CheckRow(t("Убрать nkt и с самого хоста", "Also remove nkt from the host itself"), purge) { purge = it }
                Text(
                    if (purge) t("Служба, данные, правило sudo и ключ хаба будут удалены с хоста, вход по паролю — возвращён. Запись в хабе удаляется в любом случае, даже если хост не ответит.", "The service, data, sudo rule and hub key will be removed from the host, password sign-in restored. The hub record is deleted in any case, even if the host does not answer.")
                    else t("Удаляется только запись в хабе; nkt на хосте продолжит работать.", "Only the hub record is deleted; nkt on the host keeps running."),
                    style = MaterialTheme.typography.bodySmall,
                )
            }
        },
        confirmButton = {
            TextButton(onClick = { onDelete(purge) }) { Text(t("Удалить", "Delete"), color = MaterialTheme.colorScheme.error) }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text(t("Отмена", "Cancel")) } },
    )
}

/** Move a host to a group (or out of any). */
@Composable
fun SetGroupDialog(host: HubHost, groups: List<String>, onDismiss: () -> Unit, onSet: (String) -> Unit) {
    var group by remember { mutableStateOf(host.group) }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(t("Группа ${host.name}", "Group of ${host.name}")) },
        text = {
            Column {
                OutlinedTextField(value = group, onValueChange = { group = it }, label = { Text(t("Группа", "Group")) }, singleLine = true)
                Row(modifier = Modifier.horizontalScroll(rememberScrollState()).padding(top = 8.dp)) {
                    FilterChip(selected = group.isBlank(), onClick = { group = "" }, label = { Text(t("Без группы", "No group")) }, modifier = Modifier.padding(end = 6.dp))
                    groups.forEach { g ->
                        FilterChip(selected = group == g, onClick = { group = g }, label = { Text(g) }, modifier = Modifier.padding(end = 6.dp))
                    }
                }
                Text(
                    t("Перенос в группу ничего на хосте не меняет — профиль группы к нему не применяется.", "Moving to a group changes nothing on the host — the group's profile is not applied to it."),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(top = 8.dp),
                )
            }
        },
        confirmButton = { TextButton(onClick = { onSet(group) }) { Text(t("Сохранить", "Save")) } },
        dismissButton = { TextButton(onClick = onDismiss) { Text(t("Отмена", "Cancel")) } },
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
            text = { Text(t("Удалить группу «$g»? Её хосты останутся — без группы.", "Delete group “$g”? Its hosts stay — without a group.")) },
            confirmButton = {
                TextButton(onClick = {
                    onDelete(g)
                    deleting = null
                }) { Text(t("Удалить", "Delete")) }
            },
            dismissButton = { TextButton(onClick = { deleting = null }) { Text(t("Отмена", "Cancel")) } },
        )
    }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(t("Группы хостов", "Host groups")) },
        text = {
            Column(modifier = Modifier.verticalScroll(rememberScrollState())) {
                if (groups.isEmpty()) Text(t("Групп пока нет", "No groups yet"))
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
                            }) { Text(t("Переименовать", "Rename")) }
                            TextButton(onClick = { deleting = g }) { Text(t("Удалить", "Delete")) }
                        }
                    }
                }
                Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.padding(top = 8.dp)) {
                    OutlinedTextField(
                        value = newName, onValueChange = { newName = it },
                        label = { Text(t("Новая группа", "New group")) }, singleLine = true, modifier = Modifier.weight(1f),
                    )
                    TextButton(enabled = newName.isNotBlank() && newName.trim() !in groups, onClick = {
                        onCreate(newName)
                        newName = ""
                    }) { Text(t("Создать", "Create")) }
                }
            }
        },
        confirmButton = { TextButton(onClick = onDismiss) { Text(t("Закрыть", "Close")) } },
    )
}
