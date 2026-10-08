package com.netknownsthat.domain.repository

import com.netknownsthat.domain.model.ActiveJob
import com.netknownsthat.domain.model.NewTarget
import com.netknownsthat.domain.common.HostTarget
import com.netknownsthat.domain.common.JobOwner
import com.netknownsthat.domain.common.Outcome
import com.netknownsthat.domain.model.AuditResponse
import com.netknownsthat.domain.model.CertificatesResponse
import com.netknownsthat.domain.model.ClamResponse
import com.netknownsthat.domain.model.ConfigDiffResponse
import com.netknownsthat.domain.model.ConfigFileResponse
import com.netknownsthat.domain.model.ConfigVersionsResponse
import com.netknownsthat.domain.model.ConfigWriteResult
import com.netknownsthat.domain.model.ConfigsResponse
import com.netknownsthat.domain.model.ContainerInspect
import com.netknownsthat.domain.model.ContainersResponse
import com.netknownsthat.domain.model.Fail2banResponse
import com.netknownsthat.domain.model.FindingsResponse
import com.netknownsthat.domain.model.FirewallNumberedResponse
import com.netknownsthat.domain.model.FirewallResponse
import com.netknownsthat.domain.model.FirewalldPortSpec
import com.netknownsthat.domain.model.ImagesResponse
import com.netknownsthat.domain.model.InterfacesResponse
import com.netknownsthat.domain.model.JobListResponse
import com.netknownsthat.domain.model.JobLogResponse
import com.netknownsthat.domain.model.JobsResponse
import com.netknownsthat.domain.model.K8sUsageCluster
import com.netknownsthat.domain.model.LXDResponse
import com.netknownsthat.domain.model.LineageInfo
import com.netknownsthat.domain.model.LogSource
import com.netknownsthat.domain.model.MiscResponse
import com.netknownsthat.domain.model.OutagesResponse
import com.netknownsthat.domain.model.Overview
import com.netknownsthat.domain.model.PodmanResponse
import com.netknownsthat.domain.model.RebootPreview
import com.netknownsthat.domain.model.RenewJobStatus
import com.netknownsthat.domain.model.RuleSpec
import com.netknownsthat.domain.model.SelfSignedResponse
import com.netknownsthat.domain.model.ServicesResponse
import com.netknownsthat.domain.model.TargetsResponse
import com.netknownsthat.domain.model.TopologyResponse
import com.netknownsthat.domain.model.UsageResponse
import com.netknownsthat.domain.model.UsageTopResponse
import com.netknownsthat.domain.model.UsersResponse
import com.netknownsthat.domain.model.VMsResponse
import com.netknownsthat.domain.model.VulnResponse
import kotlinx.coroutines.flow.Flow

/** Read-only views of a host's state. */
interface HostInfoRepository {
    suspend fun overview(host: HostTarget): Outcome<Overview>
    suspend fun findings(host: HostTarget): Outcome<FindingsResponse>
    suspend fun interfaces(host: HostTarget): Outcome<InterfacesResponse>
    suspend fun misc(host: HostTarget): Outcome<MiscResponse>
    suspend fun audit(host: HostTarget): Outcome<AuditResponse>
    suspend fun topology(host: HostTarget): Outcome<TopologyResponse>
}

interface ServicesRepository {
    suspend fun services(host: HostTarget): Outcome<ServicesResponse>
    suspend fun action(host: HostTarget, name: String, action: String): Outcome<Unit>

    /** A live check, not the last snapshot. */
    suspend fun installed(host: HostTarget, name: String): Outcome<Boolean>
}

/** Container engines and virtual machines. */
enum class Runtime { DOCKER, PODMAN, LXD, VM }

interface ContainersRepository {
    suspend fun docker(host: HostTarget): Outcome<ContainersResponse>
    suspend fun podman(host: HostTarget): Outcome<PodmanResponse>
    suspend fun lxd(host: HostTarget): Outcome<LXDResponse>
    suspend fun vms(host: HostTarget): Outcome<VMsResponse>
    suspend fun images(host: HostTarget): Outcome<ImagesResponse>
    suspend fun action(host: HostTarget, runtime: Runtime, name: String, action: String): Outcome<Unit>
    suspend fun inspect(host: HostTarget, name: String, reveal: Boolean): Outcome<ContainerInspect>
    suspend fun removeImages(host: HostTarget, refs: List<String>, force: Boolean): Outcome<Unit>
    suspend fun saveImages(host: HostTarget, refs: List<String>): Outcome<Unit>
    suspend fun pruneImages(host: HostTarget): Outcome<Unit>

