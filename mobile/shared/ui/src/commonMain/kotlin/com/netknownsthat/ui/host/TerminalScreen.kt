package com.netknownsthat.ui.host

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.ui.text.TextRange
import androidx.compose.ui.text.input.KeyboardCapitalization
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.TextFieldValue
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.FilterChip
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
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
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalSoftwareKeyboardController
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.TextMeasurer
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.drawText
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.rememberTextMeasurer
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.netknownsthat.domain.repository.TerminalMode
import com.netknownsthat.terminal.TerminalEmulator
import com.netknownsthat.terminal.ImeInput
import com.netknownsthat.terminal.TerminalKeys
import com.netknownsthat.terminal.computeTerminalGrid
import com.netknownsthat.ui.common.FullScreen
import com.netknownsthat.ui.common.SnackbarMessage
import com.netknownsthat.ui.i18n.t
import com.netknownsthat.ui.status.HealthStatus
import com.netknownsthat.ui.theme.statusColor

private val TERMINAL_BACKGROUND = Color(0xFF12141A)
private val TERMINAL_FOREGROUND = Color(0xFFD8DEE9)
private val BASE_FONT_SIZE = 12.sp
private val MIN_FONT_SIZE = 5.sp

/** btop refuses to draw below 80x24, so the font gives way for it. */
private const val BTOP_MIN_COLUMNS = 80
private const val BTOP_MIN_ROWS = 24

/**
 * A live shell (or btop). The terminal takes the whole screen (no navigation
 * bar, no drawer whose swipe could fight the terminal's own gestures); the
 * key bar carries the keys a phone keyboard lacks and, in tmux, a menu of
 * tmux actions so nobody has to type Ctrl+B sequences on glass.
 */
@Composable
fun TerminalScreen(viewModel: TerminalViewModel, btop: Boolean) {
    FullScreen()
    val focusRequester = remember { FocusRequester() }
    val keyboard = LocalSoftwareKeyboardController.current
    var installOpen by remember { mutableStateOf(false) }

    DisposableEffect(viewModel) {
        viewModel.start()
        onDispose { viewModel.stop() }
    }
    SnackbarMessage(viewModel.message) { viewModel.message = null }
    if (installOpen) TmuxInstallDialog(viewModel, onDismiss = { installOpen = false })

    val showKeyboard: () -> Unit = {
        focusRequester.requestFocus()
        keyboard?.show()
    }

    Column(modifier = Modifier.fillMaxSize().background(TERMINAL_BACKGROUND)) {
        if (viewModel.tmuxMissing && !btop) {
            Card(modifier = Modifier.fillMaxWidth().padding(8.dp)) {
                Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.padding(horizontal = 12.dp, vertical = 4.dp)) {
                    Text(
                        t("tmux не установлен — сессия не переживёт обрыв связи", "tmux is not installed — the session will not survive a dropped connection"),
                        style = MaterialTheme.typography.bodySmall,
                        modifier = Modifier.weight(1f),
                    )
                    TextButton(onClick = { installOpen = true; viewModel.installTmux() }) { Text(t("Установить", "Install")) }
                }
            }
        }
        StatusLine(viewModel)
        Box(
            modifier = Modifier
                .weight(1f)
                .clickable(interactionSource = remember { MutableInteractionSource() }, indication = null) { showKeyboard() },
        ) {
            TerminalCanvas(
                viewModel = viewModel,
                minColumns = if (btop) BTOP_MIN_COLUMNS else 20,
                minRows = if (btop) BTOP_MIN_ROWS else 5,
            )
            if (viewModel.status == TerminalStatus.CLOSED || viewModel.status == TerminalStatus.FAILED) {
                Column(modifier = Modifier.align(Alignment.BottomCenter).padding(16.dp)) {
                    viewModel.error?.let { Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall) }
                    // A plain shell dies with the socket — the honest offer
                    // is a new session.
                    Button(onClick = { viewModel.start() }) { Text(t("Новая сессия", "New session")) }
                }
            }
        }
        var ctrlArmed by remember { mutableStateOf(false) }
        // The bar sits on the terminal's dark background whatever the app
        // theme: light-theme chips there were grey on black.
        MaterialTheme(colorScheme = darkColorScheme()) {
            KeyBar(
                viewModel = viewModel,
                tmux = viewModel.mode == TerminalMode.TMUX,
                ctrlArmed = ctrlArmed,
                onToggleCtrl = { ctrlArmed = !ctrlArmed },
                onShowKeyboard = showKeyboard,
            )
        }
        HiddenInput(focusRequester) { edit ->
            val ctrl = ctrlArmed && edit.added.isNotEmpty()
            viewModel.send(ImeInput.bytes(edit, ctrl))
            if (ctrl) ctrlArmed = false
        }
    }
}

