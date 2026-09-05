package com.netknownsthat.app.terminal

/**
 * How many columns and rows fit, and at what font size.
 *
 * Pulled out of the Compose code so it can be tested: getting this wrong is
 * not a cosmetic problem — btop refuses to draw at all below 80x24 ("Terminal
 * size too small. Need w=80 h=24"), which is exactly what a phone in portrait
 * produces at a comfortable font size.
 */
data class TerminalGrid(
    val columns: Int,
    val rows: Int,
    /** Font size in sp to actually render with. */
    val fontSp: Float,
)

/**
 * Picks the largest font that still fits [minColumns] x [minRows], then
 * measures the grid at that size.
 *
 * A monospace face scales linearly, so the required size is computed rather
 * than searched for. When even [minFontSp] is not small enough the grid is
 * allowed to overflow instead of shrinking into illegibility — the view
 * scrolls horizontally, which is worse than fitting but better than a wall of
 * unreadable pixels.
 */
fun computeTerminalGrid(
    widthPx: Float,
    heightPx: Float,
    baseCharWidthPx: Float,
    baseLineHeightPx: Float,
    minColumns: Int,
    minRows: Int,
    baseFontSp: Float,
    minFontSp: Float,
): TerminalGrid {
    if (widthPx <= 0f || heightPx <= 0f || baseCharWidthPx <= 0f || baseLineHeightPx <= 0f) {
        return TerminalGrid(minColumns, minRows, baseFontSp)
    }

    val naturalColumns = widthPx / baseCharWidthPx
    val naturalRows = heightPx / baseLineHeightPx
    val scale = minOf(1f, naturalColumns / minColumns, naturalRows / minRows)

    val fontSp = if (scale >= 1f) baseFontSp
    else (baseFontSp * scale).coerceAtLeast(minFontSp)

    val ratio = fontSp / baseFontSp
    val columns = (widthPx / (baseCharWidthPx * ratio)).toInt().coerceIn(minColumns, 400)
    val rows = (heightPx / (baseLineHeightPx * ratio)).toInt().coerceIn(minRows, 200)
    return TerminalGrid(columns, rows, fontSp)
}
