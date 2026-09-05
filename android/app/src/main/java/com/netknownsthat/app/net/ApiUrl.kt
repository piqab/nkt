package com.netknownsthat.app.net

import okhttp3.HttpUrl

/**
 * Builds the absolute URL for an API call from a path that may carry its own
 * query string, e.g. "/configs/file?path=/etc/nginx/nginx.conf".
 *
 * Separate from HubClient so it can be tested: OkHttp is a plain JVM library
 * with no Android dependency, and this is where a real bug lived. Passing the
 * whole string to `encodedPath` percent-encoded the "?" as part of the path,
 * so the server saw a route that does not exist and answered "Неизвестный
 * метод API" — which looked like a missing endpoint rather than a malformed
 * URL.
 *
 * Query values go through `addQueryParameter`, which encodes them properly:
 * the values here are filesystem paths full of slashes, and they must arrive
 * as a single parameter rather than extra path segments.
 */
fun buildApiUrl(base: HttpUrl, pathWithQuery: String): HttpUrl {
    val separator = pathWithQuery.indexOf('?')
    val path = if (separator >= 0) pathWithQuery.substring(0, separator) else pathWithQuery
    val query = if (separator >= 0) pathWithQuery.substring(separator + 1) else ""

    val builder = base.newBuilder().encodedPath(path)
    if (query.isNotEmpty()) {
        query.split('&').filter { it.isNotEmpty() }.forEach { pair ->
            val equals = pair.indexOf('=')
            if (equals >= 0) {
                builder.addQueryParameter(pair.substring(0, equals), pair.substring(equals + 1))
            } else {
                builder.addQueryParameter(pair, null)
            }
        }
    }
    return builder.build()
}
