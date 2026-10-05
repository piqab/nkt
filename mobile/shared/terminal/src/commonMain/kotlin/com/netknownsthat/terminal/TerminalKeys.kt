package com.netknownsthat.terminal

/**
 * Byte sequences for the keys a phone keyboard does not have but a terminal
 * needs constantly. Arrow keys use the "normal" (CSI) forms rather than the
 * application-cursor (SS3) ones — that is what a shell without an active
 * full-screen program expects, and what the web terminal sends too.
 */
object TerminalKeys {
    val ESC = byteArrayOf(0x1B)
    val TAB = byteArrayOf(0x09)
    val ENTER = byteArrayOf(0x0D)
    val BACKSPACE = byteArrayOf(0x7F)
    val UP = "\u001b[A".encodeToByteArray()
    val DOWN = "\u001b[B".encodeToByteArray()
    val RIGHT = "\u001b[C".encodeToByteArray()
    val LEFT = "\u001b[D".encodeToByteArray()
    val HOME = "\u001b[H".encodeToByteArray()
    val END = "\u001b[F".encodeToByteArray()
    val PAGE_UP = "\u001b[5~".encodeToByteArray()
    val PAGE_DOWN = "\u001b[6~".encodeToByteArray()

    /** Ctrl+<letter>: the control code is the letter with its top bits
     * cleared, which is all "control" ever meant on a teletype. */
    fun ctrl(letter: Char): ByteArray {
        val upper = letter.uppercaseChar()
        return byteArrayOf(((upper.code) and 0x1F).toByte())
    }
}
