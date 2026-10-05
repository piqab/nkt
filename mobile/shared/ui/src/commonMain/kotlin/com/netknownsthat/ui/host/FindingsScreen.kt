package com.netknownsthat.ui.host

import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.material3.Card
import androidx.compose.material3.FilterChip
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.netknownsthat.domain.model.Finding
import com.netknownsthat.ui.i18n.t
import com.netknownsthat.ui.common.SectionContent

private val SEVERITY_ORDER = listOf("critical", "high", "medium", "low", "info")

private val SEVERITY_LABELS get() = mapOf(
    "critical" to t("Критичные", "Critical"),
    "high" to t("Высокие", "High"),
    "medium" to t("Средние", "Medium"),
    "low" to t("Низкие", "Low"),
    "info" to t("Информационные", "Info"),
)

/** A jump from a finding: a host section, and for configs the file to open. */
data class FindingJump(val label: String, val section: com.netknownsthat.domain.usecase.HostSectionKey, val configPath: String? = null)

private val FIREWALL_RULES = setOf(
    "sensitive-port-public",
    "docker-bypasses-firewall",
    "public-port-blocked",
    "no-default-deny",
    "stale-firewall-rule",
    "k8s-port-bypasses-firewall",
    "admin-interface-open",
)

private val SERVICE_PAGE_NAMES = setOf("nginx", "haproxy", "caddy", "docker", "podman", "lxd", "libvirt", "ufw", "firewalld", "fail2ban")

/** Same rules as web/src/components/FindingActions.tsx's findingActions —
 * the section where a finding is dealt with. */
fun findingJumps(f: Finding): List<FindingJump> {
    val out = mutableListOf<FindingJump>()
    val obj = f.`object`.orEmpty()
    val file = f.file.orEmpty()
    if (f.rule.startsWith("tls-cert-") && obj.isNotEmpty()) out += FindingJump(t("К сертификату", "To certificate"), com.netknownsthat.domain.usecase.HostSectionKey.CERTIFICATES)
    if (f.service == "docker" && obj.isNotEmpty() && f.rule.startsWith("container-")) {
        out += FindingJump(t("К контейнеру $obj", "To container $obj"), com.netknownsthat.domain.usecase.HostSectionKey.CONTAINERS)
    } else if (f.service in SERVICE_PAGE_NAMES) {
        out += FindingJump(t("К службе ${f.service}", "To service ${f.service}"), com.netknownsthat.domain.usecase.HostSectionKey.SERVICES)
    }
    if (f.rule in FIREWALL_RULES) out += FindingJump("Firewall", com.netknownsthat.domain.usecase.HostSectionKey.FIREWALL)
    if (f.rule.startsWith("malware-")) out += FindingJump(t("К вредоносному", "To malware"), com.netknownsthat.domain.usecase.HostSectionKey.MALWARE)
    if (f.rule == "listening-not-declared" && obj.isNotEmpty()) {
        out += FindingJump(t("Порт ${obj.substringAfterLast(':')} в Firewall", "Port ${obj.substringAfterLast(':')} in Firewall"), com.netknownsthat.domain.usecase.HostSectionKey.FIREWALL)
        out += FindingJump(t("На карте ресурсов", "On the resource map"), com.netknownsthat.domain.usecase.HostSectionKey.TOPOLOGY)
    }
    if (f.rule == "fail2ban") out += FindingJump("Fail2ban", com.netknownsthat.domain.usecase.HostSectionKey.FAIL2BAN)
    if (file.isNotEmpty() && !f.rule.startsWith("malware-") && !f.rule.startsWith("tls-cert-")) {
        out += FindingJump(if (f.line > 0) t("Открыть на строке ${f.line}", "Open at line ${f.line}") else t("Открыть файл", "Open file"), com.netknownsthat.domain.usecase.HostSectionKey.CONFIGS, file)
    }
    return out
}

@Composable
fun FindingsScreen(viewModel: FindingsViewModel, onJump: (FindingJump) -> Unit = {}) {
    var severityFilter by remember { mutableStateOf<String?>(null) }

    SectionContent(
        state = viewModel.state,
        emptyText = t("Проблем не найдено", "No problems found"),
        isEmpty = { it.findings.isEmpty() },
    ) { response ->
        // Filtering client-side rather than refetching with ?severity=: the
        // whole list is already here, and a round trip per chip tap would
        // make the filter feel worse than it is.
        val visible = response.findings.filter {
            severityFilter == null || it.severity == severityFilter
        }

        Column {
            Row(
                modifier = Modifier
                    .horizontalScroll(rememberScrollState())
                    .padding(horizontal = 16.dp, vertical = 8.dp),
            ) {
                SEVERITY_ORDER.filter { (response.counts[it] ?: 0) > 0 }.forEach { severity ->
                    FilterChip(
                        selected = severityFilter == severity,
                        onClick = {
                            severityFilter = if (severityFilter == severity) null else severity
                        },
                        label = {
                            Text("${SEVERITY_LABELS[severity] ?: severity} ${response.counts[severity]}")
                        },
                        modifier = Modifier.padding(end = 8.dp),
                    )
                }
            }

            LazyColumn(contentPadding = PaddingValues(16.dp)) {
                items(visible, key = { it.id }) { FindingCard(it, onJump) }
            }
        }
    }
}

@Composable
private fun FindingCard(finding: Finding, onJump: (FindingJump) -> Unit) {
    Card(modifier = Modifier.fillMaxWidth().padding(bottom = 8.dp)) {
        Column(modifier = Modifier.padding(16.dp)) {
            Text(
                text = SEVERITY_LABELS[finding.severity] ?: finding.severity,
                style = MaterialTheme.typography.labelMedium,
                color = when (finding.severity) {
                    "critical", "high" -> MaterialTheme.colorScheme.error
                    "medium" -> MaterialTheme.colorScheme.tertiary
                    else -> MaterialTheme.colorScheme.onSurfaceVariant
                },
            )
            Text(
                text = finding.title,
                style = MaterialTheme.typography.titleSmall,
                modifier = Modifier.padding(top = 4.dp),
            )
            if (finding.detail.isNotBlank()) {
                Text(
                    text = finding.detail,
                    style = MaterialTheme.typography.bodyMedium,
                    modifier = Modifier.padding(top = 8.dp),
                )
            }
            finding.suggestion?.takeIf { it.isNotBlank() }?.let {
                Text(
                    text = t("Что сделать: $it", "What to do: $it"),
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.primary,
                    modifier = Modifier.padding(top = 8.dp),
                )
            }
            val location = listOfNotNull(
                finding.service.takeIf { it.isNotBlank() },
                finding.file?.takeIf { it.isNotBlank() }
                    ?.let { if (finding.line > 0) "$it:${finding.line}" else it },
            ).joinToString(" · ")
            if (location.isNotEmpty()) {
                Text(
                    text = location,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(top = 8.dp),
                )
            }
            val jumps = findingJumps(finding)
            if (jumps.isNotEmpty()) {
                Row(modifier = Modifier.horizontalScroll(rememberScrollState()).padding(top = 4.dp)) {
                    jumps.forEach { j -> TextButton(onClick = { onJump(j) }) { Text(j.label) } }
                }
            }
        }
    }
}
