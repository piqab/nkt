package com.netknownsthat.ui.hub

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.netknownsthat.domain.common.HostTarget
import com.netknownsthat.domain.common.JobOwner
import com.netknownsthat.domain.common.Outcome
import com.netknownsthat.domain.model.ClamResponse
import com.netknownsthat.domain.model.DeploymentsResponse
import com.netknownsthat.domain.model.Fail2banResponse
import com.netknownsthat.domain.model.FleetBannedResponse
import com.netknownsthat.domain.model.HubEventsResponse
import com.netknownsthat.domain.model.JobListResponse
import com.netknownsthat.domain.model.JobLogLine
import com.netknownsthat.domain.model.JobRecord
import com.netknownsthat.domain.model.MonitoringOverview
import com.netknownsthat.domain.model.PipelinesResponse
import com.netknownsthat.domain.model.RebootPreview
import com.netknownsthat.domain.repository.DeploymentsRepository
import com.netknownsthat.domain.repository.EventsRepository
import com.netknownsthat.domain.repository.FleetFail2banRepository
import com.netknownsthat.domain.repository.JobsRepository
import com.netknownsthat.domain.repository.MonitoringRepository
import com.netknownsthat.domain.repository.SecurityRepository
import com.netknownsthat.domain.repository.SystemRepository
import com.netknownsthat.domain.usecase.WatchJobUseCase
import com.netknownsthat.ui.common.SectionViewModel
import com.netknownsthat.ui.common.failedText
import com.netknownsthat.ui.common.text
import com.netknownsthat.ui.i18n.t
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch

/**
 * One job's live log — the phone's JobLogModal, used by the job page.
 * Follows the log until the job finishes (WatchJobUseCase).
 */
class JobLogViewModel(
    private val owner: JobOwner,
    private val jobId: Long,
    private val jobs: JobsRepository,
    private val watch: WatchJobUseCase,
) : ViewModel() {
    var job by mutableStateOf<JobRecord?>(null)
        private set
    var error by mutableStateOf<String?>(null)
        private set
    var canceling by mutableStateOf(false)
        private set
    val lines = mutableStateListOf<JobLogLine>()
    private var follower: Job? = null

    fun start() {
        if (follower?.isActive == true) return
        lines.clear()
        follower = viewModelScope.launch {
            watch(owner, jobId).collect { p ->
                error = p.error?.text()
                p.job?.let { job = it }
                lines.addAll(p.newLines)
                // A phone has no use for a hundred thousand lines; the tail
                // is what is being watched.
                if (lines.size > MAX_LINES) lines.removeRange(0, lines.size - MAX_LINES)
            }
        }
    }

    fun cancel() {
        if (canceling) return
        viewModelScope.launch {
            canceling = true
            (jobs.cancel(owner, jobId) as? Outcome.Failure)?.let { error = it.error.text() }
            canceling = false
        }
    }

    private companion object {
        const val MAX_LINES = 3_000
    }
}

/** Jobs list of a host or of the hub. */
class JobsViewModel(val owner: JobOwner, private val jobs: JobsRepository) : SectionViewModel<JobListResponse>() {
    /** Comma-separated status filter, empty — every status. */
    var statusFilter by mutableStateOf("")

    override suspend fun fetch() = jobs.jobs(owner, statusFilter, 100)

    fun cancel(id: Long) = act(t("Задание отменяется", "Cancelling the job")) { jobs.cancel(owner, id) }
    fun retry(id: Long) = act(t("Задание запущено снова", "Job restarted")) { jobs.retry(owner, id) }
}

/** The hub's alert journal. */
class EventsViewModel(private val events: EventsRepository) : SectionViewModel<HubEventsResponse>() {
    /** Kind filter; null — every kind. */
    var kindFilter by mutableStateOf<String?>(null)

    override suspend fun fetch(): Outcome<HubEventsResponse> {
        val r = events.events(limit = 200, kind = kindFilter)
        // Opening the journal is reading it — same as the web UI's bell.
        if (r is Outcome.Success && r.value.unread > 0) events.markSeen()
        return r
    }
}

/** Host fail2ban: jails, current bans, manual ban and unban. */
class Fail2banViewModel(private val host: HostTarget, private val repo: SecurityRepository) : SectionViewModel<Fail2banResponse>() {
    override suspend fun fetch() = repo.fail2ban(host)
    fun ban(ip: String, banTimeSeconds: Long) = act(t("$ip забанен", "$ip banned")) { repo.ban(host, ip, banTimeSeconds) }
    fun unban(ip: String, jail: String) = act(t("$ip разбанен", "$ip unbanned")) { repo.unban(host, ip, jail) }
}

