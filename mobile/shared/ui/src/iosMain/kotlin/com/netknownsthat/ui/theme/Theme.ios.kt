package com.netknownsthat.ui.theme

import androidx.compose.material3.ColorScheme
import androidx.compose.runtime.Composable

/** iOS has no wallpaper palette — nkt's own colours. */
@Composable
actual fun platformColorScheme(dark: Boolean): ColorScheme? = null
