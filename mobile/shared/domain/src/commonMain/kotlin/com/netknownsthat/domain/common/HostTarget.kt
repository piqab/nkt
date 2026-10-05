package com.netknownsthat.domain.common

/**
 * Which machine a host-level call addresses — passed explicitly to every
 * host repository method instead of a global "current host", so a request
 * can never be scoped to the host opened a moment ago by mistake.
 *
 * [LOCAL] is the hub's own machine (the "localhost" row), every other id a
 * managed host. The hub API serves them at /hosts/local/… and /hosts/{id}/….
 */
data class HostTarget(val id: Long) {
    val isLocal: Boolean get() = id == LOCAL_ID

    companion object {
        const val LOCAL_ID = -1L
        val LOCAL = HostTarget(LOCAL_ID)
    }
}

/** Where a background job lives: on a host, or on the hub itself. */
sealed interface JobOwner {
    data class Host(val target: HostTarget) : JobOwner
    data object Hub : JobOwner
}
