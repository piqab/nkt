package com.netknownsthat.app.ui.host

import com.netknownsthat.app.net.model.RuleSpec
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The firewall form's one genuinely dangerous outcome is locking the operator
 * out of the host they are configuring — from a phone, with no console to
 * fall back on. This is the check that puts a warning in front of that.
 */
class FirewallLockoutTest {

    @Test
    fun `blocking ssh is flagged`() {
        assertTrue(ruleRisksLockout(RuleSpec(action = "deny", port = 22)))
        assertTrue(ruleRisksLockout(RuleSpec(action = "reject", port = 22)))
    }

    @Test
    fun `the hub's own ports are flagged too`() {
        // Losing these does not lock out ssh, but it does cut off the very
        // app being used to make the change, and its tunnel to the hub.
        assertTrue(ruleRisksLockout(RuleSpec(action = "deny", port = 8077)))
        assertTrue(ruleRisksLockout(RuleSpec(action = "deny", port = 8078)))
    }

    @Test
    fun `allowing ssh is not a lockout`() {
        assertFalse(ruleRisksLockout(RuleSpec(action = "allow", port = 22)))
        assertFalse(ruleRisksLockout(RuleSpec(action = "limit", port = 22)))
    }

    @Test
    fun `an ordinary port is not flagged`() {
        assertFalse(ruleRisksLockout(RuleSpec(action = "deny", port = 8080)))
        assertFalse(ruleRisksLockout(RuleSpec(action = "deny", port = 443)))
    }
}
