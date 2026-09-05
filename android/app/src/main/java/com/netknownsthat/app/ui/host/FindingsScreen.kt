package com.netknownsthat.app.ui.host

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
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.netknownsthat.app.net.model.Finding

private val SEVERITY_ORDER = listOf("critical", "high", "medium", "low", "info")

private val SEVERITY_LABELS = mapOf(
    "critical" to "Критичные",
    "high" to "Высокие",
    "medium" to "Средние",
    "low" to "Низкие",
    "info" to "Информационные",
)

@Composable
fun FindingsScreen(viewModel: FindingsViewModel) {
    var severityFilter by remember { mutableStateOf<String?>(null) }

    SectionContent(
        state = viewModel.state,
        emptyText = "Проблем не найдено",
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
                items(visible, key = { it.id }) { FindingCard(it) }
            }
        }
    }
}

@Composable
private fun FindingCard(finding: Finding) {
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
                    text = "Что сделать: $it",
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
        }
    }
}
