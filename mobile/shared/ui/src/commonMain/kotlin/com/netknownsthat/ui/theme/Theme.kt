package com.netknownsthat.ui.theme

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.ColorScheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color

// A dark surface is nkt's own default look (the web dashboard's palette
// mirrors it too); the brand colours are the same on every platform.
private val DarkColors = darkColorScheme(
    primary = Color(0xFF7FB0FF),
    secondary = Color(0xFF9ED3B8),
)
private val LightColors = lightColorScheme(
    primary = Color(0xFF18539E),
    secondary = Color(0xFF1B6B4B),
)

/** The platform's own palette when it has one (Android 12+ wallpaper
 * colours); null — use nkt's. */
@Composable
expect fun platformColorScheme(dark: Boolean): ColorScheme?

@Composable
fun NktTheme(
    darkTheme: Boolean = isSystemInDarkTheme(),
    content: @Composable () -> Unit,
) {
    val scheme = platformColorScheme(darkTheme) ?: if (darkTheme) DarkColors else LightColors
    MaterialTheme(colorScheme = scheme, content = content)
}
