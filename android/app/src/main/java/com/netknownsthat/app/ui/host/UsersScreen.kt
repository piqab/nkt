package com.netknownsthat.app.ui.host

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Card
import androidx.compose.material3.FilterChip
import androidx.compose.material3.FloatingActionButton
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
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
import androidx.compose.ui.unit.dp
import com.netknownsthat.app.net.model.User
import com.netknownsthat.app.status.userHealth
import com.netknownsthat.app.ui.theme.StatusDot
import com.netknownsthat.app.ui.theme.statusColor

@Composable
fun UsersScreen(viewModel: UsersViewModel) {
    var showCreate by remember { mutableStateOf(false) }
    var pendingDelete by remember { mutableStateOf<User?>(null) }

    if (showCreate) {
        CreateUserDialog(
            onDismiss = { showCreate = false },
            onCreate = { name, password, role ->
                viewModel.create(name, password, role)
                showCreate = false
            },
        )
    }

    pendingDelete?.let { user ->
        AlertDialog(
            onDismissRequest = { pendingDelete = null },
            title = { Text("Удалить пользователя") },
            text = { Text("${user.username} будет удалён безвозвратно.") },
            confirmButton = {
                TextButton(onClick = {
                    viewModel.delete(user.username)
                    pendingDelete = null
                }) { Text("Удалить") }
            },
            dismissButton = { TextButton(onClick = { pendingDelete = null }) { Text("Отмена") } },
        )
    }

    androidx.compose.foundation.layout.Box(modifier = Modifier.fillMaxWidth()) {
        SectionContent(
            state = viewModel.state,
            emptyText = "Пользователей нет",
            isEmpty = { it.users.isEmpty() },
        ) { response ->
            LazyColumn(contentPadding = PaddingValues(16.dp)) {
                items(response.users, key = { it.id }) { user ->
                    UserCard(
                        user = user,
                        enabled = !viewModel.actionInProgress,
                        onToggleDisabled = { viewModel.setDisabled(user.username, !user.disabled) },
                        onDelete = { pendingDelete = user },
                    )
                }
            }
        }
        FloatingActionButton(
            onClick = { showCreate = true },
            modifier = Modifier.align(Alignment.BottomEnd).padding(16.dp),
        ) { Icon(Icons.Default.Add, contentDescription = "Добавить пользователя") }
    }
}

@Composable
private fun UserCard(
    user: User,
    enabled: Boolean,
    onToggleDisabled: () -> Unit,
    onDelete: () -> Unit,
) {
    Card(modifier = Modifier.fillMaxWidth().padding(bottom = 8.dp)) {
        Column(modifier = Modifier.padding(16.dp)) {
            Row(
                modifier = Modifier.fillMaxWidth(),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                StatusDot(userHealth(user.disabled))
                Text(
                    text = user.username,
                    style = MaterialTheme.typography.titleSmall,
                    modifier = Modifier.weight(1f),
                )
                Text(
                    text = user.role,
                    style = MaterialTheme.typography.labelMedium,
                    color = MaterialTheme.colorScheme.primary,
                )
            }
            if (user.disabled) {
                // Grey, not red: an account switched off on purpose is not a
                // fault.
                Text(
                    text = "Отключён",
                    style = MaterialTheme.typography.bodySmall,
                    color = statusColor(userHealth(true)),
                )
            }
            val details = listOfNotNull(
                user.createdAt.takeIf { it.isNotBlank() }?.let { "создан $it" },
                user.lastLoginAt.takeIf { it.isNotBlank() }?.let { "вход $it" },
            ).joinToString(" · ")
            if (details.isNotEmpty()) {
                Text(
                    text = details,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(top = 4.dp),
                )
            }
            Row(modifier = Modifier.padding(top = 12.dp)) {
                OutlinedButton(
                    onClick = onToggleDisabled,
                    enabled = enabled,
                    modifier = Modifier.padding(end = 8.dp),
                ) { Text(if (user.disabled) "Включить" else "Отключить") }
                OutlinedButton(onClick = onDelete, enabled = enabled) { Text("Удалить") }
            }
        }
    }
}

@Composable
private fun CreateUserDialog(
    onDismiss: () -> Unit,
    onCreate: (String, String, String) -> Unit,
) {
    var username by remember { mutableStateOf("") }
    var password by remember { mutableStateOf("") }
    var role by remember { mutableStateOf("viewer") }

    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("Новый пользователь") },
        text = {
            Column {
                OutlinedTextField(
                    value = username,
                    onValueChange = { username = it },
                    label = { Text("Логин") },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                )
                OutlinedTextField(
                    value = password,
                    onValueChange = { password = it },
                    label = { Text("Пароль") },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth().padding(top = 8.dp),
                )
                Row(modifier = Modifier.padding(top = 12.dp)) {
                    listOf("viewer" to "Наблюдатель", "admin" to "Администратор")
                        .forEach { (value, label) ->
                            FilterChip(
                                selected = role == value,
                                onClick = { role = value },
                                label = { Text(label) },
                                modifier = Modifier.padding(end = 8.dp),
                            )
                        }
                }
            }
        },
        confirmButton = {
            TextButton(
                onClick = { onCreate(username, password, role) },
                enabled = username.isNotBlank() && password.isNotBlank(),
            ) { Text("Создать") }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Отмена") } },
    )
}
