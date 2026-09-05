package com.netknownsthat.app.terminal

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The terminal emulator is the one piece of this app that can be verified
 * properly without a device, so it is tested against the sequences a real
 * shell and btop actually emit rather than only the happy path.
 */
class TerminalEmulatorTest {

    private fun emulator(columns: Int = 20, rows: Int = 5) =
        TerminalEmulator(columns = columns, rows = rows)

    private fun TerminalEmulator.write(text: String) = feed(text.toByteArray(Charsets.UTF_8))

    private fun TerminalEmulator.line(index: Int) = visibleLines[index].text()

    @Test
    fun `plain text lands on the first line`() {
        val term = emulator()
        term.write("hello")
        assertEquals("hello", term.line(0))
        assertEquals(5, term.cursorColumn)
    }

    @Test
    fun `carriage return and line feed move as a teletype would`() {
        val term = emulator()
        term.write("one\r\ntwo")
        assertEquals("one", term.line(0))
        assertEquals("two", term.line(1))
    }

    @Test
    fun `backspace overwrite replaces the character`() {
        val term = emulator()
        term.write("ab\bc")
        assertEquals("ac", term.line(0))
    }

    @Test
    fun `text wraps at the right margin`() {
        val term = emulator(columns = 5)
        term.write("abcdefg")
        assertEquals("abcde", term.line(0))
        assertEquals("fg", term.line(1))
    }

    @Test
    fun `a character in the last column does not scroll early`() {
        // The classic off-by-one. Filling the bottom row exactly to the
        // right margin must not scroll: a terminal that wraps eagerly (moves
        // the cursor the moment the last column is written, instead of
        // waiting to see whether another character actually follows) throws
        // away the line above here. Checking it on the bottom row is what
        // makes the difference observable at all — anywhere else the two
        // behaviours look identical.
        val term = emulator(columns = 5, rows = 2)
        term.write("top")
        term.write("\u001b[2;1Habcde")
        assertEquals("scrolled a line away too early", "top", term.line(0))
        assertEquals("abcde", term.line(1))
    }

    @Test
    fun `cursor positioning is one-based`() {
        val term = emulator()
        term.write("\u001b[3;5Hx")
        assertEquals("    x", term.line(2))
    }

    @Test
    fun `erase in line clears from the cursor`() {
        val term = emulator()
        term.write("abcdef\u001b[1;4H\u001b[K")
        assertEquals("abc", term.line(0))
    }

    @Test
    fun `erase in display with mode 2 clears everything`() {
        val term = emulator()
        term.write("one\r\ntwo\u001b[2J")
        assertEquals("", term.line(0))
        assertEquals("", term.line(1))
    }

    @Test
    fun `scrolling pushes the top line into scrollback`() {
        val term = emulator(rows = 2)
        term.write("a\r\nb\r\nc")
        assertEquals("b", term.line(0))
        assertEquals("c", term.line(1))
        // "a" left the screen but must still be readable by scrolling back.
        assertTrue("scrollback lost the first line",
            term.allLines().any { it.text() == "a" })
    }

    @Test
    fun `a scroll region keeps the lines outside it still`() {
        val term = emulator(rows = 5)
        term.write("\u001b[1;5H")           // fill five lines
        term.write("\u001b[1;1Htop")
        term.write("\u001b[5;1Hbottom")
        term.write("\u001b[2;4r")           // scroll region = rows 2..4
        term.write("\u001b[4;1Hx\n")        // force a scroll inside it
        assertEquals("top", term.line(0))
        assertEquals("bottom", term.line(4))
    }

    @Test
    fun `delete and insert characters shift the line`() {
        val term = emulator()
        term.write("abcdef\u001b[1;2H\u001b[2P")
        assertEquals("adef", term.line(0))

        val second = emulator()
        second.write("abc\u001b[1;2H\u001b[2@")
        assertEquals("a  bc", second.line(0))
    }

    @Test
    fun `insert and delete lines respect the screen`() {
        val term = emulator(rows = 3)
        term.write("one\r\ntwo\r\nthree")
        term.write("\u001b[1;1H\u001b[1M") // delete first line
        assertEquals("two", term.line(0))
        assertEquals("three", term.line(1))
    }

    @Test
    fun `sgr sets and resets colours`() {
        val term = emulator()
        term.write("\u001b[31mred\u001b[0mplain")
        val line = term.visibleLines[0]
        assertTrue("foreground was not set", line.fg[0] != TerminalEmulator.DEFAULT_COLOR)
        assertEquals(
            "colour leaked past the reset",
            TerminalEmulator.DEFAULT_COLOR,
            line.fg[3],
        )
    }

    @Test
    fun `256-colour and truecolour forms both decode`() {
        val term = emulator()
        term.write("\u001b[38;5;196ma")
        term.write("\u001b[38;2;10;20;30mb")
        val line = term.visibleLines[0]
        assertEquals("256-colour index 196 is not the expected red", xterm256(196), line.fg[0])
        assertEquals(
            "truecolour did not decode",
            (0xFF shl 24) or (10 shl 16) or (20 shl 8) or 30,
            line.fg[1],
        )
    }

    @Test
    fun `bold and inverse flags are tracked`() {
        val term = emulator()
        term.write("\u001b[1;7mx\u001b[0my")
        val line = term.visibleLines[0]
        assertTrue("bold missing", line.flags[0] and TerminalEmulator.FLAG_BOLD != 0)
        assertTrue("inverse missing", line.flags[0] and TerminalEmulator.FLAG_INVERSE != 0)
        assertEquals("flags leaked past the reset", 0, line.flags[1])
    }

