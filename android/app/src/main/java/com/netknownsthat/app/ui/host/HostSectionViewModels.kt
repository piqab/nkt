package com.netknownsthat.app.ui.host

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.netknownsthat.app.net.HubClient
import com.netknownsthat.app.net.model.AuditResponse
import com.netknownsthat.app.net.model.CertificatesResponse
import com.netknownsthat.app.net.model.ConfigFileResponse
import com.netknownsthat.app.net.model.ConfigsResponse
import com.netknownsthat.app.net.model.ContainersResponse
import com.netknownsthat.app.net.model.FindingsResponse
import com.netknownsthat.app.net.model.FirewallNumberedResponse
import com.netknownsthat.app.net.model.FirewallResponse
import com.netknownsthat.app.net.model.InterfacesResponse
import com.netknownsthat.app.net.model.JobsResponse
import com.netknownsthat.app.net.model.LXDResponse
import com.netknownsthat.app.net.model.MiscResponse
import com.netknownsthat.app.net.model.OutagesResponse
import com.netknownsthat.app.net.model.Overview
import com.netknownsthat.app.net.model.PodmanResponse
import com.netknownsthat.app.net.model.ServicesResponse
import com.netknownsthat.app.net.model.TargetsResponse
import com.netknownsthat.app.net.model.TopologyResponse
import com.netknownsthat.app.net.model.UsageResponse
import com.netknownsthat.app.net.model.UsageTopResponse
import com.netknownsthat.app.net.model.UsersResponse
import com.netknownsthat.app.net.model.VMsResponse
import com.netknownsthat.app.net.model.VulnResponse
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.serialization.builtins.serializer
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject

/** What every read-only section needs and nothing more. */
data class SectionState<T>(
    val loading: Boolean = false,
    val data: T? = null,
    val error: String? = null,
)

/**
 * Shared plumbing for the phase-2 sections: they differ only in which path
 * they read and what type comes back, so the fetch-and-store dance lives
 * here once.
 *
 * Nothing loads in `init` on purpose. These ViewModels are created with the
 * activity, before any host has been picked, so an eager load would fire
 * with no host scope set (see HostScope) and read the wrong thing entirely.
 * The screens call [load] from a LaunchedEffect keyed on the selected host,
 * which also gets a fresh fetch when the operator switches hosts.
 */
abstract class SectionViewModel<T>(protected val hubClient: HubClient) : ViewModel() {
    var state by mutableStateOf(SectionState<T>())
        private set

    /** Result of the last action, for a snackbar. Null once shown. */
    var actionMessage by mutableStateOf<String?>(null)

    /** Set while an action is in flight, so buttons can be disabled — acting
     * twice on a service because the first tap looked like it did nothing is
     * worth preventing. */
    var actionInProgress by mutableStateOf(false)
        private set

    protected abstract suspend fun fetch(): HubClient.ApiResult<T>

    fun load() {
        viewModelScope.launch {
            state = state.copy(loading = true, error = null)
            state = when (val result = fetch()) {
                is HubClient.ApiResult.Success -> state.copy(loading = false, data = result.value)
                is HubClient.ApiResult.Failure -> state.copy(loading = false, error = result.message)
            }
        }
    }

    /**
     * Runs a mutating call and refetches afterwards, so the screen shows
     * what the host actually ended up in rather than what was asked for —
     * a service can fail to start and still return a successful HTTP call.
     */
    protected fun act(okMessage: String, call: suspend () -> HubClient.ApiResult<*>) {
        if (actionInProgress) return
        viewModelScope.launch {
            actionInProgress = true
            actionMessage = when (val result = call()) {
                is HubClient.ApiResult.Success -> okMessage
                is HubClient.ApiResult.Failure -> "Не удалось: ${result.message}"
            }
            actionInProgress = false
            load()
        }
    }
}

class OverviewViewModel(hubClient: HubClient) : SectionViewModel<Overview>(hubClient) {
    override suspend fun fetch() = hubClient.get<Overview>("/overview")
}

class FindingsViewModel(hubClient: HubClient) : SectionViewModel<FindingsResponse>(hubClient) {
    override suspend fun fetch() = hubClient.get<FindingsResponse>("/findings")
}

class InterfacesViewModel(hubClient: HubClient) : SectionViewModel<InterfacesResponse>(hubClient) {
    override suspend fun fetch() = hubClient.get<InterfacesResponse>("/interfaces")
}

