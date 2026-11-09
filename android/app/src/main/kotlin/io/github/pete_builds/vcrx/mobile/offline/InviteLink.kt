package io.github.pete_builds.vcrx.mobile.offline

import io.github.pete_builds.vcrx.mobile.net.ServerAddress

/** An invite pasted on the Connect screen: the server it came from, when it says, and its secret. */
data class InviteLink(val serverUrl: String?, val secret: String) {
    companion object {
        private const val MARKER = "#/invite/"

        /**
         * Reads "https://media.example.com/#/invite/SECRET" (what Settings > Devices
         * copies), or a bare secret. Pure, so it is unit tested.
         */
        fun parse(text: String): InviteLink? {
            val t = text.trim()
            if (t.isEmpty()) return null
            val at = t.indexOf(MARKER)
            if (at < 0) {
                return if (t.all { it.isLetterOrDigit() || it == '-' || it == '_' } && t.length in 8..128) InviteLink(null, t) else null
            }
            val secret = t.substring(at + MARKER.length).substringBefore('?').substringBefore('/').trim()
            if (secret.isEmpty()) return null
            val origin = t.substring(0, at).trimEnd('/')
            return InviteLink(origin.takeIf { it.isNotEmpty() }?.let(ServerAddress::normalize), secret)
        }
    }
}
