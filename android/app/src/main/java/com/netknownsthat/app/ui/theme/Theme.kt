package com.netknownsthat.app.ui.theme

import android.os.Build
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.dynamicDarkColorScheme
import androidx.compose.material3.dynamicLightColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.platform.LocalContext

// A dark surface is nkt's own default look across every other surface (the
// web dashboard's own palette mirrors it too — see internal/tui/theme.go's
// comment on the same reasoning) — this app follows suit rather than
// picking an unrelated brand color from scratch.
private val DarkColors = darkColorScheme(
    primary = androidx.compose.ui.graphics.Color(0xFF7FB0FF),
    secondary = androidx.compose.ui.graphics.Color(0xFF9ED3B8),
)
private val LightColors = lightColorScheme(
    primary = androidx.compose.ui.graphics.Color(0xFF18539E),
    secondary = androidx.compose.ui.graphics.Color(0xFF1B6B4B),
)

@Composable
fun NktTheme(
    darkTheme: Boolean = isSystemInDarkTheme(),
    dynamicColor: Boolean = true,
    content: @Composable () -> Unit,
) {
    val colorScheme = when {
        dynamicColor && Build.VERSION.SDK_INT >= Build.VERSION_CODES.S -> {
            val context = LocalContext.current
            if (darkTheme) dynamicDarkColorScheme(context) else dynamicLightColorScheme(context)
        }
        darkTheme -> DarkColors
        else -> LightColors
    }
    MaterialTheme(colorScheme = colorScheme, content = content)
}
