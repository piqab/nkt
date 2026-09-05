package com.netknownsthat.app.ui.theme

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.ReadOnlyComposable
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp
import com.netknownsthat.app.status.HealthStatus

/**
 * Fixed colours for status, on purpose.
 *
 * The rest of the app uses Material's dynamic colour, which derives the
 * palette from the user's wallpaper — perfectly good for chrome, and wrong
 * for meaning: on a device with a warm wallpaper `colorScheme.primary` is
 * itself red-ish, so painting "running" with it would show a healthy service
 * in the colour of a broken one. Green/amber/red are the point here, not the
 * brand.
 *
 * Two variants because a colour legible on white is not legible on near-black.
 */
private val OkLight = Color(0xFF2E7D32)
private val OkDark = Color(0xFF6FCF80)
private val WarnLight = Color(0xFFB26A00)
private val WarnDark = Color(0xFFE0A030)
private val BadLight = Color(0xFFC62828)
private val BadDark = Color(0xFFF28B82)

@Composable
@ReadOnlyComposable
fun statusColor(status: HealthStatus): Color {
    val dark = MaterialTheme.colorScheme.background.luminance() < 0.5f
    return when (status) {
        HealthStatus.OK -> if (dark) OkDark else OkLight
        HealthStatus.WARN -> if (dark) WarnDark else WarnLight
        HealthStatus.BAD -> if (dark) BadDark else BadLight
        HealthStatus.UNKNOWN -> MaterialTheme.colorScheme.onSurfaceVariant
    }
}

private fun Color.luminance(): Float = 0.299f * red + 0.587f * green + 0.114f * blue

/**
 * The status marker that precedes an item's name: a coloured dot, or a
 * spinner while an action on that item is still settling.
 */
@Composable
fun StatusDot(
    status: HealthStatus,
    busy: Boolean = false,
    modifier: Modifier = Modifier,
) {
    if (busy) {
        CircularProgressIndicator(
            strokeWidth = 2.dp,
            modifier = modifier.padding(end = 8.dp).size(10.dp),
        )
        return
    }
    Box(
        modifier = modifier
            .padding(end = 8.dp)
            .size(10.dp)
            .background(statusColor(status), CircleShape)
    )
}
