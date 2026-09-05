package com.netknownsthat.app.ui.host

import androidx.compose.foundation.background
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.material3.Button
import androidx.compose.material3.FilterChip
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.TextMeasurer
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.rememberTextMeasurer
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.netknownsthat.app.terminal.TerminalEmulator
import com.netknownsthat.app.terminal.TerminalKeys
import com.netknownsthat.app.terminal.TerminalSession

private val TERMINAL_BACKGROUND = Color(0xFF12141A)
private val TERMINAL_FOREGROUND = Color(0xFFD8DEE9)
private val FONT_SIZE = 12.sp

/**
 * A live shell (or btop) on the host.
 *
 * Unlike every other screen this one cannot fall back to "show what the
 * server said" — it has to emulate a terminal locally, so the parser lives
 * in com.netknownsthat.app.terminal and is unit-tested; this file is only
 * the rendering and the on-screen keys a phone keyboard lacks.
 */
@Composable
fun TerminalScreen(viewModel: TerminalViewModel, btop: Boolean = false) {
    val session = viewModel.session

    DisposableEffect(btop) {
        viewModel.start(btop)
        onDispose { viewModel.stop() }
    }

    Column(modifier = Modifier.fillMaxSize().background(TERMINAL_BACKGROUND)) {
        Box(modifier = Modifier.weight(1f)) {
            if (session == null) {
                Text(
                    text = "Подключение…",
                    color = TERMINAL_FOREGROUND,
                    modifier = Modifier.align(Alignment.Center),
                )
            } else {
                TerminalCanvas(session)
                if (session.state == TerminalSession.State.FAILED ||
                    session.state == TerminalSession.State.CLOSED
                ) {
                    Column(
                        modifier = Modifier.align(Alignment.BottomCenter).padding(16.dp),
                    ) {
                        session.error?.let {
                            Text(
                                text = it,
                                color = MaterialTheme.colorScheme.error,
                                style = MaterialTheme.typography.bodySmall,
                            )
                        }
                        // The shell dies with the socket (unlike the install
                        // log endpoints, which can be resumed), so the only
                        // honest offer here is a fresh session.
                        Button(onClick = { viewModel.start(btop) }) {
                            Text("Новая сессия")
                        }
                    }
                }
            }
        }

        // btop is a viewer, not a shell: it takes no typing, so the keyboard
        // and key bar would only get in the way of the one thing it shows.
        if (session != null && !btop) {
            var ctrlArmed by remember { mutableStateOf(false) }
            KeyBar(
                session = session,
                ctrlArmed = ctrlArmed,
                onToggleCtrl = { ctrlArmed = !ctrlArmed },
            )
            HiddenInput(
                onTyped = { typed ->
                    if (ctrlArmed && typed.isNotEmpty()) {
                        session.send(TerminalKeys.ctrl(typed.first()))
                        if (typed.length > 1) session.sendText(typed.substring(1))
                        ctrlArmed = false
                    } else {
                        session.sendText(typed)
                    }
                },
            )
        }
    }
}

/**
 * Draws the emulator's grid. Reading `session.revision` inside the composable
 * is what ties the redraw to the emulator's own mutations, which Compose
 * cannot observe on its own.
 */
@Composable
private fun TerminalCanvas(session: TerminalSession) {
    val measurer = rememberTextMeasurer()
    val density = LocalDensity.current
    var sizePx by remember { mutableStateOf(Pair(0, 0)) }

    val charWidth = remember(measurer) { measureCharWidth(measurer) }
    val lineHeight = with(density) { (FONT_SIZE.toPx() * 1.25f) }

    // Recompute the grid whenever the view or the font metrics change, and
    // tell the server: a PTY that thinks it is 80x24 draws full-screen
    // programs at the wrong size forever otherwise.
    LaunchedEffect(sizePx, charWidth) {
        val (widthPx, heightPx) = sizePx
        if (widthPx > 0 && heightPx > 0 && charWidth > 0f) {
            val columns = (widthPx / charWidth).toInt().coerceIn(20, 400)
            val rows = (heightPx / lineHeight).toInt().coerceIn(5, 200)
            if (columns != session.emulator.columns || rows != session.emulator.rows) {
                session.sendResize(columns, rows)
            }
        }
    }

    val revision = session.revision
    Column(
        modifier = Modifier
            .fillMaxSize()
            .horizontalScroll(rememberScrollState())
            .padding(4.dp)
            .onSizeChanged { sizePx = it.width to it.height },
    ) {
        // revision is read here so the whole block recomposes on new output.
        @Suppress("UNUSED_EXPRESSION")
        revision
        session.emulator.visibleLines.forEachIndexed { index, line ->
            Text(
                text = line.toAnnotated(
                    cursorColumn = if (
                        session.emulator.cursorVisible && index == session.emulator.cursorRow
                    ) session.emulator.cursorColumn else -1,
                ),
                style = TextStyle(
                    fontFamily = FontFamily.Monospace,
                    fontSize = FONT_SIZE,
                    color = TERMINAL_FOREGROUND,
                ),
                softWrap = false,
            )
        }
    }
}

