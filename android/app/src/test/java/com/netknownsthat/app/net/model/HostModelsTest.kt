package com.netknownsthat.app.net.model

import kotlinx.serialization.json.Json
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Decodes real server responses with the models the app actually uses.
 *
 * The fixtures in src/test/resources/api are not hand-written: they are what
 * `nkt serve` (NKT_MODE=fixtures) returned from /api/overview, /api/findings,
 * /api/interfaces and /api/audit. A renamed or retyped field on the Go side
 * is otherwise invisible until the screen is open on a phone — which is
 * exactly the check that cannot be run while writing this code, so it is
 * worth having one that can.
 *
 * Regenerate after changing the Go response shapes: start the server in
 * fixtures mode, log in, and save each endpoint's body over these files.
 */
class HostModelsTest {

    // ignoreUnknownKeys mirrors HubClient's own Json configuration: the app
    // must tolerate fields it does not model (overview alone returns
    // firewall/sources/services that no phase-2 screen reads yet).
    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
    }

    private fun fixture(name: String): String =
        checkNotNull(javaClass.getResourceAsStream("/api/$name")) { "missing fixture $name" }
            .bufferedReader().use { it.readText() }

    @Test
    fun `overview decodes with the fields the screen shows`() {
        val overview = json.decodeFromString<Overview>(fixture("overview.json"))

        assertTrue("hostname is blank", overview.host.hostname.isNotBlank())
        assertTrue("mode is blank", overview.mode.isNotBlank())
        assertTrue("scanned is blank", overview.scanned.isNotBlank())
        assertTrue("no counts decoded", overview.counts.isNotEmpty())
        // The overview screen indexes counts by these exact keys; a rename on
        // the Go side would silently blank the card out rather than fail.
        assertTrue("endpoints count missing", overview.counts.containsKey("endpoints"))
        assertTrue("containers count missing", overview.counts.containsKey("containers"))
        assertTrue("certificates summary missing", overview.certificates != null)
        assertTrue("availability summary missing", overview.availability != null)
    }

    @Test
    fun `findings decode with severities the screen filters on`() {
        val response = json.decodeFromString<FindingsResponse>(fixture("findings.json"))

        assertTrue("no findings in fixture", response.findings.isNotEmpty())
        assertEquals(
            "total disagrees with the list",
            response.total,
            response.findings.size,
        )
        response.findings.forEach {
            assertTrue("finding without id", it.id.isNotBlank())
            assertTrue("finding without severity", it.severity.isNotBlank())
        }
        // Chips are built from `counts`, and each key must be a severity the
        // screen has a label for — an unlabelled one would render raw.
        val known = setOf("critical", "high", "medium", "low", "info")
        response.counts.keys.forEach {
            assertTrue("unexpected severity key: $it", it in known)
        }
    }

    @Test
    fun `interfaces decode including the carrier state`() {
        val response = json.decodeFromString<InterfacesResponse>(fixture("interfaces.json"))

        assertTrue("no interfaces in fixture", response.interfaces.isNotEmpty())
        val loopback = response.interfaces.firstOrNull { it.loopback }
        assertTrue("no loopback interface decoded", loopback != null)
        assertTrue("loopback should be up", loopback!!.up)
        assertTrue("loopback should have addresses", loopback.addresses.isNotEmpty())
        // Asserted explicitly because a Boolean with a default decodes to
        // false rather than failing when its name stops matching — checking
        // a value known to be true in the fixture is what catches that.
        assertTrue("loopback should report carrier (lower_up)", loopback.lowerUp)
        assertTrue("byte counters did not decode", loopback.rxBytes > 0)
    }

    @Test
    fun `audit entries decode`() {
        val response = json.decodeFromString<AuditResponse>(fixture("audit.json"))

        response.entries.forEach {
            assertTrue("audit entry without action", it.action.isNotBlank())
            assertTrue("audit entry without timestamp", it.ts.isNotBlank())
        }
    }
}
