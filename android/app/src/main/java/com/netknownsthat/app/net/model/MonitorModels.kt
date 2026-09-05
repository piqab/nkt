package com.netknownsthat.app.net.model

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/** Monitoring and vulnerabilities — the phase-4 screens. */

@Serializable
data class TargetsResponse(
    val targets: List<Target> = emptyList(),
    val interval: String = "",
    val simulated: Boolean = false,
)

@Serializable
data class Target(
    val id: Long = 0,
    val label: String = "",
    val kind: String = "",
    val host: String = "",
    val port: Int = 0,
    val path: String = "",
    val source: String = "",
    val service: String = "",
    val enabled: Boolean = true,
    @SerialName("last_check") val lastCheck: String = "",
    @SerialName("last_ok") val lastOk: Boolean? = null,
    /** Fractional: sub-millisecond probes are common on a LAN, and the Go
     * side keeps it as float64 rather than rounding. */
    @SerialName("last_latency_ms") val lastLatencyMs: Double = 0.0,
    @SerialName("last_error") val lastError: String = "",
    @SerialName("checks_24h") val checks24h: Int = 0,
    @SerialName("failures_24h") val failures24h: Int = 0,
    @SerialName("uptime_24h") val uptime24h: Double = 0.0,
    @SerialName("avg_latency_24h") val avgLatency24h: Double = 0.0,
)

@Serializable
data class OutagesResponse(
    val outages: List<Outage> = emptyList(),
)

@Serializable
data class Outage(
    @SerialName("target_id") val targetId: Long = 0,
    val label: String = "",
    val start: String = "",
    val end: String = "",
    val checks: Int = 0,
    val error: String = "",
)

@Serializable
data class UsageResponse(
    val metric: String = "",
    val source: String = "",
    val total: Int = 0,
    val simulated: Boolean = false,
    val points: List<UsagePoint> = emptyList(),
)

@Serializable
data class UsagePoint(
    val bucket: String = "",
    val subject: String = "",
    val value: Double = 0.0,
)

@Serializable
data class UsageTopResponse(
    val metric: String = "",
    val source: String = "",
    val top: List<UsageTopEntry> = emptyList(),
)

@Serializable
data class UsageTopEntry(
    val subject: String = "",
    val total: Double = 0.0,
    val samples: Int = 0,
)

@Serializable
data class JobsResponse(
    val enabled: Boolean = false,
    val jobs: List<Job> = emptyList(),
    val intervals: Map<String, String> = emptyMap(),
)

@Serializable
data class Job(
    val name: String = "",
    @SerialName("last_run") val lastRun: String = "",
    @SerialName("last_count") val lastCount: Int = 0,
    @SerialName("duration_ms") val durationMs: Long = 0,
    val interval: String = "",
    val runs: Int = 0,
)

/**
 * GET /api/vulnerabilities. `scan` is absent until this host has been
 * scanned at least once — a fresh install legitimately returns nothing but
 * the two status fields (see handleVulnerabilities).
 */
@Serializable
data class VulnResponse(
    val scanning: Boolean = false,
    val progress: String = "",
    val scan: VulnScan? = null,
    val error: String? = null,
)

@Serializable
data class VulnScan(
    val available: Boolean = false,
    val findings: List<VulnFinding> = emptyList(),
    /** False on a host's very first scan: there was nothing to diff against,
     * so `new` on the findings below means nothing yet. */
    val compared: Boolean = false,
    @SerialName("new_count") val newCount: Int = 0,
    @SerialName("fixed_count") val fixedCount: Int = 0,
    val warnings: List<String> = emptyList(),
    @SerialName("scanned_at") val scannedAt: String = "",
)

@Serializable
data class VulnFinding(
    val id: String = "",
    @SerialName("package") val packageName: String = "",
    @SerialName("installed_version") val installedVersion: String = "",
    /** Empty when no fix exists yet — distinct from "nothing to do". */
    @SerialName("fixed_version") val fixedVersion: String = "",
    val severity: String = "",
    val title: String = "",
    val url: String = "",
    val new: Boolean = false,
    /** Empty for the host's own packages; a container image reference when
     * the finding came from inside one. */
    val target: String = "",
)
