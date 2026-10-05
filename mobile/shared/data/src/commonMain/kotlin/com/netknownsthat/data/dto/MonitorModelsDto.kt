package com.netknownsthat.data.dto

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/** Monitoring and vulnerabilities — the phase-4 screens. */

@Serializable
data class TargetsResponseDto(
    val targets: List<TargetDto> = emptyList(),
    val interval: String = "",
    val simulated: Boolean = false,
)

@Serializable
data class TargetDto(
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
data class OutagesResponseDto(
    val outages: List<OutageDto> = emptyList(),
)

@Serializable
data class OutageDto(
    @SerialName("target_id") val targetId: Long = 0,
    val label: String = "",
    val start: String = "",
    val end: String = "",
    val checks: Int = 0,
    val error: String = "",
)

@Serializable
data class UsageResponseDto(
    val metric: String = "",
    val source: String = "",
    val total: Double? = null,
    val simulated: Boolean = false,
    val points: List<UsagePointDto> = emptyList(),
)

@Serializable
data class UsagePointDto(
    val bucket: String = "",
    val subject: String = "",
    val value: Double = 0.0,
)

@Serializable
data class UsageTopResponseDto(
    val metric: String = "",
    val source: String = "",
    val top: List<UsageTopEntryDto> = emptyList(),
)

@Serializable
data class UsageTopEntryDto(
    val subject: String = "",
    val total: Double = 0.0,
    val samples: Int = 0,
)

@Serializable
data class JobsResponseDto(
    val enabled: Boolean = false,
    val jobs: List<JobDto> = emptyList(),
    val intervals: Map<String, String> = emptyMap(),
)

@Serializable
data class JobDto(
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
data class VulnResponseDto(
    val scanning: Boolean = false,
    val progress: String = "",
    val scan: VulnScanDto? = null,
    val error: String? = null,
)

@Serializable
data class VulnScanDto(
    val available: Boolean = false,
    val findings: List<VulnFindingDto> = emptyList(),
    /** False on a host's very first scan: there was nothing to diff against,
     * so `new` on the findings below means nothing yet. */
    val compared: Boolean = false,
    @SerialName("new_count") val newCount: Int = 0,
    @SerialName("fixed_count") val fixedCount: Int = 0,
    val warnings: List<String> = emptyList(),
    @SerialName("scanned_at") val scannedAt: String = "",
)

@Serializable
data class VulnFindingDto(
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

/** GET /api/monitor/usage/sources — sources with data in the last day. */
@Serializable
data class UsageSourcesResponseDto(val sources: List<String> = emptyList())

/** GET /api/monitor/usage/k8s — nodes, and the node of every pod
 * ("namespace/pod" → node); empty off a control plane. */
@Serializable
data class K8sUsageClusterDto(
    val nodes: List<K8sUsageNodeDto> = emptyList(),
    val pods: Map<String, String> = emptyMap(),
)

@Serializable
data class K8sUsageNodeDto(
    val name: String = "",
    val ready: Boolean = false,
    val ip: String = "",
    @SerialName("control_plane") val controlPlane: Boolean = false,
)
