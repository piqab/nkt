package com.netknownsthat.ui.host

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.lifecycle.viewModelScope
import com.netknownsthat.domain.common.AppError
import com.netknownsthat.domain.common.HostTarget
import com.netknownsthat.domain.common.Outcome
import com.netknownsthat.domain.common.getOrNull
import com.netknownsthat.domain.model.AuditResponse
import com.netknownsthat.domain.model.CertificatesResponse
import com.netknownsthat.domain.model.ConfigFileResponse
import com.netknownsthat.domain.model.ConfigVersion
import com.netknownsthat.domain.model.ConfigWriteResult
import com.netknownsthat.domain.model.ConfigsResponse
import com.netknownsthat.domain.model.ContainerInspect
import com.netknownsthat.domain.model.FindingsResponse
import com.netknownsthat.domain.model.FirewalldPortSpec
import com.netknownsthat.domain.model.InterfacesResponse
import com.netknownsthat.domain.model.LineageInfo
import com.netknownsthat.domain.model.MiscResponse
import com.netknownsthat.domain.model.Overview
import com.netknownsthat.domain.model.RenewEvent
import com.netknownsthat.domain.model.RuleSpec
import com.netknownsthat.domain.model.SelfSignedResult
import com.netknownsthat.domain.model.ServicesResponse
import com.netknownsthat.domain.model.TopologyResponse
import com.netknownsthat.domain.model.UsersResponse
import com.netknownsthat.domain.model.VulnResponse
import com.netknownsthat.domain.repository.CertificatesRepository
import com.netknownsthat.domain.repository.ConfigsRepository
import com.netknownsthat.domain.repository.ContainersRepository
import com.netknownsthat.domain.repository.FirewallRepository
import com.netknownsthat.domain.repository.HostInfoRepository
import com.netknownsthat.domain.repository.HostMonitoringRepository
import com.netknownsthat.domain.repository.Runtime
import com.netknownsthat.domain.repository.SecurityRepository
import com.netknownsthat.domain.repository.ServicesRepository
import com.netknownsthat.domain.repository.UsersRepository
import com.netknownsthat.domain.usecase.AvailabilityData
import com.netknownsthat.domain.usecase.ContainerRuntimes
import com.netknownsthat.domain.usecase.FirewallData
import com.netknownsthat.domain.usecase.LoadAvailabilityUseCase
import com.netknownsthat.domain.usecase.LoadContainersUseCase
import com.netknownsthat.domain.usecase.LoadFirewallUseCase
import com.netknownsthat.domain.usecase.LoadUsageUseCase
import com.netknownsthat.domain.usecase.USAGE_MAX_SERIES
import com.netknownsthat.domain.usecase.UsageData
import com.netknownsthat.domain.usecase.UsageQuery
import com.netknownsthat.ui.common.SectionViewModel
import com.netknownsthat.ui.common.failedText
import com.netknownsthat.ui.common.text
import com.netknownsthat.ui.i18n.t
import com.netknownsthat.ui.status.HealthStatus
import com.netknownsthat.ui.status.serviceHealth
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

class OverviewViewModel(private val host: HostTarget, private val repo: HostInfoRepository) : SectionViewModel<Overview>() {
    override suspend fun fetch() = repo.overview(host)
}

class FindingsViewModel(private val host: HostTarget, private val repo: HostInfoRepository) : SectionViewModel<FindingsResponse>() {
    override suspend fun fetch() = repo.findings(host)
}

class InterfacesViewModel(private val host: HostTarget, private val repo: HostInfoRepository) : SectionViewModel<InterfacesResponse>() {
    override suspend fun fetch() = repo.interfaces(host)
}

class AuditViewModel(private val host: HostTarget, private val repo: HostInfoRepository) : SectionViewModel<AuditResponse>() {
    override suspend fun fetch() = repo.audit(host)
}

class MiscViewModel(private val host: HostTarget, private val repo: HostInfoRepository) : SectionViewModel<MiscResponse>() {
    override suspend fun fetch() = repo.misc(host)
}

class TopologyViewModel(private val host: HostTarget, private val repo: HostInfoRepository) : SectionViewModel<TopologyResponse>() {
    override suspend fun fetch() = repo.topology(host)
}

class ServicesViewModel(private val host: HostTarget, private val repo: ServicesRepository) : SectionViewModel<ServicesResponse>() {
    override suspend fun fetch() = repo.services(host)

