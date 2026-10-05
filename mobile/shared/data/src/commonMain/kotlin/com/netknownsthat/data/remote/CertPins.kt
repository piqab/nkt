package com.netknownsthat.data.remote

import com.netknownsthat.data.local.LocalStore
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch
import kotlin.concurrent.Volatile

/**
 * Trust-on-first-use pins of self-signed hub certificates, one per
 * "host:port". A hub with NKT_TLS_ENABLED makes its own certificate, so chain
 * validation cannot work; the first certificate seen is remembered and any
 * different one later is refused — the same model the hub uses for its own
 * tunnels. The platform engines (androidMain/iosMain) consult this.
 */
class CertPins(private val scope: CoroutineScope, private val local: LocalStore) {
    private val pins = mutableMapOf<String, String>()

    /** The hub currently talked to — set with the base URL. */
    @Volatile
    var currentAuthority: String? = null

    /** Set by an engine when a pinned certificate did not match, so the
     * resulting I/O failure can be reported as what it is. */
    @Volatile
    var lastMismatch: Mismatch? = null

    data class Mismatch(val authority: String, val pinned: String, val presented: String)

    suspend fun load() {
        val loaded = local.pinnedCerts().mapNotNull { entry ->
            val authority = entry.substringBefore('=', "")
            val fingerprint = entry.substringAfter('=', "")
            if (authority.isNotEmpty() && fingerprint.isNotEmpty()) authority to fingerprint else null
        }
        synchronized { pins.clear(); pins.putAll(loaded) }
    }

    fun pinnedFor(authority: String): String? = synchronized { pins[authority] }

    /**
     * Checks [fingerprint] presented by [authority]: true — trusted (pinned
     * now if it was the first time), false — a different certificate than
     * the pinned one.
     */
    fun verify(authority: String, fingerprint: String): Boolean {
        val pinned = synchronized { pins[authority] }
        return when (pinned) {
            null -> { record(authority, fingerprint); true }
            fingerprint -> true
            else -> { lastMismatch = Mismatch(authority, pinned, fingerprint); false }
        }
    }

    fun forget(authority: String) {
        synchronized { pins.remove(authority) }
        persist()
    }

    private fun record(authority: String, fingerprint: String) {
        synchronized { pins[authority] = fingerprint }
        persist()
    }

    private fun persist() {
        val snapshot = synchronized { pins.entries.map { "${it.key}=${it.value}" }.toSet() }
        scope.launch { local.setPinnedCerts(snapshot) }
    }

    // A tiny lock: pins are read on network threads and written from
    // coroutines; kotlinx.atomicfu would be one more dependency for this.
    private val lock = Lock()
    private inline fun <T> synchronized(block: () -> T): T = lock.withLock(block)
}

/** Minimal cross-platform mutual exclusion (see Lock.android/ios). */
expect class Lock() {
    fun lock()
    fun unlock()
}

inline fun <T> Lock.withLock(block: () -> T): T {
    lock()
    try {
        return block()
    } finally {
        unlock()
    }
}
