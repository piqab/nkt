package com.netknownsthat.domain.usecase

import com.netknownsthat.domain.common.AppError
import com.netknownsthat.domain.common.HostTarget
import com.netknownsthat.domain.common.Outcome
import com.netknownsthat.domain.common.getOrDefault
import com.netknownsthat.domain.common.getOrNull
import com.netknownsthat.domain.model.ContainersResponse
import com.netknownsthat.domain.model.FirewallNumberedResponse
import com.netknownsthat.domain.model.FirewallResponse
import com.netknownsthat.domain.model.ImagesResponse
import com.netknownsthat.domain.model.LXDResponse
import com.netknownsthat.domain.model.OutagesResponse
import com.netknownsthat.domain.model.PodmanResponse
import com.netknownsthat.domain.model.TargetsResponse
import com.netknownsthat.domain.model.VMsResponse
import com.netknownsthat.domain.repository.ContainersRepository
import com.netknownsthat.domain.repository.FirewallRepository
import com.netknownsthat.domain.repository.HostMonitoringRepository
import com.netknownsthat.domain.repository.ServicesRepository
import kotlinx.coroutines.async
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlin.time.Duration
import kotlin.time.Duration.Companion.milliseconds
import kotlin.time.Duration.Companion.seconds
import kotlin.time.TimeSource

/** Every container engine of a host at once — tabs switch instantly. */
data class ContainerRuntimes(
    val docker: ContainersResponse,
    val podman: PodmanResponse,
    val lxd: LXDResponse,
    val vms: VMsResponse,
    val images: ImagesResponse,
    /** Live check: false hides nothing, it adds a "Docker is not installed" note. */
    val dockerInstalled: Boolean,
)

class LoadContainersUseCase(
    private val containers: ContainersRepository,
    private val services: ServicesRepository,
) {
    suspend operator fun invoke(host: HostTarget): Outcome<ContainerRuntimes> = coroutineScope {
        val docker = async { containers.docker(host) }
        val installed = async { services.installed(host, "docker") }
        // Podman/LXD/libvirt are often absent where Docker runs (and the
        // other way round): their failure means "nothing here", not an
        // error worth blanking the screen over.
        val podman = async { containers.podman(host) }
        val lxd = async { containers.lxd(host) }
        val vms = async { containers.vms(host) }
        val images = async { containers.images(host) }
        val dockerInstalled = installed.await().getOrDefault(true)
        val dockerResult = docker.await()
        val dockerData = when {
            dockerResult is Outcome.Success -> dockerResult.value
            !dockerInstalled -> ContainersResponse()
            else -> return@coroutineScope dockerResult as Outcome.Failure
        }
        Outcome.Success(
            ContainerRuntimes(
                docker = dockerData,
                podman = podman.await().getOrDefault(PodmanResponse()),
                lxd = lxd.await().getOrDefault(LXDResponse()),
                vms = vms.await().getOrDefault(VMsResponse()),
                images = images.await().getOrDefault(ImagesResponse()),
                dockerInstalled = dockerInstalled,
            ),
        )
    }
}

data class AvailabilityData(val targets: TargetsResponse, val outages: OutagesResponse)

class LoadAvailabilityUseCase(private val monitoring: HostMonitoringRepository) {
    suspend operator fun invoke(host: HostTarget): Outcome<AvailabilityData> = coroutineScope {
        val outages = async { monitoring.outages(host) }
        when (val targets = monitoring.targets(host)) {
            is Outcome.Failure -> targets
            is Outcome.Success -> Outcome.Success(
                AvailabilityData(targets.value, outages.await().getOrDefault(OutagesResponse())),
            )
        }
    }
}

data class FirewallData(val state: FirewallResponse, val numbered: FirewallNumberedResponse)

class LoadFirewallUseCase(private val firewall: FirewallRepository) {
    suspend operator fun invoke(host: HostTarget): Outcome<FirewallData> = coroutineScope {
        val numbered = async { firewall.numbered(host) }
        when (val state = firewall.state(host)) {
            is Outcome.Failure -> state
            is Outcome.Success -> Outcome.Success(FirewallData(state.value, numbered.await().getOrDefault(FirewallNumberedResponse())))
        }
    }
}

/** How an action on a service/container ended. */
sealed interface ActionResult {
    /** The host reached the asked-for state. */
    data object Settled : ActionResult

    /** Accepted, but the state did not change within the wait. */
    data class NotSettled(val waited: Duration) : ActionResult
    data class Failed(val error: AppError) : ActionResult
}

/**
 * Runs an action and waits until the host shows its result: systemd, Docker
 * and libvirt answer at once and do the work afterwards, so a single
 * refetch would usually still show the old state. Gives up after [timeout]
 * rather than spinning forever.
 */
class AwaitSettledUseCase(
    private val timeout: Duration = 20.seconds,
    private val poll: Duration = 800.milliseconds,
) {
    suspend operator fun <T> invoke(
        action: suspend () -> Outcome<Unit>,
        fetch: suspend () -> Outcome<T>,
        onFetched: (T) -> Unit,
        settled: (T) -> Boolean,
    ): ActionResult {
        when (val r = action()) {
            is Outcome.Failure -> return ActionResult.Failed(r.error)
            is Outcome.Success -> Unit
        }
        val started = TimeSource.Monotonic.markNow()
        while (started.elapsedNow() < timeout) {
            delay(poll)
            val data = fetch().getOrNull() ?: continue
            onFetched(data)
            if (settled(data)) return ActionResult.Settled
        }
        return ActionResult.NotSettled(timeout)
    }
}
