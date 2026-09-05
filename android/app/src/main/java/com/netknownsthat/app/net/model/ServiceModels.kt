package com.netknownsthat.app.net.model

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/**
 * Services, the four container runtimes, stray listeners and users — the
 * phase-3 (action) screens.
 *
 * Shapes taken from real responses captured off `nkt serve` in fixtures
 * mode; those exact payloads live in src/test/resources/api and are decoded
 * by HostModelsTest, so a rename on the Go side fails a test rather than
 * quietly blanking a screen.
 */

@Serializable
data class ServicesResponse(
    val services: List<ServiceUnit> = emptyList(),
    @SerialName("allow_mutations") val allowMutations: Boolean = false,
)

@Serializable
data class ServiceUnit(
    val name: String = "",
    val unit: String = "",
    val description: String = "",
    @SerialName("active_state") val activeState: String = "",
    @SerialName("sub_state") val subState: String = "",
    val enabled: String = "",
    @SerialName("main_pid") val mainPid: Int = 0,
    @SerialName("memory_bytes") val memoryBytes: Long = 0,
    val since: String = "",
    val installed: Boolean = false,
    @SerialName("config_files") val configFiles: List<String> = emptyList(),
    /** Which actions this unit actually accepts — the buttons are built from
     * this rather than assumed, since not every unit supports reload. */
    val actions: List<String> = emptyList(),
)

@Serializable
data class ContainersResponse(
    val containers: List<Container> = emptyList(),
    val networks: List<DockerNetwork> = emptyList(),
)

@Serializable
data class Container(
    val id: String = "",
    val name: String = "",
    val image: String = "",
    val state: String = "",
    val status: String = "",
    val project: String = "",
    @SerialName("service_name") val serviceName: String = "",
    val ports: List<ContainerPort> = emptyList(),
    val networks: List<ContainerNetwork> = emptyList(),
    /** Declared in a compose file, as opposed to started by hand. */
    val declared: Boolean = false,
    val running: Boolean = false,
)

@Serializable
data class ContainerPort(
    @SerialName("host_ip") val hostIp: String = "",
    @SerialName("host_port") val hostPort: Int = 0,
    @SerialName("container_port") val containerPort: Int = 0,
    val protocol: String = "",
)

@Serializable
data class ContainerNetwork(
    val name: String = "",
    @SerialName("ip_address") val ipAddress: String = "",
    val gateway: String = "",
)

@Serializable
data class DockerNetwork(
    val id: String = "",
    val name: String = "",
    val driver: String = "",
    val scope: String = "",
    val internal: Boolean = false,
    val subnets: List<String> = emptyList(),
    val gateway: String = "",
    val bridge: String = "",
)

@Serializable
data class PodmanResponse(
    val containers: List<PodmanContainer> = emptyList(),
)

@Serializable
data class PodmanContainer(
    val id: String = "",
    val name: String = "",
    val image: String = "",
    val state: String = "",
    val status: String = "",
)

@Serializable
data class LXDResponse(
    val instances: List<LXDInstance> = emptyList(),
)

@Serializable
data class LXDInstance(
    val name: String = "",
    val type: String = "",
    val status: String = "",
    val architecture: String = "",
    val ipv4: List<String> = emptyList(),
)

@Serializable
data class VMsResponse(
    val vms: List<VirtualMachine> = emptyList(),
)

@Serializable
data class VirtualMachine(
    val name: String = "",
    val uuid: String = "",
    val state: String = "",
    val persistent: Boolean = false,
    val autostart: Boolean = false,
    val vcpus: Int = 0,
    @SerialName("memory_kb") val memoryKb: Long = 0,
)

/** GET /api/misc — listeners no parsed config accounts for. */
@Serializable
data class MiscResponse(
    val listeners: List<Listener> = emptyList(),
)

@Serializable
data class Listener(
    val protocol: String = "",
    val address: String = "",
    val port: Int = 0,
    val process: String = "",
    val pid: Int = 0,
    val command: String = "",
    val user: String = "",
    val unit: String = "",
    val origin: String = "",
)

@Serializable
data class UsersResponse(
    val users: List<User> = emptyList(),
)

@Serializable
data class User(
    val id: Long = 0,
    val username: String = "",
    val role: String = "",
    val disabled: Boolean = false,
    @SerialName("created_at") val createdAt: String = "",
    @SerialName("last_login_at") val lastLoginAt: String = "",
)
