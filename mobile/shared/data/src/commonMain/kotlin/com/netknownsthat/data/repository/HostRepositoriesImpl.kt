package com.netknownsthat.data.repository

import com.netknownsthat.domain.model.ActiveJob
import com.netknownsthat.data.dto.ActiveJobsResponseDto
import com.netknownsthat.data.dto.AuditResponseDto
import com.netknownsthat.data.dto.CertificatesResponseDto
import com.netknownsthat.data.dto.ClamResponseDto
import com.netknownsthat.data.dto.ConfigDiffResponseDto
import com.netknownsthat.data.dto.ConfigFileResponseDto
import com.netknownsthat.data.dto.ConfigVersionsResponseDto
import com.netknownsthat.data.dto.ConfigWriteResultDto
import com.netknownsthat.data.dto.ConfigsResponseDto
import com.netknownsthat.data.dto.ContainerInspectDto
import com.netknownsthat.data.dto.ContainersResponseDto
import com.netknownsthat.data.dto.Fail2banResponseDto
import com.netknownsthat.data.dto.FindingsResponseDto
import com.netknownsthat.data.dto.FirewallNumberedResponseDto
import com.netknownsthat.data.dto.FirewallResponseDto
import com.netknownsthat.data.dto.FirewalldPortSpecDto
import com.netknownsthat.data.dto.HAProxyPathsResponseDto
import com.netknownsthat.data.dto.ImagesResponseDto
import com.netknownsthat.data.dto.InstalledResponseDto
import com.netknownsthat.data.dto.InterfacesResponseDto
import com.netknownsthat.data.dto.JobIdResponseDto
import com.netknownsthat.data.dto.JobListResponseDto
import com.netknownsthat.data.dto.JobLogResponseDto
import com.netknownsthat.data.dto.JobStartedDto
import com.netknownsthat.data.dto.JobsResponseDto
import com.netknownsthat.data.dto.K8sUsageClusterDto
import com.netknownsthat.data.dto.LXDResponseDto
import com.netknownsthat.data.dto.LineagesResponseDto
import com.netknownsthat.data.dto.LogSourcesResponseDto
import com.netknownsthat.data.dto.LogTailResponseDto
import com.netknownsthat.data.dto.MiscResponseDto
import com.netknownsthat.data.dto.OutagesResponseDto
import com.netknownsthat.data.dto.OverviewDto
import com.netknownsthat.data.dto.PodmanResponseDto
import com.netknownsthat.data.dto.RebootPreviewDto
import com.netknownsthat.data.dto.RenewJobStatusDto
import com.netknownsthat.data.dto.RuleSpecDto
import com.netknownsthat.data.dto.SelfSignedResponseDto
import com.netknownsthat.data.dto.ServicesResponseDto
import com.netknownsthat.data.dto.TargetsResponseDto
import com.netknownsthat.data.dto.TopologyResponseDto
import com.netknownsthat.data.dto.UsageResponseDto
import com.netknownsthat.data.dto.UsageSourcesResponseDto
import com.netknownsthat.data.dto.UsageTopResponseDto
import com.netknownsthat.data.dto.UsersResponseDto
import com.netknownsthat.data.dto.VMsResponseDto
import com.netknownsthat.data.dto.VulnResponseDto
import com.netknownsthat.data.mapper.toDomain
import com.netknownsthat.data.remote.ApiClient
import com.netknownsthat.data.remote.WebSocketChannel
import com.netknownsthat.data.remote.hostPath
import com.netknownsthat.data.remote.q
import com.netknownsthat.domain.common.HostTarget
import com.netknownsthat.domain.common.JobOwner
import com.netknownsthat.domain.common.Outcome
import com.netknownsthat.domain.common.map
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
import com.netknownsthat.domain.repository.CertificatesRepository
import com.netknownsthat.domain.repository.ConfigsRepository
import com.netknownsthat.domain.repository.ContainersRepository
import com.netknownsthat.domain.repository.FirewallRepository
import com.netknownsthat.domain.repository.HostInfoRepository
import com.netknownsthat.domain.repository.HostMonitoringRepository
import com.netknownsthat.domain.repository.JobsRepository
import com.netknownsthat.domain.repository.LogsRepository
import com.netknownsthat.domain.repository.Runtime
import com.netknownsthat.domain.repository.SecurityRepository
import com.netknownsthat.domain.repository.ServicesRepository
import com.netknownsthat.domain.repository.StreamChannel
import com.netknownsthat.domain.repository.SystemRepository
import com.netknownsthat.domain.repository.TerminalChannel
import com.netknownsthat.domain.repository.TerminalMode
import com.netknownsthat.domain.repository.TerminalRepository
import com.netknownsthat.domain.repository.TmuxInstallStatus
import com.netknownsthat.domain.repository.TmuxStatus
import com.netknownsthat.domain.repository.UsersRepository
import kotlinx.serialization.Serializable
import kotlinx.serialization.SerialName
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.add
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import kotlinx.serialization.json.putJsonArray

