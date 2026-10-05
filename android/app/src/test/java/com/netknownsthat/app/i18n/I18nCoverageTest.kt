package com.netknownsthat.app.i18n

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.File

/**
 * Every Russian text in the app must be the first argument of t(ru, en), and
 * its English pair must contain no Cyrillic — otherwise an English interface
 * shows Russian. Scans the sources the same way the compiler sees string
 * literals: comments skipped, templates (with nested strings) kept inside
 * their literal.
 */
class I18nCoverageTest {
    private val cyrillic = Regex("[А-Яа-яЁё]")
    private val enumEntry = Regex("""(^|\n)\s*[A-Z][A-Z0-9_]*\($""")

    private data class Literal(val start: Int, val body: String)

    private fun literals(src: String): List<Literal> {
        val out = mutableListOf<Literal>()
        val n = src.length

        fun skipString(start: Int): Int {
            if (src.startsWith("\"\"\"", start)) {
                var j = src.indexOf("\"\"\"", start + 3)
                while (src.startsWith("\"\"\"\"", j)) j++
                return j + 3
            }
            var j = start + 1
            while (j < n) {
                when {
                    src[j] == '\\' -> j += 2
                    src[j] == '"' -> return j + 1
                    src[j] == '$' && j + 1 < n && src[j + 1] == '{' -> {
                        var depth = 1
                        j += 2
                        while (depth > 0) {
                            when (src[j]) {
                                '"' -> { j = skipString(j); continue }
                                '{' -> depth++
                                '}' -> depth--
                            }
                            j++
                        }
                    }
                    else -> j++
                }
            }
            error("unterminated string at $start")
        }

        var i = 0
        while (i < n) {
            when {
                src.startsWith("//", i) -> i = src.indexOf('\n', i).let { if (it < 0) n else it }
                src.startsWith("/*", i) -> i = src.indexOf("*/", i) + 2
                src[i] == '\'' -> i += if (src[i + 1] == '\\') 4 else 3
                src[i] == '"' -> {
                    val end = skipString(i)
                    out += Literal(i, src.substring(i, end))
                    i = end
                }
                else -> i++
            }
        }
        return out
    }

    @Test
    fun everyRussianTextHasAnEnglishPair() {
        val root = File("src/main/java")
        assertTrue("sources not found from ${File(".").absolutePath}", root.isDirectory)
        val problems = mutableListOf<String>()
        var pairs = 0
        root.walk().filter { it.extension == "kt" }.forEach { file ->
            val src = file.readText()
            literals(src).forEach { lit ->
                if (!cyrillic.containsMatchIn(lit.body)) return@forEach
                val before = src.substring(0, lit.start).trimEnd()
                // AppLang's own name for Russian is meant to stay Russian.
                if (file.name == "I18n.kt" && lit.body == "\"Русский\"") return@forEach
                // t(ru, en), or an enum entry carrying the pair to its own
                // title getter — HostSection("Обзор", "Overview").
                if (before.endsWith("t(") || enumEntry.containsMatchIn(before.takeLast(64))) {
                    pairs++
                } else {
                    val line = src.substring(0, lit.start).count { it == '\n' } + 1
                    problems += "${file.path}:$line ${lit.body.take(80)}"
                }
            }
        }
        assertEquals("Russian literals outside t(ru, en):\n" + problems.joinToString("\n"), 0, problems.size)
        assertTrue("suspiciously few translated texts: $pairs", pairs > 500)
    }

    @Test
    fun switchingLanguageSwitchesText() {
        val saved = I18n.lang
        try {
            I18n.lang = AppLang.EN
            assertEquals("Refresh", t("Обновить", "Refresh"))
            I18n.lang = AppLang.RU
            assertEquals("Обновить", t("Обновить", "Refresh"))
        } finally {
            I18n.lang = saved
        }
    }
}
