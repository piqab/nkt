package com.netknownsthat.app.i18n

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import java.util.Locale

/** Interface language of the app. [code] is what X-NKT-Lang carries, so the
 * server answers (errors, job logs, alert details) in the same language. */
enum class AppLang(val code: String, val label: String) {
    RU("ru", "Русский"),
    EN("en", "English"),
    ;

    companion object {
        fun fromCode(code: String?): AppLang? = entries.firstOrNull { it.code == code }

        /** The phone's language when it is one of ours; English otherwise —
         * an operator reading neither is more likely to read English. */
        fun system(): AppLang = if (Locale.getDefault().language == "ru") RU else EN
    }
}

/**
 * Current language. Compose state on purpose: every [t] call made during
 * composition reads it, so switching the language redraws whatever is on
 * screen without restarting the activity.
 */
object I18n {
    var lang by mutableStateOf(AppLang.system())
}

/**
 * The app's one translation call: both texts sit side by side at the call
 * site, templates included, so a string and its translation cannot drift
 * apart and nothing needs a key. I18nCoverageTest fails the build on any
 * Cyrillic literal that is not the first argument of [t].
 *
 * Values computed once (a top-level map, an enum constructor) would freeze
 * the language they were built in — such places are getters instead.
 */
fun t(ru: String, en: String): String = if (I18n.lang == AppLang.EN) en else ru
