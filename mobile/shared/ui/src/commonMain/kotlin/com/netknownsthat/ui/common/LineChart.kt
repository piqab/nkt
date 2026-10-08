package com.netknownsthat.ui.common

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.gestures.awaitEachGesture
import androidx.compose.foundation.gestures.awaitFirstDown
import androidx.compose.foundation.gestures.detectHorizontalDragGestures
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.PathEffect
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.input.pointer.PointerEventPass
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.dp
import com.netknownsthat.ui.i18n.t

/** One point of a line: [x] is the bucket's time as the hub sent it. */
data class ChartPoint(val x: String, val y: Double)

/** One line; [color] — null takes the next colour of [SERIES_COLORS]. */
data class ChartSeries(val name: String, val points: List<ChartPoint>, val color: Color? = null)

/** Distinct series colours that read on both light and dark backgrounds. */
val SERIES_COLORS = listOf(
    Color(0xFF3B82F6), Color(0xFFF59E0B), Color(0xFF10B981), Color(0xFFEF4444),
    Color(0xFF8B5CF6), Color(0xFF06B6D4), Color(0xFFEC4899), Color(0xFF84CC16),
)

/**
 * Touches anywhere in the app, counted. A chart's readout stays after the
 * finger is lifted and goes away at the next touch outside the chart: the
 * chart remembers the count its own touch had, and a later touch elsewhere
 * moves the count on. The counter sees every touch first (the Initial pass)
 * and consumes nothing, so lists still scroll and buttons still click.
 */
class ChartTouches {
    var count by mutableIntStateOf(0)
}

val LocalChartTouches = staticCompositionLocalOf { ChartTouches() }

/** Put on the app's root: counts every touch for [ChartTouches]. */
fun Modifier.countChartTouches(touches: ChartTouches): Modifier = pointerInput(touches) {
    awaitEachGesture {
        awaitFirstDown(requireUnconsumed = false, pass = PointerEventPass.Initial)
        touches.count++
    }
}

/**
 * A line chart with a readout: touching or sliding a finger along it marks
 * the nearest bucket and shows its time and every line's value there.
 * Only a sideways slide is taken — an up or down one still scrolls the page
 * the chart sits in.
 */
