package com.netknownsthat.data.remote

import co.touchlab.kermit.Logger
import com.netknownsthat.domain.common.AppError
import com.netknownsthat.domain.common.HostTarget
import com.netknownsthat.domain.common.Outcome
import com.netknownsthat.domain.repository.LanguageProvider
import io.ktor.client.HttpClient
import io.ktor.client.plugins.HttpTimeout
import io.ktor.client.plugins.cookies.HttpCookies
import io.ktor.client.plugins.defaultRequest
import io.ktor.client.plugins.websocket.WebSockets
import io.ktor.client.request.header
import io.ktor.client.request.request
import io.ktor.client.request.setBody
import io.ktor.client.statement.bodyAsText
import io.ktor.http.ContentType
import io.ktor.http.HttpMethod
import io.ktor.http.URLProtocol
import io.ktor.http.Url
import io.ktor.http.contentType
import io.ktor.http.encodeURLParameter
import io.ktor.http.takeFrom
import io.ktor.http.isSuccess
import kotlinx.coroutines.CancellationException
import kotlinx.serialization.KSerializer
import kotlinx.serialization.Serializable
import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.serializer

/**
 * The one networking chokepoint: REST and WebSockets to the hub, the session
 * cookie, the language header and the mapping of every failure to an
 * [AppError]. Host scoping is a path prefix ([hostPath]) chosen per call —
 * there is no global "current host".
 */
class ApiClient(
    engineFactory: () -> io.ktor.client.engine.HttpClientEngine,
    val cookies: PersistentCookieStore,
    val pins: CertPins,
    private val language: LanguageProvider,
) {
    val json = NktJson

    /** 401 on anything but the auth calls — the session ended on the hub. */
    val unauthorized = kotlinx.coroutines.flow.MutableSharedFlow<Unit>(extraBufferCapacity = 1)

    /** The hub, e.g. http://127.0.0.1:8077 — the API is always at /api. */
    var baseUrl: Url? = null
        private set

    val http: HttpClient = HttpClient(engineFactory()) {
        expectSuccess = false
        install(HttpCookies) { storage = cookies }
        install(WebSockets)
        install(HttpTimeout) {
            connectTimeoutMillis = 15_000
            requestTimeoutMillis = 60_000
        }
        defaultRequest {
            header("X-NKT-Lang", language.current().code)
        }
    }

    fun setBase(url: Url?) {
        baseUrl = url
        pins.currentAuthority = url?.let { "${it.host}:${it.port}" }
    }

    fun url(path: String): String? {
        val base = baseUrl ?: return null
        // Origin only: the hub serves the API at /api from the root.
        val port = if (base.port == base.protocol.defaultPort) "" else ":${base.port}"
        return "${base.protocol.name}://${base.host}$port/api$path"
    }

    fun webSocketUrl(path: String): String? {
        val base = baseUrl ?: return null
        val scheme = if (base.protocol == URLProtocol.HTTPS) "wss" else "ws"
        return "$scheme://${base.host}:${base.port}/api$path"
    }

    @Serializable
    private data class ErrorBody(val error: String = "")

    /** Raw text of a successful call, or the failure. */
    suspend fun call(method: HttpMethod, path: String, body: JsonElement? = null): Outcome<String> {
        val target = url(path) ?: return Outcome.Failure(AppError.NotConfigured)
        pins.lastMismatch = null
        return try {
            val response = http.request {
                this.method = method
                url.takeFrom(target)
                if (body != null) {
                    contentType(ContentType.Application.Json)
                    setBody(json.encodeToString(JsonElement.serializer(), body))
                } else if (method == HttpMethod.Post || method == HttpMethod.Put || method == HttpMethod.Patch) {
                    contentType(ContentType.Application.Json)
                    setBody("{}")
                }
            }
            val text = response.bodyAsText()
            if (response.status.isSuccess()) {
                Outcome.Success(text)
            } else {
                val message = runCatching { json.decodeFromString(ErrorBody.serializer(), text).error }
                    .getOrNull()?.takeIf { it.isNotBlank() } ?: "HTTP ${response.status.value}"
                if (response.status.value == 401 && !path.startsWith("/auth/")) unauthorized.tryEmit(Unit)
                Outcome.Failure(
                    if (response.status.value == 401) AppError.Unauthorized(message)
                    else AppError.Server(response.status.value, message),
                )
            }
        } catch (e: CancellationException) {
            throw e
        } catch (e: Throwable) {
            Outcome.Failure(networkError(e))
        }
    }

    /** A failed connection: a pinned-certificate mismatch is said as such. */
    fun networkError(e: Throwable): AppError {
        pins.lastMismatch?.let { return AppError.CertificateChanged(it.authority, it.pinned, it.presented) }
        Logger.w(e) { "request failed" }
        return AppError.Network(e.message ?: e::class.simpleName.orEmpty())
    }

    fun <T> decode(serializer: KSerializer<T>, text: String): Outcome<T> = try {
        Outcome.Success(json.decodeFromString(serializer, text))
    } catch (e: SerializationException) {
        Outcome.Failure(AppError.BadResponse(e.message.orEmpty()))
    } catch (e: IllegalArgumentException) {
        Outcome.Failure(AppError.BadResponse(e.message.orEmpty()))
    }

    suspend inline fun <reified T> get(path: String): Outcome<T> = typed(HttpMethod.Get, path, null)
    suspend inline fun <reified T> post(path: String, body: JsonElement? = null): Outcome<T> = typed(HttpMethod.Post, path, body)
    suspend inline fun <reified T> put(path: String, body: JsonElement): Outcome<T> = typed(HttpMethod.Put, path, body)
    suspend inline fun <reified T> patch(path: String, body: JsonElement): Outcome<T> = typed(HttpMethod.Patch, path, body)
    suspend inline fun <reified T> delete(path: String, body: JsonElement? = null): Outcome<T> = typed(HttpMethod.Delete, path, body)

    suspend inline fun <reified T> typed(method: HttpMethod, path: String, body: JsonElement?): Outcome<T> =
        when (val r = call(method, path, body)) {
            is Outcome.Failure -> r
            is Outcome.Success ->
                if (T::class == Unit::class) {
                    @Suppress("UNCHECKED_CAST")
                    Outcome.Success(Unit as T)
                } else {
                    decode(serializer<T>(), r.value)
                }
        }
}

/** "/hosts/{id}" or "/hosts/local" + [path] — how the hub addresses a host. */
fun hostPath(host: HostTarget, path: String): String =
    if (host.isLocal) "/hosts/local$path" else "/hosts/${host.id}$path"

/** For a query-string value: paths may hold spaces, '+', '&' or '#'. */
fun String.q(): String = encodeURLParameter()

/** An empty JSON object, for actions without a body. */
val EmptyBody: JsonElement = JsonObject(emptyMap())
