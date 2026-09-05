package com.netknownsthat.app.status

import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * The status mapping decides what colour every list in the app shows, and the
 * four runtimes it covers all spell their states differently — so the states
 * asserted here are the literal strings the server returns (see the captured
 * fixtures in test/resources/api).
 */
class HealthStatusTest {

    @Test
    fun `systemd states map to health`() {
        assertEquals(HealthStatus.OK, serviceHealth("active"))
        assertEquals(HealthStatus.BAD, serviceHealth("failed"))
        assertEquals(HealthStatus.UNKNOWN, serviceHealth("inactive"))
        // Transitional states are what an operator sees for the first seconds
        // after pressing start — neither working nor broken.
        assertEquals(HealthStatus.WARN, serviceHealth("activating"))
        assertEquals(HealthStatus.WARN, serviceHealth("deactivating"))
    }

    @Test
    fun `service state comparison ignores case`() {
        assertEquals(HealthStatus.OK, serviceHealth("ACTIVE"))
        assertEquals(HealthStatus.BAD, serviceHealth("Failed"))
    }

    @Test
    fun `container states map to health`() {
        assertEquals(HealthStatus.OK, containerHealth("running"))
        assertEquals(HealthStatus.BAD, containerHealth("exited"))
        assertEquals(HealthStatus.BAD, containerHealth("dead"))
        assertEquals(HealthStatus.WARN, containerHealth("restarting"))
        assertEquals(HealthStatus.WARN, containerHealth("paused"))
    }

    @Test
    fun `lxd and libvirt states map to health`() {
        assertEquals(HealthStatus.OK, instanceHealth("RUNNING"))
        // libvirt spells a stopped domain "shut off", with a space.
        assertEquals(HealthStatus.UNKNOWN, instanceHealth("shut off"))
        assertEquals(HealthStatus.UNKNOWN, instanceHealth("STOPPED"))
        assertEquals(HealthStatus.WARN, instanceHealth("paused"))
        assertEquals(HealthStatus.BAD, instanceHealth("crashed"))
    }

    @Test
    fun `a target that was never checked is not reported as down`() {
        assertEquals(HealthStatus.UNKNOWN, targetHealth(lastOk = null))
        assertEquals(HealthStatus.OK, targetHealth(lastOk = true))
        assertEquals(HealthStatus.BAD, targetHealth(lastOk = false))
        // A disabled target is not being checked at all, so its last result
        // says nothing about now.
        assertEquals(HealthStatus.UNKNOWN, targetHealth(lastOk = false, enabled = false))
    }

    @Test
    fun `certificates warn on expiry and on having nothing to renew them`() {
        assertEquals(HealthStatus.OK, certificateHealth(90, automaticRenewal = true))
        assertEquals(HealthStatus.WARN, certificateHealth(10, automaticRenewal = true))
        assertEquals(HealthStatus.BAD, certificateHealth(-1, automaticRenewal = true))
        // Plenty of time left, but nothing will renew it: this is the one
        // that quietly takes a site down months from now.
        assertEquals(HealthStatus.WARN, certificateHealth(200, automaticRenewal = false))
        assertEquals(
            HealthStatus.UNKNOWN,
            certificateHealth(0, automaticRenewal = false, unreadable = true),
        )
    }

    @Test
    fun `an interface that is up without carrier counts as broken`() {
        assertEquals(HealthStatus.OK, interfaceHealth(up = true, lowerUp = true, loopback = false))
        // The case a plain "up" flag cannot distinguish: enabled, but the
        // cable fell out.
        assertEquals(HealthStatus.BAD, interfaceHealth(up = true, lowerUp = false, loopback = false))
        assertEquals(
            HealthStatus.UNKNOWN,
            interfaceHealth(up = false, lowerUp = false, loopback = false),
        )
        assertEquals(HealthStatus.UNKNOWN, interfaceHealth(up = true, lowerUp = true, loopback = true))
    }

    @Test
    fun `an installed but inactive firewall is a warning, not a blank`() {
        assertEquals(HealthStatus.OK, firewallManagerHealth(installed = true, active = true))
        assertEquals(HealthStatus.WARN, firewallManagerHealth(installed = true, active = false))
        assertEquals(HealthStatus.UNKNOWN, firewallManagerHealth(installed = false, active = false))
    }

    @Test
    fun `a disabled account is not an error`() {
        assertEquals(HealthStatus.OK, userHealth(disabled = false))
        assertEquals(HealthStatus.UNKNOWN, userHealth(disabled = true))
    }
}
