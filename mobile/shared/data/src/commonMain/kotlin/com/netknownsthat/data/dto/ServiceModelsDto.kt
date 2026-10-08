package com.netknownsthat.data.dto

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
data class ServicesResponseDto(
    val services: List<ServiceUnitDto> = emptyList(),
    @SerialName("allow_mutations") val allowMutations: Boolean = false,
)

@Serializable
data class ServiceUnitDto(
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
data class ContainersResponseDto(
    val containers: List<ContainerDto> = emptyList(),
    val networks: List<DockerNetworkDto> = emptyList(),
)

@Serializable
data class ContainerDto(
    val id: String = "",
    val name: String = "",
    val image: String = "",
    val state: String = "",
    val status: String = "",
    val project: String = "",
    @SerialName("service_name") val serviceName: String = "",
    val ports: List<ContainerPortDto> = emptyList(),
    val networks: List<ContainerNetworkDto> = emptyList(),
    /** Declared in a compose file, as opposed to started by hand. */
    val declared: Boolean = false,
    val running: Boolean = false,
)

@Serializable
data class ContainerPortDto(
    @SerialName("host_ip") val hostIp: String = "",
    @SerialName("host_port") val hostPort: Int = 0,
    @SerialName("container_port") val containerPort: Int = 0,
    val protocol: String = "",
)

@Serializable
data class ContainerNetworkDto(
    val name: String = "",
    @SerialName("ip_address") val ipAddress: String = "",
    val gateway: String = "",
)

@Serializable
data class DockerNetworkDto(
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
data class PodmanResponseDto(
    val containers: List<PodmanContainerDto> = emptyList(),
)

@Serializable
data class PodmanContainerDto(
    val id: String = "",
    val name: String = "",
    val image: String = "",
    val state: String = "",
    val status: String = "",
)

@Serializable
data class LXDResponseDto(
    val instances: List<LXDInstanceDto> = emptyList(),
)

@Serializable
data class LXDInstanceDto(
    val name: String = "",
    val type: String = "",
    val status: String = "",
    val architecture: String = "",
    val ipv4: List<String> = emptyList(),
)

@Serializable
data class VMsResponseDto(
    val vms: List<VirtualMachineDto> = emptyList(),
)

@Serializable
data class VirtualMachineDto(
    val name: String = "",
    val uuid: String = "",
    val state: String = "",
    val persistent: Boolean = false,
    val autostart: Boolean = false,
    val vcpus: Int = 0,
    @SerialName("memory_kb") val memoryKb: Long = 0,
    val graphics: List<String> = emptyList(),
)

/** GET /api/misc — listeners no parsed config accounts for. */
@Serializable
data class MiscResponseDto(
    val listeners: List<ListenerDto> = emptyList(),
)

@Serializable
data class ListenerDto(
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
data class UsersResponseDto(
    val users: List<UserDto> = emptyList(),
)

@Serializable
data class UserDto(
    val id: Long = 0,
    val username: String = "",
    val role: String = "",
    val disabled: Boolean = false,
    @SerialName("created_at") val createdAt: String = "",
    @SerialName("last_login_at") val lastLoginAt: String = "",
)

/** GET /api/images — Docker images on the host. */
@Serializable
data class ImagesResponseDto(
    val images: List<DockerImageDto> = emptyList(),
    @SerialName("backup_dir") val backupDir: String = "",
)

@Serializable
data class DockerImageDto(
    val id: String = "",
    /** Empty for a dangling image — one whose tag moved to a newer build. */
    val tags: List<String> = emptyList(),
    val size: Long = 0,
    val created: String = "",
    /** A container is running from it, so Docker refuses to remove it
     * without force. */
    @SerialName("in_use") val inUse: Boolean = false,
    @SerialName("used_by") val usedBy: List<String> = emptyList(),
    val dangling: Boolean = false,
)

/** Per-reference outcome of a batch image action. */
@Serializable
data class ImageOutcomeDto(
    val ref: String = "",
    val ok: Boolean = false,
    val error: String = "",
    val path: String = "",
)

@Serializable
data class ImageActionResponseDto(
    val results: List<ImageOutcomeDto> = emptyList(),
    val reclaimed: Long = 0,
)
