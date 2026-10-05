package com.netknownsthat.domain.repository

import com.netknownsthat.domain.common.Outcome
import com.netknownsthat.domain.model.AddHostResponse
import com.netknownsthat.domain.model.DeploymentsResponse
import com.netknownsthat.domain.model.FleetBannedResponse
import com.netknownsthat.domain.model.HubEventsResponse
import com.netknownsthat.domain.model.HubHost
import com.netknownsthat.domain.model.HubVersionInfo
import com.netknownsthat.domain.model.HubVulnDBInfo
import com.netknownsthat.domain.model.MonitoringOverview
import com.netknownsthat.domain.model.PipelinesResponse

/** A new host as entered in the add-host form. */
data class NewHost(
    val name: String,
    val addr: String,
    val sshPort: Int,
    val sshUser: String,
    /** password | key | generated */
    val authKind: String,
    /** Password or PEM private key; empty for "generated". */
    val secret: String,
    val group: String,
    val terminalEnabled: Boolean,
)

/** Result of starting an install: a hub job, or a foreign nkt already there. */
sealed interface InstallStart {
    data class Started(val jobId: Long) : InstallStart
    data class ForeignInstall(val message: String) : InstallStart
}

/** Hosts registered on the hub and their groups. */
interface HostsRepository {
    suspend fun hosts(): Outcome<List<HubHost>>
    suspend fun add(host: NewHost): Outcome<AddHostResponse>
    suspend fun install(hostId: Long, force: Boolean): Outcome<InstallStart>
    suspend fun latestInstallJob(hostId: Long): Outcome<Long>

    /** [purge] — also remove nkt from the host itself. */
    suspend fun delete(hostId: Long, purge: Boolean): Outcome<Unit>
    suspend fun setGroup(hostId: Long, group: String): Outcome<Unit>
    suspend fun groups(): Outcome<List<String>>
    suspend fun createGroup(name: String): Outcome<Unit>
    suspend fun renameGroup(name: String, to: String): Outcome<Unit>
    suspend fun deleteGroup(name: String): Outcome<Unit>
}

/** The hub's own state: version, vulnerability database. */
interface HubInfoRepository {
    suspend fun version(): Outcome<HubVersionInfo>
    suspend fun vulnDb(): Outcome<HubVulnDBInfo>
}

/** The hub's alert journal. */
interface EventsRepository {
    /** [kind] — one kind or null for all; [after] — only events newer than this id. */
    suspend fun events(limit: Int, kind: String? = null, after: Long = 0): Outcome<HubEventsResponse>
    suspend fun markSeen(): Outcome<Unit>
}

/** Hub monitoring of every host. */
interface MonitoringRepository {
    suspend fun overview(): Outcome<MonitoringOverview>
    suspend fun collectNow(): Outcome<Unit>
}

/** fail2ban across the fleet. */
interface FleetFail2banRepository {
    suspend fun banned(): Outcome<FleetBannedResponse>

    /** Starts a hub job; [hostIds] empty — every host with fail2ban. */
    suspend fun fleet(action: String, ips: List<String>, banTimeSeconds: Long, hostIds: List<Long>): Outcome<Long>
}

/** Deployment pipelines. */
interface DeploymentsRepository {
    suspend fun pipelines(): Outcome<PipelinesResponse>
    suspend fun history(pipelineId: Long): Outcome<DeploymentsResponse>

    /** Each starts a hub job and returns its id. */
    suspend fun deploy(pipelineId: Long): Outcome<Long>
    suspend fun dryRun(pipelineId: Long): Outcome<Long>
    suspend fun rollback(pipelineId: Long, deploymentId: Long): Outcome<Long>

    /** Removes an old stack left on a previous host, or only forgets it. */
    suspend fun removeLeftover(pipelineId: Long, hostId: Long, project: String, forget: Boolean): Outcome<Unit>
}
