package com.netknownsthat.app.terminal

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Regression tests for the grid arithmetic behind a bug found on a real
 * phone: btop reported "Terminal size too small. Need w=80 h=24" because the
 * columns were computed purely from screen width at a fixed font size, which
 * on a portrait phone comes out around 50.
 */
class TerminalGridTest {

    // A 1080x1920 phone at 3x density, 12sp monospace: roughly 21.6px per
    // character and a 45px line box.
    private val phoneWidth = 1080f
    private val phoneHeight = 1700f
    private val charWidth = 21.6f
    private val lineHeight = 45f
    private val baseFont = 12f
    private val minFont = 5f

    @Test
    fun `btop gets its 80 by 24 on a portrait phone`() {
        val grid = computeTerminalGrid(
            widthPx = phoneWidth,
            heightPx = phoneHeight,
            baseCharWidthPx = charWidth,
            baseLineHeightPx = lineHeight,
            minColumns = 80,
            minRows = 24,
            baseFontSp = baseFont,
            minFontSp = minFont,
        )
        assertTrue("btop needs at least 80 columns, got ${grid.columns}", grid.columns >= 80)
        assertTrue("btop needs at least 24 rows, got ${grid.rows}", grid.rows >= 24)
        assertTrue("the font should have shrunk to fit", grid.fontSp < baseFont)
    }

    @Test
    fun `a plain shell keeps the comfortable font`() {
        // No 80-column floor, so nothing needs to shrink: a shell is far
        // more usable at a readable size than at btop's.
        val grid = computeTerminalGrid(
            widthPx = phoneWidth,
            heightPx = phoneHeight,
            baseCharWidthPx = charWidth,
            baseLineHeightPx = lineHeight,
            minColumns = 20,
            minRows = 5,
            baseFontSp = baseFont,
            minFontSp = minFont,
        )
        assertEquals("the shell should not shrink", baseFont, grid.fontSp, 0.01f)
        assertEquals(50, grid.columns)
    }

    @Test
    fun `a wide landscape screen needs no shrinking even for btop`() {
        val grid = computeTerminalGrid(
            widthPx = 2400f,
            heightPx = 1080f,
            baseCharWidthPx = charWidth,
            baseLineHeightPx = lineHeight,
            minColumns = 80,
            minRows = 24,
            baseFontSp = baseFont,
            minFontSp = minFont,
        )
        assertEquals(baseFont, grid.fontSp, 0.01f)
        assertTrue(grid.columns >= 80 && grid.rows >= 24)
    }

    @Test
    fun `height is taken into account, not only width`() {
        // Wide but very short: the rows are what fails to fit here, and a
        // width-only calculation would happily report 24 rows that are not
        // there.
        val grid = computeTerminalGrid(
            widthPx = 2400f,
            heightPx = 500f,
            baseCharWidthPx = charWidth,
            baseLineHeightPx = lineHeight,
            minColumns = 80,
            minRows = 24,
            baseFontSp = baseFont,
            minFontSp = minFont,
        )
        assertTrue("the font should have shrunk for height", grid.fontSp < baseFont)
        assertTrue(grid.rows >= 24)
    }

    @Test
    fun `the font never goes below the readable floor`() {
        // A tiny viewport cannot fit 80x24 at any legible size; overflowing
        // (and scrolling) beats rendering unreadable pixels.
        val grid = computeTerminalGrid(
            widthPx = 200f,
            heightPx = 200f,
            baseCharWidthPx = charWidth,
            baseLineHeightPx = lineHeight,
            minColumns = 80,
            minRows = 24,
            baseFontSp = baseFont,
            minFontSp = minFont,
        )
        assertEquals(minFont, grid.fontSp, 0.01f)
    }

    @Test
    fun `a zero-sized viewport falls back to the minimum rather than dividing by zero`() {
        val grid = computeTerminalGrid(
            widthPx = 0f,
            heightPx = 0f,
            baseCharWidthPx = charWidth,
            baseLineHeightPx = lineHeight,
            minColumns = 80,
            minRows = 24,
            baseFontSp = baseFont,
            minFontSp = minFont,
        )
        assertEquals(80, grid.columns)
        assertEquals(24, grid.rows)
    }
}
