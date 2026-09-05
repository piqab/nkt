package com.netknownsthat.app.net

import com.netknownsthat.app.net.model.HubHost
import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * Host scoping is a single string prefix, and getting it wrong sends a
 * request to the wrong machine entirely — which is how the About screen
 * ended up asking a managed host for the hub's own version and being told
 * the method does not exist.
 */
class HostScopeTest {

    @Test
    fun `nothing is prefixed while no host is selected`() {
        val scope = HostScope()
        assertEquals("/overview", scope.scoped("/overview"))
    }

    @Test
    fun `a selected host prefixes ordinary calls`() {
        val scope = HostScope()
        scope.select(7)
        assertEquals("/hosts/7/overview", scope.scoped("/overview"))
        assertEquals("/hosts/7/services", scope.scoped("/services"))
    }

    @Test
    fun `the hub's synthetic localhost row uses the local sentinel`() {
        val scope = HostScope()
        scope.select(HubHost.LOCAL_HOST_ID)
        assertEquals("/hosts/local/overview", scope.scoped("/overview"))
    }

    @Test
    fun `auth is never scoped`() {
        val scope = HostScope()
        scope.select(7)
        // Authentication always targets the hub's own session.
        assertEquals("/auth/me", scope.scoped("/auth/me"))
        assertEquals("/auth/login", scope.scoped("/auth/login"))
    }

    @Test
    fun `hub endpoints are never scoped`() {
        val scope = HostScope()
        scope.select(7)
        // These routes exist only on the hub. Scoped, they would be proxied
        // to a host that has no such route — the bug this test locks down.
        assertEquals("/hub/version", scope.scoped("/hub/version"))
        assertEquals("/hub/vulndb", scope.scoped("/hub/vulndb"))
        assertEquals("/hub/hosts", scope.scoped("/hub/hosts"))
    }

    @Test
    fun `deselecting returns to hub scope`() {
        val scope = HostScope()
        scope.select(7)
        scope.select(null)
        assertEquals("/overview", scope.scoped("/overview"))
    }
}
