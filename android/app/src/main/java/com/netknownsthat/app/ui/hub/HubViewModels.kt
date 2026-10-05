package com.netknownsthat.app.ui.hub

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.netknownsthat.app.net.HubClient
import com.netknownsthat.app.net.model.DeploymentsResponse
import com.netknownsthat.app.net.model.Fail2banResponse
import com.netknownsthat.app.net.model.FleetBannedResponse
import com.netknownsthat.app.net.model.HubEventsResponse
import com.netknownsthat.app.net.model.JobIdResponse
import com.netknownsthat.app.net.model.JobListResponse
import com.netknownsthat.app.net.model.JobLogLine
import com.netknownsthat.app.net.model.JobLogResponse
import com.netknownsthat.app.net.model.JobRecord
import com.netknownsthat.app.net.model.MonitoringOverview
import com.netknownsthat.app.net.model.PipelinesResponse
import com.netknownsthat.app.net.model.RebootPreview
import com.netknownsthat.app.ui.host.SectionViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.add
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import kotlinx.serialization.json.putJsonArray
import com.netknownsthat.app.i18n.t

/**
 * Where a job lives. Host jobs go through the host scope like every other
 * host call ("/jobs" → "/hosts/3/jobs"); the hub's own jobs (deployments,
 * fleet bans) are the hub machine's local API — "/hosts/local/jobs", which
 * is exactly where the web UI reads them too.
 */
enum class JobPlace(val prefix: String) {
    HOST(""),
    HUB("/hosts/local"),
}

/**
 * One live job log at a time, shared by every screen that starts or opens a
 * job — the phone equivalent of the web UI's JobLogModal. Polls
 * /jobs/{id}/log?after=N (the same incremental read the hub itself uses to
 * follow a host's job, see internal/hub/hostcall.go) until the job finishes.
 */
class JobLogViewModel(private val hubClient: HubClient) : ViewModel() {
    var open by mutableStateOf(false)
        private set
    var job by mutableStateOf<JobRecord?>(null)
        private set
    var error by mutableStateOf<String?>(null)
        private set
    var canceling by mutableStateOf(false)
        private set
    val lines = mutableStateListOf<JobLogLine>()

    private var place = JobPlace.HOST
    private var jobId = 0L
    private var poller: Job? = null

    fun show(place: JobPlace, id: Long) {
        poller?.cancel()
        this.place = place
        jobId = id
        job = null
        error = null
        canceling = false
        lines.clear()
        open = true
        poller = viewModelScope.launch {
            var after = 0L
            while (isActive) {
                when (val r = hubClient.get<JobLogResponse>("${place.prefix}/jobs/$id/log?after=$after")) {
                    is HubClient.ApiResult.Success -> {
                        error = null
                        job = r.value.job
                        r.value.lines.forEach { after = maxOf(after, it.seq) }
                        lines.addAll(r.value.lines)
                        // A phone has no use for a hundred thousand lines;
                        // the tail is what is being watched.
                        if (lines.size > MAX_LINES) lines.removeRange(0, lines.size - MAX_LINES)
                        if (r.value.job.finished && r.value.lines.isEmpty()) break
                    }
                    is HubClient.ApiResult.Failure -> error = r.message
                }
                delay(POLL_MS)
            }
        }
    }

    fun cancel() {
        if (canceling) return
        viewModelScope.launch {
            canceling = true
            val r = hubClient.post<Unit>("${place.prefix}/jobs/$jobId/cancel")
            if (r is HubClient.ApiResult.Failure) error = r.message
            canceling = false
        }
    }

    fun close() {
        poller?.cancel()
        open = false
    }

    private companion object {
        const val POLL_MS = 1_500L
        const val MAX_LINES = 3_000
    }
}

/** Jobs list of a host or of the hub (see [JobPlace]). */
abstract class JobsViewModel(hubClient: HubClient, val place: JobPlace) :
    SectionViewModel<JobListResponse>(hubClient) {
    /** Comma-separated status filter, empty — every status. */
    var statusFilter by mutableStateOf("")

    override suspend fun fetch(): HubClient.ApiResult<JobListResponse> {
        val status = if (statusFilter.isEmpty()) "" else "&status=$statusFilter"
        return hubClient.get("${place.prefix}/jobs?limit=100$status")
    }

    fun cancel(id: Long) = act(t("Задание отменяется", "Cancelling the job")) {
        hubClient.post<Unit>("${place.prefix}/jobs/$id/cancel")
    }

    fun retry(id: Long) = act(t("Задание запущено снова", "Job restarted")) {
        hubClient.post<Unit>("${place.prefix}/jobs/$id/retry")
    }
}

