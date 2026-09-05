package com.netknownsthat.app.net

import com.netknownsthat.app.net.model.HubHost
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow

/**
 * Mirrors web/src/api.ts's `hostScope` exactly: which managed host every
 * subsequent API call should be scoped to, expressed purely as a URL path
 * prefix — `null` means "talk to the hub itself" (or, for a plain
 * single-host install with no hub at all, the only meaning there is), and
 * [HubHost.LOCAL_HOST_ID] means the hub's own synthetic "localhost" row.
 *
 * Anything under `/auth/` is deliberately never prefixed regardless of the current
 * selection — authentication always targets the hub's own session, never a
 * per-host one (the hub swaps in that host's own cached bootstrap-admin
 * cookie server-side, transparently — see internal/hub/proxy.go's
 * Manager.Proxy).
 */
class HostScope {
    private val _currentHostId = MutableStateFlow<Long?>(null)
    val currentHostId: StateFlow<Long?> = _currentHostId

    fun select(hostId: Long?) {
        _currentHostId.value = hostId
    }

    /** path must start with "/", e.g. "/overview", "/auth/me". */
    fun scoped(path: String): String {
        if (path.startsWith("/auth/")) return path
        return when (val id = _currentHostId.value) {
            null -> path
            HubHost.LOCAL_HOST_ID -> "/hosts/local$path"
            else -> "/hosts/$id$path"
        }
    }
}
