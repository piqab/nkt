package com.netknownsthat.ui.common

import com.netknownsthat.domain.common.AppError
import com.netknownsthat.ui.i18n.t

/** An error as a sentence in the interface language. The server's own
 * message is already in that language (the app sends X-NKT-Lang). */
fun AppError.text(): String = when (this) {
    AppError.NotConfigured -> t("Хаб не настроен — укажите адрес на экране входа", "No hub configured — enter its address on the sign-in screen")
    is AppError.Network -> t("Сетевая ошибка: $detail", "Network error: $detail")
    is AppError.Unauthorized -> t("Сессия закончилась — войдите снова", "The session has ended — sign in again")
    is AppError.Server -> message
    is AppError.BadResponse -> t("Не удалось разобрать ответ сервера: $detail", "Could not parse the server response: $detail")
    is AppError.CertificateChanged -> t(
        "Сертификат хаба $authority изменился (ожидался $pinned, получен $presented) — либо хаб переустановлен, либо соединение перехватывается",
        "The certificate of hub $authority changed (expected $pinned, got $presented) — either the hub was reinstalled or the connection is being intercepted",
    )
}

fun failedText(error: AppError): String = t("Не удалось: ${error.text()}", "Failed: ${error.text()}")
