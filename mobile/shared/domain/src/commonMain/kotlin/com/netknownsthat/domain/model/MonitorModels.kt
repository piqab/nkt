package com.netknownsthat.domain.model


/** Monitoring and vulnerabilities — the phase-4 screens. */

data class TargetsResponse(
    val targets: List<Target> = emptyList(),
    val interval: String = "",
    val simulated: Boolean = false,
)

/** A target to add by hand (POST /monitor/targets). */
data class NewTarget(
    val label: String = "",
    val kind: String = "icmp",
    val host: String = "",
    val port: Int = 0,
    val path: String = "/",
)

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
    val lastCheck: String = "",
    val lastOk: Boolean? = null,
    /** Fractional: sub-millisecond probes are common on a LAN, and the Go
     * side keeps it as float64 rather than rounding. */
    val lastLatencyMs: Double = 0.0,
    val lastError: String = "",
    val checks24h: Int = 0,
    val failures24h: Int = 0,
    val uptime24h: Double = 0.0,
    val avgLatency24h: Double = 0.0,
)

data class OutagesResponse(
    val outages: List<Outage> = emptyList(),
)

data class Outage(
    val targetId: Long = 0,
    val label: String = "",
    val start: String = "",
    val end: String = "",
    val checks: Int = 0,
    val error: String = "",
)

data class UsageResponse(
    val metric: String = "",
    val source: String = "",
    val total: Double? = null,
    val simulated: Boolean = false,
    val points: List<UsagePoint> = emptyList(),
)

data class UsagePoint(
    val bucket: String = "",
    val subject: String = "",
    val value: Double = 0.0,
)

data class UsageTopResponse(
    val metric: String = "",
    val source: String = "",
    val top: List<UsageTopEntry> = emptyList(),
)

data class UsageTopEntry(
    val subject: String = "",
    val total: Double = 0.0,
    val samples: Int = 0,
)

data class JobsResponse(
    val enabled: Boolean = false,
    val jobs: List<Job> = emptyList(),
    val intervals: Map<String, String> = emptyMap(),
)

data class Job(
    val name: String = "",
    val lastRun: String = "",
    val lastCount: Int = 0,
    val durationMs: Long = 0,
    val interval: String = "",
    val runs: Int = 0,
)

/**
 * GET /api/vulnerabilities. `scan` is absent until this host has been
 * scanned at least once — a fresh install legitimately returns nothing but
 * the two status fields (see handleVulnerabilities).
 */
data class VulnResponse(
    val scanning: Boolean = false,
    val progress: String = "",
    val scan: VulnScan? = null,
    val error: String? = null,
)

data class VulnScan(
    val available: Boolean = false,
    val findings: List<VulnFinding> = emptyList(),
    /** False on a host's very first scan: there was nothing to diff against,
     * so `new` on the findings below means nothing yet. */
    val compared: Boolean = false,
    val newCount: Int = 0,
    val fixedCount: Int = 0,
    val warnings: List<String> = emptyList(),
    val scannedAt: String = "",
)

data class VulnFinding(
    val id: String = "",
    val packageName: String = "",
    val installedVersion: String = "",
    /** Empty when no fix exists yet — distinct from "nothing to do". */
    val fixedVersion: String = "",
    val severity: String = "",
    val title: String = "",
    val url: String = "",
    val new: Boolean = false,
    /** Empty for the host's own packages; a container image reference when
     * the finding came from inside one. */
    val target: String = "",
)

/** GET /api/monitor/usage/sources — sources with data in the last day. */
data class UsageSourcesResponse(val sources: List<String> = emptyList())

/** GET /api/monitor/usage/k8s — nodes, and the node of every pod
 * ("namespace/pod" → node); empty off a control plane. */
data class K8sUsageCluster(
    val nodes: List<K8sUsageNode> = emptyList(),
    val pods: Map<String, String> = emptyMap(),
)

data class K8sUsageNode(
    val name: String = "",
    val ready: Boolean = false,
    val ip: String = "",
    val controlPlane: Boolean = false,
)
