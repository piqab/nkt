package com.netknownsthat.ui.host

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.produceState
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.netknownsthat.domain.repository.SessionRepository
import com.netknownsthat.domain.repository.WebSession
import com.netknownsthat.ui.common.FullScreen
import com.netknownsthat.ui.i18n.t
import com.netknownsthat.ui.platform.HubWebView
import org.koin.compose.koinInject

/** Names that go into the page address as they are (libvirt, LXD). */
private val SCREEN_NAME = Regex("^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$")

/** Whether [name] can be opened on the screen page at all. */
fun screenNameOk(name: String) = SCREEN_NAME.matches(name)

/**
 * A machine's screen (VNC or SPICE) on the whole display: the hub's own
 * screen page (/screen/popout) in an embedded browser, signed in with the
 * app's session — the same noVNC and spice-html5 the web interface uses.
 * [kind]: "vm" (libvirt) or "lxd"; [proto]: "vnc" or "spice".
 */
@Composable
fun VmScreen(hostId: Long, kind: String, name: String, proto: String) {
    FullScreen()
    val sessions = koinInject<SessionRepository>()
    val session by produceState<WebSession?>(null) { value = sessions.webSession() }
    // lang: the page speaks the app's language, not the phone's.
    val path = "/screen/popout?host=$hostId&kind=$kind&name=$name&proto=$proto&lang=${com.netknownsthat.ui.i18n.I18n.lang.code}"
    Box(Modifier.fillMaxSize()) {
        when {
            !screenNameOk(name) -> Text(t("Неверное имя машины", "Invalid machine name"), color = MaterialTheme.colorScheme.error, modifier = Modifier.padding(24.dp))
            session == null -> CircularProgressIndicator(Modifier.align(Alignment.Center))
            else -> HubWebView(session!!, path, Modifier.fillMaxSize())
        }
    }
}