@Composable
fun LineChart(
    series: List<ChartSeries>,
    formatValue: (Double) -> String,
    modifier: Modifier = Modifier,
    formatX: (String) -> String = { it },
    height: Dp = 160.dp,
    /** Top of the scale; null — the largest value. */
    yMax: Double? = null,
    /** A horizontal line for a limit (memory size, say). */
    reference: Double? = null,
    /** Shade under the first line. */
    area: Boolean = false,
) {
    val xs = remember(series) { series.flatMap { s -> s.points.map { it.x } }.distinct().sorted() }
    if (xs.isEmpty() || series.all { it.points.isEmpty() }) {
        Text(t("Данных пока нет", "No data yet"), style = MaterialTheme.typography.bodySmall, modifier = modifier)
        return
    }
    val colors = series.mapIndexed { i, s -> s.color ?: SERIES_COLORS[i % SERIES_COLORS.size] }
    val byX = remember(series) { series.map { s -> s.points.associate { it.x to it.y } } }
    val top = remember(series, yMax, reference) {
        (yMax ?: maxOf(series.maxOf { s -> s.points.maxOfOrNull { it.y } ?: 0.0 }, reference ?: 0.0)).coerceAtLeast(1e-9)
    }
    val touches = LocalChartTouches.current
    var selected by remember(xs) { mutableStateOf<Int?>(null) }
    var selectedAt by remember { mutableIntStateOf(-1) }
    val shown = selected?.takeIf { selectedAt == touches.count }
    val axis = MaterialTheme.colorScheme.outlineVariant
    val marker = MaterialTheme.colorScheme.onSurface

    Column(modifier = modifier) {
        Text(t("макс ${formatValue(top)}", "max ${formatValue(top)}"), style = MaterialTheme.typography.labelSmall)
        BoxWithConstraints(modifier = Modifier.fillMaxWidth().height(height).padding(vertical = 4.dp)) {
            val widthPx = with(LocalDensity.current) { maxWidth.toPx() }
            val step = if (xs.size > 1) widthPx / (xs.size - 1) else 0f
            fun indexAt(x: Float): Int = if (step == 0f) 0 else (x / step).let { kotlin.math.round(it).toInt() }.coerceIn(0, xs.lastIndex)
            val pick: (Float) -> Unit = { x ->
                selected = indexAt(x)
                selectedAt = touches.count
            }
            Canvas(
                modifier = Modifier
                    .fillMaxWidth()
                    .height(height)
                    .pointerInput(xs) { detectTapGestures(onTap = { pick(it.x) }) }
                    .pointerInput(xs) {
                        detectHorizontalDragGestures(
                            onDragStart = { pick(it.x) },
                            onHorizontalDrag = { change, _ -> change.consume(); pick(change.position.x) },
                        )
                    },
            ) {
                val h = size.height
                fun yOf(v: Double) = h - (v / top * h).toFloat().coerceIn(0f, h)
                drawLine(axis, Offset(0f, h), Offset(size.width, h))
                reference?.let { r ->
                    drawLine(axis, Offset(0f, yOf(r)), Offset(size.width, yOf(r)), strokeWidth = 1.dp.toPx(), pathEffect = PathEffect.dashPathEffect(floatArrayOf(8f, 6f)))
                }
                series.forEachIndexed { i, _ ->
                    val values = byX[i]
                    val path = Path()
                    var started = false
                    var firstX = 0f
                    var lastX = 0f
                    var any = false
                    xs.forEachIndexed { xi, x ->
                        val v = values[x] ?: run { started = false; return@forEachIndexed }
                        val pt = Offset(xi * step, yOf(v))
                        if (started) path.lineTo(pt.x, pt.y) else path.moveTo(pt.x, pt.y)
                        if (!any) { firstX = pt.x; any = true }
                        lastX = pt.x
                        started = true
                    }
                    if (area && i == 0) {
                        val fill = Path().apply { addPath(path); lineTo(lastX, h); lineTo(firstX, h); close() }
                        drawPath(fill, colors[i].copy(alpha = 0.15f))
                    }
                    drawPath(path, colors[i], style = Stroke(width = 2.dp.toPx()))
                }
                shown?.let { idx ->
                    val x = idx * step
                    drawLine(marker.copy(alpha = 0.6f), Offset(x, 0f), Offset(x, h), strokeWidth = 1.dp.toPx())
                    series.indices.forEach { i ->
                        byX[i][xs[idx]]?.let { v -> drawCircle(colors[i], radius = 4.dp.toPx(), center = Offset(x, yOf(v))) }
                    }
                }
            }
            shown?.let { idx -> Readout(idx * step, widthPx, formatX(xs[idx]), series.indices.map { i -> Triple(series[i].name, colors[i], byX[i][xs[idx]]) }, formatValue) }
        }
        Row {
            Text(formatX(xs.first()), style = MaterialTheme.typography.labelSmall, modifier = Modifier.weight(1f))
            Text(formatX(xs.last()), style = MaterialTheme.typography.labelSmall)
        }
        if (series.size > 1) {
            FlowRow(modifier = Modifier.padding(top = 4.dp)) {
                series.forEachIndexed { i, s -> Legend(s.name, colors[i]) }
            }
        }
    }
}

/** The values at the marked bucket, on the side of the line with room. */
@Composable
private fun Readout(lineX: Float, widthPx: Float, title: String, rows: List<Triple<String, Color, Double?>>, formatValue: (Double) -> String) {
    var boxWidth by remember { mutableIntStateOf(0) }
    val gap = with(LocalDensity.current) { 8.dp.toPx() }
    val left = if (lineX + gap + boxWidth <= widthPx) lineX + gap else (lineX - gap - boxWidth).coerceAtLeast(0f)
    Surface(
        shape = RoundedCornerShape(6.dp),
        color = MaterialTheme.colorScheme.inverseSurface,
        contentColor = MaterialTheme.colorScheme.inverseOnSurface,
        modifier = Modifier
            .offset { IntOffset(left.toInt(), 0) }
            .widthIn(max = 220.dp)
            .onSizeChanged { boxWidth = it.width },
    ) {
        Column(modifier = Modifier.padding(horizontal = 8.dp, vertical = 4.dp)) {
            Text(title, style = MaterialTheme.typography.labelSmall)
            rows.forEach { (name, color, v) ->
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Box(Modifier.size(8.dp).background(color, CircleShape))
                    Text(" $name: ${v?.let(formatValue) ?: "—"}", style = MaterialTheme.typography.labelSmall)
                }
            }
        }
    }
}

@Composable
private fun Legend(name: String, color: Color) {
    Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.padding(end = 10.dp)) {
        Box(Modifier.size(8.dp).background(color, CircleShape))
        Text(" $name", style = MaterialTheme.typography.labelSmall)
    }
}
