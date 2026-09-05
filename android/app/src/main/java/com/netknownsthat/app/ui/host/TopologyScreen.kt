package com.netknownsthat.app.ui.host

import androidx.compose.foundation.background
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.gestures.detectTransformGestures
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Card
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.drawscope.scale
import androidx.compose.ui.graphics.drawscope.translate
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.text.TextMeasurer
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.drawText
import androidx.compose.ui.text.rememberTextMeasurer
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.netknownsthat.app.net.model.TopologyNode
import com.netknownsthat.app.net.model.TopologyResponse
import androidx.compose.foundation.Canvas
import kotlin.math.cos
import kotlin.math.hypot
import kotlin.math.sin

/**
 * The resource map, drawn natively rather than ported from the web SVG.
 *
 * The web version is built entirely on mouse events (wheel zoom, drag,
 * hover), none of which exist on a touch screen — so this is pinch/drag/tap
 * from the start, which is also why the layout is computed here rather than
 * reusing coordinates the server does not send anyway.
 *
 * Layout is a simple ring per node kind: with no positions in the API and no
 * force simulation running on a phone, concentric rings at least group
 * related things together and stay stable between refreshes (the order comes
 * from the response, which is itself stable).
 */
@Composable
fun TopologyScreen(viewModel: TopologyViewModel) {
    SectionContent(
        state = viewModel.state,
        emptyText = "Карта пуста",
        isEmpty = { it.nodes.isEmpty() },
    ) { topology ->
        var scale by remember { mutableFloatStateOf(1f) }
        var offset by remember { mutableStateOf(Offset.Zero) }
        var selected by remember { mutableStateOf<TopologyNode?>(null) }

        val positions = remember(topology) { layout(topology) }
        val measurer = rememberTextMeasurer()
        val surface = MaterialTheme.colorScheme.surfaceVariant
        val onSurface = MaterialTheme.colorScheme.onSurface
        val edgeColor = MaterialTheme.colorScheme.outline
        val errorColor = MaterialTheme.colorScheme.error
        val okColor = MaterialTheme.colorScheme.primary

        Box(modifier = Modifier.fillMaxSize()) {
            Canvas(
                modifier = Modifier
                    .fillMaxSize()
                    .background(MaterialTheme.colorScheme.surface)
                    .pointerInput(topology) {
                        detectTransformGestures { _, pan, zoom, _ ->
                            scale = (scale * zoom).coerceIn(0.3f, 4f)
                            offset += pan
                        }
                    }
                    .pointerInput(topology, positions) {
                        detectTapGestures { tap ->
                            // Undo the pan/zoom to compare in layout space,
                            // so hit-testing keeps working after a gesture.
                            val canvasCenter = Offset(size.width / 2f, size.height / 2f)
                            val point = (tap - offset - canvasCenter) / scale
                            selected = positions.entries
                                .firstOrNull { hypot(
                                    point.x - it.value.x,
                                    point.y - it.value.y,
                                ) < NODE_RADIUS }
                                ?.let { entry -> topology.nodes.find { it.id == entry.key } }
                        }
                    },
            ) {
                withTransform(offset, scale) {
                    topology.edges.forEach { edge ->
                        val from = positions[edge.from] ?: return@forEach
                        val to = positions[edge.to] ?: return@forEach
                        drawLine(
                            color = edgeColor.copy(alpha = 0.5f),
                            start = from,
                            end = to,
                            strokeWidth = 1.5f,
                        )
                    }
                    topology.nodes.forEach { node ->
                        val position = positions[node.id] ?: return@forEach
                        drawCircle(
                            color = when {
                                node.findings > 0 -> errorColor
                                node.status == "ok" || node.status == "running" -> okColor
                                else -> surface
                            },
                            radius = NODE_RADIUS,
                            center = position,
                        )
                        drawCircle(
                            color = onSurface.copy(alpha = 0.4f),
                            radius = NODE_RADIUS,
                            center = position,
                            style = Stroke(width = 1f),
                        )
                        if (scale > 0.7f) {
                            label(measurer, node.label, position, onSurface)
                        }
                    }
                }
            }

            selected?.let { node ->
                Card(
                    modifier = Modifier
                        .align(Alignment.BottomCenter)
                        .fillMaxWidth()
                        .padding(16.dp),
                ) {
                    Column(modifier = Modifier.padding(16.dp)) {
                        Text(node.label, style = MaterialTheme.typography.titleSmall)
                        Text(
                            text = listOfNotNull(
                                node.kind.takeIf { it.isNotBlank() },
                                node.status.takeIf { it.isNotBlank() },
                            ).joinToString(" · "),
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                        if (node.findings > 0) {
                            Text(
                                text = "Проблем: ${node.findings}",
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.error,
                            )
                        }
                        val related = topology.findings.filter { it.nodeId == node.id }
                        related.forEach {
                            Text(
                                text = "• ${it.title}",
                                style = MaterialTheme.typography.bodySmall,
                                modifier = Modifier.padding(top = 4.dp),
                            )
                        }
                    }
                }
            }

            Text(
                text = "Узлов: ${topology.nodes.size} · связей: ${topology.edges.size}. " +
                    "Щипок — масштаб, перетаскивание — сдвиг, касание — узел.",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.align(Alignment.TopStart).padding(12.dp),
            )
        }
    }
}

private const val NODE_RADIUS = 14f

private fun DrawScope.withTransform(offset: Offset, scale: Float, body: DrawScope.() -> Unit) {
    translate(offset.x + center.x, offset.y + center.y) {
        scale(scale, Offset.Zero) { body() }
    }
}

private fun DrawScope.label(
    measurer: TextMeasurer,
    text: String,
    at: Offset,
    color: Color,
) {
    val layout = measurer.measure(
        text = text.take(18),
        style = TextStyle(fontSize = 9.sp, color = color),
    )
    drawText(
        textLayoutResult = layout,
        topLeft = Offset(at.x - layout.size.width / 2f, at.y + NODE_RADIUS + 2f),
    )
}

/**
 * One concentric ring per node kind, ordered so the host sits innermost and
 * the internet outermost — the same inside-out reading the web map has,
 * without needing coordinates the API does not provide.
 */
private fun layout(topology: TopologyResponse): Map<String, Offset> {
    val order = listOf(
        "host", "service", "endpoint", "container", "podman_container",
        "lxd_instance", "vm", "network", "backend", "upstream", "undeclared", "internet",
    )
    val byKind = topology.nodes.groupBy { it.kind }
    val kinds = byKind.keys.sortedBy { kind ->
        order.indexOf(kind).let { if (it < 0) order.size else it }
    }

    val positions = mutableMapOf<String, Offset>()
    kinds.forEachIndexed { ring, kind ->
        val nodes = byKind.getValue(kind)
        val radius = 90f + ring * 85f
        nodes.forEachIndexed { index, node ->
            val angle = (2 * Math.PI * index / nodes.size).toFloat()
            positions[node.id] = Offset(radius * cos(angle), radius * sin(angle))
        }
    }
    return positions
}