class AuditViewModel(hubClient: HubClient) : SectionViewModel<AuditResponse>(hubClient) {
    override suspend fun fetch() = hubClient.get<AuditResponse>("/audit")
}

class ServicesViewModel(hubClient: HubClient) : SectionViewModel<ServicesResponse>(hubClient) {
    override suspend fun fetch() = hubClient.get<ServicesResponse>("/services")

    fun action(name: String, action: String) = act("$name: $action выполнено") {
        hubClient.post<JsonObject>("/services/$name/$action")
    }
}

/**
 * All four container runtimes behind one ViewModel: the screen shows them as
 * tabs, and fetching all four together keeps a tab switch instant instead of
 * spinning on every tap.
 */
class ContainersViewModel(hubClient: HubClient) : SectionViewModel<ContainerRuntimes>(hubClient) {
    override suspend fun fetch(): HubClient.ApiResult<ContainerRuntimes> {
        val docker = hubClient.get<ContainersResponse>("/containers")
        if (docker is HubClient.ApiResult.Failure) return docker
        // Podman/LXD/libvirt are frequently absent on a host that runs
        // Docker (and vice versa); a failure for those is "nothing here",
        // not an error worth blanking the whole screen over.
        val podman = hubClient.get<PodmanResponse>("/podman/containers")
        val lxd = hubClient.get<LXDResponse>("/lxd/instances")
        val vms = hubClient.get<VMsResponse>("/vms")
        return HubClient.ApiResult.Success(
            ContainerRuntimes(
                docker = (docker as HubClient.ApiResult.Success).value,
                podman = (podman as? HubClient.ApiResult.Success)?.value ?: PodmanResponse(),
                lxd = (lxd as? HubClient.ApiResult.Success)?.value ?: LXDResponse(),
                vms = (vms as? HubClient.ApiResult.Success)?.value ?: VMsResponse(),
            )
        )
    }

    fun dockerAction(name: String, action: String) = act("$name: $action выполнено") {
        hubClient.post<JsonObject>("/containers/$name/$action")
    }

    fun podmanAction(name: String, action: String) = act("$name: $action выполнено") {
        hubClient.post<JsonObject>("/podman/containers/$name/$action")
    }

    fun lxdAction(name: String, action: String) = act("$name: $action выполнено") {
        hubClient.post<JsonObject>("/lxd/instances/$name/$action")
    }

    fun vmAction(name: String, action: String) = act("$name: $action выполнено") {
        hubClient.post<JsonObject>("/vms/$name/$action")
    }
}

data class ContainerRuntimes(
    val docker: ContainersResponse,
    val podman: PodmanResponse,
    val lxd: LXDResponse,
    val vms: VMsResponse,
)

class UsersViewModel(hubClient: HubClient) : SectionViewModel<UsersResponse>(hubClient) {
    override suspend fun fetch() = hubClient.get<UsersResponse>("/users")

    fun create(username: String, password: String, role: String) =
        act("Пользователь $username создан") {
            hubClient.post<JsonObject>(
                "/users",
                """{"username":${username.jsonString()},"password":${password.jsonString()},"role":${role.jsonString()}}""",
            )
        }

    fun setDisabled(username: String, disabled: Boolean) =
        act(if (disabled) "$username отключён" else "$username включён") {
            hubClient.patch<JsonObject>("/users/$username", """{"disabled":$disabled}""")
        }

    fun delete(username: String) = act("$username удалён") {
        hubClient.delete<JsonObject>("/users/$username")
    }
}

class MiscViewModel(hubClient: HubClient) : SectionViewModel<MiscResponse>(hubClient) {
    override suspend fun fetch() = hubClient.get<MiscResponse>("/misc")
}

/**
 * Vulnerabilities: a scan is a long job, so starting one only kicks it off —
 * the result arrives by polling this same endpoint (see handleVulnerabilities,
 * whose `scanning`/`progress` fields exist for exactly that).
 */
class VulnerabilitiesViewModel(hubClient: HubClient) : SectionViewModel<VulnResponse>(hubClient) {
    override suspend fun fetch() = hubClient.get<VulnResponse>("/vulnerabilities")

    fun startScan() = act("Сканирование запущено") {
        hubClient.post<JsonObject>("/vulnerabilities/scan")
    }

