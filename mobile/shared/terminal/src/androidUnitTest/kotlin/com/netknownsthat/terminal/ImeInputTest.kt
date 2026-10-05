package com.netknownsthat.terminal

import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Test

class ImeInputTest {
    private fun send(old: String, new: String, ctrl: Boolean = false) =
        ImeInput.bytes(ImeInput.diff(old, new), ctrl).decodeToString()

    @Test fun typedCharacter() = assertEquals("e", send("  ", "  e"))

    /** A keyboard composing a word re-sends the whole word; only the new letter goes out. */
    @Test fun composingWordSendsOnlyTheNewLetter() {
        assertEquals("c", send("  e", "  ec"))
        assertEquals("ho", send("  ec", "  echo"))
    }

    @Test fun backspaceOnEmptyLineEatsThePad() = assertEquals("\u007f", send("  ", " "))

    /** Autocorrect replacing a word: delete what changed, type the rest. */
    @Test fun replacement() = assertEquals("\u007f\u007fat", send("  cta", "  cat"))

    @Test fun newlineIsEnter() = assertEquals("ls\r", send("  ", "  ls\n"))

    @Test fun ctrlAppliesToFirstCharacter() =
        assertArrayEquals(byteArrayOf(3, 'x'.code.toByte()), ImeInput.bytes(ImeInput.diff("  ", "  cx"), ctrl = true))

    @Test fun unicode() = assertEquals("привет", send("  ", "  привет"))
}