class HostInfoRepositoryImpl(private val api: ApiClient) : HostInfoRepository {
    override suspend fun overview(host: HostTarget): Outcome<Overview> = api.get<OverviewDto>(hostPath(host, "/overview")).map { it.toDomain() }
    override suspend fun findings(host: HostTarget): Outcome<FindingsResponse> = api.get<FindingsResponseDto>(hostPath(host, "/findings")).map { it.toDomain() }
    override suspend fun interfaces(host: HostTarget): Outcome<InterfacesResponse> = api.get<InterfacesResponseDto>(hostPath(host, "/interfaces")).map { it.toDomain() }
    override suspend fun misc(host: HostTarget): Outcome<MiscResponse> = api.get<MiscResponseDto>(hostPath(host, "/misc")).map { it.toDomain() }
    override suspend fun audit(host: HostTarget): Outcome<AuditResponse> = api.get<AuditResponseDto>(hostPath(host, "/audit")).map { it.toDomain() }
    override suspend fun topology(host: HostTarget): Outcome<TopologyResponse> = api.get<TopologyResponseDto>(hostPath(host, "/topology")).map { it.toDomain() }
}

class ServicesRepositoryImpl(private val api: ApiClient) : ServicesRepository {
    override suspend fun services(host: HostTarget): Outcome<ServicesResponse> =
        api.get<ServicesResponseDto>(hostPath(host, "/services")).map { it.toDomain() }

    override suspend fun action(host: HostTarget, name: String, action: String): Outcome<Unit> =
        api.post(hostPath(host, "/services/${name.q()}/${action.q()}"))

    override suspend fun installed(host: HostTarget, name: String): Outcome<Boolean> =
        api.get<InstalledResponseDto>(hostPath(host, "/services/${name.q()}/installed")).map { it.installed }
}

class ContainersRepositoryImpl(private val api: ApiClient) : ContainersRepository {
    override suspend fun docker(host: HostTarget): Outcome<ContainersResponse> = api.get<ContainersResponseDto>(hostPath(host, "/containers")).map { it.toDomain() }
    override suspend fun podman(host: HostTarget): Outcome<PodmanResponse> = api.get<PodmanResponseDto>(hostPath(host, "/podman/containers")).map { it.toDomain() }
    override suspend fun lxd(host: HostTarget): Outcome<LXDResponse> = api.get<LXDResponseDto>(hostPath(host, "/lxd/instances")).map { it.toDomain() }
    override suspend fun vms(host: HostTarget): Outcome<VMsResponse> = api.get<VMsResponseDto>(hostPath(host, "/vms")).map { it.toDomain() }
    override suspend fun images(host: HostTarget): Outcome<ImagesResponse> = api.get<ImagesResponseDto>(hostPath(host, "/images")).map { it.toDomain() }

    override suspend fun action(host: HostTarget, runtime: Runtime, name: String, action: String): Outcome<Unit> {
        val base = when (runtime) {
            Runtime.DOCKER -> "/containers"
            Runtime.PODMAN -> "/podman/containers"
            Runtime.LXD -> "/lxd/instances"
            Runtime.VM -> "/vms"
        }
        return api.post(hostPath(host, "$base/${name.q()}/${action.q()}"))
    }

