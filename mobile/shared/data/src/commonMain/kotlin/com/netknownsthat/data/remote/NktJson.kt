package com.netknownsthat.data.remote

import kotlinx.serialization.json.Json

/**
 * How the hub's JSON is read — one configuration for the client and for the
 * tests that parse recorded responses, so the tests read what the app reads.
 *
 * `coerceInputValues`: Go writes an empty slice or map as `null`
 * (`"kinds":null`); with it such a field takes its default (an empty list)
 * instead of failing the whole response.
 */
val NktJson = Json {
    ignoreUnknownKeys = true
    explicitNulls = false
    encodeDefaults = true
    coerceInputValues = true
}
