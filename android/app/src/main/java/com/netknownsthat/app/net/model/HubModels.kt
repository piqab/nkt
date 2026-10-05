package com.netknownsthat.app.net.model

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/*
 * Hub-level screens (alerts, jobs, fail2ban, monitoring, deployments) and the
 * host-level jobs/fail2ban/reboot ones. Field names mirror the Go structs
 * 1:1 — store.HostEvent, store.Job, store.JobLogLine, store.Pipeline,
 * store.Deployment — and the hand-written maps of the handlers that return
 * them (internal/hub/handlers.go handleEvents, internal/api/handlers_jobs.go,
 * internal/api/handlers_reboot.go, internal/hub/monitoring.go).
 */

/** GET /api/hub/events. */
@Serializable
data class HubEventsResponse(
    val events: List<HubEvent> = emptyList(),
    val unread: Int = 0,
    val total: Int = 0,
    val hosts: List<String> = emptyList(),
    val kinds: List<String> = emptyList(),
    /** Which kinds the hub notifies about (Slack/Telegram) — the phone
     * follows the same choice. */
    val notify: Map<String, Boolean> = emptyMap(),
)

@Serializable
data class HubEvent(
    val id: Long = 0,
    val ts: String = "",
    @SerialName("host_id") val hostId: Long = 0,
    @SerialName("host_name") val hostName: String = "",
    @SerialName("host_addr") val hostAddr: String = "",
    val kind: String = "",
    val severity: String = "",
    val detail: String = "",
    /** Host section where the event is seen, e.g. "/findings?focus=…";
     * empty on older events — the section is then derived from [kind]. */
    val link: String = "",
)

/** GET /api/jobs (host) or /api/hosts/local/jobs (the hub's own jobs). */
@Serializable
data class JobListResponse(
    val jobs: List<JobRecord> = emptyList(),
    val total: Int = 0,
    val active: Int = 0,
    val kinds: List<String> = emptyList(),
)

@Serializable
data class JobRecord(
    val id: Long = 0,
    val kind: String = "",
    val title: String = "",
    val queue: String = "",
    val status: String = "",
    val step: Int = 0,
    val steps: Int = 0,
    @SerialName("step_name") val stepName: String = "",
    val error: String = "",
    val author: String = "",
    @SerialName("created_at") val createdAt: String = "",
    @SerialName("started_at") val startedAt: String = "",
    @SerialName("finished_at") val finishedAt: String = "",
) {
    val finished: Boolean
        get() = status == "succeeded" || status == "failed" || status == "canceled" || status == "interrupted"
}

/** GET /api/jobs/{id}/log?after=N — the job plus lines after seq N. */
@Serializable
data class JobLogResponse(
    val job: JobRecord = JobRecord(),
    val lines: List<JobLogLine> = emptyList(),
)

@Serializable
data class JobLogLine(
    val seq: Long = 0,
    val ts: String = "",
    val text: String = "",
)

/** Answer of every endpoint that starts a job: {"job_id": N}. */
@Serializable
data class JobIdResponse(
    @SerialName("job_id") val jobId: Long = 0,
    @SerialName("deployment_id") val deploymentId: Long = 0,
)

/** GET /api/fail2ban (host). */
@Serializable
data class Fail2banResponse(
    val state: Fail2banState = Fail2banState(),
    @SerialName("manual_jail") val manualJail: String = "",
    @SerialName("manual_ready") val manualReady: Boolean = false,
    @SerialName("hub_addr") val hubAddr: String = "",
    @SerialName("client_ip") val clientIp: String = "",
    val simulated: Boolean = false,
)

@Serializable
data class Fail2banState(
    val installed: Boolean = false,
    val running: Boolean = false,
    val version: String = "",
    val jails: List<Fail2banJail> = emptyList(),
)

@Serializable
data class Fail2banJail(
    val name: String = "",
    @SerialName("max_retry") val maxRetry: Int = 0,
    @SerialName("find_time") val findTime: Long = 0,
    @SerialName("ban_time") val banTime: Long = 0,
    val failed: Int = 0,
    @SerialName("total_failed") val totalFailed: Int = 0,
    val banned: Int = 0,
    @SerialName("total_banned") val totalBanned: Int = 0,
    val bans: List<Fail2banBan> = emptyList(),
)

@Serializable
data class Fail2banBan(
    val ip: String = "",
    val jail: String = "",
    val since: String = "",
    val until: String = "",
)

/** GET /api/hub/fail2ban/banned — every banned address across the fleet. */
@Serializable
data class FleetBannedResponse(
    val hosts: List<FleetF2BHost> = emptyList(),
    val ips: List<FleetBannedIP> = emptyList(),
)

