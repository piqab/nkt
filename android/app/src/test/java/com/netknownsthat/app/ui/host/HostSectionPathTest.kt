package com.netknownsthat.app.ui.host

import org.junit.Assert.assertEquals
import org.junit.Test

/** Links from hub alerts and monitoring insights land on the right section. */
class HostSectionPathTest {
    @Test
    fun paths() {
        assertEquals(HostSection.FINDINGS, HostSection.fromPath("/findings?focus=a,b"))
        assertEquals(HostSection.FAIL2BAN, HostSection.fromPath("/fail2ban?focus=1.2.3.4"))
        assertEquals(HostSection.AVAILABILITY, HostSection.fromPath("/availability?focus=caddy+%C2%B7+x"))
        assertEquals(HostSection.JOBS, HostSection.fromPath("/jobs"))
        assertEquals(HostSection.CONTAINERS, HostSection.fromPath("/docker"))
        assertEquals(HostSection.OVERVIEW, HostSection.fromPath("/"))
        assertEquals(HostSection.OVERVIEW, HostSection.fromPath("/something-new"))
    }
}