    fun action(name: String, action: String) {
        val call = suspend { repo.action(host, name, action) }
        val expectRunning = when (action) {
            "start", "restart", "reload" -> true
            "stop" -> false
            else -> return act(t("$name: $action выполнено", "$name: $action done"), call)
        }
        actAwaiting(
            key = name,
            okMessage = if (expectRunning) t("$name запущен", "$name started") else t("$name остановлен", "$name stopped"),
            settled = { r ->
                val unit = r.services.find { it.name == name } ?: return@actAwaiting true
                val health = serviceHealth(unit.activeState)
                (health == HealthStatus.OK) == expectRunning || health == HealthStatus.BAD
            },
            call = call,
        )
    }
}

class ContainersViewModel(
    private val host: HostTarget,
    private val repo: ContainersRepository,
    private val loadContainers: LoadContainersUseCase,
) : SectionViewModel<ContainerRuntimes>() {
    override suspend fun fetch() = loadContainers(host)

    var inspect by mutableStateOf<ContainerInspect?>(null)
        private set
    var inspectError by mutableStateOf<String?>(null)
        private set

    fun loadInspect(name: String, reveal: Boolean) {
        viewModelScope.launch {
            inspectError = null
            if (inspect?.name != name) inspect = null
            when (val r = repo.inspect(host, name, reveal)) {
                is Outcome.Success -> inspect = r.value
                is Outcome.Failure -> inspectError = r.error.text()
            }
        }
    }

    fun dockerAction(name: String, action: String) = runtimeAction(Runtime.DOCKER, name, action) { d ->
        d.docker.containers.find { it.name == name }?.running
    }

    fun podmanAction(name: String, action: String) = runtimeAction(Runtime.PODMAN, name, action) { d ->
        d.podman.containers.find { it.name == name }?.let { it.state.equals("running", ignoreCase = true) }
    }

    fun lxdAction(name: String, action: String) = runtimeAction(Runtime.LXD, name, action) { d ->
        d.lxd.instances.find { it.name == name }?.let { it.status.equals("running", ignoreCase = true) }
    }

    fun vmAction(name: String, action: String) = runtimeAction(Runtime.VM, name, action) { d ->
        d.vms.vms.find { it.name == name }?.let { it.state.equals("running", ignoreCase = true) }
    }

    fun removeImages(refs: List<String>, force: Boolean) =
        act(t("Удаление образов выполнено", "Images deleted")) { repo.removeImages(host, refs, force) }

    fun saveImages(refs: List<String>) = act(t("Образы сохранены на хосте", "Images saved on the host")) { repo.saveImages(host, refs) }

    fun pruneImages() = act(t("Осиротевшие образы убраны", "Dangling images removed")) { repo.pruneImages(host) }

    /** The request returns at once and the container takes a moment, so the
     * spinner waits for [isRunning]; null — gone from the list (a stop's answer too). */
    private fun runtimeAction(runtime: Runtime, name: String, action: String, isRunning: (ContainerRuntimes) -> Boolean?) {
        val expectRunning = action != "stop"
        actAwaiting(
            key = name,
            okMessage = if (expectRunning) t("$name запущен", "$name started") else t("$name остановлен", "$name stopped"),
            settled = { d -> (isRunning(d) ?: !expectRunning) == expectRunning },
            call = { repo.action(host, runtime, name, action) },
        )
    }
}

class UsersViewModel(private val host: HostTarget, private val repo: UsersRepository) : SectionViewModel<UsersResponse>() {
    override suspend fun fetch() = repo.users(host)

    fun create(username: String, password: String, role: String) =
        act(t("Пользователь $username создан", "User $username created")) { repo.create(host, username, password, role) }

    fun setDisabled(username: String, disabled: Boolean) =
        act(if (disabled) t("$username отключён", "$username disabled") else t("$username включён", "$username enabled")) {
            repo.setDisabled(host, username, disabled)
        }

    fun delete(username: String) = act(t("$username удалён", "$username deleted")) { repo.delete(host, username) }
}

class VulnerabilitiesViewModel(private val host: HostTarget, private val repo: SecurityRepository) : SectionViewModel<VulnResponse>() {
    override suspend fun fetch() = repo.vulnerabilities(host)

    fun startScan() = act(t("Сканирование запущено", "Scan started")) { repo.startVulnScan(host) }

    /** Polls while a scan runs, then stops — no timer keeping the radio awake. */
    fun pollWhileScanning() {
        viewModelScope.launch {
            while (state.data?.scanning == true) {
                delay(3000)
                load()
            }
        }
    }
}

