package com.netknownsthat.data.repository

import com.netknownsthat.data.dto.AddHostResponseDto
import com.netknownsthat.data.dto.DeploymentsResponseDto
import com.netknownsthat.data.dto.FleetBannedResponseDto
import com.netknownsthat.data.dto.HostGroupsResponseDto
import com.netknownsthat.data.dto.HubEventsResponseDto
import com.netknownsthat.data.dto.HubHostDto
import com.netknownsthat.data.dto.HubVersionInfoDto
import com.netknownsthat.data.dto.HubVulnDBInfoDto
import com.netknownsthat.data.dto.InstallJobResponseDto
import com.netknownsthat.data.dto.JobIdResponseDto
import com.netknownsthat.data.dto.MonitoringOverviewDto
import com.netknownsthat.data.dto.PipelinesResponseDto
import com.netknownsthat.data.mapper.toDomain
import com.netknownsthat.data.remote.ApiClient
import com.netknownsthat.data.remote.q
import com.netknownsthat.domain.common.AppError
import com.netknownsthat.domain.common.Outcome
import com.netknownsthat.domain.common.map
import com.netknownsthat.domain.model.AddHostResponse
import com.netknownsthat.domain.model.DeploymentsResponse
import com.netknownsthat.domain.model.FleetBannedResponse
import com.netknownsthat.domain.model.HubEventsResponse
import com.netknownsthat.domain.model.HubHost
import com.netknownsthat.domain.model.HubVersionInfo
import com.netknownsthat.domain.model.HubVulnDBInfo
import com.netknownsthat.domain.model.MonitoringOverview
import com.netknownsthat.domain.model.PipelinesResponse
import com.netknownsthat.domain.repository.DeploymentsRepository
import com.netknownsthat.domain.repository.EventsRepository
import com.netknownsthat.domain.repository.FleetFail2banRepository
import com.netknownsthat.domain.repository.HostsRepository
import com.netknownsthat.domain.repository.HubInfoRepository
import com.netknownsthat.domain.repository.InstallStart
import com.netknownsthat.domain.repository.MonitoringRepository
import com.netknownsthat.domain.repository.NewHost
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.add
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import kotlinx.serialization.json.putJsonArray

class HostsRepositoryImpl(private val api: ApiClient) : HostsRepository {
    override suspend fun hosts(): Outcome<List<HubHost>> =
        api.get<List<HubHostDto>>("/hub/hosts").map { list -> list.map { it.toDomain() } }

    override suspend fun add(host: NewHost): Outcome<AddHostResponse> =
        api.post<AddHostResponseDto>(
            "/hub/hosts",
            buildJsonObject {
                put("name", host.name.trim())
                put("addr", host.addr.trim())
                put("ssh_port", host.sshPort)
                put("ssh_user", host.sshUser.trim())
                put("auth_kind", host.authKind)
                if (host.authKind != "generated") put("secret", host.secret)
                put("group", host.group.trim())
                put("terminal_enabled", host.terminalEnabled)
            },
        ).map { it.toDomain() }

    override suspend fun install(hostId: Long, force: Boolean): Outcome<InstallStart> {
        val path = "/hub/hosts/$hostId/install" + if (force) "?force=true" else ""
        return when (val r = api.post<InstallJobResponseDto>(path)) {
            is Outcome.Success -> Outcome.Success(InstallStart.Started(r.value.job))
            // 409 — a foreign nkt is on the host: the operator decides.
            is Outcome.Failure -> (r.error as? AppError.Server)?.takeIf { it.status == 409 }
                ?.let { Outcome.Success(InstallStart.ForeignInstall(it.message)) } ?: r
        }
    }

    override suspend fun latestInstallJob(hostId: Long): Outcome<Long> =
        api.get<InstallJobResponseDto>("/hub/hosts/$hostId/install/latest").map { it.job }

    override suspend fun delete(hostId: Long, purge: Boolean): Outcome<Unit> =
        api.delete<Unit>(
            "/hub/hosts/$hostId",
            if (purge) buildJsonObject {
                put("service", true)
                put("data", true)
                put("access", true)
                put("restore_password", true)
            } else null,
        )

    override suspend fun setGroup(hostId: Long, group: String): Outcome<Unit> =
        api.post("/hub/hosts/$hostId/group", buildJsonObject { put("group", group.trim()) })

