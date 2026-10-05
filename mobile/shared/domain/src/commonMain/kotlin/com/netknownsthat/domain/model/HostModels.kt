package com.netknownsthat.domain.model


/**
 * Models for the read-only, host-scoped endpoints (the plan's phase 2).
 *
 * Every field below is taken from the Go side rather than from what a
 * response happened to contain: internal/api/handlers_inventory.go for the
 * hand-written map shapes (handleOverview, handleFindings, handleInterfaces,
 * handleAudit), internal/model/model.go and internal/store for the structs
 * they embed. Anything the Go side marks `omitempty` is nullable or
 * defaulted here, since it genuinely can be absent.
 */

/** GET /api/overview — assembled by hand in handleOverview, not a struct. */
data class Overview(
    val host: HostInfo,
    val mode: String,
    val scanned: String,
    val scanMs: Long = 0,
    val simulated: Boolean = false,
    val version: String = "",
    val counts: Map<String, Int> = emptyMap(),
    val findings: Map<String, Int> = emptyMap(),
    val certificates: CertSummary? = null,
    val availability: AvailabilitySummary? = null,
)

data class HostInfo(
    val mode: String = "",
    val hostname: String = "",
    val kernel: String = "",
    val os: String = "",
    val notes: List<String> = emptyList(),
)

data class CertSummary(
    val total: Int = 0,
    val expired: Int = 0,
    val expiring: Int = 0,
    val unreadable: Int = 0,
    val unmanaged: Int = 0,
    /** -1 when nothing has a future expiry to count down to. */
    val soonestDays: Int = -1,
    val soonestName: String = "",
)

data class AvailabilitySummary(
    val targets: Int = 0,
    val up: Int = 0,
    val down: Int = 0,
    val avgUptime: Double = 0.0,
)

/** GET /api/findings */
data class FindingsResponse(
    val findings: List<Finding> = emptyList(),
    val counts: Map<String, Int> = emptyMap(),
    val total: Int = 0,
)

data class Finding(
    val id: String = "",
    val rule: String = "",
    val severity: String = "",
    val title: String = "",
    val detail: String = "",
    val service: String = "",
    val `object`: String? = null,
    val file: String? = null,
    val line: Int = 0,
    val suggestion: String? = null,
    val refs: List<String> = emptyList(),
)

/** GET /api/interfaces */
data class InterfacesResponse(
    val interfaces: List<NetworkInterface> = emptyList(),
)

data class NetworkInterface(
    val name: String = "",
    val mac: String? = null,
    val mtu: Int = 0,
    /** Administrative state; [lowerUp] is what says whether a link partner
     * is actually answering — see the Go field's own comment. */
    val up: Boolean = false,
    val lowerUp: Boolean = false,
    val loopback: Boolean = false,
    val addresses: List<String> = emptyList(),
    val rxBytes: Long = 0,
    val txBytes: Long = 0,
    val rxErrors: Long = 0,
    val rxDropped: Long = 0,
    val txErrors: Long = 0,
    val txDropped: Long = 0,
    val dockerNetwork: String? = null,
    val attachedContainers: Int = 0,
)

/** GET /api/audit */
data class AuditResponse(
    val entries: List<AuditEntry> = emptyList(),
)

data class AuditEntry(
    val id: Long = 0,
    val ts: String = "",
    val username: String = "",
    val action: String = "",
    val target: String = "",
    val result: String = "",
    val detail: String = "",
)
