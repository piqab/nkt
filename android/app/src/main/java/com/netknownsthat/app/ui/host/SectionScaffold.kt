package com.netknownsthat.app.ui.host

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Card
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp

/**
 * Renders the three states every read-only section shares, so each screen
 * only has to describe what its data looks like.
 *
 * Stale data outlives a failed refresh deliberately: a section that already
 * has something worth reading should keep showing it when the host briefly
 * goes unreachable, rather than blanking out.
 */
@Composable
fun <T> SectionContent(
    state: SectionState<T>,
    emptyText: String = "Пусто",
    isEmpty: (T) -> Boolean = { false },
    content: @Composable (T) -> Unit,
) {
    val data = state.data
    Box(modifier = Modifier.fillMaxSize()) {
        when {
            data == null && state.loading ->
                CircularProgressIndicator(modifier = Modifier.align(Alignment.Center))

            data == null ->
                Text(
                    text = state.error ?: emptyText,
                    color = if (state.error != null) MaterialTheme.colorScheme.error
                    else MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.align(Alignment.Center).padding(24.dp),
                )

            isEmpty(data) ->
                Text(
                    text = emptyText,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.align(Alignment.Center).padding(24.dp),
                )

            else -> Column(modifier = Modifier.fillMaxSize()) {
                state.error?.let { StaleWarning(it) }
                content(data)
            }
        }
    }
}

/** Shown above data that is still on screen after a refresh failed — the
 * numbers below it are real but no longer current, which is worth saying. */
@Composable
private fun StaleWarning(message: String) {
    Card(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = 16.dp, vertical = 8.dp),
    ) {
        Text(
            text = "Не удалось обновить: $message",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.error,
            modifier = Modifier.padding(12.dp),
        )
    }
}

/** One "label — value" line, the unit most of these screens are built from. */
@Composable
fun LabeledValue(label: String, value: String) {
    Column(modifier = Modifier.padding(vertical = 4.dp)) {
        Text(
            text = label,
            style = MaterialTheme.typography.labelMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Text(text = value, style = MaterialTheme.typography.bodyMedium)
    }
}
