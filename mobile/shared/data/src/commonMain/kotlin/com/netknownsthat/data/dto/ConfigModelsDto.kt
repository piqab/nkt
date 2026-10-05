package com.netknownsthat.data.dto

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/** Configs, firewall, certificates and the topology map — phases 5-8. */

@Serializable
data class ConfigsResponseDto(
    val files: List<ManagedFileDto> = emptyList(),
)

@Serializable
data class ManagedFileDto(
    val path: String = "",
    val service: String = "",
    val size: Long = 0,
    @SerialName("mod_time") val modTime: String = "",
    val sha256: String = "",
    val editable: Boolean = false,
    val readable: Boolean = false,
)

/** GET /api/configs/file?path=… */
@Serializable
data class ConfigFileResponseDto(
    val path: String = "",
    val content: String = "",
    val sha256: String = "",
    val editable: Boolean = false,
)

/** GET /api/configs/versions?path=… — newest first. */
@Serializable
data class ConfigVersionsResponseDto(
    val versions: List<ConfigVersionDto> = emptyList(),
)

@Serializable
data class ConfigVersionDto(
    val id: Long = 0,
    val path: String = "",
    val service: String = "",
    val ts: String = "",
    val author: String = "",
    /** "edit", "rollback", or "observed" — the state recorded before the
     * first edit, which has no author behind it. */
    val action: String = "",
    val note: String = "",
    val size: Long = 0,
    val sha256: String = "",
)

/**
 * GET /api/configs/versions/{id}/diff.
 *
 * A unified diff of that version against the file as it is **now**, not
 * against the version before it (see ConfigManager.Diff) — so it reads as
 * "what rolling back to this version would change", and is empty for the
 * version that is already current.
 */
@Serializable
data class ConfigDiffResponseDto(
    val diff: String = "",
)

/**
 * Result of PUT /api/configs/file and of a rollback.
 *
 * [rolledBack] is the safety net that makes editing from a phone reasonable:
 * the host validates the new content with the owning service and restores the
 * previous file if it does not pass, so a bad edit cannot leave a service
 * with a config it refuses to start on.
 */
@Serializable
data class ConfigWriteResultDto(
    val path: String = "",
    @SerialName("version_id") val versionId: Long = 0,
    val validated: Boolean = false,
    val validation: CommandResultDto? = null,
    @SerialName("rolled_back") val rolledBack: Boolean = false,
    val message: String = "",
    val applied: Boolean = false,
    val apply: CommandResultDto? = null,
)

@Serializable
data class CommandResultDto(
    val argv: List<String> = emptyList(),
    @SerialName("exit_code") val exitCode: Int = 0,
    val stdout: String = "",
    val stderr: String = "",
    val simulated: Boolean = false,
)

@Serializable
data class FirewallResponseDto(
    val managers: List<FirewallManagerDto> = emptyList(),
    val backends: List<String> = emptyList(),
    val policies: List<FirewallPolicyDto> = emptyList(),
    val rules: List<FirewallRuleDto> = emptyList(),
    val listeners: List<ListenerDto> = emptyList(),
)

@Serializable
data class FirewallManagerDto(
    val name: String = "",
    val installed: Boolean = false,
    val active: Boolean = false,
    val policy: String = "",
)

@Serializable
data class FirewallPolicyDto(
    val backend: String = "",
    val table: String = "",
    val chain: String = "",
    val policy: String = "",
    val packets: Long = 0,
    val bytes: Long = 0,
)

@Serializable
data class FirewallRuleDto(
    val id: String = "",
    val backend: String = "",
    val table: String = "",
    val chain: String = "",
    val order: Int = 0,
    val action: String = "",
    @SerialName("in_iface") val inIface: String = "",
    val packets: Long = 0,
    val bytes: Long = 0,
    val raw: String = "",
    @SerialName("managed_by") val managedBy: String = "",
)

/** GET /api/firewall/rules — ufw's own two views of its rules. */
@Serializable
data class FirewallNumberedResponseDto(
    /** Empty while ufw is inactive: `ufw status numbered` prints nothing but
     * "Status: inactive" then, even when rules exist. */
    val rules: List<NumberedRuleDto> = emptyList(),
    val added: List<AddedRuleDto> = emptyList(),
)

@Serializable
data class NumberedRuleDto(
    val number: Int = 0,
    val text: String = "",
)

@Serializable
data class AddedRuleDto(
    val spec: String = "",
    val action: String = "",
    val port: Int = 0,
    val protocol: String = "",
)

/**
 * A ufw rule to add or remove (control.RuleSpecDto). [from] empty means
 * "anywhere".
 */
@Serializable
data class RuleSpecDto(
    val action: String = "allow", // allow | deny | reject | limit
    val port: Int = 0,
    val protocol: String = "tcp", // tcp | udp
    val from: String = "",
    val comment: String = "",
)

/** A firewalld port or service, in one zone (control.FirewalldPortSpecDto). */
@Serializable
data class FirewalldPortSpecDto(
    val zone: String = "",
    val port: Int = 0,
    val protocol: String = "tcp",
    val service: String = "",
    /** Permanent survives a reload; runtime takes effect now. The web UI
     * sets both for an ordinary change, and so does this app. */
    val permanent: Boolean = true,
    val runtime: Boolean = true,
)

/** What every firewall mutation answers with. */
@Serializable
data class CommandStatusDto(
    val status: String = "",
    val output: String = "",
    val simulated: Boolean = false,
)

