package com.netknownsthat.domain.model


/**
 * Services, the four container runtimes, stray listeners and users — the
 * phase-3 (action) screens.
 *
 * Shapes taken from real responses captured off `nkt serve` in fixtures
 * mode; those exact payloads live in src/test/resources/api and are decoded
 * by HostModelsTest, so a rename on the Go side fails a test rather than
 * quietly blanking a screen.
 */

data class ServicesResponse(
    val services: List<ServiceUnit> = emptyList(),
    val allowMutations: Boolean = false,
)

data class ServiceUnit(
    val name: String = "",
    val unit: String = "",
    val description: String = "",
    val activeState: String = "",
    val subState: String = "",
    val enabled: String = "",
    val mainPid: Int = 0,
    val memoryBytes: Long = 0,
    val since: String = "",
    val installed: Boolean = false,
    val configFiles: List<String> = emptyList(),
    /** Which actions this unit actually accepts — the buttons are built from
     * this rather than assumed, since not every unit supports reload. */
    val actions: List<String> = emptyList(),
)

data class ContainersResponse(
    val containers: List<Container> = emptyList(),
    val networks: List<DockerNetwork> = emptyList(),
)

data class Container(
    val id: String = "",
    val name: String = "",
    val image: String = "",
    val state: String = "",
    val status: String = "",
    val project: String = "",
    val serviceName: String = "",
    val ports: List<ContainerPort> = emptyList(),
    val networks: List<ContainerNetwork> = emptyList(),
    /** Declared in a compose file, as opposed to started by hand. */
    val declared: Boolean = false,
    val running: Boolean = false,
)

data class ContainerPort(
    val hostIp: String = "",
    val hostPort: Int = 0,
    val containerPort: Int = 0,
    val protocol: String = "",
)

data class ContainerNetwork(
    val name: String = "",
    val ipAddress: String = "",
    val gateway: String = "",
)

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

data class PodmanResponse(
    val containers: List<PodmanContainer> = emptyList(),
)

data class PodmanContainer(
    val id: String = "",
    val name: String = "",
    val image: String = "",
    val state: String = "",
    val status: String = "",
)

data class LXDResponse(
    val instances: List<LXDInstance> = emptyList(),
)

data class LXDInstance(
    val name: String = "",
    val type: String = "",
    val status: String = "",
    val architecture: String = "",
    val ipv4: List<String> = emptyList(),
)

data class VMsResponse(
    val vms: List<VirtualMachine> = emptyList(),
)

data class VirtualMachine(
    val name: String = "",
    val uuid: String = "",
    val state: String = "",
    val persistent: Boolean = false,
    val autostart: Boolean = false,
    val vcpus: Int = 0,
    val memoryKb: Long = 0,
    /** The machine's screens: "vnc", "spice"; empty — unknown (an old host). */
    val graphics: List<String> = emptyList(),
)

/** GET /api/misc — listeners no parsed config accounts for. */
data class MiscResponse(
    val listeners: List<Listener> = emptyList(),
)

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

data class UsersResponse(
    val users: List<User> = emptyList(),
)

data class User(
    val id: Long = 0,
    val username: String = "",
    val role: String = "",
    val disabled: Boolean = false,
    val createdAt: String = "",
    val lastLoginAt: String = "",
)

/** GET /api/images — Docker images on the host. */
data class ImagesResponse(
    val images: List<DockerImage> = emptyList(),
    val backupDir: String = "",
)

data class DockerImage(
    val id: String = "",
    /** Empty for a dangling image — one whose tag moved to a newer build. */
    val tags: List<String> = emptyList(),
    val size: Long = 0,
    val created: String = "",
    /** A container is running from it, so Docker refuses to remove it
     * without force. */
    val inUse: Boolean = false,
    val usedBy: List<String> = emptyList(),
    val dangling: Boolean = false,
)

/** Per-reference outcome of a batch image action. */
data class ImageOutcome(
    val ref: String = "",
    val ok: Boolean = false,
    val error: String = "",
    val path: String = "",
)

data class ImageActionResponse(
    val results: List<ImageOutcome> = emptyList(),
    val reclaimed: Long = 0,
)
