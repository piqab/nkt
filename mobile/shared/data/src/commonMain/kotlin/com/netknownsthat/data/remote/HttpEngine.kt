package com.netknownsthat.data.remote

import io.ktor.client.engine.HttpClientEngine

/**
 * The platform HTTP engine with hub certificate pinning: OkHttp with a
 * trust-on-first-use trust manager on Android, NSURLSession with a
 * server-trust challenge handler on iOS. Both consult [pins].
 */
expect fun createHttpEngine(pins: CertPins): HttpClientEngine