/** Groups adjacent cells that share styling into one span, so a line costs a
 * handful of spans rather than one per character. */
private fun TerminalEmulator.Line.toAnnotated(cursorColumn: Int): AnnotatedString =
    buildAnnotatedString {
        var index = 0
        while (index < chars.size) {
            var end = index + 1
            while (
                end < chars.size &&
                fg[end] == fg[index] &&
                bg[end] == bg[index] &&
                flags[end] == flags[index] &&
                end != cursorColumn &&
                index != cursorColumn
            ) end++

            val inverse = flags[index] and TerminalEmulator.FLAG_INVERSE != 0 ||
                (cursorColumn in index until end)
            val foreground = colorOf(fg[index], TERMINAL_FOREGROUND)
            val background = colorOf(bg[index], TERMINAL_BACKGROUND)

            withStyle(
                SpanStyle(
                    color = if (inverse) background else foreground,
                    background = if (inverse) foreground else background,
                    fontWeight = if (flags[index] and TerminalEmulator.FLAG_BOLD != 0)
                        FontWeight.Bold else FontWeight.Normal,
                    textDecoration = if (flags[index] and TerminalEmulator.FLAG_UNDERLINE != 0)
                        TextDecoration.Underline else null,
                )
            ) {
                append(String(chars, index, end - index))
            }
            index = end
        }
    }

private fun colorOf(value: Int, fallback: Color): Color =
    if (value == TerminalEmulator.DEFAULT_COLOR) fallback else Color(value)

private fun measureCharWidth(measurer: TextMeasurer): Float {
    // Measuring a run and dividing keeps rounding error from accumulating
    // into a column or two of drift on a wide screen.
    val sample = "0".repeat(50)
    val result = measurer.measure(
        text = AnnotatedString(sample),
        style = TextStyle(fontFamily = FontFamily.Monospace, fontSize = FONT_SIZE),
    )
    return result.size.width / sample.length.toFloat()
}

@Composable
private fun KeyBar(
    session: TerminalSession,
    ctrlArmed: Boolean,
    onToggleCtrl: () -> Unit,
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .horizontalScroll(rememberScrollState())
            .padding(horizontal = 8.dp, vertical = 4.dp),
    ) {
        // Ctrl is a modifier, not a key: it arms the next letter typed,
        // which is the only workable model on a touch keyboard.
        FilterChip(
            selected = ctrlArmed,
            onClick = onToggleCtrl,
            label = { Text("Ctrl") },
            modifier = Modifier.padding(end = 6.dp),
        )
        session.keyChip("Esc", TerminalKeys.ESC)
        session.keyChip("Tab", TerminalKeys.TAB)
        session.keyChip("↑", TerminalKeys.UP)
        session.keyChip("↓", TerminalKeys.DOWN)
        session.keyChip("←", TerminalKeys.LEFT)
        session.keyChip("→", TerminalKeys.RIGHT)
        session.keyChip("^C", TerminalKeys.ctrl('C'))
        session.keyChip("^D", TerminalKeys.ctrl('D'))
        session.keyChip("^Z", TerminalKeys.ctrl('Z'))
        session.keyChip("^L", TerminalKeys.ctrl('L'))
        session.keyChip("Home", TerminalKeys.HOME)
        session.keyChip("End", TerminalKeys.END)
        session.keyChip("PgUp", TerminalKeys.PAGE_UP)
        session.keyChip("PgDn", TerminalKeys.PAGE_DOWN)
    }
}

@Composable
private fun TerminalSession.keyChip(label: String, bytes: ByteArray) {
    FilterChip(
        selected = false,
        onClick = { send(bytes) },
        label = { Text(label) },
        modifier = Modifier.padding(end = 6.dp),
    )
}

/**
 * An always-empty text field: the soft keyboard needs something focusable to
 * type into, but a terminal has no editable buffer of its own — every
 * character is sent the moment it is typed and the field resets.
 */
@Composable
private fun HiddenInput(onTyped: (String) -> Unit) {
    val focusRequester = remember { FocusRequester() }
    var value by remember { mutableStateOf("") }

    LaunchedEffect(Unit) { focusRequester.requestFocus() }

    BasicTextField(
        value = value,
        onValueChange = { typed ->
            if (typed.isNotEmpty()) {
                onTyped(typed)
                value = ""
            }
        },
        textStyle = TextStyle(color = Color.Transparent, fontSize = FONT_SIZE),
        cursorBrush = SolidColor(Color.Transparent),
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = 8.dp)
            .focusRequester(focusRequester),
    )
}

