package com.netknownsthat.app.net.model

import kotlinx.serialization.json.Json
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/** Endpoints behind the parity updates of existing host screens (inspect,
 * ClamAV, usage sources and k8s, k8s availability targets, map folding). */
class ParityEndpointsTest {
    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
    }

    private inline fun <reified T> decode(name: String): T =
        json.decodeFromString(
            checkNotNull(javaClass.getResourceAsStream("/api/$name.json")) { "missing fixture $name" }
                .bufferedReader().use { it.readText() }
        )

    @Test
    fun containerInspect() {
        val r = decode<ContainerInspect>("container_inspect")
        assertEquals("acme-app", r.name)
        assertTrue(r.env.isNotEmpty() && r.env.all { it.masked } && !r.revealed)
        assertTrue(r.env.any { it.origin == "image" } && r.env.any { it.origin == "container" })
        assertTrue(r.ports.first().hostPort.isNotBlank() && r.mounts.isNotEmpty() && r.networks.first().aliases.isNotEmpty())
        assertTrue(r.memoryLimit > 0 && r.nanoCpus > 0)
    }

    @Test
    fun clamav() {
        val r = decode<ClamResponse>("clamav")
        assertTrue(r.paths.isNotEmpty() && r.images.isNotEmpty() && r.apt)
    }

    @Test
    fun usage() {
        val sources = decode<UsageSourcesResponse>("usage_sources").sources
        assertTrue("k8s_node" in sources && "host" in sources)
        val k8s = decode<K8sUsageCluster>("usage_k8s")
        assertTrue(k8s.nodes.any { it.controlPlane } && k8s.nodes.any { !it.controlPlane })
        assertTrue(k8s.pods.values.any { it.startsWith("lab-w") })
        val series = decode<UsageResponse>("usage_k8s_node")
        assertTrue(series.points.isNotEmpty() && series.points.map { it.subject }.toSet().size > 1)
    }

    @Test
    fun k8sTargets() {
        val r = decode<TargetsResponse>("monitor_targets")
        assertTrue(r.targets.any { it.source == "kubernetes" && it.kind == "icmp" })
    }

    @Test
    fun topologyGroups() {
        val r = decode<TopologyResponse>("topology")
        val endpoints = r.nodes.filter { it.kind == "endpoint" }
        assertTrue(endpoints.isNotEmpty() && endpoints.any { it.group.isNotBlank() })
    }
}