@Composable
private fun StatusLine(viewModel: TerminalViewModel) {
    val (label, health) = when (viewModel.status) {
        TerminalStatus.CONNECTING -> t("Подключение…", "Connecting…") to HealthStatus.WARN
        TerminalStatus.RECONNECTING -> t("Связь потеряна — переподключаюсь к tmux…", "Connection lost — reconnecting to tmux…") to HealthStatus.WARN
        TerminalStatus.CONNECTED -> when (viewModel.mode) {
            TerminalMode.TMUX -> t("tmux · сессия переживёт обрыв", "tmux · the session survives a dropped link")
            TerminalMode.BTOP -> "btop"
            else -> t("оболочка", "shell")
        } to HealthStatus.OK
        TerminalStatus.CLOSED -> t("Сессия закрыта", "Session closed") to HealthStatus.UNKNOWN
        TerminalStatus.FAILED -> t("Не удалось подключиться", "Could not connect") to HealthStatus.BAD
    }
    Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.padding(horizontal = 8.dp, vertical = 2.dp)) {
        if (viewModel.status == TerminalStatus.CONNECTING || viewModel.status == TerminalStatus.RECONNECTING) {
            CircularProgressIndicator(strokeWidth = 2.dp, modifier = Modifier.padding(end = 6.dp).width(12.dp).height(12.dp))
        }
        Text(label, color = statusColor(health), style = MaterialTheme.typography.labelSmall)
    }
}

/**
 * The emulator's grid, drawn on one canvas: each line is laid out once by
 * the TextMeasurer (its cache keeps unchanged lines from being measured
 * again) instead of composing one Text per line on every byte of output.
 */
@Composable
private fun TerminalCanvas(viewModel: TerminalViewModel, minColumns: Int, minRows: Int) {
    val measurer = rememberTextMeasurer(cacheSize = 256)
    val density = LocalDensity.current
    val baseCharWidth = remember(measurer) { measureCharWidth(measurer) }
    val baseLineHeight = with(density) { BASE_FONT_SIZE.toPx() * 1.25f }

    BoxWithConstraints(modifier = Modifier.fillMaxSize().padding(4.dp)) {
        val widthPx = with(density) { maxWidth.toPx() }
        val heightPx = with(density) { maxHeight.toPx() }
        val grid = remember(widthPx, heightPx, baseCharWidth, minColumns, minRows) {
            computeTerminalGrid(
                widthPx = widthPx,
                heightPx = heightPx,
                baseCharWidthPx = baseCharWidth,
                baseLineHeightPx = baseLineHeight,
                minColumns = minColumns,
                minRows = minRows,
                baseFontSp = BASE_FONT_SIZE.value,
                minFontSp = MIN_FONT_SIZE.value,
            )
        }
        LaunchedEffect(grid) { viewModel.resize(grid.columns, grid.rows) }

        val style = TextStyle(fontFamily = FontFamily.Monospace, fontSize = grid.fontSp.sp, color = TERMINAL_FOREGROUND)
        val lineHeight = with(density) { grid.fontSp.sp.toPx() * 1.25f }
        val charWidth = baseCharWidth * grid.fontSp / BASE_FONT_SIZE.value
        val contentWidth = with(density) { (charWidth * grid.columns).toDp() }
        val scroll = rememberScrollState()

        Canvas(modifier = Modifier.horizontalScroll(scroll).width(maxOf(contentWidth, maxWidth)).fillMaxSize()) {
            // Read here so the canvas redraws on every change of the screen.
            @Suppress("UNUSED_VARIABLE")
            val rev = viewModel.revision
            val emu = viewModel.emulator
            emu.visibleLines.forEachIndexed { index, line ->
                val cursor = if (emu.cursorVisible && index == emu.cursorRow) emu.cursorColumn else -1
                val layout = measurer.measure(line.toAnnotated(cursor), style, softWrap = false)
                drawText(layout, topLeft = Offset(0f, index * lineHeight))
            }
        }
    }
}

