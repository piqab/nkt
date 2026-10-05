package com.netknownsthat.domain.usecase

import com.netknownsthat.domain.common.HostTarget
import com.netknownsthat.domain.common.Outcome
import com.netknownsthat.domain.common.getOrDefault
import com.netknownsthat.domain.common.getOrNull
import com.netknownsthat.domain.model.JobsResponse
import com.netknownsthat.domain.model.K8sUsageCluster
import com.netknownsthat.domain.model.UsageResponse
import com.netknownsthat.domain.model.UsageTopEntry
import com.netknownsthat.domain.repository.HostMonitoringRepository

/** Workload sources in the web UI's order; the host itself last. */
val USAGE_SOURCE_ORDER = listOf("docker", "podman", "lxd", "libvirt", "k8s", "k8s_node", "host")

/** CPU first — what is looked at first. */
val USAGE_METRIC_ORDER = listOf("cpu_pct", "mem_bytes", "net_rx_bytes", "net_tx_bytes")

/** Most series one chart shows (the web UI's MAX_SERIES). */
const val USAGE_MAX_SERIES = 8

/** What the usage screen asked for. */
data class UsageQuery(
    val source: String? = null,
    val metric: String = "cpu_pct",
    /** k8s pods only: of this node / namespace; null — all. */
    val node: String? = null,
    val namespace: String? = null,
    /** Subjects on the chart; empty — the top five. */
    val picked: Set<String> = emptySet(),
)

data class UsageData(
    /** The source actually shown (the first available when none was asked). */
    val source: String,
    val metric: String,
    val sources: List<String>,
    val top: List<UsageTopEntry>,
    val cluster: K8sUsageCluster?,
    val series: UsageResponse,
    val chartSubjects: List<String>,
    val jobs: JobsResponse,
)

/**
 * The usage screen in one call: available sources (opening the first working
 * engine, not Docker the host may lack), the top list filtered by k8s node
 * and namespace, and the chart series of the picked (or top) subjects.
 */
class LoadUsageUseCase(private val monitoring: HostMonitoringRepository) {
    suspend operator fun invoke(host: HostTarget, query: UsageQuery): Outcome<UsageData> {
        val sources = monitoring.usageSources(host).getOrDefault(emptyList())
        val available = USAGE_SOURCE_ORDER.filter { it in sources }
        val source = query.source?.takeIf { it in available } ?: available.firstOrNull() ?: "docker"
        // The host has no network series.
        val metric = if (source == "host" && query.metric.startsWith("net_")) "cpu_pct" else query.metric
        val top = when (val r = monitoring.usageTop(host, source, metric, 500)) {
            is Outcome.Failure -> return r
            is Outcome.Success -> r.value.top
        }
        val cluster = if (source == "k8s" || source == "k8s_node") monitoring.k8sCluster(host).getOrNull() else null
        val entries = top.filter { e ->
            source != "k8s" || (
                (query.node == null || cluster?.pods?.get(e.subject) == query.node) &&
                    (query.namespace == null || e.subject.substringBefore('/') == query.namespace)
                )
        }
        val chartSubjects = query.picked.ifEmpty { entries.take(5).map { it.subject }.toSet() }.toList()
        val agg = if (metric.startsWith("net_")) "sum" else "avg"
        val series = if (chartSubjects.isEmpty()) UsageResponse()
        else monitoring.usageSeries(host, source, metric, agg, chartSubjects).getOrDefault(UsageResponse())
        return Outcome.Success(
            UsageData(
                source = source,
                metric = metric,
                sources = available,
                top = entries,
                cluster = cluster,
                series = series,
                chartSubjects = chartSubjects,
                jobs = monitoring.collectorJobs(host).getOrDefault(JobsResponse()),
            ),
        )
    }
}