class HostJobsViewModel(hubClient: HubClient) : JobsViewModel(hubClient, JobPlace.HOST)
class HubJobsViewModel(hubClient: HubClient) : JobsViewModel(hubClient, JobPlace.HUB)

/** Hub alerts journal (web: «Оповещения → Журнал»). */
class EventsViewModel(hubClient: HubClient) : SectionViewModel<HubEventsResponse>(hubClient) {
    /** Kind filter; null — every kind. */
    var kindFilter by mutableStateOf<String?>(null)

    override suspend fun fetch(): HubClient.ApiResult<HubEventsResponse> {
        val kind = kindFilter?.let { "&kind=$it" } ?: ""
        val r = hubClient.get<HubEventsResponse>("/hub/events?limit=200$kind")
        // Opening the journal is reading it — same as the web UI's bell.
        if (r is HubClient.ApiResult.Success && r.value.unread > 0) hubClient.post<Unit>("/hub/events/seen")
        return r
    }
}

/** Host fail2ban: jails, current bans, manual ban and unban. */
class Fail2banViewModel(hubClient: HubClient) : SectionViewModel<Fail2banResponse>(hubClient) {
    override suspend fun fetch() = hubClient.get<Fail2banResponse>("/fail2ban")

    fun ban(ip: String, banTimeSeconds: Long) = act(t("$ip забанен", "$ip banned")) {
        hubClient.post<Unit>(
            "/fail2ban/ban",
            buildJsonObject {
                putJsonArray("ips") { add(ip) }
                put("ban_time", banTimeSeconds)
            }.toString(),
        )
    }

    fun unban(ip: String, jail: String) = act(t("$ip разбанен", "$ip unbanned")) {
        hubClient.post<Unit>(
            "/fail2ban/unban",
            buildJsonObject {
                put("jail", jail)
                putJsonArray("ips") { add(ip) }
            }.toString(),
        )
    }
}

/** Fleet fail2ban on the hub: who is banned where, ban/unban everywhere. */
class FleetFail2banViewModel(hubClient: HubClient) : SectionViewModel<FleetBannedResponse>(hubClient) {
    override suspend fun fetch() = hubClient.get<FleetBannedResponse>("/hub/fail2ban/banned")

    /** Starts the hub job; [onJob] opens its log. Empty [hostIds] — every
     * host with fail2ban. */
    fun fleet(action: String, ips: List<String>, banTimeSeconds: Long, hostIds: List<Long>, onJob: (Long) -> Unit) =
        startJob(onJob) {
            hubClient.post<JobIdResponse>(
                "/hub/fail2ban/fleet",
                buildJsonObject {
                    put("action", action)
                    putJsonArray("ips") { ips.forEach { add(it) } }
                    if (banTimeSeconds > 0) put("ban_time", banTimeSeconds)
                    putJsonArray("host_ids") { hostIds.forEach { add(JsonPrimitive(it)) } }
                }.toString(),
            )
        }

    private fun startJob(onJob: (Long) -> Unit, call: suspend () -> HubClient.ApiResult<JobIdResponse>) {
        viewModelScope.launch {
            when (val r = call()) {
                is HubClient.ApiResult.Success -> onJob(r.value.jobId)
                is HubClient.ApiResult.Failure -> actionMessage = t("Не удалось: ${r.message}", "Failed: ${r.message}")
            }
        }
    }
}

/** Hub monitoring overview: per-host load, forecasts, k8s clusters. */
class MonitoringViewModel(hubClient: HubClient) : SectionViewModel<MonitoringOverview>(hubClient) {
    override suspend fun fetch() = hubClient.get<MonitoringOverview>("/hub/monitoring/overview")

    fun collect() = act(t("Сбор запущен", "Collection started")) { hubClient.post<Unit>("/hub/monitoring/collect") }
}

/** Deployments (pipelines): deploy, dry run, history, leftovers. */
class DeploymentsViewModel(hubClient: HubClient) : SectionViewModel<PipelinesResponse>(hubClient) {
    override suspend fun fetch() = hubClient.get<PipelinesResponse>("/hub/pipelines")

    /** History of the pipeline open in the history sheet. */
    var history by mutableStateOf<Pair<Long, DeploymentsResponse?>?>(null)
        private set

    fun openHistory(id: Long) {
        history = id to null
        viewModelScope.launch {
            when (val r = hubClient.get<DeploymentsResponse>("/hub/pipelines/$id/deployments")) {
                is HubClient.ApiResult.Success -> if (history?.first == id) history = id to r.value
                is HubClient.ApiResult.Failure -> {
                    actionMessage = t("Не удалось: ${r.message}", "Failed: ${r.message}")
                    history = null
                }
            }
        }
    }

