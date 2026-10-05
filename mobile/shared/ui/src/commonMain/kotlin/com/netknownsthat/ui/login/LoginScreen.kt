package com.netknownsthat.ui.login

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.verticalScroll
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardCapitalization
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import com.netknownsthat.ui.session.AuthViewModel
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import com.netknownsthat.domain.model.Me
import com.netknownsthat.ui.i18n.t

/**
 * The only screen reachable before a session exists — sets the hub's own
 * address (typically an SSH-tunnelled loopback address or a LAN address,
 * see network_security_config.xml's own comment on why cleartext is
 * allowed) and logs in with the same account a browser tab would use.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun LoginScreen(
    viewModel: AuthViewModel,
    onLoggedIn: (Me) -> Unit,
    onLanguage: (com.netknownsthat.domain.repository.AppLanguage) -> Unit,
) {
    val state = viewModel.uiState

    Column(
        // Scrolls, so that with the keyboard open (the shell pads for it)
        // the password field and the button are still reachable.
        modifier = Modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(24.dp),
        verticalArrangement = Arrangement.Center,
    ) {
        Text(
            text = "NetKnownsThat",
            style = MaterialTheme.typography.headlineMedium,
        )
        Text(
            text = t("Вход в хаб", "Sign in to the hub"),
            style = MaterialTheme.typography.bodyMedium,
            modifier = Modifier.padding(bottom = 8.dp),
        )
        com.netknownsthat.ui.i18n.LanguagePicker(onLanguage, modifier = Modifier.padding(bottom = 16.dp))

        OutlinedTextField(
            value = state.hubUrl,
            onValueChange = viewModel::onHubUrlChange,
            label = { Text(t("Адрес хаба", "Hub address")) },
            placeholder = { Text("http://127.0.0.1:8077") },
            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri, autoCorrectEnabled = false, imeAction = ImeAction.Next),
            singleLine = true,
            modifier = Modifier
                .fillMaxWidth()
                .padding(bottom = 8.dp),
        )
        OutlinedTextField(
            value = state.username,
            onValueChange = viewModel::onUsernameChange,
            label = { Text(t("Логин", "Login")) },
            singleLine = true,
            // Logins are case-sensitive: no capital first letter, no autocorrect.
            keyboardOptions = KeyboardOptions(
                capitalization = KeyboardCapitalization.None,
                autoCorrectEnabled = false,
                keyboardType = KeyboardType.Ascii,
                imeAction = ImeAction.Next,
            ),
            modifier = Modifier
                .fillMaxWidth()
                .padding(bottom = 8.dp),
        )
        OutlinedTextField(
            value = state.password,
            onValueChange = viewModel::onPasswordChange,
            label = { Text(t("Пароль", "Password")) },
            singleLine = true,
            visualTransformation = PasswordVisualTransformation(),
            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password, imeAction = ImeAction.Done),
            keyboardActions = KeyboardActions(onDone = { viewModel.login(onLoggedIn) }),
            modifier = Modifier
                .fillMaxWidth()
                .padding(bottom = 16.dp),
        )

        if (state.error != null) {
            Text(
                text = state.error,
                color = MaterialTheme.colorScheme.error,
                modifier = Modifier.padding(bottom = 16.dp),
            )
        }

        Button(
            onClick = { viewModel.login(onLoggedIn) },
            enabled = !state.loading,
            modifier = Modifier
                .fillMaxWidth()
                .align(Alignment.CenterHorizontally),
        ) {
            if (state.loading) {
                CircularProgressIndicator(modifier = Modifier.padding(end = 8.dp))
            }
            Text(t("Войти", "Sign in"))
        }
    }
}
