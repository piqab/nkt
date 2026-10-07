package com.netknownsthat.ui.hub

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.Badge
import androidx.compose.material3.BadgedBox
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.netknownsthat.domain.common.Outcome
import com.netknownsthat.domain.model.ActiveJob
import com.netknownsthat.domain.repository.JobsRepository
import com.netknownsthat.ui.i18n.t
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch

/** How often the indicator asks the hub. */
private const val ACTIVE_JOBS_POLL_MS = 8_000L

/**
 * The background operations indicator, as in the web interface: jobs
 * queued or running right now on the hub and on every host. Without it a
 * job started from one screen was visible only in that host's "Jobs".
 */
class ActiveJobsViewModel(private val jobs: JobsRepository) : ViewModel() {
    var list by mutableStateOf<List<ActiveJob>>(emptyList())
        private set

    private var polling = false

    /** Polls while the app is signed in; a failed poll keeps the last list. */
    fun start() {
        if (polling) return
        polling = true
        viewModelScope.launch {
            while (isActive) {
                (jobs.active() as? Outcome.Success)?.let { list = it.value }
                delay(ACTIVE_JOBS_POLL_MS)
            }
        }
    }
}

/** Top bar icon with the count; the list opens a job's live log. */
@Composable
fun ActiveJobsButton(viewModel: ActiveJobsViewModel, onOpen: (ActiveJob) -> Unit) {
    LaunchedEffect(Unit) { viewModel.start() }
    val list = viewModel.list
    if (list.isEmpty()) return
    var open by remember { mutableStateOf(false) }
    Box {
        IconButton(onClick = { open = true }) {
            BadgedBox(badge = { Badge { Text(list.size.toString()) } }) {
                Icon(Icons.Default.Refresh, contentDescription = t("Фоновые операции", "Background operations"))
            }
        }
        DropdownMenu(expanded = open, onDismissRequest = { open = false }, modifier = Modifier.width(320.dp)) {
            Text(
                t("Фоновые операции", "Background operations"),
                style = MaterialTheme.typography.titleSmall,
                modifier = Modifier.padding(horizontal = 16.dp, vertical = 8.dp),
            )
            list.forEach { a ->
                DropdownMenuItem(
                    text = {
                        Column(Modifier.fillMaxWidth()) {
                            Text("${a.hostName} · ${a.job.title}", maxLines = 1, overflow = TextOverflow.Ellipsis)
                            Text(
                                if (a.job.status == "queued") t("в очереди", "queued") else a.job.stepName.ifBlank { t("идёт", "running") },
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                                maxLines = 1,
                                overflow = TextOverflow.Ellipsis,
                            )
                            if (a.job.status == "running" && a.job.steps > 0) {
                                LinearProgressIndicator(
                                    progress = { (a.job.step.toFloat() / a.job.steps).coerceIn(0f, 1f) },
                                    modifier = Modifier.fillMaxWidth().padding(top = 4.dp),
                                )
                            }
                        }
                    },
                    onClick = {
                        open = false
                        onOpen(a)
                    },
                )
            }
        }
    }
}
