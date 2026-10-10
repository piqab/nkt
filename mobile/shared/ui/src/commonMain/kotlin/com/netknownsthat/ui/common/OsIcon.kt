package com.netknownsthat.ui.common

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.luminance
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.graphics.vector.PathParser
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.netknownsthat.domain.model.OsInfo
import com.netknownsthat.ui.i18n.t

// ID без своего значка → значок родственной ОС (как в вебе, OsIcon.tsx).
private val ALIASES = mapOf("amzn" to "fedora", "ol" to "rhel", "centos-stream" to "centos", "kali-rolling" to "kali", "pop-os" to "pop")

private fun glyphFor(os: OsInfo?): OsGlyph? {
    if (os == null || os.id.isBlank()) return null
    val id = ALIASES[os.id] ?: os.id
    return OS_GLYPHS[id]
        ?: os.like.takeIf { it.isNotBlank() }?.let { OS_GLYPHS[ALIASES[it] ?: it] }
        ?: if (id == "windows") null else OS_GLYPHS["linux"]
}

private val vectors = mutableMapOf<String, ImageVector>()

private fun vectorOf(g: OsGlyph): ImageVector = vectors.getOrPut(g.path) {
    ImageVector.Builder(defaultWidth = 24.dp, defaultHeight = 24.dp, viewportWidth = 24f, viewportHeight = 24f)
        .addPath(pathData = PathParser().parsePathString(g.path).toNodes(), fill = SolidColor(Color.Black))
        .build()
}

/**
 * Значок ОС перед именем хоста, машины или контейнера — фирменного цвета
 * (слишком тёмный на тёмной теме — цвета текста). Догадка (Hyper-V-флаги) —
 * полупрозрачный с «?»; ОС неизвестна — «?».
 */
@Composable
fun OsIcon(os: OsInfo?, size: Dp = 16.dp, modifier: Modifier = Modifier) {
    val glyph = glyphFor(os)
    val guess = os?.source == "guess"
    val name = os?.name?.ifBlank { null } ?: glyph?.title ?: t("ОС неизвестна", "OS unknown")
    val label = if (guess) t("вероятно $name", "probably $name") else name
    val dark = MaterialTheme.colorScheme.background.luminance() < 0.5f
    Box(modifier.padding(end = 6.dp).semantics { contentDescription = label }) {
        if (glyph == null) {
            Text("?", fontSize = (size.value * 0.9f).sp, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.size(size), maxLines = 1)
            return@Box
        }
        val brand = Color(glyph.color)
        val tint = if (dark && brand.luminance() < 0.12f) MaterialTheme.colorScheme.onSurface else brand
        val vector = remember(glyph.path) { vectorOf(glyph) }
        Icon(vector, contentDescription = null, tint = tint, modifier = Modifier.size(size).alpha(if (guess) 0.5f else 1f))
        if (guess) {
            Text("?", fontSize = (size.value * 0.6f).sp, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.align(Alignment.BottomEnd))
        }
    }
}