    /** Polls while a scan is running, then stops. Called by the screen when
     * it sees `scanning` — a timer that runs regardless would keep the
     * radio awake for nothing. */
    fun pollWhileScanning() {
        viewModelScope.launch {
            while (state.data?.scanning == true) {
                delay(3000)
                load()
            }
        }
    }
}

class AvailabilityViewModel(hubClient: HubClient) : SectionViewModel<AvailabilityData>(hubClient) {
    override suspend fun fetch(): HubClient.ApiResult<AvailabilityData> {
        val targets = hubClient.get<TargetsResponse>("/monitor/targets")
        if (targets is HubClient.ApiResult.Failure) return targets
        val outages = hubClient.get<OutagesResponse>("/monitor/outages")
        return HubClient.ApiResult.Success(
            AvailabilityData(
                targets = (targets as HubClient.ApiResult.Success).value,
                outages = (outages as? HubClient.ApiResult.Success)?.value ?: OutagesResponse(),
            )
        )
    }

    fun check(targetId: Long) = act("Проверка выполнена") {
        hubClient.post<JsonObject>("/monitor/targets/$targetId/check")
    }
}

data class AvailabilityData(
    val targets: TargetsResponse,
    val outages: OutagesResponse,
)

class UsageViewModel(hubClient: HubClient) : SectionViewModel<UsageData>(hubClient) {
    override suspend fun fetch(): HubClient.ApiResult<UsageData> {
        val usage = hubClient.get<UsageResponse>("/monitor/usage")
        if (usage is HubClient.ApiResult.Failure) return usage
        val top = hubClient.get<UsageTopResponse>("/monitor/usage/top")
        val jobs = hubClient.get<JobsResponse>("/monitor/jobs")
        return HubClient.ApiResult.Success(
            UsageData(
                usage = (usage as HubClient.ApiResult.Success).value,
                top = (top as? HubClient.ApiResult.Success)?.value ?: UsageTopResponse(),
                jobs = (jobs as? HubClient.ApiResult.Success)?.value ?: JobsResponse(),
            )
        )
    }
}

data class UsageData(
    val usage: UsageResponse,
    val top: UsageTopResponse,
    val jobs: JobsResponse,
)

class ConfigsViewModel(hubClient: HubClient) : SectionViewModel<ConfigsResponse>(hubClient) {
    override suspend fun fetch() = hubClient.get<ConfigsResponse>("/configs")

    var openFile by mutableStateOf<ConfigFileResponse?>(null)
        private set
    var openFileError by mutableStateOf<String?>(null)
        private set
    var openFileLoading by mutableStateOf(false)
        private set

    fun open(path: String) {
        viewModelScope.launch {
            openFileLoading = true
            openFileError = null
            openFile = null
            when (val result = hubClient.get<ConfigFileResponse>("/configs/file?path=$path")) {
                is HubClient.ApiResult.Success -> openFile = result.value
                is HubClient.ApiResult.Failure -> openFileError = result.message
            }
            openFileLoading = false
        }
    }

    fun closeFile() {
        openFile = null
        openFileError = null
    }
}

class FirewallViewModel(hubClient: HubClient) : SectionViewModel<FirewallData>(hubClient) {
    override suspend fun fetch(): HubClient.ApiResult<FirewallData> {
        val state = hubClient.get<FirewallResponse>("/firewall")
        if (state is HubClient.ApiResult.Failure) return state
        val numbered = hubClient.get<FirewallNumberedResponse>("/firewall/rules")
        return HubClient.ApiResult.Success(
            FirewallData(
                state = (state as HubClient.ApiResult.Success).value,
                numbered = (numbered as? HubClient.ApiResult.Success)?.value
                    ?: FirewallNumberedResponse(),
            )
        )
    }
}

data class FirewallData(
    val state: FirewallResponse,
    val numbered: FirewallNumberedResponse,
)

class CertificatesViewModel(hubClient: HubClient) :
    SectionViewModel<CertificatesResponse>(hubClient) {
    override suspend fun fetch() = hubClient.get<CertificatesResponse>("/certificates")
}

class TopologyViewModel(hubClient: HubClient) : SectionViewModel<TopologyResponse>(hubClient) {
    override suspend fun fetch() = hubClient.get<TopologyResponse>("/topology")
}

/** Minimal JSON string escaping for the small bodies built by hand above. */
private fun String.jsonString(): String = Json.encodeToString(String.serializer(), this)
