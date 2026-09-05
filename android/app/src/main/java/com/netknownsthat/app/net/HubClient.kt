package com.netknownsthat.app.net

import com.netknownsthat.app.data.SettingsStore
import com.netknownsthat.app.net.model.LoginRequest
import com.netknownsthat.app.net.model.Me
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import java.io.IOException
import java.util.concurrent.TimeUnit

/**
 * The one networking chokepoint every screen goes through — REST today,
 * WebSocket joins it from phase 3 onward (see the plan's shared
 * live-log-job component). Deliberately just one configured hub at a time
 * for now (see SettingsStore's own doc comment); a real multi-hub picker is
 * the plan's phase 7, layered on top without changing this class's shape.
 *
 * The hub's own base URL is assumed to have no meaningful path component —
 * matching how the Go server itself always serves the API at `/api/...`
 * from the origin root (see internal/hub/server.go, internal/api/server.go)
 * — a value like "http://127.0.0.1:8077" or "https://hub.example.com" is
 * expected, not one with its own sub-path.
 */
class HubClient(
    private val settingsStore: SettingsStore,
    scope: CoroutineScope,
) {
    val hostScope = HostScope()

    private val cookieJar = PersistentCookieJar(scope, settingsStore)
    private val certPins = CertPinStore(scope, settingsStore)
    private var baseUrl: HttpUrl? = null

    // internal, not private: the reified `execute` below is inline, and an
    // inline function may only touch declarations at least as visible as
    // itself. Only the deserialization step is inlined; everything else
    // (OkHttp, cookies, base URL) stays private behind executeRaw.
    @PublishedApi
    internal val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
    }

    private val okHttp = OkHttpClient.Builder()
        .cookieJar(cookieJar)
        .connectTimeout(15, TimeUnit.SECONDS)
        .readTimeout(30, TimeUnit.SECONDS)
        .apply {
            // A hub with NKT_TLS_ENABLED generates its own self-signed
            // certificate, so plain chain validation cannot work — see
            // net/CertPinning.kt for what replaces it.
            val (factory, trustManager) = tofuSslSocketFactory(certPins)
            sslSocketFactory(factory, trustManager)
            hostnameVerifier(tofuHostnameVerifier(certPins))
        }
        .build()

    /** OkHttp instance other layers (WebSocket client, phase 3+) share, so
     * every connection carries the same cookie jar/timeouts. */
    fun okHttpClient(): OkHttpClient = okHttp

    fun currentBaseUrl(): HttpUrl? = baseUrl

    /**
     * Completed once [bootstrap] has run, whatever it found. Requests wait on
     * it so that a ViewModel firing early cannot beat the restore and report
     * a configured hub as missing — which is exactly what happened when the
     * host list fetched from its own `init`, during composition, before the
     * bootstrap coroutine had a chance to run.
     */
    private val bootstrapped = CompletableDeferred<Unit>()

    /** Loads whatever hub URL + session cookie were persisted from a
     * previous run. Returns true if a hub URL was configured at all (not
     * whether the session is still valid — call [me] to find that out). */
    suspend fun bootstrap(): Boolean {
        try {
            certPins.load()
            val saved = settingsStore.hubBaseUrl()?.toHttpUrlOrNull() ?: return false
            baseUrl = saved
            certPins.currentAuthority = saved.authority()
            cookieJar.restore(saved)
            return true
        } finally {
            // In a finally so a failure to restore still releases callers:
            // hanging every request forever is a far worse outcome than
            // reporting that no hub is configured.
            bootstrapped.complete(Unit)
        }
    }

    private fun HttpUrl.authority(): String = "$host:$port"

    /**
     * WebSocket URL for a host-scoped path, e.g. "/terminal/ws?tmux=1".
     *
     * Same `/api` prefix and same host-scoping rule as every REST call (see
     * HostScope), and the same cookie too — OkHttp's CookieJar applies to
     * the upgrade request like any other, which is what authenticates the
     * socket. Only the scheme differs.
     */
    fun webSocketUrl(path: String): String? {
        val base = baseUrl ?: return null
        val separator = path.indexOf('?')
        val bare = if (separator >= 0) path.substring(0, separator) else path
        val query = if (separator >= 0) path.substring(separator) else ""
        val scoped = hostScope.scoped(bare)
        val scheme = if (base.isHttps) "wss" else "ws"
        return "$scheme://${base.host}:${base.port}/api$scoped$query"
    }

    /** SHA-256 of the certificate pinned for the configured hub, if it is
     * using a self-signed one — shown on the About screen so an operator can
     * compare it with what the hub itself reports. */
    fun pinnedCertFingerprint(): String? =
        baseUrl?.let { certPins.pinnedFor(it.authority()) }

    /** Forgets the pinned certificate, so the next connection trusts on first
     * use again — what is needed after deliberately reinstalling a hub. */
    fun forgetPinnedCert() {
        baseUrl?.let { certPins.forget(it.authority()) }
    }

    /** Validates and persists a new hub base URL — called from the login
     * screen before the first request there. */
    suspend fun setHubBaseUrl(rawUrl: String): Result<Unit> {
        val normalized = rawUrl.trim().let { if ("://" in it) it else "http://$it" }
        val parsed = normalized.toHttpUrlOrNull()
            ?: return Result.failure(IllegalArgumentException("Не похоже на адрес хаба: $rawUrl"))
        baseUrl = parsed
        certPins.currentAuthority = parsed.authority()
        settingsStore.setHubBaseUrl(parsed.toString())
        return Result.success(Unit)
    }

    sealed class ApiResult<out T> {
        data class Success<T>(val value: T) : ApiResult<T>()
        data class Failure(val message: String, val httpCode: Int? = null) : ApiResult<Nothing>()
    }

    suspend inline fun <reified T> get(path: String): ApiResult<T> = execute("GET", path, null)
    suspend inline fun <reified T> post(path: String, jsonBody: String? = null): ApiResult<T> =
        execute("POST", path, jsonBody)
    suspend inline fun <reified T> put(path: String, jsonBody: String): ApiResult<T> =
        execute("PUT", path, jsonBody)
    suspend inline fun <reified T> patch(path: String, jsonBody: String): ApiResult<T> =
        execute("PATCH", path, jsonBody)
    suspend inline fun <reified T> delete(path: String): ApiResult<T> = execute("DELETE", path, null)

    @Serializable
    private data class ErrorBody(val error: String)

    /** Raw outcome of a call — the response body is still undecoded here so
     * that only the tiny reified [execute] wrapper needs to be inline. */
    @PublishedApi
    internal sealed class RawResult {
        data class Ok(val text: String) : RawResult()
        data class Err(val message: String, val httpCode: Int?) : RawResult()
    }

    /** Every REST call funnels through here — GET/POST/PUT/PATCH/DELETE
     * differ only in method and whether a body is attached. */
    @PublishedApi
    internal suspend fun executeRaw(method: String, path: String, jsonBody: String?): RawResult =
        withContext(Dispatchers.IO) {
            // Bounded rather than an open-ended await: if bootstrap were
            // somehow never called, failing with the usual message beats a
            // request that never returns.
            withTimeoutOrNull(5_000) { bootstrapped.await() }

            val base = baseUrl
                ?: return@withContext RawResult.Err("Хаб не настроен — укажите адрес на экране входа", null)
            try {
                // Scope the path only; the query string must stay a query
                // string (see buildApiUrl).
                val separator = path.indexOf('?')
                val bare = if (separator >= 0) path.substring(0, separator) else path
                val query = if (separator >= 0) path.substring(separator) else ""
                val url = buildApiUrl(base, "/api${hostScope.scoped(bare)}$query")
                val builder = Request.Builder().url(url)
                val body = jsonBody?.toRequestBody("application/json; charset=utf-8".toMediaType())
                when (method) {
                    "GET" -> builder.get()
                    "DELETE" -> if (body != null) builder.delete(body) else builder.delete()
                    else -> builder.method(method, body ?: ByteArray(0).toRequestBody(null))
                }

                okHttp.newCall(builder.build()).execute().use { response ->
                    val text = response.body?.string().orEmpty()
                    if (!response.isSuccessful) {
                        val message = runCatching { json.decodeFromString<ErrorBody>(text).error }
                            .getOrNull()
                            ?.takeIf { it.isNotBlank() }
                            ?: "Ошибка ${response.code}"
                        return@withContext RawResult.Err(message, response.code)
                    }
                    RawResult.Ok(text)
                }
            } catch (e: IOException) {
                // A pin mismatch arrives wrapped in an SSL failure, and the
                // wrapper's own message says nothing useful — dig out ours.
                val mismatch = generateSequence(e as Throwable) { it.cause }
                    .filterIsInstance<CertPinMismatchException>()
                    .firstOrNull()
                RawResult.Err(mismatch?.message ?: e.message ?: "Сетевая ошибка", null)
            }
        }

    suspend inline fun <reified T> execute(method: String, path: String, jsonBody: String?): ApiResult<T> =
        when (val raw = executeRaw(method, path, jsonBody)) {
            is RawResult.Err -> ApiResult.Failure(raw.message, raw.httpCode)
            is RawResult.Ok ->
                if (T::class == Unit::class) {
                    @Suppress("UNCHECKED_CAST")
                    ApiResult.Success(Unit as T)
                } else {
                    try {
                        ApiResult.Success(json.decodeFromString<T>(raw.text))
                    } catch (e: kotlinx.serialization.SerializationException) {
                        ApiResult.Failure("Не удалось разобрать ответ сервера: ${e.message}")
                    }
                }
        }

    /**
     * Login, then fetch /auth/me separately — mirrors web/src/App.tsx's own
     * loadMe(), rather than assuming a particular shape for the login
     * response body itself (unconfirmed on the Go side; only writeError's
     * {"error": "..."} failure shape is confirmed from this session's own
     * work on internal/api/server.go).
     */
    suspend fun login(username: String, password: String): ApiResult<Me> {
        val body = json.encodeToString(LoginRequest(username, password))
        return when (val loginResp = post<Unit>("/auth/login", body)) {
            is ApiResult.Failure -> loginResp
            is ApiResult.Success -> me()
        }
    }

    suspend fun me(): ApiResult<Me> = get("/auth/me")

    suspend fun logout() {
        post<Unit>("/auth/logout")
        cookieJar.clear()
    }
}