/** Groups adjacent cells sharing a style into one span. */
private fun TerminalEmulator.Line.toAnnotated(cursorColumn: Int): AnnotatedString = buildAnnotatedString {
    var index = 0
    while (index < chars.size) {
        var end = index + 1
        while (
            end < chars.size && fg[end] == fg[index] && bg[end] == bg[index] && flags[end] == flags[index] &&
            end != cursorColumn && index != cursorColumn
        ) end++
        val inverse = flags[index] and TerminalEmulator.FLAG_INVERSE != 0 || (cursorColumn in index until end)
        val foreground = colorOf(fg[index], TERMINAL_FOREGROUND)
        val background = colorOf(bg[index], TERMINAL_BACKGROUND)
        withStyle(
            SpanStyle(
                color = if (inverse) background else foreground,
                background = if (inverse) foreground else if (bg[index] == TerminalEmulator.DEFAULT_COLOR) Color.Unspecified else background,
                fontWeight = if (flags[index] and TerminalEmulator.FLAG_BOLD != 0) FontWeight.Bold else FontWeight.Normal,
                textDecoration = if (flags[index] and TerminalEmulator.FLAG_UNDERLINE != 0) TextDecoration.Underline else null,
            ),
        ) { append(chars.concatToString(index, end)) }
        index = end
    }
}

private fun colorOf(value: Int, fallback: Color): Color =
    if (value == TerminalEmulator.DEFAULT_COLOR) fallback else Color(value)

private fun measureCharWidth(measurer: TextMeasurer): Float {
    // A run divided by its length keeps rounding error from drifting columns.
    val sample = "0".repeat(50)
    val result = measurer.measure(AnnotatedString(sample), TextStyle(fontFamily = FontFamily.Monospace, fontSize = BASE_FONT_SIZE))
    return result.size.width / sample.length.toFloat()
}

/** tmux actions: prefix + key, said in words. */
private val TMUX_ACTIONS: List<Triple<String, String, String>>
    get() = listOf(
        Triple("c", t("Новое окно", "New window"), "^B c"),
        Triple("n", t("Следующее окно", "Next window"), "^B n"),
        Triple("p", t("Предыдущее окно", "Previous window"), "^B p"),
        Triple("w", t("Список окон", "List windows"), "^B w"),
        Triple("\"", t("Разделить по горизонтали", "Split horizontally"), "^B \""),
        Triple("%", t("Разделить по вертикали", "Split vertically"), "^B %"),
        Triple("o", t("Следующая панель", "Next pane"), "^B o"),
        Triple("z", t("Развернуть панель", "Zoom pane"), "^B z"),
        Triple("[", t("Прокрутка (q — выход)", "Scroll mode (q — exit)"), "^B ["),
        Triple("d", t("Отсоединиться", "Detach"), "^B d"),
    )