    override suspend fun inspect(host: HostTarget, name: String, reveal: Boolean): Outcome<ContainerInspect> =
        api.get<ContainerInspectDto>(hostPath(host, "/containers/${name.q()}/inspect" + if (reveal) "?reveal=1" else ""))
            .map { it.toDomain() }

    private fun refs(refs: List<String>) = JsonArray(refs.map { JsonPrimitive(it) })

    override suspend fun removeImages(host: HostTarget, refs: List<String>, force: Boolean): Outcome<Unit> =
        api.post(hostPath(host, "/images/remove"), buildJsonObject { put("refs", refs(refs)); put("force", force) })

    override suspend fun saveImages(host: HostTarget, refs: List<String>): Outcome<Unit> =
        api.post(hostPath(host, "/images/save"), buildJsonObject { put("refs", refs(refs)) })

    override suspend fun pruneImages(host: HostTarget): Outcome<Unit> = api.post(hostPath(host, "/images/prune"))
}

class UsersRepositoryImpl(private val api: ApiClient) : UsersRepository {
    override suspend fun users(host: HostTarget): Outcome<UsersResponse> = api.get<UsersResponseDto>(hostPath(host, "/users")).map { it.toDomain() }

    override suspend fun create(host: HostTarget, username: String, password: String, role: String): Outcome<Unit> =
        api.post(hostPath(host, "/users"), buildJsonObject { put("username", username); put("password", password); put("role", role) })

    override suspend fun setDisabled(host: HostTarget, username: String, disabled: Boolean): Outcome<Unit> =
        api.patch(hostPath(host, "/users/${username.q()}"), buildJsonObject { put("disabled", disabled) })

    override suspend fun delete(host: HostTarget, username: String): Outcome<Unit> =
        api.delete(hostPath(host, "/users/${username.q()}"))
}

class SecurityRepositoryImpl(private val api: ApiClient) : SecurityRepository {
    override suspend fun vulnerabilities(host: HostTarget): Outcome<VulnResponse> = api.get<VulnResponseDto>(hostPath(host, "/vulnerabilities")).map { it.toDomain() }
    override suspend fun startVulnScan(host: HostTarget): Outcome<Unit> = api.post(hostPath(host, "/vulnerabilities/scan"))
    override suspend fun clamav(host: HostTarget): Outcome<ClamResponse> = api.get<ClamResponseDto>(hostPath(host, "/clamav")).map { it.toDomain() }

    override suspend fun clamavJob(host: HostTarget, op: String): Outcome<Long> {
        val body = when (op) {
            "scan" -> buildJsonObject { putJsonArray("paths") {} }
            "scan-images" -> buildJsonObject { putJsonArray("images") {} }
            else -> buildJsonObject {}
        }
        return api.post<JobIdResponseDto>(hostPath(host, "/clamav/${op.q()}"), body).map { it.jobId }
    }

    override suspend fun fail2ban(host: HostTarget): Outcome<Fail2banResponse> = api.get<Fail2banResponseDto>(hostPath(host, "/fail2ban")).map { it.toDomain() }

    override suspend fun ban(host: HostTarget, ip: String, banTimeSeconds: Long): Outcome<Unit> =
        api.post(hostPath(host, "/fail2ban/ban"), buildJsonObject { putJsonArray("ips") { add(ip) }; put("ban_time", banTimeSeconds) })

    override suspend fun unban(host: HostTarget, ip: String, jail: String): Outcome<Unit> =
        api.post(hostPath(host, "/fail2ban/unban"), buildJsonObject { put("jail", jail); putJsonArray("ips") { add(ip) } })
}

class HostMonitoringRepositoryImpl(private val api: ApiClient) : HostMonitoringRepository {
    override suspend fun targets(host: HostTarget): Outcome<TargetsResponse> = api.get<TargetsResponseDto>(hostPath(host, "/monitor/targets")).map { it.toDomain() }
    override suspend fun outages(host: HostTarget): Outcome<OutagesResponse> = api.get<OutagesResponseDto>(hostPath(host, "/monitor/outages")).map { it.toDomain() }
    override suspend fun checkTarget(host: HostTarget, targetId: Long): Outcome<Unit> = api.post(hostPath(host, "/monitor/targets/$targetId/check"))
    override suspend fun usageSources(host: HostTarget): Outcome<List<String>> = api.get<UsageSourcesResponseDto>(hostPath(host, "/monitor/usage/sources")).map { it.sources }