    fun closeHistory() {
        history = null
    }

    fun deploy(id: Long, onJob: (Long) -> Unit) = startJob(onJob) {
        hubClient.post<JobIdResponse>("/hub/pipelines/$id/deploy", "{}")
    }

    fun dryRun(id: Long, onJob: (Long) -> Unit) = startJob(onJob) {
        hubClient.post<JobIdResponse>("/hub/pipelines/dryrun", buildJsonObject { put("pipeline_id", id) }.toString())
    }

    fun rollback(id: Long, deploymentId: Long, onJob: (Long) -> Unit) = startJob(onJob) {
        hubClient.post<JobIdResponse>(
            "/hub/pipelines/$id/rollback",
            buildJsonObject { put("deployment_id", deploymentId) }.toString(),
        )
    }

    /** Removes the old stack left on a previous host, or only forgets it
     * ([forget]) when the host is gone for good. */
    fun leftover(id: Long, hostId: Long, project: String, forget: Boolean) =
        act(if (forget) t("Забыто", "Forgotten") else t("Старый стек убирается", "Removing the old stack")) {
            hubClient.post<Unit>(
                "/hub/pipelines/$id/leftovers/remove",
                buildJsonObject {
                    put("host_id", hostId)
                    put("project", project)
                    put("forget", forget)
                }.toString(),
            )
        }

    private fun startJob(onJob: (Long) -> Unit, call: suspend () -> HubClient.ApiResult<JobIdResponse>) {
        viewModelScope.launch {
            when (val r = call()) {
                is HubClient.ApiResult.Success -> {
                    onJob(r.value.jobId)
                    load()
                }
                is HubClient.ApiResult.Failure -> actionMessage = t("Не удалось: ${r.message}", "Failed: ${r.message}")
            }
        }
    }
}

/** Host reboot: what will not come back by itself, then the reboot. */
class RebootViewModel(private val hubClient: HubClient) : ViewModel() {
    var preview by mutableStateOf<RebootPreview?>(null)
        private set
    var loading by mutableStateOf(false)
        private set
    var message by mutableStateOf<String?>(null)

    fun loadPreview() {
        viewModelScope.launch {
            loading = true
            preview = null
            when (val r = hubClient.get<RebootPreview>("/system/reboot/preview")) {
                is HubClient.ApiResult.Success -> preview = r.value
                is HubClient.ApiResult.Failure -> message = t("Не удалось: ${r.message}", "Failed: ${r.message}")
            }
            loading = false
        }
    }

    fun reboot(onDone: () -> Unit) {
        viewModelScope.launch {
            loading = true
            message = when (val r = hubClient.post<Unit>("/system/reboot", buildJsonObject { put("confirm", true) }.toString())) {
                is HubClient.ApiResult.Success ->
                    if (preview?.simulated == true) t("Перезагрузка (стенд — не выполняется)", "Reboot (fixtures stand — not performed)") else t("Хост перезагружается", "The host is rebooting")
                is HubClient.ApiResult.Failure -> t("Не удалось: ${r.message}", "Failed: ${r.message}")
            }
            loading = false
            onDone()
        }
    }
}

/** Host ClamAV: install, signature update and scans all run as host jobs;
 * [onJob] opens the live log. */
class ClamAVViewModel(hubClient: HubClient) : SectionViewModel<com.netknownsthat.app.net.model.ClamResponse>(hubClient) {
    override suspend fun fetch() = hubClient.get<com.netknownsthat.app.net.model.ClamResponse>("/clamav")

    fun install(onJob: (Long) -> Unit) = start("/clamav/install", "{}", onJob)
    fun updateDb(onJob: (Long) -> Unit) = start("/clamav/update-db", "{}", onJob)
    fun scanHost(onJob: (Long) -> Unit) = start("/clamav/scan", """{"paths":[]}""", onJob)
    fun scanImages(onJob: (Long) -> Unit) = start("/clamav/scan-images", """{"images":[]}""", onJob)

    private fun start(path: String, body: String, onJob: (Long) -> Unit) {
        viewModelScope.launch {
            when (val r = hubClient.post<JobIdResponse>(path, body)) {
                is HubClient.ApiResult.Success -> onJob(r.value.jobId)
                is HubClient.ApiResult.Failure -> actionMessage = t("Не удалось: ${r.message}", "Failed: ${r.message}")
            }
            load()
        }
    }
}
