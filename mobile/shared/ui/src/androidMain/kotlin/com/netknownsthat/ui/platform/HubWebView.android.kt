package com.netknownsthat.ui.platform

import android.annotation.SuppressLint
import android.net.Uri
import android.net.http.SslError
import android.os.Build
import android.view.ViewGroup
import android.webkit.CookieManager
import android.webkit.SslErrorHandler
import android.webkit.WebResourceRequest
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.viewinterop.AndroidView
import com.netknownsthat.domain.repository.WebSession
import java.security.MessageDigest

@SuppressLint("SetJavaScriptEnabled")
@Composable
actual fun HubWebView(session: WebSession, path: String, modifier: Modifier) {
    val url = session.origin + path
    AndroidView(
        modifier = modifier,
        factory = { context ->
            // The app's session cookie, so the page opens signed in.
            val cookies = CookieManager.getInstance()
            cookies.setAcceptCookie(true)
            session.cookies.forEach { cookies.setCookie(session.origin, it) }
            cookies.flush()
            WebView(context).apply {
                layoutParams = ViewGroup.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT)
                settings.javaScriptEnabled = true
                settings.domStorageEnabled = true
                // The screen fits the view; pinch zoom for a closer look.
                settings.useWideViewPort = true
                settings.loadWithOverviewMode = true
                settings.builtInZoomControls = true
                settings.displayZoomControls = false
                webViewClient = HubOnlyClient(session)
                loadUrl(url)
            }
        },
        onRelease = { it.destroy() },
    )
}

private class HubOnlyClient(private val session: WebSession) : WebViewClient() {
    private val origin = Uri.parse(session.origin)

    /** Links away from the hub are not followed: this view carries its session. */
    override fun shouldOverrideUrlLoading(view: WebView, request: WebResourceRequest): Boolean {
        val u = request.url
        return !(u.scheme == origin.scheme && u.host == origin.host && u.port == origin.port)
    }

    /**
     * A self-signed hub: proceed only when the certificate is the one pinned
     * at sign-in, as the app's own connections do. Android before 10 does not
     * hand out the certificate itself — there the page is refused.
     */
    override fun onReceivedSslError(view: WebView, handler: SslErrorHandler, error: SslError) {
        val pinned = session.pinnedSha256
        val cert = if (Build.VERSION.SDK_INT >= 29) error.certificate.x509Certificate else null
        val sameHost = Uri.parse(error.url).host == origin.host
        if (pinned != null && cert != null && sameHost) {
            val sha = MessageDigest.getInstance("SHA-256").digest(cert.encoded).joinToString("") { "%02x".format(it) }
            if (sha.equals(pinned, ignoreCase = true)) {
                handler.proceed()
                return
            }
        }
        handler.cancel()
    }
}