    @Test
    fun `cursor visibility responds to the private mode`() {
        val term = emulator()
        term.write("\u001b[?25l")
        assertFalse("cursor should be hidden", term.cursorVisible)
        term.write("\u001b[?25h")
        assertTrue("cursor should be visible", term.cursorVisible)
    }

    @Test
    fun `window title sequences are swallowed, not printed`() {
        val term = emulator()
        term.write("\u001b]0;some title\u0007done")
        assertEquals("done", term.line(0))
    }

    @Test
    fun `an unknown sequence does not leak escape characters`() {
        val term = emulator()
        term.write("\u001b[>4;2mok")
        assertEquals("ok", term.line(0))
    }

    @Test
    fun `utf-8 split across two feeds still decodes`() {
        // This is the case that only shows up over a real socket: a
        // multi-byte character arriving in two frames.
        val term = emulator()
        val bytes = "п".toByteArray(Charsets.UTF_8)
        term.feed(byteArrayOf(bytes[0]))
        term.feed(byteArrayOf(bytes[1]))
        assertEquals("п", term.line(0))
    }

    @Test
    fun `cyrillic and box drawing survive intact`() {
        val term = emulator()
        term.write("привет │┤")
        assertEquals("привет │┤", term.line(0))
    }

    @Test
    fun `resize keeps the most recent output`() {
        val term = emulator(rows = 4)
        term.write("a\r\nb\r\nc\r\nd")
        term.resize(20, 2)
        assertEquals(2, term.visibleLines.size)
        assertEquals("c", term.line(0))
        assertEquals("d", term.line(1))
    }

    @Test
    fun `save and restore cursor round-trips`() {
        val term = emulator()
        term.write("\u001b[2;3H\u001b7\u001b[1;1H\u001b8x")
        assertEquals("  x", term.line(1))
    }

    @Test
    fun `real bash output renders without escape junk`() {
        // Captured verbatim from a real /api/terminal/ws session against
        // nkt serve — bracketed-paste mode, an OSC title, a colourised
        // prompt and the command's own output. Synthetic sequences only
        // prove the parser handles what I thought to write; this proves it
        // handles what a shell actually sends.
        val term = emulator(columns = 80, rows = 24)
        term.write(
            "\u001b[?2004h\u001b]0;alex@R: ~/NetKnownsThat\u0007" +
                "\u001b[01;32malex@R\u001b[00m:\u001b[01;34m~/NetKnownsThat\u001b[00m$ " +
                "echo NKT_MARKER_OK\r\n\u001b[?2004l\r" +
                "NKT_MARKER_OK\r\n" +
                "\u001b[?2004h\u001b]0;alex@R: ~/NetKnownsThat\u0007" +
                "\u001b[01;32malex@R\u001b[00m:\u001b[01;34m~/NetKnownsThat\u001b[00m$ "
        )

        assertEquals("alex@R:~/NetKnownsThat$ echo NKT_MARKER_OK", term.line(0))
        assertEquals("NKT_MARKER_OK", term.line(1))
        assertEquals("alex@R:~/NetKnownsThat$", term.line(2))

        val screen = term.screenText()
        assertFalse("escape characters leaked to the screen", screen.contains('\u001b'))
        assertFalse("the window title leaked to the screen", screen.contains("0;alex@R"))
        assertFalse("bracketed-paste mode leaked", screen.contains("2004"))

        // The prompt really is coloured: green user@host, blue path.
        val prompt = term.visibleLines[0]
        assertTrue(
            "prompt lost its colour",
            prompt.fg[0] != TerminalEmulator.DEFAULT_COLOR &&
                prompt.fg[0] != prompt.fg[prompt.chars.indexOf('~')],
        )
    }

    @Test
    fun `a real tmux session renders its status bar and nothing else`() {
        // Captured from an actual /api/terminal/ws?tmux=1 session. tmux is
        // the full-screen case the shell tests do not cover: alternate
        // screen, DECSTBM scroll region, charset designation, window
        // operations, and OSC queries terminated by ST rather than BEL —
        // several of which have their own code path in the parser and none
        // of which a hand-written sample would have exercised together.
        val term = emulator(columns = 80, rows = 24)
        term.feed(
            checkNotNull(javaClass.getResourceAsStream("/terminal/tmux-session.txt"))
                .bufferedReader().use { it.readText() }
                .toByteArray(Charsets.UTF_8)
        )

        val screen = term.screenText()
        assertFalse("escape characters leaked to the screen", screen.contains(''))
        assertTrue("tmux status bar is missing", screen.contains("[nkt]"))
        assertTrue("the active window is not shown", screen.contains("0:bash"))
        // The shell prompt tmux started sits on the first line, its status
        // bar on the last, and everything between was cleared and must stay
        // clear: a parser that prints what it does not understand fills
        // those lines with garbage instead.
        assertTrue("the shell prompt inside tmux is missing", term.line(0).endsWith("$"))
        assertTrue("the status bar is not on the last row", term.line(23).startsWith("[nkt]"))
        assertTrue(
            "tmux cleared the screen but something was drawn anyway",
            (1..22).all { term.line(it).isEmpty() },
        )
    }

    @Test
    fun `tab advances to the next eight-column stop`() {
        val term = emulator(columns = 30)
        term.write("ab\tc")
        assertEquals("ab      c", term.line(0))
    }
}