@Composable
private fun KeyBar(
    viewModel: TerminalViewModel,
    tmux: Boolean,
    ctrlArmed: Boolean,
    onToggleCtrl: () -> Unit,
    onShowKeyboard: () -> Unit,
) {
    var tmuxMenu by remember { mutableStateOf(false) }
    Row(
        modifier = Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()).padding(horizontal = 8.dp, vertical = 4.dp),
    ) {
        FilterChip(selected = false, onClick = onShowKeyboard, label = { Text(t("⌨ Клавиатура", "⌨ Keyboard")) }, modifier = Modifier.padding(end = 6.dp))
        if (tmux) {
            Box {
                FilterChip(selected = tmuxMenu, onClick = { tmuxMenu = true }, label = { Text("tmux ▾") }, modifier = Modifier.padding(end = 6.dp))
                DropdownMenu(expanded = tmuxMenu, onDismissRequest = { tmuxMenu = false }) {
                    TMUX_ACTIONS.forEach { (key, label, hint) ->
                        DropdownMenuItem(
                            text = { Text(label) },
                            trailingIcon = { Text(hint, style = MaterialTheme.typography.labelSmall, fontFamily = FontFamily.Monospace) },
                            onClick = { tmuxMenu = false; viewModel.tmux(key) },
                        )
                    }
                    viewModel.tmuxMouse?.let { on ->
                        DropdownMenuItem(
                            text = { Text(if (on) t("Мышь tmux: выключить", "tmux mouse: turn off") else t("Мышь tmux: включить", "tmux mouse: turn on")) },
                            onClick = { tmuxMenu = false; viewModel.toggleMouse() },
                        )
                    }
                }
            }
        }
        // Ctrl arms the next letter typed — the only workable model on glass.
        FilterChip(selected = ctrlArmed, onClick = onToggleCtrl, label = { Text("Ctrl") }, modifier = Modifier.padding(end = 6.dp))
        listOf(
            "Esc" to TerminalKeys.ESC, "Tab" to TerminalKeys.TAB,
            "↑" to TerminalKeys.UP, "↓" to TerminalKeys.DOWN, "←" to TerminalKeys.LEFT, "→" to TerminalKeys.RIGHT,
            "^C" to TerminalKeys.ctrl('C'), "^D" to TerminalKeys.ctrl('D'), "^Z" to TerminalKeys.ctrl('Z'), "^L" to TerminalKeys.ctrl('L'),
            "Home" to TerminalKeys.HOME, "End" to TerminalKeys.END, "PgUp" to TerminalKeys.PAGE_UP, "PgDn" to TerminalKeys.PAGE_DOWN,
        ).forEach { (label, bytes) ->
            FilterChip(selected = false, onClick = { viewModel.send(bytes) }, label = { Text(label) }, modifier = Modifier.padding(end = 6.dp))
        }
    }
}

/** The field the soft keyboard types into. It is never cleared while the
 * keyboard is composing a word — see [ImeInput] for why — only once it has
 * committed and the text has grown long. */
@Composable
private fun HiddenInput(focusRequester: FocusRequester, onEdit: (ImeInput.Edit) -> Unit) {
    var value by remember { mutableStateOf(TextFieldValue(ImeInput.PAD, TextRange(ImeInput.PAD.length))) }
    LaunchedEffect(Unit) { focusRequester.requestFocus() }
    BasicTextField(
        value = value,
        onValueChange = { next ->
            val edit = ImeInput.diff(value.text, next.text)
            if (!edit.isEmpty) onEdit(edit)
            value = when {
                next.composition != null -> next
                next.text.length < ImeInput.PAD.length || next.text.length > 64 || next.text.endsWith("\n") ->
                    TextFieldValue(ImeInput.PAD, TextRange(ImeInput.PAD.length))
                else -> next
            }
        },
        // No suggestions or autocorrect: a shell command is not prose.
        keyboardOptions = KeyboardOptions(
            capitalization = KeyboardCapitalization.None,
            autoCorrectEnabled = false,
            keyboardType = KeyboardType.Ascii,
        ),
        textStyle = TextStyle(color = Color.Transparent, fontSize = BASE_FONT_SIZE),
        cursorBrush = SolidColor(Color.Transparent),
        modifier = Modifier.fillMaxWidth().height(1.dp).focusRequester(focusRequester),
    )
}

/** apt-get install tmux, streamed live. */
@Composable
private fun TmuxInstallDialog(viewModel: TerminalViewModel, onDismiss: () -> Unit) {
    val measurer = rememberTextMeasurer(cacheSize = 64)
    AlertDialog(
        onDismissRequest = { if (!viewModel.installing) onDismiss() },
        title = { Text(t("Установка tmux", "Installing tmux")) },
        text = {
            Canvas(modifier = Modifier.fillMaxWidth().height(260.dp).background(TERMINAL_BACKGROUND)) {
                @Suppress("UNUSED_VARIABLE")
                val rev = viewModel.installRevision
                val style = TextStyle(fontFamily = FontFamily.Monospace, fontSize = 9.sp, color = TERMINAL_FOREGROUND)
                val lh = 9.sp.toPx() * 1.25f
                viewModel.installEmulator.visibleLines.forEachIndexed { i, line ->
                    drawText(measurer.measure(line.toAnnotated(-1), style, softWrap = false), topLeft = Offset(2f, i * lh))
                }
            }
        },
        confirmButton = {
            TextButton(onClick = onDismiss, enabled = !viewModel.installing) {
                Text(
                    when (viewModel.installResult) {
                        null -> t("Устанавливается…", "Installing…")
                        true -> t("Готово", "Done")
                        false -> t("Закрыть", "Close")
                    },
                )
            }
        },
    )
}
