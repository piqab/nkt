package com.netknownsthat.data.dto

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/*
 * Hub-level screens (alerts, jobs, fail2ban, monitoring, deployments) and the
 * host-level jobs/fail2ban/reboot ones. Field names mirror the Go structs
 * 1:1 — store.HostEvent, store.JobDto, store.JobLogLineDto, store.PipelineDto,
 * store.DeploymentDto — and the hand-written maps of the handlers that return
 * them (internal/hub/handlers.go handleEvents, internal/api/handlers_jobs.go,
 * internal/api/handlers_reboot.go, internal/hub/monitoring.go).
 */

/** GET /api/hub/events. */
@Serializable
data class HubEventsResponseDto(
    val events: List<HubEventDto> = emptyList(),
    val unread: Int = 0,
    val total: Int = 0,
    val hosts: List<String> = emptyList(),
    val kinds: List<String> = emptyList(),
    /** Which kinds the hub notifies about (Slack/Telegram) — the phone
     * follows the same choice. */
    val notify: Map<String, Boolean> = emptyMap(),
)

@Serializable
data class HubEventDto(
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
data class JobListResponseDto(
    val jobs: List<JobRecordDto> = emptyList(),
    val total: Int = 0,
    val active: Int = 0,
    val kinds: List<String> = emptyList(),
)

/** GET /hub/jobs/active. */
@Serializable
data class ActiveJobsResponseDto(val jobs: List<ActiveJobDto> = emptyList())

@Serializable
data class ActiveJobDto(
    @SerialName("host_id") val hostId: Long = 0,
    @SerialName("host_name") val hostName: String = "",
    val job: JobRecordDto = JobRecordDto(),
)

@Serializable
data class JobRecordDto(
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
data class JobLogResponseDto(
    val job: JobRecordDto = JobRecordDto(),
    val lines: List<JobLogLineDto> = emptyList(),
)

@Serializable
data class JobLogLineDto(
    val seq: Long = 0,
    val ts: String = "",
    val text: String = "",
)

/** Answer of every endpoint that starts a job: {"job_id": N}. */
@Serializable
data class JobIdResponseDto(
    @SerialName("job_id") val jobId: Long = 0,
    @SerialName("deployment_id") val deploymentId: Long = 0,
)

/** GET /api/fail2ban (host). */
@Serializable
data class Fail2banResponseDto(
    val state: Fail2banStateDto = Fail2banStateDto(),
    @SerialName("manual_jail") val manualJail: String = "",
    @SerialName("manual_ready") val manualReady: Boolean = false,
    @SerialName("hub_addr") val hubAddr: String = "",
    @SerialName("client_ip") val clientIp: String = "",
    val simulated: Boolean = false,
)

@Serializable
data class Fail2banStateDto(
    val installed: Boolean = false,
    val running: Boolean = false,
    val version: String = "",
    val jails: List<Fail2banJailDto> = emptyList(),
)

@Serializable
data class Fail2banJailDto(
    val name: String = "",
    @SerialName("max_retry") val maxRetry: Int = 0,
    @SerialName("find_time") val findTime: Long = 0,
    @SerialName("ban_time") val banTime: Long = 0,
    val failed: Int = 0,
    @SerialName("total_failed") val totalFailed: Int = 0,
    val banned: Int = 0,
    @SerialName("total_banned") val totalBanned: Int = 0,
    val bans: List<Fail2banBanDto> = emptyList(),
)

@Serializable
data class Fail2banBanDto(
    val ip: String = "",
    val jail: String = "",
    val since: String = "",
    val until: String = "",
)

/** GET /api/hub/fail2ban/banned — every banned address across the fleet. */
@Serializable
data class FleetBannedResponseDto(
    val hosts: List<FleetF2BHostDto> = emptyList(),
    val ips: List<FleetBannedIPDto> = emptyList(),
)

@Serializable
data class FleetF2BHostDto(
    val id: Long = 0,
    val name: String = "",
    val installed: Boolean = false,
    val running: Boolean = false,
    val banned: Int = 0,
    val error: String = "",
)

@Serializable
data class FleetBannedIPDto(
    val ip: String = "",
    val hosts: List<FleetBannedOnDto> = emptyList(),
)

@Serializable
data class FleetBannedOnDto(
    val id: Long = 0,
    val name: String = "",
    val jails: List<String> = emptyList(),
)

/** GET /api/system/reboot/preview. */
@Serializable
data class RebootPreviewDto(
    @SerialName("reboot_required") val rebootRequired: Boolean = false,
    val running: Map<String, Int> = emptyMap(),
    @SerialName("no_autostart") val noAutostart: List<RebootNoAutoDto> = emptyList(),
    val simulated: Boolean = false,
)

@Serializable
data class RebootNoAutoDto(
    val kind: String = "",
    val name: String = "",
    val reason: String = "",
)

/** GET /api/hub/monitoring/overview — only the parts a phone shows: per-host
 * load, insights (forecasts/drops) and the k8s clusters. */
@Serializable
data class MonitoringOverviewDto(
    val hosts: List<MonHostDto> = emptyList(),
    val insights: List<MonInsightDto> = emptyList(),
    val clusters: List<MonClusterDto> = emptyList(),
    val collecting: Boolean = false,
    @SerialName("last_run") val lastRun: String = "",
)

@Serializable
data class MonHostDto(
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
    val disks: List<MonDiskDto> = emptyList(),
    val workloads: Int = 0,
    @SerialName("k8s_role") val k8sRole: String = "",
    @SerialName("k8s_node") val k8sNode: String = "",
)

@Serializable
data class MonDiskDto(
    val mount: String = "",
    val used: Double = 0.0,
    val size: Double = 0.0,
    val pct: Double = 0.0,
    /** Days until full at the current growth; absent when not growing. */
    @SerialName("eta_days") val etaDays: Double? = null,
)

@Serializable
data class MonInsightDto(
    val kind: String = "",
    val severity: String = "",
    @SerialName("host_id") val hostId: Long = 0,
    val host: String = "",
    val text: String = "",
    /** Host section with focus, same form as [HubEventDto.link]. */
    val path: String = "",
)

@Serializable
data class MonClusterDto(
    val name: String = "",
    @SerialName("host_id") val hostId: Long = 0,
    val nodes: List<MonClusterNodeDto> = emptyList(),
)

@Serializable
data class MonClusterNodeDto(
    val name: String = "",
    val ip: String = "",
    val ready: Boolean = false,
    @SerialName("control_plane") val controlPlane: Boolean = false,
)

/** GET /api/hub/pipelines. */
@Serializable
data class PipelinesResponseDto(val pipelines: List<PipelineDto> = emptyList())

@Serializable
data class PipelineDto(
    val id: Long = 0,
    val name: String = "",
    val enabled: Boolean = false,
    val action: String = "",
    @SerialName("last_commit") val lastCommit: String = "",
    @SerialName("last_tag") val lastTag: String = "",
    val last: DeploymentDto? = null,
    val leftovers: List<LeftoverStackDto> = emptyList(),
)

@Serializable
data class DeploymentDto(
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
data class DeploymentsResponseDto(val deployments: List<DeploymentDto> = emptyList())

/** An old stack left on the previous host after a deployment moved. */
@Serializable
data class LeftoverStackDto(
    @SerialName("host_id") val hostId: Long = 0,
    val host: String = "",
    val project: String = "",
    val reason: String = "",
    val at: String = "",
)

/** GET /api/containers/{name}/inspect[?reveal=1] — values of [env] are
 * masked unless an admin asked to reveal them (audited on the host). */
@Serializable
data class ContainerInspectDto(
    val name: String = "",
    val id: String = "",
    val image: String = "",
    @SerialName("image_id") val imageId: String = "",
    @SerialName("image_outdated") val imageOutdated: Boolean = false,
    val state: String = "",
    val health: String = "",
    @SerialName("exit_code") val exitCode: Int = 0,
    val created: String = "",
    @SerialName("started_at") val startedAt: String = "",
    @SerialName("restart_count") val restartCount: Int = 0,
    @SerialName("restart_policy") val restartPolicy: String = "",
    @SerialName("memory_limit") val memoryLimit: Long = 0,
    @SerialName("nano_cpus") val nanoCpus: Long = 0,
    val entrypoint: List<String>? = null,
    val cmd: List<String>? = null,
    val user: String = "",
    @SerialName("working_dir") val workingDir: String = "",
    @SerialName("compose_project") val composeProject: String = "",
    @SerialName("compose_service") val composeService: String = "",
    val env: List<InspectEnvDto> = emptyList(),
    val revealed: Boolean = false,
    val ports: List<InspectPortDto> = emptyList(),
    val mounts: List<InspectMountDto> = emptyList(),
    val networks: List<InspectNetDto> = emptyList(),
    val labels: Map<String, String> = emptyMap(),
)

@Serializable
data class InspectEnvDto(
    val name: String = "",
    val value: String = "",
    val masked: Boolean = false,
    /** container | override | image */
    val origin: String = "",
)

@Serializable
data class InspectPortDto(
    val container: String = "",
    @SerialName("host_ip") val hostIp: String = "",
    @SerialName("host_port") val hostPort: String = "",
)

@Serializable
data class InspectMountDto(
    val type: String = "",
    val source: String = "",
    val name: String = "",
    val destination: String = "",
    val rw: Boolean = false,
)

@Serializable
data class InspectNetDto(
    val name: String = "",
    val ip: String = "",
    val gateway: String = "",
    val aliases: List<String> = emptyList(),
)

/** GET /api/services/{name}/installed — a live check, not the snapshot. */
@Serializable
data class InstalledResponseDto(val installed: Boolean = false)

/** GET /api/clamav — everything the ClamAV card needs in one call. */
@Serializable
data class ClamResponseDto(
    val status: ClamStatusDto = ClamStatusDto(),
    val running: Boolean = false,
    val op: String = "",
    @SerialName("job_id") val jobId: Long = 0,
    @SerialName("host_scan") val hostScan: ClamScanDto? = null,
    @SerialName("image_scan") val imageScan: ClamScanDto? = null,
    val paths: List<String> = emptyList(),
    val images: List<String> = emptyList(),
    val apt: Boolean = false,
)

@Serializable
data class ClamStatusDto(
    val installed: Boolean = false,
    val version: String = "",
    @SerialName("db_version") val dbVersion: String = "",
    @SerialName("db_date") val dbDate: String = "",
    @SerialName("db_present") val dbPresent: Boolean = false,
    @SerialName("freshclam_active") val freshclamActive: Boolean = false,
)

@Serializable
data class ClamScanDto(
    val kind: String = "",
    val targets: List<String> = emptyList(),
    @SerialName("finished_at") val finishedAt: String = "",
    val scanned: Int = 0,
    val hits: List<ClamHitDto> = emptyList(),
    val error: String = "",
)

@Serializable
data class ClamHitDto(
    val path: String = "",
    val signature: String = "",
    val target: String = "",
)

/** GET /api/hub/groups. */
@Serializable
data class HostGroupsResponseDto(val groups: List<String> = emptyList())

/** POST /api/hub/hosts — `authorized_key` only for auth_kind "generated":
 * the line to put into the host's authorized_keys before installing. */
@Serializable
data class AddHostResponseDto(
    val id: Long = 0,
    @SerialName("authorized_key") val authorizedKey: String = "",
)

/** POST /api/hub/hosts/{id}/install and GET …/install/latest: {"job": N}. */
@Serializable
data class InstallJobResponseDto(val job: Long = 0)

/** DELETE /hub/hosts/{id}: with a cleanup, the hub job that does it. */
@Serializable
data class DeleteHostResponseDto(@SerialName("job_id") val jobId: Long? = null)
