package com.netknownsthat.app.net.model

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/**
 * GET /api/auth/me — a hand-written map on the Go side
 * (internal/hub/handlers.go's handleMe), mirrored field-for-field from
 * web/src/types.ts's own `Me` interface, which reads the exact same
 * response.
 */
@Serializable
data class Me(
    val username: String,
    val role: String, // "admin" | "viewer"
    @SerialName("is_admin") val isAdmin: Boolean,
    val mode: String,
    @SerialName("allow_mutations") val allowMutations: Boolean,
    val simulated: Boolean,
    @SerialName("hub_version") val hubVersion: String? = null,
)

/**
 * POST /api/auth/login request body.
 */
@Serializable
data class LoginRequest(
    val username: String,
    val password: String,
)

/**
 * One row of GET /api/hub/hosts — internal/store.Host embedded plus the
 * overview fields internal/hub/handlers.go's hostWithOverview adds
 * (findings/reachable/running_version/last_polled_at/channel/
 * tunnel_connected). LOCAL_HOST_ID (-1) marks the synthetic "localhost"
 * row for the machine the hub itself runs on — see HostScope.
 */
@Serializable
data class HubHost(
    val id: Long,
    val name: String,
    val addr: String,
    @SerialName("ssh_port") val sshPort: Int = 0,
    @SerialName("ssh_user") val sshUser: String = "",
    @SerialName("ssh_auth_kind") val sshAuthKind: String = "",
    val arch: String = "",
    val status: String,
    @SerialName("nkt_version") val nktVersion: String = "",
    @SerialName("sudo_status") val sudoStatus: String? = null,
    @SerialName("terminal_enabled") val terminalEnabled: Boolean = false,
    @SerialName("tunnel_enabled") val tunnelEnabled: Boolean = false,
    @SerialName("error_msg") val errorMsg: String? = null,
    @SerialName("created_at") val createdAt: String = "",
    @SerialName("last_seen_at") val lastSeenAt: String? = null,
    // hostWithOverview's own additions — absent entirely for a host never
    // polled yet (see internal/hub/overview_poll.go), not just empty.
    val findings: Map<String, Int>? = null,
    val reachable: Boolean? = null,
    @SerialName("running_version") val runningVersion: String? = null,
    @SerialName("last_polled_at") val lastPolledAt: String? = null,
    val channel: String? = null, // "ssh" | "tunnel"
    @SerialName("tunnel_connected") val tunnelConnected: Boolean = false,
) {
    companion object {
        /** Mirrors web/src/api.ts's LOCAL_HOST_ID — the hub's own machine. */
        const val LOCAL_HOST_ID = -1L
    }
}

/**
 * GET/POST /api/hub/version(/check) — internal/hub/handlers.go's
 * versionInfoJSON. checkedAt/checkError are only present in the JSON when
 * non-zero/non-empty on the Go side; kotlinx.serialization treats a missing
 * key the same as null here since both are declared nullable with no
 * default requirement.
 */
@Serializable
data class HubVersionInfo(
    val current: String,
    val latest: String? = null,
    @SerialName("update_available") val updateAvailable: Boolean = false,
    val updatable: Boolean = false,
    @SerialName("checked_at") val checkedAt: String? = null,
    @SerialName("check_error") val checkError: String? = null,
)

/**
 * GET /api/hub/vulndb — internal/hub/handlers.go's vulnDBInfoJSON.
 */
@Serializable
data class HubVulnDBInfo(
    val available: Boolean,
    val refreshing: Boolean,
    @SerialName("updated_at") val updatedAt: String? = null,
    val progress: String? = null,
    val error: String? = null,
)
