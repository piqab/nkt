package com.netknownsthat.app.status

/**
 * Whether a thing is working, not working, or somewhere in between.
 *
 * Deliberately free of any Compose or Android import: the mapping below is
 * the part that is easy to get wrong (systemd, Docker, libvirt and LXD all
 * spell their states differently) and keeping it pure means it can be tested
 * on the JVM — see HealthStatusTest.
 */
enum class HealthStatus {
    /** Working as intended. */
    OK,

    /** In transition, or working but with something to attend to. */
    WARN,

    /** Not working, and that is a problem. */
    BAD,

    /** Deliberately off, or simply not known. Not an error. */
    UNKNOWN,
}

/** systemd's ActiveState. */
fun serviceHealth(activeState: String): HealthStatus = when (activeState.lowercase()) {
    "active", "running" -> HealthStatus.OK
    "failed", "error" -> HealthStatus.BAD
    // Mid-transition: reported by systemd while a unit is coming up or going
    // down, which is exactly what an operator sees right after pressing a
    // button.
    "activating", "deactivating", "reloading" -> HealthStatus.WARN
    else -> HealthStatus.UNKNOWN
}

/** Docker/Podman container state. */
fun containerHealth(state: String): HealthStatus = when (state.lowercase()) {
    "running" -> HealthStatus.OK
    // "exited" covers both a clean stop and a crash; the app cannot tell
    // them apart from the state alone, and a container that should be up but
    // is not is the case worth flagging.
    "dead", "exited" -> HealthStatus.BAD
    "restarting", "paused", "created", "removing" -> HealthStatus.WARN
    else -> HealthStatus.UNKNOWN
}

/** LXD instance / libvirt domain state. */
fun instanceHealth(state: String): HealthStatus = when (state.lowercase()) {
    "running" -> HealthStatus.OK
    "stopped", "shut off", "shutoff" -> HealthStatus.UNKNOWN
    "crashed", "error" -> HealthStatus.BAD
    "paused", "frozen", "starting", "stopping", "pmsuspended" -> HealthStatus.WARN
    else -> HealthStatus.UNKNOWN
}

/**
 * A monitored target. [lastOk] is null until the target has ever been
 * checked, which is not the same thing as being down.
 */
fun targetHealth(lastOk: Boolean?, enabled: Boolean = true): HealthStatus = when {
    !enabled -> HealthStatus.UNKNOWN
    lastOk == null -> HealthStatus.UNKNOWN
    lastOk -> HealthStatus.OK
    else -> HealthStatus.BAD
}

/**
 * A certificate, by how long it has left. [automaticRenewal] false is worth a
 * warning even on a certificate with months remaining: nothing will renew it,
 * so it will eventually take a site down without anyone touching anything.
 */
fun certificateHealth(daysLeft: Int, automaticRenewal: Boolean, unreadable: Boolean = false):
    HealthStatus = when {
    unreadable -> HealthStatus.UNKNOWN
    daysLeft < 0 -> HealthStatus.BAD
    daysLeft <= 30 -> HealthStatus.WARN
    !automaticRenewal -> HealthStatus.WARN
    else -> HealthStatus.OK
}

/**
 * A network interface. Administratively up but with no carrier is the case
 * that deserves attention: `ip link` calls it "UP" and it is not passing a
 * single packet.
 */
fun interfaceHealth(up: Boolean, lowerUp: Boolean, loopback: Boolean): HealthStatus = when {
    loopback -> HealthStatus.UNKNOWN
    up && lowerUp -> HealthStatus.OK
    up -> HealthStatus.BAD
    else -> HealthStatus.UNKNOWN
}

/** A firewall manager (ufw, firewalld, nftables...). */
fun firewallManagerHealth(installed: Boolean, active: Boolean): HealthStatus = when {
    !installed -> HealthStatus.UNKNOWN
    active -> HealthStatus.OK
    // Installed but switched off is a real finding: the operator believes
    // there is a firewall, and there is not.
    else -> HealthStatus.WARN
}

/** A web-interface account. Disabled is intentional, not broken. */
fun userHealth(disabled: Boolean): HealthStatus =
    if (disabled) HealthStatus.UNKNOWN else HealthStatus.OK
