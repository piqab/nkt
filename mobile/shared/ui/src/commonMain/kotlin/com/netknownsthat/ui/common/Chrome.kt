package com.netknownsthat.ui.common

import androidx.compose.foundation.layout.RowScope
import androidx.compose.material3.SnackbarHostState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.compositionLocalOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue

/**
 * What a screen can put into the shared shell (App.kt): buttons in the top
 * bar, messages in the snackbar, and whether the navigation bar should give
 * the screen the whole height (the terminal does).
 */
class Chrome {
    /** Top bar buttons, one entry per ScreenActions in composition — a
     * list, not one slot: with one slot a screen leaving after the next
     * one had put its buttons (a navigation transition) wiped them, and
     * two on one page (a section's refresh and the page's own "+")
     * replaced each other. */
    val actionEntries = mutableStateListOf<ActionsEntry>()
    var fullScreen by mutableStateOf(false)
    val snackbar = SnackbarHostState()
}

/** One ScreenActions' buttons. */
class ActionsEntry(val content: @Composable RowScope.() -> Unit)

val LocalChrome = compositionLocalOf { Chrome() }

/** Top bar buttons of the current screen, removed when it leaves. */
@Composable
fun ScreenActions(content: @Composable RowScope.() -> Unit) {
    val chrome = LocalChrome.current
    val current by rememberUpdatedState(content)
    DisposableEffect(chrome) {
        val entry = ActionsEntry { current() }
        chrome.actionEntries.add(entry)
        onDispose { chrome.actionEntries.remove(entry) }
    }
}

/** The screen wants the whole height (no navigation bar). */
@Composable
fun FullScreen() {
    val chrome = LocalChrome.current
    DisposableEffect(chrome) {
        chrome.fullScreen = true
        onDispose { chrome.fullScreen = false }
    }
}

/** Shows a section's action result in the snackbar, then clears it. */
@Composable
fun ActionMessages(viewModel: SectionViewModel<*>) {
    val chrome = LocalChrome.current
    LaunchedEffect(viewModel.actionMessage) {
        viewModel.actionMessage?.let {
            chrome.snackbar.showSnackbar(it)
            viewModel.actionMessage = null
        }
    }
}

/** Same for any message state. */
@Composable
fun SnackbarMessage(message: String?, onShown: () -> Unit) {
    val chrome = LocalChrome.current
    LaunchedEffect(message) {
        if (message != null) {
            chrome.snackbar.showSnackbar(message)
            onShown()
        }
    }
}
