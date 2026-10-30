package io.github.pete_builds.vcrx.tv.net

import kotlinx.serialization.ExperimentalSerializationApi
import kotlinx.serialization.json.Json

/**
 * The one JSON configuration: unknown fields are ignored and nulls for defaulted
 * fields fall back to the default, so a newer server never breaks this client;
 * null fields are left out of requests.
 */
@OptIn(ExperimentalSerializationApi::class)
val ApiJson: Json = Json {
    ignoreUnknownKeys = true
    explicitNulls = false
    encodeDefaults = true
    coerceInputValues = true
}
