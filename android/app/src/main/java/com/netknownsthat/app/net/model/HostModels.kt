package com.netknownsthat.app.net.model

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

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
@Serializable
data class Overview(
    val host: HostInfo,
    val mode: String,
    val scanned: String,
    @SerialName("scan_ms") val scanMs: Long = 0,
    val simulated: Boolean = false,
    val version: String = "",
    val counts: Map<String, Int> = emptyMap(),
    val findings: Map<String, Int> = emptyMap(),
    val certificates: CertSummary? = null,
    val availability: AvailabilitySummary? = null,
)

@Serializable
data class HostInfo(
    val mode: String = "",
    val hostname: String = "",
    val kernel: String = "",
    val os: String = "",
    val notes: List<String> = emptyList(),
)

@Serializable
data class CertSummary(
    val total: Int = 0,
    val expired: Int = 0,
    val expiring: Int = 0,
    val unreadable: Int = 0,
    val unmanaged: Int = 0,
    /** -1 when nothing has a future expiry to count down to. */
    @SerialName("soonest_days") val soonestDays: Int = -1,
    @SerialName("soonest_name") val soonestName: String = "",
)

@Serializable
data class AvailabilitySummary(
    val targets: Int = 0,
    val up: Int = 0,
    val down: Int = 0,
    @SerialName("avg_uptime") val avgUptime: Double = 0.0,
)

/** GET /api/findings */
@Serializable
data class FindingsResponse(
    val findings: List<Finding> = emptyList(),
    val counts: Map<String, Int> = emptyMap(),
    val total: Int = 0,
)

@Serializable
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
@Serializable
data class InterfacesResponse(
    val interfaces: List<NetworkInterface> = emptyList(),
)

@Serializable
data class NetworkInterface(
    val name: String = "",
    val mac: String? = null,
    val mtu: Int = 0,
    /** Administrative state; [lowerUp] is what says whether a link partner
     * is actually answering — see the Go field's own comment. */
    val up: Boolean = false,
    @SerialName("lower_up") val lowerUp: Boolean = false,
    val loopback: Boolean = false,
    val addresses: List<String> = emptyList(),
    @SerialName("rx_bytes") val rxBytes: Long = 0,
    @SerialName("tx_bytes") val txBytes: Long = 0,
    @SerialName("rx_errors") val rxErrors: Long = 0,
    @SerialName("rx_dropped") val rxDropped: Long = 0,
    @SerialName("tx_errors") val txErrors: Long = 0,
    @SerialName("tx_dropped") val txDropped: Long = 0,
    @SerialName("docker_network") val dockerNetwork: String? = null,
    @SerialName("attached_containers") val attachedContainers: Int = 0,
)

/** GET /api/audit */
@Serializable
data class AuditResponse(
    val entries: List<AuditEntry> = emptyList(),
)

@Serializable
data class AuditEntry(
    val id: Long = 0,
    val ts: String = "",
    val username: String = "",
    val action: String = "",
    val target: String = "",
    val result: String = "",
    val detail: String = "",
)
