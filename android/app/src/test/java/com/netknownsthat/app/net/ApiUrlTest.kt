package com.netknownsthat.app.net

import okhttp3.HttpUrl.Companion.toHttpUrl
import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * Regression tests for a bug seen on a device: opening a config file answered
 * "Неизвестный метод API". The query string was being folded into the path
 * and its "?" percent-encoded, so the request went to a route that does not
 * exist.
 */
class ApiUrlTest {

    private val base = "http://hub.example.com:8077/".toHttpUrl()

    @Test
    fun `a plain path is left alone`() {
        assertEquals(
            "http://hub.example.com:8077/api/overview",
            buildApiUrl(base, "/api/overview").toString(),
        )
    }

    @Test
    fun `a query string stays a query string`() {
        val url = buildApiUrl(base, "/api/configs/file?path=/etc/nginx/nginx.conf")
        // The "?" must separate, not be encoded into the path.
        assertEquals("/api/configs/file", url.encodedPath)
        assertEquals("/etc/nginx/nginx.conf", url.queryParameter("path"))
    }

    @Test
    fun `slashes in a value do not become path segments`() {
        // A config path is mostly slashes; if they leaked into the path the
        // request would land on a completely different route.
        val url = buildApiUrl(base, "/api/configs/versions?path=/etc/caddy/Caddyfile")
        assertEquals("/api/configs/versions", url.encodedPath)
        assertEquals("/etc/caddy/Caddyfile", url.queryParameter("path"))
    }

    @Test
    fun `host-scoped paths keep working with a query`() {
        val url = buildApiUrl(base, "/api/hosts/7/configs/file?path=/etc/hosts")
        assertEquals("/api/hosts/7/configs/file", url.encodedPath)
        assertEquals("/etc/hosts", url.queryParameter("path"))
    }

    @Test
    fun `several parameters all survive`() {
        val url = buildApiUrl(base, "/api/configs/versions?path=/etc/hosts&limit=10")
        assertEquals("/etc/hosts", url.queryParameter("path"))
        assertEquals("10", url.queryParameter("limit"))
    }

    @Test
    fun `a value containing a space or an ampersand is encoded, not lost`() {
        val url = buildApiUrl(base, "/api/configs/file?path=/etc/odd name.conf")
        assertEquals("/etc/odd name.conf", url.queryParameter("path"))
    }

    @Test
    fun `a base url with a trailing path does not double up`() {
        val url = buildApiUrl("http://hub.example.com:8077/".toHttpUrl(), "/api/auth/me")
        assertEquals("http://hub.example.com:8077/api/auth/me", url.toString())
    }
}
