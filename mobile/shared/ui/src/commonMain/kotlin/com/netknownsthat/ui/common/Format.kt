package com.netknownsthat.ui.common

import com.netknownsthat.ui.i18n.t
import kotlinx.datetime.TimeZone
import kotlinx.datetime.toLocalDateTime
import kotlin.math.abs
import kotlin.math.pow
import kotlin.math.roundToLong
import kotlin.time.Instant

/** [this] with [digits] after the point — what "%.1f".format() did on the
 * JVM, which common code does not have. */
fun Double.fmt(digits: Int = 1): String {
    if (isNaN() || isInfinite()) return toString()
    val factor = 10.0.pow(digits)
    val scaled = (abs(this) * factor).roundToLong()
    val whole = scaled / factor.toLong()
    val frac = scaled % factor.toLong()
    val sign = if (this < 0 && scaled != 0L) "-" else ""
    return if (digits == 0) "$sign$whole" else "$sign$whole.${frac.toString().padStart(digits, '0')}"
}

fun Float.fmt(digits: Int = 1): String = toDouble().fmt(digits)

private fun two(n: Int) = n.toString().padStart(2, '0')

/** Server timestamps are RFC 3339; shown as dd.MM HH:mm:ss in the device
 * zone, with the year (dd.MM.yyyy) when it is not the current one. */
@OptIn(kotlin.time.ExperimentalTime::class)
fun formatTs(raw: String): String = runCatching {
    val zone = TimeZone.currentSystemDefault()
    val local = Instant.parse(raw).toLocalDateTime(zone)
    val thisYear = kotlin.time.Clock.System.now().toLocalDateTime(zone).year
    val year = if (local.year != thisYear) ".${local.year}" else ""
    "${two(local.day)}.${two(local.month.ordinal + 1)}$year ${two(local.hour)}:${two(local.minute)}:${two(local.second)}"
}.getOrDefault(raw)

/** 1536 → "1.5 KB"; units in the interface language. */
fun humanBytes(bytes: Double): String {
    val units = listOf(t("Б", "B"), t("КБ", "KB"), t("МБ", "MB"), t("ГБ", "GB"), t("ТБ", "TB"))
    var v = bytes
    var i = 0
    while (v >= 1024 && i < units.lastIndex) {
        v /= 1024
        i++
    }
    return if (i == 0) "${v.toLong()} ${units[i]}" else "${v.fmt(1)} ${units[i]}"
}

fun humanBytes(bytes: Long): String = humanBytes(bytes.toDouble())
