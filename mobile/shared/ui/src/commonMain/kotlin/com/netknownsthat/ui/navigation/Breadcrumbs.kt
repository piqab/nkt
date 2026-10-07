package com.netknownsthat.ui.navigation

import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.navigation.NavBackStackEntry
import androidx.navigation.NavHostController
import androidx.navigation.toRoute
import androidx.navigation.NavDestination.Companion.hasRoute
import com.netknownsthat.domain.usecase.HostSectionKey
import com.netknownsthat.ui.i18n.t

/** Name of a back stack entry as a crumb; null — not shown. */
fun crumbOf(entry: NavBackStackEntry): String? {
    val d = entry.destination
    return when {
        d.hasRoute<HostsRoute>() -> t("Хосты", "Hosts")
        d.hasRoute<EventsRoute>() -> t("Оповещения", "Alerts")
        d.hasRoute<MonitoringRoute>() -> t("Мониторинг", "Monitoring")
        d.hasRoute<HubJobsRoute>() -> t("Задания хаба", "Hub jobs")
        d.hasRoute<MoreRoute>() -> t("Ещё", "More")
        d.hasRoute<FleetFail2banRoute>() -> "Fail2ban"
        d.hasRoute<DeploymentsRoute>() -> t("Выкладка", "Deployments")
        d.hasRoute<AboutRoute>() -> t("О системе", "About")
        d.hasRoute<HostRoute>() -> entry.toRoute<HostRoute>().hostName
        d.hasRoute<HostSectionRoute>() -> sectionTitle(HostSectionKey.valueOf(entry.toRoute<HostSectionRoute>().section))
        d.hasRoute<ContainerRoute>() -> entry.toRoute<ContainerRoute>().container
        d.hasRoute<ConsoleRoute>() -> t("Консоль ${entry.toRoute<ConsoleRoute>().name}", "Console ${entry.toRoute<ConsoleRoute>().name}")
        d.hasRoute<ConfigFileRoute>() -> entry.toRoute<ConfigFileRoute>().path.substringAfterLast('/')
        d.hasRoute<JobRoute>() -> t("Задание #${entry.toRoute<JobRoute>().jobId}", "Job #${entry.toRoute<JobRoute>().jobId}")
        d.hasRoute<PipelineRoute>() -> entry.toRoute<PipelineRoute>().name
        else -> null
    }
}

/** Section names, the same as in the web UI's menu. */
fun sectionTitle(s: HostSectionKey): String = when (s) {
    HostSectionKey.OVERVIEW -> t("Обзор", "Overview")
    HostSectionKey.FINDINGS -> t("Проблемы", "Problems")
    HostSectionKey.TERMINAL -> t("Терминал", "Terminal")
    HostSectionKey.BTOP -> t("Монитор (btop)", "Monitor (btop)")
    HostSectionKey.LOGS -> t("Логи", "Logs")
    HostSectionKey.SERVICES -> t("Сервисы", "Services")
    HostSectionKey.CONTAINERS -> t("Контейнеры", "Containers")
    HostSectionKey.VULNERABILITIES -> t("Уязвимости", "Vulnerabilities")
    HostSectionKey.MALWARE -> t("Вредоносное (ClamAV)", "Malware (ClamAV)")
    HostSectionKey.AVAILABILITY -> t("Доступность", "Availability")
    HostSectionKey.USAGE -> t("Нагрузка", "Usage")
    HostSectionKey.CONFIGS -> t("Конфигурация", "Configuration")
    HostSectionKey.FIREWALL -> "Firewall"
    HostSectionKey.CERTIFICATES -> t("Сертификаты", "Certificates")
    HostSectionKey.INTERFACES -> t("Интерфейсы", "Interfaces")
    HostSectionKey.MISC -> t("Разное", "Misc")
    HostSectionKey.TOPOLOGY -> t("Карта", "Map")
    HostSectionKey.USERS -> t("Пользователи", "Users")
    HostSectionKey.JOBS -> t("Задания", "Jobs")
    HostSectionKey.FAIL2BAN -> "Fail2ban"
    HostSectionKey.AUDIT -> t("Журнал", "Log")
}

/**
 * "Hosts › web-1 › Containers › acme-app" — the back stack, read from the
 * bottom. Every crumb but the last goes back to its level; the row scrolls
 * to its end so the current place is always visible.
 */
@Composable
fun Breadcrumbs(nav: NavHostController, modifier: Modifier = Modifier) {
    val stack by nav.currentBackStack.collectAsState()
    val crumbs = stack.mapNotNull { entry -> crumbOf(entry)?.let { entry to it } }
    if (crumbs.size < 2) return
    val scroll = rememberScrollState()
    LaunchedEffect(crumbs.size) { scroll.scrollTo(scroll.maxValue) }
    Row(
        verticalAlignment = Alignment.CenterVertically,
        modifier = modifier.fillMaxWidth().horizontalScroll(scroll).padding(horizontal = 8.dp),
    ) {
        crumbs.forEachIndexed { i, (entry, label) ->
            val last = i == crumbs.lastIndex
            if (i > 0) Text("›", color = MaterialTheme.colorScheme.onSurfaceVariant)
            if (last) {
                Text(
                    label,
                    style = MaterialTheme.typography.labelLarge,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.padding(horizontal = 8.dp),
                )
            } else {
                TextButton(onClick = { nav.popBackStack(entry.destination.id, inclusive = false) }) {
                    Text(label, maxLines = 1, overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.labelLarge)
                }
            }
        }
    }
}