@Serializable
data class FleetF2BHost(
    val id: Long = 0,
    val name: String = "",
    val installed: Boolean = false,
    val running: Boolean = false,
    val banned: Int = 0,
    val error: String = "",
)

@Serializable
data class FleetBannedIP(
    val ip: String = "",
    val hosts: List<FleetBannedOn> = emptyList(),
)

@Serializable
data class FleetBannedOn(
    val id: Long = 0,
    val name: String = "",
    val jails: List<String> = emptyList(),
)

/** GET /api/system/reboot/preview. */
@Serializable
data class RebootPreview(
    @SerialName("reboot_required") val rebootRequired: Boolean = false,
    val running: Map<String, Int> = emptyMap(),
    @SerialName("no_autostart") val noAutostart: List<RebootNoAuto> = emptyList(),
    val simulated: Boolean = false,
)

@Serializable
data class RebootNoAuto(
    val kind: String = "",
    val name: String = "",
    val reason: String = "",
)

/** GET /api/hub/monitoring/overview — only the parts a phone shows: per-host
 * load, insights (forecasts/drops) and the k8s clusters. */
@Serializable
data class MonitoringOverview(
    val hosts: List<MonHost> = emptyList(),
    val insights: List<MonInsight> = emptyList(),
    val clusters: List<MonCluster> = emptyList(),
    val collecting: Boolean = false,
    @SerialName("last_run") val lastRun: String = "",
)

@Serializable
data class MonHost(
    val id: Long = 0,
    val name: String = "",
    val group: String = "",
    val reachable: Boolean = true,
    @SerialName("has_data") val hasData: Boolean = false,
    @SerialName("cpu_now") val cpuNow: Double = 0.0,
    @SerialName("cpu_avg") val cpuAvg: Double = 0.0,
    @SerialName("cpu_max") val cpuMax: Double = 0.0,
    @SerialName("mem_used") val memUsed: Double = 0.0,
    @SerialName("mem_total") val memTotal: Double = 0.0,
    @SerialName("mem_avg_pct") val memAvgPct: Double = 0.0,
    @SerialName("load_avg") val loadAvg: Double = 0.0,
    val disks: List<MonDisk> = emptyList(),
    val workloads: Int = 0,
    @SerialName("k8s_role") val k8sRole: String = "",
    @SerialName("k8s_node") val k8sNode: String = "",
)

@Serializable
data class MonDisk(
    val mount: String = "",
    val used: Double = 0.0,
    val size: Double = 0.0,
    val pct: Double = 0.0,
    /** Days until full at the current growth; absent when not growing. */
    @SerialName("eta_days") val etaDays: Double? = null,
)

@Serializable
data class MonInsight(
    val kind: String = "",
    val severity: String = "",
    @SerialName("host_id") val hostId: Long = 0,
    val host: String = "",
    val text: String = "",
    /** Host section with focus, same form as [HubEvent.link]. */
    val path: String = "",
)

@Serializable
data class MonCluster(
    val name: String = "",
    @SerialName("host_id") val hostId: Long = 0,
    val nodes: List<MonClusterNode> = emptyList(),
)

@Serializable
data class MonClusterNode(
    val name: String = "",
    val ip: String = "",
    val ready: Boolean = false,
    @SerialName("control_plane") val controlPlane: Boolean = false,
)

/** GET /api/hub/pipelines. */
@Serializable
data class PipelinesResponse(val pipelines: List<Pipeline> = emptyList())

@Serializable
data class Pipeline(
    val id: Long = 0,
    val name: String = "",
    val enabled: Boolean = false,
    val action: String = "",
    @SerialName("last_commit") val lastCommit: String = "",
    @SerialName("last_tag") val lastTag: String = "",
    val last: Deployment? = null,
    val leftovers: List<LeftoverStack> = emptyList(),
)

@Serializable
data class Deployment(
    val id: Long = 0,
    val ref: String = "",
    val commit: String = "",
    val tag: String = "",
    val trigger: String = "",
    val author: String = "",
    @SerialName("job_id") val jobId: Long = 0,
    val status: String = "",
    val error: String = "",
    @SerialName("created_at") val createdAt: String = "",
    @SerialName("finished_at") val finishedAt: String = "",
)

@Serializable
data class DeploymentsResponse(val deployments: List<Deployment> = emptyList())

/** An old stack left on the previous host after a deployment moved. */
@Serializable
data class LeftoverStack(
    @SerialName("host_id") val hostId: Long = 0,
    val host: String = "",
    val project: String = "",
    val reason: String = "",
    val at: String = "",
)
