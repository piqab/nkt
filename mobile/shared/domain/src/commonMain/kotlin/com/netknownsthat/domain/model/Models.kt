package com.netknownsthat.domain.model


/**
 * GET /api/auth/me — a hand-written map on the Go side
 * (internal/hub/handlers.go's handleMe), mirrored field-for-field from
 * web/src/types.ts's own `Me` interface, which reads the exact same
 * response.
 */
data class Me(
    val username: String,
    val role: String, // "admin" | "viewer"
    val isAdmin: Boolean,
    val mode: String,
    val allowMutations: Boolean,
    val simulated: Boolean,
    val hubVersion: String? = null,
)

/**
 * One row of GET /api/hub/hosts — internal/store.Host embedded plus the
 * overview fields internal/hub/handlers.go's hostWithOverview adds
 * (findings/reachable/running_version/last_polled_at/channel/
 * tunnel_connected). LOCAL_HOST_ID (-1) marks the synthetic "localhost"
 * row for the machine the hub itself runs on — see HostScope.
 */
data class HubHost(
    val id: Long,
    val name: String,
    val addr: String,
    val sshPort: Int = 0,
    val sshUser: String = "",
    val sshAuthKind: String = "",
    val arch: String = "",
    val status: String,
    val nktVersion: String = "",
    val sudoStatus: String? = null,
    val terminalEnabled: Boolean = false,
    val tunnelEnabled: Boolean = false,
    val errorMsg: String? = null,
    val createdAt: String = "",
    val lastSeenAt: String? = null,
    // hostWithOverview's own additions — absent entirely for a host never
    // polled yet (see internal/hub/overview_poll.go), not just empty.
    val findings: Map<String, Int>? = null,
    val reachable: Boolean? = null,
    val runningVersion: String? = null,
    val lastPolledAt: String? = null,
    val channel: String? = null, // "ssh" | "tunnel"
    val tunnelConnected: Boolean = false,
    val group: String = "",
    /** control-plane | worker — set for hosts of a cluster the hub built. */
    val k8sRole: String = "",
    val installActive: Boolean = false,
    /** The hub's own version — a host behind it is offered «Обновить». */
    val hubVersion: String = "",
    /** ОС хоста по опросу хаба — значок перед именем. */
    val osInfo: OsInfo? = null,
) {
    /** Version the host actually runs (falls back to what was installed). */
    val shownVersion: String get() = runningVersion?.ifBlank { null } ?: nktVersion

    val outdated: Boolean
        get() = hubVersion.isNotBlank() && shownVersion.isNotBlank() && shownVersion != hubVersion

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
data class HubVersionInfo(
    val current: String,
    val latest: String? = null,
    val updateAvailable: Boolean = false,
    val updatable: Boolean = false,
    val checkedAt: String? = null,
    val checkError: String? = null,
    /** Описание последнего релиза с GitHub — что несёт версия, которой
     * здесь ещё нет. Present only when a check has succeeded and the
     * release carries a body. */
    val notes: String? = null,
)

/**
 * GET /api/hub/vulndb — internal/hub/handlers.go's vulnDBInfoJSON.
 */
data class HubVulnDBInfo(
    val available: Boolean,
    val refreshing: Boolean,
    val updatedAt: String? = null,
    val progress: String? = null,
    val error: String? = null,
)
