package io.github.pete_builds.vcrx.tv.net

import java.net.InetAddress
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull

/** Turns what someone typed on the Connect screen into a base URL, and resolves server paths against it. */
object ServerAddress {
    const val DEFAULT_PORT = 3750

    /**
     * Normalizes input such as "192.168.1.20", "nix1:3750" or "https://media.example.com/vcrx"
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

    /**
     * True when [baseUrl] is plain http:// to a host that is not on a private network,
     * so the password, the session and every stream would cross the internet
     * unencrypted. The Connect screen asks before using such an address. https://
     * and private hosts are never flagged.
     */
    fun cleartextToPublicHost(baseUrl: String): Boolean {
        val url = baseUrl.toHttpUrlOrNull() ?: return false
        return !url.isHttps && !isPrivateHost(url.host)
    }

    /**
     * A host that only resolves or routes inside a private network: IPv4 RFC 1918,
     * loopback, link-local and Tailscale's 100.64.0.0/10; IPv6 loopback, link-local
     * and unique local (fc00::/7); and names that public DNS never answers for:
     * .local (mDNS), localhost, .home.arpa (RFC 8375) and a bare single-label name
     * such as nix1, which only a local resolver knows. Never does a DNS lookup:
     * a name is judged by its form, an address by its bytes.
     */
    fun isPrivateHost(host: String): Boolean {
        val name = host.lowercase().removePrefix("[").removeSuffix("]").trimEnd('.')
        if (name.isEmpty()) return false
        if (':' in name) return ipv6Bytes(name)?.let(::isPrivateAddress) ?: false
        ipv4Bytes(name)?.let { return isPrivateAddress(it) }
        return '.' !in name ||
            name == "localhost" || name.endsWith(".localhost") ||
            name.endsWith(".local") || name.endsWith(".home.arpa")
    }

    /** 4 bytes for IPv4, 16 for IPv6. */
    private fun isPrivateAddress(b: ByteArray): Boolean {
        val u = b.map { it.toInt() and 0xff }
        return if (u.size == 4) {
            u[0] == 10 ||
                (u[0] == 172 && u[1] in 16..31) ||
                (u[0] == 192 && u[1] == 168) ||
                u[0] == 127 ||
                (u[0] == 169 && u[1] == 254) ||
                (u[0] == 100 && u[1] in 64..127)
        } else if (u.take(10).all { it == 0 } && u[10] == 0xff && u[11] == 0xff) {
            isPrivateAddress(b.copyOfRange(12, 16)) // ::ffff:a.b.c.d, an IPv4 address
        } else {
            (u.take(15).all { it == 0 } && u[15] == 1) || // ::1
                (u[0] == 0xfe && (u[1] and 0xc0) == 0x80) || // fe80::/10
                (u[0] and 0xfe) == 0xfc // fc00::/7
        }
    }

    /** Dotted-decimal IPv4, as OkHttp writes it; null for anything else. */
    private fun ipv4Bytes(name: String): ByteArray? {
        val parts = name.split('.')
        if (parts.size != 4) return null
        val octets = parts.map { part -> part.takeIf { it.length in 1..3 && it.all(Char::isDigit) }?.toInt()?.takeIf { it <= 255 } ?: return null }
        return ByteArray(4) { octets[it].toByte() }
    }

    /**
     * An IPv6 literal's bytes (some platforms answer an IPv4-mapped one with its 4
     * IPv4 bytes). Only hex digits, colons and dots get this far, so InetAddress
     * always reads a literal and never looks a name up.
     */
    private fun ipv6Bytes(name: String): ByteArray? {
        if (!name.all { it.isDigit() || it in 'a'..'f' || it == ':' || it == '.' }) return null
        return runCatching { InetAddress.getByName(name).address }.getOrNull()
    }

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
