package com.netknownsthat.app.net.model

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/** Configs, firewall, certificates and the topology map — phases 5-8. */

@Serializable
data class ConfigsResponse(
    val files: List<ManagedFile> = emptyList(),
)

@Serializable
data class ManagedFile(
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
data class ConfigFileResponse(
    val path: String = "",
    val content: String = "",
    val sha256: String = "",
    val editable: Boolean = false,
)

/** GET /api/configs/versions?path=… — newest first. */
@Serializable
data class ConfigVersionsResponse(
    val versions: List<ConfigVersion> = emptyList(),
)

@Serializable
data class ConfigVersion(
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
data class ConfigDiffResponse(
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
data class ConfigWriteResult(
    val path: String = "",
    @SerialName("version_id") val versionId: Long = 0,
    val validated: Boolean = false,
    val validation: CommandResult? = null,
    @SerialName("rolled_back") val rolledBack: Boolean = false,
    val message: String = "",
    val applied: Boolean = false,
    val apply: CommandResult? = null,
)

@Serializable
data class CommandResult(
    val argv: List<String> = emptyList(),
    @SerialName("exit_code") val exitCode: Int = 0,
    val stdout: String = "",
    val stderr: String = "",
    val simulated: Boolean = false,
)

@Serializable
data class FirewallResponse(
    val managers: List<FirewallManager> = emptyList(),
    val backends: List<String> = emptyList(),
    val policies: List<FirewallPolicy> = emptyList(),
    val rules: List<FirewallRule> = emptyList(),
    val listeners: List<Listener> = emptyList(),
)

@Serializable
data class FirewallManager(
    val name: String = "",
    val installed: Boolean = false,
    val active: Boolean = false,
    val policy: String = "",
)

@Serializable
data class FirewallPolicy(
    val backend: String = "",
    val table: String = "",
    val chain: String = "",
    val policy: String = "",
    val packets: Long = 0,
    val bytes: Long = 0,
)

@Serializable
data class FirewallRule(
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
data class FirewallNumberedResponse(
    /** Empty while ufw is inactive: `ufw status numbered` prints nothing but
     * "Status: inactive" then, even when rules exist. */
    val rules: List<NumberedRule> = emptyList(),
    val added: List<AddedRule> = emptyList(),
)

@Serializable
data class NumberedRule(
    val number: Int = 0,
    val text: String = "",
)

@Serializable
data class AddedRule(
    val spec: String = "",
    val action: String = "",
    val port: Int = 0,
    val protocol: String = "",
)

@Serializable
data class CertificatesResponse(
    val certificates: List<Certificate> = emptyList(),
    val summary: CertificatesSummary? = null,
)

@Serializable
data class CertificatesSummary(
    val total: Int = 0,
    val expired: Int = 0,
    val expiring: Int = 0,
    val unreadable: Int = 0,
    val unmanaged: Int = 0,
)

@Serializable
data class Certificate(
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
    val renewal: CertRenewal? = null,
    val error: String = "",
)

@Serializable
data class CertRenewal(
    val tool: String = "",
    val managed: Boolean = false,
    /** Whether renewal actually happens on its own — an unmanaged
     * certificate is the one that will silently expire. */
    val automatic: Boolean = false,
    val detail: String = "",
    val lineage: String = "",
)

@Serializable
data class TopologyResponse(
    val nodes: List<TopologyNode> = emptyList(),
    val edges: List<TopologyEdge> = emptyList(),
    val stats: Map<String, Int> = emptyMap(),
    val findings: List<TopologyFinding> = emptyList(),
)

@Serializable
data class TopologyNode(
    val id: String = "",
    val kind: String = "",
    val label: String = "",
    val status: String = "",
    val findings: Int = 0,
)

@Serializable
data class TopologyEdge(
    val id: String = "",
    val from: String = "",
    val to: String = "",
    val kind: String = "",
    val label: String = "",
    val status: String = "",
)

@Serializable
data class TopologyFinding(
    @SerialName("node_id") val nodeId: String = "",
    val title: String = "",
    val severity: String = "",
)
