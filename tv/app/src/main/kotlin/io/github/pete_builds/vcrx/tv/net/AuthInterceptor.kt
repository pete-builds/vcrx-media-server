package io.github.pete_builds.vcrx.tv.net

import io.github.pete_builds.vcrx.tv.data.SettingsStore
import io.github.pete_builds.vcrx.tv.data.TokenStore
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.Interceptor
import okhttp3.Response

/**
 * Adds the bearer token to every request bound for the configured server: API
 * calls, Coil images, ExoPlayer streams and subtitles all share one OkHttpClient.
 * Requests to any other origin go out without it, so the token never leaks.
 */
class AuthInterceptor(
    private val settings: SettingsStore,
    private val tokens: TokenStore,
    private val userAgent: String,
) : Interceptor {
    override fun intercept(chain: Interceptor.Chain): Response {
        val request = chain.request()
        val builder = request.newBuilder().header("User-Agent", userAgent)
        val token = tokens.token()
        val base = settings.serverUrl?.toHttpUrlOrNull()
        if (token != null && base != null && request.header("Authorization") == null &&
            ServerAddress.sameOrigin(request.url, base)
        ) {
            builder.header("Authorization", "Bearer $token")
        }
        return chain.proceed(builder.build())
    }
}
