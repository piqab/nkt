package com.netknownsthat.ui.hub

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Card
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.netknownsthat.ui.i18n.t

/** Hub sections that do not fit the navigation bar. */
@Composable
fun MoreScreen(onFail2ban: () -> Unit, onDeployments: () -> Unit, onAbout: () -> Unit) {
    Column(modifier = Modifier.padding(16.dp)) {
        listOf(
            Triple("Fail2ban", t("Забаненные адреса всех хостов, бан и разбан везде", "Banned addresses of every host, ban and unban everywhere"), onFail2ban),
            Triple(t("Выкладка", "Deployments"), t("Конвейеры: выложить, сухой прогон, история и откат", "Pipelines: deploy, dry run, history and rollback"), onDeployments),
            Triple(t("О системе", "About"), t("Версия хаба и приложения, язык, сертификат, выход", "Hub and app version, language, certificate, sign out"), onAbout),
        ).forEach { (title, hint, open) ->
            Card(onClick = open, modifier = Modifier.fillMaxWidth().padding(bottom = 8.dp)) {
                Column(modifier = Modifier.padding(16.dp)) {
                    Text(title, style = MaterialTheme.typography.titleMedium)
                    Text(hint, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
            }
        }
    }
}
