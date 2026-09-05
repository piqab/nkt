package com.netknownsthat.app.net.model

import kotlinx.serialization.json.Json
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Decodes every captured endpoint with the model the corresponding screen
 * uses. Same purpose as HostModelsTest and same fixtures directory — this
 * one covers the screens added in phases 3-8.
 *
 * Each test asserts on a value that is genuinely non-default in the fixture,
 * not merely that decoding threw nothing: a field whose name stops matching
 * decodes to its default instead of failing, so only checking a known-true
 * value actually catches that.
 */
class AllEndpointsTest {

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
    fun services() {
        val response = decode<ServicesResponse>("services")
        assertTrue("no services", response.services.isNotEmpty())
        val unit = response.services.first()
        assertTrue("name missing", unit.name.isNotBlank())
        assertTrue("active_state missing", unit.activeState.isNotBlank())
        // The action buttons are built from this list, so an empty one would
        // silently render a service nothing can be done to.
        assertTrue(
            "no service exposes actions",
            response.services.any { it.actions.isNotEmpty() },
        )
    }

    @Test
    fun containers() {
        val response = decode<ContainersResponse>("containers")
        assertTrue("no containers", response.containers.isNotEmpty())
        assertTrue("no networks", response.networks.isNotEmpty())
        assertTrue(
            "nothing decoded as running",
            response.containers.any { it.running },
        )
        assertTrue(
            "no ports decoded",
            response.containers.any { it.ports.isNotEmpty() },
        )
    }

    @Test
    fun podmanLxdAndVms() {
        assertTrue("no podman containers", decode<PodmanResponse>("podman").containers.isNotEmpty())
        assertTrue("no lxd instances", decode<LXDResponse>("lxd").instances.isNotEmpty())
        val vms = decode<VMsResponse>("vms").vms
        assertTrue("no vms", vms.isNotEmpty())
        assertTrue("vcpus did not decode", vms.any { it.vcpus > 0 })
    }

    @Test
    fun miscListeners() {
        val listeners = decode<MiscResponse>("misc").listeners
        assertTrue("no listeners", listeners.isNotEmpty())
        assertTrue("ports did not decode", listeners.any { it.port > 0 })
    }

    @Test
    fun users() {
        val users = decode<UsersResponse>("users").users
        assertTrue("no users", users.isNotEmpty())
        assertTrue("username missing", users.first().username.isNotBlank())
        assertTrue("role missing", users.first().role.isNotBlank())
    }

    @Test
    fun monitorTargets() {
        val response = decode<TargetsResponse>("monitor_targets")
        assertTrue("no targets", response.targets.isNotEmpty())
        assertTrue("labels missing", response.targets.all { it.label.isNotBlank() })
        assertTrue(
            "24h stats did not decode",
            response.targets.any { it.checks24h > 0 },
        )
    }

    @Test
    fun monitorOutagesUsageAndJobs() {
        assertTrue("no outages", decode<OutagesResponse>("monitor_outages").outages.isNotEmpty())

        val usage = decode<UsageResponse>("monitor_usage")
        assertTrue("no usage points", usage.points.isNotEmpty())
        assertTrue("metric missing", usage.metric.isNotBlank())

        val top = decode<UsageTopResponse>("monitor_usage_top").top
        assertTrue("no top entries", top.isNotEmpty())
        assertTrue("totals did not decode", top.any { it.total > 0 })

        val jobs = decode<JobsResponse>("monitor_jobs")
        assertTrue("no jobs", jobs.jobs.isNotEmpty())
        assertTrue("intervals missing", jobs.intervals.isNotEmpty())
    }

    @Test
    fun vulnerabilitiesWithoutAScan() {
        // The fixture was taken on a host that had never been scanned: the
        // point is that a missing `scan` decodes as null rather than failing.
        val response = decode<VulnResponse>("vulnerabilities")
        assertTrue("scan should be absent in this fixture", response.scan == null)
    }

    @Test
    fun configs() {
        val files = decode<ConfigsResponse>("configs").files
        assertTrue("no config files", files.isNotEmpty())
        assertTrue("paths missing", files.all { it.path.isNotBlank() })
        assertTrue("nothing marked editable", files.any { it.editable })
    }

    @Test
    fun firewall() {
        val response = decode<FirewallResponse>("firewall")
        assertTrue("no rules", response.rules.isNotEmpty())
        assertTrue("no managers", response.managers.isNotEmpty())
        assertTrue("no backends", response.backends.isNotEmpty())
        assertTrue(
            "rule text (raw) did not decode",
            response.rules.any { it.raw.isNotBlank() },
        )

        val numbered = decode<FirewallNumberedResponse>("firewall_rules")
        assertTrue("no added rules", numbered.added.isNotEmpty())
    }

    @Test
    fun certificates() {
        val response = decode<CertificatesResponse>("certificates")
        assertTrue("no certificates", response.certificates.isNotEmpty())
        val cert = response.certificates.first()
        assertTrue("names missing", cert.names.isNotEmpty())
        assertTrue("not_after missing", cert.notAfter.isNotBlank())
        assertTrue("summary missing", response.summary != null)
        // Renewal is what separates "will renew itself" from "will expire
        // one day and take the site down" — worth failing on if it stops
        // decoding.
        assertTrue(
            "renewal info did not decode for any certificate",
            response.certificates.any { it.renewal != null },
        )
    }

    @Test
    fun topology() {
        val response = decode<TopologyResponse>("topology")
        assertTrue("no nodes", response.nodes.isNotEmpty())
        assertTrue("no edges", response.edges.isNotEmpty())
        assertTrue("no stats", response.stats.isNotEmpty())
        assertTrue("node labels missing", response.nodes.all { it.label.isNotBlank() })
        // Every edge must point at nodes that exist, or the map draws lines
        // to nowhere.
        val ids = response.nodes.map { it.id }.toSet()
        assertTrue(
            "edges reference unknown nodes",
            response.edges.all { it.from in ids && it.to in ids },
        )
    }
}
