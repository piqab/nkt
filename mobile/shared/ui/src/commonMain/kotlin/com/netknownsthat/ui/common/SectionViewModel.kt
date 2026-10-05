package com.netknownsthat.ui.common

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.netknownsthat.domain.common.Outcome
import com.netknownsthat.domain.usecase.ActionResult
import com.netknownsthat.domain.usecase.AwaitSettledUseCase
import com.netknownsthat.ui.i18n.t
import kotlinx.coroutines.launch

/** What every read-only section needs: loading, the data, the last error. */
data class SectionState<T>(
    val loading: Boolean = false,
    val data: T? = null,
    val error: String? = null,
)

/**
 * The fetch-and-store plumbing of a screen showing one piece of host or hub
 * state, plus actions on it. Subclasses provide [fetch] (a repository or use
 * case call — never data-layer code). Stale data outlives a failed refresh:
 * a section that has something worth reading keeps showing it.
 */
abstract class SectionViewModel<T> : ViewModel() {
    var state by mutableStateOf(SectionState<T>())
        private set

    /** Result of the last action, for a snackbar; null once shown. */
    var actionMessage by mutableStateOf<String?>(null)

    /** An action is in flight — buttons are disabled meanwhile. */
    var actionInProgress by mutableStateOf(false)
        private set

    /** Item whose action is still settling (its row shows a spinner). */
    var pendingKey by mutableStateOf<String?>(null)
        private set

    protected abstract suspend fun fetch(): Outcome<T>

    fun load() {
        viewModelScope.launch {
            state = state.copy(loading = true, error = null)
            state = when (val r = fetch()) {
                is Outcome.Success -> state.copy(loading = false, data = r.value)
                is Outcome.Failure -> state.copy(loading = false, error = r.error.text())
            }
        }
    }

    /** Runs a mutating call and refetches, so the screen shows what the host
     * ended up in rather than what was asked for. */
    protected fun act(okMessage: String, call: suspend () -> Outcome<*>) {
        if (actionInProgress) return
        viewModelScope.launch {
            actionInProgress = true
            actionMessage = when (val r = call()) {
                is Outcome.Success -> okMessage
                is Outcome.Failure -> failedText(r.error)
            }
            actionInProgress = false
            load()
        }
    }

    /** Like [act], but keeps [key]'s spinner until [settled] says the host
     * reached the asked-for state (see AwaitSettledUseCase). */
    protected fun actAwaiting(
        key: String,
        okMessage: String,
        settled: (T) -> Boolean,
        call: suspend () -> Outcome<Unit>,
    ) {
        if (actionInProgress) return
        viewModelScope.launch {
            actionInProgress = true
            pendingKey = key
            val result = AwaitSettledUseCase()(
                action = call,
                fetch = { fetch() },
                onFetched = { state = state.copy(loading = false, data = it, error = null) },
                settled = settled,
            )
            actionMessage = when (result) {
                ActionResult.Settled -> okMessage
                is ActionResult.NotSettled -> t(
                    "$okMessage — но состояние не изменилось за ${result.waited.inWholeSeconds} с",
                    "$okMessage — but the state did not change within ${result.waited.inWholeSeconds} s",
                )
                is ActionResult.Failed -> failedText(result.error)
            }
            pendingKey = null
            actionInProgress = false
            if (result is ActionResult.Failed) load()
        }
    }
}