    override suspend fun groups(): Outcome<List<String>> =
        api.get<HostGroupsResponseDto>("/hub/groups").map { it.groups }

    override suspend fun createGroup(name: String): Outcome<Unit> =
        api.post("/hub/groups", buildJsonObject { put("name", name.trim()) })

    override suspend fun renameGroup(name: String, to: String): Outcome<Unit> =
        api.post("/hub/groups/rename", buildJsonObject { put("name", name); put("to", to.trim()) })

    override suspend fun deleteGroup(name: String): Outcome<Unit> =
        api.post("/hub/groups/delete", buildJsonObject { put("name", name) })
}

class HubInfoRepositoryImpl(private val api: ApiClient) : HubInfoRepository {
    override suspend fun version(): Outcome<HubVersionInfo> = api.get<HubVersionInfoDto>("/hub/version").map { it.toDomain() }
    override suspend fun vulnDb(): Outcome<HubVulnDBInfo> = api.get<HubVulnDBInfoDto>("/hub/vulndb").map { it.toDomain() }
}

class EventsRepositoryImpl(private val api: ApiClient) : EventsRepository {
    override suspend fun events(limit: Int, kind: String?, after: Long): Outcome<HubEventsResponse> {
        val query = buildString {
            append("?limit=$limit")
            if (kind != null) append("&kind=${kind.q()}")
            if (after > 0) append("&after=$after")
        }
        return api.get<HubEventsResponseDto>("/hub/events$query").map { it.toDomain() }
    }

    override suspend fun markSeen(): Outcome<Unit> = api.post("/hub/events/seen")
}

class MonitoringRepositoryImpl(private val api: ApiClient) : MonitoringRepository {
    override suspend fun overview(): Outcome<MonitoringOverview> =
        api.get<MonitoringOverviewDto>("/hub/monitoring/overview").map { it.toDomain() }

    override suspend fun collectNow(): Outcome<Unit> = api.post("/hub/monitoring/collect")
}

class FleetFail2banRepositoryImpl(private val api: ApiClient) : FleetFail2banRepository {
    override suspend fun banned(): Outcome<FleetBannedResponse> =
        api.get<FleetBannedResponseDto>("/hub/fail2ban/banned").map { it.toDomain() }

    override suspend fun fleet(action: String, ips: List<String>, banTimeSeconds: Long, hostIds: List<Long>): Outcome<Long> =
        api.post<JobIdResponseDto>(
            "/hub/fail2ban/fleet",
            buildJsonObject {
                put("action", action)
                putJsonArray("ips") { ips.forEach { add(it) } }
                if (banTimeSeconds > 0) put("ban_time", banTimeSeconds)
                putJsonArray("host_ids") { hostIds.forEach { add(JsonPrimitive(it)) } }
            },
        ).map { it.jobId }
}

class DeploymentsRepositoryImpl(private val api: ApiClient) : DeploymentsRepository {
    override suspend fun pipelines(): Outcome<PipelinesResponse> =
        api.get<PipelinesResponseDto>("/hub/pipelines").map { it.toDomain() }

    override suspend fun history(pipelineId: Long): Outcome<DeploymentsResponse> =
        api.get<DeploymentsResponseDto>("/hub/pipelines/$pipelineId/deployments").map { it.toDomain() }

    override suspend fun deploy(pipelineId: Long): Outcome<Long> =
        api.post<JobIdResponseDto>("/hub/pipelines/$pipelineId/deploy").map { it.jobId }

    override suspend fun dryRun(pipelineId: Long): Outcome<Long> =
        api.post<JobIdResponseDto>("/hub/pipelines/dryrun", buildJsonObject { put("pipeline_id", pipelineId) }).map { it.jobId }

    override suspend fun rollback(pipelineId: Long, deploymentId: Long): Outcome<Long> =
        api.post<JobIdResponseDto>(
            "/hub/pipelines/$pipelineId/rollback",
            buildJsonObject { put("deployment_id", deploymentId) },
        ).map { it.jobId }

    override suspend fun removeLeftover(pipelineId: Long, hostId: Long, project: String, forget: Boolean): Outcome<Unit> =
        api.post(
            "/hub/pipelines/$pipelineId/leftovers/remove",
            buildJsonObject {
                put("host_id", hostId)
                put("project", project)
                put("forget", forget)
            },
        )
}
