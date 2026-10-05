package com.netknownsthat.data.remote

import io.ktor.client.engine.HttpClientEngine
import io.ktor.client.engine.okhttp.OkHttp
import okhttp3.OkHttpClient
import java.security.KeyStore
import java.security.MessageDigest
import java.security.SecureRandom
import java.security.cert.CertificateException
import java.security.cert.X509Certificate
import java.util.concurrent.TimeUnit
import javax.net.ssl.HostnameVerifier
import javax.net.ssl.HttpsURLConnection
import javax.net.ssl.SSLContext
import javax.net.ssl.TrustManager
import javax.net.ssl.TrustManagerFactory
import javax.net.ssl.X509TrustManager

private fun sha256Hex(cert: X509Certificate): String =
    MessageDigest.getInstance("SHA-256").digest(cert.encoded).joinToString("") { "%02x".format(it) }

/**
 * Chain validation first (a hub behind a reverse proxy with a real
 * certificate stays on it); a self-signed one is checked against the pin.
 */
private class TofuTrustManager(
    private val delegate: X509TrustManager,
    private val pins: CertPins,
) : X509TrustManager {
    override fun checkServerTrusted(chain: Array<out X509Certificate>?, authType: String?) {
        val leaf = chain?.firstOrNull() ?: throw CertificateException("no server certificate")
        try {
            delegate.checkServerTrusted(chain, authType)
            return
        } catch (_: CertificateException) {
            // Not chain-valid — fall through to the pin.
        }
        val authority = pins.currentAuthority ?: throw CertificateException("unknown hub")
        if (!pins.verify(authority, sha256Hex(leaf))) throw CertificateException("pinned certificate mismatch")
    }

    override fun checkClientTrusted(chain: Array<out X509Certificate>?, authType: String?) =
        delegate.checkClientTrusted(chain, authType)

    override fun getAcceptedIssuers(): Array<X509Certificate> = delegate.acceptedIssuers
}

actual fun createHttpEngine(pins: CertPins): HttpClientEngine {
    val factory = TrustManagerFactory.getInstance(TrustManagerFactory.getDefaultAlgorithm())
    factory.init(null as KeyStore?)
    val platform = factory.trustManagers.filterIsInstance<X509TrustManager>().first()
    val trustManager = TofuTrustManager(platform, pins)
    val ssl = SSLContext.getInstance("TLS").apply { init(null, arrayOf<TrustManager>(trustManager), SecureRandom()) }
    val defaultVerifier = HttpsURLConnection.getDefaultHostnameVerifier()
    // A self-signed hub certificate rarely names the address it is reached
    // by; the pin is what identifies it, so a pinned match is accepted.
    val verifier = HostnameVerifier { hostname, session ->
        if (defaultVerifier.verify(hostname, session)) return@HostnameVerifier true
        val leaf = session?.peerCertificates?.firstOrNull() as? X509Certificate ?: return@HostnameVerifier false
        val authority = pins.currentAuthority ?: return@HostnameVerifier false
        pins.pinnedFor(authority) == sha256Hex(leaf)
    }
    return OkHttp.create {
        preconfigured = OkHttpClient.Builder()
            .sslSocketFactory(ssl.socketFactory, trustManager)
            .hostnameVerifier(verifier)
            .connectTimeout(15, TimeUnit.SECONDS)
            .readTimeout(30, TimeUnit.SECONDS)
            .pingInterval(25, TimeUnit.SECONDS)
            .build()
    }
}
