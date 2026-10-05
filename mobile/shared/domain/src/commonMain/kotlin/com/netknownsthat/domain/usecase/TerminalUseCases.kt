package com.netknownsthat.domain.usecase

import com.netknownsthat.domain.common.HostTarget
import com.netknownsthat.domain.common.getOrNull
import com.netknownsthat.domain.repository.TerminalChannel
import com.netknownsthat.domain.repository.TerminalMode
import com.netknownsthat.domain.repository.TerminalRepository

/** An opened terminal and how it ended up being opened. */
data class OpenedTerminal(
    val channel: TerminalChannel,
    val mode: TerminalMode,
    /** tmux was wanted but is not on the host — a plain shell instead. */
    val tmuxMissing: Boolean,
)

/**
 * Opens a shell on [host]: in tmux when the host has it (the session then
 * survives a dropped connection and is re-attached), a plain shell
 * otherwise — asking for tmux on a host without it fails outright, which is
 * what made the terminal "sometimes not work".
 */
class OpenTerminalUseCase(private val terminal: TerminalRepository) {
    suspend operator fun invoke(host: HostTarget, btop: Boolean, preferTmux: Boolean = true): OpenedTerminal {
        if (btop) return OpenedTerminal(terminal.open(host, TerminalMode.BTOP), TerminalMode.BTOP, false)
        val tmux = preferTmux && terminal.tmuxStatus(host).getOrNull()?.available == true
        val mode = if (tmux) TerminalMode.TMUX else TerminalMode.SHELL
        return OpenedTerminal(terminal.open(host, mode), mode, tmuxMissing = preferTmux && !tmux)
    }
}

/** The host's sections, by the web UI path an alert or insight links to. */
enum class HostSectionKey {
    OVERVIEW, FINDINGS, TERMINAL, BTOP, LOGS, SERVICES, CONTAINERS, VULNERABILITIES, MALWARE,
    AVAILABILITY, USAGE, CONFIGS, FIREWALL, CERTIFICATES, INTERFACES, MISC, TOPOLOGY, USERS,
    JOBS, FAIL2BAN, AUDIT,
    ;

    companion object {
        /** "/findings?focus=…" → FINDINGS; unknown paths land on the overview. */
        fun fromPath(path: String): HostSectionKey {
            val first = path.trimStart('/').substringBefore('?').substringBefore('/')
            return when (first) {
                "findings" -> FINDINGS
                "services" -> SERVICES
                "containers", "docker", "podman", "lxd", "vms" -> CONTAINERS
                "vulnerabilities", "packages" -> if ("tab=malware" in path) MALWARE else VULNERABILITIES
                "malware", "clamav" -> MALWARE
                "availability" -> AVAILABILITY
                "usage" -> USAGE
                "configs" -> CONFIGS
                "firewall" -> FIREWALL
                "certificates" -> CERTIFICATES
                "interfaces" -> INTERFACES
                "topology" -> TOPOLOGY
                "users" -> USERS
                "jobs" -> JOBS
                "fail2ban" -> FAIL2BAN
                "audit" -> AUDIT
                "logs" -> LOGS
                else -> OVERVIEW
            }
        }
    }
}
