package com.netknownsthat.terminal

/**
 * Turns edits of the soft keyboard's text field into terminal input.
 *
 * A phone keyboard does not send keys, it edits text — and while it is
 * composing a word it may replace the whole word at once. So the field is
 * never cleared under the keyboard's feet (that duplicated letters: the
 * keyboard re-committed its word into the emptied field); instead each new
 * value is compared with the previous one: removed characters become
 * Backspace, added ones are sent, a newline is Enter.
 *
 * The field starts with [PAD], so that Backspace on an "empty" line is still
 * an edit the keyboard reports.
 */
object ImeInput {
    const val PAD = "  "

    class Edit(val deleted: Int, val added: String) {
        val isEmpty get() = deleted == 0 && added.isEmpty()
    }

    fun diff(old: String, new: String): Edit {
        var common = 0
        val max = minOf(old.length, new.length)
        while (common < max && old[common] == new[common]) common++
        return Edit(old.length - common, new.substring(common))
    }

    /** The bytes for an edit; [ctrl] applies Ctrl to the first added character. */
    fun bytes(edit: Edit, ctrl: Boolean = false): ByteArray {
        val out = ArrayList<Byte>()
        repeat(edit.deleted) { out += TerminalKeys.BACKSPACE.toList() }
        edit.added.forEachIndexed { i, c ->
            when {
                ctrl && i == 0 -> out += TerminalKeys.ctrl(c).toList()
                c == '\n' -> out += TerminalKeys.ENTER.toList()
                else -> out += c.toString().encodeToByteArray().toList()
            }
        }
        return out.toByteArray()
    }
}