class AvailabilityViewModel(
    private val host: HostTarget,
    private val repo: HostMonitoringRepository,
    private val loadAvailability: LoadAvailabilityUseCase,
) : SectionViewModel<AvailabilityData>() {
    override suspend fun fetch() = loadAvailability(host)
    fun check(targetId: Long) = act(t("Проверка выполнена", "Check done")) { repo.checkTarget(host, targetId) }
}

class UsageViewModel(private val host: HostTarget, private val loadUsage: LoadUsageUseCase) : SectionViewModel<UsageData>() {
    var query by mutableStateOf(UsageQuery())
        private set

    val source: String? get() = state.data?.source ?: query.source
    val metric: String get() = state.data?.metric ?: query.metric
    val node: String? get() = query.node
    val namespace: String? get() = query.namespace
    val picked: Set<String> get() = query.picked

    override suspend fun fetch() = loadUsage(host, query)

    fun choose(source: String? = this.source, metric: String = this.metric) {
        query = if (source != this.source) UsageQuery(source = source, metric = metric)
        else query.copy(metric = metric, picked = if (metric != this.metric) emptySet() else query.picked)
        load()
    }

    fun setNode(node: String?) { query = query.copy(node = node); load() }
    fun setNamespace(namespace: String?) { query = query.copy(namespace = namespace); load() }

    fun toggle(subject: String) {
        val p = query.picked
        query = query.copy(
            picked = when {
                subject in p -> p - subject
                p.size >= USAGE_MAX_SERIES -> p
                else -> p + subject
            },
        )
        load()
    }
}

class ConfigsViewModel(private val host: HostTarget, private val repo: ConfigsRepository) : SectionViewModel<ConfigsResponse>() {
    override suspend fun fetch() = repo.configs(host)

    var openFile by mutableStateOf<ConfigFileResponse?>(null)
        private set
    var openFileError by mutableStateOf<String?>(null)
        private set
    var openFileLoading by mutableStateOf(false)
        private set
    var versions by mutableStateOf<List<ConfigVersion>>(emptyList())
        private set
    var diff by mutableStateOf<String?>(null)
        private set
    var writeResult by mutableStateOf<ConfigWriteResult?>(null)
        private set
    var saving by mutableStateOf(false)
        private set