/** A certbot lineage under /etc/letsencrypt/live. */
@Serializable
data class LineageInfoDto(
    val name: String = "",
    /** Readable form when certbot named the lineage in punycode. */
    @SerialName("name_unicode") val nameUnicode: String = "",
    /** False when fullchain.pem could not be read — still a valid target,
     * just with no expiry to show. */
    val known: Boolean = false,
    @SerialName("not_after") val notAfter: String = "",
    @SerialName("days_left") val daysLeft: Int = 0,
)

@Serializable
data class LineagesResponseDto(val lineages: List<LineageInfoDto> = emptyList())

@Serializable
data class HAProxyPathsResponseDto(val paths: List<String> = emptyList())

/** Certbot issue/renew return a job id; progress arrives by polling. */
@Serializable
data class JobStartedDto(val job: String = "")

@Serializable
data class RenewJobStatusDto(
    val events: List<RenewEventDto> = emptyList(),
    val done: Boolean = false,
    val error: String = "",
)

@Serializable
data class RenewEventDto(
    val time: String = "",
    val text: String = "",
)

/**
 * A generated self-signed certificate. [snippet] is configuration to paste
 * through the config editor — the host deliberately does not edit nginx or
 * haproxy itself here.
 */
/**
 * POST /api/certificates/self-signed answers with one result per name.
 * A host still running an older nkt answers with a bare result object
 * instead, so [results] is optional and the flat fields are read as a
 * fallback.
 */
@Serializable
data class SelfSignedResponseDto(
    val results: List<SelfSignedResultDto> = emptyList(),
    val names: List<String> = emptyList(),
    @SerialName("cert_path") val certPath: String = "",
    val snippet: String = "",
    @SerialName("not_after") val notAfter: String = "",
) {
    fun asList(): List<SelfSignedResultDto> = when {
        results.isNotEmpty() -> results
        names.isNotEmpty() -> listOf(
            SelfSignedResultDto(names = names, certPath = certPath, snippet = snippet, notAfter = notAfter)
        )
        else -> emptyList()
    }
}

@Serializable
data class SelfSignedResultDto(
    val names: List<String> = emptyList(),
    @SerialName("cert_path") val certPath: String = "",
    @SerialName("key_path") val keyPath: String = "",
    @SerialName("combined_path") val combinedPath: String = "",
    val fingerprint: String = "",
    @SerialName("not_after") val notAfter: String = "",
    val snippet: String = "",
)

@Serializable
data class CertificatesResponseDto(
    val certificates: List<CertificateDto> = emptyList(),
    val summary: CertificatesSummaryDto? = null,
)

@Serializable
data class CertificatesSummaryDto(
    val total: Int = 0,
    val expired: Int = 0,
    val expiring: Int = 0,
    val unreadable: Int = 0,
    val unmanaged: Int = 0,
)

@Serializable
data class CertificateDto(
    val id: String = "",
    val path: String = "",
    val service: String = "",
    val names: List<String> = emptyList(),
    val subject: String = "",
    val issuer: String = "",
    @SerialName("not_before") val notBefore: String = "",
    @SerialName("not_after") val notAfter: String = "",
    @SerialName("days_left") val daysLeft: Int = 0,
    @SerialName("key_algorithm") val keyAlgorithm: String = "",
    @SerialName("key_bits") val keyBits: Int = 0,
    @SerialName("self_signed") val selfSigned: Boolean = false,
    val fingerprint: String = "",
    val renewal: CertRenewalDto? = null,
    val error: String = "",
)

@Serializable
data class CertRenewalDto(
    val tool: String = "",
    val managed: Boolean = false,
    /** Whether renewal actually happens on its own — an unmanaged
     * certificate is the one that will silently expire. */
    val automatic: Boolean = false,
    val detail: String = "",
    val lineage: String = "",
)

@Serializable
data class TopologyResponseDto(
    val nodes: List<TopologyNodeDto> = emptyList(),
    val edges: List<TopologyEdgeDto> = emptyList(),
    val stats: Map<String, Int> = emptyMap(),
    val findings: List<TopologyFindingDto> = emptyList(),
)

@Serializable
data class TopologyNodeDto(
    val id: String = "",
    val kind: String = "",
    val label: String = "",
    val status: String = "",
    val findings: Int = 0,
    /** Service a listener belongs to — what folding groups by. */
    val group: String = "",
    val meta: Map<String, String> = emptyMap(),
)

@Serializable
data class TopologyEdgeDto(
    val id: String = "",
    val from: String = "",
    val to: String = "",
    val kind: String = "",
    val label: String = "",
    val status: String = "",
)

@Serializable
data class TopologyFindingDto(
    @SerialName("node_id") val nodeId: String = "",
    val title: String = "",
    val severity: String = "",
)

/** GET /api/logs/sources */
@Serializable
data class LogSourcesResponseDto(
    val sources: List<LogSourceDto> = emptyList(),
    /** The only directory plain files may come from — shown in the hint for
     * the custom-path field so it is not a guess. */
    val root: String = "/var/log",
)

@Serializable
data class LogSourceDto(
    /** "unit" (systemd journal) or "file". */
    val kind: String = "",
    val name: String = "",
    val size: Long = 0,
    val service: String = "",
    /** A rotated generation: never grows again, so it is read once rather
     * than followed — and often where the content actually is, since the
     * active file can sit empty for days after a rotation. */
    val archived: Boolean = false,
    /** Has to be decoded on the host before it is text at all. */
    val compressed: Boolean = false,
)

/** GET /api/logs/tail — a one-shot read, used for archives. */
@Serializable
data class LogTailResponseDto(val output: String = "")