/** Starts a job and reports its id (to open its log) or the failure. */
private fun SectionViewModel<*>.startJob(scope: kotlinx.coroutines.CoroutineScope, onJob: (Long) -> Unit, call: suspend () -> Outcome<Long>) {
    scope.launch {
        when (val r = call()) {
            is Outcome.Success -> { onJob(r.value); load() }
            is Outcome.Failure -> actionMessage = failedText(r.error)
        }
    }
}

/** Fleet fail2ban on the hub: who is banned where; ban/unban everywhere. */
class FleetFail2banViewModel(private val repo: FleetFail2banRepository) : SectionViewModel<FleetBannedResponse>() {
    override suspend fun fetch() = repo.banned()

    fun fleet(action: String, ips: List<String>, banTimeSeconds: Long, hostIds: List<Long>, onJob: (Long) -> Unit) =
        startJob(viewModelScope, onJob) { repo.fleet(action, ips, banTimeSeconds, hostIds) }
}

class MonitoringViewModel(private val repo: MonitoringRepository) : SectionViewModel<MonitoringOverview>() {
    override suspend fun fetch() = repo.overview()
    fun collect() = act(t("Сбор запущен", "Collection started")) { repo.collectNow() }
}

class DeploymentsViewModel(private val repo: DeploymentsRepository) : SectionViewModel<PipelinesResponse>() {
    override suspend fun fetch() = repo.pipelines()

    fun deploy(id: Long, onJob: (Long) -> Unit) = startJob(viewModelScope, onJob) { repo.deploy(id) }
    fun dryRun(id: Long, onJob: (Long) -> Unit) = startJob(viewModelScope, onJob) { repo.dryRun(id) }

    /** Removes an old stack left on a previous host, or only forgets it. */
    fun leftover(id: Long, hostId: Long, project: String, forget: Boolean) =
        act(if (forget) t("Забыто", "Forgotten") else t("Старый стек убирается", "Removing the old stack")) {
            repo.removeLeftover(id, hostId, project, forget)
        }
}

/** One pipeline: its deployment history and rollback. */
class PipelineViewModel(private val pipelineId: Long, private val repo: DeploymentsRepository) : SectionViewModel<DeploymentsResponse>() {
    override suspend fun fetch() = repo.history(pipelineId)
    fun rollback(deploymentId: Long, onJob: (Long) -> Unit) = startJob(viewModelScope, onJob) { repo.rollback(pipelineId, deploymentId) }
}

/** Host reboot: what will not come back by itself, then the reboot. */
class RebootViewModel(private val host: HostTarget, private val repo: SystemRepository) : ViewModel() {
    var preview by mutableStateOf<RebootPreview?>(null)
        private set
    var loading by mutableStateOf(false)
        private set
    var message by mutableStateOf<String?>(null)

    fun loadPreview() {
        viewModelScope.launch {
            loading = true
            preview = null
            when (val r = repo.rebootPreview(host)) {
                is Outcome.Success -> preview = r.value
                is Outcome.Failure -> message = failedText(r.error)
            }
            loading = false
        }
    }

    fun reboot(onDone: () -> Unit) {
        viewModelScope.launch {
            loading = true
            message = when (val r = repo.reboot(host)) {
                is Outcome.Success ->
                    if (preview?.simulated == true) t("Перезагрузка (стенд — не выполняется)", "Reboot (fixtures stand — not performed)")
                    else t("Хост перезагружается", "The host is rebooting")
                is Outcome.Failure -> failedText(r.error)
            }
            loading = false
            onDone()
        }
    }
}

/** Host ClamAV: install, signature update and scans run as host jobs. */
class ClamAVViewModel(private val host: HostTarget, private val repo: SecurityRepository) : SectionViewModel<ClamResponse>() {
    override suspend fun fetch() = repo.clamav(host)
    fun install(onJob: (Long) -> Unit) = startJob(viewModelScope, onJob) { repo.clamavJob(host, "install") }
    fun updateDb(onJob: (Long) -> Unit) = startJob(viewModelScope, onJob) { repo.clamavJob(host, "update-db") }
    fun scanHost(onJob: (Long) -> Unit) = startJob(viewModelScope, onJob) { repo.clamavJob(host, "scan") }
    fun scanImages(onJob: (Long) -> Unit) = startJob(viewModelScope, onJob) { repo.clamavJob(host, "scan-images") }
}
