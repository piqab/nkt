package com.netknownsthat.ui.hosts

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.netknownsthat.domain.common.Outcome
import com.netknownsthat.domain.common.getOrNull
import com.netknownsthat.domain.model.HubHost
import com.netknownsthat.domain.repository.EventsRepository
import com.netknownsthat.domain.repository.HostsRepository
import com.netknownsthat.domain.repository.InstallStart
import com.netknownsthat.domain.repository.NewHost
import com.netknownsthat.ui.common.failedText
import com.netknownsthat.ui.common.text
import com.netknownsthat.ui.i18n.t
import kotlinx.coroutines.launch

data class HostListUiState(
    val loading: Boolean = true,
    val hosts: List<HubHost> = emptyList(),
    val error: String? = null,
    /** Unread hub alerts, for the bell. */
    val unread: Int = 0,
)

/** The add-host form. auth_kind: password, key (PEM private key in
 * [secret]) or generated (the hub makes a key pair and shows the public line). */
data class AddHostForm(
    val name: String = "",
    val addr: String = "",
    val port: Int = 22,
    val user: String = "root",
    val authKind: String = "password",
    val secret: String = "",
    val group: String = "",
    val terminal: Boolean = false,
)

/** The hub's hosts and their management (the web UI's «Хосты» actions). */
class HostListViewModel(
    private val hostsRepo: HostsRepository,
    private val events: EventsRepository,
) : ViewModel() {
    var uiState by mutableStateOf(HostListUiState())
        private set

    /** Result of the last management action, for a snackbar. */
    var message by mutableStateOf<String?>(null)
    var groups by mutableStateOf<List<String>>(emptyList())
        private set

    /** A key the operator must add on a new host (auth "generated"). */
    var pendingKey by mutableStateOf<String?>(null)

    /** The host already has a foreign nkt: install waits for «install over it». */
    var foreignInstall by mutableStateOf<Pair<Long, String>?>(null)

    fun refresh() {
        viewModelScope.launch {
            uiState = uiState.copy(loading = true, error = null)
            uiState = when (val r = hostsRepo.hosts()) {
                is Outcome.Success -> uiState.copy(loading = false, hosts = r.value)
                is Outcome.Failure -> uiState.copy(loading = false, error = r.error.text())
            }
            events.events(limit = 1).getOrNull()?.let { uiState = uiState.copy(unread = it.unread) }
        }
    }

    fun loadGroups() {
        viewModelScope.launch { hostsRepo.groups().getOrNull()?.let { groups = it } }
    }

    fun addHost(form: AddHostForm, install: Boolean, onJob: (Long) -> Unit) {
        viewModelScope.launch {
            val r = hostsRepo.add(
                NewHost(form.name, form.addr, form.port, form.user, form.authKind, form.secret, form.group, form.terminal),
            )
            when (r) {
                is Outcome.Failure -> message = failedText(r.error)
                is Outcome.Success -> {
                    refresh()
                    when {
                        // Nothing can be installed until the key is on the host.
                        r.value.authorizedKey.isNotBlank() -> {
                            pendingKey = r.value.authorizedKey
                            message = t(
                                "Хост ${form.name} добавлен — добавьте ключ хаба на хост и нажмите «Установить nkt»",
                                "Host ${form.name} added — add the hub key on the host and tap “Install nkt”",
                            )
                        }
                        install -> install(r.value.id, false, onJob)
                        else -> message = t("Хост ${form.name} добавлен", "Host ${form.name} added")
                    }
                }
            }
        }
    }

    fun install(id: Long, force: Boolean, onJob: (Long) -> Unit) {
        viewModelScope.launch {
            when (val r = hostsRepo.install(id, force)) {
                is Outcome.Success -> when (val s = r.value) {
                    is InstallStart.Started -> { onJob(s.jobId); refresh() }
                    is InstallStart.ForeignInstall -> foreignInstall = id to s.message
                }
                is Outcome.Failure -> message = failedText(r.error)
            }
        }
    }

    fun openInstallLog(id: Long, onJob: (Long) -> Unit) {
        viewModelScope.launch {
            when (val r = hostsRepo.latestInstallJob(id)) {
                is Outcome.Success -> onJob(r.value)
                is Outcome.Failure -> message = r.error.text()
            }
        }
    }

    /** [purge] — also remove nkt from the host itself. */
    fun delete(host: HubHost, purge: Boolean) = call(t("${host.name} удалён", "${host.name} deleted")) { hostsRepo.delete(host.id, purge) }
    fun setGroup(host: HubHost, group: String) = call(t("Группа изменена", "Group changed")) { hostsRepo.setGroup(host.id, group) }
    fun createGroup(name: String) = call(t("Группа создана", "Group created")) { hostsRepo.createGroup(name) }
    fun renameGroup(name: String, to: String) = call(t("Группа переименована", "Group renamed")) { hostsRepo.renameGroup(name, to) }

    /** Hosts of a deleted group move to «Без группы». */
    fun deleteGroup(name: String) = call(t("Группа удалена", "Group deleted")) { hostsRepo.deleteGroup(name) }

    private fun call(ok: String, block: suspend () -> Outcome<Unit>) {
        viewModelScope.launch {
            message = when (val r = block()) {
                is Outcome.Success -> ok
                is Outcome.Failure -> failedText(r.error)
            }
            loadGroups()
            refresh()
        }
    }
}
