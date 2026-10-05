package com.netknownsthat.ui.i18n

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.netknownsthat.domain.repository.AppLanguage
import com.netknownsthat.domain.repository.LanguageProvider

/** Label of each language in its own language — the same on both sides. */
val AppLanguage.label: String
    get() = when (this) {
        AppLanguage.RU -> "Русский"
        AppLanguage.EN -> "English"
    }

/**
 * Current interface language. Compose state on purpose: every [t] call made
 * during composition reads it, so switching the language redraws whatever is
 * on screen at once. Also the [LanguageProvider] the data layer puts into
 * X-NKT-Lang, so the hub answers in the same language.
 */
object I18n : LanguageProvider {
    var lang by mutableStateOf(AppLanguage.EN)

    override fun current(): AppLanguage = lang
}

/**
 * The app's one translation call: both texts at the call site, templates
 * included, so a string and its translation cannot drift apart.
 * I18nCoverageTest fails the build on any Cyrillic literal that is not the
 * first argument of [t]. Values computed once (top-level vals, enum
 * entries) would freeze the language they were built in — those are
 * getters instead.
 */
fun t(ru: String, en: String): String = if (I18n.lang == AppLanguage.EN) en else ru
