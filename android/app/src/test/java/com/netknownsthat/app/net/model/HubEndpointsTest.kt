package com.netknownsthat.app.net.model

import kotlinx.serialization.json.Json
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Hub-level endpoints of the mobile-first screens (alerts, jobs, fail2ban,
 * monitoring, deployments, reboot), decoded from responses captured from a
 * running `nkt hub`. Same rule as AllEndpointsTest: assert on values that
 * are really non-default in the fixture.
 */
class HubEndpointsTest {
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
    fun events() {
        val r = decode<HubEventsResponse>("hub_events")
        assertTrue("no events", r.events.isNotEmpty())
        val e = r.events.first()
        assertTrue(e.id > 0 && e.hostId != 0L && e.hostName.isNotBlank() && e.kind.isNotBlank())
        assertTrue("kinds", "unreachable" in r.kinds)
        assertTrue("notify", r.notify["unreachable"] == true)
    }

    @Test
    fun jobs() {
        val r = decode<JobListResponse>("jobs")
        assertTrue(r.jobs.isNotEmpty() && r.total > 0)
        val j = r.jobs.first()
        assertTrue(j.id > 0 && j.title.isNotBlank() && j.createdAt.isNotBlank())
        assertTrue("finished job", j.finished)
    }

    @Test
    fun jobLog() {
        val r = decode<JobLogResponse>("job_log")
        assertTrue(r.job.id > 0)
        assertTrue("no lines", r.lines.isNotEmpty())
        assertTrue(r.lines.all { it.seq > 0 && it.text.isNotEmpty() })
    }

    @Test
    fun hostFail2ban() {
        val r = decode<Fail2banResponse>("fail2ban")
        assertTrue(r.state.installed && r.state.running && r.manualReady)
        assertEquals("nkt-manual", r.manualJail)
        val sshd = r.state.jails.first { it.name == "sshd" }
        assertTrue(sshd.bans.isNotEmpty() && sshd.bans.first().ip.isNotBlank() && sshd.banTime > 0)
    }

    @Test
    fun fleetBanned() {
        val r = decode<FleetBannedResponse>("hub_f2b_banned")
        assertTrue(r.hosts.any { it.installed && it.banned > 0 })
        assertTrue(r.ips.isNotEmpty() && r.ips.first().hosts.first().jails.isNotEmpty())
    }

    @Test
    fun rebootPreview() {
        val r = decode<RebootPreview>("reboot_preview")
        assertTrue((r.running["docker"] ?: 0) > 0)
        assertTrue(r.noAutostart.isNotEmpty() && r.noAutostart.first().reason.isNotBlank())
    }

    @Test
    fun monitoring() {
        val r = decode<MonitoringOverview>("hub_monitoring")
        val h = r.hosts.first { it.hasData && it.id > 0 }
        assertTrue(h.cpuMax > 0 && h.memTotal > 0 && h.disks.isNotEmpty())
        assertTrue("eta", r.hosts.any { host -> host.disks.any { it.etaDays != null } })
        assertTrue(r.insights.isNotEmpty() && r.insights.first().path.startsWith("/"))
        assertTrue(r.clusters.isNotEmpty() && r.clusters.first().nodes.any { it.controlPlane })
    }

    @Test
    fun pipelines() {
        val r = decode<PipelinesResponse>("hub_pipelines")
        assertTrue(r.pipelines.isNotEmpty() && r.pipelines.first().action == "compose")
        val left = r.pipelines.flatMap { it.leftovers }
        assertTrue("leftovers", left.isNotEmpty() && left.first().hostId > 0 && left.first().project.isNotBlank())
    }
}