    /** The same as [action], as a host job (?job=1) — for what outlasts a
     * request (an LXD machine shutting down): the job id, or null from an
     * older host that did it within the request. */
    suspend fun actionJob(host: HostTarget, runtime: Runtime, name: String, action: String): Outcome<Long?>

    /** docker save as a host job, one step per image (null — older host). */
    suspend fun saveImagesJob(host: HostTarget, refs: List<String>): Outcome<Long?>
}

interface UsersRepository {
    suspend fun users(host: HostTarget): Outcome<UsersResponse>
    suspend fun create(host: HostTarget, username: String, password: String, role: String): Outcome<Unit>
    suspend fun setDisabled(host: HostTarget, username: String, disabled: Boolean): Outcome<Unit>
    suspend fun delete(host: HostTarget, username: String): Outcome<Unit>
}

interface SecurityRepository {
    suspend fun vulnerabilities(host: HostTarget): Outcome<VulnResponse>
    suspend fun startVulnScan(host: HostTarget): Outcome<Unit>
    suspend fun clamav(host: HostTarget): Outcome<ClamResponse>

    /** op: install | update-db | scan | scan-images — a host job. */
    suspend fun clamavJob(host: HostTarget, op: String): Outcome<Long>
    suspend fun fail2ban(host: HostTarget): Outcome<Fail2banResponse>
    suspend fun ban(host: HostTarget, ip: String, banTimeSeconds: Long): Outcome<Unit>
    suspend fun unban(host: HostTarget, ip: String, jail: String): Outcome<Unit>
}

interface HostMonitoringRepository {
    suspend fun targets(host: HostTarget): Outcome<TargetsResponse>
    suspend fun outages(host: HostTarget): Outcome<OutagesResponse>
    suspend fun checkTarget(host: HostTarget, targetId: Long): Outcome<Unit>

    /** Hourly history of a target over [since] ("24h", "7d"…); [tzMinutes]
     * — the device's UTC offset, so the hours are local. */
    suspend fun targetHistory(host: HostTarget, targetId: Long, since: String, tzMinutes: Int): Outcome<List<com.netknownsthat.domain.model.AvailabilityBucket>>

    /** A target of your own (source "manual"): an address no host config
     * mentions. kind: icmp | tcp | http | https. */
    suspend fun addTarget(host: HostTarget, target: NewTarget): Outcome<Unit>
    suspend fun setTargetEnabled(host: HostTarget, targetId: Long, enabled: Boolean): Outcome<Unit>
    suspend fun deleteTarget(host: HostTarget, targetId: Long): Outcome<Unit>
    suspend fun usageSources(host: HostTarget): Outcome<List<String>>
    suspend fun usageTop(host: HostTarget, source: String, metric: String, limit: Int): Outcome<UsageTopResponse>
    suspend fun usageSeries(host: HostTarget, source: String, metric: String, agg: String, subjects: List<String>): Outcome<UsageResponse>
    suspend fun k8sCluster(host: HostTarget): Outcome<K8sUsageCluster>
    suspend fun collectorJobs(host: HostTarget): Outcome<JobsResponse>
}

interface ConfigsRepository {
    suspend fun configs(host: HostTarget): Outcome<ConfigsResponse>
    suspend fun file(host: HostTarget, path: String): Outcome<ConfigFileResponse>
    suspend fun versions(host: HostTarget, path: String): Outcome<ConfigVersionsResponse>
    suspend fun diff(host: HostTarget, versionId: Long): Outcome<ConfigDiffResponse>

    /** Writes only if the file still has [expectedSha256]; [apply] reloads the owning service. */
    suspend fun save(host: HostTarget, path: String, content: String, expectedSha256: String, note: String, apply: Boolean): Outcome<ConfigWriteResult>
    suspend fun rollback(host: HostTarget, versionId: Long, apply: Boolean): Outcome<ConfigWriteResult>
}

interface FirewallRepository {
    suspend fun state(host: HostTarget): Outcome<FirewallResponse>
    suspend fun numbered(host: HostTarget): Outcome<FirewallNumberedResponse>
    suspend fun addUfw(host: HostTarget, spec: RuleSpec): Outcome<Unit>

    /** By number — [expectedText] guards against ufw having renumbered. */
    suspend fun deleteUfw(host: HostTarget, number: Int, expectedText: String): Outcome<Unit>
    suspend fun deleteUfwBySpec(host: HostTarget, spec: RuleSpec): Outcome<Unit>
    suspend fun reloadUfw(host: HostTarget): Outcome<Unit>
    suspend fun addFirewalld(host: HostTarget, spec: FirewalldPortSpec): Outcome<Unit>
    suspend fun deleteFirewalld(host: HostTarget, spec: FirewalldPortSpec): Outcome<Unit>
    suspend fun reloadFirewalld(host: HostTarget): Outcome<Unit>
}