    override suspend fun usageTop(host: HostTarget, source: String, metric: String, limit: Int): Outcome<UsageTopResponse> =
        api.get<UsageTopResponseDto>(hostPath(host, "/monitor/usage/top?source=${source.q()}&metric=${metric.q()}&limit=$limit")).map { it.toDomain() }

    override suspend fun usageSeries(host: HostTarget, source: String, metric: String, agg: String, subjects: List<String>): Outcome<UsageResponse> =
        api.get<UsageResponseDto>(
            hostPath(host, "/monitor/usage?source=${source.q()}&metric=${metric.q()}&agg=${agg.q()}&subjects=${subjects.joinToString(",").q()}"),
        ).map { it.toDomain() }

    override suspend fun k8sCluster(host: HostTarget): Outcome<K8sUsageCluster> = api.get<K8sUsageClusterDto>(hostPath(host, "/monitor/usage/k8s")).map { it.toDomain() }
    override suspend fun collectorJobs(host: HostTarget): Outcome<JobsResponse> = api.get<JobsResponseDto>(hostPath(host, "/monitor/jobs")).map { it.toDomain() }
}

class ConfigsRepositoryImpl(private val api: ApiClient) : ConfigsRepository {
    override suspend fun configs(host: HostTarget): Outcome<ConfigsResponse> = api.get<ConfigsResponseDto>(hostPath(host, "/configs")).map { it.toDomain() }
    override suspend fun file(host: HostTarget, path: String): Outcome<ConfigFileResponse> = api.get<ConfigFileResponseDto>(hostPath(host, "/configs/file?path=${path.q()}")).map { it.toDomain() }
    override suspend fun versions(host: HostTarget, path: String): Outcome<ConfigVersionsResponse> = api.get<ConfigVersionsResponseDto>(hostPath(host, "/configs/versions?path=${path.q()}")).map { it.toDomain() }
    override suspend fun diff(host: HostTarget, versionId: Long): Outcome<ConfigDiffResponse> = api.get<ConfigDiffResponseDto>(hostPath(host, "/configs/versions/$versionId/diff")).map { it.toDomain() }

    override suspend fun save(host: HostTarget, path: String, content: String, expectedSha256: String, note: String, apply: Boolean): Outcome<ConfigWriteResult> =
        api.put<ConfigWriteResultDto>(
            hostPath(host, "/configs/file"),
            buildJsonObject {
                put("path", path)
                put("content", content)
                put("note", note)
                put("apply", apply)
                put("expected_sha256", expectedSha256)
            },
        ).map { it.toDomain() }

    override suspend fun rollback(host: HostTarget, versionId: Long, apply: Boolean): Outcome<ConfigWriteResult> =
        api.post<ConfigWriteResultDto>(hostPath(host, "/configs/versions/$versionId/rollback"), buildJsonObject { put("apply", apply) })
            .map { it.toDomain() }
}

private fun RuleSpec.toDto() = RuleSpecDto(action = action, port = port, protocol = protocol, from = from, comment = comment)
private fun FirewalldPortSpec.toDto() = FirewalldPortSpecDto(zone = zone, port = port, protocol = protocol, service = service, permanent = permanent, runtime = runtime)

class FirewallRepositoryImpl(private val api: ApiClient) : FirewallRepository {
    private fun RuleSpecDto.json() = api.json.encodeToJsonElement(RuleSpecDto.serializer(), this)
    private fun FirewalldPortSpecDto.json() = api.json.encodeToJsonElement(FirewalldPortSpecDto.serializer(), this)

    override suspend fun state(host: HostTarget): Outcome<FirewallResponse> = api.get<FirewallResponseDto>(hostPath(host, "/firewall")).map { it.toDomain() }
    override suspend fun numbered(host: HostTarget): Outcome<FirewallNumberedResponse> = api.get<FirewallNumberedResponseDto>(hostPath(host, "/firewall/rules")).map { it.toDomain() }
    override suspend fun addUfw(host: HostTarget, spec: RuleSpec): Outcome<Unit> = api.post(hostPath(host, "/firewall/rules"), spec.toDto().json())

