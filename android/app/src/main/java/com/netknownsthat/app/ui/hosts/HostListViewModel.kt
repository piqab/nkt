package com.netknownsthat.app.ui.hosts

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.netknownsthat.app.net.HubClient
import com.netknownsthat.app.net.model.AddHostResponse
import com.netknownsthat.app.net.model.HostGroupsResponse
import com.netknownsthat.app.net.model.HubEventsResponse
import com.netknownsthat.app.net.model.InstallJobResponse
import kotlinx.serialization.json.JsonObjectBuilder
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import com.netknownsthat.app.net.model.HubHost
import kotlinx.coroutines.launch

data class HostListUiState(
    val loading: Boolean = true,
    val hosts: List<HubHost> = emptyList(),
    val error: String? = null,
    /** Unread hub alerts, for the bell. */
    val unread: Int = 0,
)

/**
 * Phase 1 scope only: list + open. Add/edit/delete/install-log/export-import
 * (the plan's phase 7 — by far the most complex single screen, see the
 * plan's own "Топ-6" ranking) come later, layered onto this same
 * ViewModel/screen rather than a rewrite.
 */
class HostListViewModel(private val hubClient: HubClient) : ViewModel() {
    var uiState by mutableStateOf(HostListUiState())
        private set

    fun refresh() {
        viewModelScope.launch {
            uiState = uiState.copy(loading = true, error = null)
            when (val result = hubClient.get<List<HubHost>>("/hub/hosts")) {
                is HubClient.ApiResult.Success ->
                    uiState = uiState.copy(loading = false, hosts = result.value)
                is HubClient.ApiResult.Failure ->
                    uiState = uiState.copy(loading = false, error = result.message)
            }
            val events = hubClient.get<HubEventsResponse>("/hub/events?limit=1")
            if (events is HubClient.ApiResult.Success) uiState = uiState.copy(unread = events.value.unread)
        }
    }

    /**
     * Selects a host by id for a jump from an alert, a monitoring insight or
     * a notification — the list may not be loaded yet (cold start from a
     * notification), so it is fetched when needed. Null if the host is gone.
     */
    suspend fun selectById(id: Long): HubHost? {
        var host = uiState.hosts.firstOrNull { it.id == id }
        if (host == null) {
            val result = hubClient.get<List<HubHost>>("/hub/hosts")
            if (result is HubClient.ApiResult.Success) {
                uiState = uiState.copy(hosts = result.value)
                host = result.value.firstOrNull { it.id == id }
            }
        }
        host?.let(::select)
        return host
    }

    /** The host whose sections are open, kept here so the host screen can
     * title itself and refetch when the selection changes. */
    var selectedHost by mutableStateOf<HubHost?>(null)
        private set

    /** Selecting a host scopes every subsequent API call to it (see
     * HostScope) — screens from phase 2 onward read hubClient.hostScope to
     * know which host's overview/findings/etc. to show. */
    fun select(host: HubHost) {
        selectedHost = host
        hubClient.hostScope.select(host.id)
    }

    /** Back to hub scope: hub-level screens ("О системе", this list itself)
     * must not inherit the host that happened to be open last. */
    fun deselectHost() {
        selectedHost = null
        hubClient.hostScope.select(null)
    }

    // ---- Host management (admin) — the web UI's «Хосты» actions. ----

    /** Result of the last management action, for a snackbar. */
    var message by mutableStateOf<String?>(null)

    var groups by mutableStateOf<List<String>>(emptyList())
        private set

    /** A key the operator must add on a new host (auth "generated"). */
    var pendingKey by mutableStateOf<Pair<HubHost?, String>?>(null)

    /** Set when the host already has a foreign nkt: the install waits for
     * an explicit «переустановить поверх» (?force=true). */
    var foreignInstall by mutableStateOf<Pair<Long, String>?>(null)

    fun loadGroups() {
        viewModelScope.launch {
            (hubClient.get<HostGroupsResponse>("/hub/groups") as? HubClient.ApiResult.Success)
                ?.let { groups = it.value.groups }
        }
    }

