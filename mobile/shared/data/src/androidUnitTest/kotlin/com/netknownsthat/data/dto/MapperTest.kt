package com.netknownsthat.data.dto

import com.netknownsthat.data.remote.NktJson
import com.netknownsthat.data.mapper.toDomain
import kotlinx.serialization.json.Json
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * DTO → domain: values survive the mapping (a field the mapper forgot would
 * fall back to its default and show up here as a mismatch).
 */
class MapperTest {
    private val json = NktJson

    private inline fun <reified T> decode(name: String): T =
        json.decodeFromString(
            checkNotNull(javaClass.getResourceAsStream("/api/$name.json")) { "missing fixture $name" }
                .bufferedReader().use { it.readText() },
        )

    @Test
    fun overviewAndFindings() {
        val dto = decode<FindingsResponseDto>("findings")
        val domain = dto.toDomain()
        assertEquals(dto.findings.size, domain.findings.size)
        assertEquals(dto.findings.first().title, domain.findings.first().title)
        assertEquals(dto.counts, domain.counts)
    }

    @Test
    fun hubHostsKeepComputedState() {
        val dto = json.decodeFromString<List<HubHostDto>>(
            checkNotNull(javaClass.getResourceAsStream("/api/hub_hosts.json")).bufferedReader().readText(),
        )
        val domain = dto.map { it.toDomain() }
        assertEquals(dto.map { it.id }, domain.map { it.id })
        assertEquals(dto.map { it.outdated }, domain.map { it.outdated })
        assertTrue(domain.any { it.group.isNotBlank() })
    }

    @Test
    fun nestedListsAndMaps() {
        val monitoring = decode<MonitoringOverviewDto>("hub_monitoring").toDomain()
        assertTrue(monitoring.hosts.first { it.hasData && it.id > 0 }.disks.isNotEmpty())
        val f2b = decode<Fail2banResponseDto>("fail2ban").toDomain()
        assertTrue(f2b.state.jails.first { it.name == "sshd" }.bans.isNotEmpty())
        val inspect = decode<ContainerInspectDto>("container_inspect").toDomain()
        assertTrue(inspect.env.any { it.origin == "image" } && inspect.labels.isNotEmpty())
    }

    /** Go writes an empty slice as null — seen live on an empty hub job list. */
    @Test
    fun nullListIsEmpty() {
        val r = json.decodeFromString(JobListResponseDto.serializer(), """{"active":0,"jobs":[],"kinds":null,"total":0}""")
        assertEquals(emptyList<String>(), r.kinds)
    }
}