    override suspend fun deleteUfw(host: HostTarget, number: Int, expectedText: String): Outcome<Unit> =
        api.delete(hostPath(host, "/firewall/rules/$number"), buildJsonObject { put("expected", expectedText) })

    override suspend fun deleteUfwBySpec(host: HostTarget, spec: RuleSpec): Outcome<Unit> = api.delete(hostPath(host, "/firewall/rules"), spec.toDto().json())
    override suspend fun reloadUfw(host: HostTarget): Outcome<Unit> = api.post(hostPath(host, "/firewall/reload"))
    override suspend fun addFirewalld(host: HostTarget, spec: FirewalldPortSpec): Outcome<Unit> = api.post(hostPath(host, "/firewall/firewalld/rules"), spec.toDto().json())
    override suspend fun deleteFirewalld(host: HostTarget, spec: FirewalldPortSpec): Outcome<Unit> = api.delete(hostPath(host, "/firewall/firewalld/rules"), spec.toDto().json())
    override suspend fun reloadFirewalld(host: HostTarget): Outcome<Unit> = api.post(hostPath(host, "/firewall/firewalld/reload"))
}

class CertificatesRepositoryImpl(private val api: ApiClient) : CertificatesRepository {
    override suspend fun certificates(host: HostTarget): Outcome<CertificatesResponse> = api.get<CertificatesResponseDto>(hostPath(host, "/certificates")).map { it.toDomain() }
    override suspend fun lineages(host: HostTarget): Outcome<List<LineageInfo>> = api.get<LineagesResponseDto>(hostPath(host, "/certificates/lineages")).map { r -> r.lineages.map { it.toDomain() } }
    override suspend fun haproxyPaths(host: HostTarget): Outcome<List<String>> = api.get<HAProxyPathsResponseDto>(hostPath(host, "/certificates/haproxy-paths")).map { it.paths }

    override suspend fun selfSigned(host: HostTarget, names: List<String>, service: String, bits: Int, days: Int): Outcome<SelfSignedResponse> =
        api.post<SelfSignedResponseDto>(
            hostPath(host, "/certificates/self-signed"),
            buildJsonObject {
                put("names", JsonArray(names.map { JsonPrimitive(it) }))
                put("service", service)
                put("bits", bits)
                put("days", days)
            },
        ).map { it.toDomain() }

    override suspend fun issue(host: HostTarget, domains: List<String>): Outcome<String> =
        api.post<JobStartedDto>(hostPath(host, "/certificates/issue"), buildJsonObject { put("domains", JsonArray(domains.map { JsonPrimitive(it) })) }).map { it.job }

    override suspend fun renew(host: HostTarget, lineage: String): Outcome<String> =
        api.post<JobStartedDto>(hostPath(host, "/certificates/renew"), buildJsonObject { put("lineage", lineage) }).map { it.job }

    override suspend fun renewStatus(host: HostTarget, jobId: String): Outcome<RenewJobStatus> =
        api.get<RenewJobStatusDto>(hostPath(host, "/certificates/renew/${jobId.q()}")).map { it.toDomain() }

    override suspend fun combine(host: HostTarget, lineage: String, targetPath: String): Outcome<Unit> =
        api.post(hostPath(host, "/certificates/combine"), buildJsonObject { put("lineage", lineage); put("target_path", targetPath) })
}

class JobsRepositoryImpl(private val api: ApiClient) : JobsRepository {
    /** Hub jobs are the hub machine's local API jobs (/hosts/local/jobs). */
    private fun prefix(owner: JobOwner) = when (owner) {
        is JobOwner.Host -> hostPath(owner.target, "")
        JobOwner.Hub -> hostPath(HostTarget.LOCAL, "")
    }

    override suspend fun jobs(owner: JobOwner, statusFilter: String, limit: Int): Outcome<JobListResponse> {
        val status = if (statusFilter.isEmpty()) "" else "&status=${statusFilter.q()}"
        return api.get<JobListResponseDto>("${prefix(owner)}/jobs?limit=$limit$status").map { it.toDomain() }
    }

