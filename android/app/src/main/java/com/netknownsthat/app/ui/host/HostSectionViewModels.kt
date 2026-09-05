package com.netknownsthat.app.ui.host

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.netknownsthat.app.net.HubClient
import com.netknownsthat.app.status.HealthStatus
import com.netknownsthat.app.status.containerHealth
import com.netknownsthat.app.status.instanceHealth
import com.netknownsthat.app.status.serviceHealth
import com.netknownsthat.app.net.model.AuditResponse
import com.netknownsthat.app.net.model.CertificatesResponse
import com.netknownsthat.app.net.model.CommandStatus
import com.netknownsthat.app.net.model.FirewalldPortSpec
import com.netknownsthat.app.net.model.HAProxyPathsResponse
import com.netknownsthat.app.net.model.JobStarted
import com.netknownsthat.app.net.model.LineageInfo
import com.netknownsthat.app.net.model.LineagesResponse
import com.netknownsthat.app.net.model.RenewEvent
import com.netknownsthat.app.net.model.RenewJobStatus
import com.netknownsthat.app.net.model.RuleSpec
import com.netknownsthat.app.net.model.SelfSignedResult
import com.netknownsthat.app.net.model.ConfigFileResponse
import com.netknownsthat.app.net.model.ConfigVersion
import com.netknownsthat.app.net.model.ConfigVersionsResponse
import com.netknownsthat.app.net.model.ConfigDiffResponse
import com.netknownsthat.app.net.model.ConfigWriteResult
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
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.builtins.serializer
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put

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

    /** Which item currently has an action in flight, so its row can show a
     * spinner instead of a status dot. Null when nothing is pending. */
    var pendingKey by mutableStateOf<String?>(null)
        private set

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

    /**
     * Like [act], but keeps [key]'s spinner running until the host actually
     * reaches the state that was asked for.
     *
     * systemd (and Docker, and libvirt) answer the request immediately and
     * then take their time doing the work, so a single refetch after the call
     * usually still shows the old state — the operator presses "start", sees
     * "inactive" a moment later, and reasonably concludes it failed. Polling
     * until [settled] says otherwise is what makes the button honest.
     *
     * Gives up after [timeoutMs] and says so rather than spinning forever: a
     * service that is genuinely stuck must not look like a hung app.
     */
    protected fun actAwaiting(
        key: String,
        okMessage: String,
        timeoutMs: Long = 20_000,
        pollMs: Long = 800,
        settled: (T) -> Boolean,
        call: suspend () -> HubClient.ApiResult<*>,
    ) {
        if (actionInProgress) return
        viewModelScope.launch {
            actionInProgress = true
            pendingKey = key

            when (val result = call()) {
                is HubClient.ApiResult.Failure -> {
                    actionMessage = "Не удалось: ${result.message}"
                    actionInProgress = false
                    pendingKey = null
                    load()
                    return@launch
                }

                is HubClient.ApiResult.Success -> Unit
            }

            val deadline = System.currentTimeMillis() + timeoutMs
            var reached = false
            while (System.currentTimeMillis() < deadline) {
                delay(pollMs)
                when (val fetched = fetch()) {
                    is HubClient.ApiResult.Success -> {
                        state = state.copy(loading = false, data = fetched.value, error = null)
                        if (settled(fetched.value)) {
                            reached = true
                            break
                        }
                    }

                    is HubClient.ApiResult.Failure ->
                        state = state.copy(loading = false, error = fetched.message)
                }
            }

            actionMessage = if (reached) okMessage
            else "$okMessage — но состояние не изменилось за ${timeoutMs / 1000} с"
            pendingKey = null
            actionInProgress = false
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

    fun action(name: String, action: String) {
        val call: suspend () -> HubClient.ApiResult<*> =
            { hubClient.post<JsonObject>("/services/$name/$action") }

        // enable/disable change whether a unit starts at boot, not whether it
        // is running now — there is no runtime state to wait for.
        val expectRunning = when (action) {
            "start", "restart", "reload" -> true
            "stop" -> false
            else -> return act("$name: $action выполнено", call)
        }

        actAwaiting(
            key = name,
            okMessage = if (expectRunning) "$name запущен" else "$name остановлен",
            settled = { response ->
                val unit = response.services.find { it.name == name }
                    ?: return@actAwaiting true
                val running = serviceHealth(unit.activeState) == HealthStatus.OK
                // A unit that lands in "failed" is settled too: it is done
                // trying, and spinning until the timeout would only hide the
                // answer the operator needs.
                running == expectRunning || serviceHealth(unit.activeState) == HealthStatus.BAD
            },
            call = call,
        )
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

    fun dockerAction(name: String, action: String) = runtimeAction(name, action, "/containers/$name/$action") { data ->
        data.docker.containers.find { it.name == name }?.running
    }

    fun podmanAction(name: String, action: String) =
        runtimeAction(name, action, "/podman/containers/$name/$action") { data ->
            data.podman.containers.find { it.name == name }
                ?.let { containerHealth(it.state) == HealthStatus.OK }
        }

    fun lxdAction(name: String, action: String) =
        runtimeAction(name, action, "/lxd/instances/$name/$action") { data ->
            data.lxd.instances.find { it.name == name }
                ?.let { instanceHealth(it.status) == HealthStatus.OK }
        }

    fun vmAction(name: String, action: String) =
        runtimeAction(name, action, "/vms/$name/$action") { data ->
            data.vms.vms.find { it.name == name }
                ?.let { instanceHealth(it.state) == HealthStatus.OK }
        }

    /**
     * All four runtimes behave the same way here: the request returns at
     * once and the container takes a moment to actually start or stop, so
     * the spinner waits for [isRunning] to report what was asked for. Null
     * from it means the item is gone from the list, which for a stop is the
     * answer too.
     */
    private fun runtimeAction(
        name: String,
        action: String,
        path: String,
        isRunning: (ContainerRuntimes) -> Boolean?,
    ) {
        val expectRunning = action != "stop"
        actAwaiting(
            key = name,
            okMessage = if (expectRunning) "$name запущен" else "$name остановлен",
            settled = { data -> (isRunning(data) ?: !expectRunning) == expectRunning },
            call = { hubClient.post<JsonObject>(path) },
        )
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
        versions = emptyList()
        diff = null
        writeResult = null
    }

    var versions by mutableStateOf<List<ConfigVersion>>(emptyList())
        private set
    var diff by mutableStateOf<String?>(null)
        private set
    var writeResult by mutableStateOf<ConfigWriteResult?>(null)
        private set
    var saving by mutableStateOf(false)
        private set

    fun dismissWriteResult() {
        writeResult = null
    }

    fun loadVersions(path: String) {
        viewModelScope.launch {
            val result = hubClient.get<ConfigVersionsResponse>("/configs/versions?path=$path")
            if (result is HubClient.ApiResult.Success) versions = result.value.versions
        }
    }

    fun loadDiff(versionId: Long) {
        viewModelScope.launch {
            diff = null
            when (val result = hubClient.get<ConfigDiffResponse>("/configs/versions/$versionId/diff")) {
                is HubClient.ApiResult.Success ->
                    // An empty diff is the honest answer for the version that
                    // is already on disk, and saying so beats a blank screen.
                    diff = result.value.diff.ifEmpty { "Эта версия совпадает с текущим файлом." }

                is HubClient.ApiResult.Failure -> diff = "Не удалось получить diff: ${result.message}"
            }
        }
    }

    fun clearDiff() {
        diff = null
    }

    /**
     * Saves an edit. [expectedSha256] is always sent: the server refuses the
     * write if the file changed on disk since it was read, which is the only
     * thing standing between two people editing at once and one of them
     * losing their work silently.
     *
     * [apply] asks the host to reload the owning service afterwards. Off by
     * default — writing the file and restarting a service are different sizes
     * of decision, especially from a phone.
     */
    fun save(path: String, content: String, expectedSha256: String, note: String, apply: Boolean) {
        if (saving) return
        viewModelScope.launch {
            saving = true
            val body = buildJsonObject {
                put("path", path)
                put("content", content)
                put("note", note)
                put("apply", apply)
                put("expected_sha256", expectedSha256)
            }.toString()

            when (val result = hubClient.put<ConfigWriteResult>("/configs/file", body)) {
                is HubClient.ApiResult.Success -> {
                    writeResult = result.value
                    open(path)
                    loadVersions(path)
                }

                is HubClient.ApiResult.Failure -> {
                    // 409 is the stale-content case specifically, and the
                    // remedy is different from any other failure: reload and
                    // redo the edit rather than retry the same write.
                    openFileError = if (result.httpCode == 409)
                        "Файл изменился на хосте после того, как был открыт. " +
                            "Откройте его заново и повторите правку."
                    else result.message
                }
            }
            saving = false
        }
    }

    fun rollback(versionId: Long, path: String, apply: Boolean) {
        if (saving) return
        viewModelScope.launch {
            saving = true
            val body = buildJsonObject { put("apply", apply) }.toString()
            when (val result =
                hubClient.post<ConfigWriteResult>("/configs/versions/$versionId/rollback", body)) {
                is HubClient.ApiResult.Success -> {
                    writeResult = result.value
                    diff = null
                    open(path)
                    loadVersions(path)
                }

                is HubClient.ApiResult.Failure -> openFileError = result.message
            }
            saving = false
        }
    }
}

class FirewallViewModel(hubClient: HubClient) : SectionViewModel<FirewallData>(hubClient) {

    fun addUfwRule(spec: RuleSpec) = act("Правило добавлено") {
        hubClient.post<CommandStatus>("/firewall/rules", Json.encodeToString(RuleSpec.serializer(), spec))
    }

    /**
     * Deletes by ufw's own rule number, sending the text that was on screen
     * next to it. The server compares the two before touching anything:
     * ufw renumbers after every change, so acting on a stale number is how
     * the wrong rule — possibly the one keeping SSH open — gets deleted.
     */
    fun deleteUfwRule(number: Int, expectedText: String) = act("Правило удалено") {
        hubClient.delete<CommandStatus>(
            "/firewall/rules/$number",
            buildJsonObject { put("expected", expectedText) }.toString(),
        )
    }

    /** For rules ufw knows about but does not number — `ufw status numbered`
     * lists nothing at all while ufw is inactive. */
    fun deleteUfwRuleBySpec(spec: RuleSpec) = act("Правило удалено") {
        hubClient.delete<CommandStatus>(
            "/firewall/rules",
            Json.encodeToString(RuleSpec.serializer(), spec),
        )
    }

    fun deleteFirewalldRule(spec: FirewalldPortSpec) = act("Правило удалено") {
        hubClient.delete<CommandStatus>(
            "/firewall/firewalld/rules",
            Json.encodeToString(FirewalldPortSpec.serializer(), spec),
        )
    }

    fun reloadUfw() = act("ufw перечитан") {
        hubClient.post<CommandStatus>("/firewall/reload")
    }

    fun addFirewalldRule(spec: FirewalldPortSpec) = act("Правило добавлено") {
        hubClient.post<CommandStatus>(
            "/firewall/firewalld/rules",
            Json.encodeToString(FirewalldPortSpec.serializer(), spec),
        )
    }

    fun reloadFirewalld() = act("firewalld перечитан") {
        hubClient.post<CommandStatus>("/firewall/firewalld/reload")
    }

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

/** Ports whose loss locks an operator out of the host entirely. */
private val LOCKOUT_PORTS = setOf(22, 8077, 8078)

/**
 * True when a rule would block a port that the way in depends on.
 *
 * Only deny and reject qualify: ufw's `limit` rate-limits connections but
 * still lets them through, so warning about it would cry wolf on a rule
 * people add to SSH on purpose.
 */
fun ruleRisksLockout(spec: RuleSpec): Boolean =
    spec.action in setOf("deny", "reject") && spec.port in LOCKOUT_PORTS

class CertificatesViewModel(hubClient: HubClient) :
    SectionViewModel<CertificatesResponse>(hubClient) {
    override suspend fun fetch() = hubClient.get<CertificatesResponse>("/certificates")

    var lineages by mutableStateOf<List<LineageInfo>>(emptyList())
        private set
    var haproxyPaths by mutableStateOf<List<String>>(emptyList())
        private set

    /** Log of the running (or last) certbot job, newest last. */
    var jobEvents by mutableStateOf<List<RenewEvent>>(emptyList())
        private set
    var jobRunning by mutableStateOf(false)
        private set
    var jobError by mutableStateOf<String?>(null)
        private set

    var selfSigned by mutableStateOf<SelfSignedResult?>(null)
        private set

    fun loadForms() {
        viewModelScope.launch {
            (hubClient.get<LineagesResponse>("/certificates/lineages")
                as? HubClient.ApiResult.Success)?.let { lineages = it.value.lineages }
            (hubClient.get<HAProxyPathsResponse>("/certificates/haproxy-paths")
                as? HubClient.ApiResult.Success)?.let { haproxyPaths = it.value.paths }
        }
    }

    fun dismissJob() {
        jobEvents = emptyList()
        jobError = null
    }

    fun dismissSelfSigned() {
        selfSigned = null
    }

    fun generateSelfSigned(names: List<String>, service: String, bits: Int, days: Int) {
        if (actionInProgress) return
        viewModelScope.launch {
            val body = buildJsonObject {
                put("names", Json.encodeToJsonElement(ListSerializer(String.serializer()), names))
                put("service", service)
                put("bits", bits)
                put("days", days)
            }.toString()
            when (val result = hubClient.post<SelfSignedResult>("/certificates/self-signed", body)) {
                is HubClient.ApiResult.Success -> {
                    selfSigned = result.value
                    load()
                }

                is HubClient.ApiResult.Failure -> actionMessage = "Не удалось: ${result.message}"
            }
        }
    }

    fun issue(domains: List<String>) = startJob("/certificates/issue") {
        put("domains", Json.encodeToJsonElement(ListSerializer(String.serializer()), domains))
    }

    fun renew(lineage: String) = startJob("/certificates/renew") { put("lineage", lineage) }

    fun combine(lineage: String, targetPath: String) {
        if (actionInProgress) return
        viewModelScope.launch {
            val body = buildJsonObject {
                put("lineage", lineage)
                put("target_path", targetPath)
            }.toString()
            actionMessage = when (val result = hubClient.post<JsonObject>("/certificates/combine", body)) {
                is HubClient.ApiResult.Success -> "PEM собран"
                is HubClient.ApiResult.Failure -> "Не удалось: ${result.message}"
            }
            load()
        }
    }

    /**
     * Issuing and renewing take minutes (stop services, run certbot, restart),
     * so the server answers with a job id straight away and the progress log
     * is polled — see handleRenewJobStatus.
     */
    private fun startJob(path: String, body: kotlinx.serialization.json.JsonObjectBuilder.() -> Unit) {
        if (jobRunning) return
        viewModelScope.launch {
            jobEvents = emptyList()
            jobError = null
            val started = hubClient.post<JobStarted>(path, buildJsonObject(body).toString())
            if (started is HubClient.ApiResult.Failure) {
                jobError = started.message
                return@launch
            }
            val id = (started as HubClient.ApiResult.Success).value.job
            jobRunning = true
            while (jobRunning) {
                delay(1500)
                when (val status = hubClient.get<RenewJobStatus>("/certificates/renew/$id")) {
                    is HubClient.ApiResult.Success -> {
                        jobEvents = status.value.events
                        if (status.value.error.isNotBlank()) jobError = status.value.error
                        if (status.value.done) jobRunning = false
                    }

                    is HubClient.ApiResult.Failure -> {
                        // A 404 means the job is gone — finished and evicted,
                        // or never existed. Either way there is nothing left
                        // to poll for.
                        jobError = status.message
                        jobRunning = false
                    }
                }
            }
            load()
            loadForms()
        }
    }
}

class TopologyViewModel(hubClient: HubClient) : SectionViewModel<TopologyResponse>(hubClient) {
    override suspend fun fetch() = hubClient.get<TopologyResponse>("/topology")
}

/** Minimal JSON string escaping for the small bodies built by hand above. */
private fun String.jsonString(): String = Json.encodeToString(String.serializer(), this)