    fun open(path: String) {
        viewModelScope.launch {
            openFileLoading = true
            openFileError = null
            openFile = null
            when (val r = repo.file(host, path)) {
                is Outcome.Success -> openFile = r.value
                is Outcome.Failure -> openFileError = r.error.text()
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

    fun dismissWriteResult() { writeResult = null }

    fun loadVersions(path: String) {
        viewModelScope.launch { repo.versions(host, path).getOrNull()?.let { versions = it.versions } }
    }

    fun loadDiff(versionId: Long) {
        viewModelScope.launch {
            diff = null
            diff = when (val r = repo.diff(host, versionId)) {
                // An empty diff is the honest answer for the version on disk.
                is Outcome.Success -> r.value.diff.ifEmpty { t("Эта версия совпадает с текущим файлом.", "This version matches the current file.") }
                is Outcome.Failure -> t("Не удалось получить diff: ${r.error.text()}", "Could not get the diff: ${r.error.text()}")
            }
        }
    }

    fun clearDiff() { diff = null }

    /** [expectedSha256] makes the host refuse a write over someone else's
     * newer edit; [apply] reloads the owning service afterwards. */
    fun save(path: String, content: String, expectedSha256: String, note: String, apply: Boolean) {
        if (saving) return
        viewModelScope.launch {
            saving = true
            when (val r = repo.save(host, path, content, expectedSha256, note, apply)) {
                is Outcome.Success -> {
                    writeResult = r.value
                    open(path)
                    loadVersions(path)
                }
                is Outcome.Failure -> openFileError =
                    if ((r.error as? AppError.Server)?.status == 409) {
                        t("Файл изменился на хосте после того, как был открыт. Откройте его заново и повторите правку.",
                            "The file changed on the host after it was opened. Open it again and repeat the edit.")
                    } else r.error.text()
            }
            saving = false
        }
    }

    fun rollback(versionId: Long, path: String, apply: Boolean) {
        if (saving) return
        viewModelScope.launch {
            saving = true
            when (val r = repo.rollback(host, versionId, apply)) {
                is Outcome.Success -> {
                    writeResult = r.value
                    diff = null
                    open(path)
                    loadVersions(path)
                }
                is Outcome.Failure -> openFileError = r.error.text()
            }
            saving = false
        }
    }
}

class FirewallViewModel(
    private val host: HostTarget,
    private val repo: FirewallRepository,
    private val loadFirewall: LoadFirewallUseCase,
) : SectionViewModel<FirewallData>() {
    override suspend fun fetch() = loadFirewall(host)

    fun addUfwRule(spec: RuleSpec) = act(t("Правило добавлено", "Rule added")) { repo.addUfw(host, spec) }

    /** By ufw's number with the text that was on screen: ufw renumbers after
     * every change, and the host checks the two before touching anything. */
    fun deleteUfwRule(number: Int, expectedText: String) = act(t("Правило удалено", "Rule deleted")) { repo.deleteUfw(host, number, expectedText) }

    fun deleteUfwRuleBySpec(spec: RuleSpec) = act(t("Правило удалено", "Rule deleted")) { repo.deleteUfwBySpec(host, spec) }
    fun deleteFirewalldRule(spec: FirewalldPortSpec) = act(t("Правило удалено", "Rule deleted")) { repo.deleteFirewalld(host, spec) }
    fun reloadUfw() = act(t("ufw перечитан", "ufw reloaded")) { repo.reloadUfw(host) }
    fun addFirewalldRule(spec: FirewalldPortSpec) = act(t("Правило добавлено", "Rule added")) { repo.addFirewalld(host, spec) }
    fun reloadFirewalld() = act(t("firewalld перечитан", "firewalld reloaded")) { repo.reloadFirewalld(host) }
}

/** Ports whose loss locks an operator out of the host entirely. */
private val LOCKOUT_PORTS = setOf(22, 8077, 8078)

/** A deny/reject on a port the way in depends on (limit still lets
 * connections through, so it is not flagged). */
fun ruleRisksLockout(spec: RuleSpec): Boolean =
    spec.action in setOf("deny", "reject") && spec.port in LOCKOUT_PORTS

class CertificatesViewModel(private val host: HostTarget, private val repo: CertificatesRepository) : SectionViewModel<CertificatesResponse>() {
    override suspend fun fetch() = repo.certificates(host)

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
    var selfSigned by mutableStateOf<List<SelfSignedResult>>(emptyList())
        private set

    fun loadForms() {
        viewModelScope.launch {
            repo.lineages(host).getOrNull()?.let { lineages = it }
            repo.haproxyPaths(host).getOrNull()?.let { haproxyPaths = it }
        }
    }

    fun dismissJob() { jobEvents = emptyList(); jobError = null }
    fun dismissSelfSigned() { selfSigned = emptyList() }

    fun generateSelfSigned(names: List<String>, service: String, bits: Int, days: Int) {
        if (actionInProgress) return
        viewModelScope.launch {
            when (val r = repo.selfSigned(host, names, service, bits, days)) {
                is Outcome.Success -> { selfSigned = r.value.asList(); load() }
                is Outcome.Failure -> actionMessage = failedText(r.error)
            }
        }
    }

    fun issue(domains: List<String>) = startJob { repo.issue(host, domains) }
    fun renew(lineage: String) = startJob { repo.renew(host, lineage) }

    fun combine(lineage: String, targetPath: String) =
        act(t("PEM собран", "PEM built")) { repo.combine(host, lineage, targetPath) }

    /** Issuing and renewing take minutes: the host answers with a job id at
     * once and the progress log is polled. */
    private fun startJob(start: suspend () -> Outcome<String>) {
        if (jobRunning) return
        viewModelScope.launch {
            jobEvents = emptyList()
            jobError = null
            val id = when (val r = start()) {
                is Outcome.Failure -> { jobError = r.error.text(); return@launch }
                is Outcome.Success -> r.value
            }
            jobRunning = true
            while (jobRunning) {
                delay(1500)
                when (val s = repo.renewStatus(host, id)) {
                    is Outcome.Success -> {
                        jobEvents = s.value.events
                        if (s.value.error.isNotBlank()) jobError = s.value.error
                        if (s.value.done) jobRunning = false
                    }
                    // Gone (finished and evicted, or never existed): nothing to poll.
                    is Outcome.Failure -> { jobError = s.error.text(); jobRunning = false }
                }
            }
            load()
            loadForms()
        }
    }
}