    override suspend fun log(owner: JobOwner, jobId: Long, after: Long): Outcome<JobLogResponse> =
        api.get<JobLogResponseDto>("${prefix(owner)}/jobs/$jobId/log?after=$after").map { it.toDomain() }

    override suspend fun cancel(owner: JobOwner, jobId: Long): Outcome<Unit> = api.post("${prefix(owner)}/jobs/$jobId/cancel")
    override suspend fun retry(owner: JobOwner, jobId: Long): Outcome<Unit> = api.post("${prefix(owner)}/jobs/$jobId/retry")

    override suspend fun active(): Outcome<List<ActiveJob>> =
        api.get<ActiveJobsResponseDto>("/hub/jobs/active").map { r -> r.jobs.map { it.toDomain() } }
}

class SystemRepositoryImpl(private val api: ApiClient) : SystemRepository {
    override suspend fun rebootPreview(host: HostTarget): Outcome<RebootPreview> = api.get<RebootPreviewDto>(hostPath(host, "/system/reboot/preview")).map { it.toDomain() }
    override suspend fun reboot(host: HostTarget): Outcome<Unit> = api.post(hostPath(host, "/system/reboot"), buildJsonObject { put("confirm", true) })
}

class LogsRepositoryImpl(private val api: ApiClient) : LogsRepository {
    private fun query(source: LogSource) =
        if (source.kind == "unit") "unit=${source.name.q()}" else "path=${source.name.q()}"

    override suspend fun sources(host: HostTarget): Outcome<Pair<List<LogSource>, String>> =
        api.get<LogSourcesResponseDto>(hostPath(host, "/logs/sources")).map { r -> r.sources.map { it.toDomain() } to r.root }

    override suspend fun tail(host: HostTarget, source: LogSource, lines: Int): Outcome<String> =
        api.get<LogTailResponseDto>(hostPath(host, "/logs/tail?${query(source)}&lines=$lines")).map { it.output }

    override fun follow(host: HostTarget, source: LogSource, lines: Int): StreamChannel =
        WebSocketChannel(api, hostPath(host, "/logs/ws?${query(source)}&lines=$lines"))
}

@Serializable
private data class TmuxStatusDto(val available: Boolean = false)

@Serializable
private data class TmuxMouseDto(val session: Boolean = false, val mouse: Boolean = false)

@Serializable
private data class TmuxInstallStatusDto(
    val active: Boolean = false,
    val finished: Boolean = false,
    val succeeded: Boolean = false,
    @SerialName("exit_code") val exitCode: Int = 0,
)

class TerminalRepositoryImpl(private val api: ApiClient) : TerminalRepository {
    override fun open(host: HostTarget, mode: TerminalMode): TerminalChannel =
        WebSocketChannel(
            api,
            hostPath(
                host,
                when (mode) {
                    TerminalMode.BTOP -> "/terminal/btop/ws"
                    TerminalMode.TMUX -> "/terminal/ws?tmux=1"
                    TerminalMode.SHELL -> "/terminal/ws"
                },
            ),
        )

    override suspend fun tmuxStatus(host: HostTarget): Outcome<TmuxStatus> =
        api.get<TmuxStatusDto>(hostPath(host, "/system/tmux-status")).map { TmuxStatus(it.available) }

    override suspend fun tmuxMouse(host: HostTarget): Outcome<Boolean?> =
        api.get<TmuxMouseDto>(hostPath(host, "/terminal/tmux/mouse")).map { if (it.session) it.mouse else null }

    override suspend fun setTmuxMouse(host: HostTarget, on: Boolean): Outcome<Boolean> =
        api.post<TmuxMouseDto>(hostPath(host, "/terminal/tmux/mouse"), buildJsonObject { put("mouse", on) }).map { it.mouse }

    override fun installTmux(host: HostTarget): StreamChannel = WebSocketChannel(api, hostPath(host, "/system/tmux-install/ws"))

    override suspend fun tmuxInstallStatus(host: HostTarget): Outcome<TmuxInstallStatus> =
        api.get<TmuxInstallStatusDto>(hostPath(host, "/system/tmux-install/status"))
            .map { TmuxInstallStatus(it.active, it.finished, it.succeeded) }
}