interface CertificatesRepository {
    suspend fun certificates(host: HostTarget): Outcome<CertificatesResponse>
    suspend fun lineages(host: HostTarget): Outcome<List<LineageInfo>>
    suspend fun haproxyPaths(host: HostTarget): Outcome<List<String>>
    suspend fun selfSigned(host: HostTarget, names: List<String>, service: String, bits: Int, days: Int): Outcome<SelfSignedResponse>

    /** issue/renew start a certbot job on the host; its id goes to [renewStatus]. */
    suspend fun issue(host: HostTarget, domains: List<String>): Outcome<String>
    suspend fun renew(host: HostTarget, lineage: String): Outcome<String>
    suspend fun renewStatus(host: HostTarget, jobId: String): Outcome<RenewJobStatus>
    suspend fun combine(host: HostTarget, lineage: String, targetPath: String): Outcome<Unit>
}

/** Background jobs of a host or of the hub. */
interface JobsRepository {
    suspend fun jobs(owner: JobOwner, statusFilter: String, limit: Int): Outcome<JobListResponse>
    suspend fun log(owner: JobOwner, jobId: Long, after: Long): Outcome<JobLogResponse>
    suspend fun cancel(owner: JobOwner, jobId: Long): Outcome<Unit>
    suspend fun retry(owner: JobOwner, jobId: Long): Outcome<Unit>

    /** Jobs queued or running right now — the hub's own and every online
     * host's (GET /hub/jobs/active): the background operations indicator. */
    suspend fun active(): Outcome<List<ActiveJob>> = Outcome.Success(emptyList())
}

interface SystemRepository {
    suspend fun rebootPreview(host: HostTarget): Outcome<RebootPreview>
    suspend fun reboot(host: HostTarget): Outcome<Unit>
}

/** Events of a live stream (terminal output, log lines). */
sealed interface StreamEvent {
    data object Opened : StreamEvent
    data class Bytes(val data: ByteArray) : StreamEvent
    data class Text(val text: String) : StreamEvent

    /** The stream ended; [error] when it failed rather than closed. */
    data class Closed(val error: com.netknownsthat.domain.common.AppError? = null) : StreamEvent
}

/** A live bidirectional connection; collecting [events] opens it. */
interface StreamChannel {
    val events: Flow<StreamEvent>
    suspend fun send(bytes: ByteArray)
    suspend fun sendText(text: String)
    fun close()
}

interface LogsRepository {
    suspend fun sources(host: HostTarget): Outcome<Pair<List<LogSource>, String>>

    /** One-off read (archives, rotated copies). */
    suspend fun tail(host: HostTarget, source: LogSource, lines: Int): Outcome<String>

    /** Followed live; Text events carry newline-separated lines. */
    fun follow(host: HostTarget, source: LogSource, lines: Int): StreamChannel
}

enum class TerminalMode { SHELL, TMUX, BTOP }

/** What a console opens a shell in: the server's /console/ws kinds. */
enum class ConsoleKind(val wire: String) { DOCKER("docker"), PODMAN("podman"), LXD("lxd"), VM("vm") }

/**
 * A console: a shell inside a container or an LXD instance, or a VM's
 * serial console. [user] — the user to exec as (Docker and Podman only;
 * empty — the image's default).
 */
data class ConsoleTarget(val kind: ConsoleKind, val name: String, val user: String = "")

/** tmux on the host: present, and whether its mouse mode is on. */
data class TmuxStatus(val available: Boolean)

/** A PTY: binary output in Bytes events, keystrokes via [send]. */
interface TerminalChannel : StreamChannel {
    /** Tells the PTY the real grid — full-screen programs draw for it. */
    suspend fun resize(columns: Int, rows: Int)
}

interface TerminalRepository {
    fun open(host: HostTarget, mode: TerminalMode): TerminalChannel
    fun openConsole(host: HostTarget, target: ConsoleTarget): TerminalChannel
    suspend fun tmuxStatus(host: HostTarget): Outcome<TmuxStatus>
    suspend fun tmuxMouse(host: HostTarget): Outcome<Boolean?>
    suspend fun setTmuxMouse(host: HostTarget, on: Boolean): Outcome<Boolean>

    /** Installs tmux on the host; output streamed like a terminal. */
    fun installTmux(host: HostTarget): StreamChannel
    suspend fun tmuxInstallStatus(host: HostTarget): Outcome<TmuxInstallStatus>
}

data class TmuxInstallStatus(val active: Boolean, val finished: Boolean, val succeeded: Boolean)
