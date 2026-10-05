package com.netknownsthat.domain.common

/**
 * Result of every operation that can fail — the domain's own type, so no
 * layer above data ever sees an HTTP client exception or status code.
 */
sealed interface Outcome<out T> {
    data class Success<T>(val value: T) : Outcome<T>
    data class Failure(val error: AppError) : Outcome<Nothing>
}

inline fun <T, R> Outcome<T>.map(transform: (T) -> R): Outcome<R> = when (this) {
    is Outcome.Success -> Outcome.Success(transform(value))
    is Outcome.Failure -> this
}

inline fun <T, R> Outcome<T>.flatMap(transform: (T) -> Outcome<R>): Outcome<R> = when (this) {
    is Outcome.Success -> transform(value)
    is Outcome.Failure -> this
}

fun <T> Outcome<T>.getOrNull(): T? = (this as? Outcome.Success)?.value

fun <T> Outcome<T>.getOrDefault(default: T): T = getOrNull() ?: default

inline fun <T> Outcome<T>.onSuccess(block: (T) -> Unit): Outcome<T> {
    if (this is Outcome.Success) block(value)
    return this
}

inline fun <T> Outcome<T>.onFailure(block: (AppError) -> Unit): Outcome<T> {
    if (this is Outcome.Failure) block(error)
    return this
}

/**
 * What went wrong, as a kind rather than a sentence: presentation turns it
 * into text in the interface language. [message] is the server's own
 * explanation (already in the language the app asked for) when there is one.
 */
sealed interface AppError {
    /** No hub address saved yet. */
    data object NotConfigured : AppError

    /** The hub could not be reached at all. */
    data class Network(val detail: String) : AppError

    /** The session is gone (401) — sign in again. */
    data class Unauthorized(val message: String) : AppError

    /** The hub answered with an error status and its explanation. */
    data class Server(val status: Int, val message: String) : AppError

    /** The answer did not have the expected shape — app and hub disagree. */
    data class BadResponse(val detail: String) : AppError

    /** The hub's pinned self-signed certificate changed. */
    data class CertificateChanged(val authority: String, val pinned: String, val presented: String) : AppError
}

/** The server's message for the cases that carry one; null otherwise. */
val AppError.serverMessage: String?
    get() = when (this) {
        is AppError.Server -> message
        is AppError.Unauthorized -> message
        else -> null
    }
