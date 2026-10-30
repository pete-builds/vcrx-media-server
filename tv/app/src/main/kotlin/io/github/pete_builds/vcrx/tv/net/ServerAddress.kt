package io.github.pete_builds.vcrx.tv.net

import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull

/** Turns what someone typed on the Connect screen into a base URL, and resolves server paths against it. */
object ServerAddress {
    const val DEFAULT_PORT = 3750

    /**
     * Normalizes input such as "192.168.86.20", "nix1:3750" or "https://media.example.com/vcrx"
     * into a base URL without a trailing slash. A bare host gets http and port 3750, the server's
     * default; an https URL without a port keeps 443, since that means a reverse proxy.
     * Returns null when the input cannot be an http(s) server address.
     */
    fun normalize(input: String): String? {
        val trimmed = input.trim()
        if (trimmed.isEmpty() || trimmed.any { it.isWhitespace() }) return null
        val withScheme = if ("://" in trimmed) trimmed else "http://$trimmed"
        val scheme = withScheme.substringBefore("://").lowercase()
        if (scheme != "http" && scheme != "https") return null
        val url = withScheme.toHttpUrlOrNull() ?: return null
        if (url.host.isEmpty()) return null
        val builder = url.newBuilder().query(null).fragment(null).username("").password("")
        if (!hasExplicitPort(withScheme) && scheme == "http") builder.port(DEFAULT_PORT)
        val path = url.encodedPath.trimEnd('/')
        builder.encodedPath(if (path.isEmpty()) "/" else path)
        return builder.build().toString().trimEnd('/')
    }

    /** Joins a base URL and a server path such as "/api/v1/versions/3/file?session=x". */
    fun resolve(baseUrl: String, path: String): HttpUrl? {
        if (path.startsWith("http://") || path.startsWith("https://")) return path.toHttpUrlOrNull()
        return (baseUrl.trimEnd('/') + "/" + path.trimStart('/')).toHttpUrlOrNull()
    }

    /** True when two URLs share scheme, host and port, so a token may be sent. */
    fun sameOrigin(a: HttpUrl, b: HttpUrl): Boolean =
        a.scheme == b.scheme && a.host.equals(b.host, ignoreCase = true) && a.port == b.port

    private fun hasExplicitPort(url: String): Boolean {
        val authority = url.substringAfter("://").substringBefore('/').substringBefore('?').substringBefore('#')
        val hostPort = authority.substringAfterLast('@')
        return if (hostPort.startsWith("[")) {
            hostPort.substringAfter(']').startsWith(":")
        } else {
            ':' in hostPort
        }
    }
}
