package com.netknownsthat.app.net

import com.netknownsthat.app.data.SettingsStore
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch
import java.security.KeyStore
import java.security.MessageDigest
import java.security.SecureRandom
import java.security.cert.CertificateException
import java.security.cert.X509Certificate
import java.util.concurrent.ConcurrentHashMap
import javax.net.ssl.HostnameVerifier
import javax.net.ssl.HttpsURLConnection
import javax.net.ssl.SSLContext
import javax.net.ssl.SSLSession
import javax.net.ssl.SSLSocketFactory
import javax.net.ssl.TrustManager
import javax.net.ssl.TrustManagerFactory
import javax.net.ssl.X509TrustManager

/**
 * Thrown when an already-pinned hub presents a different certificate. Kept
 * distinct from every other TLS failure so the UI can say what actually
 * happened instead of "handshake failed" — the same reason
 * internal/hub/tunnelpin.go has its own tunnelCertMismatchError.
 */
class CertPinMismatchException(
    val authority: String,
    val pinned: String,
    val presented: String,
) : CertificateException(
    "Сертификат хаба $authority изменился (ожидался $pinned, получен $presented) — " +
        "либо хаб переустановлен и сгенерировал новый сертификат, либо соединение перехватывается"
)

private fun sha256Hex(cert: X509Certificate): String =
    MessageDigest.getInstance("SHA-256").digest(cert.encoded)
        .joinToString("") { "%02x".format(it) }

/**
 * Remembers which certificate each hub showed the first time it was reached.
 *
 * The hub itself already works this way when it dials a managed host's backup
 * channel (internal/hub/tunnelpin.go, HUB.md §"TLS-сертификат хоста
 * самоподписанный"): a self-signed certificate has no chain to validate, so
 * what gets checked is the SHA-256 of the leaf certificate, trusted on first
 * use and enforced strictly afterwards. Same fingerprint definition here —
 * SHA-256 over the leaf's DER bytes — so an operator can compare what the
 * phone shows with what the hub reports.
 */
class CertPinStore(
    private val scope: CoroutineScope,
    private val settingsStore: SettingsStore,
) {
    private val pins = ConcurrentHashMap<String, String>()

    /** Which hub the app is currently pointed at, as "host:port". The trust
     * manager has no hostname of its own to work with, and this app talks to
     * exactly one hub at a time (see SettingsStore). */
    @Volatile
    var currentAuthority: String? = null

    suspend fun load() {
        settingsStore.pinnedCerts().forEach { entry ->
            val authority = entry.substringBefore('=', "")
            val fingerprint = entry.substringAfter('=', "")
            if (authority.isNotEmpty() && fingerprint.isNotEmpty()) pins[authority] = fingerprint
        }
    }

    fun pinnedFor(authority: String): String? = pins[authority]

    fun record(authority: String, fingerprint: String) {
        pins[authority] = fingerprint
        val snapshot = pins.entries.map { "${it.key}=${it.value}" }.toSet()
        scope.launch { settingsStore.savePinnedCerts(snapshot) }
    }

    /** Drops the pin for a hub, so the next connection trusts on first use
     * again — what an operator needs after deliberately reinstalling a hub. */
    fun forget(authority: String) {
        pins.remove(authority)
        val snapshot = pins.entries.map { "${it.key}=${it.value}" }.toSet()
        scope.launch { settingsStore.savePinnedCerts(snapshot) }
    }
}

/**
 * Standard validation first, pinning only as the fallback: a hub behind a
 * real certificate (a reverse proxy with Let's Encrypt, say) keeps the full
 * chain check and never gets pinned, while a self-signed one — which
 * NKT_TLS_ENABLED generates on its own, so it is an ordinary setup rather
 * than an edge case — falls through to trust-on-first-use.
 */
private class TofuTrustManager(
    private val delegate: X509TrustManager,
    private val pins: CertPinStore,
) : X509TrustManager {

    override fun checkServerTrusted(chain: Array<out X509Certificate>?, authType: String?) {
        val leaf = chain?.firstOrNull() ?: throw CertificateException("Хаб не предъявил сертификат")
        try {
            delegate.checkServerTrusted(chain, authType)
            return
        } catch (_: CertificateException) {
            // Not chain-validatable — the self-signed case below.
        }

        val authority = pins.currentAuthority
            ?: throw CertificateException("Неизвестно, к какому хабу идёт соединение — сертификат не проверить")
        val presented = sha256Hex(leaf)
        val pinned = pins.pinnedFor(authority)
        when (pinned) {
            null -> pins.record(authority, presented)
            presented -> Unit
            else -> throw CertPinMismatchException(authority, pinned, presented)
        }
    }

    override fun checkClientTrusted(chain: Array<out X509Certificate>?, authType: String?) =
        delegate.checkClientTrusted(chain, authType)

    override fun getAcceptedIssuers(): Array<X509Certificate> = delegate.acceptedIssuers
}

/** The platform's own trust manager, used for ordinary chain validation. */
private fun platformTrustManager(): X509TrustManager {
    val factory = TrustManagerFactory.getInstance(TrustManagerFactory.getDefaultAlgorithm())
    factory.init(null as KeyStore?)
    return factory.trustManagers.filterIsInstance<X509TrustManager>().first()
}

/** SSL machinery for OkHttp: [SSLSocketFactory] plus the [X509TrustManager]
 * OkHttp wants alongside it. */
fun tofuSslSocketFactory(pins: CertPinStore): Pair<SSLSocketFactory, X509TrustManager> {
    val trustManager = TofuTrustManager(platformTrustManager(), pins)
    val context = SSLContext.getInstance("TLS").apply {
        init(null, arrayOf<TrustManager>(trustManager), SecureRandom())
    }
    return context.socketFactory to trustManager
}

/**
 * A self-signed certificate generated for a LAN address usually carries no
 * name matching whatever the operator typed, so the default verifier rejects
 * it even though the certificate is the pinned one. Accept that case — and
 * only that case: the pin is what establishes identity here, exactly as it
 * does for the hub's own tunnel connections.
 */
fun tofuHostnameVerifier(pins: CertPinStore): HostnameVerifier {
    val default = HttpsURLConnection.getDefaultHostnameVerifier()
    return HostnameVerifier { hostname: String?, session: SSLSession? ->
        if (default.verify(hostname, session)) return@HostnameVerifier true
        val leaf = session?.peerCertificates?.firstOrNull() as? X509Certificate
            ?: return@HostnameVerifier false
        val authority = pins.currentAuthority ?: return@HostnameVerifier false
        pins.pinnedFor(authority) == sha256Hex(leaf)
    }
}