    fun addHost(form: AddHostForm, install: Boolean, onJob: (Long) -> Unit) {
        viewModelScope.launch {
            val body = buildJsonObject {
                put("name", form.name.trim())
                put("addr", form.addr.trim())
                put("ssh_port", form.port)
                put("ssh_user", form.user.trim())
                put("auth_kind", form.authKind)
                if (form.authKind != "generated") put("secret", form.secret)
                put("group", form.group.trim())
                put("terminal_enabled", form.terminal)
            }.toString()
            when (val r = hubClient.post<AddHostResponse>("/hub/hosts", body)) {
                is HubClient.ApiResult.Failure -> message = "Не удалось: ${r.message}"
                is HubClient.ApiResult.Success -> {
                    refresh()
                    if (r.value.authorizedKey.isNotBlank()) {
                        // Nothing can be installed until the key is on the host.
                        pendingKey = null to r.value.authorizedKey
                        message = "Хост ${form.name} добавлен — добавьте ключ хаба на хост и нажмите «Установить nkt»"
                    } else if (install) {
                        install(r.value.id, false, onJob)
                    } else {
                        message = "Хост ${form.name} добавлен"
                    }
                }
            }
        }
    }

    fun install(id: Long, force: Boolean, onJob: (Long) -> Unit) {
        viewModelScope.launch {
            val path = "/hub/hosts/$id/install" + if (force) "?force=true" else ""
            when (val r = hubClient.post<InstallJobResponse>(path, "{}")) {
                is HubClient.ApiResult.Success -> {
                    onJob(r.value.job)
                    refresh()
                }
                is HubClient.ApiResult.Failure ->
                    if (r.httpCode == 409) foreignInstall = id to r.message
                    else message = "Не удалось: ${r.message}"
            }
        }
    }

    fun openInstallLog(id: Long, onJob: (Long) -> Unit) {
        viewModelScope.launch {
            when (val r = hubClient.get<InstallJobResponse>("/hub/hosts/$id/install/latest")) {
                is HubClient.ApiResult.Success -> onJob(r.value.job)
                is HubClient.ApiResult.Failure -> message = r.message
            }
        }
    }

    /** [purge] — also remove nkt from the host itself (service, data, the
     * hub's access) and give back password sign-in; otherwise only the
     * hub's record goes. */
    fun delete(host: HubHost, purge: Boolean) {
        viewModelScope.launch {
            val body = if (purge) buildJsonObject {
                put("service", true)
                put("data", true)
                put("access", true)
                put("restore_password", true)
            }.toString() else null
            message = when (val r = hubClient.delete<Unit>("/hub/hosts/${host.id}", body)) {
                is HubClient.ApiResult.Success -> "${host.name} удалён"
                is HubClient.ApiResult.Failure -> "Не удалось: ${r.message}"
            }
            refresh()
        }
    }

    fun setGroup(host: HubHost, group: String) = groupCall("/hub/hosts/${host.id}/group", "Группа изменена") {
        put("group", group.trim())
    }

    fun createGroup(name: String) = groupCall("/hub/groups", "Группа создана") { put("name", name.trim()) }

    fun renameGroup(name: String, to: String) = groupCall("/hub/groups/rename", "Группа переименована") {
        put("name", name)
        put("to", to.trim())
    }

    /** Hosts of a deleted group move to «Без группы». */
    fun deleteGroup(name: String) = groupCall("/hub/groups/delete", "Группа удалена") { put("name", name) }

    private fun groupCall(path: String, ok: String, body: JsonObjectBuilder.() -> Unit) {
        viewModelScope.launch {
            message = when (val r = hubClient.post<Unit>(path, buildJsonObject(body).toString())) {
                is HubClient.ApiResult.Success -> ok
                is HubClient.ApiResult.Failure -> "Не удалось: ${r.message}"
            }
            loadGroups()
            refresh()
        }
    }
}

/** The add-host form. auth_kind: "password", "key" (PEM private key in
 * [secret]) or "generated" (the hub makes a key pair and shows the public
 * line to add on the host). */
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
